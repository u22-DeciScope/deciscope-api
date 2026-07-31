package application

import (
	"encoding/json"
	"testing"

	"deciscope-core-api/internal/domain"
)

func agendaProgressEvidenceTestSegments(texts ...string) []domain.TranscriptSegment {
	segments := make([]domain.TranscriptSegment, 0, len(texts))
	for index, text := range texts {
		segments = append(segments, domain.TranscriptSegment{
			SequenceNo: int64(index + 1), Text: text, IsFinal: true,
		})
	}
	return segments
}

func agendaProgressEvidenceTestContext() *meetingContext {
	return &meetingContext{Agenda: []agendaItem{{
		ID: "agenda-impact", Title: "ネットワーク障害の影響範囲",
		Description:   "3階の接続障害と2階の通信遅延、利用できない業務を確認する",
		SemanticHints: []string{"3階", "接続障害", "2階", "通信遅延"},
		Order:         1, Role: agendaRolePrimary,
	}}}
}

func agendaProgressEvidenceTestTree(items ...liveAnalysisItem) (*liveAnalysisTree, string) {
	topicID := stableAgendaTopicID("agenda-impact", 0)
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "障害レビュー", Origin: topicOriginSystem},
		{
			ID: topicID, Kind: "topic", ParentID: treeRootNodeID,
			Label: "ネットワーク障害の影響範囲", Origin: topicOriginAgenda,
			AgendaRefs: []string{"agenda-impact"}, Materialized: true,
		},
	}}
	for _, item := range items {
		tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{
			ID: item.ID, Kind: item.Kind, ParentID: topicID,
			Label: item.Title, Description: item.Body,
		})
	}
	rebuildTreeEdges(tree)
	return tree, topicID
}

func TestFinalAgendaProgressUsesActiveCanonicalItems(t *testing.T) {
	mc := agendaProgressEvidenceTestContext()
	segments := agendaProgressEvidenceTestSegments(
		"3階では社内ネットワークへの接続障害が発生しました。",
		"2階では一部端末に通信遅延が発生しました。",
	)
	items := []liveAnalysisItem{
		{
			ID: "issue-third-floor", Kind: "issue", Title: "3階でネットワーク接続障害が発生",
			Body: "3階で社内ネットワークへ接続できない", Status: "open",
			ClassificationStatus: classificationAssigned, AssignmentConfidence: .9,
			EvidenceSequenceNos: []int64{1}, RelatedAgendaIDs: []string{"agenda-impact"},
		},
		{
			ID: "fact-second-floor", Kind: "fact", Title: "2階の一部端末で通信遅延",
			Body:                 "2階の一部端末では通信に遅れがある",
			ClassificationStatus: classificationAssigned, AssignmentConfidence: .8,
			EvidenceSequenceNos: []int64{2}, RelatedAgendaIDs: []string{"agenda-impact"},
		},
	}
	tree, _ := agendaProgressEvidenceTestTree(items...)
	state := &liveAnalysisPayload{Items: items, Tree: tree}
	state.AgendaAnchors = reconcileAgendaAnchors(nil, mc, tree, items, 20, true)
	state.AgendaProgress = synthesizeAgendaProgressFromAnchors(mc, state.AgendaAnchors, 20)

	finalizeAgendaProgress(state, mc, 20, segments)
	entry := agendaProgressEntryByID(state.AgendaProgress, "agenda-impact")
	if entry == nil || entry.ComputedStatus != agendaProgressDiscussed ||
		entry.ProgressSource != agendaProgressEvidenceSourceActiveItem ||
		entry.ActiveItemCount != 2 || len(entry.EvidenceSequenceNos) != 2 ||
		entry.DiscussionWeight <= 0 || entry.DiscussionVolume != 2 {
		t.Fatalf("entry=%+v", entry)
	}
}

func TestFinalAgendaProgressUsesMultiplePrimaryTranscriptStatementsWithoutItem(t *testing.T) {
	mc := agendaProgressEvidenceTestContext()
	segments := agendaProgressEvidenceTestSegments(
		"3階では社内ネットワークへの接続障害が発生しました。",
		"2階では複数端末に通信遅延が発生しました。",
	)
	tree, _ := agendaProgressEvidenceTestTree()
	state := &liveAnalysisPayload{Tree: tree}
	state.AgendaAnchors = reconcileAgendaAnchors(nil, mc, tree, nil, 20, true)
	state.AgendaProgress = synthesizeAgendaProgressFromAnchors(mc, state.AgendaAnchors, 20)

	finalizeAgendaProgress(state, mc, 20, segments)
	entry := agendaProgressEntryByID(state.AgendaProgress, "agenda-impact")
	if entry == nil || entry.ComputedStatus != agendaProgressDiscussed ||
		entry.ProgressSource != agendaProgressEvidenceSourceTranscript ||
		entry.ActiveItemCount != 0 || len(entry.EvidenceSequenceNos) != 2 ||
		entry.SemanticCoverageScore < agendaReconciliationMinScore ||
		entry.ProgressReason != "meaningful_discussion_without_persisted_item" ||
		entry.DiscussionWeight <= 0 {
		t.Fatalf("entry=%+v", entry)
	}
}

func TestFinalAgendaProgressRejectsMaterializationAndRecapOnlySupport(t *testing.T) {
	mc := agendaProgressEvidenceTestContext()
	tree, _ := agendaProgressEvidenceTestTree()
	tests := []struct {
		name     string
		segments []domain.TranscriptSegment
	}{
		{name: "materialized only", segments: []domain.TranscriptSegment{}},
		{name: "recap only", segments: agendaProgressEvidenceTestSegments(
			"まとめると。",
			"3階では社内ネットワークへの接続障害が発生しました。",
			"2階では複数端末に通信遅延が発生しました。",
		)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := &liveAnalysisPayload{Tree: tree}
			state.AgendaAnchors = reconcileAgendaAnchors(nil, mc, tree, nil, 20, true)
			state.AgendaProgress = synthesizeAgendaProgressFromAnchors(mc, state.AgendaAnchors, 20)
			finalizeAgendaProgress(state, mc, 20, test.segments)
			entry := agendaProgressEntryByID(state.AgendaProgress, "agenda-impact")
			if entry == nil || entry.ComputedStatus != agendaProgressNotStarted ||
				entry.ProgressSource != agendaProgressEvidenceSourceNone ||
				entry.ActiveItemCount != 0 || len(entry.EvidenceSequenceNos) != 0 ||
				entry.DiscussionWeight != 0 {
				t.Fatalf("entry=%+v", entry)
			}
		})
	}
}

func TestFinalAgendaProgressReparentRecomputesWeightAndIsIdempotent(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{
			ID: "agenda-old", Title: "障害影響の確認", Description: "利用者影響を確認する",
			SemanticHints: []string{"利用者", "影響"}, Order: 1, Role: agendaRolePrimary,
		},
		{
			ID: "agenda-new", Title: "復旧対応の確認", Description: "切り戻しと設定修正を確認する",
			SemanticHints: []string{"切り戻し", "設定修正"}, Order: 2, Role: agendaRolePrimary,
		},
	}}
	oldTopicID := stableAgendaTopicID("agenda-old", 0)
	newTopicID := stableAgendaTopicID("agenda-new", 0)
	item := liveAnalysisItem{
		ID: "fact-recovery", Kind: "fact", Title: "旧スイッチへ切り戻して設定修正を完了",
		Body:                 "旧スイッチへ切り戻し、トランク設定を修正した",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: .95,
		EvidenceSequenceNos: []int64{1}, RelatedAgendaIDs: []string{"agenda-new"},
	}
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Origin: topicOriginSystem},
		{
			ID: oldTopicID, Kind: "topic", ParentID: treeRootNodeID,
			Label: "障害影響の確認", Origin: topicOriginAgenda,
			AgendaRefs: []string{"agenda-old"}, Materialized: true,
		},
		{
			ID: newTopicID, Kind: "topic", ParentID: treeRootNodeID,
			Label: "復旧対応の確認", Origin: topicOriginAgenda,
			AgendaRefs: []string{"agenda-new"}, Materialized: true,
		},
		{
			ID: item.ID, Kind: item.Kind, ParentID: newTopicID,
			Label: item.Title, Description: item.Body,
		},
	}}
	rebuildTreeEdges(tree)
	segments := agendaProgressEvidenceTestSegments("旧スイッチへ切り戻してトランク設定を修正しました。")
	state := &liveAnalysisPayload{Tree: tree, Items: []liveAnalysisItem{item}}
	state.AgendaAnchors = reconcileAgendaAnchors(nil, mc, tree, state.Items, 21, true)
	state.AgendaProgress = synthesizeAgendaProgressFromAnchors(mc, state.AgendaAnchors, 21)
	oldEntry := agendaProgressEntryByID(state.AgendaProgress, "agenda-old")
	oldEntry.ComputedStatus = agendaProgressDiscussed
	oldEntry.WeightRaw = 9
	oldEntry.DiscussionWeight = 1

	finalizeAgendaProgress(state, mc, 21, segments)
	oldEntry = agendaProgressEntryByID(state.AgendaProgress, "agenda-old")
	newEntry := agendaProgressEntryByID(state.AgendaProgress, "agenda-new")
	if oldEntry.ComputedStatus != agendaProgressNotStarted ||
		oldEntry.ProgressSource != agendaProgressEvidenceSourceNone ||
		oldEntry.DiscussionWeight != 0 || oldEntry.ActiveItemCount != 0 {
		t.Fatalf("old entry=%+v", oldEntry)
	}
	if newEntry.ComputedStatus != agendaProgressDiscussed ||
		newEntry.ProgressSource != agendaProgressEvidenceSourceActiveItem ||
		newEntry.ActiveItemCount != 1 || newEntry.DiscussionWeight <= 0 {
		t.Fatalf("new entry=%+v", newEntry)
	}

	first, err := json.Marshal(state.AgendaProgress)
	if err != nil {
		t.Fatal(err)
	}
	finalizeAgendaProgress(state, mc, 21, segments)
	second, err := json.Marshal(state.AgendaProgress)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("progress is not idempotent:\nfirst=%s\nsecond=%s", first, second)
	}

	effective := applyAgendaProgressOverrides(state.AgendaProgress, &AgendaProgressOverrides{
		StatusOverrides: map[string]string{"agenda-old": agendaProgressDiscussed},
	})
	if got := agendaProgressEntryByID(effective, "agenda-old"); got == nil ||
		got.ComputedStatus != agendaProgressNotStarted ||
		got.EffectiveStatus != agendaProgressDiscussed {
		t.Fatalf("manual override lost: %+v", got)
	}
}
