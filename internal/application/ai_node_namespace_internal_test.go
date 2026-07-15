package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestReservedAgendaItemIDIsRemappedWithoutChangingFixedTopic(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "渡り鳥の調査計画", Role: agendaRolePrimary}}}
	content := `{"summary":"","currentTopic":"渡り鳥","resolvedIds":[],"items":[{"id":"agenda-1","kind":"decision","severity":"high","title":"三地点で調査する","body":"海岸側、北側、南側で実施する","status":"open","evidenceSequenceNos":[9]}],"newTopics":[],"assignments":[{"nodeId":"agenda-1","parentTopicId":"agenda-1","confidence":0.9,"reason":""}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(content, nil, mc, 1, []int64{9}, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 1 || !strings.HasPrefix(state.Items[0].ID, "item-decision-") || reservedItemID(state.Items[0].ID) {
		t.Fatalf("items=%+v", state.Items)
	}
	if parentOf(state.Tree, state.Items[0].ID) != "agenda-1" {
		t.Fatalf("remapped assignment parent=%q tree=%+v", parentOf(state.Tree, state.Items[0].ID), state.Tree)
	}
	agenda := treeNodeByID(state.Tree, "agenda-1")
	if agenda == nil || agenda.Kind != "topic" || agenda.ParentID != treeRootNodeID || agenda.Label != "渡り鳥の調査計画" {
		t.Fatalf("fixed agenda=%+v", agenda)
	}
	diagnostics := validateTreeIntegrity(state.Tree, state.Items, mc)
	if !diagnostics.Valid || stats.ReservedItemIDsRemapped != 1 {
		t.Fatalf("diagnostics=%+v stats=%+v", diagnostics, stats)
	}
}

func TestLiveStrictSchemaUsesClientKeyAndPlannerCannotAddSecondActionAgenda(t *testing.T) {
	if !strings.Contains(liveAnalysisResponseJSONSchema, `"clientKey"`) || strings.Contains(liveAnalysisResponseJSONSchema, `"required": ["id", "kind"`) {
		t.Fatalf("live schema must use model clientKey: %s", liveAnalysisResponseJSONSchema)
	}
	fallback := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥", Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音", Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民資料", Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "今後の対応事項", Role: agendaRoleActionSummary},
	}}
	planned, err := parseContextPlannerResult(`{"agendaItems":[{"title":"渡り鳥","role":"primary"},{"title":"騒音","role":"primary"},{"title":"住民資料","role":"primary"},{"title":"今後の対応事項","role":"action_summary"},{"title":"追加論点","role":"action_summary"}]}`, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Agenda) != 4 || len(planned.actionSummaryAgendaIDs()) != 1 || planned.logicalActionSummaryAgendaID() != "agenda-4" {
		t.Fatalf("planned=%+v action=%v", planned.Agenda, planned.actionSummaryAgendaIDs())
	}
}

func TestCrossKindTopicCollisionRemapsItemAndRejectsSelfParent(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "既存議題", Role: agendaRolePrimary}}}
	content := `{"summary":"","currentTopic":"湿地","resolvedIds":[],"items":[{"id":"topic-wetland","kind":"open_issue","severity":"high","title":"湿地の植物が未確認","body":"種類を確認する必要がある","status":"open","evidenceSequenceNos":[25]},{"id":"item-risk-self","kind":"risk","severity":"medium","title":"自己参照テスト","body":"親が自身","status":"open","evidenceSequenceNos":[25]}],"newTopics":[{"id":"topic-wetland","label":"湿地・希少植物","description":""}],"assignments":[{"nodeId":"topic-wetland","parentTopicId":"topic-wetland","confidence":0.9,"reason":""},{"nodeId":"item-risk-self","parentTopicId":"item-risk-self","confidence":0.9,"reason":""}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(content, nil, mc, 1, []int64{25}, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	for _, item := range state.Items {
		if reservedItemID(item.ID) {
			t.Fatalf("reserved item persisted: %+v", item)
		}
	}
	if stats.SelfParentRejected != 1 {
		t.Fatalf("selfParentRejected=%d stats=%+v", stats.SelfParentRejected, stats)
	}
	if diagnostics := validateTreeIntegrity(state.Tree, state.Items, mc); !diagnostics.Valid {
		t.Fatalf("diagnostics=%+v tree=%+v", diagnostics, state.Tree)
	}
}

func TestFixedAgendaOperationsAndDisplayIDsAreRejected(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥", Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音", Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民資料", Role: agendaRolePrimary},
	}}
	tree := fixedAgendaSkeleton(mc)
	tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: "item-1", Kind: "todo", ParentID: "agenda-3", Label: "資料を公開"})
	tree.Edges = append(tree.Edges, liveAnalysisTreeEdge{Source: "agenda-3", Target: "item-1"})
	operations := []treeOperation{
		{Type: "move_node", NodeID: "agenda-3", ToParentID: "agenda-2"},
		{Type: "rename_topic", TopicID: "agenda-1", Label: "変更"},
		{Type: "merge_topic", FromTopicID: "agenda-1", IntoTopicID: "agenda-2"},
		{Type: "delete_empty_group", GroupID: "agenda-2"},
		{Type: "move_node", NodeID: "item-1", ToParentID: "agenda-3 [topic] 住民資料"},
	}
	stats := &liveAnalysisTreeMergeStats{}
	result, applied := applyTreeOperations(tree, mc, operations, TreeClassificationConfig{}, stats, 4)
	if applied != 0 || result != tree {
		t.Fatalf("applied=%d result=%+v", applied, result)
	}
	if stats.FixedAgendaOperationsRejected != 4 || stats.NonCanonicalNodeIDs != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	for index := 0; index < 4; index++ {
		if stats.ReorganizeOperations[index].Reason != "fixed_agenda_immutable" {
			t.Fatalf("operation[%d]=%+v", index, stats.ReorganizeOperations[index])
		}
	}
	if stats.ReorganizeOperations[4].Reason != "non_canonical_node_id" {
		t.Fatalf("display operation=%+v", stats.ReorganizeOperations[4])
	}
}

func TestTreeIntegrityFailurePreservesPreviousCanonicalTree(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "渡り鳥", Role: agendaRolePrimary}}}
	previous := fixedAgendaSkeleton(mc)
	broken := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
		{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "渡り鳥", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary},
		{ID: "agenda-1", Kind: "decision", ParentID: "agenda-1", Label: "三地点"},
	}}
	stats := &liveAnalysisTreeMergeStats{}
	selected, diagnostics, degraded := preserveTreeOnIntegrityFailure(
		broken,
		previous,
		[]liveAnalysisItem{{ID: "agenda-1", Kind: "decision", Title: "三地点", Status: "open"}},
		nil,
		mc,
		stats,
	)
	if !degraded || selected != previous || diagnostics.Valid {
		t.Fatalf("degraded=%t selected=%p previous=%p diagnostics=%+v", degraded, selected, previous, diagnostics)
	}
	if stats.TreePayloadRejected != 1 || stats.PreviousTreePreserved != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestLegacyCorruptPayloadIsRepairedForDeliveryWithoutDatabaseWrite(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "渡り鳥", Role: agendaRolePrimary}}}
	legacy := liveAnalysisPayload{
		Items: []liveAnalysisItem{{ID: "agenda-1", Kind: "decision", Severity: "high", Title: "三地点", Status: "open"}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
			{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "渡り鳥", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary},
			{ID: "agenda-1", Kind: "decision", ParentID: "agenda-1", Label: "三地点"},
		}, Edges: []liveAnalysisTreeEdge{{Source: treeRootNodeID, Target: "agenda-1"}, {Source: "agenda-1", Target: "agenda-1"}}},
	}
	payload, _ := json.Marshal(legacy)
	stored := &domain.MeetingAIAnalysis{SessionID: "session", Type: domain.MeetingAIAnalysisLive, Version: 4, Payload: payload}
	delivered := sanitizeLiveAnalysisForDelivery(stored, mc, TreeClassificationConfig{})
	if delivered == stored || string(stored.Payload) != string(payload) {
		t.Fatal("sanitizer must return a copy and leave the stored value untouched")
	}
	state := previousLiveAnalysisState(delivered.Payload)
	if !state.Degraded || state.DegradedReason == "" {
		t.Fatalf("delivery state=%+v", state)
	}
	if diagnostics := validateTreeIntegrity(state.Tree, state.Items, mc); !diagnostics.Valid {
		t.Fatalf("delivered diagnostics=%+v tree=%+v items=%+v", diagnostics, state.Tree, state.Items)
	}
}

func TestResolvedDetailsAndEmptyFixedAgendaRemainInCanonicalTree(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥", Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音", Role: agendaRolePrimary},
	}}
	items := []liveAnalysisItem{{ID: "item-open-bird", Kind: "open_issue", Severity: "high", Title: "観測地点不足", Status: "resolved"}}
	tree, _, _ := rebuildDiscussionTree(nil, mc, items, nil, []treeAssignment{{NodeID: "item-open-bird", ParentTopicID: "agenda-1", Confidence: 0.9}}, nil, nil, 2, TreeClassificationConfig{}, nil)
	resolved := treeNodeByID(tree, "item-open-bird")
	if resolved == nil || resolved.Status != "resolved" || resolved.ParentID != "agenda-1" {
		t.Fatalf("resolved=%+v", resolved)
	}
	if empty := treeNodeByID(tree, "agenda-2"); empty == nil || empty.ParentID != treeRootNodeID {
		t.Fatalf("empty fixed agenda=%+v", empty)
	}
	if diagnostics := validateTreeIntegrity(tree, items, mc); !diagnostics.Valid {
		t.Fatalf("diagnostics=%+v", diagnostics)
	}
}

func TestSession91f9cfe6aad64b7bDeterministicReplay(t *testing.T) {
	segments := session91f9cfe6aad64b7bSegments()
	if len(segments) != 30 {
		t.Fatalf("segments=%d", len(segments))
	}
	mc := &meetingContext{Title: "沿岸部風力発電計画に関する環境アセスメント検討会9", Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥の調査計画", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定の実施方法", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料の作成", Order: 3, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "今後の対応事項", Order: 4, Role: agendaRoleActionSummary},
		{ID: "agenda-5", Title: "追加対応", Order: 5, Role: agendaRoleActionSummary},
	}}
	content := `{"summary":"fixture","currentTopic":"まとめ","resolvedIds":[],"resolutionUpdates":[{"itemId":"risk-bird-sites","status":"resolved","evidenceSequenceNos":[7],"reason":"追加地点で観測地点不足に対応できる"}],"items":[
	{"id":"risk-bird-sites","kind":"risk","severity":"high","title":"渡り鳥の観測地点不足","body":"一地点では移動経路を確認できない","status":"open","evidenceSequenceNos":[4,7]},
	{"id":"agenda-1","kind":"decision","severity":"high","title":"渡り鳥を三地点で調査する","body":"海岸側、北側、南側で実施する","status":"open","evidenceSequenceNos":[9]},
	{"id":"agenda-2","kind":"decision","severity":"high","title":"騒音を合計三回測定する","body":"昼間一回、夜間二回実施する","status":"open","evidenceSequenceNos":[13]},
	{"id":"open-wind","kind":"open_issue","severity":"high","title":"強風日の風速基準が未確定","body":"気象データを確認して判断する","status":"open","evidenceSequenceNos":[14,15,30]},
	{"id":"todo-weather","kind":"todo","severity":"medium","title":"気象データを確認する","body":"強風日の基準風速を判断する","status":"open","evidenceSequenceNos":[15]},
	{"id":"decision-web","kind":"decision","severity":"high","title":"調査結果をWeb公開する","body":"住民が後から確認できるようにする","status":"open","evidenceSequenceNos":[18,29]},
	{"id":"decision-diagram","kind":"decision","severity":"medium","title":"公開資料に図と説明を付ける","body":"専門用語だけにしない","status":"open","evidenceSequenceNos":[19]},
	{"id":"open-meeting-date","kind":"open_issue","severity":"high","title":"住民説明会の開催日が未確定","body":"自治会から候補日を受け取る","status":"open","evidenceSequenceNos":[20,21,30]},
	{"id":"todo-meeting-date","kind":"todo","severity":"medium","title":"自治会から候補日を受け取る","body":"説明会開催日を確定する","status":"open","evidenceSequenceNos":[21]},
	{"id":"todo-wetland","kind":"todo","severity":"medium","title":"湿地の予備調査を検討する","body":"専門家に希少植物を確認する","status":"open","evidenceSequenceNos":[25,26,27,30]}
	],"newTopics":[{"id":"topic-wetland","label":"湿地・希少植物","description":"アジェンダ外の調査課題"}],"assignments":[
	{"nodeId":"risk-bird-sites","parentTopicId":"agenda-1","confidence":0.9,"reason":""},
	{"nodeId":"agenda-1","parentTopicId":"agenda-1","confidence":0.9,"reason":""},
	{"nodeId":"agenda-2","parentTopicId":"agenda-2","confidence":0.9,"reason":""},
	{"nodeId":"open-wind","parentTopicId":"agenda-2","confidence":0.9,"reason":""},
	{"nodeId":"todo-weather","parentTopicId":"agenda-2","confidence":0.9,"reason":""},
	{"nodeId":"decision-web","parentTopicId":"agenda-3","confidence":0.9,"reason":""},
	{"nodeId":"decision-diagram","parentTopicId":"agenda-3","confidence":0.9,"reason":""},
	{"nodeId":"open-meeting-date","parentTopicId":"agenda-3","confidence":0.9,"reason":""},
	{"nodeId":"todo-meeting-date","parentTopicId":"agenda-4","confidence":0.9,"reason":""},
	{"nodeId":"todo-wetland","parentTopicId":"topic-wetland","confidence":0.9,"reason":""}
	]}`
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{}, CurrentRound: map[int64]struct{}{}, TranscriptText: map[int64]string{}, CoveredThrough: 30}
	roundSeqNos := make([]int64, 0, len(segments))
	for index, text := range segments {
		sequenceNo := int64(index + 1)
		scope.Allowed[sequenceNo] = struct{}{}
		scope.CurrentRound[sequenceNo] = struct{}{}
		scope.TranscriptText[sequenceNo] = text
		roundSeqNos = append(roundSeqNos, sequenceNo)
	}
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(content, nil, mc, 12, roundSeqNos, scope, TreeClassificationConfig{PromotionMinItems: 1, PromotionMinRounds: 1}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	diagnostics := validateTreeIntegrity(state.Tree, state.Items, mc)
	if !diagnostics.Valid {
		t.Fatalf("diagnostics=%+v tree=%+v", diagnostics, state.Tree)
	}
	for _, agendaID := range []string{"agenda-1", "agenda-2", "agenda-3"} {
		node := treeNodeByID(state.Tree, agendaID)
		if node == nil || node.Kind != "topic" || node.ParentID != treeRootNodeID {
			t.Fatalf("fixed agenda %s=%+v", agendaID, node)
		}
	}
	for _, actionID := range []string{"agenda-4", "agenda-5"} {
		if node := treeNodeByID(state.Tree, actionID); node != nil {
			t.Fatalf("action summary leaked into tree: %+v", node)
		}
	}
	for _, item := range state.Items {
		if reservedItemID(item.ID) {
			t.Fatalf("reserved item=%+v", item)
		}
	}
	if stats.SourceActionSummaryAgendaCount != 2 || stats.LogicalActionSummaryCount != 1 || stats.RenderedActionItems > stats.ActionSummaryCandidates {
		t.Fatalf("action stats=%+v", stats)
	}
	if itemTopicID(state.Tree, "open-wind") != "agenda-2" || itemTopicID(state.Tree, "decision-web") != "agenda-3" || itemTopicID(state.Tree, "todo-wetland") != "topic-wetland" {
		t.Fatalf("parents wind=%s web=%s wetland=%s assignments=%+v transitions=%+v", parentOf(state.Tree, "open-wind"), parentOf(state.Tree, "decision-web"), parentOf(state.Tree, "todo-wetland"), stats.AssignmentDecisions, stats.AgendaTransitions)
	}
	resolved := findItemByID(state.Items, "risk-bird-sites")
	if resolved == nil || resolved.Status != "resolved" || treeNodeByID(state.Tree, "risk-bird-sites") == nil {
		t.Fatalf("resolved history=%+v", resolved)
	}
	if len(state.Tree.Edges) != len(state.Tree.Nodes)-1 || state.Degraded {
		t.Fatalf("nodes=%d edges=%d degraded=%t integrity=%+v", len(state.Tree.Nodes), len(state.Tree.Edges), state.Degraded, state.TreeIntegrity)
	}
	agendaCounts := itemTopicCounts(state.Tree, state.Items)
	encoded, _ := json.Marshal(diagnostics)
	t.Logf("session_91f9 replay nodes=%d edges=%d fixed=3 agendaItems=%v sourceAction=2 logicalAction=1 renderedAction=%d resolvedVisible=1 diagnostics=%s coverage=30 incomplete=false", len(state.Tree.Nodes), len(state.Tree.Edges), agendaCounts, stats.RenderedActionItems, encoded)
}

func session91f9cfe6aad64b7bSegments() []string {
	return []string{
		"それでは、沿岸部風力発電計画に関する環境アセスメント検討会を始めます。",
		"まず、渡り鳥の調査計画について確認します。",
		"事前調査では、風力発電設備の建設予定地付近を春と秋に複数の渡り鳥が通過する可能性があるとされています。",
		"現在の計画では海岸側の観測地点が一カ所しかなく、鳥の移動経路を十分に確認できていないのではないかという懸念が出ていました。",
		"これについて、現地担当者から会館側に加えて、予定地の北側と南側にも観測地点を設置できるという回答がありました。",
		"3方向から観測すれば、主な飛行経路と飛行行動を確認できる見込みです。",
		"したがって、観測地点が不足しているという問題は、追加地点を設けることで対応できると判断します。",
		"この論点は解決済みとします。",
		"渡り鳥の調査については、海岸側、北側、南側の合計三地点で実施することを決定します。",
		"次に、騒音測定の実施方法についてです。",
		"周辺住民からは昼間よりも夜間の低周波音を心配する声が出ています。当初の計画では昼間のみ2回測定する予定でしたが、それでは住民の懸念に十分対応できません。",
		"そこで、昼間に1回、夜間に2回測定する案を採用したいと思います。夜間の測定は風邪が比較的弱い人、強い日に。",
		"1回ずつ実施します。騒音測定は昼間1回、夜間2回の合計3回実施することを決定します。",
		"ただし、強風日の測定条件については、どの風速を基準にするか決まっていません。",
		"この点は気象データを確認してから判断するため、現時点では未解決の課題として残します。",
		"続いて、住民説明資料についてです。",
		"現在の資料には設備の位置と調査日程は記載されていますが、調査結果をどのように公開するかが書かれていません。",
		"住民が後から確認できるよう、調査結果の概要や団体のWeb制度で公開する方針にします。",
		"公開する資料には、専門用語だけでなく、図や簡単な説明をつけることも決定します。",
		"一方説明会そのものの開催日は、市域の自治会と調節できていません。",
		"開催日はまだ決定せず、自治会から候補日を受け取った後に改めて確定します。",
		"最後に。アジェンダにはありませんでしたが。",
		"現地担当者から新しい報告があります。",
		"建設予定地の近くには。",
		"ええ。小規模な湿地が見つかり、希少な植物が生育している可能性があるとのことです。",
		"現時点では植物の種類が確認できていないため、既存の鳥類調査や騒音調査の中に無理に含めず、新しい調査課題として扱う必要があります。",
		"植物の種類を確認するため、専門家による予備調査を実施するかどうかを次回の会議で検討をします。",
		"井戸をまとめます。",
		"まあ、さりどりの観測地点不足については、三地点で調査することで解決済みです。決定事項は、渡り鳥を三地点で調査すること、騒音を広間1回と約2回測定すること、そして調査結果を図付けでウェブ公開することです。",
		"未解決の課題は強風日の風速基準と住民説明会の開催日です。また、設置の希少植物については、アジェンダ街から生まれた新しい論点として次回以降も検討します。",
	}
}
