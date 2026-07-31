package application

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNetworkAgendaAndVPNNoAgendaReplay(t *testing.T) {
	mc := &meetingContext{Title: "ネットワーク更改会議", Agenda: []agendaItem{
		{ID: "agenda-1", Title: "ネットワーク構成", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "セキュリティ方針", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "移行計画", Order: 3, Role: agendaRolePrimary},
	}}
	scope := liveEvidenceScope{
		Allowed: map[int64]struct{}{1: {}, 2: {}, 3: {}, 4: {}, 5: {}, 6: {}},
		TranscriptText: map[int64]string{
			1: "ネットワーク構成について議論します。",
			2: "拠点間回線は冗長化が必要です。",
			3: "アジェンダ外ですが、在宅勤務用VPNの同時接続数も確認します。",
			4: "VPNは同時接続数が不足するリスクがあります。",
			5: "VPNの増強案を次回までに比較します。",
			6: "VPNライセンス数と増強費用を確認します。",
		},
		CoveredThrough: 6,
	}
	cfg := TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}

	scope.CurrentRound = map[int64]struct{}{1: {}, 2: {}}
	round1 := `{"summary":"構成","currentTopic":"ネットワーク構成","items":[{"clientKey":"network-redundancy","kind":"issue","subtype":"discussion","severity":"high","title":"拠点間回線の冗長化","body":"単一回線では障害時に通信を継続できない","status":"open","evidenceSequenceNos":[2]}],"newTopics":[],"assignments":[{"nodeId":"network-redundancy","parentTopicId":"agenda-1","confidence":0.94}]}`
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(round1, nil, mc, 1, []int64{1, 2}, scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	state1 := previousLiveAnalysisState(raw)
	agenda1Topic := agendaTopicNodeByRef(state1.Tree, "agenda-1")
	if agenda1Topic == nil || agenda1Topic.ID == "agenda-1" || !strings.HasPrefix(agenda1Topic.ID, "topic-") || agendaTopicNodeByRef(state1.Tree, "agenda-2") != nil || agendaTopicNodeByRef(state1.Tree, "agenda-3") != nil {
		t.Fatalf("round1 tree=%+v", state1.Tree.Nodes)
	}
	if len(state1.AgendaAnchors) != 3 || state1.AgendaAnchors[0].Status != agendaStatusDiscussed || state1.AgendaAnchors[1].Status != agendaStatusPlanned || !containsExactString(state1.AgendaAnchors[0].MaterializedTopicIDs, agenda1Topic.ID) {
		t.Fatalf("round1 anchors=%+v", state1.AgendaAnchors)
	}

	scope.CurrentRound = map[int64]struct{}{3: {}, 4: {}, 5: {}}
	round2 := `{"summary":"VPN","currentTopic":"在宅勤務VPN","items":[{"clientKey":"vpn-capacity-risk","kind":"risk","severity":"high","title":"VPN同時接続数の不足","body":"在宅勤務者が集中すると接続できない","status":"open","evidenceSequenceNos":[4]},{"clientKey":"vpn-plan-todo","kind":"todo","severity":"medium","title":"VPN増強案を比較する","body":"次回までにライセンス案を比較する","status":"open","evidenceSequenceNos":[5]}],"newTopics":[{"id":"topic-vpn","label":"在宅勤務VPN","description":"同時接続数と増強案"}],"assignments":[{"nodeId":"vpn-capacity-risk","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"vpn-plan-todo","parentTopicId":"agenda-2","confidence":0.9}]}`
	stats2 := &liveAnalysisTreeMergeStats{}
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(round2, raw, mc, 2, []int64{3, 4, 5}, scope, cfg, stats2)
	if err != nil {
		t.Fatal(err)
	}
	state2 := previousLiveAnalysisState(raw)
	candidateID, _ := canonicalCandidateID("在宅勤務VPN", "同時接続数と増強案")
	dynamicID := stableDynamicTopicID(candidateID)
	if agendaTopicNodeByRef(state2.Tree, "agenda-2") != nil || len(state2.EmergingTopics) != 0 ||
		treeNodeByID(state2.Tree, dynamicID) == nil ||
		stats2.FixedAgendaAssignmentRejectedByNoAgendaSpan < 2 ||
		stats2.CandidatePromotedSingleBatch != 1 {
		t.Fatalf("round2 tree=%+v candidates=%+v stats=%+v", state2.Tree.Nodes, state2.EmergingTopics, stats2)
	}

	scope.CurrentRound = map[int64]struct{}{6: {}}
	round3 := `{"summary":"VPN増強","currentTopic":"在宅勤務VPN","items":[{"clientKey":"vpn-license-check","kind":"issue","subtype":"confirmation","severity":"medium","title":"VPNライセンス数と費用の確認","body":"増強に必要なライセンス数と費用を確認する","status":"open","evidenceSequenceNos":[6]}],"newTopics":[{"id":"topic-vpn","label":"在宅勤務VPN","description":"同時接続数と増強案"}],"assignments":[{"nodeId":"vpn-license-check","parentTopicId":"agenda-2","confidence":0.9}]}`
	stats3 := &liveAnalysisTreeMergeStats{}
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(round3, raw, mc, 3, []int64{6}, scope, cfg, stats3)
	if err != nil {
		t.Fatal(err)
	}
	state3 := previousLiveAnalysisState(raw)
	if replayed := agendaTopicNodeByRef(state3.Tree, "agenda-1"); replayed == nil || replayed.ID != agenda1Topic.ID {
		t.Fatalf("agenda topic ID changed across rounds: first=%+v replayed=%+v", agenda1Topic, replayed)
	}
	if topic := treeNodeByID(state3.Tree, dynamicID); topic == nil || topic.Origin != topicOriginDynamic ||
		agendaTopicNodeByRef(state3.Tree, "agenda-2") != nil ||
		agendaTopicNodeByRef(state3.Tree, "agenda-3") != nil ||
		stats3.DynamicTopicsPromoted != 0 ||
		itemTopicID(state3.Tree, findItemByTitlePart(state3.Items, "VPNライセンス数").ID) != dynamicID {
		t.Fatalf("round3 dynamic=%q nodes=%+v stats=%+v", dynamicID, state3.Tree.Nodes, stats3)
	}
	if diagnostics := validateTreeIntegrity(state3.Tree, state3.Items, mc); !diagnostics.Valid || diagnostics.AgendaRecordCount != 3 || diagnostics.MaterializedAgendaCount != 1 || diagnostics.PlannedAgendaCount != 2 {
		t.Fatalf("integrity=%+v", diagnostics)
	}

	finalRaw, err := finalizeAgendaLifecyclePayload(raw, mc, 3)
	if err != nil {
		t.Fatal(err)
	}
	finalState := previousLiveAnalysisState(finalRaw)
	counts := summarizeAgendaAnchorStatuses(finalState.AgendaAnchors)
	if counts[agendaStatusDiscussed] != 1 || counts[agendaStatusNotDiscussed] != 2 || treeNodeByID(finalState.Tree, dynamicID) == nil {
		t.Fatalf("final anchors=%+v tree=%+v", finalState.AgendaAnchors, finalState.Tree.Nodes)
	}
	t.Logf("network-vpn replay agendaRecordCount=3 materializedAgendaCount=1 discussedAgendaCount=1 notDiscussedAgendaCount=2 dynamicVPNTopics=1 nodes=%d edges=%d", len(finalState.Tree.Nodes), len(finalState.Tree.Edges))
}

func TestLegacyAgendaTopicIdentityNormalizationIsStableAndIdempotent(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "ネットワーク構成", Order: 1, Role: agendaRolePrimary}}}
	legacy := liveAnalysisPayload{
		Items:         []liveAnalysisItem{{ID: "issue-network", Kind: "issue", Title: "回線冗長化", CandidateTopicID: "agenda-1", EvidenceSequenceNos: []int64{7}}},
		AgendaAnchors: []agendaAnchor{{AgendaID: "agenda-1", OriginalTitle: "ネットワーク構成", Order: 1, Role: agendaRolePrimary, Status: agendaStatusDiscussed, MaterializedTopicIDs: []string{"agenda-1"}}},
		TreeChanges:   &liveAnalysisTreeChanges{TreeVersion: 7, UpdatedNodeIDs: []string{"agenda-1"}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem},
			{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク構成", Origin: topicOriginAgenda},
			{ID: "issue-network", Kind: "issue", ParentID: "agenda-1", Label: "回線冗長化"},
		}, Edges: []liveAnalysisTreeEdge{{Source: treeRootNodeID, Target: "agenda-1"}, {Source: "agenda-1", Target: "issue-network"}}, Relations: []liveAnalysisTreeRelation{{Source: "agenda-1", Target: "issue-network", Kind: "supports"}}},
	}
	rawIntegrity := validateTreeIntegrity(legacy.Tree, legacy.Items, mc, legacy.AgendaAnchors)
	if rawIntegrity.Valid || rawIntegrity.AgendaNodeIDNamespaceValid || len(rawIntegrity.AgendaTopicIDCollisions) != 1 {
		t.Fatalf("legacy collision not detected: %+v", rawIntegrity)
	}
	remap := normalizeLegacyAgendaTopicIDs(&legacy, mc, nil)
	wantTopicID := stableAgendaTopicID("agenda-1", 0)
	if remap["agenda-1"] != wantTopicID || wantTopicID == "agenda-1" || !strings.HasPrefix(wantTopicID, "topic-") {
		t.Fatalf("remap=%v wantTopicID=%q", remap, wantTopicID)
	}
	if second := normalizeLegacyAgendaTopicIDs(&legacy, mc, nil); len(second) != 0 {
		t.Fatalf("normalization not idempotent: %v", second)
	}
	topic := treeNodeByID(legacy.Tree, wantTopicID)
	itemNode := treeNodeByID(legacy.Tree, "issue-network")
	if topic == nil || !containsExactString(topic.AgendaRefs, "agenda-1") || itemNode == nil || itemNode.ParentID != wantTopicID || legacy.Items[0].CandidateTopicID != wantTopicID {
		t.Fatalf("normalized state=%+v", legacy)
	}
	if len(legacy.Tree.Edges) != 2 || legacy.Tree.Edges[1].Source != wantTopicID || legacy.Tree.Relations[0].Source != wantTopicID || legacy.TreeChanges.UpdatedNodeIDs[0] != wantTopicID || legacy.AgendaAnchors[0].MaterializedTopicIDs[0] != wantTopicID {
		t.Fatalf("references were not remapped: %+v", legacy)
	}
	legacy.AgendaAnchors = reconcileAgendaAnchors(legacy.AgendaAnchors, mc, legacy.Tree, legacy.Items, 7, false)
	diagnostics := validateTreeIntegrity(legacy.Tree, legacy.Items, mc, legacy.AgendaAnchors)
	if !diagnostics.Valid || !diagnostics.AgendaNodeIDNamespaceValid || len(diagnostics.AgendaTopicIDCollisions) != 0 || len(diagnostics.UnknownAgendaRefs) != 0 || len(diagnostics.OrphanMaterializedTopicIDs) != 0 {
		t.Fatalf("normalized integrity=%+v", diagnostics)
	}
	t.Logf("agendaAnchorId=%s materializedTopicId=%s agendaAnchorIdEqualsTopicId=%t agendaTopicIdCollisions=%d unknownAgendaRefs=%d orphanMaterializedTopicIds=%d treeIntegrityValid=%t",
		legacy.AgendaAnchors[0].AgendaID, wantTopicID, legacy.AgendaAnchors[0].AgendaID == wantTopicID,
		len(diagnostics.AgendaTopicIDCollisions), len(diagnostics.UnknownAgendaRefs), len(diagnostics.OrphanMaterializedTopicIDs), diagnostics.Valid)
}

func TestAgendaTopicStableIDEscapesOccupiedNodeIDAndThenReusesIt(t *testing.T) {
	records := agendaRecordMap(&meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "ネットワーク構成", Role: agendaRolePrimary}}})
	occupiedID := stableAgendaTopicID("agenda-1", 0)
	topics := map[string]liveAnalysisTreeNode{
		occupiedID: {ID: occupiedID, Kind: "topic", Label: "モデル由来の別topic", Origin: topicOriginDynamic},
	}

	topicID, reused := availableAgendaTopicID("agenda-1", topics, records)
	if reused || topicID == occupiedID || topicID != stableAgendaTopicID("agenda-1", 1) || topicID == "agenda-1" {
		t.Fatalf("collision escape topicID=%q reused=%t occupiedID=%q", topicID, reused, occupiedID)
	}
	topics[topicID] = liveAnalysisTreeNode{ID: topicID, Kind: "topic", AgendaRefs: []string{"agenda-1"}, Materialized: true}
	replayedID, replayed := availableAgendaTopicID("agenda-1", topics, records)
	if !replayed || replayedID != topicID {
		t.Fatalf("materialized topic was not reused: first=%q replayed=%q reused=%t", topicID, replayedID, replayed)
	}
}

func TestAgendaTopicMergeDematerializeAndIntegrityFindings(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "VPN増強", Order: 1, Role: agendaRolePrimary}}}
	agendaTopicID := stableAgendaTopicID("agenda-1", 0)
	topics := map[string]liveAnalysisTreeNode{
		agendaTopicID: {ID: agendaTopicID, Kind: "topic", Label: "VPN増強", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}, Materialized: true},
		"topic-vpn":   {ID: "topic-vpn", Kind: "topic", Label: "VPN増強", Origin: topicOriginDynamic},
	}
	parents := map[string]string{agendaTopicID: treeRootNodeID, "topic-vpn": treeRootNodeID, "risk-vpn": "topic-vpn"}
	stats := &liveAnalysisTreeMergeStats{}
	order := mergeEquivalentAgendaDynamicTopics(topics, []string{agendaTopicID, "topic-vpn"}, parents, 4, stats)
	if len(order) != 1 || topics["topic-vpn"].ID != "" || parents["risk-vpn"] != agendaTopicID || topics[agendaTopicID].Origin != topicOriginMixed || stats.AgendaTopicsMerged != 1 {
		t.Fatalf("topics=%+v parents=%+v order=%v stats=%+v", topics, parents, order, stats)
	}

	empty := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem},
		{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN増強", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}, Materialized: true, CreatedAtVersion: 4},
	}, Edges: []liveAnalysisTreeEdge{{Source: treeRootNodeID, Target: "agenda-1"}}}
	precheck := deterministicTreeAuditPrecheck(liveAnalysisPayload{Tree: empty, TreeVersion: 4}, mc, nil, TreeAuditConfig{})
	for _, want := range []TreeAuditFindingType{TreeAuditPlannedAgendaWithoutEvidence, TreeAuditEmptyAgendaTopic, TreeAuditAgendaTopicShouldDematerialize} {
		found := false
		for _, finding := range precheck {
			found = found || finding.Type == want
		}
		if !found {
			t.Fatalf("missing finding %s: %+v", want, precheck)
		}
	}
	staleDiscussed := deterministicTreeAuditPrecheck(liveAnalysisPayload{
		Tree:          &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem}}},
		TreeVersion:   5,
		AgendaAnchors: []agendaAnchor{{AgendaID: "agenda-1", Status: agendaStatusDiscussed}},
	}, mc, nil, TreeAuditConfig{})
	foundMissingTopic := false
	for _, finding := range staleDiscussed {
		foundMissingTopic = foundMissingTopic || finding.Type == TreeAuditDiscussedAgendaMissingTopic
	}
	if !foundMissingTopic {
		t.Fatalf("missing discussed agenda finding: %+v", staleDiscussed)
	}
	missingRecord := validateTreeIntegrity(staleDiscussedTree(), nil, mc, []agendaAnchor{})
	if missingRecord.Valid || missingRecord.AgendaRecordIntegrityValid || missingRecord.AgendaRecordsPreserved != 0 || len(missingRecord.MissingAgendaRecordIDs) != 1 {
		t.Fatalf("missing agenda record integrity=%+v", missingRecord)
	}
	pruneEmptyAgendaTopics(empty, mc, 4, true, stats)
	if treeNodeByID(empty, "agenda-1") != nil || stats.AgendaTopicsDematerialized != 1 {
		t.Fatalf("empty tree=%+v stats=%+v", empty, stats)
	}
}

func staleDiscussedTree() *liveAnalysisTree {
	return &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem}}}
}

func TestMixedCandidateRepairMaterializesAgendaAndHonorsNoAgenda(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "ネットワーク構成", Order: 1, Role: agendaRolePrimary}}}
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{
			{ID: "issue-network-external", Kind: "issue", Title: "ネットワーク回線の帯域不足", Body: "アジェンダ外の検討として扱う", CandidateTopicID: "candidate-mixed", ClassificationStatus: classificationTentative, AssignmentSource: assignmentSourceNoAgendaSpan},
			{ID: "risk-network", Kind: "risk", Title: "ネットワーク経路の単一障害点", Body: "冗長化されていない経路がある", CandidateTopicID: "candidate-mixed", ClassificationStatus: classificationTentative},
			{ID: "todo-lunch", Kind: "todo", Title: "昼食会場を予約する", Body: "参加人数を確認して予約する", CandidateTopicID: "candidate-mixed", ClassificationStatus: classificationTentative},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem},
			{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginSystem},
			{ID: "issue-network-external", Kind: "issue", ParentID: treeUnclassifiedTopicID, Label: "ネットワーク回線の帯域不足"},
			{ID: "risk-network", Kind: "risk", ParentID: treeUnclassifiedTopicID, Label: "ネットワーク経路の単一障害点"},
			{ID: "todo-lunch", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "昼食会場を予約する"},
		}},
		EmergingTopics: []emergingTopicCandidate{{ID: "candidate-mixed", Label: "雑談", EvidenceItemIDs: []string{"issue-network-external", "risk-network", "todo-lunch"}, RoundCount: 2}},
	}
	stats := &liveAnalysisTreeMergeStats{}
	repairMixedEmergingCandidates(&state, mc, 6, stats)
	agendaTopic := agendaTopicNodeByRef(state.Tree, "agenda-1")
	if agendaTopic == nil || !agendaTopic.Materialized || len(agendaTopic.AgendaRefs) != 1 || agendaTopic.AgendaRefs[0] != "agenda-1" || stats.AgendaTopicsMaterialized != 1 {
		t.Fatalf("agenda topic=%+v stats=%+v", agendaTopic, stats)
	}
	if parent := treeNodeByID(state.Tree, "risk-network").ParentID; parent != agendaTopic.ID {
		t.Fatalf("grounded agenda item parent=%q", parent)
	}
	if parent := treeNodeByID(state.Tree, "issue-network-external").ParentID; parent == agendaTopic.ID {
		t.Fatalf("explicit no-agenda item moved into agenda topic: parent=%q", parent)
	}
	if state.Items[0].AssignmentSource != assignmentSourceNoAgendaSpan || state.Items[0].CandidateTopicID == "" {
		t.Fatalf("no-agenda item=%+v", state.Items[0])
	}
}

func TestIncidentRecoveryAgendaReplay(t *testing.T) {
	mc := &meetingContext{Title: "ネットワーク障害対応", Agenda: []agendaItem{
		{ID: "agenda-1", Title: "障害の概要", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "現状の調査と復旧対応", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "原因と再発防止", Order: 3, Role: agendaRolePrimary},
	}}
	scope := liveEvidenceScope{
		Allowed: map[int64]struct{}{1: {}, 2: {}, 3: {}, 4: {}, 5: {}, 6: {}, 7: {}, 8: {}, 9: {}, 10: {}, 11: {}},
		TranscriptText: map[int64]string{
			1: "まず、障害の概要について確認します。", 2: "社内ネットワークの一部が利用できません。",
			3: "続いて、現状の調査と復旧対応について議論します。", 4: "ルーターとファイアウォールを確認しました。",
			5: "3階アクセススイッチを確認しました。", 6: "旧スイッチへ切り戻し、VLAN設定を修正しました。", 7: "復旧後に疎通確認を行います。",
			8: "ここでアジェンダにはなかった別の問題があります。", 9: "VPN証明書の期限切れが見つかりました。",
			10: "VPN証明書の更新手順を確認します。", 11: "VPN証明書の担当者と期限を決めます。",
		},
		CoveredThrough: 11,
	}
	cfg := TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}

	scope.CurrentRound = map[int64]struct{}{1: {}, 2: {}}
	round1 := `{"summary":"障害概要","currentTopic":"障害の概要","items":[{"clientKey":"outage-summary","kind":"issue","subtype":"confirmation","severity":"high","title":"社内ネットワーク障害の発生範囲","body":"社内ネットワークの一部が利用できない","status":"open","evidenceSequenceNos":[2]}],"newTopics":[],"assignments":[{"nodeId":"outage-summary","parentTopicId":"agenda-1","confidence":0.95}]}`
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(round1, nil, mc, 1, []int64{1, 2}, scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	state1 := previousLiveAnalysisState(raw)
	if agendaTopicNodeByRef(state1.Tree, "agenda-1") == nil || agendaTopicNodeByRef(state1.Tree, "agenda-2") != nil || agendaTopicNodeByRef(state1.Tree, "agenda-3") != nil {
		t.Fatalf("round1 topics=%+v", state1.Tree.Nodes)
	}

	scope.CurrentRound = map[int64]struct{}{3: {}, 4: {}, 5: {}, 6: {}, 7: {}}
	round2 := `{"summary":"調査と復旧","currentTopic":"現状の調査と復旧対応","items":[{"clientKey":"router-firewall","kind":"fact","severity":"medium","title":"ルーターとファイアウォールの確認結果","body":"ルーターとファイアウォールの状態を確認した","status":"open","evidenceSequenceNos":[4]},{"clientKey":"floor-switch","kind":"issue","subtype":"confirmation","severity":"high","title":"3階アクセススイッチの状態確認","body":"3階アクセススイッチの状態を確認した","status":"open","evidenceSequenceNos":[5]},{"clientKey":"rollback","kind":"decision","severity":"high","title":"旧スイッチへの切り戻し","body":"復旧のため旧スイッチへ切り戻す","status":"open","evidenceSequenceNos":[6]},{"clientKey":"vlan-fix","kind":"decision","severity":"high","title":"VLAN設定の修正","body":"誤っていたVLAN設定を修正する","status":"open","evidenceSequenceNos":[6]},{"clientKey":"connectivity-check","kind":"todo","severity":"medium","title":"復旧後の疎通確認","body":"切り戻しと設定修正後に疎通を確認する","status":"open","evidenceSequenceNos":[7]}],"newTopics":[{"id":"topic-recovery","label":"調査と復旧対応","description":"機器確認と切り戻し"}],"assignments":[{"nodeId":"router-firewall","parentTopicId":"agenda-2","confidence":0.95},{"nodeId":"floor-switch","parentTopicId":"agenda-2","confidence":0.95},{"nodeId":"rollback","parentTopicId":"agenda-2","confidence":0.95},{"nodeId":"vlan-fix","parentTopicId":"agenda-2","confidence":0.95},{"nodeId":"connectivity-check","parentTopicId":"agenda-2","confidence":0.95}]}`
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(round2, raw, mc, 2, []int64{3, 4, 5, 6, 7}, scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	state2 := previousLiveAnalysisState(raw)
	agenda2Topic := agendaTopicNodeByRef(state2.Tree, "agenda-2")
	if agenda2Topic == nil {
		t.Fatalf("agenda-2 topic missing: %+v", state2.Tree.Nodes)
	}
	for _, title := range []string{"ルーターとファイアウォール", "3階アクセススイッチ", "旧スイッチへの切り戻し", "VLAN設定の修正", "復旧後の疎通確認"} {
		item := findItemByTitlePart(state2.Items, title)
		if item == nil || itemTopicID(state2.Tree, item.ID) != agenda2Topic.ID {
			t.Fatalf("recovery item %q=%+v topic=%q", title, item, func() string {
				if item == nil {
					return ""
				}
				return itemTopicID(state2.Tree, item.ID)
			}())
		}
	}

	scope.CurrentRound = map[int64]struct{}{8: {}, 9: {}, 10: {}}
	round3 := `{"summary":"VPN証明書","currentTopic":"VPN証明書問題","items":[{"clientKey":"vpn-cert-risk","kind":"risk","severity":"high","title":"VPN証明書の期限切れ","body":"期限切れでVPN接続に失敗する可能性がある","status":"open","evidenceSequenceNos":[9]},{"clientKey":"vpn-cert-procedure","kind":"issue","subtype":"investigation","severity":"medium","title":"VPN証明書の更新手順確認","body":"更新手順と影響範囲を確認する","status":"open","evidenceSequenceNos":[10]}],"newTopics":[{"id":"topic-vpn-certificate","label":"VPN証明書問題","description":"期限切れと更新対応"}],"assignments":[{"nodeId":"vpn-cert-risk","parentTopicId":"agenda-3","confidence":0.9},{"nodeId":"vpn-cert-procedure","parentTopicId":"agenda-3","confidence":0.9}]}`
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(round3, raw, mc, 3, []int64{8, 9, 10}, scope, cfg)
	if err != nil {
		t.Fatal(err)
	}

	scope.CurrentRound = map[int64]struct{}{11: {}}
	round4 := `{"summary":"VPN対応","currentTopic":"VPN証明書問題","items":[{"clientKey":"vpn-cert-owner","kind":"todo","severity":"medium","title":"VPN証明書更新の担当者と期限を決める","body":"更新担当者と実施期限を確定する","status":"open","evidenceSequenceNos":[11]}],"newTopics":[{"id":"topic-vpn-certificate","label":"VPN証明書問題","description":"期限切れと更新対応"}],"assignments":[{"nodeId":"vpn-cert-owner","parentTopicId":"agenda-3","confidence":0.9}]}`
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(round4, raw, mc, 4, []int64{11}, scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	finalRaw, err := finalizeAgendaLifecyclePayload(raw, mc, 4)
	if err != nil {
		t.Fatal(err)
	}
	finalState := previousLiveAnalysisState(finalRaw)
	diagnostics := validateTreeIntegrity(finalState.Tree, finalState.Items, mc, finalState.AgendaAnchors)
	counts := summarizeAgendaAnchorStatuses(finalState.AgendaAnchors)
	if !diagnostics.Valid || diagnostics.AgendaRecordCount != 3 || diagnostics.AgendaRecordsPreserved != 3 || diagnostics.MaterializedAgendaCount != 2 || counts[agendaStatusDiscussed] != 2 || counts[agendaStatusNotDiscussed] != 1 || len(diagnostics.EmptyAgendaTopicIDs) != 0 {
		t.Fatalf("final integrity=%+v anchors=%+v", diagnostics, finalState.AgendaAnchors)
	}
	// NotDiscussedAgendaCount was previously never populated by
	// validateTreeIntegrity (always 0). It must now match the anchor-status
	// count of agendaStatusNotDiscussed anchors when anchorValues is passed.
	if diagnostics.NotDiscussedAgendaCount != counts[agendaStatusNotDiscussed] {
		t.Fatalf("diagnostics.NotDiscussedAgendaCount=%d want anchor-status count=%d", diagnostics.NotDiscussedAgendaCount, counts[agendaStatusNotDiscussed])
	}
	if agendaTopicNodeByRef(finalState.Tree, "agenda-3") != nil {
		t.Fatalf("undiscussed agenda materialized: %+v", finalState.Tree.Nodes)
	}
	vpnTopics := 0
	for _, node := range finalState.Tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic && strings.Contains(node.Label, "VPN") {
			vpnTopics++
		}
	}
	if vpnTopics != 1 || itemTopicID(finalState.Tree, findItemByTitlePart(finalState.Items, "VPN証明書更新の担当者").ID) == stableAgendaTopicID("agenda-3", 0) {
		t.Fatalf("vpnTopics=%d tree=%+v", vpnTopics, finalState.Tree.Nodes)
	}
	health := computeTreeHealth(finalState.Tree)
	topicSummaries := make([]string, 0)
	for _, node := range finalState.Tree.Nodes {
		if node.Kind == "topic" && node.ID != treeRootNodeID && node.ID != treeUnclassifiedTopicID {
			topicSummaries = append(topicSummaries, node.ID+":"+node.Label+":"+strings.Join(node.AgendaRefs, ","))
		}
	}
	t.Logf("incident-recovery replay agendaRecordCount=3 agendaRecordsPreserved=3 materializedAgendaCount=2 discussedAgendaCount=2 notDiscussedAgendaCount=1 emptyAgendaTopicsAfter=0 dynamicVPNTopics=1 nodes=%d edges=%d topics=%v needsReorganization=%t reorganizationReasons=%v reorganizationMetrics=%q treeIntegrityValid=%t", len(finalState.Tree.Nodes), len(finalState.Tree.Edges), topicSummaries, health.needsReorganization(), health.reorganizationReasons(), health.String(), diagnostics.Valid)
}

func TestMultipleAgendaTopicsMergeIntoOneReferenceSet(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "障害調査", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "復旧対応", Order: 2, Role: agendaRolePrimary},
	}}
	topic1ID, topic2ID := stableAgendaTopicID("agenda-1", 0), stableAgendaTopicID("agenda-2", 0)
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem},
		{ID: topic1ID, Kind: "topic", ParentID: treeRootNodeID, Label: "障害調査と復旧", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}, Materialized: true},
		{ID: topic2ID, Kind: "topic", ParentID: treeRootNodeID, Label: "復旧対応", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-2"}, Materialized: true},
		{ID: "fact-router", Kind: "fact", ParentID: topic1ID, Label: "ルーター確認"},
		{ID: "decision-rollback", Kind: "decision", ParentID: topic2ID, Label: "切り戻し"},
	}, Edges: []liveAnalysisTreeEdge{
		{Source: treeRootNodeID, Target: topic1ID}, {Source: treeRootNodeID, Target: topic2ID},
		{Source: topic1ID, Target: "fact-router"}, {Source: topic2ID, Target: "decision-rollback"},
	}, Relations: []liveAnalysisTreeRelation{{Source: "fact-router", Target: "decision-rollback", Kind: "supports"}}}
	stats := &liveAnalysisTreeMergeStats{}
	merged, applied := applyTreeOperations(tree, mc, []treeOperation{{Type: "merge_topic", FromTopicID: topic2ID, IntoTopicID: topic1ID}}, TreeClassificationConfig{}, stats, 8)
	topic := treeNodeByID(merged, topic1ID)
	if applied != 1 || topic == nil || len(topic.AgendaRefs) != 2 || !containsExactString(topic.MergedFromNodeIDs, topic2ID) || treeNodeByID(merged, topic2ID) != nil {
		t.Fatalf("applied=%d topic=%+v tree=%+v", applied, topic, merged)
	}
	if treeNodeByID(merged, "fact-router") == nil || treeNodeByID(merged, "decision-rollback") == nil || itemTopicID(merged, "decision-rollback") != topic1ID || len(merged.Relations) != 1 {
		t.Fatalf("merged children/relations lost: %+v", merged)
	}
	diagnostics := validateTreeIntegrity(merged, nil, mc)
	if !diagnostics.Valid || diagnostics.MaterializedAgendaCount != 2 || diagnostics.MergedAgendaCount != 2 || len(diagnostics.DuplicateAgendaMaterializations) != 0 {
		t.Fatalf("integrity=%+v", diagnostics)
	}
}

func TestEmptyAgendaTopicMergeTransfersReferenceAndDematerializes(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "復旧対応", Order: 1, Role: agendaRolePrimary}}}
	agendaTopicID := stableAgendaTopicID("agenda-1", 0)
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem},
		{ID: agendaTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "復旧対応", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}, Materialized: true},
		{ID: "topic-recovery", Kind: "topic", ParentID: treeRootNodeID, Label: "切り戻しによる復旧", Origin: topicOriginDynamic},
		{ID: "decision-rollback", Kind: "decision", ParentID: "topic-recovery", Label: "旧スイッチへ切り戻す"},
	}, Edges: []liveAnalysisTreeEdge{
		{Source: treeRootNodeID, Target: agendaTopicID}, {Source: treeRootNodeID, Target: "topic-recovery"}, {Source: "topic-recovery", Target: "decision-rollback"},
	}}
	stats := &liveAnalysisTreeMergeStats{}
	merged, applied := applyTreeOperations(tree, mc, []treeOperation{{Type: "merge_topic", FromTopicID: agendaTopicID, IntoTopicID: "topic-recovery"}}, TreeClassificationConfig{}, stats, 9)
	topic := treeNodeByID(merged, "topic-recovery")
	diagnostics := validateTreeIntegrity(merged, nil, mc)
	if applied != 1 || topic == nil || !containsExactString(topic.AgendaRefs, "agenda-1") || topic.Origin != topicOriginMixed || treeNodeByID(merged, agendaTopicID) != nil || treeNodeByID(merged, "decision-rollback") == nil {
		t.Fatalf("applied=%d topic=%+v tree=%+v", applied, topic, merged)
	}
	if !diagnostics.Valid || diagnostics.MaterializedAgendaCount != 1 || len(diagnostics.EmptyAgendaTopicIDs) != 0 || stats.AgendaTopicsDematerialized != 1 {
		t.Fatalf("integrity=%+v stats=%+v", diagnostics, stats)
	}
}

func TestCandidateMaterializesMatchingPlannedAgendaBeforeDynamicPromotion(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "ネットワーク復旧対応", Order: 1, Role: agendaRolePrimary}}}
	scope := liveEvidenceScope{
		Allowed: map[int64]struct{}{1: {}, 2: {}},
		TranscriptText: map[int64]string{
			1: "ネットワーク復旧対応として旧スイッチへ切り戻すことを決定します。", 2: "復旧後に疎通確認を行います。",
		},
		CoveredThrough: 2,
	}
	cfg := TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}
	scope.CurrentRound = map[int64]struct{}{1: {}}
	round1 := `{"summary":"切り戻し","currentTopic":"ネットワーク復旧","items":[{"clientKey":"rollback","kind":"decision","severity":"high","title":"旧スイッチへの切り戻し","body":"ネットワーク復旧対応として旧スイッチへ切り戻すことを決定します","status":"open","evidenceSequenceNos":[1]}],"newTopics":[{"id":"topic-recovery","label":"切り戻しと疎通確認による復旧","description":"旧スイッチと疎通の確認"}],"assignments":[{"nodeId":"rollback","parentTopicId":"topic-recovery","confidence":0.9}]}`
	round1Stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(round1, nil, mc, 1, []int64{1}, scope, cfg, round1Stats)
	if err != nil {
		t.Fatal(err)
	}
	state1 := previousLiveAnalysisState(raw)
	agendaTopic1 := agendaTopicNodeByRef(state1.Tree, "agenda-1")
	if agendaTopic1 == nil || agendaTopic1.ID == "agenda-1" || !strings.HasPrefix(agendaTopic1.ID, "topic-") ||
		!containsExactString(agendaTopic1.ModelTopicIDs, "topic-recovery") ||
		round1Stats.AgendaTopicsMaterialized != 1 || len(state1.EmergingTopics) != 0 {
		t.Fatalf("first-round planned agenda was not reconciled: tree=%+v candidates=%+v stats=%+v", state1.Tree.Nodes, state1.EmergingTopics, round1Stats)
	}
	firstItem := findItemByTitlePart(state1.Items, "旧スイッチへの切り戻し")
	if firstItem == nil || itemTopicID(state1.Tree, firstItem.ID) != agendaTopic1.ID {
		t.Fatalf("first item=%+v agendaTopic=%+v", firstItem, agendaTopic1)
	}

	scope.CurrentRound = map[int64]struct{}{2: {}}
	round2 := `{"summary":"疎通確認","currentTopic":"ネットワーク復旧","items":[{"clientKey":"connectivity","kind":"todo","severity":"medium","title":"復旧後の疎通確認","body":"切り戻し後にネットワーク疎通を確認する","status":"open","evidenceSequenceNos":[2]}],"newTopics":[{"id":"topic-recovery","label":"切り戻しと疎通確認による復旧","description":"旧スイッチと疎通の確認"}],"assignments":[{"nodeId":"connectivity","parentTopicId":"topic-recovery","confidence":0.9}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(round2, raw, mc, 2, []int64{2}, scope, cfg, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	agendaTopic := agendaTopicNodeByRef(state.Tree, "agenda-1")
	if agendaTopic == nil || agendaTopic.ID != agendaTopic1.ID || stats.AgendaTopicsMaterialized != 0 || stats.AgendaTopicIDsReused == 0 || len(state.EmergingTopics) != 0 {
		t.Fatalf("tree=%+v candidates=%+v stats=%+v", state.Tree.Nodes, state.EmergingTopics, stats)
	}
	for _, title := range []string{"旧スイッチへの切り戻し", "復旧後の疎通確認"} {
		item := findItemByTitlePart(state.Items, title)
		if item == nil || itemTopicID(state.Tree, item.ID) != agendaTopic.ID {
			t.Fatalf("item %q=%+v", title, item)
		}
	}
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic && strings.Contains(node.Label, "復旧") {
			t.Fatalf("duplicate dynamic topic remained: %+v", node)
		}
	}
}

func TestAgendaTopicSplitKeepsExplicitReferenceHistory(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "復旧対応", Order: 1, Role: agendaRolePrimary}}}
	agendaTopicID := stableAgendaTopicID("agenda-1", 0)
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem},
		{ID: agendaTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "復旧対応", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}, Materialized: true},
		{ID: "fact-check", Kind: "fact", ParentID: agendaTopicID, Label: "機器確認"},
		{ID: "decision-rollback", Kind: "decision", ParentID: agendaTopicID, Label: "切り戻し"},
		{ID: "todo-connectivity", Kind: "todo", ParentID: agendaTopicID, Label: "疎通確認"},
	}, Edges: []liveAnalysisTreeEdge{
		{Source: treeRootNodeID, Target: agendaTopicID}, {Source: agendaTopicID, Target: "fact-check"},
		{Source: agendaTopicID, Target: "decision-rollback"}, {Source: agendaTopicID, Target: "todo-connectivity"},
	}}
	stats := &liveAnalysisTreeMergeStats{}
	split, applied := applyTreeOperations(tree, mc, []treeOperation{{
		Type: "split_topic", FromTopicID: agendaTopicID, TopicID: "topic-recovery-validation",
		Label: "切り戻し後の復旧確認", EvidenceItemIDs: []string{"decision-rollback", "todo-connectivity"},
	}}, TreeClassificationConfig{}, stats, 10)
	fromTopic, newTopic := treeNodeByID(split, agendaTopicID), treeNodeByID(split, "topic-recovery-validation")
	if applied != 1 || fromTopic == nil || newTopic == nil || fromTopic.AgendaSplitGroupID == "" || fromTopic.AgendaSplitGroupID != newTopic.AgendaSplitGroupID || stats.AgendaTopicsSplit != 1 {
		t.Fatalf("applied=%d from=%+v new=%+v stats=%+v", applied, fromTopic, newTopic, stats)
	}
	if itemTopicID(split, "fact-check") != agendaTopicID || itemTopicID(split, "decision-rollback") != "topic-recovery-validation" || itemTopicID(split, "todo-connectivity") != "topic-recovery-validation" {
		t.Fatalf("split parents=%+v", split.Nodes)
	}
	diagnostics := validateTreeIntegrity(split, nil, mc)
	anchors := reconcileAgendaAnchors(nil, mc, split, nil, 10, false)
	if !diagnostics.Valid || diagnostics.MaterializedAgendaCount != 1 || len(diagnostics.DuplicateAgendaMaterializations) != 0 || len(anchors) != 1 || len(anchors[0].MaterializedTopicIDs) != 2 {
		t.Fatalf("integrity=%+v anchors=%+v", diagnostics, anchors)
	}
}

func TestFinalAgendaLifecycleMergesOverlappingDynamicTopic(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "ネットワーク復旧", Order: 1, Role: agendaRolePrimary}}}
	agendaTopicID := stableAgendaTopicID("agenda-1", 0)
	state := liveAnalysisPayload{TreeVersion: 12, Items: []liveAnalysisItem{
		{ID: "fact-router", Kind: "fact", Title: "ルーター確認", EvidenceSequenceNos: []int64{4}},
		{ID: "decision-rollback", Kind: "decision", Title: "旧スイッチへの切り戻し", EvidenceSequenceNos: []int64{6}},
	}, Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem},
		{ID: agendaTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク復旧", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}, Materialized: true},
		{ID: "topic-recovery", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク復旧", Description: "切り戻しによる復旧", Origin: topicOriginDynamic},
		{ID: "fact-router", Kind: "fact", ParentID: agendaTopicID, Label: "ルーター確認"},
		{ID: "decision-rollback", Kind: "decision", ParentID: "topic-recovery", Label: "旧スイッチへの切り戻し"},
	}, Edges: []liveAnalysisTreeEdge{
		{Source: treeRootNodeID, Target: agendaTopicID}, {Source: treeRootNodeID, Target: "topic-recovery"},
		{Source: agendaTopicID, Target: "fact-router"}, {Source: "topic-recovery", Target: "decision-rollback"},
	}}}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	finalRaw, err := finalizeAgendaLifecyclePayload(raw, mc, 12)
	if err != nil {
		t.Fatal(err)
	}
	finalState := previousLiveAnalysisState(finalRaw)
	topic := treeNodeByID(finalState.Tree, agendaTopicID)
	if topic == nil || topic.Origin != topicOriginMixed || !containsExactString(topic.MergedFromNodeIDs, "topic-recovery") || treeNodeByID(finalState.Tree, "topic-recovery") != nil || itemTopicID(finalState.Tree, "decision-rollback") != agendaTopicID {
		t.Fatalf("final tree=%+v", finalState.Tree)
	}
	if len(finalState.AgendaAnchors) != 1 || finalState.AgendaAnchors[0].Status != agendaStatusMerged || !validateTreeIntegrity(finalState.Tree, finalState.Items, mc, finalState.AgendaAnchors).Valid {
		t.Fatalf("anchors=%+v integrity=%+v", finalState.AgendaAnchors, finalState.TreeIntegrity)
	}
}

func TestAgendaDematerializeProtectsManualOrReferencedTopic(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "手動確認", Order: 1, Role: agendaRolePrimary}}}
	agendaTopicID := stableAgendaTopicID("agenda-1", 0)
	for name, fixture := range map[string]struct {
		node      liveAnalysisTreeNode
		relations []liveAnalysisTreeRelation
	}{
		"manual": {
			node: liveAnalysisTreeNode{ID: agendaTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "手動確認", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}, Materialized: true, LastParentChangeSource: "manual"},
		},
		"referenced": {
			node:      liveAnalysisTreeNode{ID: agendaTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "手動確認", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}, Materialized: true},
			relations: []liveAnalysisTreeRelation{{Source: agendaTopicID, Target: treeRootNodeID, Kind: "references"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem}, fixture.node}, Edges: []liveAnalysisTreeEdge{{Source: treeRootNodeID, Target: agendaTopicID}}, Relations: fixture.relations}
			pruneEmptyAgendaTopics(tree, mc, 10, true, nil)
			if treeNodeByID(tree, agendaTopicID) == nil {
				t.Fatalf("protected topic was dematerialized: %+v", tree)
			}
		})
	}
}

func agendaTopicNodeByRef(tree *liveAnalysisTree, agendaID string) *liveAnalysisTreeNode {
	if tree == nil {
		return nil
	}
	for index := range tree.Nodes {
		node := &tree.Nodes[index]
		if node.Kind == "topic" && containsExactString(node.AgendaRefs, agendaID) {
			return node
		}
	}
	return nil
}
