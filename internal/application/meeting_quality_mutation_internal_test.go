package application

import (
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestMeetingQualityEvaluatorMutationMatrix(t *testing.T) {
	baseState, scenario, segments := meetingQualityMutationFixture()
	base := evaluateMeetingQualityMutationSnapshot(scenario, baseState, segments)
	if !base.Passed {
		t.Fatalf("mutation control fixture is not valid: %+v", base)
	}
	baseline := NewMeetingQualityBaseline(MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion,
		Suite:         "deterministic",
		Passed:        true,
		Scenarios:     []MeetingQualityScenarioResult{base},
	})

	tests := []struct {
		name   string
		mutate func(*liveAnalysisPayload, *MeetingQualityScenario, *[]domain.TranscriptSegment)
		assert func(*testing.T, MeetingQualityScenarioResult, MeetingQualityComparisonReport)
	}{
		{
			name: "required risk removed",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				removeMeetingQualityMutationItem(state, "risk")
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if result.Metrics.RiskRecall != 0 || !containsExactString(result.MissingRequiredPropositions, "required-risk") {
					t.Fatalf("risk mutation result=%+v", result)
				}
			},
		},
		{
			name: "required todo removed",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				removeMeetingQualityMutationItem(state, "todo")
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if result.Metrics.TodoRecall != 0 || !containsExactString(result.MissingRequiredPropositions, "required-todo") {
					t.Fatalf("todo mutation result=%+v", result)
				}
			},
		},
		{
			name: "fact changed to wrong kind",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				for index := range state.Items {
					if state.Items[index].ID == "fact" {
						state.Items[index].Kind = "risk"
					}
				}
				for index := range state.Tree.Nodes {
					if state.Tree.Nodes[index].ID == "fact" {
						state.Tree.Nodes[index].Kind = "risk"
					}
				}
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, comparison MeetingQualityComparisonReport) {
				if result.Metrics.ClassificationAccuracy >= base.Metrics.ClassificationAccuracy ||
					len(result.KindMismatches) != 1 || len(comparison.WorsenedMetrics) == 0 {
					t.Fatalf("kind mutation result=%+v comparison=%+v", result, comparison)
				}
			},
		},
		{
			name: "unsupported proposition added",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				item := liveAnalysisItem{
					ID: "unsupported", Kind: "fact",
					Title:               "大阪支社のサーバー100台を停止した",
					Body:                "大阪支社のサーバー100台を停止した",
					EvidenceSequenceNos: []int64{1}, CreatedThroughSequenceNo: 5,
				}
				state.Items = append(state.Items, item)
				state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
					ID: item.ID, Kind: item.Kind, ParentID: "group", Label: item.Title,
				})
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if result.Metrics.UnsupportedPropositionCount == 0 || len(result.UnsupportedItems) == 0 {
					t.Fatalf("unsupported mutation result=%+v", result)
				}
			},
		},
		{
			name: "future evidence added",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				state.Items[0].EvidenceSequenceNos = append(state.Items[0].EvidenceSequenceNos, 99)
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if !containsStringPrefix(result.HardInvariantViolations, "future_evidence:") {
					t.Fatalf("future evidence was not detected: %+v", result)
				}
			},
		},
		{
			name: "orphan node created",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				state.Tree.Nodes[1].ParentID = "missing-parent"
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if !containsStringPrefix(result.HardInvariantViolations, "orphan_node:") {
					t.Fatalf("orphan was not detected: %+v", result)
				}
			},
		},
		{
			name: "duplicate node id created",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				state.Tree.Nodes = append(state.Tree.Nodes, state.Tree.Nodes[len(state.Tree.Nodes)-1])
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if !containsStringPrefix(result.HardInvariantViolations, "duplicate_node_id:") {
					t.Fatalf("duplicate node ID was not detected: %+v", result)
				}
			},
		},
		{
			name: "semantic duplicate added",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				duplicate := state.Items[0]
				duplicate.ID = "risk-duplicate"
				state.Items = append(state.Items, duplicate)
				state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
					ID: duplicate.ID, Kind: duplicate.Kind, ParentID: "group", Label: duplicate.Title,
				})
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if result.Metrics.SemanticDuplicateCount <= base.Metrics.SemanticDuplicateCount {
					t.Fatalf("semantic duplicate metric did not worsen: %+v", result)
				}
			},
		},
		{
			name: "label and description made identical",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				for index := range state.Items {
					if state.Items[index].ID == "risk" {
						state.Items[index].Body = state.Items[index].Title
					}
				}
				for index := range state.Tree.Nodes {
					if state.Tree.Nodes[index].ID == "risk" {
						state.Tree.Nodes[index].Description = state.Tree.Nodes[index].Label
					}
				}
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, comparison MeetingQualityComparisonReport) {
				if result.Metrics.LabelDescriptionExactDuplicateCount <= base.Metrics.LabelDescriptionExactDuplicateCount ||
					len(comparison.WorsenedMetrics) == 0 {
					t.Fatalf("label/description duplicate mutation result=%+v comparison=%+v", result, comparison)
				}
			},
		},
		{
			name: "unsupported description deadline added",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				for index := range state.Items {
					if state.Items[index].ID == "fact" {
						state.Items[index].Body += "。対応期限は8月31日です"
					}
				}
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, comparison MeetingQualityComparisonReport) {
				if result.Metrics.DescriptionUnsupportedAtomCount <= base.Metrics.DescriptionUnsupportedAtomCount ||
					len(comparison.WorsenedMetrics) == 0 {
					t.Fatalf("unsupported description mutation result=%+v comparison=%+v", result, comparison)
				}
			},
		},
		{
			name: "decision duplicated as issue",
			mutate: func(state *liveAnalysisPayload, scenario *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				decision := liveAnalysisItem{
					ID: "decision", Kind: "decision", Title: "証明書更新手順を確認することを決定",
					Body:                "田中が明日までに証明書更新手順を確認することを決定した",
					EvidenceSequenceNos: []int64{2}, CreatedThroughSequenceNo: 2,
				}
				issue := decision
				issue.ID = "decision-as-issue"
				issue.Kind = "issue"
				issue.Title = "証明書更新手順を確認するか"
				issue.Body = "田中が明日までに証明書更新手順を確認するか確認が必要"
				state.Items = append(state.Items, decision, issue)
				state.Tree.Nodes = append(state.Tree.Nodes,
					liveAnalysisTreeNode{ID: decision.ID, Kind: decision.Kind, ParentID: "group", Label: decision.Title},
					liveAnalysisTreeNode{ID: issue.ID, Kind: issue.Kind, ParentID: "group", Label: issue.Title},
				)
				scenario.ForbiddenResults = append(scenario.ForbiddenResults,
					MeetingQualityForbiddenResult{Type: "decision_issue_same_proposition"})
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if !containsStringPrefix(result.ForbiddenResultsFound, "decision_issue_same_proposition:") {
					t.Fatalf("decision/issue exclusivity mutation escaped: %+v", result)
				}
			},
		},
		{
			name: "corrected stale proposition reactivated",
			mutate: func(state *liveAnalysisPayload, scenario *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				stale := liveAnalysisItem{
					ID: "stale-access", Kind: "fact", Title: "交換後スイッチはアクセスポート設定だった",
					Body: "交換後スイッチはアクセスポート設定だった", EvidenceSequenceNos: []int64{3},
				}
				state.Items = append(state.Items, stale)
				state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
					ID: stale.ID, Kind: stale.Kind, ParentID: "group", Label: stale.Title,
				})
				scenario.ForbiddenResults = append(scenario.ForbiddenResults,
					MeetingQualityForbiddenResult{Type: "proposition", Text: stale.Title})
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if !containsExactString(result.ForbiddenResultsFound, "proposition:交換後スイッチはアクセスポート設定だった") {
					t.Fatalf("stale correction mutation escaped: %+v", result)
				}
			},
		},
		{
			name: "truncated label added",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, segments *[]domain.TranscriptSegment) {
				full := strings.Repeat("証明書更新手順の詳細を関係者へ共有するため確認する", 3)
				title := string([]rune(full)[:liveAnalysisItemLabelPreferredMaxRunes])
				item := liveAnalysisItem{
					ID: "truncated", Kind: "fact", Title: title, Body: full,
					EvidenceSequenceNos: []int64{6}, CreatedThroughSequenceNo: 6,
				}
				state.Items = append(state.Items, item)
				state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
					ID: item.ID, Kind: item.Kind, ParentID: "group", Label: item.Title,
				})
				state.CoveredThroughSequenceNo = 6
				*segments = append(*segments, domain.TranscriptSegment{
					SessionID: scenario.ID, CallID: "call", SequenceNo: 6, IsFinal: true, Text: full,
				})
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if result.Metrics.TruncatedLabelCount <= base.Metrics.TruncatedLabelCount {
					t.Fatalf("truncated label metric did not worsen: %+v", result)
				}
			},
		},
		{
			name: "context dependent label added",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, segments *[]domain.TranscriptSegment) {
				item := liveAnalysisItem{
					ID: "anaphora", Kind: "issue", Title: "それを確認する", Body: "その件は未確認",
					EvidenceSequenceNos: []int64{6}, CreatedThroughSequenceNo: 6,
				}
				state.Items = append(state.Items, item)
				state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
					ID: item.ID, Kind: item.Kind, ParentID: "group", Label: item.Title,
				})
				state.CoveredThroughSequenceNo = 6
				*segments = append(*segments, domain.TranscriptSegment{
					SessionID: scenario.ID, CallID: "call", SequenceNo: 6, IsFinal: true,
					Text: "それを確認する。その件は未確認です",
				})
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if result.Metrics.ContextDependentLabelCount <= base.Metrics.ContextDependentLabelCount {
					t.Fatalf("context-dependent metric did not worsen: %+v", result)
				}
			},
		},
		{
			name: "logical siblings split",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				state.Tree.Relations = nil
				for _, value := range []struct{ id, label string }{
					{"topic-fact", "確認済み事実"},
					{"topic-hypothesis", "原因仮説"},
					{"topic-unresolved", "未解決事項"},
				} {
					state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
						ID: value.id, Kind: "topic", ParentID: treeRootNodeID, Label: value.label,
					})
				}
				for index := range state.Tree.Nodes {
					switch state.Tree.Nodes[index].ID {
					case "fact":
						state.Tree.Nodes[index].ParentID = "topic-fact"
					case "hypothesis":
						state.Tree.Nodes[index].ParentID = "topic-hypothesis"
					case "unresolved":
						state.Tree.Nodes[index].ParentID = "topic-unresolved"
					}
				}
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if result.Metrics.HierarchyRelationAccuracy >= base.Metrics.HierarchyRelationAccuracy ||
					len(result.RelationFailures) != 2 {
					t.Fatalf("logical sibling mutation result=%+v", result)
				}
			},
		},
		{
			name: "supported by relation removed",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				kept := state.Tree.Relations[:0]
				for _, relation := range state.Tree.Relations {
					if relation.Kind != itemRelationSupportedBy {
						kept = append(kept, relation)
					}
				}
				state.Tree.Relations = kept
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if result.Metrics.HierarchyRelationAccuracy >= base.Metrics.HierarchyRelationAccuracy ||
					len(result.RelationFailures) != 1 ||
					!strings.Contains(result.RelationFailures[0], "supported_by") {
					t.Fatalf("supported_by deletion mutation result=%+v", result)
				}
			},
		},
		{
			name: "dynamic topic item moved outside tree",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				removeMeetingQualityMutationNode(state, "todo")
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if !containsStringPrefix(result.HardInvariantViolations, "active_item_missing_tree_node:todo") {
					t.Fatalf("item outside dynamic topic was not detected: %+v", result)
				}
			},
		},
		{
			name: "final coverage reduced",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				state.CoveredThroughSequenceNo = 4
			},
			assert: func(t *testing.T, result MeetingQualityScenarioResult, _ MeetingQualityComparisonReport) {
				if !containsStringPrefix(result.HardInvariantViolations, "final_coverage:") {
					t.Fatalf("coverage regression was not detected: %+v", result)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := cloneLiveAnalysisPayload(baseState)
			localScenario := scenario
			localSegments := append([]domain.TranscriptSegment(nil), segments...)
			test.mutate(&state, &localScenario, &localSegments)
			rebuildTreeAuditEdges(state.Tree)
			result := evaluateMeetingQualityMutationSnapshot(localScenario, state, localSegments)
			current := MeetingQualitySuiteReport{
				SchemaVersion: meetingQualitySchemaVersion,
				Suite:         "deterministic",
				Passed:        result.Passed,
				Scenarios:     []MeetingQualityScenarioResult{result},
			}
			comparison := CompareMeetingQualityBaseline(baseline, current)
			if result.Passed && comparison.Passed {
				t.Fatalf("mutation escaped both suite and baseline comparison: result=%+v comparison=%+v", result, comparison)
			}
			test.assert(t, result, comparison)
		})
	}
}

func meetingQualityMutationFixture() (liveAnalysisPayload, MeetingQualityScenario, []domain.TranscriptSegment) {
	items := []liveAnalysisItem{
		{ID: "risk", Kind: "risk", Title: "VPN証明書が来週失効し接続できなくなるリスク", Body: "VPN証明書が来週失効し接続できなくなるリスクがあります", EvidenceSequenceNos: []int64{1}, CreatedThroughSequenceNo: 1},
		{ID: "todo", Kind: "todo", Title: "田中が明日までに証明書更新手順を確認する", Body: "田中が明日までに証明書更新手順を確認します", EvidenceSequenceNos: []int64{2}, CreatedThroughSequenceNo: 2},
		{ID: "fact", Kind: "fact", Title: "VLAN30が許可一覧から漏れている", Body: "VLAN30が許可一覧から漏れていることを確認しました", EvidenceSequenceNos: []int64{3}, CreatedThroughSequenceNo: 3},
		{ID: "hypothesis", Kind: "issue", Title: "設定漏れが通信障害の原因である可能性", Body: "設定漏れが通信障害の原因である可能性があります", EvidenceSequenceNos: []int64{4}, CreatedThroughSequenceNo: 4},
		{ID: "unresolved", Kind: "issue", Title: "2階の遅延まで説明できるか未確認", Body: "2階の遅延まで説明できるかは未確認です", EvidenceSequenceNos: []int64{5}, CreatedThroughSequenceNo: 5},
	}
	state := liveAnalysisPayload{
		Items: items,
		Tree: &liveAnalysisTree{
			Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
				{ID: "dynamic-topic", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク障害と証明書更新", Origin: topicOriginDynamic},
				{ID: "group", Kind: "group", ParentID: "dynamic-topic", Label: "事実・原因仮説・未解決事項"},
				{ID: "risk", Kind: "risk", ParentID: "group", Label: items[0].Title},
				{ID: "todo", Kind: "todo", ParentID: "group", Label: items[1].Title},
				{ID: "fact", Kind: "fact", ParentID: "group", Label: items[2].Title},
				{ID: "hypothesis", Kind: "issue", ParentID: "group", Label: items[3].Title},
				{ID: "unresolved", Kind: "issue", ParentID: "group", Label: items[4].Title},
			},
			Relations: []liveAnalysisTreeRelation{
				{Source: "hypothesis", Target: "fact", Kind: "supported_by"},
				{Source: "unresolved", Target: "hypothesis", Kind: "limits"},
			},
		},
		CoveredThroughSequenceNo: 5,
		TreeVersion:              1,
	}
	rebuildTreeAuditEdges(state.Tree)
	scenario := MeetingQualityScenario{
		ID: "mutation-control",
		RequiredPropositions: []MeetingQualityProposition{
			{ID: "required-risk", Text: items[0].Body, RequiredKind: "risk", EvidenceSequenceNos: []int64{1}},
			{ID: "required-todo", Text: items[1].Body, RequiredKind: "todo", EvidenceSequenceNos: []int64{2}},
			{ID: "required-fact", Text: items[2].Body, RequiredKind: "fact", EvidenceSequenceNos: []int64{3}},
			{ID: "required-hypothesis", Text: items[3].Body, RequiredKind: "issue", EvidenceSequenceNos: []int64{4}},
			{ID: "required-unresolved", Text: items[4].Body, RequiredKind: "issue", EvidenceSequenceNos: []int64{5}},
		},
		RequiredRelations: []MeetingQualityRelation{
			{From: "required-hypothesis", To: "required-fact", Kind: "supported_by"},
			{From: "required-unresolved", To: "required-hypothesis", Kind: "limits"},
		},
		FinalCoverage: 5,
	}
	segments := make([]domain.TranscriptSegment, 0, len(items))
	for index, item := range items {
		segments = append(segments, domain.TranscriptSegment{
			SessionID: scenario.ID, CallID: "call", SequenceNo: int64(index + 1),
			IsFinal: true, Text: item.Body,
		})
	}
	return state, scenario, segments
}

func evaluateMeetingQualityMutationSnapshot(
	scenario MeetingQualityScenario,
	state liveAnalysisPayload,
	segments []domain.TranscriptSegment,
) MeetingQualityScenarioResult {
	result := MeetingQualityScenarioResult{ID: scenario.ID}
	evaluateMeetingQualityResult(&result, scenario, state, &meetingContext{}, segments)
	result.FinalCoverage = state.CoveredThroughSequenceNo
	result.TreeVersion = state.TreeVersion
	result.Passed = result.Error == "" &&
		len(result.HardInvariantViolations) == 0 &&
		len(result.MissingRequiredPropositions) == 0 &&
		len(result.RelationFailures) == 0 &&
		len(result.ForbiddenResultsFound) == 0 &&
		len(result.SafetyFailures) == 0
	return result
}

func removeMeetingQualityMutationItem(state *liveAnalysisPayload, id string) {
	if state == nil {
		return
	}
	items := state.Items[:0]
	for _, item := range state.Items {
		if item.ID != id {
			items = append(items, item)
		}
	}
	state.Items = items
	removeMeetingQualityMutationNode(state, id)
}

func removeMeetingQualityMutationNode(state *liveAnalysisPayload, id string) {
	if state == nil || state.Tree == nil {
		return
	}
	nodes := state.Tree.Nodes[:0]
	for _, node := range state.Tree.Nodes {
		if node.ID != id {
			nodes = append(nodes, node)
		}
	}
	state.Tree.Nodes = nodes
	relations := state.Tree.Relations[:0]
	for _, relation := range state.Tree.Relations {
		if relation.Source != id && relation.Target != id {
			relations = append(relations, relation)
		}
	}
	state.Tree.Relations = relations
}
