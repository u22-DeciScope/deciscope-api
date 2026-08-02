package application

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestMeetingQualitySemanticMatcherUsesDeterministicPropositionFacets(t *testing.T) {
	tests := []struct {
		name      string
		expected  string
		actual    string
		wantMatch bool
	}{
		{
			name:      "natural paraphrase",
			expected:  "VPN証明書が来週失効する",
			actual:    "来週にVPN証明書が期限切れになる",
			wantMatch: true,
		},
		{
			name:     "different deadline",
			expected: "証明書更新の期限は金曜日",
			actual:   "証明書更新の期限は来週月曜日",
		},
		{
			name:     "different floor qualifier",
			expected: "2階で通信障害が発生",
			actual:   "3階で通信障害が発生",
		},
		{
			name:     "hypothesis is not confirmed fact",
			expected: "設定漏れが原因である可能性",
			actual:   "設定漏れが原因だと確認しました",
		},
		{
			name:     "same subject different predicate",
			expected: "VPN証明書を更新する",
			actual:   "VPN接続を停止する",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			score := qualityPropositionSimilarity(test.expected, test.actual)
			if got := score >= defaultPropositionMatchSimilarity; got != test.wantMatch {
				t.Fatalf("score=%.3f match=%t, want %t", score, got, test.wantMatch)
			}
		})
	}
}

func TestMeetingQualityBaselineImprovementRatchet(t *testing.T) {
	before := MeetingQualityScenarioResult{
		ID: "risk-ratchet", Passed: true,
		Metrics: MeetingQualityMetrics{
			RequiredPropositionRecall: 1,
			ClassificationAccuracy:    0.5,
			RiskRecall:                0,
			TodoRecall:                1,
			DecisionRecall:            1,
			HierarchyRelationAccuracy: 1,
		},
	}
	improved := before
	improved.Metrics.ClassificationAccuracy = 1
	improved.Metrics.RiskRecall = 1
	baseline := NewMeetingQualityBaseline(MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion,
		Suite:         "deterministic",
		Passed:        true,
		Scenarios:     []MeetingQualityScenarioResult{before},
	})
	current := MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion,
		Suite:         "deterministic",
		Passed:        true,
		Scenarios:     []MeetingQualityScenarioResult{improved},
	}
	comparison := CompareMeetingQualityBaseline(baseline, current)
	if comparison.Passed || !comparison.BaselineUpdateRequired {
		t.Fatalf("unaccepted improvement must fail CI comparison: %+v", comparison)
	}
	ratcheted, update, err := AcceptMeetingQualityImprovements(baseline, current)
	if err != nil {
		t.Fatalf("accept improvements: %v", err)
	}
	if len(update.AppliedMetrics) != 2 {
		t.Fatalf("applied=%+v, want classification and risk improvements", update.AppliedMetrics)
	}
	if comparison := CompareMeetingQualityBaseline(ratcheted, current); !comparison.Passed {
		t.Fatalf("ratcheted baseline does not match improved result: %+v", comparison)
	}
	regressed := current
	regressed.Scenarios = []MeetingQualityScenarioResult{before}
	if comparison := CompareMeetingQualityBaseline(ratcheted, regressed); comparison.Passed ||
		len(comparison.WorsenedMetrics) != 2 {
		t.Fatalf("post-improvement regression was not detected: %+v", comparison)
	}
	if _, _, err := AcceptMeetingQualityImprovements(ratcheted, regressed); err == nil {
		t.Fatal("worsened result was accepted into baseline")
	}
}

func TestMeetingQualityBaselineRejectsSchemaAndScenarioDeletion(t *testing.T) {
	result := MeetingQualityScenarioResult{ID: "kept", Passed: true}
	baseline := NewMeetingQualityBaseline(MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion,
		Suite:         "deterministic",
		Passed:        true,
		Scenarios:     []MeetingQualityScenarioResult{result},
	})
	brokenSchema := baseline
	brokenSchema.MetricSchema = brokenSchema.MetricSchema[:len(brokenSchema.MetricSchema)-1]
	if err := ValidateMeetingQualityBaseline(brokenSchema); err == nil {
		t.Fatal("baseline metric deletion was not rejected")
	}
	current := MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion,
		Suite:         "deterministic",
		Passed:        true,
	}
	if _, _, err := AcceptMeetingQualityImprovements(baseline, current); err == nil {
		t.Fatal("scenario deletion was accepted")
	}
}

func TestMeetingQualityBaselineMigratesOnlyKnownAdditiveV3Metrics(t *testing.T) {
	currentScenario := MeetingQualityScenarioResult{ID: "kept", Passed: true}
	currentScenario.Metrics.DescriptionAddedGroundedDetailCount = 2
	currentScenario.Metrics.LabelCompressionRatio = 0.5
	current := MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion, Suite: "deterministic", Passed: true,
		Scenarios: []MeetingQualityScenarioResult{currentScenario},
	}
	current.Scenarios[0].PropositionMatches = []MeetingQualityPropositionMatch{{
		PropositionID: "fact", RequiredTemporalScope: "unknown", Matched: true,
	}}
	legacy := MeetingQualityBaseline{
		SchemaVersion: 3, Suite: "deterministic",
		MetricSchema: append([]string(nil), meetingQualityMetricSchemaV3...),
		Scenarios: []MeetingQualityScenarioResult{{
			ID: "kept", Passed: true, Metrics: MeetingQualityMetrics{PastFactCount: 1},
			PropositionMatches: []MeetingQualityPropositionMatch{{PropositionID: "fact"}},
		}},
	}

	updated, report, err := AcceptMeetingQualityImprovements(legacy, current)
	if err != nil {
		t.Fatalf("migrate additive v3 baseline: %v", err)
	}
	if updated.SchemaVersion != meetingQualityBaselineSchemaVersion ||
		!reflect.DeepEqual(updated.MetricSchema, qualityMetricNames()) ||
		len(report.AddedMetricSchema) != len(qualityMetricNames())-len(meetingQualityMetricSchemaV3) ||
		len(report.AppliedMetrics) != 1 || report.AppliedMetrics[0].Metric != "pastFactCount" {
		t.Fatalf("updated=%+v report=%+v", updated, report)
	}
	if comparison := CompareMeetingQualityBaseline(updated, current); !comparison.Passed {
		t.Fatalf("migrated baseline does not match current: %+v", comparison)
	}

	broken := legacy
	broken.MetricSchema = append([]string(nil), legacy.MetricSchema...)
	broken.MetricSchema[0], broken.MetricSchema[1] = broken.MetricSchema[1], broken.MetricSchema[0]
	if _, _, err := AcceptMeetingQualityImprovements(broken, current); err == nil {
		t.Fatal("reordered legacy metric schema was accepted")
	}
}

func TestMeetingQualityBaselineAddsPassingScenarioOnlyByExplicitAcceptance(t *testing.T) {
	kept := MeetingQualityScenarioResult{ID: "kept", Passed: true}
	added := MeetingQualityScenarioResult{ID: "added", Passed: true}
	baseline := NewMeetingQualityBaseline(MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion,
		Suite:         "deterministic",
		Passed:        true,
		Scenarios:     []MeetingQualityScenarioResult{kept},
	})
	current := MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion,
		Suite:         "deterministic",
		Passed:        true,
		Scenarios:     []MeetingQualityScenarioResult{kept, added},
	}
	comparison := CompareMeetingQualityBaseline(baseline, current)
	if comparison.Passed || !comparison.BaselineUpdateRequired ||
		len(comparison.NewScenarios) != 1 || comparison.NewScenarios[0] != "added" ||
		len(comparison.NewFailures) != 0 {
		t.Fatalf("new passing scenario was not reported as an explicit update: %+v", comparison)
	}
	updated, report, err := AcceptMeetingQualityImprovements(baseline, current)
	if err != nil {
		t.Fatalf("accept new passing scenario: %v", err)
	}
	if len(report.AddedScenarios) != 1 || report.AddedScenarios[0] != "added" {
		t.Fatalf("added scenarios=%v", report.AddedScenarios)
	}
	if comparison := CompareMeetingQualityBaseline(updated, current); !comparison.Passed {
		t.Fatalf("accepted scenario did not ratchet: %+v", comparison)
	}
	if _, _, err := AcceptMeetingQualityImprovements(updated, MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion,
		Suite:         "deterministic",
		Passed:        true,
		Scenarios:     []MeetingQualityScenarioResult{kept},
	}); err == nil {
		t.Fatal("scenario deletion was accepted after ratcheting")
	}
}

func TestMeetingQualityBaselineAcceptsExactFactIncreaseOnlyWithNewMatchedExpectation(t *testing.T) {
	before := MeetingQualityScenarioResult{
		ID: "atomic-facts", Passed: true,
		Metrics: MeetingQualityMetrics{PastFactCount: 1},
		PropositionMatches: []MeetingQualityPropositionMatch{{
			PropositionID: "vlan", RequiredKind: "fact", Matched: true,
		}},
		KindDistribution:  []MeetingQualityKindCount{{Kind: "fact", Count: 1}},
		ParentAssignments: []MeetingQualityParentAssignment{{PropositionID: "vlan", Kind: "fact", ParentPath: []string{"設定"}}},
	}
	after := before
	after.Metrics.PastFactCount = 2
	after.PropositionMatches = append(after.PropositionMatches, MeetingQualityPropositionMatch{
		PropositionID: "trunk", RequiredKind: "fact", Matched: true,
	})
	after.KindDistribution = []MeetingQualityKindCount{{Kind: "fact", Count: 2}}
	after.ParentAssignments = append(after.ParentAssignments,
		MeetingQualityParentAssignment{PropositionID: "trunk", Kind: "fact", ParentPath: []string{"設定"}})
	baseline := NewMeetingQualityBaseline(MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion, Suite: "deterministic", Passed: true,
		Scenarios: []MeetingQualityScenarioResult{before},
	})
	current := MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion, Suite: "deterministic", Passed: true,
		Scenarios: []MeetingQualityScenarioResult{after},
	}
	updated, report, err := AcceptMeetingQualityImprovements(baseline, current)
	if err != nil {
		t.Fatalf("accept reviewed exact fact increase: %v", err)
	}
	if len(report.AppliedMetrics) != 1 || report.AppliedMetrics[0].Metric != "pastFactCount" ||
		updated.Scenarios[0].Metrics.PastFactCount != 2 || len(updated.Scenarios[0].ParentAssignments) != 2 {
		t.Fatalf("updated=%+v report=%+v", updated, report)
	}
	if comparison := CompareMeetingQualityBaseline(updated, current); !comparison.Passed {
		t.Fatalf("reviewed exact fact baseline does not match: %+v", comparison)
	}

	unreviewed := after
	unreviewed.PropositionMatches = append([]MeetingQualityPropositionMatch(nil), before.PropositionMatches...)
	if _, _, err := AcceptMeetingQualityImprovements(baseline, MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion, Suite: "deterministic", Passed: true,
		Scenarios: []MeetingQualityScenarioResult{unreviewed},
	}); err == nil {
		t.Fatal("exact fact drift without a new matched expectation was accepted")
	}
}

func TestMeetingQualityEvaluatorDetectsGroundedRiskLoss(t *testing.T) {
	scenario := MeetingQualityScenario{
		ID: "risk-loss",
		RequiredPropositions: []MeetingQualityProposition{{
			ID: "vpn-risk", Text: "VPN証明書の期限切れで接続できなくなるリスク",
			RequiredKind: "risk", EvidenceSequenceNos: []int64{1},
		}},
		FinalCoverage: 1,
	}
	state := liveAnalysisPayload{
		Tree: &liveAnalysisTree{
			Nodes: []liveAnalysisTreeNode{{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"}},
			Edges: []liveAnalysisTreeEdge{},
		},
		CoveredThroughSequenceNo: 1,
	}
	segments := []domain.TranscriptSegment{{
		SequenceNo: 1, CallID: "call", IsFinal: true,
		Text: "VPN証明書が来週期限切れになり、更新しなければ接続できなくなるリスクがあります。",
	}}
	var result MeetingQualityScenarioResult
	evaluateMeetingQualityResult(&result, scenario, state, &meetingContext{}, segments)
	if !containsStringPrefix(result.HardInvariantViolations, "missing_required_proposition:vpn-risk") {
		t.Fatalf("violations=%v, want grounded risk loss", result.HardInvariantViolations)
	}
	if result.Metrics.RiskRecall != 0 {
		t.Fatalf("riskRecall=%v, want 0", result.Metrics.RiskRecall)
	}
}

func TestMeetingQualityEvaluatorReportsTemporalAndResolutionLifecycle(t *testing.T) {
	text := "障害発生時には接続できる端末と接続できない端末が混在していました。"
	scenario := MeetingQualityScenario{
		ID: "temporal-lifecycle-metrics",
		RequiredPropositions: []MeetingQualityProposition{{
			ID: "historical", Text: "障害時に接続可否が端末ごとに混在していた",
			RequiredKind: "fact", RequiredTemporalScope: "past", RequiredEpistemicStatus: "confirmed", RequiredStatus: "open",
			EvidenceSequenceNos: []int64{1},
		}},
		FinalCoverage: 1,
	}
	item := liveAnalysisItem{
		ID: "historical", Kind: "fact", Title: "障害時に端末ごとの接続可否が混在",
		Body: text, Status: "open", EvidenceSequenceNos: []int64{1}, CreatedThroughSequenceNo: 1,
	}
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{item},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
			{ID: "historical", Kind: "fact", ParentID: treeRootNodeID, Label: item.Title},
		}},
		CoveredThroughSequenceNo: 1,
	}
	rebuildTreeAuditEdges(state.Tree)
	segments := []domain.TranscriptSegment{{SequenceNo: 1, CallID: "call", IsFinal: true, Text: text}}
	var correct MeetingQualityScenarioResult
	evaluateMeetingQualityResult(&correct, scenario, state, &meetingContext{}, segments)
	if correct.Metrics.TemporalScopeAccuracy != 1 || correct.Metrics.PastFactCount != 1 ||
		correct.Metrics.IssueCount != 0 || correct.Metrics.ResolvedIssueCount != 0 ||
		correct.Metrics.IncorrectResolvedIssueCount != 0 || len(correct.SemanticStateMismatches) != 0 {
		t.Fatalf("correct temporal lifecycle metrics=%+v mismatches=%+v", correct.Metrics, correct.SemanticStateMismatches)
	}

	state.Items[0].Kind = "issue"
	state.Items[0].Status = "resolved"
	state.Tree.Nodes[1].Kind = "issue"
	var corrupted MeetingQualityScenarioResult
	evaluateMeetingQualityResult(&corrupted, scenario, state, &meetingContext{}, segments)
	if corrupted.Metrics.ClassificationAccuracy != 0 || corrupted.Metrics.IssueCount != 1 ||
		corrupted.Metrics.ResolvedIssueCount != 1 || corrupted.Metrics.IncorrectResolvedIssueCount != 1 ||
		len(corrupted.KindMismatches) != 1 || len(corrupted.SemanticStateMismatches) != 1 {
		t.Fatalf("incorrect historical resolution escaped evaluator: metrics=%+v kind=%+v state=%+v",
			corrupted.Metrics, corrupted.KindMismatches, corrupted.SemanticStateMismatches)
	}

	state.Items[0] = item
	state.Items[0].Title = "現在も全端末で接続できない"
	state.Items[0].Body = "現在も全端末で接続できません。"
	state.Tree.Nodes[1].Kind = "fact"
	state.Tree.Nodes[1].Label = state.Items[0].Title
	temporalScenario := scenario
	temporalScenario.RequiredPropositions = append([]MeetingQualityProposition(nil), scenario.RequiredPropositions...)
	temporalScenario.RequiredPropositions[0].Text = "現在も全端末で接続できない"
	temporalSegments := append([]domain.TranscriptSegment(nil), segments...)
	temporalSegments[0].Text = state.Items[0].Body
	var temporalMismatch MeetingQualityScenarioResult
	evaluateMeetingQualityResult(&temporalMismatch, temporalScenario, state, &meetingContext{}, temporalSegments)
	if temporalMismatch.Metrics.TemporalScopeAccuracy != 0 || len(temporalMismatch.SemanticStateMismatches) == 0 ||
		!containsStringPrefix(temporalMismatch.HardInvariantViolations, "semantic_state_mismatch:historical:temporalScope:") {
		t.Fatalf("temporal mismatch escaped evaluator: %+v", temporalMismatch)
	}
}

func TestMeetingQualityEvaluatorDetectsUnnaturalLogicalHierarchy(t *testing.T) {
	scenario := MeetingQualityScenario{
		ID: "network-hierarchy",
		RequiredPropositions: []MeetingQualityProposition{
			{ID: "fact", Text: "VLAN30が許可一覧から漏れていた", RequiredKind: "fact"},
			{ID: "hypothesis", Text: "設定漏れが3階障害の直接原因である可能性が高い", AllowedKinds: []string{"issue", "fact"}},
			{ID: "limit", Text: "2階の通信遅延まで説明できるかは未確認", RequiredKind: "issue"},
		},
		RequiredRelations: []MeetingQualityRelation{
			{From: "hypothesis", To: "fact", Kind: "supported_by", RequireSameBranch: true},
			{From: "limit", To: "hypothesis", Kind: "limits", RequireSameBranch: true},
		},
		FinalCoverage: 3,
	}
	items := []liveAnalysisItem{
		{ID: "fact-1", Kind: "fact", Title: "VLAN30が許可一覧から漏れていた", Body: "許可設定を確認した", EvidenceSequenceNos: []int64{1}, CreatedThroughSequenceNo: 1},
		{ID: "hypothesis-1", Kind: "issue", Title: "設定漏れが3階障害の直接原因である可能性が高い", Body: "原因仮説", EvidenceSequenceNos: []int64{2}, CreatedThroughSequenceNo: 2},
		{ID: "limit-1", Kind: "issue", Title: "2階の通信遅延まで説明できるかは未確認", Body: "適用範囲の確認", EvidenceSequenceNos: []int64{3}, CreatedThroughSequenceNo: 3},
	}
	state := liveAnalysisPayload{
		Items: items,
		Tree: &liveAnalysisTree{
			Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
				{ID: "topic-fact", Kind: "topic", ParentID: treeRootNodeID, Label: "VLAN設定"},
				{ID: "topic-hypothesis", Kind: "topic", ParentID: treeRootNodeID, Label: "3階障害"},
				{ID: "topic-limit", Kind: "topic", ParentID: treeRootNodeID, Label: "2階遅延"},
				{ID: "fact-1", Kind: "fact", ParentID: "topic-fact", Label: items[0].Title},
				{ID: "hypothesis-1", Kind: "issue", ParentID: "topic-hypothesis", Label: items[1].Title},
				{ID: "limit-1", Kind: "issue", ParentID: "topic-limit", Label: items[2].Title},
			},
		},
		CoveredThroughSequenceNo: 3,
	}
	rebuildTreeAuditEdges(state.Tree)
	segments := []domain.TranscriptSegment{
		{SequenceNo: 1, CallID: "call", IsFinal: true, Text: items[0].Title},
		{SequenceNo: 2, CallID: "call", IsFinal: true, Text: items[1].Title},
		{SequenceNo: 3, CallID: "call", IsFinal: true, Text: items[2].Title},
	}
	var result MeetingQualityScenarioResult
	evaluateMeetingQualityResult(&result, scenario, state, &meetingContext{}, segments)
	if len(result.RelationFailures) != 2 {
		t.Fatalf("relationFailures=%v, want both logical relations rejected", result.RelationFailures)
	}
	if result.Metrics.HierarchyRelationAccuracy != 0 {
		t.Fatalf("hierarchyRelationAccuracy=%v, want 0", result.Metrics.HierarchyRelationAccuracy)
	}
}

func TestCompareMeetingQualityBaselineDoesNotOffsetWorseningWithImprovement(t *testing.T) {
	before := MeetingQualityScenarioResult{
		ID: "scenario", Passed: true,
		Metrics: MeetingQualityMetrics{
			RequiredPropositionRecall: 1,
			ClassificationAccuracy:    0.8,
			RiskRecall:                1,
			TodoRecall:                1,
			DecisionRecall:            1,
			HierarchyRelationAccuracy: 1,
			SemanticDuplicateCount:    1,
		},
	}
	after := before
	after.Metrics.ClassificationAccuracy = 1
	after.Metrics.RequiredPropositionRecall = 0.5
	after.Metrics.SemanticDuplicateCount = 0
	comparison := CompareMeetingQualityBaseline(
		MeetingQualityBaseline{
			SchemaVersion: meetingQualityBaselineSchemaVersion,
			Suite:         "deterministic",
			MetricSchema:  qualityMetricNames(),
			Scenarios:     []MeetingQualityScenarioResult{before},
		},
		MeetingQualitySuiteReport{SchemaVersion: meetingQualitySchemaVersion, Suite: "deterministic", Passed: true, Scenarios: []MeetingQualityScenarioResult{after}},
	)
	if comparison.Passed {
		t.Fatal("comparison passed despite a worsened independent axis")
	}
	if len(comparison.WorsenedMetrics) != 1 || comparison.WorsenedMetrics[0].Metric != "requiredPropositionRecall" {
		t.Fatalf("worsened=%+v", comparison.WorsenedMetrics)
	}
	if len(comparison.ImprovedMetrics) != 2 {
		t.Fatalf("improved=%+v, want independent classification and duplicate improvements", comparison.ImprovedMetrics)
	}
}

func TestCompareMeetingQualityBaselineReportsSemanticAndStructuralDiffs(t *testing.T) {
	beforeStable := MeetingQualityScenarioResult{
		ID: "stable", Passed: true,
		Metrics: MeetingQualityMetrics{
			RequiredPropositionRecall: 1, ClassificationAccuracy: 1,
			RiskRecall: 1, TodoRecall: 1, DecisionRecall: 1,
			HierarchyRelationAccuracy: 1,
		},
		ParentAssignments: []MeetingQualityParentAssignment{{
			PropositionID: "risk", Kind: "risk", ParentPath: []string{"before"},
		}},
		KindDistribution: []MeetingQualityKindCount{{Kind: "risk", Count: 1}},
	}
	afterStable := beforeStable
	afterStable.Passed = false
	afterStable.MissingRequiredPropositions = []string{"risk"}
	afterStable.UnsupportedPropositions = []string{"invented"}
	afterStable.ParentAssignments = []MeetingQualityParentAssignment{{
		PropositionID: "risk", Kind: "risk", ParentPath: []string{"after"},
	}}
	afterStable.KindDistribution = []MeetingQualityKindCount{{Kind: "fact", Count: 1}}
	beforeBroken := beforeStable
	beforeBroken.ID = "repaired"
	beforeBroken.Passed = false
	afterRepaired := beforeBroken
	afterRepaired.Passed = true

	comparison := CompareMeetingQualityBaseline(
		MeetingQualityBaseline{
			SchemaVersion: meetingQualityBaselineSchemaVersion,
			Suite:         "deterministic",
			MetricSchema:  qualityMetricNames(),
			Scenarios:     []MeetingQualityScenarioResult{beforeStable, beforeBroken},
		},
		MeetingQualitySuiteReport{
			SchemaVersion: meetingQualitySchemaVersion,
			Suite:         "deterministic",
			Scenarios:     []MeetingQualityScenarioResult{afterStable, afterRepaired},
		},
	)
	if comparison.Passed {
		t.Fatal("comparison passed despite new semantic and structural failures")
	}
	if len(comparison.NewFailures) != 1 || comparison.NewFailures[0] != "stable" {
		t.Fatalf("newFailures=%v", comparison.NewFailures)
	}
	if len(comparison.RepairedScenarios) != 1 || comparison.RepairedScenarios[0] != "repaired" {
		t.Fatalf("repaired=%v", comparison.RepairedScenarios)
	}
	if len(comparison.LostRequiredPropositions) != 1 ||
		len(comparison.NewUnsupportedPropositions) != 1 ||
		len(comparison.ParentRelationDiffs) != 1 ||
		len(comparison.KindDistributionDiffs) != 1 {
		t.Fatalf("comparison did not report all semantic/structural diffs: %+v", comparison)
	}
}

func TestRunMeetingQualitySuiteUsesProductionMergeAndSemanticExpectation(t *testing.T) {
	response := json.RawMessage(`{
		"summary":"証明書更新",
		"currentTopic":"VPN証明書",
		"items":[{
			"clientKey":"risk-cert",
			"kind":"risk",
			"severity":"high",
			"title":"VPN証明書失効による接続不能",
			"body":"来週までに更新しないとVPNへ接続できない",
			"status":"open",
			"evidenceSequenceNos":[1],
			"evidenceSnippets":["VPN証明書は来週失効し、更新しないとVPNへ接続できません"]
		}],
		"newTopics":[{"id":"topic-vpn","label":"VPN証明書更新","description":"失効リスク"}],
		"assignments":[{"nodeId":"risk-cert","parentTopicId":"topic-vpn","confidence":0.9}]
	}`)
	suite := MeetingQualitySuite{
		SchemaVersion: meetingQualitySchemaVersion,
		Name:          "deterministic",
		Scenarios: []MeetingQualityScenario{{
			ID: "semantic-replay",
			TranscriptSegments: []MeetingQualityTranscriptSegment{{
				SequenceNo: 1, Text: "VPN証明書は来週失効し、更新しないとVPNへ接続できません。",
			}},
			Rounds: []MeetingQualityRound{{SequenceNos: []int64{1}, FixedAIResponse: response}},
			RequiredPropositions: []MeetingQualityProposition{{
				ID: "risk", Text: "VPN証明書の期限切れで接続不能になるリスク", RequiredKind: "risk",
			}},
			FinalCoverage: 1,
		}},
	}
	report := RunMeetingQualitySuite(suite)
	if !report.Passed {
		t.Fatalf("report=%+v", report.Scenarios[0])
	}
	if report.Scenarios[0].Metrics.RequiredPropositionRecall != 1 ||
		report.Scenarios[0].FinalCoverage != 1 {
		t.Fatalf("result=%+v", report.Scenarios[0])
	}
}

func TestMeetingQualityHardInvariantsFailIndependently(t *testing.T) {
	base := qualityInvariantTestState()
	segments := []domain.TranscriptSegment{{
		SequenceNo: 1, CallID: "call", IsFinal: true,
		Text: "VPN証明書の有効期限を確認した。",
	}}
	scenario := MeetingQualityScenario{
		ID: "hard-invariants",
		RequiredPropositions: []MeetingQualityProposition{{
			ID: "fact", Text: "VPN証明書の有効期限を確認した", RequiredKind: "fact",
		}},
		FinalCoverage: 1,
	}
	context := &meetingContext{}
	tests := []struct {
		name   string
		prefix string
		mutate func(*liveAnalysisPayload, *MeetingQualityScenario, *[]domain.TranscriptSegment, **meetingContext)
	}{
		{
			name: "root missing", prefix: "root_count:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.Tree.Nodes = state.Tree.Nodes[1:]
				rebuildTreeAuditEdges(state.Tree)
			},
		},
		{
			name: "orphan node", prefix: "orphan_node:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.Tree.Nodes[2].ParentID = "missing-parent"
				rebuildTreeAuditEdges(state.Tree)
			},
		},
		{
			name: "missing edge endpoint", prefix: "missing_edge_endpoint:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.Tree.Edges = append(state.Tree.Edges, liveAnalysisTreeEdge{Source: "missing", Target: "fact-1"})
			},
		},
		{
			name: "duplicate node id", prefix: "duplicate_node_id:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.Tree.Nodes = append(state.Tree.Nodes, state.Tree.Nodes[2])
			},
		},
		{
			name: "invalid parent kind", prefix: "invalid_parent_kind:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.Items = append(state.Items, liveAnalysisItem{
					ID: "todo-2", Kind: "todo", Title: "確認結果を共有する",
					EvidenceSequenceNos: []int64{1}, CreatedThroughSequenceNo: 1,
				})
				state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
					ID: "todo-2", Kind: "todo", ParentID: "fact-1", Label: "確認結果を共有する",
				})
				rebuildTreeAuditEdges(state.Tree)
			},
		},
		{
			name: "self parent", prefix: "self_parent:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.Tree.Nodes[2].ParentID = "fact-1"
				rebuildTreeAuditEdges(state.Tree)
			},
		},
		{
			name: "depth limit", prefix: "depth_limit:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				parent := "topic-1"
				for index := 0; index < treeHardMaxDepth; index++ {
					id := "group-" + string(rune('a'+index))
					state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
						ID: id, Kind: "group", ParentID: parent, Label: id,
					})
					parent = id
				}
				state.Tree.Nodes[2].ParentID = parent
				rebuildTreeAuditEdges(state.Tree)
			},
		},
		{
			name: "agenda reference", prefix: "unknown_agenda_reference:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.Tree.Nodes[1].AgendaRefs = []string{"agenda-missing"}
			},
		},
		{
			name: "duplicate materialization", prefix: "duplicate_materialized_topic:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, context **meetingContext) {
				*context = &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "証明書", Order: 1}}}
				state.Tree.Nodes[1].AgendaRefs = []string{"agenda-1"}
				state.Tree.Nodes[1].Origin = topicOriginAgenda
				state.Items = append(state.Items, liveAnalysisItem{
					ID: "fact-2", Kind: "fact", Title: "証明書更新手順を確認した",
					EvidenceSequenceNos: []int64{1}, CreatedThroughSequenceNo: 1,
				})
				state.Tree.Nodes = append(state.Tree.Nodes,
					liveAnalysisTreeNode{ID: "topic-2", Kind: "topic", ParentID: treeRootNodeID, Label: "証明書更新", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}},
					liveAnalysisTreeNode{ID: "fact-2", Kind: "fact", ParentID: "topic-2", Label: "証明書更新手順を確認した"},
				)
				rebuildTreeAuditEdges(state.Tree)
			},
		},
		{
			name: "inactive resurrection", prefix: "inactive_item_resurrected:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.Items[0].Inactive = true
			},
		},
		{
			name: "future evidence", prefix: "future_evidence:",
			mutate: func(state *liveAnalysisPayload, _ *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.Items[0].EvidenceSequenceNos = []int64{2}
			},
		},
		{
			name: "unsupported central proposition", prefix: "unsupported_central_proposition:",
			mutate: func(state *liveAnalysisPayload, scenario *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.Items[0].Title = "大阪支社のサーバー100台を停止した"
				state.Items[0].Body = "大阪支社のサーバーを停止した"
				state.Tree.Nodes[2].Label = state.Items[0].Title
				scenario.RequiredPropositions = []MeetingQualityProposition{{
					ID: "fabricated", Text: state.Items[0].Title, RequiredKind: "fact",
				}}
			},
		},
		{
			name: "required proposition missing", prefix: "missing_required_proposition:",
			mutate: func(_ *liveAnalysisPayload, scenario *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				scenario.RequiredPropositions = []MeetingQualityProposition{{
					ID: "missing", Text: "存在しない必須命題", RequiredKind: "risk",
				}}
			},
		},
		{
			name: "final coverage", prefix: "final_coverage:",
			mutate: func(state *liveAnalysisPayload, scenario *MeetingQualityScenario, _ *[]domain.TranscriptSegment, _ **meetingContext) {
				state.CoveredThroughSequenceNo = 0
				scenario.FinalCoverage = 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := cloneLiveAnalysisPayload(base)
			localScenario := scenario
			localSegments := append([]domain.TranscriptSegment(nil), segments...)
			localContext := context
			test.mutate(&state, &localScenario, &localSegments, &localContext)
			var result MeetingQualityScenarioResult
			evaluateMeetingQualityResult(&result, localScenario, state, localContext, localSegments)
			if !containsStringPrefix(result.HardInvariantViolations, test.prefix) {
				t.Fatalf("violations=%v, want prefix %q", result.HardInvariantViolations, test.prefix)
			}
		})
	}
}

func TestMeetingQualityEvaluatorSupportsAllLogicalRelationKinds(t *testing.T) {
	kinds := []string{
		"supported_by",
		"caused_by",
		"limits",
		"resolves",
		"action_for",
		"contradicts",
		"refines",
	}
	matches := map[string]meetingQualityMatch{
		"from": {Item: liveAnalysisItem{ID: "item-from"}, Found: true},
		"to":   {Item: liveAnalysisItem{ID: "item-to"}, Found: true},
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			tree := &liveAnalysisTree{Relations: []liveAnalysisTreeRelation{{
				Source: "item-from", Target: "item-to", Kind: kind,
			}}}
			failures := qualityRelationFailures([]MeetingQualityRelation{{
				From: "from", To: "to", Kind: kind,
			}}, matches, tree)
			if len(failures) != 0 {
				t.Fatalf("relation %s failures=%v", kind, failures)
			}
		})
	}
}

func qualityInvariantTestState() liveAnalysisPayload {
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{{
			ID: "fact-1", Kind: "fact", Severity: "medium",
			Title: "VPN証明書の有効期限を確認した", Body: "有効期限を確認した", Status: "open",
			EvidenceSequenceNos: []int64{1}, CreatedThroughSequenceNo: 1,
		}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
			{ID: "topic-1", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN証明書"},
			{ID: "fact-1", Kind: "fact", ParentID: "topic-1", Label: "VPN証明書の有効期限を確認した"},
		}},
		CoveredThroughSequenceNo: 1,
	}
	rebuildTreeAuditEdges(state.Tree)
	return state
}

func containsStringPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
