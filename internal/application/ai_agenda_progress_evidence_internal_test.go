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

func TestFinalAgendaProgressUsesConcreteContinuationUnderShortAgendaTitle(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{
		ID: "agenda-target", Title: "試験導入の対象部署",
		Description:   "導入対象の部署と人数を決める",
		SemanticHints: []string{"対象部署", "営業部", "対象人数"},
		Order:         1, Role: agendaRolePrimary,
	}}}
	segments := agendaProgressEvidenceTestSegments(
		"まず対象部署ですが、営業部から始めるのがよいと思います。",
		"営業部全体ではなく、資料作成が多い人に絞りましょう。",
		"最初は5人くらいを対象にします。",
	)
	state := &liveAnalysisPayload{}
	state.AgendaAnchors = reconcileAgendaAnchors(nil, mc, nil, nil, 20, true)
	state.AgendaProgress = synthesizeAgendaProgressFromAnchors(mc, state.AgendaAnchors, 20)

	finalizeAgendaProgress(state, mc, 20, segments)
	entry := agendaProgressEntryByID(state.AgendaProgress, "agenda-target")
	if entry == nil || entry.ComputedStatus != agendaProgressDiscussed ||
		entry.ProgressSource != agendaProgressEvidenceSourceTranscript ||
		len(entry.EvidenceSequenceNos) < 2 || entry.DiscussionWeight <= 0 {
		t.Fatalf("entry=%+v", entry)
	}
}

func TestFinalAgendaProgressBackfillsTargetDepartmentFromRecordedSessionTranscript(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "試験導入の対象部署", SemanticHints: []string{"対象部署"}, Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "試験導入の期間", SemanticHints: []string{"期間"}, Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "セキュリティ上の懸念", SemanticHints: []string{"セキュリティ"}, Order: 3, Role: agendaRolePrimary},
	}}
	segments := []domain.TranscriptSegment{
		finalSegment(2, "まず対象部署なんですけど、営業部から始めるのがいいと思います。"),
		finalSegment(3, "営業部全体では怖いですね。"),
		finalSegment(4, "最初は5人くらいを対象にしましょう。"),
		finalSegment(5, "試験期間はどれくらいにしますか。"),
		finalSegment(6, "2週間で進めましょう。"),
		finalSegment(7, "開始前にセキュリティ上のルールを確認したいです。"),
		finalSegment(21, "今日決まったのは、営業部の5人を対象に2週間試験すること。開始前にセキュリティ上のルールを確認すること。"),
	}
	state := &liveAnalysisPayload{}
	state.AgendaAnchors = reconcileAgendaAnchors(nil, mc, nil, nil, 21, true)
	state.AgendaProgress = synthesizeAgendaProgressFromAnchors(mc, state.AgendaAnchors, 21)

	finalizeAgendaProgress(state, mc, 21, segments)
	entry := agendaProgressEntryByID(state.AgendaProgress, "agenda-1")
	if entry == nil || entry.ComputedStatus != agendaProgressDiscussed ||
		entry.ProgressSource != agendaProgressEvidenceSourceTranscript ||
		len(entry.EvidenceSequenceNos) < 2 || entry.DiscussionWeight <= 0 {
		t.Fatalf("entry=%+v", entry)
	}
}

func TestAgendaProgressRepairsActiveItemsWithoutAccumulatedWeight(t *testing.T) {
	mc := agendaProgressEvidenceTestContext()
	item := liveAnalysisItem{
		ID: "issue-impact", Kind: "issue", Title: "3階で接続障害が発生",
		Body: "3階の業務端末が社内ネットワークへ接続できない", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: .9,
		EvidenceSequenceNos: []int64{1},
	}
	tree, topicID := agendaProgressEvidenceTestTree(item)
	previous := &agendaProgressState{Entries: []agendaProgressEntry{{
		ID: "agenda-impact", Title: "ネットワーク障害の影響範囲", SourceType: agendaProgressSourceFixed,
		ComputedStatus: agendaProgressNotStarted, MaterializedTopicIDs: []string{topicID},
	}}}
	anchors := []agendaAnchor{{AgendaID: "agenda-impact", Status: agendaStatusDiscussed, MaterializedTopicIDs: []string{topicID}}}
	progress := evaluateAgendaProgress(agendaProgressInputs{
		Previous: previous, MC: mc, Tree: tree, Items: []liveAnalysisItem{item}, Anchors: anchors,
		TreeVersion: 12, Timeline: discourseTimeline{Roles: map[int64]liveEvidenceRole{1: liveEvidencePrimary}},
	})
	entry := agendaProgressEntryByID(progress, "agenda-impact")
	if entry == nil || entry.ComputedStatus == agendaProgressNotStarted ||
		entry.ActiveItemCount != 1 || entry.DiscussionWeight <= 0 || entry.WeightRaw <= 0 {
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

func TestFinalAgendaProgressReparentPreservesIndependentTranscriptSupportAndIsIdempotent(t *testing.T) {
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
	segments := agendaProgressEvidenceTestSegments(
		"利用者への影響を確認したところ、3階の受注業務が停止していました。",
		"2階でも利用者の一部に業務遅延が発生していました。",
		"旧スイッチへ切り戻してトランク設定を修正しました。",
	)
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
	if oldEntry.ComputedStatus != agendaProgressDiscussed ||
		oldEntry.ProgressSource != agendaProgressEvidenceSourceTranscript ||
		oldEntry.DiscussionWeight <= 0 || oldEntry.ActiveItemCount != 0 || len(oldEntry.EvidenceSequenceNos) < 2 {
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
		got.ComputedStatus != agendaProgressDiscussed ||
		got.EffectiveStatus != agendaProgressDiscussed {
		t.Fatalf("manual override lost: %+v", got)
	}
}
