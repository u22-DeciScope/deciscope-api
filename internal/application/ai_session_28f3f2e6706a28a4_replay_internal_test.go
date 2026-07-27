package application

import "testing"

// TestSession28f3GroupParentsSurviveActiveSpanReplay reproduces the persisted
// v13 shape from session_28f3f2e6706a28a4. In the incident, the next ordinary
// live round generated active_span assignments for all eight grouped items.
// Those topic-level assignments stripped the group parents; the existing-group
// branch then observed the empty group but did not restore its children, and
// assembleTree flattened both groups. The resulting v14 hash returned exactly
// to v12. A topic-level active_span update must now preserve the concrete group.
func TestSession28f3GroupParentsSurviveActiveSpanReplay(t *testing.T) {
	const (
		noiseTopic     = "topic-agenda-64b761a79cc0"
		residentTopic  = "topic-agenda-7dd3ab9e5ea9"
		noiseGroup     = "group-dd10e2044647"
		residentGroup  = "group-e0d0e2c2c03e"
		persistedV13ID = "4d20f581d3ad4232"
	)
	_ = persistedV13ID // documents the DB fixture identity beside its shape.

	noiseChildren := []string{
		"item-issue-discussion-29c86541aab4",
		"item-decision-8f543482e3d9",
		"item-issue-discussion-a742c0ebe0fe",
		"issue-question-auto-855f8b7e8690",
	}
	residentChildren := []string{
		"item-issue-discussion-456fe82e3d68",
		"issue-question-auto-0f39ebf87e87",
		"decision-auto-669c840e2ece",
		"decision-auto-b3c34089ab49",
	}
	nodes := []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "沿岸部風力発電計画に関する環境アセスメン", Origin: topicOriginSystem},
		{ID: "topic-agenda-a5f8fcd0c7a2", Kind: "topic", ParentID: treeRootNodeID, Label: "渡り鳥調査計画の確認不足点", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-1"}, Materialized: true},
		{ID: noiseTopic, Kind: "topic", ParentID: treeRootNodeID, Label: "騒音測定の方法見直し", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-2"}, Materialized: true},
		{ID: residentTopic, Kind: "topic", ParentID: treeRootNodeID, Label: "住民説明資料の公開方針の検討", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-3"}, Materialized: true},
		{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginSystem},
		{ID: noiseGroup, Kind: "group", ParentID: noiseTopic, Label: "騒音測定の方法見直し", Origin: assignmentSourceRule, CreatedAtVersion: 13},
		{ID: residentGroup, Kind: "group", ParentID: residentTopic, Label: "住民説明資料の公開方針の検討", Origin: assignmentSourceRule, CreatedAtVersion: 13},
	}
	items := []liveAnalysisItem{
		{ID: "item-issue-discussion-240df133b0ea", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "渡り鳥調査計画の観測地点追加の決定", Status: "open", ClassificationStatus: classificationAssigned},
		{ID: "decision-auto-88e2365fd6a8", Kind: "decision", Title: "渡り鳥調査：海岸・北側・南側の計3地点で", Status: "open", ClassificationStatus: classificationAssigned},
		{ID: noiseChildren[0], Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "騒音測定の方法見直し", Status: "open", ClassificationStatus: classificationAssigned},
		{ID: noiseChildren[1], Kind: "decision", Title: "夜間測定の頻度・条件の確定", Status: "open", ClassificationStatus: classificationAssigned},
		{ID: noiseChildren[2], Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "強風日での測定条件の基準風速未定", Status: "open", ClassificationStatus: classificationAssigned},
		{ID: noiseChildren[3], Kind: "issue", Subtype: issueSubtypeQuestion, Title: "強風日の測定条件についてどの風速を基準にするか", Status: "open", ClassificationStatus: classificationAssigned},
		{ID: residentChildren[0], Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "住民説明資料の公開方針の検討", Status: "open", ClassificationStatus: classificationAssigned},
		{ID: residentChildren[1], Kind: "issue", Subtype: issueSubtypeQuestion, Title: "調査結果をどのように公開するか", Status: "open", ClassificationStatus: classificationAssigned},
		{ID: residentChildren[2], Kind: "decision", Title: "調査結果の概要を団体ウェブサイトで公開する", Status: "open", ClassificationStatus: classificationAssigned},
		{ID: residentChildren[3], Kind: "decision", Title: "公開資料に図と簡単な説明をつける", Status: "open", ClassificationStatus: classificationAssigned},
		{ID: "item-issue-discussion-98c520e31c06", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "現地説明会の開催ビラの自治会調整未完了", Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-9f7b2ff07074"},
		{ID: "item-issue-discussion-045965fbcc11", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "現地担当者の新規報告の取り扱い", Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-9f7b2ff07074"},
		{ID: "item-issue-investigation-3f2b67d5a15e", Kind: "issue", Subtype: issueSubtypeInvestigation, Title: "新たな湿地・希少植物の可能性", Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-52bec832205a"},
		{ID: "item-issue-discussion-7a80aaca6c47", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "新規調査課題の扱い方針", Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-52bec832205a"},
		{ID: "item-issue-investigation-f23edf70268c", Kind: "issue", Subtype: issueSubtypeInvestigation, Title: "植物の種類確認の予備調査検討", Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-52bec832205a"},
		{ID: "issue-question-auto-9974d430b117", Kind: "issue", Subtype: issueSubtypeQuestion, Title: "植物の種類を確認するため予備調査を実施するか", Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-52bec832205a"},
	}
	parent := map[string]string{
		items[0].ID: "topic-agenda-a5f8fcd0c7a2",
		items[1].ID: "topic-agenda-a5f8fcd0c7a2",
	}
	for _, id := range noiseChildren {
		parent[id] = noiseGroup
	}
	for _, id := range residentChildren {
		parent[id] = residentGroup
	}
	for _, item := range items[10:] {
		parent[item.ID] = treeUnclassifiedTopicID
	}
	for _, item := range items {
		nodes = append(nodes, liveAnalysisTreeNode{ID: item.ID, Kind: liveAnalysisTreeNodeKindForItem(item.Kind), Subtype: item.Subtype, ParentID: parent[item.ID], Label: item.Title, Status: "open"})
	}
	previous := &liveAnalysisTree{Nodes: nodes}
	previous.Edges = edgesFromTreeParents(nodes)

	assignments := make([]treeAssignment, 0, 8)
	for _, id := range noiseChildren {
		assignments = append(assignments, treeAssignment{NodeID: id, ParentTopicID: noiseTopic, Confidence: 0.95, ServerSource: assignmentSourceActiveSpan})
	}
	for _, id := range residentChildren {
		assignments = append(assignments, treeAssignment{NodeID: id, ParentTopicID: residentTopic, Confidence: 0.95, ServerSource: assignmentSourceActiveSpan})
	}
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥調査計画の確認不足点", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定の方法見直し", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料の公開方針の検討", Order: 3, Role: agendaRolePrimary},
	}}

	next, _, _ := rebuildDiscussionTree(previous, mc, items, nil, assignments, nil, nil, 14, TreeClassificationConfig{}, &liveAnalysisTreeMergeStats{})
	if len(next.Nodes) != len(previous.Nodes) {
		t.Fatalf("v14 nodeCount=%d, want v13 LKG shape %d", len(next.Nodes), len(previous.Nodes))
	}
	if computeTreeHealth(next).GroupCount != 2 {
		t.Fatalf("v14 groupCount=%d, want 2", computeTreeHealth(next).GroupCount)
	}
	for _, id := range noiseChildren {
		if got := parentOf(next, id); got != noiseGroup {
			t.Fatalf("%s parent=%s, want %s", id, got, noiseGroup)
		}
	}
	for _, id := range residentChildren {
		if got := parentOf(next, id); got != residentGroup {
			t.Fatalf("%s parent=%s, want %s", id, got, residentGroup)
		}
	}

	// A further round must remain stable rather than alternate again.
	nextAgain, _, _ := rebuildDiscussionTree(next, mc, items, nil, assignments, nil, nil, 15, TreeClassificationConfig{}, &liveAnalysisTreeMergeStats{})
	if computeTreeHealth(nextAgain).GroupCount != 2 || len(nextAgain.Nodes) != len(next.Nodes) {
		t.Fatalf("v15 shape changed: nodes=%d groups=%d", len(nextAgain.Nodes), computeTreeHealth(nextAgain).GroupCount)
	}
}

func edgesFromTreeParents(nodes []liveAnalysisTreeNode) []liveAnalysisTreeEdge {
	edges := make([]liveAnalysisTreeEdge, 0, len(nodes)-1)
	for _, node := range nodes {
		if node.ParentID != "" {
			edges = append(edges, liveAnalysisTreeEdge{Source: node.ParentID, Target: node.ID})
		}
	}
	return edges
}
