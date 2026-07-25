package application

import (
	"testing"
)

// このファイルは session_a4b76401f100a446 (名古屋支社ネットワーク障害) で
// ライブ分析version 1〜3の議論ツリーが空のままになった事象の回帰テストを持つ。
//
// 修正前の挙動は次のとおりだった。
//   - STTが「…について振り返ります。」を2発話へ分割し、単独の「振り返ります。」が
//     recap_intro と判定された。
//   - 以降の全発話が reference_recap となり、ai_item_validator.go が本文・新規性・
//     具体性を一切見ずに全itemを low_information として破棄した。
//   - 空ツリーだったため全候補が「新規item」に該当し、0ノードで終わった。
//
// 修正後は、recap は「発話が振り返りの文脈にある」ことだけを表し、破棄は
// filterReferenceRecapDiff (ai_proposition.go) の既存itemとの意味的照合と
// 新規性・具体性評価だけが決める。

// sessionA4B76401Transcript は実際にDBへ保存されていた確定transcriptである
// (transcript_segments, session_id=session_a4b76401f100a446, seq1-7)。
var sessionA4B76401Transcript = map[int64]string{
	1: "それでは、名古屋支社をええ。発生したネットワーク障害について。",
	2: "振り返ります。",
	3: "本日午後、ええ、していました。本日午前9時20分ごろ、名古屋支社の3階を中心に社内ネットワークへ接続できないという報告がありました。",
	4: "図書は3回だけの紹介だと考えていましたが、正確には2階の一部でも通信遅延が発生していました。",
	5: "影響を受けたのは、有線LAN、車内無線LAN、ファイルサーバー、社内システムへの接続です。",
	6: "インターネットが完全に停止したわけではなく、接続付ける端末を接続できない端末が混在していました。",
	7: "障害発生後、最初にルーターとファイアウォールを確認しましたが、どちらにも明確な異常はありませんでした。",
}

func sessionA4B76401ModelItems() []liveAnalysisItem {
	return []liveAnalysisItem{
		{ID: "issue-1", Kind: "issue", Subtype: issueSubtypeDiscussion, Severity: "high",
			Title: "3階を中心に社内ネットワークへ接続不能", Body: "本日午前9時20分ごろ、名古屋支社の3階を中心に接続不能の報告があった",
			Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true},
		{ID: "fact-1", Kind: "fact", Severity: "medium",
			Title: "2階の一部でも通信遅延が発生", Body: "当初は3階だけと考えられていたが、2階の一部でも通信遅延が発生していた",
			Status: "open", EvidenceSequenceNos: []int64{4}, evidenceSpecified: true},
		{ID: "fact-2", Kind: "fact", Severity: "medium",
			Title: "有線LAN・無線LAN・ファイルサーバーに影響", Body: "有線LAN、無線LAN、ファイルサーバー、社内システムへの接続が影響を受けた",
			Status: "open", EvidenceSequenceNos: []int64{5}, evidenceSpecified: true},
		{ID: "fact-3", Kind: "fact", Severity: "medium",
			Title: "ルーターとファイアウォールに異常なし", Body: "障害発生後に確認したが、どちらにも明確な異常はなかった",
			Status: "open", EvidenceSequenceNos: []int64{7}, evidenceSpecified: true},
	}
}

// runRecapItemGate は recap 判定からitem採否までのサーバー側ゲートだけを通す。
// パイプライン順序 (ai_analysis.go:4677-4684) と同じ順で呼ぶ。
func runRecapItemGate(t *testing.T, texts map[int64]string, round []int64, previous, diff []liveAnalysisItem) (discourseTimeline, []liveAnalysisItem, *liveAnalysisTreeMergeStats) {
	t.Helper()
	scope := evidenceScopeFromTexts(texts, round...)
	timeline := classifyDiscourseTimelineWithModel(scope, nil)
	stats := &liveAnalysisTreeMergeStats{}
	repaired, _ := repairLowInformationIssueItems(previous, diff, nil, timeline, scope, stats)
	kept := filterLowInformationLiveItems(previous, repaired, timeline, scope, stats)
	kept = filterReferenceRecapDiff(previous, kept, round, timeline, scope, stats)
	for sequenceNo := int64(1); sequenceNo <= int64(len(texts)); sequenceNo++ {
		t.Logf("seq=%d act=%-16s evidenceRole=%-16s detectedRole=%s",
			sequenceNo, classifyDiscourseAct(texts[sequenceNo]), timeline.Roles[sequenceNo], timeline.DetectedRoles[sequenceNo])
	}
	for _, decision := range stats.RecapDecisions {
		t.Logf("recap decision itemId=%s kind=%s decision=%s matchId=%s score=%.2f novel=%t concrete=%t reason=%s",
			decision.ItemID, decision.Kind, decision.Decision, decision.ExistingMatchID, decision.MatchScore,
			decision.NovelSubject, decision.ConcreteInfo, decision.RejectionReason)
	}
	return timeline, kept, stats
}

func itemTitles(items []liveAnalysisItem) []string {
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return titles
}

func recapDecisionFor(stats *liveAnalysisTreeMergeStats, itemID string) string {
	for _, decision := range stats.RecapDecisions {
		if decision.ItemID == itemID {
			return decision.Decision
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// recap判定そのものの挙動(修正対象外・回帰固定)
// ---------------------------------------------------------------------------

// TestRecapDocTriggerRequiresSplitUtterance は一次原因を固定する。recap判定は
// 「振り返ります」が単独の短い発話になったときだけ発火する。判定自体は今回
// 変更していない(破棄側を直した)ので、この挙動は維持される。
func TestRecapDocTriggerRequiresSplitUtterance(t *testing.T) {
	cases := []struct {
		text string
		want discourseAct
	}{
		{"振り返ります。", discourseRecapIntro},
		{"それでは、名古屋支社で発生したネットワーク障害について振り返ります。", discourseContent},
		{"今日発生した障害について振り返ります。", discourseContent},
		{"振り返り", discourseRecapIntro},
		{"まとめると。", discourseRecapIntro},
		{"整理すると。", discourseRecapIntro},
		{"振り返ると。", discourseContent},
		{"ここまでが昨日の障害の振り返りです。", discourseContent},
	}
	for _, tc := range cases {
		if act := classifyDiscourseAct(tc.text); act != tc.want {
			t.Errorf("classifyDiscourseAct(%q) = %q, want %q", tc.text, act, tc.want)
		}
	}
}

// TestRecapDocWholeMeetingBecomesReferenceRecap は、recapスパンの範囲自体は
// 変わっていないことを固定する。修正したのは「recapだから捨てる」処理であって
// 「どこがrecapか」ではない。
func TestRecapDocWholeMeetingBecomesReferenceRecap(t *testing.T) {
	scope := evidenceScopeFromTexts(sessionA4B76401Transcript, 3, 4, 5, 6, 7)
	timeline := classifyDiscourseTimelineWithModel(scope, nil)

	if len(timeline.Transitions) != 1 {
		t.Fatalf("transitions = %+v, want exactly one", timeline.Transitions)
	}
	transition := timeline.Transitions[0]
	if transition.SequenceNo != 2 || transition.From != "content" || transition.To != "recap" || transition.Act != discourseRecapIntro {
		t.Fatalf("transition = %+v, want seq=2 content->recap recap_intro", transition)
	}
	if timeline.Roles[1] != liveEvidencePrimary {
		t.Errorf("Roles[1] = %q, want primary (recap宣言より前)", timeline.Roles[1])
	}
	for _, sequenceNo := range []int64{3, 4, 5, 6, 7} {
		if role := timeline.Roles[sequenceNo]; role != liveEvidenceReferenceRecap {
			t.Errorf("Roles[%d] = %q, want %q", sequenceNo, role, liveEvidenceReferenceRecap)
		}
	}
}

// ---------------------------------------------------------------------------
// 修正の中核: recapだけを理由に破棄しない
// ---------------------------------------------------------------------------

// TestRecapConcreteItemsSurviveOnEmptyTree は本件の直接の回帰テスト。
// 空ツリーから始まる振り返り会議で、具体的なissue/factが保持される。
func TestRecapConcreteItemsSurviveOnEmptyTree(t *testing.T) {
	_, kept, stats := runRecapItemGate(t, sessionA4B76401Transcript, []int64{3, 4, 5, 6, 7}, nil, sessionA4B76401ModelItems())

	if len(kept) != 4 {
		t.Fatalf("kept = %d items %v, want 4", len(kept), itemTitles(kept))
	}
	if stats.LowInformationItemsRejected != 0 {
		t.Errorf("LowInformationItemsRejected = %d, want 0", stats.LowInformationItemsRejected)
	}
	if stats.ReferenceRecapItemsRejected != 0 {
		t.Errorf("ReferenceRecapItemsRejected = %d, want 0", stats.ReferenceRecapItemsRejected)
	}
	if stats.ReferenceRecapItemsRetained != 4 {
		t.Errorf("ReferenceRecapItemsRetained = %d, want 4", stats.ReferenceRecapItemsRetained)
	}
	for _, item := range kept {
		if got := recapDecisionFor(stats, item.ID); got != recapDecisionRetainedNovel {
			t.Errorf("decision for %s = %q, want %q", item.ID, got, recapDecisionRetainedNovel)
		}
	}
}

// TestRecapRejectionRequiresContentEvaluation は、recap evidenceだけを根拠に
// low_information を返さなくなったことを固定する。採否は本文と新規性で決まる。
func TestRecapRejectionRequiresContentEvaluation(t *testing.T) {
	scope := evidenceScopeFromTexts(sessionA4B76401Transcript, 3)
	timeline := classifyDiscourseTimelineWithModel(scope, nil)
	item := sessionA4B76401ModelItems()[0]

	if reason, _ := validateLiveItemInformation(item, false, timeline, scope); reason != "" {
		t.Fatalf("recap evidence: reason=%q, want empty (recap単独では破棄しない)", reason)
	}
	// discourse_only(制御発話のみ)は従来どおり破棄する。
	discourseTimeline := timeline
	discourseTimeline.Roles = map[int64]liveEvidenceRole{3: liveEvidenceDiscourseOnly}
	if reason, _ := validateLiveItemInformation(item, false, discourseTimeline, scope); reason != "low_information" {
		t.Fatalf("discourse_only evidence: reason=%q, want low_information", reason)
	}
}

// TestRecapNoveltyRescueIsReachable は、ai_proposition.go の救済経路が実際に
// 到達可能になったことを固定する(修正前は filterLowInformationLiveItems が
// 先に候補を消していた)。
func TestRecapNoveltyRescueIsReachable(t *testing.T) {
	scope := evidenceScopeFromTexts(sessionA4B76401Transcript, 3)
	timeline := classifyDiscourseTimelineWithModel(scope, nil)
	item := sessionA4B76401ModelItems()[0]

	if !recapItemHasNovelSubject(item, nil) {
		t.Fatal("recapItemHasNovelSubject = false, want true")
	}
	if !recapItemIsSubstantive(item, scope) {
		t.Fatal("recapItemIsSubstantive = false, want true")
	}
	if kept := filterLowInformationLiveItems(nil, []liveAnalysisItem{item}, timeline, scope, &liveAnalysisTreeMergeStats{}); len(kept) != 1 {
		t.Fatalf("filterLowInformationLiveItems kept %d items, want 1 (救済経路へ到達させる)", len(kept))
	}
}

// TestRecapMetaUtteranceStillRejected は抑制側の回帰。recapを理由にしない
// 代わりに、メタ発話・低情報itemは従来どおり除外され続ける。
func TestRecapMetaUtteranceStillRejected(t *testing.T) {
	texts := map[int64]string{
		1: "振り返ります。",
		2: "以上がここまでのまとめです。",
		3: "追加の論点があります。",
	}
	_, kept, stats := runRecapItemGate(t, texts, []int64{1, 2, 3}, nil, []liveAnalysisItem{
		{ID: "item-meta", Kind: "fact", Title: "ここまでのまとめ", Body: "以上がここまでのまとめです",
			Status: "open", EvidenceSequenceNos: []int64{2}, evidenceSpecified: true},
		{ID: "item-generic", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "追加の論点", Body: "追加の論点が存在する",
			Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true},
	})
	if len(kept) != 0 {
		t.Fatalf("kept = %v, want 0 (メタ発話は従来どおり除外)", itemTitles(kept))
	}
	if stats.LowInformationItemsRejected+stats.ReferenceRecapItemsRejected != 2 {
		t.Errorf("rejected low=%d recap=%d, want 2 total", stats.LowInformationItemsRejected, stats.ReferenceRecapItemsRejected)
	}
}

// ---------------------------------------------------------------------------
// recapから通常議論への復帰
// ---------------------------------------------------------------------------

// TestRecapExitsOnDiscussionResumption は、自然な議論再開表現でrecapが終わる
// ことを固定する。修正前は「次の話題へ移ります」型の表現しか解除条件が無く、
// 会議の残り全部がrecapのままだった。
func TestRecapExitsOnDiscussionResumption(t *testing.T) {
	for _, resumption := range []string{
		"では、再発防止策を決めましょう。",
		"では、対策を決めましょう。",
		"ここから今後の対応を話します。",
		"次に再発防止策を検討します。",
		"それでは原因の調査に移ります。",
		"今後どうするかを決めたいです。",
	} {
		texts := map[int64]string{
			1: "振り返ります。",
			2: "3階で接続不能が発生しました。",
			3: resumption,
			4: "監視アラートの閾値を1分へ変更する案があります。",
		}
		scope := evidenceScopeFromTexts(texts, 1, 2, 3, 4)
		timeline := classifyDiscourseTimelineWithModel(scope, nil)
		if timeline.Roles[2] != liveEvidenceReferenceRecap {
			t.Errorf("%q: Roles[2] = %q, want reference_recap (再開前はrecapのまま)", resumption, timeline.Roles[2])
		}
		if timeline.Roles[4] != liveEvidencePrimary {
			t.Errorf("%q: Roles[4] = %q, want primary (再開後は通常議論)", resumption, timeline.Roles[4])
		}
	}
}

// TestDiscussionResumptionDoesNotFireOnContentUtterance は誤検知の回帰。
// 文中に「では」を含むだけの内容発話は再開宣言にしない。
func TestDiscussionResumptionDoesNotFireOnContentUtterance(t *testing.T) {
	for _, text := range []string{
		"原因はVLAN設定ではなく、配線の問題だと考えます。",
		"3階でネットワーク障害が発生しました。",
		"影響範囲は2階と3階です。",
		"今後の対応として、チェックリストを作成することを必須にします。",
	} {
		if isDiscussionResumption(text) {
			t.Errorf("isDiscussionResumption(%q) = true, want false", text)
		}
	}
}

// TestRecapExitsOnModelSubstantiveRole は、ルールベースのrecapモード中でも
// LLMが明示的に substantive と判断した発話を一律に無視しないことを固定する。
func TestRecapExitsOnModelSubstantiveRole(t *testing.T) {
	texts := map[int64]string{1: "振り返ります。", 2: "3階で接続不能が発生しました。", 3: "2階でも遅延が発生しました。"}
	scope := evidenceScopeFromTexts(texts, 1, 2, 3)

	withoutModel := classifyDiscourseTimelineWithModel(scope, nil)
	if withoutModel.Roles[2] != liveEvidenceReferenceRecap {
		t.Fatalf("model無し: Roles[2] = %q, want reference_recap", withoutModel.Roles[2])
	}

	withModel := classifyDiscourseTimelineWithModel(scope, []liveUtteranceRoleRef{{SequenceNo: 2, Role: liveUtteranceSubstantive}})
	if withModel.Roles[2] != liveEvidencePrimary {
		t.Errorf("model=substantive: Roles[2] = %q, want primary", withModel.Roles[2])
	}
	if withModel.Roles[3] != liveEvidencePrimary {
		t.Errorf("model=substantive後: Roles[3] = %q, want primary (recapモードが解除される)", withModel.Roles[3])
	}
	// モデル単独のrecap判定は当該発話だけに効き、mode遷移は起こさない。
	solo := classifyDiscourseTimelineWithModel(
		evidenceScopeFromTexts(map[int64]string{1: "3階で接続不能が発生しました。", 2: "2階でも遅延が発生しました。"}, 1, 2),
		[]liveUtteranceRoleRef{{SequenceNo: 1, Role: liveUtteranceRecap}})
	if solo.Roles[1] != liveEvidenceReferenceRecap || solo.Roles[2] != liveEvidencePrimary {
		t.Errorf("model-only recap: Roles = %+v, want seq1=reference_recap seq2=primary", solo.Roles)
	}
}

// ---------------------------------------------------------------------------
// 指定ケース1〜7
// ---------------------------------------------------------------------------

// ケース1: 今回のSTT分割。ツリーが0ノードにならないことをフルパイプラインで確認する。
func TestRecapCase1_SplitSTTKeepsConcreteItems(t *testing.T) {
	texts := map[int64]string{
		1: "今日発生した障害について。",
		2: "振り返ります。",
		3: "午前9時20分ごろ、3階でネットワークへ接続できなくなりました。",
		4: "2階でも遅延が発生しました。",
	}
	scope := evidenceScopeFromTexts(texts, 1, 2, 3, 4)
	diff := `{"summary":"障害の振り返り","currentTopic":"ネットワーク障害","items":[
	  {"clientKey":"issue-3f","kind":"issue","subtype":"discussion","severity":"high","title":"3階でネットワークへ接続不能","body":"午前9時20分ごろ、3階でネットワークへ接続できなくなった","status":"open","evidenceSequenceNos":[3]},
	  {"clientKey":"fact-2f","kind":"fact","subtype":"","severity":"medium","title":"2階でも遅延が発生","body":"2階でも通信遅延が発生した","status":"open","evidenceSequenceNos":[4]}
	],"newTopics":[],"assignments":[
	  {"nodeId":"issue-3f","parentTopicId":"topic-unclassified","confidence":0.6,"reason":"障害報告"},
	  {"nodeId":"fact-2f","parentTopicId":"topic-unclassified","confidence":0.6,"reason":"障害報告"}
	],"resolvedIds":[],"resolutionUpdates":[],"utteranceRoles":[]}`

	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, nil, nil, 1, []int64{1, 2, 3, 4}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 2 {
		t.Fatalf("items = %v, want 2 (3階の接続不能と2階の遅延)", itemTitles(state.Items))
	}
	if len(state.Tree.Nodes) == 0 {
		t.Fatal("tree.nodes = 0, want > 0 (ツリーが空にならないこと)")
	}
	t.Logf("items=%v treeNodes=%d", itemTitles(state.Items), len(state.Tree.Nodes))
}

// ケース2: 既出情報の単純なまとめ。重複itemを作らない(抑制機能の回帰)。
func TestRecapCase2_PlainRestatementDoesNotDuplicate(t *testing.T) {
	previous := []liveAnalysisItem{
		{ID: "issue-3f", Kind: "issue", Subtype: issueSubtypeDiscussion,
			Title: "3階でネットワーク障害が発生", Body: "3階でネットワーク障害が発生しました",
			Status: "open", EvidenceSequenceNos: []int64{1}},
	}
	texts := map[int64]string{
		1: "3階でネットワーク障害が発生しました。",
		2: "ここまでをまとめます。",
		3: "3階でネットワーク障害が発生しました。",
	}
	_, kept, stats := runRecapItemGate(t, texts, []int64{3}, previous, []liveAnalysisItem{
		{ID: "issue-3f-dup", Kind: "issue", Subtype: issueSubtypeDiscussion,
			Title: "3階でネットワーク障害が発生", Body: "3階でネットワーク障害が発生しました",
			Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true},
	})
	if len(kept) != 1 {
		t.Fatalf("kept = %v, want 1", itemTitles(kept))
	}
	if kept[0].ID != "issue-3f" {
		t.Fatalf("kept[0].ID = %q, want issue-3f (既存itemへマージされる)", kept[0].ID)
	}
	if stats.ReferenceRecapItemsMerged != 1 {
		t.Errorf("ReferenceRecapItemsMerged = %d, want 1", stats.ReferenceRecapItemsMerged)
	}
	if got := recapDecisionFor(stats, "issue-3f-dup"); got != recapDecisionMergedExisting {
		t.Errorf("decision = %q, want %q", got, recapDecisionMergedExisting)
	}
}

// ケース3: 既出情報と新規情報の混在。3階は重複させず、2階の初出は保持する。
func TestRecapCase3_MixedKnownAndNewInformation(t *testing.T) {
	previous := []liveAnalysisItem{
		{ID: "issue-3f", Kind: "issue", Subtype: issueSubtypeDiscussion,
			Title: "3階でネットワーク障害が発生", Body: "3階でネットワーク障害が発生しました",
			Status: "open", EvidenceSequenceNos: []int64{1}},
	}
	texts := map[int64]string{
		1: "3階でネットワーク障害が発生しました。",
		2: "ここまでを整理します。",
		3: "3階の障害に加えて、2階でも通信遅延が確認されました。",
	}
	_, kept, _ := runRecapItemGate(t, texts, []int64{3}, previous, []liveAnalysisItem{
		{ID: "issue-3f-dup", Kind: "issue", Subtype: issueSubtypeDiscussion,
			Title: "3階でネットワーク障害が発生", Body: "3階でネットワーク障害が発生しました",
			Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true},
		{ID: "fact-2f", Kind: "fact",
			Title: "2階でも通信遅延を確認", Body: "2階でも通信遅延が確認された",
			Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true},
	})
	if len(kept) != 2 {
		t.Fatalf("kept = %v, want 2 (既存へのマージ1件 + 新規1件)", itemTitles(kept))
	}
	var mergedExisting, novel bool
	for _, item := range kept {
		if item.ID == "issue-3f" {
			mergedExisting = true
		}
		if item.ID == "fact-2f" {
			novel = true
		}
	}
	if !mergedExisting {
		t.Errorf("3階のitemが既存idへマージされていない: %+v", itemTitles(kept))
	}
	if !novel {
		t.Errorf("2階の初出情報が失われた: %+v", itemTitles(kept))
	}
}

// ケース4: recap後の通常議論復帰。再発防止策の提案がrecap扱いで消えない。
func TestRecapCase4_ResumedDiscussionSurvives(t *testing.T) {
	texts := map[int64]string{
		1: "ここまでをまとめます。",
		2: "3階でネットワーク障害が発生しました。",
		3: "では、再発防止策を決めましょう。",
		4: "監視アラートの閾値を5分から1分へ変更する案があります。",
	}
	timeline, kept, _ := runRecapItemGate(t, texts, []int64{1, 2, 3, 4}, nil, []liveAnalysisItem{
		{ID: "issue-3f", Kind: "issue", Subtype: issueSubtypeDiscussion,
			Title: "3階でネットワーク障害が発生", Body: "3階でネットワーク障害が発生した",
			Status: "open", EvidenceSequenceNos: []int64{2}, evidenceSpecified: true},
		{ID: "todo-threshold", Kind: "todo",
			Title: "監視アラートの閾値を1分へ変更", Body: "監視アラートの閾値を5分から1分へ変更する案がある",
			Status: "open", EvidenceSequenceNos: []int64{4}, evidenceSpecified: true},
	})
	if timeline.Roles[4] != liveEvidencePrimary {
		t.Errorf("Roles[4] = %q, want primary (議論再開後は通常議論)", timeline.Roles[4])
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %v, want 2", itemTitles(kept))
	}
}

// ケース5: 空ツリーから始まる振り返り会議。
func TestRecapCase5_RecapOnlyMeetingFromEmptyTree(t *testing.T) {
	texts := map[int64]string{
		1: "今日の障害について振り返ります。",
		2: "午前中に認証サーバーが停止しました。",
		3: "復旧までに約20分かかりました。",
	}
	// 1発話にまとまった宣言ではrecapに入らないため、STT分割版でも確認する。
	splitTexts := map[int64]string{
		1: "今日の障害について。",
		2: "振り返ります。",
		3: "午前中に認証サーバーが停止しました。",
		4: "復旧までに約20分かかりました。",
	}
	for name, tc := range map[string]struct {
		texts map[int64]string
		round []int64
		diff  []liveAnalysisItem
	}{
		"1発話宣言": {texts, []int64{1, 2, 3}, []liveAnalysisItem{
			{ID: "fact-auth", Kind: "fact", Title: "認証サーバーが停止", Body: "午前中に認証サーバーが停止した",
				Status: "open", EvidenceSequenceNos: []int64{2}, evidenceSpecified: true},
			{ID: "fact-recovery", Kind: "fact", Title: "復旧まで約20分", Body: "復旧までに約20分かかった",
				Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true},
		}},
		"STT分割": {splitTexts, []int64{1, 2, 3, 4}, []liveAnalysisItem{
			{ID: "fact-auth", Kind: "fact", Title: "認証サーバーが停止", Body: "午前中に認証サーバーが停止した",
				Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true},
			{ID: "fact-recovery", Kind: "fact", Title: "復旧まで約20分", Body: "復旧までに約20分かかった",
				Status: "open", EvidenceSequenceNos: []int64{4}, evidenceSpecified: true},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			_, kept, _ := runRecapItemGate(t, tc.texts, tc.round, nil, tc.diff)
			if len(kept) != 2 {
				t.Fatalf("kept = %v, want 2 (空ツリーのまま終了しない)", itemTitles(kept))
			}
		})
	}
}

// ケース6: 曖昧なitemと具体的なitemの逆転防止。
// grounded であることが reference_recap 下で不利にならないことを確認する。
func TestRecapCase6_GroundedItemIsNotWorseThanTentative(t *testing.T) {
	texts := map[int64]string{
		1: "振り返ります。",
		2: "この点は引き続き確認が必要です。",
		3: "3階のネットワーク接続不能について原因調査が必要です。",
	}
	vague := liveAnalysisItem{ID: "issue-vague", Kind: "issue", Subtype: issueSubtypeConfirmation,
		Title: "この点は引き続き確認が必要", Body: "この点は引き続き確認が必要",
		Status: "open", EvidenceSequenceNos: []int64{2}, evidenceSpecified: true}
	concrete := liveAnalysisItem{ID: "issue-concrete", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title: "3階のネットワーク接続不能の原因調査", Body: "3階のネットワーク接続不能について原因調査が必要",
		Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true}

	_, kept, stats := runRecapItemGate(t, texts, []int64{2, 3}, nil, []liveAnalysisItem{vague, concrete})

	var keptConcrete bool
	for _, item := range kept {
		if item.ID == "issue-concrete" {
			keptConcrete = true
		}
	}
	if !keptConcrete {
		t.Fatalf("具体的なissueが破棄された: kept=%v decisions=%+v", itemTitles(kept), stats.RecapDecisions)
	}
	// 具体的itemが曖昧itemより先に落ちていないことが本質。曖昧itemの採否は
	// 情報量ゲート側の判断に委ねる。
	if len(kept) == 1 && kept[0].ID != "issue-concrete" {
		t.Fatalf("曖昧itemだけが残った: %v", itemTitles(kept))
	}
}

// ケース7: recap中に初出となる todo / decision / risk / fact。
// kindにかかわらず、recapだけを理由に一律破棄されない。
func TestRecapCase7_AllKindsSurviveWhenNovelAndConcrete(t *testing.T) {
	texts := map[int64]string{
		1: "振り返ります。",
		2: "田中さんが月曜日までにログを確認します。",
		3: "監視間隔を5分から1分へ変更することにします。",
		4: "証明書更新を放置すると再接続できなくなるおそれがあります。",
		5: "障害は午前9時20分に発生しました。",
	}
	diff := []liveAnalysisItem{
		{ID: "todo-log", Kind: "todo", Title: "田中さんがログを確認", Body: "田中さんが月曜日までにログを確認する",
			Status: "open", EvidenceSequenceNos: []int64{2}, evidenceSpecified: true},
		{ID: "decision-interval", Kind: "decision", Title: "監視間隔を1分へ変更", Body: "監視間隔を5分から1分へ変更する",
			Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true},
		{ID: "risk-cert", Kind: "risk", Title: "証明書更新放置で再接続不能", Body: "証明書更新を放置すると再接続できなくなるおそれがある",
			Status: "open", EvidenceSequenceNos: []int64{4}, evidenceSpecified: true},
		{ID: "fact-onset", Kind: "fact", Title: "障害は午前9時20分に発生", Body: "障害は午前9時20分に発生した",
			Status: "open", EvidenceSequenceNos: []int64{5}, evidenceSpecified: true},
	}
	_, kept, stats := runRecapItemGate(t, texts, []int64{2, 3, 4, 5}, nil, diff)

	if len(kept) != len(diff) {
		t.Fatalf("kept = %v, want %d (kind別に一律破棄されない)", itemTitles(kept), len(diff))
	}
	for _, item := range diff {
		// evidenceがrecapと判定された分だけrecap decisionが残る。「変更する
		// ことにします」のように訂正・更新表現を含む発話は correction role に
		// なり、recapゲートを通らずに保持される(それも正しい経路)。
		if got := recapDecisionFor(stats, item.ID); got != "" && got != recapDecisionRetainedNovel {
			t.Errorf("kind=%s decision = %q, want %q or none", item.Kind, got, recapDecisionRetainedNovel)
		}
	}
}

// TestRecapDecisionLogFieldsArePopulated は診断ログの回帰。破棄・保持・マージの
// いずれでも判断材料が構造化ログへ残ることを確認する。
func TestRecapDecisionLogFieldsArePopulated(t *testing.T) {
	previous := []liveAnalysisItem{
		{ID: "issue-3f", Kind: "issue", Subtype: issueSubtypeDiscussion,
			Title: "3階でネットワーク障害が発生", Body: "3階でネットワーク障害が発生しました",
			Status: "open", EvidenceSequenceNos: []int64{1}},
	}
	texts := map[int64]string{
		1: "3階でネットワーク障害が発生しました。",
		2: "ここまでをまとめます。",
		3: "3階でネットワーク障害が発生しました。",
		4: "2階でも通信遅延が確認されました。",
		5: "以上がまとめです。",
	}
	_, _, stats := runRecapItemGate(t, texts, []int64{3, 4, 5}, previous, []liveAnalysisItem{
		{ID: "issue-3f-dup", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "3階でネットワーク障害が発生",
			Body: "3階でネットワーク障害が発生しました", Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true},
		{ID: "fact-2f", Kind: "fact", Title: "2階でも通信遅延を確認", Body: "2階でも通信遅延が確認された",
			Status: "open", EvidenceSequenceNos: []int64{4}, evidenceSpecified: true},
		{ID: "fact-meta", Kind: "fact", Title: "まとめ", Body: "以上がまとめです",
			Status: "open", EvidenceSequenceNos: []int64{5}, evidenceSpecified: true},
	})

	want := map[string]string{
		"issue-3f-dup": recapDecisionMergedExisting,
		"fact-2f":      recapDecisionRetainedNovel,
	}
	for itemID, decision := range want {
		if got := recapDecisionFor(stats, itemID); got != decision {
			t.Errorf("decision for %s = %q, want %q", itemID, got, decision)
		}
	}
	if got := recapDecisionFor(stats, "fact-meta"); got != recapDecisionRejectedLowInformation && got != recapDecisionRejectedDuplicate {
		t.Errorf("decision for fact-meta = %q, want a rejection", got)
	}
	for _, decision := range stats.RecapDecisions {
		if decision.ItemID == "" || decision.Kind == "" || decision.DetectedRole != liveUtteranceRecap || decision.Decision == "" {
			t.Errorf("incomplete decision row: %+v", decision)
		}
		if decision.Decision == recapDecisionMergedExisting && decision.ExistingMatchID == "" {
			t.Errorf("merged decision without match id: %+v", decision)
		}
	}
}

// TestRecapSessionA4B76401EndToEnd は対象セッションのフルパイプライン再現。
// 完了条件「今回の再現ケースでツリーが0ノードにならない」を直接検証する。
func TestRecapSessionA4B76401EndToEnd(t *testing.T) {
	scope := evidenceScopeFromTexts(sessionA4B76401Transcript, 3, 4, 5, 6, 7)
	diff := `{"summary":"名古屋支社のネットワーク障害","currentTopic":"ネットワーク障害","items":[
	  {"clientKey":"issue-outage","kind":"issue","subtype":"discussion","severity":"high","title":"3階を中心に社内ネットワークへ接続不能","body":"本日午前9時20分ごろ、名古屋支社の3階を中心に接続不能の報告があった","status":"open","evidenceSequenceNos":[3]},
	  {"clientKey":"fact-2f","kind":"fact","subtype":"","severity":"medium","title":"2階の一部でも通信遅延が発生","body":"正確には2階の一部でも通信遅延が発生していた","status":"open","evidenceSequenceNos":[4]},
	  {"clientKey":"fact-scope","kind":"fact","subtype":"","severity":"medium","title":"有線LAN・無線LAN・ファイルサーバーに影響","body":"有線LAN、無線LAN、ファイルサーバー、社内システムへの接続が影響を受けた","status":"open","evidenceSequenceNos":[5]},
	  {"clientKey":"fact-router","kind":"fact","subtype":"","severity":"medium","title":"ルーターとファイアウォールに異常なし","body":"障害発生後に確認したが、どちらにも明確な異常はなかった","status":"open","evidenceSequenceNos":[7]}
	],"newTopics":[],"assignments":[
	  {"nodeId":"issue-outage","parentTopicId":"topic-unclassified","confidence":0.7,"reason":"障害報告"},
	  {"nodeId":"fact-2f","parentTopicId":"topic-unclassified","confidence":0.7,"reason":"影響範囲"},
	  {"nodeId":"fact-scope","parentTopicId":"topic-unclassified","confidence":0.7,"reason":"影響範囲"},
	  {"nodeId":"fact-router","parentTopicId":"topic-unclassified","confidence":0.7,"reason":"切り分け"}
	],"resolvedIds":[],"resolutionUpdates":[],"utteranceRoles":[]}`

	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, nil, nil, 1, []int64{3, 4, 5, 6, 7}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) == 0 {
		t.Fatal("items = 0, want > 0 (version1〜3が空だった事象の回帰)")
	}
	if len(state.Tree.Nodes) == 0 {
		t.Fatal("tree.nodes = 0, want > 0")
	}
	for _, node := range state.Tree.Nodes {
		t.Logf("node id=%s kind=%s label=%s", node.ID, node.Kind, node.Label)
	}
	t.Logf("items=%d treeNodes=%d titles=%v", len(state.Items), len(state.Tree.Nodes), itemTitles(state.Items))
}
