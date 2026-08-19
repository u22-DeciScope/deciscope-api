package application

import (
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestSession99FAMutationMatrix(t *testing.T) {
	tests := []struct {
		name       string
		scenarioID string
		mutate     func(*liveAnalysisPayload, *MeetingQualityScenario, *[]domain.TranscriptSegment)
		assert     func(*testing.T, MeetingQualityScenarioResult, MeetingQualityScenarioResult)
	}{
		{
			name:       "grounded detail description emptied",
			scenarioID: "monitoring-risk-issue-description",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				item := mutationActiveItemByKind(state, "risk")
				item.Body = ""
				item.DescriptionResolution = &descriptionResolutionMetadata{Status: descriptionStatusIntentionallyOmitted, Reason: "mutation"}
				if node := liveTreeNodeByID(state.Tree, item.ID); node != nil {
					node.Description = ""
				}
			},
			assert: func(t *testing.T, base, result MeetingQualityScenarioResult) {
				if result.Metrics.DescriptionAddedGroundedDetailCount >= base.Metrics.DescriptionAddedGroundedDetailCount {
					t.Fatalf("grounded description metric did not worsen: base=%+v result=%+v", base.Metrics, result.Metrics)
				}
			},
		},
		{
			name:       "intentionally omitted changed to generation failed",
			scenarioID: "intentionally-omitted-description",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				item := mutationActiveItemByKind(state, "fact")
				item.DescriptionResolution = &descriptionResolutionMetadata{
					Status: descriptionStatusGenerationFailed, Reason: "mutation",
					SourceEvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
				}
			},
			assert: func(t *testing.T, _ MeetingQualityScenarioResult, result MeetingQualityScenarioResult) {
				if !containsStringPrefix(result.HardInvariantViolations, "semantic_state_mismatch:router-fact:descriptionStatus:") {
					t.Fatalf("description status mutation escaped: %+v", result)
				}
			},
		},
		{
			name:       "alert overload risk changed to issue",
			scenarioID: "monitoring-risk-issue-description",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				item := mutationActiveItemByKind(state, "risk")
				item.Kind = "issue"
				if node := liveTreeNodeByID(state.Tree, item.ID); node != nil {
					node.Kind = "issue"
				}
			},
			assert: func(t *testing.T, base, result MeetingQualityScenarioResult) {
				if result.Metrics.ClassificationAccuracy >= base.Metrics.ClassificationAccuracy || len(result.KindMismatches) == 0 {
					t.Fatalf("risk kind mutation escaped: base=%+v result=%+v", base.Metrics, result)
				}
			},
		},
		{
			name:       "risk and issue collapsed into one item",
			scenarioID: "monitoring-risk-issue-description",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				risk := mutationActiveItemByKind(state, "risk")
				issue := mutationActiveItemByKind(state, "issue")
				risk.Title = strings.TrimSpace(risk.Title + "、" + issue.Title)
				risk.Body = strings.TrimSpace(risk.Body + "。" + issue.Body)
				risk.EvidenceSequenceNos = uniqueSortedSequenceNos(sortedSequenceNos(append(risk.EvidenceSequenceNos, issue.EvidenceSequenceNos...)))
				if node := liveTreeNodeByID(state.Tree, risk.ID); node != nil {
					node.Label, node.Description = risk.Title, risk.Body
				}
				removeMeetingQualityMutationItem(state, issue.ID)
			},
			assert: func(t *testing.T, base, result MeetingQualityScenarioResult) {
				if result.Metrics.ClassificationAccuracy >= base.Metrics.ClassificationAccuracy &&
					result.Metrics.RequiredPropositionRecall >= base.Metrics.RequiredPropositionRecall {
					t.Fatalf("risk/issue collapse mutation escaped: base=%+v result=%+v", base.Metrics, result)
				}
			},
		},
		{
			name:       "superseded item reactivated",
			scenarioID: "correction-superseded-resolution-guard",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				item := mutationItemByID(state, "old-access")
				item.Inactive = false
				item.MergedIntoID = ""
				item.Status = "open"
				state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
					ID: item.ID, Kind: item.Kind, ParentID: treeRootNodeID, Label: item.Title, Description: item.Body,
				})
			},
			assert: func(t *testing.T, _ MeetingQualityScenarioResult, result MeetingQualityScenarioResult) {
				if !containsExactString(result.ForbiddenResultsFound, "proposition:交換後スイッチはアクセスポート設定だった") {
					t.Fatalf("superseded reactivation escaped: %+v", result)
				}
			},
		},
		{
			name:       "superseded item changed to resolved",
			scenarioID: "correction-superseded-resolution-guard",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				mutationItemByID(state, "old-access").Status = "resolved"
			},
			assert: func(t *testing.T, _ MeetingQualityScenarioResult, result MeetingQualityScenarioResult) {
				if !containsExactString(result.HardInvariantViolations, "superseded_item_resolved:old-access") {
					t.Fatalf("superseded resolution mutation escaped: %+v", result)
				}
			},
		},
		{
			name:       "todo assignee replaced by speaker",
			scenarioID: "confirmed-todo-speaker-assignee",
			mutate: func(state *liveAnalysisPayload, scenario *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				item := mutationActiveItemByKind(state, "todo")
				item.Title = strings.ReplaceAll(item.Title, "小橋", "山下")
				item.Body = strings.ReplaceAll(item.Body, "小橋", "山下")
				if node := liveTreeNodeByID(state.Tree, item.ID); node != nil {
					node.Label, node.Description = item.Title, item.Body
				}
				scenario.ForbiddenResults = append(scenario.ForbiddenResults, MeetingQualityForbiddenResult{Type: "proposition", Text: item.Title})
			},
			assert: func(t *testing.T, _ MeetingQualityScenarioResult, result MeetingQualityScenarioResult) {
				if !containsStringPrefix(result.ForbiddenResultsFound, "proposition:") {
					t.Fatalf("speaker/assignee mutation escaped: %+v", result)
				}
			},
		},
		{
			name:       "three recovery facts collapsed with unsupported atom",
			scenarioID: "recovery-atomic-fact-split",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				var removeIDs []string
				var evidence []int64
				for _, item := range state.Items {
					if item.Inactive || item.MergedIntoID != "" || item.Kind != "fact" {
						continue
					}
					removeIDs = append(removeIDs, item.ID)
					evidence = append(evidence, item.EvidenceSequenceNos...)
				}
				for _, id := range removeIDs {
					removeMeetingQualityMutationItem(state, id)
				}
				item := liveAnalysisItem{
					ID: "recovery-collapsed", Kind: "fact",
					Title:               "旧スイッチへ切り戻し、新スイッチの設定を修正し、10時5分に接続正常を確認",
					Body:                "旧スイッチへ切り戻し、新スイッチの設定を修正し、10時5分に接続正常を確認。大阪支社のサーバー100台も停止し、対応期限は8月31日だった",
					EvidenceSequenceNos: uniqueSortedSequenceNos(sortedSequenceNos(evidence)),
				}
				state.Items = append(state.Items, item)
				state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{ID: item.ID, Kind: item.Kind, ParentID: treeRootNodeID, Label: item.Title, Description: item.Body})
			},
			assert: func(t *testing.T, base, result MeetingQualityScenarioResult) {
				if result.Metrics.RequiredPropositionRecall >= base.Metrics.RequiredPropositionRecall ||
					result.Metrics.DescriptionUnsupportedAtomCount <= base.Metrics.DescriptionUnsupportedAtomCount ||
					len(result.MissingRequiredPropositions) == 0 {
					t.Fatalf("collapsed recovery mutation escaped: base=%+v result=%+v", base.Metrics, result)
				}
			},
		},
		{
			name:       "monitoring and vpn risks merged into one candidate",
			scenarioID: "unrelated-monitoring-vpn-candidates",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				var riskIDs []string
				for index := range state.Items {
					item := &state.Items[index]
					if item.Inactive || item.MergedIntoID != "" || item.Kind != "risk" {
						continue
					}
					item.ClassificationStatus = classificationTentative
					item.CandidateTopicID = "mixed-monitoring-vpn"
					riskIDs = append(riskIDs, item.ID)
				}
				state.EmergingTopics = append(state.EmergingTopics, emergingTopicCandidate{
					ID: "mixed-monitoring-vpn", Label: "監視運用", Description: "アラート過多",
					EvidenceItemIDs: riskIDs, OriginItemIDs: append([]string(nil), riskIDs...),
					FirstRound: 1, LastRound: 2, RoundCount: 2,
				})
			},
			assert: func(t *testing.T, base, result MeetingQualityScenarioResult) {
				if result.Metrics.CandidateFragmentationCount <= base.Metrics.CandidateFragmentationCount {
					t.Fatalf("mixed candidate metric did not worsen: base=%+v result=%+v", base.Metrics, result.Metrics)
				}
			},
		},
		{
			name:       "supported by removed from final snapshot",
			scenarioID: "final-relation-metadata-persistence",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment) {
				kept := state.Tree.Relations[:0]
				for _, relation := range state.Tree.Relations {
					if relation.Kind != itemRelationSupportedBy {
						kept = append(kept, relation)
					}
				}
				state.Tree.Relations = kept
			},
			assert: func(t *testing.T, base, result MeetingQualityScenarioResult) {
				if result.Metrics.HierarchyRelationAccuracy >= base.Metrics.HierarchyRelationAccuracy ||
					len(result.RelationFailures) == 0 {
					t.Fatalf("supported_by mutation escaped: base=%+v result=%+v", base.Metrics, result)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, scenario, segments := mutationQualityScenarioState(t, test.scenarioID)
			base := evaluateMeetingQualityMutationSnapshot(scenario, state, segments)
			if !base.Passed {
				t.Fatalf("mutation control scenario failed: %+v", base)
			}
			baseline := NewMeetingQualityBaseline(MeetingQualitySuiteReport{
				SchemaVersion: meetingQualitySchemaVersion, Suite: "deterministic", Passed: true,
				Scenarios: []MeetingQualityScenarioResult{base},
			})
			mutated := cloneLiveAnalysisPayload(state)
			localScenario := scenario
			localSegments := append([]domain.TranscriptSegment(nil), segments...)
			test.mutate(&mutated, &localScenario, &localSegments)
			rebuildTreeAuditEdges(mutated.Tree)
			result := evaluateMeetingQualityMutationSnapshot(localScenario, mutated, localSegments)
			comparison := CompareMeetingQualityBaseline(baseline, MeetingQualitySuiteReport{
				SchemaVersion: meetingQualitySchemaVersion, Suite: "deterministic", Passed: result.Passed,
				Scenarios: []MeetingQualityScenarioResult{result},
			})
			if result.Passed && comparison.Passed {
				t.Fatalf("mutation escaped both scenario and baseline comparison: %+v", result)
			}
			test.assert(t, base, result)
		})
	}
}

func mutationQualityScenarioState(t *testing.T, id string) (liveAnalysisPayload, MeetingQualityScenario, []domain.TranscriptSegment) {
	t.Helper()
	suite := loadDeterministicMeetingQualitySuite(t)
	for _, scenario := range suite.Scenarios {
		if scenario.ID != id {
			continue
		}
		_, state, _ := finalQualityScenarioState(t, id)
		state.CoveredThroughSequenceNo = scenario.FinalCoverage
		return state, scenario, qualityDomainSegments(scenario)
	}
	t.Fatalf("quality scenario %q not found", id)
	return liveAnalysisPayload{}, MeetingQualityScenario{}, nil
}

func mutationActiveItemByKind(state *liveAnalysisPayload, kind string) *liveAnalysisItem {
	if state == nil {
		panic("nil mutation state")
	}
	for index := range state.Items {
		item := &state.Items[index]
		if !item.Inactive && item.MergedIntoID == "" && item.Kind == kind {
			return item
		}
	}
	panic("active mutation item not found for kind " + kind)
}

func mutationItemByID(state *liveAnalysisPayload, id string) *liveAnalysisItem {
	if state == nil {
		panic("nil mutation state")
	}
	for index := range state.Items {
		if state.Items[index].ID == id {
			return &state.Items[index]
		}
	}
	panic("mutation item not found: " + id)
}
