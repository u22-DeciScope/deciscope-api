package application

import (
	"strings"
	"testing"
)

// Regression replay for session_0610198d8187226e. The transcript text is the
// persisted final transcript; the model diff deliberately reproduces the
// problematic proposals (agenda-forced Kyoto item, generic question, and a
// meeting-end decision) so server-side semantic guards remain testable without
// calling an external model or mutating the development database.
func TestSession0610198d8187226eSemanticReplay(t *testing.T) {
	texts := session0610198d8187226eTranscript()
	mc := &meetingContext{
		Title:      "出張申請と経費精算の運用見直し",
		Purpose:    "差し戻しや精算遅延の原因を確認し、申請者が迷いやすい点の改善方法を検討する",
		Background: "宿泊費上限超過理由、交通経路、領収書、私用予定を含む交通費運用、入力項目、説明文、入力例を改善する",
		Agenda: []agendaItem{
			{ID: "agenda-1", Title: "差し戻し発生原因の確認", Order: 1, Role: agendaRolePrimary},
			{ID: "agenda-2", Title: "申請者が迷いやすい入力項目の整理", Order: 2, Role: agendaRolePrimary},
			{ID: "agenda-3", Title: "フォームと入力例の改善方法検討", Order: 3, Role: agendaRolePrimary},
			{ID: "agenda-4", Title: "経理への確認事項の整理", Order: 4, Role: agendaRolePrimary},
			{ID: "agenda-5", Title: "次回までの作業確認", Order: 5, Role: agendaRolePrimary},
		},
	}
	scope := evidenceScopeFromTexts(texts, sequenceRange(1, 36)...)
	timeline := classifyDiscourseTimeline(scope)
	spanStats := &liveAnalysisTreeMergeStats{}
	spans := detectAgendaContextSpans(scope, mc, spanStats, timeline)
	if len(spans) != 1 || spans[0].Mode != agendaContextModeNoAgenda || spans[0].StartSequenceNo != 12 || spans[0].EndSequenceNo != 18 {
		t.Fatalf("bounded Kyoto detour spans=%+v", spans)
	}
	if mode, _, _ := agendaContextForEvidence([]int64{16}, spans); mode != agendaContextModeNoAgenda {
		t.Fatalf("Kyoto evidence mode=%q spans=%+v", mode, spans)
	}
	for _, sequenceNo := range []int64{23, 29, 31, 33, 35} {
		if mode, _, _ := agendaContextForEvidence([]int64{sequenceNo}, spans); mode == agendaContextModeNoAgenda {
			t.Fatalf("sequence %d retained stale no-agenda span: %+v", sequenceNo, spans)
		}
	}
	if spanStats.NoAgendaSpansClosed == 0 {
		t.Fatalf("span stats=%+v", spanStats)
	}

	diff := `{
		"summary":"出張申請と経費精算の見直し",
		"currentTopic":"申請フォーム改善",
		"utteranceRoles":[],
		"items":[
			{"clientKey":"cause-question","kind":"issue","subtype":"question","severity":"medium","title":"何が原因でしたか","body":"","status":"open","evidenceSequenceNos":[4]},
			{"clientKey":"kyoto-heritage","kind":"issue","subtype":"discussion","severity":"low","title":"京都の世界遺産の登録範囲","body":"清水寺や金閣寺がどの登録に含まれるかという雑談","status":"open","evidenceSequenceNos":[16,17]},
			{"clientKey":"private-fare","kind":"issue","subtype":"discussion","severity":"high","title":"私用経由時の会社負担範囲","body":"通常経路との差額を本人負担にする基準を整理する","status":"open","evidenceSequenceNos":[19,20,23]},
			{"clientKey":"form-policy","kind":"decision","subtype":"","severity":"high","title":"理由欄と私用日程欄を追加する","body":"宿泊費上限超過の理由欄と私用日程の有無を追加する方向で進める","status":"open","evidenceSequenceNos":[27]},
			{"clientKey":"form-draft","kind":"todo","subtype":"","severity":"high","title":"申請フォーム修正案と入力例を作成","body":"山下さんが申請フォームの修正案と交通費入力例を作成する","status":"open","evidenceSequenceNos":[28,29]},
			{"clientKey":"rejection-examples","kind":"todo","subtype":"","severity":"medium","title":"差し戻し事例を共有","body":"倉本さんが個人情報を除いた2〜3件を今週中に共有する","status":"open","evidenceSequenceNos":[29,30]},
			{"clientKey":"accounting-check","kind":"todo","subtype":"","severity":"high","title":"私用日程を含む精算基準を経理へ確認","body":"山下さんが経理へ確認し、回答を基に来週フォーム修正内容を確定する","status":"open","evidenceSequenceNos":[31]},
			{"clientKey":"heritage-policy","kind":"decision","subtype":"","severity":"medium","title":"世界遺産一覧は申請フォームへ入れない","body":"出張を推奨しているように見えるため一覧は入れない","status":"open","evidenceSequenceNos":[32,33]},
			{"clientKey":"meeting-end","kind":"decision","subtype":"","severity":"low","title":"本日これで終了","body":"本日の議事をここで打ち切る決定。","status":"open","evidenceSequenceNos":[35]}
		],
		"newTopics":[{"id":"topic-kyoto","label":"京都の世界遺産","description":"会議本題外の観光雑談"}],
		"assignments":[
			{"nodeId":"cause-question","parentTopicId":"agenda-1","confidence":0.9,"reason":"差し戻し原因"},
			{"nodeId":"kyoto-heritage","parentTopicId":"agenda-2","confidence":0.9,"reason":"model forced"},
			{"nodeId":"private-fare","parentTopicId":"agenda-2","confidence":0.9,"reason":"交通費入力"},
			{"nodeId":"form-policy","parentTopicId":"agenda-3","confidence":0.9,"reason":"フォーム改善"},
			{"nodeId":"form-draft","parentTopicId":"agenda-3","confidence":0.9,"reason":"フォーム改善"},
			{"nodeId":"rejection-examples","parentTopicId":"agenda-5","confidence":0.9,"reason":"次回作業"},
			{"nodeId":"accounting-check","parentTopicId":"agenda-4","confidence":0.9,"reason":"経理確認"},
			{"nodeId":"heritage-policy","parentTopicId":"agenda-3","confidence":0.9,"reason":"フォーム方針"},
			{"nodeId":"meeting-end","parentTopicId":"agenda-5","confidence":0.9,"reason":"model decision"}
		]
	}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, nil, mc, 1, sequenceRange(1, 36), scope, TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if diagnostics := validateTreeIntegrity(state.Tree, state.Items, mc, state.AgendaAnchors); !diagnostics.Valid || len(diagnostics.AgendaTopicIDCollisions) != 0 || len(diagnostics.UnknownAgendaRefs) != 0 || len(diagnostics.OrphanMaterializedTopicIDs) != 0 || len(diagnostics.EmptyAgendaTopicIDs) != 0 {
		t.Fatalf("integrity=%+v", diagnostics)
	}
	for _, item := range state.Items {
		if item.Kind == "decision" && isMeetingEndOnlyItem(item.Title, item.Body) {
			t.Fatalf("meeting-end decision survived: %+v", item)
		}
		if item.Kind == "todo" {
			if item.AssignmentSource == assignmentSourceNoAgendaSpan || itemTopicID(state.Tree, item.ID) == treeUnclassifiedTopicID {
				t.Fatalf("agenda TODO was stranded: %+v topic=%s", item, itemTopicID(state.Tree, item.ID))
			}
			if !containsExactString(item.RelatedAgendaIDs, virtualActionSummaryProjectionID) {
				t.Fatalf("TODO missing Action Summary fallback: %+v", item)
			}
		}
	}
	cause := findItemByTitlePart(state.Items, "宿泊費")
	if cause == nil || issueTextNeedsReferent(cause.Title) {
		t.Fatalf("generic cause question was not repaired: %+v", state.Items)
	}
	kyoto := findItemByTitlePart(state.Items, "京都の世界遺産")
	if kyoto == nil || kyoto.AssignmentSource != assignmentSourceNoAgendaSpan || itemTopicID(state.Tree, kyoto.ID) != treeUnclassifiedTopicID {
		t.Fatalf("Kyoto detour lost no-agenda protection: %+v topic=%s", kyoto, func() string {
			if kyoto == nil {
				return ""
			}
			return itemTopicID(state.Tree, kyoto.ID)
		}())
	}
	precheck := deterministicTreeAuditPrecheck(state, mc, classifyTreeAuditEvidence(state, nil), TreeAuditConfig{})
	for _, finding := range precheck {
		switch finding.Type {
		case TreeAuditParentChildSameTitle, TreeAuditLowInformationChild, TreeAuditGenericQuestionWithoutSubject, TreeAuditMeetingEndAsDecision, TreeAuditAgendaItemForcedNoAgenda, TreeAuditAgendaReentryMissed, TreeAuditActionSummaryMissingActiveTodos:
			t.Fatalf("replay defect remained: %+v", finding)
		}
	}
	if overlap := observeAgendaTree(state.Tree, mc).DynamicOverlap; overlap != 0 {
		t.Fatalf("agendaDynamicOverlapAfter=%d", overlap)
	}
	if stats.FixedAgendaAssignmentRejectedByNoAgendaSpan == 0 || stats.LowInformationDecisionsRejected == 0 || stats.ActiveTodoReferences != 3 || stats.SourceActionSummaryAgendaCount != 0 || stats.LogicalActionSummaryCount != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	t.Logf("session_061 replay treeIntegrityValid=true agendaTopicIdCollisions=0 unknownAgendaRefs=0 orphanMaterializedTopicIds=0 emptyAgendaTopicsAfter=0 staleNoAgendaSpans=0 unclassifiedAgendaTodos=0 parentChildSameTitleCount=0 lowInformationVisibleItems=0 meetingEndDecisions=0 agendaDynamicOverlapAfter=0 noAgendaStarts=%v noAgendaClosed=%d actionSummaryFallbackTodos=%d", stats.NoAgendaSpanStartSequences, stats.NoAgendaSpansClosed, stats.ActiveTodoReferences)
}

func sequenceRange(start, end int64) []int64 {
	values := make([]int64, 0, end-start+1)
	for value := start; value <= end; value++ {
		values = append(values, value)
	}
	return values
}

func session0610198d8187226eTranscript() map[int64]string {
	lines := []string{
		"はい。では、出張申請の運用について少し相談させてください。",
		"最近、申請の差し戻しや精算の遅れが増えているので、どこを直せばよいか整理したいです。",
		"自分も先月の大阪出張で一度差し戻されました。",
		"何が原因でしたか？",
		"宿泊費の理由を書いていなかったからです。上限を少し超えていました。",
		"上限を超えた場合に理由の記入が必要だとわかりにくいのかもしれません。",
		"金額を入力した時点で理由の欄が表示される方がわかりやすいと思います。",
		"条件に応じて入力欄を出す形ですね。",
		"他には交通費の入力ですね。新幹線と在来線を分けるのか迷います。",
		"経路検索の結果を添付すればよい運用ではなかったですか？",
		"帰りに予定を変えた場合は検索結果と領収書が一致しないことがあります。",
		"京都出張だったんですか。",
		"会場が京都駅の近くだったので、終了後に東寺を見に行きました。",
		"東寺とか。",
		"東寺は世界遺産でしたよね。五重塔があるところ。",
		"京都は世界遺産が多すぎて、どれがどの登録に含まれるかわかりません。",
		"清水寺も金閣寺も古都京都の文化財でしたっけ？",
		"出張の翌日に休みを取って、そういうところを回れたらいいんですけど。",
		"その話で思い出しましたが、出張と私用の予定を組み合わせる場合の交通費もルールが曖昧ですね。",
		"私用で別の場所に寄る場合、どこまで会社負担なのか判断しにくいんです。",
		"脱線したと思ったら、意外と本題に戻ってきましたね。",
		"世界遺産も役に立ちました。",
		"では、申請に私用日程を含むかという項目を追加し、通常経路との差額を本人負担にする考え方が良さそうです。",
		"比較する基準の経路も表示してもらえると助かります。",
		"そこは経理にも確認が必要ですね。",
		"最初は通常経路との金額と実際の金額をそれぞれ入力するだけでも良いと思います。",
		"宿泊費が上限を超えた場合の理由欄と、私用日程の有無を追加する方向で考えます。",
		"交通費については入力例も欲しいです。",
		"私が申請フォームの修正案と入力例を作ります。差し戻し事例を2〜3件共有してもらえますか？",
		"今週中にまとめますね。",
		"私は経理に私用日程を含む出張の精算基準を確認します。来週フォーム修正内容を確定しましょう。",
		"京都の世界遺産一覧は申請フォームに入れなくて大丈夫ですか？",
		"出張を推奨しているように見えるのでやめておきましょう。",
		"残念です。",
		"では、今日はここまでにします。ありがとうございました。",
		"はい。ありがとうございました。",
	}
	result := make(map[int64]string, len(lines))
	for index, line := range lines {
		result[int64(index+1)] = strings.TrimSpace(line)
	}
	return result
}
