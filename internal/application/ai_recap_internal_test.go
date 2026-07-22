package application

import (
	"encoding/json"
	"strings"
	"testing"
)

// このファイルはW4(recap処理)のテストを持つ: 節レベルrecap intro検出による
// discourseタイムラインのrecapモード遷移(W4.1)、recap重複itemのマージと
// evidence和集合・candidate非作成(W4.2/W4.3)。対象は session_125e3cc511ee69bb
// のseq19-21(「最後にここまでをまとめます。今回の障害は…」)。

func TestHasLeadingRecapIntroClauseDetectsEmbeddedDeclaration(t *testing.T) {
	long := "最後にここまでをまとめます。今回の障害は、交換したアクセススイッチでvラン30の許可設定が漏れていたことが主な原因と考えられます。"
	if !hasLeadingRecapIntroClause(long) {
		t.Fatalf("hasLeadingRecapIntroClause(%q) = false, want true", long)
	}
	if hasLeadingRecapIntroClause("交換したアクセススイッチでvラン30の許可設定が漏れていたことが主な原因と考えられます。") {
		t.Fatal("hasLeadingRecapIntroClause must require the FIRST clause to be the recap declaration")
	}
}

func TestClassifyDiscourseTimelineEntersRecapModeFromEmbeddedDeclaration(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		19: "最後にここまでをまとめます。今回の障害は、交換したアクセススイッチでvラン30の許可設定が漏れていたことが主な原因と考えられます。",
		20: "私は今週金曜日までにチェックリストアンを作成し、佐藤さんは来週火曜日までに標準設定との差分を確認します。",
	}, 19, 20)
	timeline := classifyDiscourseTimeline(scope)
	if timeline.Roles[19] != liveEvidenceReferenceRecap {
		t.Fatalf("seq19 role = %v, want reference_recap (recap declaration embedded in the leading clause)", timeline.Roles[19])
	}
	if timeline.Roles[20] != liveEvidenceReferenceRecap {
		t.Fatalf("seq20 role = %v, want reference_recap (mode carried over from seq19)", timeline.Roles[20])
	}
}

// TestRecapRoundMergesDuplicateTodoWithoutCandidate reproduces the target
// session's recap defect: a recap round restates an already-tracked TODO
// (paraphrased, under a new model-proposed id) and also proposes a new
// topic. Both a duplicate item and a new candidate must be suppressed; the
// existing TODO absorbs the recap's own evidence.
func TestRecapRoundMergesDuplicateTodoWithoutCandidate(t *testing.T) {
	previous := liveAnalysisPayload{
		Summary: "previous",
		Items: []liveAnalysisItem{
			{ID: "item-todo-checklist", Kind: "todo", Severity: "medium", Title: "vランごとの疎通確認チェックリスト作成", Body: "交換前後でvランごとの疎通確認を実施するチェックリストを作成する", Status: "open", EvidenceSequenceNos: []int64{12}},
		},
	}
	previousJSON, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	recapText := "最後にここまでをまとめます。今回の障害は、交換したアクセススイッチでvラン30の許可設定が漏れていたことが主な原因と考えられます。再発防止として、交換前後でvランごとの疎通確認を実施するチェックリストを作成することを必須にします。"
	scope := evidenceScopeFromTexts(map[int64]string{19: recapText}, 19)
	diff := `{"summary":"まとめ","currentTopic":"まとめ","items":[{"id":"item-todo-checklist-dup","kind":"todo","severity":"medium","title":"vランごとの疎通確認チェックリストの作成","body":"交換前後でvランごとの疎通確認を実施するチェックリストを作成する","status":"open"}],"newTopics":[{"id":"topic-checklist-recap","label":"チェックリスト作成"}],"assignments":[{"nodeId":"item-todo-checklist-dup","parentTopicId":"topic-checklist-recap","confidence":0.7}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, previousJSON, nil, 2, []int64{19}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	todoCount := 0
	for _, item := range state.Items {
		if item.Kind == "todo" {
			todoCount++
		}
	}
	if todoCount != 1 {
		t.Fatalf("todoCount = %d items=%+v, want 1 (recap paraphrase merged, not duplicated)", todoCount, state.Items)
	}
	original := findItemByID(state.Items, "item-todo-checklist")
	if original == nil {
		t.Fatalf("original checklist todo missing: %+v", state.Items)
	}
	if !containsInt64(original.EvidenceSequenceNos, 12) || !containsInt64(original.EvidenceSequenceNos, 19) {
		t.Fatalf("evidence = %v, want both 12 (original round) and 19 (recap round) preserved", original.EvidenceSequenceNos)
	}
	if len(state.EmergingTopics) != 0 {
		t.Fatalf("emergingTopics = %+v, want none created from a reference-only recap round", state.EmergingTopics)
	}
}

// TestRecapRoundAddsNovelDueDateToExistingTodoBody reproduces the
// filterReferenceRecapDiff body-update path: a recap sentence restates an
// existing TODO but names a due date the canonical body does not already
// carry, so the canonical body absorbs it instead of merely being discarded
// as pure restatement.
func TestRecapRoundAddsNovelDueDateToExistingTodoBody(t *testing.T) {
	previous := liveAnalysisPayload{
		Summary: "previous",
		Items: []liveAnalysisItem{
			{ID: "item-todo-checklist", Kind: "todo", Severity: "medium", Title: "チェックリスト作成", Body: "交換前後でvランごとの疎通確認を実施するチェックリストを作成する", Status: "open", EvidenceSequenceNos: []int64{12}},
		},
	}
	previousJSON, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	recapText := "最後にここまでをまとめます。私は今週金曜日までにチェックリストを作成します。"
	scope := evidenceScopeFromTexts(map[int64]string{19: recapText}, 19)
	diff := `{"summary":"まとめ","currentTopic":"まとめ","items":[{"id":"item-todo-checklist-dup","kind":"todo","severity":"medium","title":"チェックリスト作成","body":"私は今週金曜日までにチェックリストを作成する","status":"open"}],"newTopics":[],"assignments":[]}`
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, previousJSON, nil, 2, []int64{19}, scope, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	todoCount := 0
	for _, item := range state.Items {
		if item.Kind == "todo" {
			todoCount++
		}
	}
	if todoCount != 1 {
		t.Fatalf("todoCount = %d items=%+v, want 1 (no duplicate created)", todoCount, state.Items)
	}
	item := findItemByID(state.Items, "item-todo-checklist")
	if item == nil {
		t.Fatalf("item missing: %+v", state.Items)
	}
	if !strings.Contains(item.Body, "今週金曜日") {
		t.Fatalf("body = %q, want updated with the recap's new due date (今週金曜日)", item.Body)
	}
}

// TestRecapRestatementKeepsCanonicalChecklistTodo covers a short recap round
// (the recap declaration and its restatement in the SAME segment, so
// hasLeadingRecapIntroClause -- not a long multi-segment recap span -- puts
// the round in recap mode): a canonical TODO created in an earlier round must
// stay the sole surviving TODO, absorb the recap's own evidence, and must
// not spawn a new candidate or decision.
func TestRecapRestatementKeepsCanonicalChecklistTodo(t *testing.T) {
	previous := liveAnalysisPayload{
		Summary: "previous",
		Items: []liveAnalysisItem{
			{ID: "item-todo-vlan-checklist", Kind: "todo", Severity: "medium",
				Title: "VLAN疎通確認チェックリストの作成", Body: "VLAN疎通確認チェックリストを作成する", Status: "open",
				ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{3}},
		},
	}
	previousJSON, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	recapText := "以上をまとめます。チェックリスト作成も進めます。"
	scope := evidenceScopeFromTexts(map[int64]string{10: recapText}, 10)
	// evidenceSequenceNosは意図的に省略する: 明示指定するとfilterLowInformationLiveItems
	// の早期recapゲート(モデル提案idがpreviousと一致しない場合の低情報判定)で
	// 落ちてしまい、filterReferenceRecapDiffのfuzzy一致に到達できない
	// (ai_session_125e3cc5_replay_internal_testのコメント・報告を参照)。
	// 省略すればラウンド全体(ここでは単一seq10のみ)へデフォルトされ、正しく
	// canonical itemへマージされる。
	diff := `{"summary":"まとめ","currentTopic":"まとめ","items":[{"id":"item-todo-checklist-recap-dup","kind":"todo","severity":"medium","title":"チェックリスト作成","body":"チェックリスト作成も進める","status":"open"}],"newTopics":[],"assignments":[]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, previousJSON, nil, 2, []int64{10}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)

	todoCount := 0
	for _, item := range state.Items {
		if item.Kind == "todo" {
			todoCount++
		}
	}
	if todoCount != 1 {
		t.Fatalf("todoCount = %d items=%+v, want 1 (no duplicate created)", todoCount, state.Items)
	}
	todo := findItemByID(state.Items, "item-todo-vlan-checklist")
	if todo == nil {
		t.Fatalf("canonical todo lost: %+v", state.Items)
	}
	if !containsInt64(todo.EvidenceSequenceNos, 3) || !containsInt64(todo.EvidenceSequenceNos, 10) {
		t.Fatalf("evidence = %v, want both 3 (original) and 10 (recap)", todo.EvidenceSequenceNos)
	}
	if len(state.EmergingTopics) != 0 {
		t.Fatalf("emergingTopics = %+v, want none created from a recap round", state.EmergingTopics)
	}
	decisionCount := 0
	for _, item := range state.Items {
		if item.Kind == "decision" {
			decisionCount++
		}
	}
	if decisionCount != 0 {
		t.Fatalf("decisionCount = %d, want 0 (recap restatement must not create a decision)", decisionCount)
	}
}
