package application

import "testing"

func aliasLifecycleContext() *meetingContext {
	return &meetingContext{Agenda: []agendaItem{
		{
			ID: "agenda-1", Title: "障害状況と原因", Description: "通信断の範囲と設定不備の原因を確認する",
			SemanticHints: []string{"通信断", "設定不備"}, Order: 1, Role: agendaRolePrimary,
		},
		{
			ID: "agenda-2", Title: "復旧対応の確認", Description: "切り戻し、VLAN修正、サービス正常化を確認する",
			SemanticHints: []string{"旧スイッチ", "許可VLAN", "正常化"}, Order: 2, Role: agendaRolePrimary,
		},
	}}
}

func TestModelParentAliasTransfersOnMergeAndDoesNotCloneOnSplit(t *testing.T) {
	mc := aliasLifecycleContext()
	agendaTopicID := stableAgendaTopicID("agenda-2", 0)
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Origin: topicOriginSystem},
		{
			ID: agendaTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "復旧対応",
			Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-2"}, Materialized: true,
			ModelTopicIDs: []string{"model-agenda"},
		},
		{
			ID: "topic-recovery-dynamic", Kind: "topic", ParentID: treeRootNodeID, Label: "復旧対応",
			Origin: topicOriginDynamic, ModelTopicIDs: []string{"model-dynamic"},
		},
		{ID: "fact-a", Kind: "fact", ParentID: "topic-recovery-dynamic", Label: "切り戻し"},
		{ID: "fact-b", Kind: "fact", ParentID: agendaTopicID, Label: "VLAN修正"},
		{ID: "fact-c", Kind: "fact", ParentID: agendaTopicID, Label: "疎通確認"},
		{ID: "fact-d", Kind: "fact", ParentID: agendaTopicID, Label: "正常化"},
	}}
	merged, applied := applyTreeOperations(tree, mc, []treeOperation{{
		Type: "merge_topic", FromTopicID: "topic-recovery-dynamic", IntoTopicID: agendaTopicID,
	}}, TreeClassificationConfig{}, nil, 8)
	if applied != 1 {
		t.Fatalf("merge applied=%d", applied)
	}
	agendaTopic := treeNodeByID(merged, agendaTopicID)
	if agendaTopic == nil || !containsExactString(agendaTopic.ModelTopicIDs, "model-dynamic") ||
		treeNodeByID(merged, "topic-recovery-dynamic") != nil {
		t.Fatalf("merged alias lifecycle topic=%+v tree=%+v", agendaTopic, merged)
	}

	split, applied := applyTreeOperations(merged, mc, []treeOperation{{
		Type: "split_topic", FromTopicID: agendaTopicID, TopicID: "topic-recovery-validation",
		Label: "復旧後の検証", EvidenceItemIDs: []string{"fact-b", "fact-c"},
	}}, TreeClassificationConfig{}, nil, 9)
	if applied != 1 {
		t.Fatalf("split applied=%d", applied)
	}
	source := treeNodeByID(split, agendaTopicID)
	created := treeNodeByID(split, "topic-recovery-validation")
	if source == nil || created == nil || !containsExactString(source.ModelTopicIDs, "model-dynamic") {
		t.Fatalf("split source/created=%+v/%+v", source, created)
	}
	if len(created.ModelTopicIDs) != 0 {
		t.Fatalf("ambiguous source aliases were cloned into split topic: %+v", created.ModelTopicIDs)
	}
}

func TestModelParentAliasCandidateFoldDematerializeAndSessionScope(t *testing.T) {
	mc := aliasLifecycleContext()
	topicID := stableAgendaTopicID("agenda-2", 0)
	items := map[string]liveAnalysisItem{
		"fact-recovery": {
			ID: "fact-recovery", Kind: "fact", Title: "切り戻しで通信を正常化",
			ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-recovery",
		},
	}
	topics := map[string]liveAnalysisTreeNode{
		topicID: {
			ID: topicID, Kind: "topic", Label: "復旧対応", Origin: topicOriginAgenda,
			AgendaRefs: []string{"agenda-2"}, Materialized: true,
		},
	}
	details := map[string]liveAnalysisTreeNode{
		"fact-recovery": {ID: "fact-recovery", Kind: "fact", ParentID: treeUnclassifiedTopicID},
	}
	parents := map[string]string{"fact-recovery": treeUnclassifiedTopicID, topicID: treeRootNodeID}
	dynamicCount := 0
	remaining := promoteEmergingCandidates(promotionContext{
		candidates: []emergingTopicCandidate{{
			ID: "candidate-recovery", Label: "復旧対応", Description: "切り戻しと正常化",
			ModelTopicIDs: []string{"model-recovery"}, EvidenceItemIDs: []string{"fact-recovery"},
		}},
		parents: parents, details: details, topics: topics,
		labelIndex:        map[string]string{normalizeForMatch("復旧対応"): topicID},
		addTopic:          func(topic liveAnalysisTreeNode) { topics[topic.ID] = topic },
		dynamicTopicCount: &dynamicCount,
		itemAt:            func(id string) *liveAnalysisItem { item := items[id]; return &item },
		round:             3, cfg: TreeClassificationConfig{}.normalized(), mc: mc,
	})
	if len(remaining) != 0 || !containsExactString(topics[topicID].ModelTopicIDs, "model-recovery") {
		t.Fatalf("candidate fold aliases=%v remaining=%+v", topics[topicID].ModelTopicIDs, remaining)
	}

	sessionATree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic"},
		{
			ID: topicID, Kind: "topic", ParentID: treeRootNodeID, Origin: topicOriginAgenda,
			AgendaRefs: []string{"agenda-2"}, Materialized: true, ModelTopicIDs: []string{"model-recovery"},
		},
	}}
	sessionBTree := cloneLiveAnalysisPayload(liveAnalysisPayload{Tree: sessionATree}).Tree
	sessionBTree.Nodes[1].ModelTopicIDs = []string{"model-session-b"}
	pruneEmptyAgendaTopics(sessionATree, mc, 10, true, nil)
	if treeNodeByID(sessionATree, topicID) != nil {
		t.Fatalf("dematerialized topic remained: %+v", sessionATree.Nodes)
	}
	if got := treeNodeByID(sessionBTree, topicID); got == nil ||
		!containsExactString(got.ModelTopicIDs, "model-session-b") {
		t.Fatalf("session-local alias was changed by another payload lifecycle: %+v", sessionBTree.Nodes)
	}
}

func TestModelParentAliasCollisionReevaluatesEvidenceAndLaterCorrection(t *testing.T) {
	mc := aliasLifecycleContext()
	topic1ID := stableAgendaTopicID("agenda-1", 0)
	topic2ID := stableAgendaTopicID("agenda-2", 0)
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic"},
		{
			ID: topic1ID, Kind: "topic", ParentID: treeRootNodeID, Label: "一般確認",
			Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}, Materialized: true,
			ModelTopicIDs: []string{"model-shared"},
		},
		{
			ID: topic2ID, Kind: "topic", ParentID: treeRootNodeID, Label: "切り戻しとVLAN修正による正常化",
			Description: "旧スイッチへ切り戻し許可VLANを修正した", Origin: topicOriginAgenda,
			AgendaRefs: []string{"agenda-2"}, Materialized: true, ModelTopicIDs: []string{"model-shared"},
		},
		{ID: "fact-recovery", Kind: "fact", ParentID: topic2ID, Label: "サービス正常化"},
	}}
	items := []liveAnalysisItem{{
		ID: "fact-recovery", Kind: "fact", Title: "切り戻しとVLAN修正", Body: "各サービスの正常化を確認",
	}}
	reconcileAgendaModelTopicAliasConflicts(tree, mc, items)
	owners := 0
	for _, node := range tree.Nodes {
		if containsExactString(node.ModelTopicIDs, "model-shared") {
			owners++
			if node.ID != topic2ID {
				t.Fatalf("alias conflict selected stale topic: %+v", node)
			}
		}
	}
	if owners != 1 {
		t.Fatalf("alias owners=%d tree=%+v", owners, tree.Nodes)
	}

	texts := map[int64]string{
		1: "障害状況は通信断で、原因は新スイッチの設定不備でした。",
		2: "復旧対応として旧スイッチへ切り戻し、許可VLANを修正して各サービスの正常化を確認しました。",
	}
	round1 := `{"summary":"原因","currentTopic":"原因分析","items":[{"id":"fact-cause","kind":"fact","severity":"high","title":"新スイッチの設定不備が原因","body":"通信断の原因を確認した","status":"open","evidenceSequenceNos":[1]}],"newTopics":[{"id":"model-reused","label":"障害原因分析","description":"通信断と設定不備"}],"assignments":[{"nodeId":"fact-cause","parentTopicId":"model-reused","confidence":0.9}]}`
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
		round1, nil, mc, 1, []int64{1}, agendaReconciliationScope(texts, 1), TreeClassificationConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}
	first := previousLiveAnalysisState(raw)
	if topic := agendaTopicNodeByRef(first.Tree, "agenda-1"); topic == nil ||
		!containsExactString(topic.ModelTopicIDs, "model-reused") {
		t.Fatalf("round1 alias owner=%+v", topic)
	}
	round2 := `{"summary":"復旧","currentTopic":"復旧対応","items":[{"id":"fact-recovery-2","kind":"fact","severity":"high","title":"切り戻しとVLAN修正で正常化","body":"旧スイッチへ切り戻し許可VLANを修正して各サービスを正常化した","status":"open","evidenceSequenceNos":[2]}],"newTopics":[{"id":"model-reused","label":"復旧対応","description":"切り戻しとVLAN修正による正常化"}],"assignments":[{"nodeId":"fact-recovery-2","parentTopicId":"model-reused","confidence":0.92}]}`
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(
		round2, raw, mc, 2, []int64{2}, agendaReconciliationScope(texts, 2), TreeClassificationConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}
	second := previousLiveAnalysisState(raw)
	agenda1 := agendaTopicNodeByRef(second.Tree, "agenda-1")
	agenda2 := agendaTopicNodeByRef(second.Tree, "agenda-2")
	if agenda2 == nil || !containsExactString(agenda2.ModelTopicIDs, "model-reused") ||
		(agenda1 != nil && containsExactString(agenda1.ModelTopicIDs, "model-reused")) ||
		itemTopicID(second.Tree, "fact-recovery-2") != agenda2.ID {
		t.Fatalf("later correction was blocked: agenda1=%+v agenda2=%+v tree=%+v", agenda1, agenda2, second.Tree)
	}
}
