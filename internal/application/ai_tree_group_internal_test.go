package application

import (
	"encoding/json"
	"testing"

	"deciscope-core-api/internal/domain"
)

func discussionTreeFixture(topicID string, itemKinds ...string) *liveAnalysisTree {
	nodes := []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
		{ID: topicID, Kind: "topic", ParentID: treeRootNodeID, Label: "議題", Origin: topicOriginAgenda},
	}
	edges := []liveAnalysisTreeEdge{{Source: treeRootNodeID, Target: topicID}}
	for index, kind := range itemKinds {
		id := "item-" + string(rune('a'+index))
		nodes = append(nodes, liveAnalysisTreeNode{ID: id, Kind: kind, ParentID: topicID, Label: id, Status: "open", RelatedItemIDs: []string{id}})
		edges = append(edges, liveAnalysisTreeEdge{Source: topicID, Target: id})
	}
	return &liveAnalysisTree{Nodes: nodes, Edges: edges}
}

func TestCreateGroupBuildsControlledDepthThreeTree(t *testing.T) {
	tree := discussionTreeFixture("agenda-1", "risk", "fact", "decision")
	stats := &liveAnalysisTreeMergeStats{}
	rebuilt, applied := applyTreeOperations(tree, nil, []treeOperation{{
		Type: "create_group", ParentTopicID: "agenda-1", Label: "観測地点の不足",
		EvidenceItemIDs: []string{"item-a", "item-b", "item-c"},
	}}, TreeClassificationConfig{}, stats)
	if applied != 1 || stats.ReorganizeApplied != 1 {
		t.Fatalf("applied=%d stats=%+v", applied, stats)
	}
	groupID := stableGroupID("agenda-1", "観測地点の不足")
	parents := map[string]string{}
	kinds := map[string]string{}
	for _, node := range rebuilt.Nodes {
		parents[node.ID] = node.ParentID
		kinds[node.ID] = node.Kind
	}
	if kinds[groupID] != "group" || parents[groupID] != "agenda-1" {
		t.Fatalf("group kind/parent = %q/%q", kinds[groupID], parents[groupID])
	}
	for _, id := range []string{"item-a", "item-b", "item-c"} {
		if parents[id] != groupID {
			t.Fatalf("parent[%s] = %q, want %q", id, parents[id], groupID)
		}
	}
	assertControlledTreeInvariants(t, rebuilt, 3)
	if depth := treeDepthOf(rebuilt); depth != 3 {
		t.Fatalf("depth=%d, want 3", depth)
	}
}

func TestCreateGroupRejectsSingleEvidenceItem(t *testing.T) {
	tree := discussionTreeFixture("agenda-1", "risk")
	stats := &liveAnalysisTreeMergeStats{}
	rebuilt, applied := applyTreeOperations(tree, nil, []treeOperation{{
		Type: "create_group", ParentTopicID: "agenda-1", Label: "単発group", EvidenceItemIDs: []string{"item-a"},
	}}, TreeClassificationConfig{}, stats)
	if applied != 0 || rebuilt != tree {
		t.Fatalf("single evidence group must be unchanged")
	}
	if stats.ReorganizeRejected != 1 || stats.ReorganizeRejections["insufficient_evidence"] != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestTreeOperationEvaluationSeparatesAppliedNoopRejectedInvalid(t *testing.T) {
	tree := discussionTreeFixture("agenda-1", "risk", "fact", "decision")
	groupID := stableGroupID("agenda-1", "観測地点")
	stats := &liveAnalysisTreeMergeStats{}
	_, applied := applyTreeOperations(tree, nil, []treeOperation{
		{Type: "create_group", ParentTopicID: "agenda-1", Label: "観測地点", EvidenceItemIDs: []string{"item-a", "item-b"}},
		{Type: "move_nodes", ToParentID: groupID, NodeIDs: []string{"item-a", "item-b"}},
		{Type: "create_group", ParentTopicID: "agenda-1", Label: "一子", EvidenceItemIDs: []string{"item-c"}},
		{Type: "move_node", NodeID: "missing", ToParentID: "agenda-1"},
	}, TreeClassificationConfig{}, stats)
	if applied != 1 {
		t.Fatalf("applied=%d, want 1", applied)
	}
	if stats.ReorganizeProposed != 4 || stats.ReorganizeApplied != 1 || stats.ReorganizeNoop != 1 || stats.ReorganizeRejected != 1 || stats.ReorganizeInvalid != 1 {
		t.Fatalf("operation counts=%+v", stats)
	}
	if len(stats.ReorganizeOperations) != 4 {
		t.Fatalf("evaluations=%d, want 4", len(stats.ReorganizeOperations))
	}
}

func TestReorganizationReducesFlatTopicChildrenWithTwoGroups(t *testing.T) {
	tree := discussionTreeFixture("agenda-2", "risk", "question", "fact", "decision", "todo", "issue", "question")
	before := computeTreeHealth(tree)
	if !before.needsReorganization() || before.MaxTopicChildren != 7 {
		t.Fatalf("before=%+v", before)
	}
	rebuilt, applied := applyTreeOperations(tree, nil, []treeOperation{
		{Type: "create_group", ParentTopicID: "agenda-2", Label: "測定回数", EvidenceItemIDs: []string{"item-a", "item-b", "item-c"}},
		{Type: "create_group", ParentTopicID: "agenda-2", Label: "強風日の条件", EvidenceItemIDs: []string{"item-d", "item-e", "item-f"}},
	}, TreeClassificationConfig{}, &liveAnalysisTreeMergeStats{})
	if applied != 2 {
		t.Fatalf("applied=%d", applied)
	}
	after := computeTreeHealth(rebuilt)
	if after.GroupCount != 2 || after.MaxTopicChildren != 1 || after.SingleChildGroupCount != 0 {
		t.Fatalf("after=%+v", after)
	}
	assertControlledTreeInvariants(t, rebuilt, 3)
}

func TestCreateGroupDoesNotCombineItemsAcrossTopics(t *testing.T) {
	tree := discussionTreeFixture("agenda-1", "risk")
	tree.Nodes = append(tree.Nodes,
		liveAnalysisTreeNode{ID: "agenda-2", Kind: "topic", ParentID: treeRootNodeID, Label: "別議題", Origin: topicOriginAgenda},
		liveAnalysisTreeNode{ID: "item-b", Kind: "todo", ParentID: "agenda-2", Label: "別件", Status: "open"},
	)
	tree.Edges = append(tree.Edges,
		liveAnalysisTreeEdge{Source: treeRootNodeID, Target: "agenda-2"},
		liveAnalysisTreeEdge{Source: "agenda-2", Target: "item-b"},
	)
	stats := &liveAnalysisTreeMergeStats{}
	_, applied := applyTreeOperations(tree, nil, []treeOperation{{Type: "create_group", ParentTopicID: "agenda-1", Label: "混在", EvidenceItemIDs: []string{"item-a", "item-b"}}}, TreeClassificationConfig{}, stats)
	if applied != 0 || stats.ReorganizeRejections["cross_topic_group_evidence"] != 1 {
		t.Fatalf("applied=%d stats=%+v", applied, stats)
	}
}

func TestActionSummaryAgendaReferencesCanonicalActiveItems(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "鳥類", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "横断対応", Order: 3, Role: agendaRoleActionSummary},
	}}
	diff := `{"summary":"更新","currentTopic":"騒音","resolvedIds":[],"items":[{"id":"todo-wind","kind":"todo","severity":"high","title":"風速基準を決める","body":"気象データ確認後に決める","status":"open"},{"id":"question-done","kind":"question","severity":"medium","title":"解決済み質問","body":"回答済み","status":"resolved"},{"id":"decision-count","kind":"decision","severity":"high","title":"三回測定","body":"回数を決定","status":"open"}],"assignments":[{"nodeId":"todo-wind","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"question-done","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"decision-count","parentTopicId":"agenda-2","confidence":0.9}]}`
	raw, err := parseAndMergeLiveAnalysisPayload(diff, nil, mc, 1, []int64{1}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	var todo liveAnalysisItem
	for _, item := range state.Items {
		if item.ID == "todo-wind" {
			todo = item
		}
	}
	if len(todo.RelatedAgendaIDs) != 1 || todo.RelatedAgendaIDs[0] != "agenda-4" {
		t.Fatalf("relatedAgendaIds=%v", todo.RelatedAgendaIDs)
	}
	parents := map[string]string{}
	for _, node := range state.Tree.Nodes {
		parents[node.ID] = node.ParentID
		if node.ID == "agenda-4" {
			t.Fatal("action summary must not be a canonical tree node")
		}
	}
	if parents["todo-wind"] != "agenda-2" {
		t.Fatalf("canonical parent=%q, want agenda-2", parents["todo-wind"])
	}
	for _, edge := range state.Tree.Edges {
		if edge.Source == "agenda-4" && edge.Target == "todo-wind" {
			t.Fatal("cross-cutting reference must not create a second parent edge")
		}
	}
}

func TestLegacyDepthTwoPayloadRemainsReadable(t *testing.T) {
	tree := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{{ID: "root", Kind: "topic", Label: "会議"}, {ID: "agenda-1", Kind: "topic", Label: "議題"}, {ID: "risk-1", Kind: "risk", Label: "懸念"}},
		Edges: []liveAnalysisTreeEdge{{Source: "root", Target: "agenda-1"}, {Source: "agenda-1", Target: "risk-1"}},
	}
	_, parents, relations := treeStateFromPayloadTree(tree)
	if parents["agenda-1"] != "root" || parents["risk-1"] != "agenda-1" || len(relations) != 0 {
		t.Fatalf("parents=%v relations=%v", parents, relations)
	}
}

func TestNestedGroupsUseSoftDepthFourAndRejectWeakDepthFive(t *testing.T) {
	tree := discussionTreeFixture("agenda-1", "risk", "fact", "question", "open_issue", "todo", "decision", "issue", "risk")
	level2 := stableGroupID("agenda-1", "測定条件")
	level3 := stableGroupID(level2, "気象条件")
	stats := &liveAnalysisTreeMergeStats{}
	rebuilt, applied := applyTreeOperations(tree, nil, []treeOperation{
		{Type: "create_group", ParentID: "agenda-1", Label: "測定条件", EvidenceItemIDs: []string{"item-a", "item-b", "item-c", "item-d", "item-e", "item-f", "item-g"}},
		{Type: "create_group", ParentID: level2, Label: "気象条件", EvidenceItemIDs: []string{"item-a", "item-b", "item-c", "item-d", "item-e"}},
		{Type: "create_group", ParentID: level3, Label: "強風条件", EvidenceItemIDs: []string{"item-a", "item-b"}},
	}, TreeClassificationConfig{}, stats, 9)
	if applied != 2 || stats.ReorganizeRejections["hard_depth_insufficient_evidence"] != 1 {
		t.Fatalf("applied=%d stats=%+v", applied, stats)
	}
	if depth := treeDepthOf(rebuilt); depth != treeSoftMaxDepth {
		t.Fatalf("depth=%d, want soft max %d", depth, treeSoftMaxDepth)
	}
	health := computeTreeHealth(rebuilt)
	if health.NestedGroupCount != 1 || health.MaxGroupChildren < 2 {
		t.Fatalf("health=%+v", health)
	}
	assertControlledTreeInvariants(t, rebuilt, treeSoftMaxDepth)
}

func TestStrongEvidenceAllowsDepthFiveButHardLimitRejectsDepthSix(t *testing.T) {
	tree := discussionTreeFixture("agenda-1", "risk", "fact", "question", "open_issue", "todo", "decision", "issue", "risk", "fact", "todo")
	level2 := stableGroupID("agenda-1", "測定条件")
	level3 := stableGroupID(level2, "気象条件")
	level4 := stableGroupID(level3, "強風条件")
	stats := &liveAnalysisTreeMergeStats{}
	rebuilt, applied := applyTreeOperations(tree, nil, []treeOperation{
		{Type: "create_group", ParentID: "agenda-1", Label: "測定条件", EvidenceItemIDs: []string{"item-a", "item-b", "item-c", "item-d", "item-e", "item-f", "item-g", "item-h", "item-i"}},
		{Type: "create_group", ParentID: level2, Label: "気象条件", EvidenceItemIDs: []string{"item-a", "item-b", "item-c", "item-d", "item-e", "item-f", "item-g"}},
		{Type: "create_group", ParentID: level3, Label: "強風条件", EvidenceItemIDs: []string{"item-a", "item-b", "item-c", "item-d"}},
		{Type: "create_group", ParentID: level4, Label: "閾値候補", EvidenceItemIDs: []string{"item-a", "item-b"}},
	}, TreeClassificationConfig{}, stats, 12)
	if applied != 3 || stats.ReorganizeRejections["hard_depth_limit"] != 1 {
		t.Fatalf("applied=%d stats=%+v", applied, stats)
	}
	if depth := treeDepthOf(rebuilt); depth != treeHardMaxDepth {
		t.Fatalf("depth=%d, want hard max %d", depth, treeHardMaxDepth)
	}
	assertControlledTreeInvariants(t, rebuilt, treeHardMaxDepth)
}

func TestUnderfilledGroupUsesTwoVersionFlatteningHysteresis(t *testing.T) {
	tree := discussionTreeFixture("agenda-1", "risk")
	groupID := stableGroupID("agenda-1", "一時的なまとまり")
	tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: groupID, Kind: "group", ParentID: "agenda-1", Label: "一時的なまとまり"})
	for index := range tree.Nodes {
		if tree.Nodes[index].ID == "item-a" {
			tree.Nodes[index].ParentID = groupID
		}
	}
	tree.Edges = []liveAnalysisTreeEdge{{Source: "root", Target: "agenda-1"}, {Source: "agenda-1", Target: groupID}, {Source: groupID, Target: "item-a"}}

	version10 := reassembleTreeAtVersion(tree, 10)
	group := treeNodeByID(version10, groupID)
	if group == nil || group.UnderfilledSinceVersion != 10 {
		t.Fatalf("version10 group=%+v", group)
	}
	version11 := reassembleTreeAtVersion(version10, 11)
	if treeNodeByID(version11, groupID) == nil {
		t.Fatal("group flattened before grace window elapsed")
	}
	version12 := reassembleTreeAtVersion(version11, 12)
	if treeNodeByID(version12, groupID) != nil || treeNodeByID(version12, "item-a").ParentID != "agenda-1" {
		t.Fatalf("version12 tree=%+v", version12.Nodes)
	}
}

func reassembleTreeAtVersion(tree *liveAnalysisTree, version int64) *liveAnalysisTree {
	nodes, parents, relations := treeStateFromPayloadTree(tree)
	topics := make(map[string]liveAnalysisTreeNode)
	groups := make(map[string]liveAnalysisTreeNode)
	topicOrder, groupOrder := []string{}, []string{}
	details := []liveAnalysisTreeNode{}
	for _, node := range nodes {
		switch {
		case node.ID == treeRootNodeID:
		case node.Kind == "topic":
			topics[node.ID] = node
			topicOrder = append(topicOrder, node.ID)
		case node.Kind == "group":
			groups[node.ID] = node
			groupOrder = append(groupOrder, node.ID)
		default:
			details = append(details, node)
		}
	}
	return assembleTree(nil, topics, topicOrder, groups, groupOrder, details, parents, parents, relations, version, nil)
}

func TestTreeChangesReportOnlyStructuralAndSemanticUpdates(t *testing.T) {
	previous := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: "root", Kind: "topic", Label: "会議"},
		{ID: "agenda-1", Kind: "topic", ParentID: "root", Label: "議題"},
		{ID: "item-a", Kind: "todo", ParentID: "agenda-1", Label: "確認", Status: "open"},
	}}
	current := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: "root", Kind: "topic", Label: "会議"},
		{ID: "agenda-1", Kind: "topic", ParentID: "root", Label: "議題"},
		{ID: "group-1", Kind: "group", ParentID: "agenda-1", Label: "対応"},
		{ID: "item-a", Kind: "decision", ParentID: "group-1", Label: "確認", Status: "resolved"},
	}}
	changes := diffLiveAnalysisTrees(previous, current, 7)
	if changes == nil || changes.TreeVersion != 7 || len(changes.NewNodeIDs) != 1 || changes.NewNodeIDs[0] != "group-1" || len(changes.ReparentedNodeIDs) != 1 || len(changes.ResolvedNodeIDs) != 1 || len(changes.PromotedNodeIDs) != 1 {
		t.Fatalf("changes=%+v", changes)
	}
	if diffLiveAnalysisTrees(current, current, 8) != nil {
		t.Fatal("unchanged structure must not create focus changes")
	}
}

// This replay fixture uses all 33 persisted final utterances from
// session_83c10700a83ee771 and a deterministic mock of the observed live-model
// failure mode (explicit decisions returned as todo). It intentionally does
// not call or mutate the production database.
func TestSession83c10700ReplayRepairsDecisionsGroupsAndActionSummary(t *testing.T) {
	segments := []domain.TranscriptSegment{
		finalSegment(1, "それでは、沿岸部風力発電計画に関する環境アセスメント検討会を始めます。"),
		finalSegment(2, "まず、渡り鳥の調査計画について確認します。"),
		finalSegment(3, "事前調査では、風力発電設備の建設予定地付近を春と。"),
		finalSegment(4, "秋に複数の渡り鳥が通過する可能性があるとされています。"),
		finalSegment(5, "現在の計画では海岸側の観測地点が一カ所しかなく、鳥の移動経路を十分に確認できていないのではないかという懸念が出ていました。"),
		finalSegment(6, "これについて、現地担当者から海岸側に加えて、予定地の北側と南側にも観測地点を設置できるという回答がありました。"),
		finalSegment(7, "したがって、観測地点が不足しているという問題は、追加地点を設けることで対応できると判断します。"),
		finalSegment(8, "この論点は解決済みとします。"),
		finalSegment(9, "渡り鳥の調査については、海岸側、北側、南側の合計三地点で実施することを決定します。"),
		finalSegment(10, "次に、騒音測定の実施方法についてです。"),
		finalSegment(11, "周辺住民からは、昼間よりも夜間の低周波音。"),
		finalSegment(12, "心配する声が出ています。"),
		finalSegment(13, "当社の計画では昼間のみ2回測定する予定でしたが、それでは住民の懸念に十分対応できません。"),
		finalSegment(14, "そこで、昼間に1回、夜間に2回測定する案を採用したいと思います。"),
		finalSegment(15, "夜間の測定は、風が比較的弱い日、強い日に1回ずつ実施します。"),
		finalSegment(16, "騒音測定は昼間1回、夜間2回の合計3回実施することを決定事項とします。"),
		finalSegment(17, "ただし、強風日の測定条件については、どの風速を基準にするか決まっていません。"),
		finalSegment(18, "この点は気象データを確認してから判断するため、現時点では未解決の課題として残します。"),
		finalSegment(19, "続いて、住民説明資料についてです。"),
		finalSegment(20, "現在の資料には設備の位置と調査日程は記載されていますが、調査結果をどのように公開するかが書かれていません。"),
		finalSegment(21, "調査結果の概要を団体のウェブサイトで公開する方針にします。"),
		finalSegment(22, "公開資料には図や簡単な説明をつけることも決定します。"),
		finalSegment(23, "一方、説明会そのものの開催日は、地域の自治会と調整できていません。"),
		finalSegment(24, "開催日はまだ決定せず、候補日を受け取った後に確定します。"),
		finalSegment(25, "最後に、アジェンダにはありませんでしたが、現地担当者から新しい報告があります。"),
		finalSegment(26, "建設予定地の近くに小規模な湿地が見つかり、希少な植物が生育している可能性があるとのことです。"),
		finalSegment(27, "現時点では植物の種類が確認できていないため、既存の鳥類調査や騒音調査の中に無理に含めず、新しい調査課題として扱う必要があります。"),
		finalSegment(28, "植物の種類を確認するため、専門家による予備調査を次回検討します。"),
		finalSegment(29, "以上をまとめます。"),
		finalSegment(30, "渡り鳥の観測地点不足については、三地点で調査することで解決済みです。"),
		finalSegment(31, "決定事項は、渡り鳥を三地点で調査すること、騒音を昼間1回と夜間2回測定すること、そして調査結果を図付きでウェブ公開することです。"),
		finalSegment(32, "そして未解決の課題は強風日の風速基準と住民説明会の開催日です。"),
		finalSegment(33, "また、湿地の希少植物については、アジェンダ外から生まれた新しい論点として、次回以降も検討したいと思います。"),
	}
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥の調査計画", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定の実施方法", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料の作成", Order: 3, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "横断対応", Order: 4, Role: agendaRoleActionSummary},
	}}
	model := `{"summary":"fixture","currentTopic":"まとめ","resolvedIds":[],"resolutionUpdates":[{"itemId":"risk-bird-route","status":"resolved","evidenceSequenceNos":[7,8],"reason":"追加地点で対応可能と明示"}],"items":[
		{"id":"risk-bird-route","kind":"risk","severity":"high","title":"観測地点不足","body":"一地点では移動経路を確認できない","status":"open","evidenceSequenceNos":[5,7,8]},
		{"id":"fact-bird-sites","kind":"fact","severity":"medium","title":"北側・南側にも設置可能","body":"追加二地点を設置できる","status":"open","evidenceSequenceNos":[9]},
		{"id":"todo-bird-sites","kind":"todo","severity":"high","title":"三地点で調査","body":"海岸側・北側・南側の三地点で実施する","status":"open","evidenceSequenceNos":[9]},
		{"id":"todo-noise-count","kind":"todo","severity":"high","title":"昼一回・夜二回測定","body":"合計三回実施する","status":"open","evidenceSequenceNos":[16]},
		{"id":"question-wind-speed","kind":"question","severity":"medium","title":"強風日の風速基準は何か","body":"どの風速を基準にするか確認が必要","status":"open","evidenceSequenceNos":[17]},
		{"id":"open-wind-speed","kind":"open_issue","severity":"high","title":"強風日の基準風速が未確定","body":"測定条件として決める必要がある","status":"open","evidenceSequenceNos":[17,18]},
		{"id":"todo-weather-data","kind":"todo","severity":"high","title":"気象データを確認する","body":"判断材料となる気象データを確認する","status":"open","evidenceSequenceNos":[18]},
		{"id":"todo-web-publish","kind":"todo","severity":"high","title":"図付きでウェブ公開","body":"調査結果を図と簡単な説明付きで公開する","status":"open","evidenceSequenceNos":[21,22]},
		{"id":"todo-meeting-date","kind":"todo","severity":"medium","title":"説明会開催日を決める","body":"自治会の候補日受領後に確定する","status":"open","evidenceSequenceNos":[24]},
		{"id":"todo-plant-survey","kind":"todo","severity":"medium","title":"専門家の予備調査を検討","body":"湿地の植物を確認する","status":"open","evidenceSequenceNos":[28]}
	],"newTopics":[{"id":"topic-plant-habitat","label":"湿地・希少植物調査"}],"assignments":[
		{"nodeId":"risk-bird-route","parentTopicId":"agenda-1","confidence":0.9},{"nodeId":"fact-bird-sites","parentTopicId":"agenda-1","confidence":0.9},{"nodeId":"todo-bird-sites","parentTopicId":"agenda-1","confidence":0.9},
		{"nodeId":"todo-noise-count","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"question-wind-speed","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"open-wind-speed","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"todo-weather-data","parentTopicId":"agenda-2","confidence":0.9},
		{"nodeId":"todo-web-publish","parentTopicId":"agenda-3","confidence":0.9},{"nodeId":"todo-meeting-date","parentTopicId":"agenda-3","confidence":0.9},
		{"nodeId":"todo-plant-survey","parentTopicId":"topic-plant-habitat","confidence":0.9}
	]}`
	reconciled, audit, err := reconcileDecisionCandidates(model, nil, detectDecisionCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	if audit.AcceptedDecisions < 3 {
		t.Fatalf("decision audit=%+v", audit)
	}
	sequenceNos := make([]int64, 0, len(segments))
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{}, CurrentRound: map[int64]struct{}{}, TranscriptText: map[int64]string{}, CoveredThrough: int64(len(segments))}
	for _, segment := range segments {
		sequenceNos = append(sequenceNos, segment.SequenceNo)
		scope.Allowed[segment.SequenceNo] = struct{}{}
		scope.CurrentRound[segment.SequenceNo] = struct{}{}
		scope.TranscriptText[segment.SequenceNo] = segment.Text
	}
	mergeStats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(reconciled, nil, mc, 1, sequenceNos, scope, TreeClassificationConfig{PromotionMinItems: 1, PromotionMinRounds: 1}, mergeStats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	kinds := map[string]int{}
	resolvedCount := 0
	for _, item := range state.Items {
		kinds[item.Kind]++
		if item.Status == "resolved" {
			resolvedCount++
		}
	}
	if kinds["decision"] < 3 || kinds["question"] != 1 || kinds["open_issue"] != 1 || resolvedCount != 1 || len(state.Items) > 10 {
		t.Fatalf("kinds=%v itemCount=%d items=%+v resolutions=%+v", kinds, len(state.Items), state.Items, mergeStats.ResolutionDecisions)
	}
	noiseGroupID := stableGroupID("agenda-2", "夜間測定")
	reorganized, applied := applyTreeOperations(state.Tree, mc, []treeOperation{
		{Type: "create_group", ParentTopicID: "agenda-1", Label: "観測地点の不足", EvidenceItemIDs: []string{"risk-bird-route", "fact-bird-sites", "todo-bird-sites"}},
		{Type: "create_group", ParentTopicID: "agenda-2", Label: "夜間測定", EvidenceItemIDs: []string{"todo-noise-count", "question-wind-speed", "open-wind-speed", "todo-weather-data"}},
		{Type: "create_group", ParentID: noiseGroupID, Label: "強風日の条件", EvidenceItemIDs: []string{"question-wind-speed", "open-wind-speed", "todo-weather-data"}},
		{Type: "create_group", ParentTopicID: "agenda-3", Label: "公開方法と説明会", EvidenceItemIDs: []string{"todo-web-publish", "todo-meeting-date"}},
	}, TreeClassificationConfig{}, &liveAnalysisTreeMergeStats{})
	if applied != 4 {
		t.Fatalf("groups applied=%d", applied)
	}
	health := computeTreeHealth(reorganized)
	if health.GroupCount != 4 || health.NestedGroupCount != 1 || treeDepthOf(reorganized) != 4 || health.SingleChildGroupCount != 0 {
		t.Fatalf("health=%+v depth=%d", health, treeDepthOf(reorganized))
	}
	canonicalParents := make(map[string]string)
	for _, node := range reorganized.Nodes {
		canonicalParents[node.ID] = node.ParentID
	}
	if canonicalParents["open-wind-speed"] == "agenda-4" || canonicalParents["todo-meeting-date"] == "agenda-4" {
		t.Fatalf("action summary became canonical parent: %v", canonicalParents)
	}
	actionReferences := 0
	for _, item := range state.Items {
		if len(item.RelatedAgendaIDs) > 0 {
			actionReferences++
		}
	}
	t.Logf("replay metrics: decisions=%d resolved=%d todos=%d questions=%d openIssues=%d items=%d groups=%d maxDepth=%d maxTopicChildren=%d actionReferences=%d operationsApplied=%d", kinds["decision"], resolvedCount, kinds["todo"], kinds["question"], kinds["open_issue"], len(state.Items), health.GroupCount, treeDepthOf(reorganized), health.MaxTopicChildren, actionReferences, applied)
	encoded, err := json.Marshal(state)
	if err != nil || len(encoded) == 0 {
		t.Fatalf("marshal replay state: %v", err)
	}
}

func assertControlledTreeInvariants(t *testing.T, tree *liveAnalysisTree, maxDepth int) {
	t.Helper()
	if tree == nil {
		t.Fatal("tree is nil")
	}
	byID := make(map[string]liveAnalysisTreeNode)
	rootCount := 0
	for _, node := range tree.Nodes {
		if _, duplicate := byID[node.ID]; duplicate {
			t.Fatalf("duplicate node id %s", node.ID)
		}
		byID[node.ID] = node
		if node.ID == treeRootNodeID {
			rootCount++
		}
	}
	if rootCount != 1 {
		t.Fatalf("rootCount=%d", rootCount)
	}
	for _, node := range tree.Nodes {
		if node.ID == treeRootNodeID {
			continue
		}
		parent, ok := byID[node.ParentID]
		if !ok {
			t.Fatalf("node %s has missing parent %s", node.ID, node.ParentID)
		}
		switch node.Kind {
		case "topic":
			if parent.ID != treeRootNodeID {
				t.Fatalf("topic %s parent=%s", node.ID, parent.ID)
			}
		case "group":
			if (parent.Kind != "topic" && parent.Kind != "group") || parent.ID == treeRootNodeID {
				t.Fatalf("group %s parent kind/id=%s/%s", node.ID, parent.Kind, parent.ID)
			}
		default:
			if parent.Kind != "topic" && parent.Kind != "group" {
				t.Fatalf("detail %s parent kind=%s", node.ID, parent.Kind)
			}
		}
	}
	if depth := treeDepthOf(tree); depth > maxDepth {
		t.Fatalf("depth=%d > %d", depth, maxDepth)
	}
	if len(tree.Edges) != len(tree.Nodes)-1 {
		t.Fatalf("edges=%d nodes=%d", len(tree.Edges), len(tree.Nodes))
	}
	for _, edge := range tree.Edges {
		if byID[edge.Target].ParentID != edge.Source {
			t.Fatalf("edge %s->%s disagrees with parentId=%s", edge.Source, edge.Target, byID[edge.Target].ParentID)
		}
	}
}
