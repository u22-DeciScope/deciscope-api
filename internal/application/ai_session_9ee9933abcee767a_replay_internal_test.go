package application

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestStrongTodoSynthesisRecognizesFirstPersonOwner(t *testing.T) {
	scope, timeline := agendaTimelineFromSegments([]domain.TranscriptSegment{{
		SequenceNo: 1, SpeakerName: "山下", IsFinal: true,
		Text: "私は今週金曜日までにチェックリスト案を作成します。",
	}})
	stats := &liveAnalysisTreeMergeStats{}
	items := synthesizeStrongTodoItems(nil, nil, scope, timeline, stats)
	if len(items) != 1 {
		t.Fatalf("items=%+v stats=%+v", items, stats)
	}
	item := items[0]
	features := inferItemSemanticFeatures(item, scope)
	if item.Kind != "todo" || !strings.HasPrefix(item.Title, "山下さんが") ||
		!features.OwnerPresent || !features.DeadlinePresent ||
		!features.DecisionOrCommitment || !equalInt64s(item.EvidenceSequenceNos, []int64{1}) {
		t.Fatalf("item=%+v features=%+v", item, features)
	}
}

func TestStrongTodoSynthesisRecognizesDelegatedCommitment(t *testing.T) {
	scope, timeline := agendaTimelineFromSegments([]domain.TranscriptSegment{{
		SequenceNo: 2, IsFinal: true,
		Text: "佐藤さんには来週火曜日までに設定差分を確認してもらいます。",
	}})
	items := synthesizeStrongTodoItems(nil, nil, scope, timeline, &liveAnalysisTreeMergeStats{})
	if len(items) != 1 {
		t.Fatalf("items=%+v", items)
	}
	features := inferItemSemanticFeatures(items[0], scope)
	if items[0].Kind != "todo" || !features.OwnerPresent ||
		!features.DeadlinePresent || !features.DecisionOrCommitment ||
		!strings.Contains(items[0].Body, "設定差分を確認") {
		t.Fatalf("item=%+v features=%+v", items[0], features)
	}
}

func TestLowInformationAtomDoesNotHideFollowingStrongTodo(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		19: "Nuts。",
		20: "高橋さんに今週中に証明書の更新手順と作業可能日を確認してもらいます。",
	}, 19, 20)
	content := `{"summary":"","currentTopic":"","utteranceRoles":[
		{"sequenceNo":19,"role":"filler"},{"sequenceNo":20,"role":"substantive"}
	],"items":[{
		"clientKey":"noise-only","kind":"issue","subtype":"discussion","severity":"low",
		"title":"証明書更新に失敗した","body":"証明書更新に失敗した",
		"status":"open","evidenceSequenceNos":[19],"evidenceSnippets":["Nuts。"]
	}],"newTopics":[],"assignments":[]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
		content, nil, nil, 1, []int64{19, 20}, scope,
		TreeClassificationConfig{}, stats,
	)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	active := activeItemsForReplay(state.Items)
	if len(active) != 1 || active[0].Kind != "todo" ||
		!equalInt64s(active[0].EvidenceSequenceNos, []int64{20}) ||
		!strings.Contains(active[0].Body, "高橋さん") ||
		stats.GroundingRejected == 0 || stats.StrongTodosSynthesized != 1 ||
		scope.CoveredThrough != 20 {
		t.Fatalf("active=%+v stats=%+v covered=%d", active, stats, scope.CoveredThrough)
	}
}

func TestNecessityAndCommitmentRemainDistinct(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "unassigned necessity", text: "監視条件は次回までに検討が必要です。", want: "issue"},
		{name: "committed action", text: "次回までに監視条件を検討します。", want: "todo"},
		{name: "assigned deliverable", text: "山下さんが次回までに監視条件案を作成します。", want: "todo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := liveAnalysisItem{
				ID: "candidate", Kind: "todo", Title: tc.text, Body: tc.text,
				Status: "open", EvidenceSequenceNos: []int64{1},
				EvidenceSnippets: []string{strings.TrimSuffix(tc.text, "。")},
			}
			scope := evidenceScopeFromTexts(map[int64]string{1: tc.text}, 1)
			decision := evaluateLiveItemKind(item, scope, "necessity_commitment")
			if decision.CanonicalKind != tc.want {
				t.Fatalf("decision=%+v", decision)
			}
			if tc.name == "assigned deliverable" &&
				(!decision.Features.OwnerPresent || !decision.Features.DeadlinePresent) {
				t.Fatalf("features=%+v", decision.Features)
			}
		})
	}
}

func TestCorrectionPendingThenFinalRepairReconstructsIdempotently(t *testing.T) {
	old := liveAnalysisItem{
		ID: "item-fact-old-port", Kind: "fact", Severity: "medium",
		Title: "ポートはアクセスポート", Body: "ポートはアクセスポートでした。",
		Status: "open", EvidenceSequenceNos: []int64{1},
		GroundingDecision: "accepted", GroundingConfidence: 0.95,
		GroundingSourceTypes: []groundingSourceType{groundingSourceFinalTranscript},
	}
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{old}, TreeVersion: 1,
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体", Origin: topicOriginSystem},
			{ID: "topic-network", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク", Origin: topicOriginDynamic},
			{ID: old.ID, Kind: "fact", ParentID: "topic-network", Label: old.Title, Description: old.Body},
		}},
	}
	rebuildTreeAuditEdges(state.Tree)
	segments := []domain.TranscriptSegment{
		{SequenceNo: 1, IsFinal: true, Text: "ポートはアクセスポートでした。"},
		{SequenceNo: 2, IsFinal: true, Text: "正確にはトランク設定で、VLAN 30だけが許可されていませんでした。"},
	}
	scope, timeline := agendaTimelineFromSegments(segments)
	pendingStats := &liveAnalysisTreeMergeStats{}
	// This represents the point immediately after the model's correction item
	// was rejected by grounding: no replacement item exists yet.
	repairCorrectionSupersessions(&state, scope, timeline, 2, pendingStats)
	if pendingStats.CorrectionItemsPending != 1 ||
		state.Items[0].SuppressionReason != "correction_pending_replacement" ||
		!state.Items[0].CandidateInactive ||
		liveTreeNodeByID(state.Tree, old.ID) != nil ||
		len(pendingStats.CorrectionDecisions) != 1 ||
		pendingStats.CorrectionDecisions[0].TargetSequenceNo != 1 {
		t.Fatalf("state=%+v stats=%+v", state, pendingStats)
	}

	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	repairedRaw, stats := applyDeterministicFinalTreeRepairs(
		payload, nil, 2, finalRepairInput{Segments: segments},
	)
	if stats.IntegrityRejected || stats.Error != "" ||
		stats.CorrectionItemsReconstructed != 1 ||
		stats.CorrectionItemsSuperseded != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	repaired := previousLiveAnalysisState(repairedRaw)
	active := activeItemsForReplay(repaired.Items)
	if len(active) != 1 || active[0].Kind != "fact" ||
		!equalInt64s(active[0].EvidenceSequenceNos, []int64{2}) ||
		!strings.Contains(active[0].Body, "トランク設定") ||
		!replayItemInactive(repaired.Items, old.ID) ||
		len(repaired.ItemTombstones) != 1 {
		t.Fatalf("active=%+v all=%+v tombstones=%+v", active, repaired.Items, repaired.ItemTombstones)
	}

	twiceRaw, twiceStats := applyDeterministicFinalTreeRepairs(
		repairedRaw, nil, 2, finalRepairInput{Segments: segments},
	)
	twice := previousLiveAnalysisState(twiceRaw)
	if twiceStats.CorrectionItemsReconstructed != 0 ||
		twiceStats.CorrectionItemsSuperseded != 0 ||
		len(activeItemsForReplay(twice.Items)) != 1 ||
		len(twice.ItemTombstones) != 1 {
		t.Fatalf("second stats=%+v state=%+v", twiceStats, twice)
	}
}

func TestCompoundUtteranceKeepsOwnerAndDeadlineClauseLocal(t *testing.T) {
	text := "山下さんが金曜日までにチェックリストを作ります。2階の遅延原因はまだ分かっていません。"
	scope := evidenceScopeFromTexts(map[int64]string{1: text}, 1)
	todo := liveAnalysisItem{
		ID: "todo-checklist", Kind: "todo", Title: "チェックリスト作成",
		Body: "チェックリストを作る", Status: "open",
		EvidenceSequenceNos: []int64{1},
		EvidenceSnippets:    []string{"山下さんが金曜日までにチェックリストを作ります"},
	}
	issue := liveAnalysisItem{
		ID: "issue-delay", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title: "2階の遅延原因", Body: "2階の遅延原因はまだ分かっていません",
		Status: "open", EvidenceSequenceNos: []int64{1},
		EvidenceSnippets: []string{"2階の遅延原因はまだ分かっていません"},
	}
	todoDecision := evaluateLiveItemKind(todo, scope, "compound_locality")
	issueDecision := evaluateLiveItemKind(issue, scope, "compound_locality")
	if todoDecision.CanonicalKind != "todo" ||
		!todoDecision.Features.OwnerPresent || !todoDecision.Features.DeadlinePresent ||
		issueDecision.CanonicalKind != "issue" ||
		issueDecision.Features.OwnerPresent || issueDecision.Features.DeadlinePresent {
		t.Fatalf("todo=%+v issue=%+v", todoDecision, issueDecision)
	}
}

func TestDistinctFactsCannotOverwriteByReusedModelID(t *testing.T) {
	previous := liveAnalysisItem{
		ID: "item-fact-event", Kind: "fact", Title: "9時20分に障害が発生",
		Body: "9時20分に障害が発生しました。", EvidenceSequenceNos: []int64{1},
	}
	update := liveAnalysisItem{
		ID: previous.ID, Kind: "fact", Title: "9時52分に旧スイッチへ切り戻し",
		Body: "9時52分に旧スイッチへ切り戻しました。", EvidenceSequenceNos: []int64{2},
	}
	scope := evidenceScopeFromTexts(map[int64]string{
		1: previous.Body,
		2: update.Body,
	}, 2)
	stats := &liveAnalysisTreeMergeStats{}
	diff, _ := detachCrossKindActionUpdates(
		[]liveAnalysisItem{previous}, []liveAnalysisItem{update}, nil, scope, stats,
	)
	if len(diff) != 1 || diff[0].ID != "" ||
		!strings.Contains(diff[0].ClientKey, "distinct") ||
		stats.DivergentUpdatesDetached != 1 {
		t.Fatalf("diff=%+v stats=%+v", diff, stats)
	}
}

func TestRecapCannotOverwriteIssueWithTodo(t *testing.T) {
	issue := liveAnalysisItem{
		ID: "issue-delay", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Severity: "medium", Title: "2階の遅延原因", Body: "2階の遅延原因はまだ分かっていません。",
		Status: "open", EvidenceSequenceNos: []int64{1},
	}
	todo := liveAnalysisItem{
		ID: "todo-diff", Kind: "todo", Severity: "medium",
		Title: "設定差分を確認", Body: "佐藤さんが火曜日までに設定差分を確認します。",
		Status: "open", EvidenceSequenceNos: []int64{2},
	}
	previous := replayPayloadWithTopic([]liveAnalysisItem{issue, todo})
	previousRaw, _ := json.Marshal(previous)
	scope := evidenceScopeFromTexts(map[int64]string{
		1: issue.Body,
		2: todo.Body,
		3: "佐藤さんの確認と、未解決の2階遅延を継続します。",
	}, 3)
	content := `{"summary":"","currentTopic":"","utteranceRoles":[{"sequenceNo":3,"role":"recap"}],"items":[{
		"id":"issue-delay","kind":"todo","severity":"medium",
		"title":"佐藤さんの確認と2階遅延を継続","body":"佐藤さんの確認と、未解決の2階遅延を継続します。",
		"status":"open","evidenceSequenceNos":[3],
		"evidenceSnippets":["佐藤さんの確認と、未解決の2階遅延を継続します。"]
	}],"newTopics":[],"assignments":[]}`
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
		content, previousRaw, nil, 2, []int64{3}, scope,
		TreeClassificationConfig{}, &liveAnalysisTreeMergeStats{},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if got := findItemByID(state.Items, issue.ID); got == nil || got.Kind != "issue" {
		t.Fatalf("issue=%+v items=%+v", got, state.Items)
	}
	if got := findItemByID(state.Items, todo.ID); got == nil || got.Kind != "todo" {
		t.Fatalf("todo=%+v items=%+v", got, state.Items)
	}
}

func TestSession9EE9933ABCEE767AStoredV18DeterministicReplay(t *testing.T) {
	segments := session9EE9933ABCEE767ATranscripts()
	items := []liveAnalysisItem{
		replayGroundedItem("item-fact-c86e74b667c5", "fact", "", "復旧対応としては、午前9時52分に旧スイッチへ一度切り戻し、その後、新しいスイッ", "復旧対応としては、午前9時52分に旧スイッチへ一度切り戻し、その後、新しいスイッチのトランク設定と許可部位欄を修正しました", 2, 11),
		replayGroundedItem("item-fact-38a7c4faee02", "fact", "", "接続できる端末", "接続できる端末", 4),
		replayGroundedItem("item-fact-89a46d5daec9", "fact", "", "ルーターとファイアウォールの初動確認", "障害発生後、最初にルーターとファイアウォールを確認したが、いずれにも明確な異常はなかった。", 5),
		replayGroundedItem("item-fact-ae17037445cb", "fact", "", "3階スイッチの通信不安定", "3階スイッチの通信不安定", 6),
		replayGroundedItem("item-fact-7161d2ee8b5c", "fact", "", "ポート設定の誤りによる影響", "交換したスイッチでは、上位スイッチへ接続するポートの設定が、本来のトランクポートではなくアクセスポートになっていた。", 7),
		replayGroundedItem("item-issue-investigation-b86af04a9960", "issue", issueSubtypeInvestigation, "この設定漏れが障害の直接原因である可能性が最も高いと考えています", "この設定漏れが障害の直接原因である可能性が最も高いと考えています", 9),
		replayGroundedItem("issue-investigation-auto-7b15808aa786", "issue", issueSubtypeInvestigation, "ただし、 2階で発生した通信チームまで、このvラン設定だけで説明できるかは確認で", "ただし、 2階で発生した通信チームまで、このvラン設定だけで説明できるかは確認できていません。この点は未解決の調査事項として残します", 10),
		replayGroundedItem("item-todo-78c95a990ca0", "todo", "", "交換前後でvランごとの疎通確認を実施するチェックリストを作成します", "交換前後でvランごとの疎通確認を実施するチェックリストを作成します", 13, 14),
		replayGroundedItem("decision-auto-e0a1be5b4a04", "decision", "", "交換前後のvランごとの疎通確認チェックリストの運用を次回の機器交換から適用する", "交換前後のvランごとの疎通確認チェックリストの運用を次回の機器交換から適用することにします", 14),
		replayGroundedItem("item-issue-discussion-359568f7ed74", "issue", issueSubtypeDiscussion, "スイッチ交換用チェックリスト案の作成", "今週金曜日までにチェックリスト案を作成。来週火曜日までに差分を確認。監視項目へヴィラン単位の疎通確認を追加する案を検討。", 15),
		replayGroundedItem("item-risk-01ad4bc04105", "risk", "", "監査対象を増やすとアラートが多くなりすぎる可能性がある", "監査対象を増やすとアラートが多くなりすぎる可能性がある", 16),
		replayGroundedItem("item-todo-aba1abf81df3", "todo", "", "監査間隔と通知条件を次回までに検討する必要がある", "監査間隔と通知条件を次回までに検討する必要がある", 16),
		replayGroundedItem("item-fact-8a9d169c4a45", "fact", "", "VPN装置の証明書が来月末に期限切れになることがわかりました", "VPN装置の証明書が来月末に期限切れになることがわかりました", 17),
		replayGroundedItem("item-risk-3aef4d4253ec", "risk", "", "今回の支社ネットワーク障害とは直接関係ありませんが、放置するとリモート接続ができ", "今回の支社ネットワーク障害とは直接関係ありませんが、放置するとリモート接続ができなくなる可能性があります。VPN証明書の更新は、今回のブイラン障害とは別の新しい対応事項として管理します。", 18),
		replayGroundedItem("item-todo-944c603f33a4", "todo", "", "VPN証明書更新の別対応化", "VPN証明書の更新は今回の障害とは別の新しい対応事項として進めます。", 23),
	}
	items[0].Status = "updated"
	items[0].EvidenceSnippets = []string{"復旧対応としては、午前9時52分に旧スイッチへ一度切り戻し、その後、新しいスイッチのトランク設定と許可部位欄を修正しました"}
	items[5].ClassificationStatus = classificationUnclassified
	items[9].Status = "resolved"
	items[9].EvidenceSnippets = []string{"私は今週金曜日までにスイッチ交換用のチェックリスト案を作成します。"}
	items[11].EvidenceSnippets = []string{"ただし、監査対象を増やすとアラートが多くなりすぎる可能性があります。監査間隔と通知条件については、次回までに検討が必要です。"}
	items[14].ClassificationStatus = classificationTentative
	items[14].EvidenceSnippets = []string{"VPN証明書の更新は、ええ、今回の障害とは別の新しい対応事項として進めます。"}
	state := replayPayloadWithTopic(items)
	state.Tree = session9EE9933ABCEE767AV18Tree(items)
	state.TreeVersion = 18
	payload, _ := json.Marshal(state)

	raw, stats := applyDeterministicFinalTreeRepairs(
		payload, nil, 18, finalRepairInput{Segments: segments},
	)
	if stats.Error != "" || stats.IntegrityRejected {
		t.Fatalf("stats=%+v", stats)
	}
	repaired := previousLiveAnalysisState(raw)
	active := activeItemsForReplay(repaired.Items)
	if replayActiveItemByEvidence(active, 7) != nil {
		t.Fatalf("sequence 7 old fact remained active: %+v", active)
	}
	corrected := replayActiveItemByEvidence(active, 8)
	if corrected == nil || corrected.Kind != "fact" ||
		(!strings.Contains(corrected.Body, "VLAN") &&
			!strings.Contains(corrected.Body, "vラン")) {
		t.Fatalf("sequence 8 corrected fact=%+v", corrected)
	}
	seq15 := replayActiveItemsByEvidence(active, 15)
	if len(seq15) != 2 || replayKindCount(seq15, "todo") != 2 {
		var allSeq15 []string
		for _, item := range repaired.Items {
			if containsInt64(item.EvidenceSequenceNos, 15) {
				allSeq15 = append(allSeq15, strings.Join([]string{
					item.ID, item.Kind, item.Status,
					"inactive=" + strconv.FormatBool(item.Inactive),
					"merged=" + item.MergedIntoID,
					"title=" + item.Title,
					"snippets=" + strings.Join(item.EvidenceSnippets, " / "),
				}, "|"))
			}
		}
		t.Fatalf("sequence 15=%+v all=%v original=%+v tombstones=%+v sameKindMerged=%d synthesized=%d",
			seq15, allSeq15,
			findItemByID(repaired.Items, "item-issue-discussion-359568f7ed74"),
			repaired.ItemTombstones, stats.SameKindDuplicatesMerged,
			stats.StrongTodosSynthesized)
	}
	seq16 := replayActiveItemByEvidenceKind(active, 16, "issue")
	if seq16 == nil || seq16.Kind != "issue" {
		t.Fatalf("sequence 16=%+v", seq16)
	}
	seq20 := replayActiveItemByEvidenceKind(active, 20, "todo")
	if seq20 == nil || seq20.Kind != "todo" ||
		!strings.Contains(seq20.Body, "高橋さん") ||
		!strings.Contains(seq20.Body, "今週中") {
		t.Fatalf("sequence 20=%+v", seq20)
	}
	recovery := findItemByID(repaired.Items, "item-fact-c86e74b667c5")
	if recovery == nil || !equalInt64s(recovery.EvidenceSequenceNos, []int64{11}) {
		t.Fatalf("recovery=%+v localization=%+v", recovery, stats.EvidenceLocalizationDecisions)
	}
	if !replayItemInactive(repaired.Items, "item-fact-7161d2ee8b5c") ||
		stats.CorrectionItemsSuperseded != 1 ||
		stats.CorrectionItemsReconstructed != 1 ||
		stats.StrongTodosSynthesized < 2 ||
		stats.EvidenceReferencesPruned == 0 {
		t.Fatalf("items=%+v stats=%+v", repaired.Items, stats)
	}
	if integrity := validateTreeIntegrity(repaired.Tree, repaired.Items, nil); !integrity.Valid {
		t.Fatalf("integrity=%+v", integrity)
	}

	secondRaw, secondStats := applyDeterministicFinalTreeRepairs(
		raw, nil, 18, finalRepairInput{Segments: segments},
	)
	second := previousLiveAnalysisState(secondRaw)
	if secondStats.CorrectionItemsReconstructed != 0 ||
		secondStats.StrongTodosSynthesized != 0 ||
		len(activeItemsForReplay(second.Items)) != len(active) {
		t.Fatalf("second stats=%+v active=%+v", secondStats, activeItemsForReplay(second.Items))
	}
	health := computeTreeHealth(repaired.Tree)
	t.Logf("session_9ee9933abcee767a v18 replay: before=%s after=%s tombstones=%d strongTodosSynthesized=%d correctionReconstructed=%d correctionSuperseded=%d evidencePruned=%d groundingRejected=%d kindChanges=%d kindSplits=%d sameKindMerged=%d needsReorganization=%t reorganizationReasons=%v health=%+v",
		replayStateSummary(state.Items, segments), replayStateSummary(repaired.Items, segments),
		len(repaired.ItemTombstones), stats.StrongTodosSynthesized,
		stats.CorrectionItemsReconstructed, stats.CorrectionItemsSuperseded,
		stats.EvidenceReferencesPruned, stats.GroundingRejected,
		stats.KindValidationChanges, stats.KindSemanticSplits, stats.SameKindDuplicatesMerged,
		health.needsReorganization(), health.reorganizationReasons(), health)
	for _, item := range repaired.Items {
		if item.Inactive || item.MergedIntoID != "" || item.CandidateInactive ||
			item.ClassificationStatus == classificationTentative ||
			item.Status == "resolved" {
			continue
		}
		t.Logf("final active item: id=%s kind=%s status=%s evidence=%v title=%q",
			item.ID, item.Kind, item.Status, item.EvidenceSequenceNos, item.Title)
	}
}

func session9EE9933ABCEE767AV18Tree(items []liveAnalysisItem) *liveAnalysisTree {
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "名古屋支社ネットワーク障害の振り返りと再発防止", Origin: topicOriginSystem},
		{ID: "topic-agenda-64b761a79cc0", Kind: "topic", ParentID: treeRootNodeID, Label: "ルーターとファイアウォールの初動確認", Origin: topicOriginAgenda},
		{ID: "topic-agenda-7dd3ab9e5ea9", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク機器交換時のダブルチェック導入", Origin: topicOriginAgenda},
		{ID: "topic-agenda-a5f8fcd0c7a2", Kind: "topic", ParentID: treeRootNodeID, Label: "名古屋支社の3階を中心に社内ネットワーク障害", Origin: topicOriginAgenda},
		{ID: "topic-dynamic-0f2f72f65575", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN装置の証明書が来月末に期限切れ", Origin: topicOriginDynamic},
		{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: treeUnclassifiedTopicLabel, Origin: topicOriginSystem},
		{ID: "group-239151819da6", Kind: "group", ParentID: "topic-agenda-7dd3ab9e5ea9", Label: "交換前後のvランごとの疎通確認チェックリスト", Origin: assignmentSourceRule},
	}}
	parents := map[string]string{
		"item-fact-c86e74b667c5":                "topic-agenda-a5f8fcd0c7a2",
		"item-fact-38a7c4faee02":                "topic-agenda-a5f8fcd0c7a2",
		"item-fact-89a46d5daec9":                "topic-agenda-64b761a79cc0",
		"item-fact-ae17037445cb":                "topic-agenda-a5f8fcd0c7a2",
		"item-fact-7161d2ee8b5c":                "topic-agenda-a5f8fcd0c7a2",
		"item-issue-investigation-b86af04a9960": treeUnclassifiedTopicID,
		"issue-investigation-auto-7b15808aa786": "topic-agenda-64b761a79cc0",
		"item-todo-78c95a990ca0":                "group-239151819da6",
		"decision-auto-e0a1be5b4a04":            "group-239151819da6",
		"item-issue-discussion-359568f7ed74":    "group-239151819da6",
		"item-risk-01ad4bc04105":                "topic-agenda-7dd3ab9e5ea9",
		"item-todo-aba1abf81df3":                "topic-agenda-7dd3ab9e5ea9",
		"item-fact-8a9d169c4a45":                "topic-dynamic-0f2f72f65575",
		"item-risk-3aef4d4253ec":                "topic-dynamic-0f2f72f65575",
		"item-todo-944c603f33a4":                treeUnclassifiedTopicID,
	}
	for _, item := range items {
		tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{
			ID: item.ID, Kind: item.Kind, Subtype: item.Subtype,
			ParentID: parents[item.ID], Label: item.Title,
			Description: item.Body, Status: item.Status,
		})
	}
	rebuildTreeAuditEdges(tree)
	return tree
}

func replayStateSummary(items []liveAnalysisItem, segments []domain.TranscriptSegment) string {
	counts := map[string]int{}
	active, inactive, tentative, ownerTodo, deadlineTodo := 0, 0, 0, 0, 0
	scope, _ := agendaTimelineFromSegments(segments)
	for _, item := range items {
		counts[item.Kind]++
		if item.Inactive || item.MergedIntoID != "" || item.CandidateInactive {
			inactive++
		} else if item.ClassificationStatus == classificationTentative {
			tentative++
		} else if item.Status != "resolved" {
			active++
		}
		if item.Kind == "todo" && !item.Inactive && item.MergedIntoID == "" {
			features := inferItemSemanticFeatures(item, scope)
			if features.OwnerPresent {
				ownerTodo++
			}
			if features.DeadlinePresent {
				deadlineTodo++
			}
		}
	}
	return strings.Join([]string{
		"items=" + strconv.Itoa(len(items)),
		"active=" + strconv.Itoa(active),
		"inactive=" + strconv.Itoa(inactive),
		"tentative=" + strconv.Itoa(tentative),
		"fact=" + strconv.Itoa(counts["fact"]),
		"issue=" + strconv.Itoa(counts["issue"]),
		"risk=" + strconv.Itoa(counts["risk"]),
		"todo=" + strconv.Itoa(counts["todo"]),
		"decision=" + strconv.Itoa(counts["decision"]),
		"ownerTodo=" + strconv.Itoa(ownerTodo),
		"deadlineTodo=" + strconv.Itoa(deadlineTodo),
	}, ",")
}

func replayGroundedItem(id, kind, subtype, title, body string, evidence ...int64) liveAnalysisItem {
	item := liveAnalysisItem{
		ID: id, Kind: kind, Subtype: subtype, Severity: "medium",
		Title: title, Body: body, Status: "open",
		EvidenceSequenceNos: evidence,
		GroundingDecision:   "accepted",
		GroundingConfidence: 0.95,
		GroundingSourceTypes: []groundingSourceType{
			groundingSourceFinalTranscript,
		},
		ClassificationStatus: classificationAssigned,
		AssignmentSource:     "model",
	}
	return item
}

func replayPayloadWithTopic(items []liveAnalysisItem) liveAnalysisPayload {
	state := liveAnalysisPayload{
		Items: items, TreeVersion: 1,
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体", Origin: topicOriginSystem},
			{ID: "topic-network", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク障害と再発防止", Origin: topicOriginDynamic},
		}},
	}
	for _, item := range items {
		state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
			ID: item.ID, Kind: item.Kind, Subtype: item.Subtype,
			ParentID: "topic-network", Label: item.Title, Description: item.Body,
			Status: item.Status,
		})
	}
	rebuildTreeAuditEdges(state.Tree)
	return state
}

func activeItemsForReplay(items []liveAnalysisItem) []liveAnalysisItem {
	active := make([]liveAnalysisItem, 0, len(items))
	for _, item := range items {
		if !item.Inactive && item.MergedIntoID == "" && !item.CandidateInactive &&
			item.Status != "resolved" &&
			item.ClassificationStatus != classificationTentative {
			active = append(active, item)
		}
	}
	return active
}

func replayActiveItemsByEvidence(items []liveAnalysisItem, sequenceNo int64) []liveAnalysisItem {
	var matched []liveAnalysisItem
	for _, item := range items {
		if containsInt64(item.EvidenceSequenceNos, sequenceNo) {
			matched = append(matched, item)
		}
	}
	return matched
}

func replayActiveItemByEvidence(items []liveAnalysisItem, sequenceNo int64) *liveAnalysisItem {
	for index := range items {
		if containsInt64(items[index].EvidenceSequenceNos, sequenceNo) {
			return &items[index]
		}
	}
	return nil
}

func replayActiveItemByEvidenceKind(items []liveAnalysisItem, sequenceNo int64, kind string) *liveAnalysisItem {
	for index := range items {
		if items[index].Kind == kind &&
			containsInt64(items[index].EvidenceSequenceNos, sequenceNo) {
			return &items[index]
		}
	}
	return nil
}

func replayKindCount(items []liveAnalysisItem, kind string) int {
	count := 0
	for _, item := range items {
		if item.Kind == kind {
			count++
		}
	}
	return count
}

func replayItemInactive(items []liveAnalysisItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return item.Inactive && item.MergedIntoID != ""
		}
	}
	return false
}

func session9EE9933ABCEE767ATranscripts() []domain.TranscriptSegment {
	texts := []string{
		"それでは、名古屋支社で発生したネットワーク障害について振り返ります。",
		"本日午前9時20分ごろ、名古屋支社の3階を中心に社内ネットワークへ接続できないという報告がありました。",
		"当初は3階だけの障害だと考えていましたが、正確には2階の一部でも通信チームが発生していました。影響を受けたのは、有線LAN、車内無線LAN、ファイルサーファー、社内システムへの接続です。",
		"インターネットが完全に停止したわけではなく、接続できる端末、接続できない端末が混在していました。",
		"障害発生後、最初にルーターとファイアウォールを確認しましたが、どちらにも明確な異常はありませんでした。",
		"その後、前日の夜に交換した3階のアクセススイッチを確認したところ、 vラン20とvラン30の通信が不安定になっていました。",
		"交換したスイッチでは、上位スイッチへ接続するポートの設定が、本来のトランクポートではなくアクセスポートになっていました。",
		"いえ、正確には完全なアクセスポート設定ではありません。トランク設定自体は入っていましたが、許可するvランの一覧からvラン30が漏れていました。",
		"現時点では、この設定漏れが障害の直接原因である可能性が最も高いと考えています。",
		"ただし、 2階で発生した通信チームまで、このvラン設定だけで説明できるかは確認できていません。この点は未解決の調査事項として残します。",
		"復旧対応としては、午前9時52分に旧スイッチへ一度切り戻し、その後、新しいスイッチのトランク設定と許可部位欄を修正しました。",
		"午前10時5分に有線LAN、無線LANファイルサーバへの接続が正常になったことを確認しています。",
		"今後の対応についてです。まず、ネットワーク機器を交換する際は、作業者とは別の担当者が設定内容を確認するダブルチェックを必要をええ、必須必須にします。",
		"また、交換前後でvランごとの疎通確認を実施するチェックリストを作成します。この運用を次回の機器交換から適用することにします。",
		"私は今週金曜日までにスイッチ交換用のチェックリスト案を作成します。佐藤さんには、来週火曜日までに今回のスイッチ設定と標準設定との差分を確認してもらいます。さらに、 vランごとの通信切断を早期に検知できるよう、監視項目へヴィラン単位の疎通確認を追加する案もあります。",
		"ただし、監査対象を増やすとアラートが多くなりすぎる可能性があります。監査間隔と通知条件については、次回までに検討が必要です。",
		"ここでアジェンダにはなかった別の問題があります。今回ログを確認したところ、 VPN装置の証明書が来月末に期限切れになることがわかりました。",
		"今回の支社ネットワーク障害とは直接関係ありませんが、放置するとリモート接続ができなくなる可能性があります。VPN証明書の更新は、今回のブイラン障害とは別の新しい対応事項として管理します。",
		"Nuts。",
		"高橋さんに今週中に証明書の更新手順と作業可能日を確認してもらいます。",
		"最後にここまでをまとめます。今回の紹介は、交換したアクセススイッチでvラン30の許可設定が漏れていたことが主な原因と考えられます。復旧のため、旧スイッチの切り戻しと新しいスイッチのトランク設定修正を実施しました。再発防止として、設定のダブルチェックとブイランごとの疎通確認を必須にします。",
		"私は今週金曜日までにチェックリストタングを作成し、佐藤さんは来週火曜日までに標準設定とのサブを確認します。2回の通信遅延の原因と監視アラートの条件は、未解決事項として残します。",
		"VPN証明書の更新は、ええ、今回の障害とは別の新しい対応事項として進めます。以上で振り返りを終了します。",
	}
	segments := make([]domain.TranscriptSegment, 0, len(texts))
	for index, text := range texts {
		segments = append(segments, domain.TranscriptSegment{
			SequenceNo: int64(index + 1), IsFinal: true, Text: text,
			SpeakerName: "山下 耀翔",
		})
	}
	return segments
}
