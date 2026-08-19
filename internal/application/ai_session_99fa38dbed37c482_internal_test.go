package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func finalQualityScenarioState(t *testing.T, scenarioID string) (liveAnalysisPayload, liveAnalysisPayload, finalRepairStats) {
	t.Helper()
	suite := loadDeterministicMeetingQualitySuite(t)
	var scenario *MeetingQualityScenario
	for index := range suite.Scenarios {
		if suite.Scenarios[index].ID == scenarioID {
			scenario = &suite.Scenarios[index]
			break
		}
	}
	if scenario == nil {
		t.Fatalf("scenario %q not found", scenarioID)
	}
	segments := qualityDomainSegments(*scenario)
	bySequence := make(map[int64]string, len(segments))
	segmentBySequence := make(map[int64]int, len(segments))
	for index, segment := range segments {
		bySequence[segment.SequenceNo] = segment.Text
		segmentBySequence[segment.SequenceNo] = index
	}
	context := qualityMeetingContext(scenario.MeetingContext)
	raw := append(json.RawMessage(nil), scenario.SeedPayload...)
	cfg := TreeClassificationConfig{
		AgendaAssignmentThreshold: scenario.Classification.AgendaAssignmentThreshold,
		PromotionMinItems:         scenario.Classification.PromotionMinItems,
		PromotionMinRounds:        scenario.Classification.PromotionMinRounds,
		MaxDynamicTopics:          scenario.Classification.MaxDynamicTopics,
	}
	for roundIndex, round := range scenario.Rounds {
		scope := newLiveEvidenceScope()
		var roundSegments []domain.TranscriptSegment
		maxSequence := int64(0)
		for _, sequenceNo := range round.SequenceNos {
			scope.CurrentRound[sequenceNo] = struct{}{}
			if at, ok := segmentBySequence[sequenceNo]; ok {
				roundSegments = append(roundSegments, segments[at])
			}
			if sequenceNo > maxSequence {
				maxSequence = sequenceNo
			}
		}
		for _, segment := range segments {
			if segment.SequenceNo > maxSequence {
				continue
			}
			scope.Allowed[segment.SequenceNo] = struct{}{}
			scope.TranscriptText[segment.SequenceNo] = bySequence[segment.SequenceNo]
			scope.Segments[segment.SequenceNo] = segment
			scope.CoveredThrough = segment.SequenceNo
		}
		previous := previousLiveAnalysisState(raw)
		classifyLiveRoundInputs(&scope, previous, roundSegments)
		var err error
		raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(
			string(round.FixedAIResponse), raw, context, int64(roundIndex+1),
			round.SequenceNos, scope, cfg,
		)
		if err != nil {
			t.Fatalf("round %d: %v", roundIndex+1, err)
		}
	}
	live := previousLiveAnalysisState(raw)
	repaired, stats := applyDeterministicFinalTreeRepairs(
		raw, context, int64(len(scenario.Rounds)+1),
		finalRepairInput{Segments: segments},
	)
	if stats.Error != "" || stats.IntegrityRejected {
		t.Fatalf("final repair: %+v", stats)
	}
	return live, previousLiveAnalysisState(repaired), stats
}

func TestSession99FARecoveryFactsRemainAtomic(t *testing.T) {
	live, state, _ := finalQualityScenarioState(t, "recovery-atomic-fact-split")
	want := map[string]bool{"切り戻": false, "トランク設定を修正": false, "接続が正常": false}
	for _, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" || item.Kind != "fact" {
			continue
		}
		for fragment := range want {
			if strings.Contains(item.Title+" "+item.Body, fragment) {
				want[fragment] = true
			}
		}
	}
	for fragment, found := range want {
		if !found {
			t.Errorf("missing recovery fact %q: live=%+v final=%+v", fragment, live.Items, state.Items)
		}
	}
}

func TestSession99FASupersededItemCannotResolve(t *testing.T) {
	live, state, _ := finalQualityScenarioState(t, "correction-superseded-resolution-guard")
	var old *liveAnalysisItem
	for index := range state.Items {
		if state.Items[index].ID == "old-access" {
			old = &state.Items[index]
			break
		}
	}
	if old == nil || !old.Inactive || old.MergedIntoID == "" || old.Status == "resolved" {
		t.Fatalf("superseded item lifecycle = %+v", old)
	}
	facts := 0
	for _, item := range state.Items {
		if !item.Inactive && item.MergedIntoID == "" && item.Kind == "fact" && containsInt64(item.EvidenceSequenceNos, 2) {
			facts++
		}
	}
	if facts < 2 {
		t.Fatalf("corrected atomic facts=%d, live=%+v final=%+v", facts, live.Items, state.Items)
	}
}

func TestSession99FACorrectionReplacementSplitsTrunkAndVLANFacts(t *testing.T) {
	scope := newLiveEvidenceScope()
	scope.Allowed[2] = struct{}{}
	scope.CurrentRound[2] = struct{}{}
	scope.CoveredThrough = 2
	scope.TranscriptText[2] = "正確には、トランク設定はあり、許可VLAN一覧からVLAN30が漏れていました。"
	scope.Segments[2] = domain.TranscriptSegment{SequenceNo: 2, Text: scope.TranscriptText[2], IsFinal: true}
	timeline := classifyDiscourseTimeline(scope)
	replacements := correctionReplacementStatements(2, scope, timeline)
	if len(replacements) != 2 {
		t.Fatalf("replacement facts=%+v", replacements)
	}
}

func TestSession99FAFinalRelationsPersistWithMetadata(t *testing.T) {
	_, state, _ := finalQualityScenarioState(t, "final-relation-metadata-persistence")
	want := map[string]bool{itemRelationSupportedBy: false, itemRelationLimits: false, itemRelationActionFor: false}
	for _, relation := range state.Tree.Relations {
		if _, ok := want[relation.Kind]; ok && relation.ID != "" && relation.Origin != "" &&
			relation.Status == "active" && relation.Confidence > 0 && len(relation.EvidenceSequenceNos) > 0 {
			want[relation.Kind] = true
		}
	}
	for kind, found := range want {
		if !found {
			t.Errorf("missing relation %s with metadata: %+v; items=%+v", kind, state.Tree.Relations, state.Items)
		}
	}
}

func TestSession99FAFinalSynthesizedParentPrefersSharedEvidence(t *testing.T) {
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{
			{
				ID: "monitoring-todo", Kind: "todo", Title: "監視項目を追加",
				Body: "監視間隔と通知条件を検討する", EvidenceSequenceNos: []int64{18, 19},
			},
			{
				ID: "vpn-risk", Kind: "risk", Title: "VPN接続リスク",
				Body: "証明書期限により接続できない可能性", EvidenceSequenceNos: []int64{21},
			},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "root"},
			{ID: "monitoring-topic", Kind: "topic", ParentID: treeRootNodeID, Label: "監視運用"},
			{ID: "vpn-topic", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN証明書"},
			{ID: "monitoring-todo", Kind: "todo", ParentID: "monitoring-topic", Label: "監視項目を追加"},
			{ID: "vpn-risk", Kind: "risk", ParentID: "vpn-topic", Label: "VPN接続リスク"},
		}},
	}
	alertRisk := liveAnalysisItem{
		ID: "alert-risk", Kind: "risk", Title: "アラート過多リスク",
		Body:                "監査対象を増やすとアラートが多くなりすぎる可能性",
		EvidenceSequenceNos: []int64{19},
	}
	addOrUpdateFinalSynthesizedItems(&state, []liveAnalysisItem{alertRisk}, 2)

	if node := liveTreeNodeByID(state.Tree, alertRisk.ID); node == nil || node.ParentID != "monitoring-topic" {
		t.Fatalf("synthesized risk parent=%+v, want monitoring-topic", node)
	}
}

func TestSession99FAMonitoringAndVPNRemainInSeparateTopics(t *testing.T) {
	if overlap := specificSubjectOverlapLength(
		"監視対象拡大によるアラート過多 監視対象を増やすとアラートが増えすぎる可能性がある",
		"VPN証明書失効によるリモート接続不能リスク VPN証明書は来月末に期限切れとなり、未更新の場合はリモート接続できなくなる可能性がある",
	); overlap >= 2 {
		t.Fatalf("monitoring/VPN concrete subject overlap=%d, want <2: %q / %q", overlap,
			specificSubjectText("監視対象拡大によるアラート過多 監視対象を増やすとアラートが増えすぎる可能性がある"),
			specificSubjectText("VPN証明書失効によるリモート接続不能リスク VPN証明書は来月末に期限切れとなり、未更新の場合はリモート接続できなくなる可能性がある"))
	}
	if foldedInto := semanticExistingTopicID(
		"VPN証明書更新", "今回の障害とは別の対応",
		map[string]liveAnalysisTreeNode{
			"topic-monitor-topic": {ID: "topic-monitor-topic", Kind: "topic", Label: "監視運用", Description: "アラート過多"},
		},
	); foldedInto != "" {
		t.Fatalf("unrelated VPN topic folded into %q", foldedInto)
	}
	if foldedInto := semanticExistingTopicID(
		"VPN証明書失効によるリモート接続不能リスク",
		"VPN証明書は来月末に期限切れとなり、未更新の場合はリモート接続できなくなる可能性がある",
		map[string]liveAnalysisTreeNode{
			"topic-monitor-topic": {ID: "topic-monitor-topic", Kind: "topic", Label: "監視運用", Description: "アラート過多"},
		},
	); foldedInto != "" {
		t.Fatalf("rewritten unrelated VPN topic folded into %q", foldedInto)
	}
	live, final, _ := finalQualityScenarioState(t, "unrelated-monitoring-vpn-candidates")
	itemForEvidence := func(state liveAnalysisPayload, sequenceNo int64) *liveAnalysisItem {
		for index := range state.Items {
			item := &state.Items[index]
			if !item.Inactive && item.MergedIntoID == "" && item.Kind == "risk" &&
				containsInt64(item.EvidenceSequenceNos, sequenceNo) {
				return item
			}
		}
		return nil
	}
	monitoring, vpn := itemForEvidence(final, 1), itemForEvidence(final, 2)
	if monitoring == nil || vpn == nil {
		t.Fatalf("final risks missing: monitoring=%+v vpn=%+v items=%+v", monitoring, vpn, final.Items)
	}
	monitoringTopic, vpnTopic := treeItemTopic(final.Tree, monitoring.ID), treeItemTopic(final.Tree, vpn.ID)
	if monitoringTopic == "" || vpnTopic == "" || monitoringTopic == vpnTopic {
		t.Fatalf("monitoring/vpn topic=%q/%q; liveTopics=%q/%q liveCandidates=%+v finalCandidates=%+v finalTree=%+v",
			monitoringTopic, vpnTopic,
			treeItemTopic(live.Tree, itemForEvidence(live, 1).ID),
			treeItemTopic(live.Tree, itemForEvidence(live, 2).ID),
			live.EmergingTopics, final.EmergingTopics, final.Tree)
	}
	if vpn.CandidateTopicID == "" || vpn.CandidateTopicID == monitoring.CandidateTopicID {
		t.Fatalf("VPN candidate=%q monitoring candidate=%q; want separate VPN candidate",
			vpn.CandidateTopicID, monitoring.CandidateTopicID)
	}
	foundVPNCandidate := false
	for _, candidate := range final.EmergingTopics {
		if candidate.ID == vpn.CandidateTopicID && strings.Contains(candidate.Label, "VPN") &&
			containsExactString(candidate.EvidenceItemIDs, vpn.ID) {
			foundVPNCandidate = true
			break
		}
	}
	if !foundVPNCandidate {
		t.Fatalf("VPN candidate %q not preserved with VPN evidence: %+v", vpn.CandidateTopicID, final.EmergingTopics)
	}
}
