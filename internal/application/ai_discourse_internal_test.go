package application

import (
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestClassifyDiscourseAct(t *testing.T) {
	cases := []struct {
		text string
		want discourseAct
	}{
		// 会話制御発話(session_497ed2b0aedf9dc6 の実発話を含む)。
		{"以上をまとめます。", discourseRecapIntro},
		{"ここまでを整理します。", discourseRecapIntro},
		{"まとめると", discourseRecapIntro},
		{"結論として確認します", discourseRecapIntro},
		{"それでは、以上をまとめます。", discourseRecapIntro},
		{"次の話題へ移ります。", discourseTopicTransition},
		{"では次の議題に移りましょう。", discourseTopicTransition},
		{"以上でこの議題を終わります。", discourseTopicTransition},
		{"ここで、アジェンダにはなかった別の問題があります。", discourseTopicTransition},
		{"ここで、マジェンダにはなかった別の問題があります。", discourseTopicTransition},
		{"追加の論点です。", discourseTopicTransition},
		{"本題とは別ですが。", discourseTopicTransition},
		{"少し話を変えます。", discourseTopicTransition},
		{"では次に進みます。", discourseTopicTransition},
		{"ここから別件です。", discourseTopicTransition},
		{"会議を開始します。", discourseMeetingControl},
		{"よろしくお願いします。", discourseMeetingControl},
		{"お疲れ様でした。", discourseMeetingControl},
		{"では、今日はここまでにします。ありがとうございました。", discourseMeetingControl},
		{"以上で終了します。", discourseMeetingControl},
		{"これで終わります。", discourseMeetingControl},
		// 議論内容(制御表現で始まっても実質内容を含むものは content)。
		{"以上をまとめますと、観測地点は3箇所になります。", discourseContent},
		{"渡り鳥の調査計画を検討します。", discourseContent},
		{"観測地点が不足している。", discourseContent},
		{"強風日の測定条件の決定基準を確定する", discourseContent},
		{"住民説明資料の公開方針を検討する", discourseContent},
		{"専門家による植物種の予備調査の検討", discourseContent},
		{"今回はフォームに世界遺産一覧を入れないことにします。", discourseContent},
		{"", discourseContent},
	}
	for _, tc := range cases {
		if got := classifyDiscourseAct(tc.text); got != tc.want {
			t.Errorf("classifyDiscourseAct(%q) = %s, want %s", tc.text, got, tc.want)
		}
	}
}

func TestAgendaNoAgendaSpanClosesOnExplicitReturn(t *testing.T) {
	mc := &meetingContext{Title: "出張申請の改善", Agenda: []agendaItem{
		{ID: "agenda-1", Title: "出張申請の改善", Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "私用日程を含む交通費精算", Role: agendaRolePrimary},
	}}
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "出張申請の改善点を確認します。",
		2: "京都の寺院は世界遺産でしたね。",
		3: "清水寺にも行きたいです。",
		4: "話を戻します。",
		5: "私用日程がある場合は会社負担との差額を本人負担にします。",
		6: "山下さんが経理へ基準を確認してください。",
	}, 1, 2, 3, 4, 5, 6)
	stats := &liveAnalysisTreeMergeStats{}
	spans := detectAgendaContextSpans(scope, mc, stats)
	if len(spans) != 1 || spans[0].Mode != agendaContextModeNoAgenda || spans[0].StartSequenceNo != 2 || spans[0].EndSequenceNo != 3 {
		t.Fatalf("spans=%+v", spans)
	}
	if stats.NoAgendaSpansClosed != 1 || stats.ExplicitAgendaReentries != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	if mode, _, _ := agendaContextForEvidence([]int64{5, 6}, spans); mode != "" {
		t.Fatalf("returned evidence retained stale mode=%q", mode)
	}
}

func TestAgendaNoAgendaSpanClosesAfterConsecutiveSemanticReentry(t *testing.T) {
	mc := &meetingContext{
		Title: "出張申請と経費精算", Purpose: "私用日程を含む交通費と申請フォームを改善する",
		Agenda: []agendaItem{
			{ID: "agenda-1", Title: "私用日程を含む交通費精算", Role: agendaRolePrimary},
			{ID: "agenda-2", Title: "申請フォームの入力改善", Role: agendaRolePrimary},
		},
	}
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "出張申請の改善点を確認します。",
		2: "京都の世界遺産の話です。",
		3: "清水寺と金閣寺を回りたいです。",
		4: "私用日程を含む場合の運賃差額をどう入力するか確認します。",
		5: "申請フォームに私用日程欄を追加します。",
	}, 1, 2, 3, 4, 5)
	stats := &liveAnalysisTreeMergeStats{}
	spans := detectAgendaContextSpans(scope, mc, stats)
	if len(spans) == 0 || spans[0].Mode != agendaContextModeNoAgenda || spans[0].StartSequenceNo != 2 || spans[0].EndSequenceNo != 3 {
		t.Fatalf("spans=%+v", spans)
	}
	if stats.ImplicitAgendaReentries != 1 || stats.NoAgendaSpansClosed != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestExplicitNoAgendaContentRemainsProtectedWithoutReentry(t *testing.T) {
	mc := &meetingContext{Title: "出張申請", Agenda: []agendaItem{{ID: "agenda-1", Title: "申請フォーム", Role: agendaRolePrimary}}}
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "ここからはアジェンダ外ですが、VPN証明書も確認が必要です。",
		2: "証明書は来月末に期限切れになります。",
	}, 1, 2)
	spans := detectAgendaContextSpans(scope, mc, nil)
	if len(spans) != 1 || spans[0].Mode != agendaContextModeNoAgenda || spans[0].StartSequenceNo != 1 || spans[0].EndSequenceNo != 2 || !spans[0].Explicit {
		t.Fatalf("spans=%+v", spans)
	}
}

func TestBusinessItemAdditionDoesNotStartNoAgendaSpan(t *testing.T) {
	text := "では、申請フォームに私用日程を含むかという項目を追加して、通常経路との差額を本人負担にします。"
	if isExplicitNoAgendaTransition(text) {
		t.Fatal("business item addition was mistaken for an agenda-external transition")
	}
	mc := &meetingContext{Title: "出張申請", Purpose: "申請フォームを改善する", Agenda: []agendaItem{{ID: "agenda-1", Title: "申請フォームの入力改善", Role: agendaRolePrimary}}}
	spans := detectAgendaContextSpans(evidenceScopeFromTexts(map[int64]string{23: text}, 23), mc, nil)
	for _, span := range spans {
		if span.Mode == agendaContextModeNoAgenda {
			t.Fatalf("spans=%+v", spans)
		}
	}
}

func TestLowConfidenceNoAgendaSpanCannotOverrideStrongAgendaAssignment(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "申請フォームの改善", Role: agendaRolePrimary}}}
	item := liveAnalysisItem{ID: "todo-form", Kind: "todo", Title: "申請フォーム修正案を作成", Body: "山下さんが今週中に作成する", EvidenceSequenceNos: []int64{8}}
	assignments := []treeAssignment{{NodeID: item.ID, ParentTopicID: "agenda-1", Confidence: 0.90, Reason: "direct model assignment"}}
	stats := &liveAnalysisTreeMergeStats{}
	updated, topics := applyAgendaContextAssignments(assignments, nil, nil, []liveAnalysisItem{item}, []liveAnalysisItem{item}, nil, []agendaContextSpan{{Mode: agendaContextModeNoAgenda, StartSequenceNo: 8, EndSequenceNo: 8, Confidence: 0.60}}, mc, stats)
	if len(updated) != 1 || updated[0].ParentTopicID != "agenda-1" || len(topics) != 0 || stats.LowConfidenceNoAgendaOverridesRejected != 1 {
		t.Fatalf("assignments=%+v topics=%+v stats=%+v", updated, topics, stats)
	}
}

func TestModelUtteranceRoleMarksParaphrasedTransitionWithoutSuppressingFollowingContent(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		17: "場面を切り替えましょう。",
		18: "VPN証明書が来月末に期限切れになります。",
	}, 17, 18)
	timeline := classifyDiscourseTimelineWithModel(scope, []liveUtteranceRoleRef{
		{SequenceNo: 17, Role: liveUtteranceDiscourseTransition},
		{SequenceNo: 18, Role: liveUtteranceSubstantive},
	})
	if timeline.Roles[17] != liveEvidenceDiscourseOnly || timeline.Roles[18] != liveEvidencePrimary {
		t.Fatalf("roles=%+v detected=%+v", timeline.Roles, timeline.DetectedRoles)
	}
}

func TestAgendaExternalDiscourseTransitionStartsNoAgendaSpan(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		17: "ここで、アジェンダにはなかった別の問題があります。",
		18: "VPN証明書が来月末に期限切れになります。",
	}, 17, 18)
	stats := &liveAnalysisTreeMergeStats{}
	spans := detectAgendaContextSpans(scope, classificationFixtureContext(), stats)
	if len(spans) != 1 || spans[0].Mode != agendaContextModeNoAgenda || spans[0].StartSequenceNo != 17 || stats.NoAgendaSpanCount != 1 {
		t.Fatalf("spans=%+v stats=%+v", spans, stats)
	}
}

func TestNoAgendaDetectionKeepsSplitCountermeasuresInsideAgenda(t *testing.T) {
	mc := &meetingContext{
		Title:      "名古屋支社ネットワーク障害の振り返りと再発防止会議",
		Purpose:    "ネットワーク障害の原因、復旧対応、再発防止策、未解決事項とTODOを明確にする",
		Background: "アクセススイッチのVLAN設定漏れと監視ログ不足を確認する",
		Agenda: []agendaItem{
			{ID: "agenda-3", Title: "再発防止策", Role: agendaRolePrimary},
			{ID: "agenda-4", Title: "未解決事項と次回までの対応確認", Role: agendaRolePrimary},
		},
	}
	texts := map[int64]string{
		13: "まず、ネットワーク機器を交換する際は、作業者とは別の担当者が。",
		14: "設定内容を確認するダブルチェックを必須にします。",
		15: "また、交換前後でVLANごとの疎通確認を実施するチェックリストを作成します。",
		16: "の運用を次回の機器交換から適用することにします。",
		17: "山下さんが金曜日までにスイッチ交換用チェックリスト案を作成します。",
		18: "さらに、VLANごとの通信異常を早期に検知できるよう。",
		19: "監視項目へVLAN単位の疎通確認を追加します。",
		20: "ただし、監視対象を増やすとアラートが多くなりすぎる可能性があります。",
		21: "監視間隔と通知条件は次回までに検討します。",
		22: "ここで、アジェンダにはなかった別の問題があります。",
		23: "VPN装置の証明書が来月末に期限切れになります。",
	}
	scope := evidenceScopeFromTexts(texts, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23)
	scope.Segments = make(map[int64]domain.TranscriptSegment, len(texts))
	for sequenceNo, text := range texts {
		scope.Segments[sequenceNo] = domain.TranscriptSegment{SequenceNo: sequenceNo, SpeakerID: "speaker-1", Text: text, IsFinal: true}
	}
	stats := &liveAnalysisTreeMergeStats{}
	spans := detectAgendaContextSpans(scope, mc, stats)
	noAgenda := make([]agendaContextSpan, 0)
	for _, span := range spans {
		if span.Mode == agendaContextModeNoAgenda {
			noAgenda = append(noAgenda, span)
		}
	}
	if len(noAgenda) != 1 || noAgenda[0].StartSequenceNo != 22 || !noAgenda[0].Explicit || noAgenda[0].Confidence != 1 {
		t.Fatalf("spans=%+v stats=%+v", spans, stats)
	}
	for _, falseStart := range []int64{14, 18} {
		if containsInt64(stats.NoAgendaSpanStartSequences, falseStart) {
			t.Fatalf("false no-agenda start %d survived: %+v", falseStart, stats.NoAgendaSpanStartSequences)
		}
	}
}

func TestNoAgendaModifierPhrasesAreNotTransitions(t *testing.T) {
	for _, text := range []string{
		"作業者とは別の担当者が確認します。",
		"別の機器で疎通を確認します。",
		"別の方法を採用します。",
	} {
		if isExplicitNoAgendaTransition(text) {
			t.Fatalf("modifier was treated as no-agenda transition: %q", text)
		}
	}
	if !isExplicitNoAgendaTransition("ここからはアジェンダ外の別件です。") {
		t.Fatal("explicit agenda-external transition was not detected")
	}
	for _, text := range []string{"話は変わりますが。", "別の話です。", "本題外です。"} {
		if !isExplicitNoAgendaTransition(text) {
			t.Fatalf("explicit topic transition was not detected: %q", text)
		}
	}
}

func TestDiscourseTransitionProposalIsDroppedButFollowingSubstantiveItemSurvives(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		17: "ここで、別の問題があります。",
		18: "VPN証明書が来月末に期限切れになり、リモート接続が停止する可能性があります。",
	}, 17, 18)
	diff := `{
		"summary":"VPN証明書の確認","currentTopic":"VPN証明書",
		"utteranceRoles":[
			{"sequenceNo":17,"role":"discourse_transition"},
			{"sequenceNo":18,"role":"substantive"}
		],
		"items":[
			{"id":"item-meta","kind":"todo","severity":"medium","title":"別の問題の存在を確認","body":"追加論点","status":"open","evidenceSequenceNos":[17]},
			{"id":"item-vpn-risk","kind":"risk","severity":"high","title":"VPN証明書が来月末に期限切れ","body":"リモート接続が停止する可能性","status":"open","evidenceSequenceNos":[18]}
		],
		"newTopics":[{"id":"topic-next","label":"次の話題です"}],
		"assignments":[{"nodeId":"item-meta","parentTopicId":"topic-next","confidence":0.9,"reason":"model"}]
	}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, nil, nil, 1, []int64{17, 18}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatalf("parse transition round: %v", err)
	}
	state := previousLiveAnalysisState(raw)
	if itemByID(state.Items, "item-meta") != nil || itemByID(state.Items, "item-vpn-risk") == nil {
		t.Fatalf("items=%+v", state.Items)
	}
	if len(state.EmergingTopics) != 0 {
		t.Fatalf("transition-only candidate survived: %+v", state.EmergingTopics)
	}
	if stats.LowInformationItemsRejected != 1 || stats.DiscourseOnlyItemsRejected != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestIsDiscourseOnlyItem(t *testing.T) {
	if !isDiscourseOnlyItem("以上をまとめます", "") {
		t.Errorf("recap intro title must be discourse-only")
	}
	if isDiscourseOnlyItem("以上をまとめます", "観測地点は3箇所に増設する") {
		t.Errorf("item with substantive body must not be discourse-only")
	}
	if isDiscourseOnlyItem("観測地点の追加設置", "") {
		t.Errorf("substantive title must not be discourse-only")
	}
	if isDiscourseOnlyItem("", "") {
		t.Errorf("empty item is not classified as discourse-only")
	}
}

func TestDiscourseTimelineMarksRecapContentAsReferenceUntilTransition(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		30: "以上をまとめます。",
		31: "決定事項は渡り鳥を三地点で調査することです。",
		32: "未解決の課題は強風日の風速基準です。",
		33: "次の議題へ移ります。",
		34: "新しい施工計画を検討します。",
	}, 30, 31, 32, 33, 34)
	timeline := classifyDiscourseTimeline(scope)
	if timeline.Roles[30] != liveEvidenceDiscourseOnly || timeline.Roles[31] != liveEvidenceReferenceRecap || timeline.Roles[32] != liveEvidenceReferenceRecap || timeline.Roles[33] != liveEvidenceDiscourseOnly || timeline.Roles[34] != liveEvidencePrimary {
		t.Fatalf("roles=%+v transitions=%+v", timeline.Roles, timeline.Transitions)
	}
}

// 対象session(session_497ed2b0aedf9dc6)の回帰: 「以上をまとめます」発話から
// fact itemと新topic候補が作られ、recap再言及から重複issueがcandidate証拠に
// なった。制御発話はitemにもcandidateにもしない。
func TestDiscourseOnlySpeechDoesNotBecomeItemOrCandidate(t *testing.T) {
	mc := classificationFixtureContext()
	diff := `{
		"summary": "まとめの区間",
		"currentTopic": "まとめ",
		"items": [
			{"id": "fact-recap-intro", "kind": "fact", "severity": "medium", "title": "以上をまとめます", "body": "", "status": "open"}
		],
		"newTopics": [{"id": "topic-recap", "label": "以上をまとめます"}],
		"assignments": [
			{"nodeId": "fact-recap-intro", "parentTopicId": "topic-recap", "confidence": 0.7, "reason": "まとめ"}
		]
	}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	assertTreeInvariants(t, merged.Tree)
	if item := itemByID(merged.Items, "fact-recap-intro"); item != nil {
		t.Fatalf("discourse-only item must be rejected, got %+v", item)
	}
	if node := treeNodeByID(merged.Tree, "fact-recap-intro"); node != nil {
		t.Fatalf("discourse-only node must not appear in tree, got %+v", node)
	}
	if len(merged.EmergingTopics) != 0 {
		t.Fatalf("discourse-only label must not create a candidate, got %+v", merged.EmergingTopics)
	}
}

// subjectが空・会話制御発話・証拠と主題不一致のcandidateは昇格しない。
func TestCandidateSubjectIncoherenceBlocksPromotion(t *testing.T) {
	cfg := TreeClassificationConfig{}.normalized()
	itemAt := func(items []liveAnalysisItem) func(string) *liveAnalysisItem {
		return func(id string) *liveAnalysisItem {
			for i := range items {
				if items[i].ID == id {
					return &items[i]
				}
			}
			return nil
		}
	}

	// 対象sessionの実例: candidate「公開方針・情報公開」に強風条件と
	// 説明会日程のitemだけが集まった(labelと証拠が全て不一致)。
	mismatched := emergingTopicCandidate{
		ID: "candidate-mismatch", Label: "公開方針・情報公開",
		EvidenceItemIDs: []string{"todo-wind", "todo-schedule"},
	}
	mismatchedItems := []liveAnalysisItem{
		{ID: "todo-wind", Kind: "todo", Title: "強風日の測定条件の決定基準を確定する"},
		{ID: "todo-schedule", Kind: "todo", Title: "住民説明会の開催日程を確定する"},
	}
	if reason := candidateSubjectIncoherenceReason(mismatched, itemAt(mismatchedItems), cfg); reason != "subject_incoherent" {
		t.Errorf("mismatched candidate reason = %q, want subject_incoherent", reason)
	}

	empty := emergingTopicCandidate{ID: "candidate-empty", Label: "", EvidenceItemIDs: []string{"todo-wind"}}
	if reason := candidateSubjectIncoherenceReason(empty, itemAt(mismatchedItems), cfg); reason != "subject_empty" {
		t.Errorf("empty label reason = %q, want subject_empty", reason)
	}

	discourse := emergingTopicCandidate{ID: "candidate-discourse", Label: "以上をまとめます", EvidenceItemIDs: []string{"todo-wind"}}
	if reason := candidateSubjectIncoherenceReason(discourse, itemAt(mismatchedItems), cfg); reason != "discourse_only_label" {
		t.Errorf("discourse label reason = %q, want discourse_only_label", reason)
	}

	// 主題が一致する証拠を持つcandidateは昇格を保留しない。
	coherentCandidate := emergingTopicCandidate{
		ID: "candidate-plant", Label: "希少植物の事前調査",
		EvidenceItemIDs: []string{"todo-plant", "question-plant"},
	}
	plantItems := []liveAnalysisItem{
		{ID: "todo-plant", Kind: "todo", Title: "専門家による植物種の予備調査の検討"},
		{ID: "question-plant", Kind: "question", Title: "植物の種類を確認するため予備調査を実施するか"},
	}
	if reason := candidateSubjectIncoherenceReason(coherentCandidate, itemAt(plantItems), cfg); reason != "" {
		t.Errorf("coherent candidate reason = %q, want empty", reason)
	}
}

func TestCandidateOriginalSubjectRejectsUnrelatedMutation(t *testing.T) {
	candidate := emergingTopicCandidate{ID: "candidate-plant", Label: "湿地・希少植物調査", Description: "植物種類の確認"}
	initializeCandidateSubject(&candidate)
	original := candidate.OriginalSubject
	if updateCandidateSubject(&candidate, "強風日の風速基準", "騒音測定条件") {
		t.Fatal("unrelated candidate subject mutation was accepted")
	}
	if candidate.OriginalSubject != original || candidate.Label != "湿地・希少植物調査" {
		t.Fatalf("candidate mutated=%+v", candidate)
	}
}
