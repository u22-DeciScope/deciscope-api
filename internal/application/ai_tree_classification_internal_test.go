package application

import (
	"encoding/json"
	"testing"
)

// このファイルは意味分類ポリシー(ai_tree_classification.go)のfixtureテスト。
// ユーザー要件の10シナリオ(明確なアジェンダ一致 / 複数アジェンダ / 単発の
// 突発話題 / 継続する突発話題 / アジェンダと同義の新話題 / 曖昧な発言 /
// topic増殖防止 / 再配置 / 最終reorganizer / 構造制約)を、実際の会議前
// アジェンダに相当する meetingContext とモデル出力JSONで検証する。

// classificationFixtureContext は実セッション(session_f91ff969d711fb56)と
// 同じ形のアジェンダを持つ会議コンテキスト。
func classificationFixtureContext() *meetingContext {
	return buildMeetingContext(&meetingSessionPreContext{
		Title:   "検証会議",
		Purpose: "会議終了処理と今後の検証項目を確認する",
		Agenda:  "1. 会議終了処理の確認\n2. 今後の検証項目",
	})
}

func marshalPayloadForTest(t *testing.T, payload liveAnalysisPayload) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

func mergeForTestWithConfig(t *testing.T, diff string, previous json.RawMessage, mc *meetingContext, round int64, cfg TreeClassificationConfig) liveAnalysisPayload {
	t.Helper()
	raw, err := parseAndMergeLiveAnalysisPayload(diff, previous, mc, round, nil, cfg)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	return merged
}

func itemByID(items []liveAnalysisItem, id string) *liveAnalysisItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func countTopics(tree *liveAnalysisTree) (agenda, dynamic, system int) {
	if tree == nil {
		return 0, 0, 0
	}
	for _, node := range tree.Nodes {
		if node.Kind != "topic" {
			continue
		}
		switch node.Origin {
		case topicOriginAgenda:
			agenda++
		case topicOriginDynamic:
			dynamic++
		default:
			system++
		}
	}
	return agenda, dynamic, system
}

// --- シナリオ1: 明確なアジェンダ一致 -----------------------------------------

func TestClassificationAssignsClearAgendaMatch(t *testing.T) {
	mc := classificationFixtureContext()
	diff := `{
		"summary": "検証項目を確認",
		"currentTopic": "今後の検証項目",
		"items": [
			{"id": "todo-final-transcript-check", "kind": "todo", "severity": "medium", "title": "終了直前の文字起こし反映確認", "body": "次回までに終了直前の文字起こしが反映されるか確認する", "status": "open"}
		],
		"assignments": [
			{"nodeId": "todo-final-transcript-check", "parentTopicId": "agenda-2", "confidence": 0.9, "reason": "今後の検証項目そのもの"}
		]
	}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	assertTreeInvariants(t, merged.Tree)
	node := treeNodeByID(merged.Tree, "todo-final-transcript-check")
	if node == nil || node.ParentID != "agenda-2" {
		t.Fatalf("node = %+v, want assigned to agenda-2", node)
	}
	item := itemByID(merged.Items, "todo-final-transcript-check")
	if item == nil || item.ClassificationStatus != classificationAssigned || item.AssignmentSource != assignmentSourceModel {
		t.Fatalf("item = %+v, want assigned by model", item)
	}
	if _, dynamic, _ := countTopics(merged.Tree); dynamic != 0 {
		t.Fatalf("dynamic topics = %d, want 0 (no topic for an agenda match)", dynamic)
	}
	if len(merged.EmergingTopics) != 0 {
		t.Fatalf("emergingTopics = %+v, want none", merged.EmergingTopics)
	}
}

// --- シナリオ2: 複数アジェンダに関連 -----------------------------------------

func TestClassificationKeepsSinglePrimaryParentForMultiAgendaItem(t *testing.T) {
	mc := classificationFixtureContext()
	// モデルが同じitemへ2つのアジェンダを提案しても、primary parentは1つで、
	// もう一方は候補(CandidateTopicID)として保持される。複数親は作らない。
	diff := `{
		"summary": "終了処理の改善を検証項目へ",
		"currentTopic": "今後の検証項目",
		"items": [
			{"id": "todo-endflow-to-checklist", "kind": "todo", "severity": "medium", "title": "終了処理の改善を検証項目に追加", "body": "終了処理の改善を次回の検証項目に追加する", "status": "open"}
		],
		"assignments": [
			{"nodeId": "todo-endflow-to-checklist", "parentTopicId": "agenda-2", "confidence": 0.8, "reason": "検証項目への追加"},
			{"nodeId": "todo-endflow-to-checklist", "parentTopicId": "agenda-1", "confidence": 0.5, "reason": "終了処理にも関連"}
		]
	}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	assertTreeInvariants(t, merged.Tree)
	node := treeNodeByID(merged.Tree, "todo-endflow-to-checklist")
	if node == nil || node.ParentID != "agenda-2" {
		t.Fatalf("node = %+v, want primary parent agenda-2", node)
	}
	item := itemByID(merged.Items, "todo-endflow-to-checklist")
	if item == nil || item.ClassificationStatus != classificationAssigned {
		t.Fatalf("item = %+v, want assigned", item)
	}
	if item.CandidateTopicID != "agenda-1" {
		t.Fatalf("candidateTopicId = %q, want related agenda-1 retained", item.CandidateTopicID)
	}
	incoming := 0
	for _, edge := range merged.Tree.Edges {
		if edge.Target == "todo-endflow-to-checklist" {
			incoming++
		}
	}
	if incoming != 1 {
		t.Fatalf("incoming edges = %d, want exactly 1 (no multi-parent)", incoming)
	}
}

// --- シナリオ3: 単発の突発話題 -------------------------------------------------

func TestClassificationDefersSingleShotNewTopic(t *testing.T) {
	mc := classificationFixtureContext()
	diff := `{
		"summary": "レポート形式の話題",
		"currentTopic": "レポート形式",
		"items": [
			{"id": "issue-pdf-export", "kind": "issue", "severity": "low", "title": "レポートのPDF出力案", "body": "レポートをPDFで出す案もある", "status": "open"}
		],
		"newTopics": [{"id": "topic-report-format", "label": "レポート形式"}],
		"assignments": [
			{"nodeId": "issue-pdf-export", "parentTopicId": "topic-report-format", "confidence": 0.8, "reason": "レポート形式の議論"}
		]
	}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	assertTreeInvariants(t, merged.Tree)
	if treeNodeByID(merged.Tree, "topic-report-format") != nil {
		t.Fatalf("single-shot topic must not be created immediately: %+v", merged.Tree.Nodes)
	}
	node := treeNodeByID(merged.Tree, "issue-pdf-export")
	if node == nil || node.ParentID != treeUnclassifiedTopicID {
		t.Fatalf("node = %+v, want held in %s", node, treeUnclassifiedTopicID)
	}
	item := itemByID(merged.Items, "issue-pdf-export")
	candidateID, _ := canonicalCandidateID("レポート形式", "")
	if item == nil || item.ClassificationStatus != classificationTentative || item.CandidateTopicID != candidateID {
		t.Fatalf("item = %+v, want tentative with candidate topic-report-format", item)
	}
	if len(merged.EmergingTopics) != 1 {
		t.Fatalf("emergingTopics = %+v, want 1 candidate", merged.EmergingTopics)
	}
	candidate := merged.EmergingTopics[0]
	if candidate.ID != candidateID || len(candidate.EvidenceItemIDs) != 1 || candidate.EvidenceItemIDs[0] != "issue-pdf-export" {
		t.Fatalf("candidate = %+v, want evidence [issue-pdf-export]", candidate)
	}
}

// --- シナリオ4: 継続する突発話題は昇格する -------------------------------------

func TestClassificationPromotesPersistentEmergingTopic(t *testing.T) {
	mc := classificationFixtureContext()
	round1 := `{
		"summary": "レポート形式の話題",
		"currentTopic": "レポート形式",
		"items": [
			{"id": "issue-pdf-export", "kind": "issue", "severity": "low", "title": "レポートのPDF出力案", "body": "PDF出力が必要", "status": "open"}
		],
		"newTopics": [{"id": "topic-report-format", "label": "レポート形式"}],
		"assignments": [
			{"nodeId": "issue-pdf-export", "parentTopicId": "topic-report-format", "confidence": 0.8, "reason": "レポート形式"}
		]
	}`
	state1 := mergeForTestAtRound(t, round1, nil, mc, 1)
	assertTreeInvariants(t, state1.Tree)

	round2 := `{
		"summary": "レポート形式の話題が継続",
		"currentTopic": "レポート形式",
		"items": [
			{"id": "issue-markdown-readability", "kind": "issue", "severity": "medium", "title": "Markdownの可読性懸念", "body": "Markdownは利用者に分かりにくい", "status": "open"}
		],
		"newTopics": [{"id": "topic-report-format", "label": "レポート形式"}],
		"assignments": [
			{"nodeId": "issue-markdown-readability", "parentTopicId": "topic-report-format", "confidence": 0.85, "reason": "レポート形式"}
		]
	}`
	state2 := mergeForTestAtRound(t, round2, marshalPayloadForTest(t, state1), mc, 2)
	assertTreeInvariants(t, state2.Tree)

	dynamicID, _ := canonicalCandidateID("レポート形式", "")
	topic := treeNodeByID(state2.Tree, dynamicID)
	if topic == nil || topic.Kind != "topic" || topic.Origin != topicOriginDynamic {
		t.Fatalf("topic = %+v, want promoted dynamic topic with stable id", topic)
	}
	for _, id := range []string{"issue-pdf-export", "issue-markdown-readability"} {
		node := treeNodeByID(state2.Tree, id)
		if node == nil || itemTopicID(state2.Tree, id) != dynamicID {
			t.Fatalf("node %s = %+v, want reparented under promoted topic", id, node)
		}
		item := itemByID(state2.Items, id)
		if item == nil || item.ClassificationStatus != classificationAssigned {
			t.Fatalf("item %s = %+v, want assigned after promotion", id, item)
		}
	}
	if len(state2.EmergingTopics) != 0 {
		t.Fatalf("emergingTopics = %+v, want cleared after promotion", state2.EmergingTopics)
	}

	// 昇格後のラウンドでは、既存dynamic topicとして直接割当できる(stable ID)。
	round3 := `{
		"summary": "共有方法も検討",
		"currentTopic": "レポート形式",
		"items": [
			{"id": "issue-web-share-url", "kind": "issue", "severity": "low", "title": "Web共有URLの検討", "body": "Web共有URLも検討したい", "status": "open"}
		],
		"assignments": [
			{"nodeId": "issue-web-share-url", "parentTopicId": "topic-report-format", "confidence": 0.85, "reason": "レポート形式"}
		]
	}`
	state3 := mergeForTestAtRound(t, round3, marshalPayloadForTest(t, state2), mc, 3)
	assertTreeInvariants(t, state3.Tree)
	node := treeNodeByID(state3.Tree, "issue-web-share-url")
	if node == nil || itemTopicID(state3.Tree, node.ID) != dynamicID {
		t.Fatalf("node = %+v, want assigned to the promoted topic", node)
	}
}

// --- シナリオ5: 新話題に見えるが既存agendaと同義 --------------------------------

func TestClassificationAliasesNewTopicMatchingAgendaLabel(t *testing.T) {
	mc := buildMeetingContext(&meetingSessionPreContext{
		Title:  "UI改善会議",
		Agenda: "1. UI・表示方法",
	})
	diff := `{
		"summary": "終了画面の表示改善",
		"currentTopic": "UI・表示方法",
		"items": [
			{"id": "issue-ending-loading", "kind": "issue", "severity": "medium", "title": "終了画面のローディング表示改善", "body": "終了画面のローディング表示を改善する", "status": "open"}
		],
		"newTopics": [{"id": "topic-ui-display", "label": "UI・表示方法"}],
		"assignments": [
			{"nodeId": "issue-ending-loading", "parentTopicId": "topic-ui-display", "confidence": 0.8, "reason": "UI表示の議論"}
		]
	}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	assertTreeInvariants(t, merged.Tree)
	if treeNodeByID(merged.Tree, "topic-ui-display") != nil {
		t.Fatalf("agenda-equivalent topic must not be duplicated: %+v", merged.Tree.Nodes)
	}
	node := treeNodeByID(merged.Tree, "issue-ending-loading")
	if node == nil || node.ParentID != "agenda-1" {
		t.Fatalf("node = %+v, want aliased into agenda-1", node)
	}
	if len(merged.EmergingTopics) != 0 {
		t.Fatalf("emergingTopics = %+v, want none (label duplicates agenda)", merged.EmergingTopics)
	}
}

// --- シナリオ6: 曖昧な発言は無理にagendaへ押し込まない ---------------------------

func TestClassificationDefersAmbiguousLowConfidence(t *testing.T) {
	mc := classificationFixtureContext()
	diff := `{
		"summary": "曖昧な発言",
		"currentTopic": "会議終了処理の確認",
		"items": [
			{"id": "question-vague", "kind": "question", "severity": "low", "title": "何かの確認が必要か", "body": "それも確認したほうがいいかもしれない", "status": "open"}
		],
		"assignments": [
			{"nodeId": "question-vague", "parentTopicId": "agenda-1", "confidence": 0.3, "reason": "文脈から終了処理の可能性"}
		]
	}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	assertTreeInvariants(t, merged.Tree)
	node := treeNodeByID(merged.Tree, "question-vague")
	if node == nil || node.ParentID != treeUnclassifiedTopicID {
		t.Fatalf("node = %+v, want held in %s instead of forced into agenda-1", node, treeUnclassifiedTopicID)
	}
	item := itemByID(merged.Items, "question-vague")
	if item == nil || item.ClassificationStatus != classificationTentative {
		t.Fatalf("item = %+v, want tentative", item)
	}
	// 後から再評価できるよう、候補とconfidenceが保持される。
	if item.CandidateTopicID != "agenda-1" || item.AssignmentConfidence != 0.3 {
		t.Fatalf("item = %+v, want candidate agenda-1 with confidence 0.3 retained", item)
	}
}

// --- シナリオ7: topic増殖防止 ---------------------------------------------------

func TestClassificationLimitsTopicGrowth(t *testing.T) {
	mc := classificationFixtureContext()
	// 1ラウンドに3つの単発新topicを提案しても、候補はラウンド上限(2)まで、
	// topicは1つも作られない。
	round1 := `{
		"summary": "雑多な単発話題",
		"currentTopic": "雑談",
		"items": [
			{"id": "issue-a1", "kind": "issue", "severity": "low", "title": "話題Aの論点", "body": "話題A", "status": "open"},
			{"id": "issue-b1", "kind": "issue", "severity": "low", "title": "話題Bの論点", "body": "話題B", "status": "open"},
			{"id": "issue-c1", "kind": "issue", "severity": "low", "title": "話題Cの論点", "body": "話題C", "status": "open"}
		],
		"newTopics": [
			{"id": "topic-a", "label": "話題A"},
			{"id": "topic-b", "label": "話題B"},
			{"id": "topic-c", "label": "話題C"}
		],
		"assignments": [
			{"nodeId": "issue-a1", "parentTopicId": "topic-a", "confidence": 0.8, "reason": "A"},
			{"nodeId": "issue-b1", "parentTopicId": "topic-b", "confidence": 0.8, "reason": "B"},
			{"nodeId": "issue-c1", "parentTopicId": "topic-c", "confidence": 0.8, "reason": "C"}
		]
	}`
	state1 := mergeForTestAtRound(t, round1, nil, mc, 1)
	assertTreeInvariants(t, state1.Tree)
	if _, dynamic, _ := countTopics(state1.Tree); dynamic != 0 {
		t.Fatalf("dynamic topics = %d, want 0 after single round", dynamic)
	}
	if len(state1.EmergingTopics) != maxEmergingCandidatesPerRound {
		t.Fatalf("emergingTopics = %+v, want capped at %d per round", state1.EmergingTopics, maxEmergingCandidatesPerRound)
	}

	// 2ラウンド目でAとBの両方が昇格条件を満たしても、1ラウンドの昇格は
	// maxPromotionsPerRound(1)件まで。
	round2 := `{
		"summary": "話題AとBが継続",
		"currentTopic": "話題A",
		"items": [
			{"id": "issue-a2", "kind": "issue", "severity": "low", "title": "話題Aの続き", "body": "話題Aの続き", "status": "open"},
			{"id": "issue-b2", "kind": "issue", "severity": "low", "title": "話題Bの続き", "body": "話題Bの続き", "status": "open"}
		],
		"assignments": [
			{"nodeId": "issue-a2", "parentTopicId": "topic-a", "confidence": 0.8, "reason": "A"},
			{"nodeId": "issue-b2", "parentTopicId": "topic-b", "confidence": 0.8, "reason": "B"}
		]
	}`
	state2 := mergeForTestAtRound(t, round2, marshalPayloadForTest(t, state1), mc, 2)
	assertTreeInvariants(t, state2.Tree)
	if _, dynamic, _ := countTopics(state2.Tree); dynamic != maxPromotionsPerRound {
		t.Fatalf("dynamic topics = %d, want %d (promotion is rate-limited)", dynamic, maxPromotionsPerRound)
	}
	topicAID, _ := canonicalCandidateID("話題A", "")
	topicBID, _ := canonicalCandidateID("話題B", "")
	if treeNodeByID(state2.Tree, topicAID) == nil {
		t.Fatalf("first candidate topic-a must be promoted first: %+v", state2.Tree.Nodes)
	}
	if treeNodeByID(state2.Tree, topicBID) != nil {
		t.Fatalf("topic-b must wait for the next round")
	}

	// 3ラウンド目にBが昇格する(取りこぼしの永久放置はしない)。
	round3 := `{"summary": "話題Bの継続", "currentTopic": "話題B", "items": [], "assignments": []}`
	state3 := mergeForTestAtRound(t, round3, marshalPayloadForTest(t, state2), mc, 3)
	assertTreeInvariants(t, state3.Tree)
	if treeNodeByID(state3.Tree, topicBID) == nil {
		t.Fatalf("topic-b must be promoted on the following round: %+v", state3.Tree.Nodes)
	}
}

func TestClassificationRespectsMaxDynamicTopics(t *testing.T) {
	mc := classificationFixtureContext()
	cfg := TreeClassificationConfig{MaxDynamicTopics: 1}
	previous := liveAnalysisPayload{
		Summary: "既にdynamic topicが1つある",
		Items: []liveAnalysisItem{
			{ID: "issue-x", Kind: "issue", Severity: "low", Title: "既存論点", Body: "既存", Status: "open"},
		},
		Tree: &liveAnalysisTree{
			Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "topic", Label: "検証会議"},
				{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "会議終了処理の確認", Origin: topicOriginAgenda},
				{ID: "agenda-2", Kind: "topic", ParentID: treeRootNodeID, Label: "今後の検証項目", Origin: topicOriginAgenda},
				{ID: "topic-existing", Kind: "topic", ParentID: treeRootNodeID, Label: "既存の動的topic", Origin: topicOriginDynamic},
				{ID: "issue-x", Kind: "issue", ParentID: "topic-existing", Label: "既存論点"},
			},
		},
		EmergingTopics: []emergingTopicCandidate{
			{ID: "topic-new", Label: "新しい話題", EvidenceItemIDs: []string{"issue-x"}, FirstRound: 1, LastRound: 1, RoundCount: 1},
		},
	}
	diff := `{
		"summary": "新しい話題が継続",
		"currentTopic": "新しい話題",
		"items": [
			{"id": "issue-y", "kind": "issue", "severity": "low", "title": "新しい話題の論点", "body": "続き", "status": "open"}
		],
		"assignments": [
			{"nodeId": "issue-y", "parentTopicId": "topic-new", "confidence": 0.8, "reason": "新話題"}
		]
	}`
	merged := mergeForTestWithConfig(t, diff, marshalPayloadForTest(t, previous), mc, 2, cfg)
	assertTreeInvariants(t, merged.Tree)
	if treeNodeByID(merged.Tree, "topic-new") != nil {
		t.Fatalf("dynamic topic cap must block promotion: %+v", merged.Tree.Nodes)
	}
	// 候補は破棄されず、上限に空きが出れば後で昇格できる。
	if len(merged.EmergingTopics) != 1 || merged.EmergingTopics[0].ID != "topic-new" {
		t.Fatalf("emergingTopics = %+v, want candidate retained", merged.EmergingTopics)
	}
}

// --- シナリオ8: 再配置(未分類→アジェンダ) ------------------------------------

func TestClassificationReparentsTentativeOnRepeatProposal(t *testing.T) {
	mc := classificationFixtureContext()
	round1 := `{
		"summary": "曖昧な発言",
		"currentTopic": "検証",
		"items": [
			{"id": "question-x", "kind": "question", "severity": "low", "title": "検証対象の確認", "body": "何を検証するか", "status": "open"}
		],
		"assignments": [
			{"nodeId": "question-x", "parentTopicId": "agenda-2", "confidence": 0.4, "reason": "検証項目の可能性"}
		]
	}`
	state1 := mergeForTestAtRound(t, round1, nil, mc, 1)
	node1 := treeNodeByID(state1.Tree, "question-x")
	if node1 == nil || node1.ParentID != treeUnclassifiedTopicID {
		t.Fatalf("round1 node = %+v, want tentative in unclassified", node1)
	}

	// 後続発言で同じ候補が再提案されたら(閾値未満でも)アジェンダへ引き上げる。
	round2 := `{
		"summary": "検証項目として明確化",
		"currentTopic": "今後の検証項目",
		"items": [],
		"assignments": [
			{"nodeId": "question-x", "parentTopicId": "agenda-2", "confidence": 0.45, "reason": "検証項目として言及"}
		]
	}`
	state2 := mergeForTestAtRound(t, round2, marshalPayloadForTest(t, state1), mc, 2)
	assertTreeInvariants(t, state2.Tree)
	node2 := treeNodeByID(state2.Tree, "question-x")
	if node2 == nil || node2.ParentID != "agenda-2" {
		t.Fatalf("round2 node = %+v, want reparented to agenda-2 on repeat", node2)
	}
	item := itemByID(state2.Items, "question-x")
	if item == nil || item.ClassificationStatus != classificationAssigned || item.CandidateTopicID != "" {
		t.Fatalf("item = %+v, want assigned with candidate cleared", item)
	}
	// 旧親(追加論点)のエッジが残っていないこと。子を失った追加論点は消える。
	for _, edge := range state2.Tree.Edges {
		if edge.Source == treeUnclassifiedTopicID && edge.Target == "question-x" {
			t.Fatalf("old unclassified edge must be removed: %+v", state2.Tree.Edges)
		}
	}
}

// --- hysteresis: assigned済みitemの移動は保留し、繰り返しで確定 -----------------

func TestClassificationHysteresisDefersAssignedMove(t *testing.T) {
	mc := classificationFixtureContext()
	previous := liveAnalysisPayload{
		Summary: "前回",
		Items: []liveAnalysisItem{
			{ID: "issue-a", Kind: "issue", Severity: "medium", Title: "課題A", Body: "説明A", Status: "open",
				ClassificationStatus: classificationAssigned, AssignmentConfidence: 0.8, AssignmentSource: assignmentSourceModel},
		},
		Tree: &liveAnalysisTree{
			Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "topic", Label: "検証会議"},
				{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "会議終了処理の確認", Origin: topicOriginAgenda},
				{ID: "agenda-2", Kind: "topic", ParentID: treeRootNodeID, Label: "今後の検証項目", Origin: topicOriginAgenda},
				{ID: "issue-a", Kind: "issue", ParentID: "agenda-1", Label: "課題A"},
			},
		},
	}
	// 記録済みconfidence(0.8)を十分に上回らない0.6の移動提案は保留される。
	round1 := `{
		"summary": "移動の提案",
		"currentTopic": "今後の検証項目",
		"items": [],
		"assignments": [
			{"nodeId": "issue-a", "parentTopicId": "agenda-2", "confidence": 0.6, "reason": "検証項目寄り"}
		]
	}`
	state1 := mergeForTestAtRound(t, round1, marshalPayloadForTest(t, previous), mc, 2)
	node1 := treeNodeByID(state1.Tree, "issue-a")
	if node1 == nil || node1.ParentID != "agenda-1" {
		t.Fatalf("node = %+v, want move deferred (hysteresis)", node1)
	}
	item1 := itemByID(state1.Items, "issue-a")
	if item1 == nil || item1.CandidateTopicID != "agenda-2" {
		t.Fatalf("item = %+v, want move candidate recorded", item1)
	}

	// 同じ提案が2ラウンド続いたら移動する。
	round2 := `{
		"summary": "移動の再提案",
		"currentTopic": "今後の検証項目",
		"items": [],
		"assignments": [
			{"nodeId": "issue-a", "parentTopicId": "agenda-2", "confidence": 0.6, "reason": "検証項目寄り"}
		]
	}`
	state2 := mergeForTestAtRound(t, round2, marshalPayloadForTest(t, state1), mc, 3)
	assertTreeInvariants(t, state2.Tree)
	node2 := treeNodeByID(state2.Tree, "issue-a")
	if node2 == nil || node2.ParentID != "agenda-2" {
		t.Fatalf("node = %+v, want moved after repeated proposal", node2)
	}
}

// --- モデルはitemの分類メタデータを直接書けない --------------------------------

func TestClassificationIgnoresModelSuppliedItemMetadata(t *testing.T) {
	mc := classificationFixtureContext()
	diff := `{
		"summary": "改ざんの試み",
		"currentTopic": "検証",
		"items": [
			{"id": "issue-inject", "kind": "issue", "severity": "low", "title": "論点", "body": "本文",
			 "status": "open", "classificationStatus": "assigned", "candidateTopicId": "agenda-1",
			 "assignmentConfidence": 0.99, "assignmentSource": "model"}
		]
	}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	item := itemByID(merged.Items, "issue-inject")
	// assignmentsチャネルが無いので、サーバー判定は「未分類」になるはず。
	if item == nil || item.ClassificationStatus != classificationUnclassified {
		t.Fatalf("item = %+v, want unclassified (model-embedded metadata ignored)", item)
	}
	if item.CandidateTopicID != "" || item.AssignmentConfidence != 0 {
		t.Fatalf("item = %+v, want injected candidate/confidence cleared", item)
	}
}

// --- シナリオ9: 最終reorganizerの制約 ------------------------------------------

func TestApplyTreeOperationsEnforcesClassificationConstraints(t *testing.T) {
	tree := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "検証会議"},
			{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "会議終了処理の確認"},
			{ID: "agenda-2", Kind: "topic", ParentID: treeRootNodeID, Label: "今後の検証項目"},
			{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: treeUnclassifiedTopicLabel},
			{ID: "todo-1", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "検証TODO"},
			{ID: "issue-2", Kind: "issue", ParentID: treeUnclassifiedTopicID, Label: "論点2"},
			{ID: "issue-3", Kind: "issue", ParentID: treeUnclassifiedTopicID, Label: "論点3"},
		},
	}
	for _, node := range tree.Nodes {
		if node.ParentID != "" {
			tree.Edges = append(tree.Edges, liveAnalysisTreeEdge{Source: node.ParentID, Target: node.ID})
		}
	}
	stats := &liveAnalysisTreeMergeStats{}
	ops := []treeOperation{
		// 未分類ノードのアジェンダへの再配置は許可される。
		{Type: "move_node", NodeID: "todo-1", ToParentID: "agenda-2"},
		// 1ノードのためのcreate_topicは拒否される(実セッションのゴミtopic対策)。
		{Type: "create_topic", TopicID: "topic-lonely", Label: "単発の話題"},
		{Type: "move_node", NodeID: "issue-2", ToParentID: "topic-lonely"},
		// アジェンダtopicのrenameは拒否される。
		{Type: "rename_topic", TopicID: "agenda-1", Label: "勝手な改名"},
	}
	rebuilt, applied := applyTreeOperations(tree, nil, ops, TreeClassificationConfig{}, stats)
	assertTreeInvariants(t, rebuilt)
	if applied != 1 {
		t.Fatalf("applied = %d, want only the agenda move", applied)
	}
	moved := treeNodeByID(rebuilt, "todo-1")
	if moved == nil || moved.ParentID != "agenda-2" {
		t.Fatalf("moved = %+v, want reparented to agenda-2", moved)
	}
	if treeNodeByID(rebuilt, "topic-lonely") != nil {
		t.Fatalf("single-node topic must be rejected")
	}
	renamed := treeNodeByID(rebuilt, "agenda-1")
	if renamed == nil || renamed.Label != "会議終了処理の確認" {
		t.Fatalf("agenda label = %+v, want unchanged", renamed)
	}
	if stats.ReorganizeRejections["create_topic_insufficient_moves"] != 1 || stats.ReorganizeRejections["rename_agenda_topic"] != 1 {
		t.Fatalf("rejections = %+v, want per-reason counts", stats.ReorganizeRejections)
	}

	// 2ノード以上を同時に移すcreate_topicは許可される。
	ops2 := []treeOperation{
		{Type: "create_topic", TopicID: "topic-valid", Label: "まとまった話題"},
		{Type: "move_node", NodeID: "issue-2", ToParentID: "topic-valid"},
		{Type: "move_node", NodeID: "issue-3", ToParentID: "topic-valid"},
	}
	rebuilt2, applied2 := applyTreeOperations(rebuilt, nil, ops2, TreeClassificationConfig{}, nil)
	assertTreeInvariants(t, rebuilt2)
	if applied2 != 3 {
		t.Fatalf("applied = %d, want create+2 moves", applied2)
	}
	topic := treeNodeByID(rebuilt2, "topic-valid")
	if topic == nil || topic.Origin != topicOriginDynamic {
		t.Fatalf("topic = %+v, want dynamic origin", topic)
	}
}

func TestApplyTreeOperationsRespectsDynamicTopicCap(t *testing.T) {
	tree := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
			{ID: "topic-existing", Kind: "topic", ParentID: treeRootNodeID, Label: "既存動的topic", Origin: topicOriginDynamic},
			{ID: "issue-1", Kind: "issue", ParentID: "topic-existing", Label: "論点1"},
			{ID: "issue-2", Kind: "issue", ParentID: "topic-existing", Label: "論点2"},
		},
	}
	for _, node := range tree.Nodes {
		if node.ParentID != "" {
			tree.Edges = append(tree.Edges, liveAnalysisTreeEdge{Source: node.ParentID, Target: node.ID})
		}
	}
	stats := &liveAnalysisTreeMergeStats{}
	ops := []treeOperation{
		{Type: "create_topic", TopicID: "topic-overflow", Label: "上限超過の話題"},
		{Type: "move_node", NodeID: "issue-1", ToParentID: "topic-overflow"},
		{Type: "move_node", NodeID: "issue-2", ToParentID: "topic-overflow"},
	}
	rebuilt, _ := applyTreeOperations(tree, nil, ops, TreeClassificationConfig{MaxDynamicTopics: 1}, stats)
	assertTreeInvariants(t, rebuilt)
	if treeNodeByID(rebuilt, "topic-overflow") != nil {
		t.Fatalf("dynamic topic cap must block create_topic")
	}
	if stats.ReorganizeRejections["create_topic_dynamic_cap"] != 1 {
		t.Fatalf("rejections = %+v, want create_topic_dynamic_cap", stats.ReorganizeRejections)
	}
}

// --- 不正assignmentの防御と観測 -------------------------------------------------

func TestClassificationRejectsUnknownParentWithoutBreakingPlacement(t *testing.T) {
	mc := classificationFixtureContext()
	previous := liveAnalysisPayload{
		Summary: "前回",
		Items: []liveAnalysisItem{
			{ID: "issue-a", Kind: "issue", Severity: "medium", Title: "課題A", Body: "説明A", Status: "open",
				ClassificationStatus: classificationAssigned, AssignmentConfidence: 0.9},
		},
		Tree: &liveAnalysisTree{
			Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "topic", Label: "検証会議"},
				{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "会議終了処理の確認", Origin: topicOriginAgenda},
				{ID: "agenda-2", Kind: "topic", ParentID: treeRootNodeID, Label: "今後の検証項目", Origin: topicOriginAgenda},
				{ID: "issue-a", Kind: "issue", ParentID: "agenda-1", Label: "課題A"},
			},
		},
	}
	// 配置済みitemへの不正な親IDは拒否され、現在の配置は保たれる。
	diff := `{
		"summary": "不正ID",
		"currentTopic": "検証",
		"items": [],
		"assignments": [
			{"nodeId": "issue-a", "parentTopicId": "agenda-99", "confidence": 0.9, "reason": "存在しない"}
		]
	}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(diff, marshalPayloadForTest(t, previous), mc, 2, nil, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	assertTreeInvariants(t, merged.Tree)
	node := treeNodeByID(merged.Tree, "issue-a")
	if node == nil || node.ParentID != "agenda-1" {
		t.Fatalf("node = %+v, want placement kept despite invalid parent id", node)
	}
	found := false
	for _, decision := range stats.AssignmentDecisions {
		if decision.ItemID == "issue-a" && decision.Decision == assignmentRejectedUnknown {
			found = true
		}
	}
	if !found {
		t.Fatalf("decisions = %+v, want rejected_unknown_parent recorded", stats.AssignmentDecisions)
	}
}

// --- 重複統合とassignmentの引き継ぎ ---------------------------------------------

func TestClassificationSurvivesDuplicateItemMerge(t *testing.T) {
	mc := classificationFixtureContext()
	round1 := `{
		"summary": "初回",
		"currentTopic": "検証",
		"items": [
			{"id": "issue-dup", "kind": "issue", "severity": "low", "title": "同じ論点", "body": "初回", "status": "open"}
		],
		"assignments": [
			{"nodeId": "issue-dup", "parentTopicId": "agenda-1", "confidence": 0.9, "reason": "終了処理"}
		]
	}`
	state1 := mergeForTestAtRound(t, round1, nil, mc, 1)

	// 同じタイトルを新しいidで再出力しても、既存idへ統合され分類も保たれる。
	round2 := `{
		"summary": "重複",
		"currentTopic": "検証",
		"items": [
			{"id": "issue-dup-2", "kind": "issue", "severity": "low", "title": "同じ論点", "body": "言い換え", "status": "open"}
		],
		"assignments": [
			{"nodeId": "issue-dup-2", "parentTopicId": "agenda-1", "confidence": 0.9, "reason": "終了処理"}
		]
	}`
	state2 := mergeForTestAtRound(t, round2, marshalPayloadForTest(t, state1), mc, 2)
	assertTreeInvariants(t, state2.Tree)
	if itemByID(state2.Items, "issue-dup-2") != nil {
		t.Fatalf("duplicate item must be merged into the existing id")
	}
	item := itemByID(state2.Items, "issue-dup")
	if item == nil || item.ClassificationStatus != classificationAssigned {
		t.Fatalf("item = %+v, want classification preserved across dedup", item)
	}
	node := treeNodeByID(state2.Tree, "issue-dup")
	if node == nil || node.ParentID != "agenda-1" {
		t.Fatalf("node = %+v, want parent preserved across dedup", node)
	}
}

// --- アジェンダ無し会議のbootstrap互換 -------------------------------------------

func TestClassificationBootstrapCreatesTopicsWithoutAgenda(t *testing.T) {
	// アジェンダも既存topicも無い会議では、従来通りnewTopicsが直ちにtopicになる
	// (全ノードが追加論点へ沈む退行を防ぐ)。
	diff := `{
		"summary": "開始",
		"currentTopic": "進捗",
		"items": [
			{"id": "issue-first", "kind": "issue", "severity": "low", "title": "最初の論点", "body": "", "status": "open"}
		],
		"newTopics": [{"id": "topic-progress", "label": "進捗確認"}],
		"assignments": [
			{"nodeId": "issue-first", "parentTopicId": "topic-progress", "confidence": 0.9, "reason": "進捗"}
		]
	}`
	merged := mergeForTest(t, diff, nil)
	assertTreeInvariants(t, merged.Tree)
	topic := treeNodeByID(merged.Tree, "topic-progress")
	if topic == nil || topic.Origin != topicOriginDynamic {
		t.Fatalf("topic = %+v, want created directly in bootstrap", topic)
	}
	node := treeNodeByID(merged.Tree, "issue-first")
	if node == nil || node.ParentID != "topic-progress" {
		t.Fatalf("node = %+v, want assigned", node)
	}
}

// --- 再編成後のitemメタデータ同期 -----------------------------------------------

func TestSyncItemsWithReorganizedTreeMarksReorganizerSource(t *testing.T) {
	before := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
			{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "議題1"},
			{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: treeUnclassifiedTopicLabel},
			{ID: "todo-1", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "TODO1"},
		},
	}
	after := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
			{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "議題1"},
			{ID: "todo-1", Kind: "todo", ParentID: "agenda-1", Label: "TODO1"},
		},
	}
	items := []liveAnalysisItem{
		{ID: "todo-1", Kind: "todo", Title: "TODO1", Status: "open",
			ClassificationStatus: classificationTentative, CandidateTopicID: "agenda-1"},
	}
	syncItemsWithReorganizedTree(items, before, after)
	if items[0].ClassificationStatus != classificationAssigned || items[0].AssignmentSource != assignmentSourceReorganizer {
		t.Fatalf("item = %+v, want assigned by reorganizer", items[0])
	}
	if items[0].CandidateTopicID != "" {
		t.Fatalf("item = %+v, want candidate cleared after applied move", items[0])
	}
}
