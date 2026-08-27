package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

const independentCandidateBatch = `{
		"summary":"予定外の証明書更新管理を検討",
		"currentTopic":"外部接続証明書更新の別管理",
		"items":[
			{"id":"risk-certificate-expiry","kind":"risk","severity":"high","title":"外部接続証明書の期限切れリスク","body":"外部接続証明書は8月末に期限切れとなり、接続が停止する可能性があります","status":"open","evidenceSequenceNos":[41],"evidenceSnippets":["外部接続証明書は8月末に期限切れとなり、接続が停止する可能性があります。"]},
			{"id":"decision-separate-certificate-management","kind":"decision","severity":"medium","title":"証明書更新を別管理にする決定","body":"外部接続証明書の更新は基盤運用から分離し、別管理にすることを決定しました","status":"open","evidenceSequenceNos":[42],"evidenceSnippets":["外部接続証明書の更新は基盤運用から分離し、別管理にすることを決定しました。"]},
			{"id":"issue-certificate-owner-procedure","kind":"issue","severity":"medium","title":"証明書更新の担当者と手順が未確定","body":"外部接続証明書の更新担当者と実施手順はまだ決まっていません","status":"open","evidenceSequenceNos":[43],"evidenceSnippets":["外部接続証明書の更新担当者と実施手順はまだ決まっていません。"]}
		],
		"newTopics":[{"id":"topic-certificate-maintenance","label":"外部接続証明書更新の別管理","description":"期限、管理方法、担当と手順"}],
		"assignments":[
			{"nodeId":"risk-certificate-expiry","parentTopicId":"topic-certificate-maintenance","confidence":0.95,"reason":"同じ予定外論点"},
			{"nodeId":"decision-separate-certificate-management","parentTopicId":"topic-certificate-maintenance","confidence":0.95,"reason":"同じ予定外論点"},
			{"nodeId":"issue-certificate-owner-procedure","parentTopicId":"topic-certificate-maintenance","confidence":0.95,"reason":"同じ予定外論点"}
		]
	}`

func independentCandidateTranscript() map[int64]string {
	return map[int64]string{
		41: "外部接続証明書は8月末に期限切れとなり、接続が停止する可能性があります。",
		42: "外部接続証明書の更新は基盤運用から分離し、別管理にすることを決定しました。",
		43: "外部接続証明書の更新担当者と実施手順はまだ決まっていません。",
	}
}

func mergeIndependentCandidateBatch(t *testing.T) (json.RawMessage, liveAnalysisPayload, *meetingContext, *liveAnalysisTreeMergeStats, string) {
	t.Helper()
	scope := evidenceScopeFromTexts(independentCandidateTranscript(), 41, 42, 43)
	mc := buildMeetingContext(&meetingSessionPreContext{
		Title:  "定例運用会議",
		Agenda: "1. 月次稼働率の報告",
	})
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
		independentCandidateBatch, nil, mc, 1, []int64{41, 42, 43}, scope,
		TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2},
		stats,
	)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayloadWithEvidence() error = %v", err)
	}
	var state liveAnalysisPayload
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	candidateID, _ := canonicalCandidateID("外部接続証明書更新の別管理", "期限、管理方法、担当と手順")
	topicID := stableDynamicTopicID(candidateID)
	return raw, state, mc, stats, topicID
}

func TestDynamicCandidateMaterializesFromIndependentItemsInOneBatch(t *testing.T) {
	_, state, _, stats, topicID := mergeIndependentCandidateBatch(t)
	topic := treeNodeByID(state.Tree, topicID)
	if topic == nil || topic.Origin != topicOriginDynamic {
		t.Fatalf("topic = %+v, want one-batch dynamic topic %q; candidates=%+v nodes=%+v items=%+v decisions=%+v", topic, topicID, state.EmergingTopics, state.Tree.Nodes, state.Items, stats.EmergingDecisions)
	}
	for _, itemID := range []string{
		"risk-certificate-expiry",
		"decision-separate-certificate-management",
		"issue-certificate-owner-procedure",
	} {
		if got := itemTopicID(state.Tree, itemID); got != topicID {
			t.Fatalf("item %q topic = %q, want %q", itemID, got, topicID)
		}
		item := itemByID(state.Items, itemID)
		if item == nil || item.ClassificationStatus != classificationAssigned || item.CandidateTopicID != "" {
			t.Fatalf("item %q = %+v, want assigned with cleared candidate", itemID, item)
		}
	}
	var promoted *emergingDecision
	for i := range stats.EmergingDecisions {
		if stats.EmergingDecisions[i].Decision == emergingPromoted {
			promoted = &stats.EmergingDecisions[i]
		}
	}
	if promoted == nil || promoted.PromotionPath != "single_batch_independent_items" ||
		promoted.CurrentBatchItemCount != 3 ||
		promoted.IndependenceDedupBeforeCount != 3 ||
		promoted.IndependenceDedupAfterCount != 3 ||
		promoted.DistinctEvidenceCount != 3 ||
		promoted.ReparentedItemCount != 3 {
		t.Fatalf("promoted decision = %+v, want fully diagnosed single-batch promotion", promoted)
	}
	if stats.CandidatePromotedSingleBatch != 1 || stats.CandidatePromotedMultiRound != 0 {
		t.Fatalf("promotion stats = %+v, want one single-batch promotion", stats)
	}
}

func TestSingleBatchDynamicTopicSurvivesReplaySnapshotAndFinalization(t *testing.T) {
	raw, _, mc, _, topicID := mergeIndependentCandidateBatch(t)

	// A persisted full snapshot must retain the materialized topic and every
	// parent relation even though transient batch provenance is not serialized.
	reloaded := previousLiveAnalysisState(raw)
	if treeNodeByID(reloaded.Tree, topicID) == nil {
		t.Fatalf("reloaded tree lost topic %q", topicID)
	}
	for _, itemID := range []string{"risk-certificate-expiry", "decision-separate-certificate-management", "issue-certificate-owner-procedure"} {
		if itemTopicID(reloaded.Tree, itemID) != topicID {
			t.Fatalf("reloaded item %q escaped topic %q", itemID, topicID)
		}
	}

	const nextRound = `{
		"summary":"証明書更新の移行計画を追加",
		"currentTopic":"外部接続証明書更新の別管理",
		"items":[
			{"id":"todo-certificate-migration-plan","kind":"todo","severity":"medium","title":"証明書更新の移行計画を作成する","body":"外部接続証明書更新の別管理への移行計画を来週までに作成します","status":"open","evidenceSequenceNos":[44],"evidenceSnippets":["外部接続証明書更新の別管理への移行計画を来週までに作成します。"]}
		],
		"newTopics":[{"id":"topic-certificate-maintenance","label":"外部接続証明書更新の別管理","description":"期限、管理方法、担当と手順"}],
		"assignments":[
			{"nodeId":"todo-certificate-migration-plan","parentTopicId":"topic-certificate-maintenance","confidence":0.95,"reason":"既存の予定外論点"}
		]
	}`
	texts := independentCandidateTranscript()
	texts[44] = "外部接続証明書更新の別管理への移行計画を来週までに作成します。"
	scope := evidenceScopeFromTexts(texts, 44)
	nextRaw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
		nextRound, raw, mc, 2, []int64{44}, scope,
		TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2},
	)
	if err != nil {
		t.Fatalf("merge next round: %v", err)
	}
	nextState := previousLiveAnalysisState(nextRaw)
	if itemTopicID(nextState.Tree, "todo-certificate-migration-plan") != topicID {
		t.Fatalf("next-round item did not reuse topic %q: %+v", topicID, nextState.Tree.Nodes)
	}
	if got := dynamicTopicCountForTest(nextState.Tree); got != 1 {
		t.Fatalf("dynamic topics after next round = %d, want 1", got)
	}

	// Reapplying the same semantic result updates the canonical item/topic
	// instead of creating duplicates.
	replayedRaw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
		nextRound, nextRaw, mc, 3, []int64{44}, scope,
		TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2},
	)
	if err != nil {
		t.Fatalf("replay next round: %v", err)
	}
	replayed := previousLiveAnalysisState(replayedRaw)
	if dynamicTopicCountForTest(replayed.Tree) != 1 ||
		countActiveItemIDForTest(replayed.Items, "todo-certificate-migration-plan") != 1 ||
		itemTopicID(replayed.Tree, "todo-certificate-migration-plan") != topicID {
		t.Fatalf("replay was not idempotent: topics=%+v items=%+v", replayed.Tree.Nodes, replayed.Items)
	}

	segments := make([]domain.TranscriptSegment, 0, len(texts))
	for sequenceNo, text := range texts {
		segments = append(segments, domain.TranscriptSegment{SequenceNo: sequenceNo, Text: text, IsFinal: true})
	}
	finalRaw, finalStats := applyDeterministicFinalTreeRepairs(
		replayedRaw, mc, 4, finalRepairInput{Segments: segments},
	)
	if finalStats.Error != "" || finalStats.IntegrityRejected {
		t.Fatalf("final repair failed: %+v", finalStats)
	}
	finalState := previousLiveAnalysisState(finalRaw)
	if treeNodeByID(finalState.Tree, topicID) == nil {
		t.Fatalf("finalization removed topic %q: %+v", topicID, finalState.Tree.Nodes)
	}
	for _, itemID := range []string{
		"risk-certificate-expiry",
		"decision-separate-certificate-management",
		"issue-certificate-owner-procedure",
		"todo-certificate-migration-plan",
	} {
		if itemTopicID(finalState.Tree, itemID) != topicID {
			t.Fatalf("finalized item %q escaped topic %q", itemID, topicID)
		}
	}
	if !validateTreeIntegrity(finalState.Tree, finalState.Items, mc, finalState.AgendaAnchors).Valid {
		t.Fatalf("finalized state failed integrity: %+v", finalState.Tree)
	}
}

func dynamicTopicCountForTest(tree *liveAnalysisTree) int {
	count := 0
	if tree == nil {
		return count
	}
	for _, node := range tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic {
			count++
		}
	}
	return count
}

func countActiveItemIDForTest(items []liveAnalysisItem, id string) int {
	count := 0
	for _, item := range items {
		if item.ID == id && !item.Inactive && item.MergedIntoID == "" {
			count++
		}
	}
	return count
}

func TestSingleBatchCandidateRejectsNonIndependentEvidence(t *testing.T) {
	const candidateID = "candidate-batch-boundary"
	base := func(id, kind, title, body string, evidence ...int64) liveAnalysisItem {
		return liveAnalysisItem{
			ID: id, Kind: kind, Title: title, Body: body, Status: "open",
			ClassificationStatus:   classificationTentative,
			CandidateTopicID:       candidateID,
			GroundingDecision:      "accepted",
			EvidenceSequenceNos:    evidence,
			observedInCurrentBatch: true,
		}
	}
	tests := []struct {
		name            string
		items           []liveAnalysisItem
		wantReasonParts []string
	}{
		{
			name: "semantic duplicates",
			items: []liveAnalysisItem{
				base("risk-a", "risk", "証明書期限切れによる接続停止", "証明書期限切れで接続が停止する可能性", 1),
				base("risk-b", "risk", "証明書期限切れによる接続停止", "証明書期限切れで接続が停止する可能性", 2),
			},
			wantReasonParts: []string{"semantic_duplicate"},
		},
		{
			name: "split fragments",
			items: func() []liveAnalysisItem {
				left := base("split-a", "risk", "証明書期限切れ", "証明書期限切れの可能性", 1)
				right := base("split-b", "todo", "証明書を更新する", "証明書を更新する必要がある", 2)
				left.semanticSplitFragment = true
				right.semanticSplitFragment = true
				return []liveAnalysisItem{left, right}
			}(),
			wantReasonParts: []string{"semantic_split_fragment"},
		},
		{
			name: "identical evidence",
			items: []liveAnalysisItem{
				base("risk-evidence", "risk", "証明書期限切れリスク", "証明書期限切れで接続停止の可能性", 1),
				base("todo-evidence", "todo", "証明書更新計画を作る", "証明書更新の計画を作成する", 1),
			},
			wantReasonParts: []string{"duplicate_evidence"},
		},
		{
			name: "low information",
			items: []liveAnalysisItem{
				base("issue-low", "issue", "対応", "検討が必要", 1),
				base("todo-low", "todo", "確認", "対応する", 2),
			},
			wantReasonParts: []string{"low_information"},
		},
		{
			name: "unrelated subject",
			items: []liveAnalysisItem{
				base("risk-related", "risk", "証明書期限切れリスク", "証明書期限切れで接続停止の可能性", 1),
				base("todo-unrelated", "todo", "社員食堂の予約", "社員食堂の席を予約する", 2),
			},
			wantReasonParts: []string{"candidate_subject_incoherent"},
		},
		{
			name: "single valid item",
			items: []liveAnalysisItem{
				base("risk-only", "risk", "証明書期限切れリスク", "証明書期限切れで接続停止の可能性", 1),
			},
		},
		{
			name: "kind-only duplicate",
			items: []liveAnalysisItem{
				base("risk-kind", "risk", "証明書期限切れによる接続停止", "証明書期限切れで接続が停止する可能性", 1),
				base("issue-kind", "issue", "証明書期限切れによる接続停止", "証明書期限切れで接続が停止する可能性", 2),
			},
			wantReasonParts: []string{"semantic_duplicate"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topics, remaining, decision := promoteSingleBatchCandidateForTest(t, candidateID, tt.items)
			if treeNodeByID(&liveAnalysisTree{Nodes: mapTreeNodesForTest(topics)}, stableDynamicTopicID(candidateID)) != nil {
				t.Fatalf("non-independent evidence promoted a topic: %+v", topics)
			}
			if len(remaining) != 1 {
				t.Fatalf("candidate was removed: %+v", remaining)
			}
			if decision == nil || decision.Decision != emergingWaitingEvidence ||
				decision.PromotionPath != "" || decision.IndependenceDedupAfterCount >= 2 {
				t.Fatalf("decision = %+v, want waiting with fewer than two independent items", decision)
			}
			for _, part := range tt.wantReasonParts {
				if !strings.Contains(strings.Join(decision.ExcludedEvidence, ","), part) {
					t.Fatalf("excluded evidence = %v, want reason containing %q", decision.ExcludedEvidence, part)
				}
			}
		})
	}
}

func TestDynamicCandidatePreservesMultiRoundPromotionPath(t *testing.T) {
	const candidateID = "candidate-multi-round"
	items := []liveAnalysisItem{
		{
			ID: "risk-round-one", Kind: "risk", Title: "保守期限超過のリスク",
			Body: "保守期限を超えると利用できなくなる可能性", Status: "open",
			ClassificationStatus: classificationTentative, CandidateTopicID: candidateID,
			GroundingDecision: "accepted", EvidenceSequenceNos: []int64{1},
		},
		{
			ID: "todo-round-two", Kind: "todo", Title: "保守更新計画を作成する",
			Body: "保守更新の作業計画を作成する", Status: "open",
			ClassificationStatus: classificationTentative, CandidateTopicID: candidateID,
			GroundingDecision: "accepted", EvidenceSequenceNos: []int64{2},
		},
	}
	candidate := emergingTopicCandidate{
		ID: candidateID, Label: "保守更新管理", Description: "期限と更新作業",
		EvidenceItemIDs: []string{"risk-round-one", "todo-round-two"},
		OriginItemIDs:   []string{"risk-round-one", "todo-round-two"},
		FirstRound:      1, LastRound: 2, RoundCount: 2,
	}
	topics, remaining, decision := promoteCandidateForTest(t, candidate, items, 2)
	if len(remaining) != 0 || topics[stableDynamicTopicID(candidateID)].ID == "" {
		t.Fatalf("multi-round candidate was not promoted: topics=%+v remaining=%+v", topics, remaining)
	}
	if decision == nil || decision.Decision != emergingPromoted ||
		decision.PromotionPath != "multi_round" ||
		decision.IndependenceDedupAfterCount != 0 {
		t.Fatalf("decision = %+v, want unchanged multi-round path", decision)
	}
}

func promoteSingleBatchCandidateForTest(
	t *testing.T,
	candidateID string,
	items []liveAnalysisItem,
) (map[string]liveAnalysisTreeNode, []emergingTopicCandidate, *emergingDecision) {
	t.Helper()
	evidenceIDs := make([]string, 0, len(items))
	for _, item := range items {
		evidenceIDs = append(evidenceIDs, item.ID)
	}
	candidate := emergingTopicCandidate{
		ID: candidateID, Label: "証明書更新管理", Description: "期限と更新対応",
		EvidenceItemIDs: evidenceIDs, OriginItemIDs: append([]string(nil), evidenceIDs...),
		FirstRound: 1, LastRound: 1, RoundCount: 1,
	}
	return promoteCandidateForTest(t, candidate, items, 1)
}

func promoteCandidateForTest(
	t *testing.T,
	candidate emergingTopicCandidate,
	items []liveAnalysisItem,
	round int64,
) (map[string]liveAnalysisTreeNode, []emergingTopicCandidate, *emergingDecision) {
	t.Helper()
	topics := map[string]liveAnalysisTreeNode{
		treeUnclassifiedTopicID: {ID: treeUnclassifiedTopicID, Kind: "topic", Origin: topicOriginSystem},
	}
	details := make(map[string]liveAnalysisTreeNode, len(items))
	parents := make(map[string]string, len(items))
	itemIndexes := make(map[string]int, len(items))
	for i := range items {
		details[items[i].ID] = liveAnalysisTreeNode{ID: items[i].ID, Kind: liveAnalysisTreeNodeKindForItem(items[i].Kind)}
		parents[items[i].ID] = treeUnclassifiedTopicID
		itemIndexes[items[i].ID] = i
	}
	stats := &liveAnalysisTreeMergeStats{}
	dynamicCount := 0
	remaining := promoteEmergingCandidates(promotionContext{
		candidates: []emergingTopicCandidate{candidate},
		parents:    parents,
		details:    details,
		topics:     topics,
		labelIndex: map[string]string{},
		addTopic: func(node liveAnalysisTreeNode) {
			topics[node.ID] = node
		},
		dynamicTopicCount: &dynamicCount,
		itemAt: func(id string) *liveAnalysisItem {
			if at, ok := itemIndexes[id]; ok {
				return &items[at]
			}
			return nil
		},
		round: round,
		cfg: TreeClassificationConfig{
			PromotionMinItems:  2,
			PromotionMinRounds: 2,
			MaxDynamicTopics:   6,
		},
		stats: stats,
	})
	var decision *emergingDecision
	for i := range stats.EmergingDecisions {
		decision = &stats.EmergingDecisions[i]
	}
	return topics, remaining, decision
}

func mapTreeNodesForTest(nodes map[string]liveAnalysisTreeNode) []liveAnalysisTreeNode {
	result := make([]liveAnalysisTreeNode, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, node)
	}
	return result
}
