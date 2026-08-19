package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func finalSegment(sequenceNo int64, text string) domain.TranscriptSegment {
	return domain.TranscriptSegment{SequenceNo: sequenceNo, Text: text, IsFinal: true}
}

func TestDetectDecisionCandidatesAcceptsExplicitAndRejectsUndecided(t *testing.T) {
	segments := []domain.TranscriptSegment{
		finalSegment(1, "海岸側・北側・南側の三地点で調査することを決定します。"),
		finalSegment(2, "説明会の日程はまだ決定していません。"),
		finalSegment(3, "次回決定するか検討します。"),
		finalSegment(4, "調査結果を図付きでウェブ公開する方針にします。"),
	}
	candidates := detectDecisionCandidates(segments)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want two explicit decisions", candidates)
	}
	if candidates[0].SequenceNo != 1 || candidates[1].SequenceNo != 4 {
		t.Fatalf("candidate sequences = [%d %d], want [1 4]", candidates[0].SequenceNo, candidates[1].SequenceNo)
	}
}

func TestDecisionCandidatesTreatConfirmedDecisionSummaryAsGroundedDecisions(t *testing.T) {
	segments := []domain.TranscriptSegment{finalSegment(21,
		"今日決まったのは、営業部の5人を対象に2週間試験すること。開始前にセキュリティルールを確認すること。",
	)}
	candidates := detectDecisionCandidates(segments)
	if len(candidates) != 2 {
		t.Fatalf("candidates=%+v, want two confirmed summary decisions", candidates)
	}
	for _, candidate := range candidates {
		if candidate.Recap || candidate.SequenceNo != 21 || len(candidate.SourceSequenceNos) != 1 {
			t.Fatalf("candidate=%+v", candidate)
		}
	}
}

func TestConfirmedDecisionSummaryCreatesGroundedItemsAndRejectsNoDecisionSummary(t *testing.T) {
	segments := []domain.TranscriptSegment{finalSegment(21,
		"今日決まったのは、営業部の5人を対象に2週間試験すること。開始前にセキュリティルールを確認すること。",
	)}
	model := `{"summary":"会議終了","currentTopic":"","items":[],"newTopics":[],"assignments":[]}`
	reconciled, audit, err := reconcileDecisionCandidates(model, nil, detectDecisionCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Items) != 2 || audit.AcceptedDecisions != 2 {
		t.Fatalf("items=%+v audit=%+v", diff.Items, audit)
	}
	for _, item := range diff.Items {
		if item.Kind != "decision" || len(item.EvidenceSequenceNos) != 1 || item.EvidenceSequenceNos[0] != 21 {
			t.Fatalf("item=%+v", item)
		}
	}
	if candidates := detectDecisionCandidates([]domain.TranscriptSegment{
		finalSegment(22, "今日決まったことは何もありません。期間は次回検討します。"),
	}); len(candidates) != 0 {
		t.Fatalf("negative summary candidates=%+v", candidates)
	}
}

func TestDecisionCandidateJoinsAdjacentSameSpeakerFragments(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{SequenceNo: 10, SpeakerID: "speaker-1", Text: "渡り鳥の調査については、海岸側、北側、南側の合計三地点で。", IsFinal: true},
		{SequenceNo: 11, SpeakerID: "speaker-1", Text: "ええ。実施することを決定します。", IsFinal: true},
	}
	candidates := detectDecisionCandidates(segments)
	if len(candidates) != 1 {
		t.Fatalf("candidates=%+v", candidates)
	}
	if got := candidates[0].SourceSequenceNos; len(got) != 2 || got[0] != 10 || got[1] != 11 {
		t.Fatalf("sourceSequenceNos=%v", got)
	}
	if title := decisionCandidateTitle(candidates[0].Statement); !strings.Contains(title, "渡り鳥") || title == "実施" {
		t.Fatalf("title=%q statement=%q", title, candidates[0].Statement)
	}
}

func TestDecisionCandidateRejectsPredicateOnlyMarker(t *testing.T) {
	if candidates := detectDecisionCandidates([]domain.TranscriptSegment{finalSegment(11, "ええ。実施することを決定します。")}); len(candidates) != 0 {
		t.Fatalf("predicate-only candidates=%+v", candidates)
	}
}

func TestDecisionCandidateRepairsLeadingParticleFromPreviousSTTSegment(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{SequenceNo: 15, SpeakerID: "speaker-1", Text: "また、交換前後でVLANごとの疎通確認を実施するチェックリストを作成します。", IsFinal: true},
		{SequenceNo: 16, SpeakerID: "speaker-1", Text: "の運用を次回の機器交換から適用することにします。", IsFinal: true},
	}
	candidates := detectDecisionCandidates(segments)
	if len(candidates) != 1 {
		t.Fatalf("candidates=%+v", candidates)
	}
	got := decisionCandidateTitle(candidates[0].Statement)
	if strings.HasPrefix(got, "の") || !strings.Contains(got, "チェックリストの運用") || !strings.Contains(got, "次回の機器交換") || !strings.Contains(got, "適用") {
		t.Fatalf("repaired title=%q statement=%q", got, candidates[0].Statement)
	}
	if evidence := candidates[0].SourceSequenceNos; len(evidence) != 2 || evidence[0] != 15 || evidence[1] != 16 {
		t.Fatalf("sourceSequenceNos=%v", evidence)
	}
}

func TestDecisionCandidateRejectsUnrecoverableLeadingParticle(t *testing.T) {
	if candidates := detectDecisionCandidates([]domain.TranscriptSegment{finalSegment(16, "の対応を実施することにします。")}); len(candidates) != 0 {
		t.Fatalf("unrecoverable fragment candidates=%+v", candidates)
	}
	if completeDecisionStatement("その対応を実施することにします") {
		t.Fatal("anaphora-only decision was accepted without a referent")
	}
}

func TestDecisionCandidateCanUsePriorFragmentAcrossAnalysisRounds(t *testing.T) {
	current := []domain.TranscriptSegment{{SequenceNo: 11, SpeakerID: "speaker-1", Text: "実施することを決定します。", IsFinal: true}}
	prior := domain.TranscriptSegment{SequenceNo: 10, SpeakerID: "speaker-1", Text: "渡り鳥の調査は三地点で。", IsFinal: true}
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{10: {}, 11: {}}, CurrentRound: map[int64]struct{}{11: {}}, TranscriptText: map[int64]string{10: prior.Text, 11: current[0].Text}, Segments: map[int64]domain.TranscriptSegment{10: prior, 11: current[0]}}
	candidates := detectDecisionCandidates(extendDecisionSegmentsWithPriorFragment(current, scope))
	if len(candidates) != 1 || len(candidates[0].SourceSequenceNos) != 2 {
		t.Fatalf("candidates=%+v", candidates)
	}
}

func TestReconcileDecisionCandidatePromotesModelTodoWithoutChangingID(t *testing.T) {
	segments := []domain.TranscriptSegment{finalSegment(9, "渡り鳥の調査は海岸側、北側、南側の三地点で実施することを決定します。")}
	model := `{"summary":"更新","currentTopic":"渡り鳥","resolvedIds":[],"items":[{"id":"todo-bird-sites","kind":"todo","severity":"high","title":"三地点で調査する","body":"海岸側・北側・南側で調査する計画へ更新","status":"open"}],"assignments":[{"nodeId":"todo-bird-sites","parentTopicId":"agenda-1","confidence":0.9,"reason":"鳥類調査"}]}`
	reconciled, audit, err := reconcileDecisionCandidates(model, nil, detectDecisionCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Items) != 1 || diff.Items[0].ID != "todo-bird-sites" || diff.Items[0].Kind != "decision" {
		t.Fatalf("items = %+v, want same id promoted to decision", diff.Items)
	}
	if len(diff.Items[0].EvidenceSequenceNos) != 1 || diff.Items[0].EvidenceSequenceNos[0] != 9 {
		t.Fatalf("evidence = %v, want [9]", diff.Items[0].EvidenceSequenceNos)
	}
	if audit.ModelDecisionItems != 0 || audit.AcceptedDecisions != 1 {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestMixedSegmentProducesDecisionAndUnresolvedItem(t *testing.T) {
	segment := finalSegment(16, "昼一回、夜二回測定することを決定します。ただし、強風日の基準風速は未決定です。")
	model := `{"summary":"更新","currentTopic":"騒音","resolvedIds":[],"items":[{"id":"todo-noise-count","kind":"todo","severity":"high","title":"昼一回・夜二回測定","body":"合計三回測定する","status":"open","evidenceSequenceNos":[16]},{"id":"question-wind-speed","kind":"question","severity":"high","title":"強風日の基準風速が未決定","body":"気象データ確認後に決める","status":"open","evidenceSequenceNos":[16]}],"assignments":[{"nodeId":"todo-noise-count","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"question-wind-speed","parentTopicId":"agenda-2","confidence":0.9}]}`
	reconciled, _, err := reconcileDecisionCandidates(model, nil, detectDecisionCandidates([]domain.TranscriptSegment{segment}))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parseAndMergeLiveAnalysisPayload(reconciled, nil, &meetingContext{Agenda: []agendaItem{{ID: "agenda-2", Title: "騒音測定", Order: 1}}}, 1, []int64{16}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	counts := map[string]int{}
	for _, item := range state.Items {
		counts[item.Kind]++
	}
	if counts["decision"] != 1 || counts["issue"] != 1 || len(state.Items) != 2 || findItemByID(state.Items, "question-wind-speed").Subtype != issueSubtypeQuestion {
		t.Fatalf("counts=%v items=%+v", counts, state.Items)
	}
}

func TestDecisionRecapMergesEvidenceIntoExistingDecision(t *testing.T) {
	previous := liveAnalysisPayload{
		Summary: "previous",
		Items:   []liveAnalysisItem{{ID: "decision-bird", Kind: "decision", Severity: "high", Title: "渡り鳥を三地点で調査", Body: "海岸側・北側・南側で調査する", Status: "open", EvidenceSequenceNos: []int64{9}}},
	}
	previousJSON, _ := json.Marshal(previous)
	segment := finalSegment(31, "決定事項は、渡り鳥を三地点で調査することです。")
	model := `{"summary":"まとめ","currentTopic":"まとめ","resolvedIds":[],"items":[],"assignments":[]}`
	reconciled, audit, err := reconcileDecisionCandidates(model, previousJSON, detectDecisionCandidates([]domain.TranscriptSegment{segment}))
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Items) != 1 || diff.Items[0].ID != "decision-bird" {
		t.Fatalf("recap diff = %+v", diff.Items)
	}
	if audit.MergedDecisions != 1 {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestTodoTransitionsToDecisionWithoutDuplicateCard(t *testing.T) {
	previous := liveAnalysisPayload{
		Summary: "previous",
		Items:   []liveAnalysisItem{{ID: "todo-three-sites", Kind: "todo", Severity: "medium", Title: "三地点での調査を検討", Body: "海岸・北・南の三地点案を検討する", Status: "open", EvidenceSequenceNos: []int64{5}}},
	}
	previousJSON, _ := json.Marshal(previous)
	segment := finalSegment(9, "海岸側・北側・南側の三地点で調査すると決定します。")
	model := `{"summary":"更新","currentTopic":"鳥類","resolvedIds":[],"items":[],"assignments":[]}`
	reconciled, _, err := reconcileDecisionCandidates(model, previousJSON, detectDecisionCandidates([]domain.TranscriptSegment{segment}))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parseAndMergeLiveAnalysisPayload(reconciled, previousJSON, nil, 2, []int64{9}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 1 || state.Items[0].ID != "todo-three-sites" || state.Items[0].Kind != "decision" {
		t.Fatalf("items = %+v", state.Items)
	}
	if len(state.Items[0].EvidenceSequenceNos) != 2 {
		t.Fatalf("evidence = %v, want original and decision", state.Items[0].EvidenceSequenceNos)
	}
}

func TestSameRoundQuestionAndTodoRemainIndependentCanonicalPropositions(t *testing.T) {
	diff := `{"summary":"更新","currentTopic":"鳥類","resolvedIds":[],"items":[{"id":"question-sites","kind":"question","severity":"medium","title":"追加観測地点の設置基準を決める","body":"基準は何か","status":"open"},{"id":"todo-sites","kind":"todo","severity":"high","title":"追加観測地点の設置基準を決める","body":"風向と鳥の経路から基準を決める","status":"open"}],"assignments":[{"nodeId":"question-sites","parentTopicId":"agenda-1","confidence":0.9},{"nodeId":"todo-sites","parentTopicId":"agenda-1","confidence":0.9}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(diff, nil, &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "鳥類", Order: 1}}}, 1, []int64{1}, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	question := findItemByID(state.Items, "question-sites")
	todo := findItemByID(state.Items, "todo-sites")
	if len(state.Items) != 2 || question == nil || question.Kind != "issue" || question.Subtype != issueSubtypeQuestion || todo == nil || todo.Kind != "todo" {
		t.Fatalf("items=%+v", state.Items)
	}
	if stats.PropositionItemsMerged != 0 {
		t.Fatalf("propositionItemsMerged=%d", stats.PropositionItemsMerged)
	}
}

// TestDecisionCandidatesFromThreeClauseSegmentBuildTwoDecisionsAndKeepChecklistTodo
// reproduces the target session's seq12 segment (W5): 「…ダブルチェックを必須に
// します。また、…チェックリストを作成します。この運用を次回の危機交換から適用
// することにします。」. The new「を必須にします」pattern (W5.1) turns clause1 into
// its own decision; clause3's bare「この運用」is repaired using clause2 (the
// checklist creation clause, itself not a decision) as the same-segment
// referent (W5.2), producing a title that names both the target object and
// the policy verb. The model's own checklist-creation todo must remain a
// todo, not get consumed into either decision.
func TestDecisionCandidatesFromThreeClauseSegmentBuildTwoDecisionsAndKeepChecklistTodo(t *testing.T) {
	text := "今後の対応についてです。まず、ネットワーク機器を交換する際は、作業者とは別の担当者が設定内容を確認するダブルチェックを必須にします。また、交換前後でvランごとの疎通確認を実施するチェックリストを作成します。この運用を次回の危機交換から適用することにします。"
	segments := []domain.TranscriptSegment{finalSegment(12, text)}
	candidates := detectDecisionCandidates(segments)
	if len(candidates) < 2 {
		t.Fatalf("candidates = %+v, want at least 2 decision candidates from this segment", candidates)
	}
	var sawMustCheck, sawApplyChecklist bool
	for _, c := range candidates {
		if strings.Contains(c.Statement, "ダブルチェック") && strings.Contains(c.Statement, "必須") {
			sawMustCheck = true
		}
		if strings.Contains(c.Statement, "チェックリスト") && strings.Contains(c.Statement, "適用") {
			sawApplyChecklist = true
			if strings.HasPrefix(c.Statement, "の") {
				t.Fatalf("repaired statement = %q, must not still start with the bare leading particle", c.Statement)
			}
		}
	}
	if !sawMustCheck {
		t.Fatalf("candidates = %+v, want a ダブルチェックを必須にします decision", candidates)
	}
	if !sawApplyChecklist {
		t.Fatalf("candidates = %+v, want the referent-repaired チェックリストの運用を適用 decision", candidates)
	}

	model := `{"summary":"更新","currentTopic":"再発防止策","resolvedIds":[],"items":[{"id":"item-todo-checklist","kind":"todo","severity":"medium","title":"vランごとの疎通確認チェックリスト作成","body":"交換前後でvランごとの疎通確認を実施するチェックリストを作成する","status":"open","evidenceSequenceNos":[12]}],"assignments":[{"nodeId":"item-todo-checklist","parentTopicId":"agenda-3","confidence":0.8}]}`
	reconciled, audit, err := reconcileDecisionCandidates(model, nil, candidates)
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	decisionCount := 0
	var applyTitle string
	for _, item := range diff.Items {
		if item.Kind == "decision" {
			decisionCount++
			if strings.Contains(item.Title, "適用") {
				applyTitle = item.Title
			}
		}
	}
	if decisionCount < 2 {
		t.Fatalf("decisionCount = %d, items=%+v, want >= 2", decisionCount, diff.Items)
	}
	if applyTitle == "" || !strings.Contains(applyTitle, "チェックリスト") {
		t.Fatalf("applyTitle = %q, want a title containing both the target object (チェックリスト) and the policy verb (適用)", applyTitle)
	}
	todo := findItemByID(diff.Items, "item-todo-checklist")
	if todo == nil || todo.Kind != "todo" {
		t.Fatalf("checklist todo = %+v, want kind left unchanged (todo), not consumed by a decision", todo)
	}
	if audit.AcceptedDecisions < 2 {
		t.Fatalf("audit = %+v, want >= 2 accepted decisions", audit)
	}
}

// TestDetectDecisionCandidatesRejectsConsiderationAndMeetingEndPhrasing
// guards W5.1's new positive-pattern alternatives (を必須にします等) against
// over-matching plain consideration and meeting-end control speech.
func TestDetectDecisionCandidatesRejectsConsiderationAndMeetingEndPhrasing(t *testing.T) {
	cases := []string{
		"監視間隔と通知条件については、次回までに検討します。",
		"今日はここまでにします。",
	}
	for _, text := range cases {
		segments := []domain.TranscriptSegment{finalSegment(14, text)}
		if candidates := detectDecisionCandidates(segments); len(candidates) != 0 {
			t.Fatalf("text=%q candidates=%+v, want 0", text, candidates)
		}
	}
}

// TestDecisionAdoptionConsumesCreationTodoGuardsKindRewrite covers
// decisionAdoptionConsumesCreationTodo: a strongly-matching decision
// statement about ADOPTING/APPLYING a deliverable (適用/運用/導入/施行) must
// not consume a todo about CREATING/PREPARING that same deliverable
// (作成/策定/準備/起票/ドラフト) -- they are distinct propositions, so the todo
// stays a todo and the decision is created as its own item. When both the
// todo and the decision statement share the same creation verb, the normal
// same-id promotion still applies.
func TestDecisionAdoptionConsumesCreationTodoGuardsKindRewrite(t *testing.T) {
	// ケース1: 作成系todo + 適用系decision → todoは維持され、別decisionが作られる。
	adoptionSegment := finalSegment(20, "交換前後でvランごとの疎通確認を実施するチェックリストの運用を次回から適用することにします。")
	model := `{"summary":"更新","currentTopic":"再発防止策","resolvedIds":[],"items":[{"id":"item-todo-checklist","kind":"todo","severity":"medium","title":"チェックリスト作成","body":"交換前後でvランごとの疎通確認を実施するチェックリストを作成する","status":"open","evidenceSequenceNos":[12]}],"assignments":[]}`
	reconciled, audit, err := reconcileDecisionCandidates(model, nil, detectDecisionCandidates([]domain.TranscriptSegment{adoptionSegment}))
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	todo := findItemByID(diff.Items, "item-todo-checklist")
	if todo == nil || todo.Kind != "todo" {
		t.Fatalf("checklist todo = %+v, want kind left unchanged (todo)", todo)
	}
	decisionCount := 0
	for _, item := range diff.Items {
		if item.Kind == "decision" {
			decisionCount++
		}
	}
	if decisionCount != 1 {
		t.Fatalf("decisionCount = %d items=%+v, want exactly 1 separate decision item", decisionCount, diff.Items)
	}
	if audit.AcceptedDecisions != 1 {
		t.Fatalf("audit = %+v, want 1 accepted (separately created) decision", audit)
	}

	// ケース2: 同一動作(作成todo + 「作成することにします」decision) → 従来どおり
	// 同一IDのままkindがdecisionへ書き換わる(消費される命題が別ではないため)。
	creationSegment := finalSegment(20, "交換前後でvランごとの疎通確認を実施するチェックリストを作成することにします。")
	model2 := `{"summary":"更新","currentTopic":"再発防止策","resolvedIds":[],"items":[{"id":"item-todo-checklist2","kind":"todo","severity":"medium","title":"チェックリスト作成","body":"交換前後でvランごとの疎通確認を実施するチェックリストを作成する","status":"open","evidenceSequenceNos":[12]}],"assignments":[]}`
	reconciled2, _, err := reconcileDecisionCandidates(model2, nil, detectDecisionCandidates([]domain.TranscriptSegment{creationSegment}))
	if err != nil {
		t.Fatal(err)
	}
	var diff2 liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled2), &diff2); err != nil {
		t.Fatal(err)
	}
	if len(diff2.Items) != 1 || diff2.Items[0].ID != "item-todo-checklist2" || diff2.Items[0].Kind != "decision" {
		t.Fatalf("items = %+v, want the same id promoted in place to decision (same creation verb on both sides)", diff2.Items)
	}
}

// TestChecklistCreationTodoAndApplicationDecisionCoexist covers F2's new
// 「(から|を)適用します$」 marker together with F5's same-segment referent
// repair: a single segment states a creation clause ("…チェックリストを作成
// します") immediately followed by a bare-anaphora adoption clause ("この運用
// を…適用します"). The adoption clause is repaired using the creation clause
// as its referent (W5.2) and recognized as a decision only via F2's new
// pattern; decisionAdoptionConsumesCreationTodo (the earlier W5.3 guard)
// must then keep the model's own creation TODO unchanged and create the
// decision as its own separate item.
func TestChecklistCreationTodoAndApplicationDecisionCoexist(t *testing.T) {
	text := "VLAN疎通確認チェックリストを作成します。この運用を次回の機器交換から適用します。"
	segments := []domain.TranscriptSegment{finalSegment(30, text)}
	candidates := detectDecisionCandidates(segments)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly 1 (the creation clause carries no decision marker of its own)", candidates)
	}
	if !strings.Contains(candidates[0].Statement, "チェックリスト") || !strings.Contains(candidates[0].Statement, "適用") {
		t.Fatalf("repaired statement = %q, want referent-repaired to name both the target object and 適用", candidates[0].Statement)
	}
	if strings.HasPrefix(candidates[0].Statement, "この") {
		t.Fatalf("repaired statement = %q, must not still start with the bare anaphora", candidates[0].Statement)
	}

	model := `{"summary":"更新","currentTopic":"再発防止策","resolvedIds":[],"items":[{"id":"item-todo-vlan-checklist","kind":"todo","severity":"medium","title":"VLAN疎通確認チェックリストの作成","body":"VLAN疎通確認チェックリストを作成する","status":"open","evidenceSequenceNos":[30]}],"assignments":[]}`
	reconciledContent, decisionAudit, err := reconcileDecisionCandidates(model, nil, candidates)
	if err != nil {
		t.Fatal(err)
	}

	scope := evidenceScopeFromTexts(map[int64]string{30: text}, 30)
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(reconciledContent, nil, nil, 1, []int64{30}, scope, TreeClassificationConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)

	todo := findItemByID(state.Items, "item-todo-vlan-checklist")
	if todo == nil || todo.Kind != "todo" {
		t.Fatalf("todo = %+v, want kind left unchanged (todo)", todo)
	}
	decisionCount := 0
	for _, item := range state.Items {
		if item.Kind != "decision" {
			continue
		}
		decisionCount++
		if item.ID == todo.ID {
			t.Fatalf("decision must not reuse the todo's id: %+v", item)
		}
	}
	if decisionCount != 1 {
		t.Fatalf("decisionCount = %d items=%+v, want exactly 1", decisionCount, state.Items)
	}
	if decisionAudit.AcceptedDecisions != 1 {
		t.Fatalf("audit = %+v, want 1 accepted decision", decisionAudit)
	}
}

// TestDecisionDoesNotResolveExecutionTodo covers G1: a decision reaching
// consensus on ADOPTING/OPERATING a deliverable ("運用を...適用します") must
// not silently resolve a pre-existing (persisted, from previous.Items) TODO
// about CREATING that same deliverable ("...を作成する"), even though the
// two are topically related enough to match reconcileDecisionCandidates'
// subject-matched-explicit-decision resolution loop's 0.16 similarity floor.
// Making a decision does not itself complete a create/implement/confirm
// task -- the same principle the existing prompt already applies model-side
// ("decisionが出たという理由だけでは解決にしない"), now also enforced
// server-side via deliberativeTodoPattern + decisionAdoptionConsumesCreationTodo.
// A genuinely deliberative TODO ("...を検討する") must keep resolving via a
// matching decision exactly as before (regression guard: G1 must not touch
// issue/question/risk resolution, nor deliberative todo resolution).
func TestDecisionDoesNotResolveExecutionTodo(t *testing.T) {
	hasResolvedUpdateFor := func(updates []resolutionUpdate, itemID string) bool {
		key := canonicalReferenceKey(itemID)
		for _, update := range updates {
			if canonicalReferenceKey(update.ItemID) == key && normalizeResolutionStatus(update.Status) == "resolved" {
				return true
			}
		}
		return false
	}

	// ケースA(G1本体): 実行系(作成)TODOはdecisionだけではresolvedにならない。
	previousExecution := liveAnalysisPayload{
		Summary: "previous",
		Items:   []liveAnalysisItem{{ID: "item-todo-checklist", Kind: "todo", Severity: "medium", Title: "スイッチ交換用チェックリスト案の作成", Body: "スイッチ交換用チェックリスト案を作成する", Status: "open", EvidenceSequenceNos: []int64{3}}},
	}
	previousExecutionJSON, err := json.Marshal(previousExecution)
	if err != nil {
		t.Fatal(err)
	}
	executionText := "スイッチ交換用チェックリスト案を作成します。この運用を次回の機器交換から適用します。"
	executionCandidates := detectDecisionCandidates([]domain.TranscriptSegment{finalSegment(20, executionText)})
	if len(executionCandidates) != 1 {
		t.Fatalf("executionCandidates = %+v, want exactly 1 (referent-repaired 適用 decision)", executionCandidates)
	}
	model := `{"summary":"更新","currentTopic":"再発防止策","resolvedIds":[],"items":[],"assignments":[]}`
	reconciled, _, err := reconcileDecisionCandidates(model, previousExecutionJSON, executionCandidates)
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	if hasResolvedUpdateFor(diff.ResolutionUpdates, "item-todo-checklist") {
		t.Fatalf("resolutionUpdates = %+v, want no resolved update for the execution todo (a decision must not resolve a create/implement task)", diff.ResolutionUpdates)
	}
	for _, item := range diff.ResolvedIds {
		if canonicalReferenceKey(item) == canonicalReferenceKey("item-todo-checklist") {
			t.Fatalf("resolvedIds = %v, want the execution todo absent", diff.ResolvedIds)
		}
	}
	if rewritten := findItemByID(diff.Items, "item-todo-checklist"); rewritten != nil && rewritten.Kind != "todo" {
		t.Fatalf("item-todo-checklist = %+v, want kind left unchanged (todo) if present at all", rewritten)
	}

	// ケースB(回帰ガード): 検討系TODOは従来どおりdecisionでresolvedになる。
	previousDeliberation := liveAnalysisPayload{
		Summary: "previous",
		Items:   []liveAnalysisItem{{ID: "item-todo-consider-sites", Kind: "todo", Severity: "medium", Title: "三地点案の検討", Body: "三地点案を検討する", Status: "open", EvidenceSequenceNos: []int64{5}}},
	}
	previousDeliberationJSON, err := json.Marshal(previousDeliberation)
	if err != nil {
		t.Fatal(err)
	}
	deliberationCandidates := detectDecisionCandidates([]domain.TranscriptSegment{finalSegment(9, "三地点で実施することを決定します。")})
	if len(deliberationCandidates) != 1 {
		t.Fatalf("deliberationCandidates = %+v, want exactly 1", deliberationCandidates)
	}
	reconciled2, _, err := reconcileDecisionCandidates(model, previousDeliberationJSON, deliberationCandidates)
	if err != nil {
		t.Fatal(err)
	}
	var diff2 liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled2), &diff2); err != nil {
		t.Fatal(err)
	}
	if !hasResolvedUpdateFor(diff2.ResolutionUpdates, "item-todo-consider-sites") {
		t.Fatalf("resolutionUpdates = %+v, want a resolved update for the deliberative todo (regression: a decision must still resolve an open deliberation)", diff2.ResolutionUpdates)
	}
}
