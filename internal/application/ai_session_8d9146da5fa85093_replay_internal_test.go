package application

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestSession8DInitialCoverageRetriesOnlyWithNextNormalRound(t *testing.T) {
	initial := liveAnalysisPayload{
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Origin: topicOriginSystem},
		}},
	}
	raw, _ := json.Marshal(initial)
	segments := []domain.TranscriptSegment{
		{CallID: "call", SequenceNo: 1, IsFinal: true, Text: "本日は障害の影響範囲から確認します。"},
		{CallID: "call", SequenceNo: 2, IsFinal: true, Text: "影響は3階全体と2階の一部です。"},
	}
	covered, decisions, err := addLiveAnalysisCoverageWithResult(
		raw, segments, "model_returned_no_items",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 {
		t.Fatalf("coverage decisions=%+v", decisions)
	}
	for _, decision := range decisions {
		if decision.MeaningfullyCovered || !decision.RetryEligible ||
			decision.Reason != "model_returned_no_items" {
			t.Fatalf("decision=%+v, want bounded unreflected retry", decision)
		}
	}
	if got := filterAlreadyAnalyzedSegments(segments, covered); len(got) != 0 {
		t.Fatalf("same round caused an immediate retry: %+v", got)
	}

	next := append(append([]domain.TranscriptSegment(nil), segments...),
		domain.TranscriptSegment{
			CallID: "call", SequenceNo: 3, IsFinal: true,
			Text: "有線端末と無線端末の双方で影響を確認しました。",
		})
	recovered := filterAlreadyAnalyzedSegments(next, covered)
	if got := sequenceNosOfSegments(recovered); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("recovered sequence=%v, want old unreflected plus new final", got)
	}

	state := previousLiveAnalysisState(covered)
	state.Items = []liveAnalysisItem{{
		ID: "fact-impact", Kind: "fact", Status: "open",
		Title: "影響範囲", Body: "3階全体と2階の一部",
		EvidenceSequenceNos: []int64{2, 3},
	}}
	secondRaw, _ := json.Marshal(state)
	second, secondDecisions, err := addLiveAnalysisCoverageWithResult(
		secondRaw, recovered, "grounding_rejected",
	)
	if err != nil {
		t.Fatal(err)
	}
	secondState := previousLiveAnalysisState(second)
	if secondState.MeaningfullyCoveredThroughSequenceNo != 3 {
		t.Fatalf("meaningful through=%d", secondState.MeaningfullyCoveredThroughSequenceNo)
	}
	if len(filterAlreadyAnalyzedSegments(next, second)) != 0 {
		t.Fatal("bounded retry remained eligible after its second processing")
	}
	foundSeq2 := false
	for _, decision := range secondDecisions {
		if decision.SequenceNo == 2 {
			foundSeq2 = decision.MeaningfullyCovered && !decision.RetryEligible
		}
	}
	if !foundSeq2 {
		t.Fatalf("sequence 2 was not meaningfully recovered: %+v", secondDecisions)
	}
}

func TestSession8DGroundingRejectionKeepsMeaningfulCoverageSeparate(t *testing.T) {
	raw := json.RawMessage(`{"items":[],"tree":{"nodes":[{"id":"root","kind":"topic"}],"edges":[]}}`)
	segment := domain.TranscriptSegment{
		CallID: "call", SequenceNo: 3, IsFinal: true,
		Text: "2階西側でも通信できない端末がありました。",
	}
	encoded, decisions, err := addLiveAnalysisCoverageWithResult(
		raw, []domain.TranscriptSegment{segment}, "grounding_rejected",
	)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(encoded)
	if state.CoveredThroughSequenceNo != 3 ||
		state.MeaningfullyCoveredThroughSequenceNo != 0 ||
		len(decisions) != 1 || decisions[0].Reason != "grounding_rejected" ||
		!decisions[0].RetryEligible {
		t.Fatalf("state=%+v decisions=%+v", state, decisions)
	}
}

func TestSession8DUnassignedFactGetsPlannedAgendaOrPendingCandidate(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-impact", Title: "障害の影響範囲と発生時刻",
			Description:   "影響した場所と端末を確認する",
			SemanticHints: []string{"影響範囲", "通信できない端末"}, Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-prevention", Title: "再発防止策",
			Description: "予防策を決める", SemanticHints: []string{"監視", "手順"}, Order: 2, Role: agendaRolePrimary},
	}}
	item := liveAnalysisItem{
		ID: "fact-impact", Kind: "fact", Severity: "medium", Status: "open",
		Title: "名護市社3階で通信不可", Body: "3階の端末で通信できない",
		EvidenceSequenceNos: []int64{2},
	}
	scope := liveEvidenceScope{
		Allowed: map[int64]struct{}{2: {}}, CurrentRound: map[int64]struct{}{2: {}},
		TranscriptText: map[int64]string{2: "名護市社の3階で通信できない端末がありました。"},
		Segments: map[int64]domain.TranscriptSegment{
			2: {SequenceNo: 2, IsFinal: true, Text: "名護市社の3階で通信できない端末がありました。"},
		},
	}
	timeline := classifyDiscourseTimeline(scope)
	assignments := reconcileDynamicCandidateAssignments(
		nil, nil, liveAnalysisPayload{}, []liveAnalysisItem{item}, []liveAnalysisItem{item},
		mc, nil, timeline, scope, &liveAnalysisTreeMergeStats{},
	)
	if len(assignments) != 1 || assignments[0].ParentTopicID != "agenda-impact" {
		t.Fatalf("assignments=%+v, want planned impact agenda (accepted or pending)", assignments)
	}
	if assignments[0].ParentTopicID == treeUnclassifiedTopicID {
		t.Fatal("ASR variance was permanently classified as an additional topic")
	}
}

func TestSession8DLowConfidenceAgendaMatchStaysPending(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{
			ID: "agenda-impact", Title: "ネットワーク障害影響調査",
			Description: "影響拠点と通信停止端末を特定する", Order: 1, Role: agendaRolePrimary,
		},
		{
			ID: "agenda-prevention", Title: "再発防止手順",
			Description: "設定手順と監視方法を改善する", Order: 2, Role: agendaRolePrimary,
		},
	}}
	item := liveAnalysisItem{
		ID: "fact-impact", Kind: "fact", Severity: "medium", Status: "open",
		Title: "拠点へのネット影響", Body: "影響拠点と通信停止端末を確認",
		EvidenceSequenceNos: []int64{2},
	}
	scope := testSession8DScope(map[int64]string{
		2: "ネットワークの影響があり、影響拠点と通信停止端末を確認しました。",
	})
	stats := &liveAnalysisTreeMergeStats{}
	assignments := reconcileDynamicCandidateAssignments(
		nil, nil, liveAnalysisPayload{}, []liveAnalysisItem{item}, []liveAnalysisItem{item},
		mc, nil, classifyDiscourseTimeline(scope), scope, stats,
	)
	if len(assignments) != 1 ||
		assignments[0].ParentTopicID != "agenda-impact" ||
		assignments[0].Reason != "planned_agenda_match_pending" ||
		assignments[0].Confidence < agendaReconciliationPendingMinScore ||
		assignments[0].Confidence >= agendaReconciliationMinScore {
		t.Fatalf("assignments=%+v decisions=%+v, want a low-confidence planned-agenda pending assignment", assignments, stats.AgendaReconciliations)
	}
}

func TestSession8DSequence15SplitsOwnersAndIsIdempotent(t *testing.T) {
	text := "私は今週の金曜日までにスイッチ交換用のチェックリスト案を作成します佐藤さんには、来週火曜日までに今回のスイッチ設定と標準設定との差分を確認してもらいます。監視項目の追加も検討したいです。"
	scope := testSession8DScope(map[int64]string{15: text})
	combined := liveAnalysisItem{
		ID: "todo-combined", Kind: "todo", Severity: "medium", Status: "open",
		Title: "チェックリストと設定差分を確認する", Body: text,
		EvidenceSequenceNos: []int64{15}, EvidenceSnippets: []string{text},
	}
	items, _ := splitMultiAssignmentTodoDiff(
		[]liveAnalysisItem{combined}, nil, scope, &liveAnalysisTreeMergeStats{},
	)
	if len(items) != 2 {
		t.Fatalf("items=%+v, want two Todo propositions", items)
	}
	left := items[0].Title + " " + items[0].Body
	right := items[1].Title + " " + items[1].Body
	if !strings.Contains(left, "チェックリスト") ||
		!strings.Contains(right, "設定") ||
		!distinctTodoAssignments(items[0], items[1]) {
		t.Fatalf("split lost assignment identity: left=%q right=%q", left, right)
	}
	for _, item := range items {
		if !reflect.DeepEqual(item.EvidenceSequenceNos, []int64{15}) ||
			!actionDeadlinePresent(item.Body) {
			t.Fatalf("item=%+v, want same evidence and own deadline", item)
		}
	}

	state := &liveAnalysisPayload{
		Items: []liveAnalysisItem{combined},
		Tree:  testSession8DTree(combined),
	}
	splitPersistedMultiAssignmentTodos(state, scope, &liveAnalysisTreeMergeStats{})
	first, _ := json.Marshal(state)
	splitPersistedMultiAssignmentTodos(state, scope, &liveAnalysisTreeMergeStats{})
	second, _ := json.Marshal(state)
	if string(first) != string(second) || len(state.Items) != 2 {
		t.Fatalf("Todo repair not idempotent\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestSession8DFinalReplayProducesThreeOwnedDeadlineTodos(t *testing.T) {
	sequence15 := "私は今週の金曜日までにスイッチ交換用のチェックリスト案を作成します佐藤さんには、来週火曜日までに今回のスイッチ設定と標準設定との差分を確認してもらいます。監視項目の追加も検討したいです。"
	sequence19 := "高橋さんには今週中にVPN証明書の更新手順と更新日を確認してもらいます。"
	segments := []domain.TranscriptSegment{
		{
			CallID: "call", SequenceNo: 15, IsFinal: true, Text: sequence15,
			SpeakerName: "山下",
		},
		{
			CallID: "call", SequenceNo: 19, IsFinal: true, Text: sequence19,
			SpeakerName: "山下",
		},
	}
	combined := liveAnalysisItem{
		ID: "todo-combined", Kind: "todo", Severity: "medium", Status: "open",
		Title: "チェックリストと設定差分を確認する", Body: sequence15,
		EvidenceSequenceNos: []int64{15}, EvidenceSnippets: []string{sequence15},
	}
	state := &liveAnalysisPayload{
		Items: []liveAnalysisItem{combined},
		Tree:  testSession8DTree(combined),
	}

	repairFinalItemKinds(state, segments, nil, 16, &finalRepairStats{})
	first, _ := json.Marshal(state)
	repairFinalItemKinds(state, segments, nil, 16, &finalRepairStats{})
	second, _ := json.Marshal(state)
	if string(first) != string(second) {
		t.Fatalf("final replay is not idempotent\nfirst=%s\nsecond=%s", first, second)
	}

	todos := make([]liveAnalysisItem, 0, 3)
	for _, item := range state.Items {
		if item.Kind == "todo" && !item.Inactive && item.MergedIntoID == "" {
			todos = append(todos, item)
		}
	}
	if len(todos) != 3 {
		t.Fatalf("active Todos=%d, want 3: %+v", len(todos), todos)
	}
	evidenceCount := map[int64]int{}
	for _, item := range todos {
		features := inferItemSemanticFeatures(item, liveEvidenceScope{})
		if !features.OwnerPresent || !features.DeadlinePresent ||
			!features.DecisionOrCommitment || !actionDeadlinePresent(item.Title+" "+item.Body) {
			t.Fatalf("Todo lost owner/action/deadline identity: item=%+v features=%+v", item, features)
		}
		for _, sequenceNo := range item.EvidenceSequenceNos {
			evidenceCount[sequenceNo]++
		}
	}
	if evidenceCount[15] != 2 || evidenceCount[19] != 1 {
		t.Fatalf("Todo evidence distribution=%v, want sequence15=2 sequence19=1", evidenceCount)
	}
}

func TestSession8DTodoDedupRequiresOwnerIdentityAndMergesSameOwnerParaphrase(t *testing.T) {
	previous := liveAnalysisItem{
		ID: "todo-checklist", Kind: "todo", Severity: "medium", Status: "open",
		Title:               "田中さんが金曜日までに交換用チェックリスト案を作成する",
		Body:                "田中さんが金曜日までに交換用チェックリスト案を作成します",
		EvidenceSequenceNos: []int64{15},
	}

	differentOwnerScope := testSession8DScope(map[int64]string{
		15: "鈴木さんが金曜日までに交換用チェックリスト案を作成します。",
	})
	differentOwner := synthesizeStrongTodoItems(
		[]liveAnalysisItem{previous}, nil, differentOwnerScope,
		classifyDiscourseTimeline(differentOwnerScope), &liveAnalysisTreeMergeStats{},
	)
	if len(differentOwner) != 1 ||
		!distinctTodoAssignments(previous, differentOwner[0]) {
		t.Fatalf("different owner was deduplicated: previous=%+v synthesized=%+v", previous, differentOwner)
	}

	sameOwnerScope := testSession8DScope(map[int64]string{
		15: "田中さんは今週中にスイッチ交換用のチェックリストを作成します。",
	})
	stats := &liveAnalysisTreeMergeStats{}
	sameOwner := synthesizeStrongTodoItems(
		[]liveAnalysisItem{previous}, nil, sameOwnerScope,
		classifyDiscourseTimeline(sameOwnerScope), stats,
	)
	if len(sameOwner) != 0 || stats.StrongTodoDuplicatesSuppressed != 1 {
		t.Fatalf("same-owner paraphrase was not deduplicated: items=%+v stats=%+v", sameOwner, stats)
	}
}

func TestSession8DCorrectionRelationLocksSequence7AcrossRunsAndKindChange(t *testing.T) {
	factA := liveAnalysisItem{
		ID: "fact-a", Kind: "fact", Status: "open",
		Title: "VLAN20と30の通信が不安定", Body: "VLAN20と30の通信が不安定でした",
		EvidenceSequenceNos: []int64{6},
	}
	factB := liveAnalysisItem{
		ID: "fact-b", Kind: "fact", Status: "open",
		Title: "上位ポートはアクセスポート", Body: "トランクではなくアクセスポートでした",
		EvidenceSequenceNos: []int64{7},
	}
	replacement := liveAnalysisItem{
		ID: "fact-corrected", Kind: "fact", Status: "open",
		Title:               "トランク設定でVLAN30が許可一覧から欠落",
		Body:                "トランク設定はあり、VLAN30だけが許可一覧から漏れていました",
		EvidenceSequenceNos: []int64{8},
	}
	state := &liveAnalysisPayload{
		Items: []liveAnalysisItem{factA, factB, replacement},
		Tree:  testSession8DTree(factA, factB, replacement),
	}
	scope := testSession8DScope(map[int64]string{
		6: "VLAN20と30の通信が不安定でした。",
		7: "上位スイッチへ接続するポートが、トランクポートではなくアクセスポートになっていました。",
		8: "正確には完全なアクセスポート設定ではなく、トランク設定自体は入っていましたが、許可するVLAN一覧からVLAN30が漏れていました。",
	})
	timeline := classifyDiscourseTimeline(scope)
	stats := &liveAnalysisTreeMergeStats{}
	repairCorrectionSupersessions(state, scope, timeline, 6, stats)
	assertSession8DCorrectionState(t, state)
	if len(state.CorrectionRelations) != 1 ||
		state.CorrectionRelations[0].TargetSequenceNo != 7 ||
		!state.CorrectionRelations[0].Locked {
		t.Fatalf("relations=%+v", state.CorrectionRelations)
	}

	// Simulate a later validator migration. The stable item ID, relation and
	// tombstone must still prevent resurrection or drift to sequence 6.
	state.Items[1].Kind = "issue"
	first, _ := json.Marshal(state)
	stats2 := &liveAnalysisTreeMergeStats{}
	repairCorrectionSupersessions(state, scope, timeline, 7, stats2)
	assertSession8DCorrectionState(t, state)
	second, _ := json.Marshal(state)
	repairCorrectionSupersessions(state, scope, timeline, 8, &liveAnalysisTreeMergeStats{})
	third, _ := json.Marshal(state)
	if string(second) != string(third) {
		t.Fatalf("second repair changed payload\nsecond=%s\nthird=%s", second, third)
	}
	if string(first) != string(second) {
		t.Fatalf("locked relation changed canonical state on next run\nfirst=%s\nsecond=%s", first, second)
	}
	blocked := false
	for _, decision := range stats2.CorrectionDecisions {
		if (decision.Decision == "relation_change_blocked" ||
			decision.Decision == "relation_preserved") &&
			decision.OldTargetSequenceNo == 7 &&
			(decision.NewTargetSequenceNo == 6 || decision.NewTargetSequenceNo == 7) &&
			!decision.RelationChangeAllowed {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("missing drift prevention audit: %+v", stats2.CorrectionDecisions)
	}
}

func TestSession8DLowSimilarityCorrectionStaysPending(t *testing.T) {
	unrelated := liveAnalysisItem{
		ID: "fact-network", Kind: "fact", Status: "open",
		Title: "ネットワーク監視は正常", Body: "監視には異常がありません",
		EvidenceSequenceNos: []int64{7},
	}
	replacement := liveAnalysisItem{
		ID: "fact-contract", Kind: "fact", Status: "open",
		Title: "契約日は月末", Body: "契約日は月末でした",
		EvidenceSequenceNos: []int64{8},
	}
	state := &liveAnalysisPayload{
		Items: []liveAnalysisItem{unrelated, replacement},
		Tree:  testSession8DTree(unrelated, replacement),
	}
	scope := testSession8DScope(map[int64]string{
		7: "ネットワーク監視には異常がありません。",
		8: "正確には契約日は来月ではなく今月末でした。",
	})
	repairCorrectionSupersessions(
		state, scope, classifyDiscourseTimeline(scope), 2, &liveAnalysisTreeMergeStats{},
	)
	if state.Items[0].Inactive || state.Items[0].MergedIntoID != "" ||
		len(state.CorrectionRelations) != 1 ||
		state.CorrectionRelations[0].Status != "pending" {
		t.Fatalf("unrelated item was superseded: state=%+v", state)
	}
}

func sequenceNosOfSegments(segments []domain.TranscriptSegment) []int64 {
	result := make([]int64, 0, len(segments))
	for _, segment := range segments {
		result = append(result, segment.SequenceNo)
	}
	return result
}

func testSession8DScope(texts map[int64]string) liveEvidenceScope {
	scope := liveEvidenceScope{
		Allowed: make(map[int64]struct{}), CurrentRound: make(map[int64]struct{}),
		TranscriptText: make(map[int64]string), Segments: make(map[int64]domain.TranscriptSegment),
	}
	for sequenceNo, text := range texts {
		segment := domain.TranscriptSegment{
			CallID: "call", SequenceNo: sequenceNo, IsFinal: true,
			Text: text, SpeakerName: "発話者",
		}
		scope.Allowed[sequenceNo] = struct{}{}
		scope.CurrentRound[sequenceNo] = struct{}{}
		scope.TranscriptText[sequenceNo] = text
		scope.Segments[sequenceNo] = segment
		if sequenceNo > scope.CoveredThrough {
			scope.CoveredThrough = sequenceNo
		}
	}
	return scope
}

func testSession8DTree(items ...liveAnalysisItem) *liveAnalysisTree {
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Origin: topicOriginSystem},
		{ID: "topic-network", Kind: "topic", ParentID: treeRootNodeID, Origin: topicOriginDynamic},
	}}
	for _, item := range items {
		tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{
			ID: item.ID, Kind: item.Kind, ParentID: "topic-network",
			Label: item.Title, Status: item.Status, RelatedItemIDs: []string{item.ID},
		})
	}
	rebuildTreeAuditEdges(tree)
	return tree
}

func assertSession8DCorrectionState(t *testing.T, state *liveAnalysisPayload) {
	t.Helper()
	byID := make(map[string]liveAnalysisItem, len(state.Items))
	for _, item := range state.Items {
		byID[item.ID] = item
	}
	if byID["fact-a"].Inactive || byID["fact-a"].MergedIntoID != "" {
		t.Fatalf("sequence 6 drifted into supersession: %+v", byID["fact-a"])
	}
	if !byID["fact-b"].Inactive || byID["fact-b"].MergedIntoID != "fact-corrected" {
		t.Fatalf("sequence 7 not superseded: %+v", byID["fact-b"])
	}
	if byID["fact-corrected"].Inactive || byID["fact-corrected"].Kind != "fact" {
		t.Fatalf("sequence 8 replacement not active Fact: %+v", byID["fact-corrected"])
	}
}
