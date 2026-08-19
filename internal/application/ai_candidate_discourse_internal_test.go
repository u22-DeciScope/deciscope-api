package application

import (
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestCompleteDynamicTopicLabelNeverCutsConjugation(t *testing.T) {
	for _, input := range []string{
		"VPN装置の証明書が来月末に期限切れになる",
		"VPN装置の証明書が来月末に期限切れにな",
	} {
		got := completeDynamicTopicLabel(input, "VPN装置証明書の期限切れ")
		if got == "" || got == "VPN装置の証明書が来月末に期限切れにな" || dynamicTopicLabelNeedsRepair(got, got) {
			t.Fatalf("input=%q completed topic label=%q", input, got)
		}
	}
}

func TestReconcileDiscourseTopicProposalsKeepsOneStableVPNTopic(t *testing.T) {
	items := []liveAnalysisItem{
		{ID: "vpn-expiry-fact", Kind: "fact", Title: "VPN装置の証明書が来月末に期限切れになる", Body: "VPN装置の証明書が来月末に期限切れになる", Status: "open", EvidenceSequenceNos: []int64{1}},
		{ID: "vpn-remote-risk", Kind: "risk", Title: "証明書を放置するとリモート接続不能になるリスク", Body: "VPN装置の証明書を放置するとリモート接続できなくなる可能性がある", Status: "open", EvidenceSequenceNos: []int64{1, 2}},
		{ID: "vpn-management-issue", Kind: "issue", Title: "VPN証明書更新を別の対応事項として管理する", Body: "VPN証明書更新を今回の障害とは別の対応事項として管理する", Status: "open", EvidenceSequenceNos: []int64{1, 3}},
		{ID: "vpn-renewal-todo", Kind: "todo", Title: "高橋がVPN証明書の更新手順と作業可能日を確認する", Body: "高橋がVPN証明書の更新手順と作業可能日を確認する", Status: "open", EvidenceSequenceNos: []int64{4}},
	}
	previousTree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "root"},
		{ID: "topic-vpn", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN装置証明書の期限切れ対応", Origin: topicOriginDynamic},
		{ID: "vpn-expiry-fact", Kind: "fact", ParentID: "topic-vpn", Label: items[0].Title},
	}}
	assignments := []treeAssignment{
		{NodeID: "vpn-remote-risk", ParentTopicID: "remote-connection-risk", ModelParentTopicID: "model-parent", ServerSource: "model", ResolvedAgendaSpanMode: "preserved"},
		{NodeID: "vpn-management-issue", ParentTopicID: "vpn-renewal-management"},
		{NodeID: "vpn-renewal-todo", ParentTopicID: "certificate-renewal-work"},
	}
	topics := []liveAnalysisTreeNode{
		{ID: "remote-connection-risk", Kind: "topic", Label: "リモート接続リスク"},
		{ID: "vpn-renewal-management", Kind: "topic", Label: "VPN証明書更新管理"},
		{ID: "certificate-renewal-work", Kind: "topic", Label: "証明書更新作業"},
	}
	scope := vpnDiscourseEvidenceScope()
	stats := &liveAnalysisTreeMergeStats{}

	gotAssignments, gotTopics := reconcileDiscourseTopicProposals(
		assignments, topics, previousTree, items, scope,
		[]agendaContextSpan{{Mode: agendaContextModeNoAgenda, StartSequenceNo: 1, EndSequenceNo: 4}},
		&meetingContext{}, stats,
	)
	if len(gotTopics) != 0 {
		t.Fatalf("new topics=%+v, want all aliases absorbed by stable topic", gotTopics)
	}
	for _, assignment := range gotAssignments {
		if assignment.ParentTopicID != "topic-vpn" {
			t.Fatalf("assignment=%+v, want parent topic-vpn", assignment)
		}
	}
	if gotAssignments[0].ModelParentTopicID != "model-parent" || gotAssignments[0].ServerSource != assignmentSourceRule || gotAssignments[0].ResolvedAgendaSpanMode != "preserved" {
		t.Fatalf("assignment provenance was overwritten: %+v", gotAssignments[0])
	}
	if stats.CandidateIDsMerged != 3 {
		t.Fatalf("candidate merges=%d, want 3", stats.CandidateIDsMerged)
	}
}

func TestReconcileDiscourseTopicProposalsRejectsDifferentConcreteObjects(t *testing.T) {
	items := []liveAnalysisItem{
		{ID: "vpn-fact", Kind: "fact", Title: "VPN証明書が来月末に期限切れになる", Body: "VPN証明書が来月末に期限切れになる", Status: "open", EvidenceSequenceNos: []int64{1}},
		{ID: "license-fact", Kind: "fact", Title: "会計製品ライセンスが来月末に期限切れになる", Body: "会計製品ライセンスが来月末に期限切れになる", Status: "open", EvidenceSequenceNos: []int64{2}},
	}
	previousTree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "root"},
		{ID: "topic-vpn", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN証明書対応", Origin: topicOriginDynamic},
		{ID: "vpn-fact", Kind: "fact", ParentID: "topic-vpn", Label: items[0].Title},
	}}
	scope := evidenceScopeFromTexts(map[int64]string{1: items[0].Body, 2: items[1].Body}, 1, 2)
	assignments := []treeAssignment{{NodeID: "license-fact", ParentTopicID: "topic-license"}}
	topics := []liveAnalysisTreeNode{{ID: "topic-license", Kind: "topic", Label: "会計製品ライセンスの期限切れ"}}

	gotAssignments, gotTopics := reconcileDiscourseTopicProposals(
		assignments, topics, previousTree, items, scope,
		[]agendaContextSpan{{Mode: agendaContextModeNoAgenda, StartSequenceNo: 1, EndSequenceNo: 2}},
		&meetingContext{}, nil,
	)
	if len(gotTopics) != 1 || gotAssignments[0].ParentTopicID != "topic-license" {
		t.Fatalf("different business objects were merged: assignments=%+v topics=%+v", gotAssignments, gotTopics)
	}
}

func TestReconcileDiscourseTopicProposalsDoesNotBridgeExistingTopics(t *testing.T) {
	items := []liveAnalysisItem{
		{ID: "auth-a-fact", Kind: "fact", Title: "認証基盤Aの稼働状況を確認した", Body: "認証基盤Aの稼働状況を確認した", Status: "open", EvidenceSequenceNos: []int64{1}},
		{ID: "auth-b-fact", Kind: "fact", Title: "認証基盤Bの稼働状況を確認した", Body: "認証基盤Bの稼働状況を確認した", Status: "open", EvidenceSequenceNos: []int64{2}},
		{ID: "auth-bridge", Kind: "issue", Title: "認証基盤Aと認証基盤Bの統合監視を検討する", Body: "認証基盤Aと認証基盤Bの統合監視を検討する", Status: "open", EvidenceSequenceNos: []int64{3}},
		{ID: "auth-b-todo", Kind: "todo", Title: "認証基盤Bの設定変更手順を確認する", Body: "認証基盤Bの設定変更手順を確認する", Status: "open", EvidenceSequenceNos: []int64{5}},
	}
	previousTree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "root"},
		{ID: "topic-auth-a", Kind: "topic", ParentID: treeRootNodeID, Label: "認証基盤A", Origin: topicOriginDynamic},
		{ID: "topic-auth-b", Kind: "topic", ParentID: treeRootNodeID, Label: "認証基盤B", Origin: topicOriginDynamic},
		{ID: "auth-a-fact", Kind: "fact", ParentID: "topic-auth-a", Label: items[0].Title},
		{ID: "auth-b-fact", Kind: "fact", ParentID: "topic-auth-b", Label: items[1].Title},
	}}
	assignments := []treeAssignment{
		{NodeID: "auth-bridge", ParentTopicID: "topic-auth-bridge"},
		{NodeID: "auth-b-todo", ParentTopicID: "topic-auth-b-followup"},
	}
	topics := []liveAnalysisTreeNode{
		{ID: "topic-auth-bridge", Kind: "topic", Label: "認証基盤A・Bの統合監視"},
		{ID: "topic-auth-b-followup", Kind: "topic", Label: "認証基盤Bの設定変更"},
	}
	scope := evidenceScopeFromTexts(map[int64]string{
		1: items[0].Body,
		2: items[1].Body,
		3: items[2].Body,
		5: items[3].Body,
	}, 1, 2, 3, 5)

	gotAssignments, gotTopics := reconcileDiscourseTopicProposals(
		assignments, topics, previousTree, items, scope,
		[]agendaContextSpan{{Mode: agendaContextModeNoAgenda, StartSequenceNo: 1, EndSequenceNo: 5}},
		&meetingContext{}, nil,
	)
	if len(gotTopics) != 0 {
		t.Fatalf("absorbed proposals=%+v, want both proposals resolved to their direct anchors", gotTopics)
	}
	if gotAssignments[0].ParentTopicID != "topic-auth-a" {
		t.Fatalf("bridge parent=%s, want deterministic first anchor topic-auth-a", gotAssignments[0].ParentTopicID)
	}
	if gotAssignments[1].ParentTopicID != "topic-auth-b" {
		t.Fatalf("B-only proposal crossed the bridge: assignment=%+v", gotAssignments[1])
	}
}

func TestReconcileDiscourseTopicProposalsIgnoresReferenceOnlyEvidenceForProximity(t *testing.T) {
	for _, tc := range []struct {
		name string
		role liveEvidenceRole
	}{
		{name: "recap", role: liveEvidenceReferenceRecap},
		{name: "context_only", role: liveEvidenceDiscourseOnly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := []liveAnalysisItem{
				{ID: "auth-policy", Kind: "fact", Title: "認証基盤Cの失効ポリシーを確認した", Body: "認証基盤Cの失効ポリシーを確認した", Status: "open", EvidenceSequenceNos: []int64{1, 10}},
				{ID: "auth-audit", Kind: "issue", Title: "認証基盤Cの監査証跡を確認する", Body: "認証基盤Cの監査証跡を確認する", Status: "open", EvidenceSequenceNos: []int64{6, 11}},
			}
			previousTree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "root"},
				{ID: "topic-auth-policy", Kind: "topic", ParentID: treeRootNodeID, Label: "認証基盤Cの失効ポリシー", Origin: topicOriginDynamic},
				{ID: "auth-policy", Kind: "fact", ParentID: "topic-auth-policy", Label: items[0].Title},
			}}
			assignments := []treeAssignment{{NodeID: "auth-audit", ParentTopicID: "topic-auth-audit"}}
			topics := []liveAnalysisTreeNode{{ID: "topic-auth-audit", Kind: "topic", Label: "認証基盤Cの監査証跡"}}
			scope := evidenceScopeFromTexts(map[int64]string{
				1:  items[0].Body,
				6:  items[1].Body,
				10: "先ほどの認証基盤Cの失効ポリシーをまとめます。",
				11: "続けて認証基盤Cの監査証跡にも触れます。",
			}, 1, 6, 10, 11)
			scope.EvidenceRoles = map[int64]liveEvidenceRole{
				1: liveEvidencePrimary, 6: liveEvidencePrimary,
				10: tc.role, 11: tc.role,
			}
			if tc.role == liveEvidenceReferenceRecap {
				scope.RecapRound = map[int64]struct{}{10: {}, 11: {}}
			} else {
				scope.ContextOnlyRound = map[int64]struct{}{10: {}, 11: {}}
			}

			gotAssignments, gotTopics := reconcileDiscourseTopicProposals(
				assignments, topics, previousTree, items, scope,
				[]agendaContextSpan{{Mode: agendaContextModeNoAgenda, StartSequenceNo: 1, EndSequenceNo: 11}},
				&meetingContext{}, nil,
			)
			if len(gotTopics) != 1 || gotAssignments[0].ParentTopicID != "topic-auth-audit" {
				t.Fatalf("reference-only evidence merged distant topics: assignments=%+v topics=%+v", gotAssignments, gotTopics)
			}
		})
	}
}

func TestReconcileDiscourseTopicProposalsDoesNotUseAgendaLinkedTopicAsNoAgendaAnchor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		origin string
	}{
		{name: "mixed_topic", origin: topicOriginMixed},
		{name: "dynamic_topic_with_agenda_refs", origin: topicOriginDynamic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := []liveAnalysisItem{
				{ID: "agenda-vpn-fact", Kind: "fact", Title: "VPN証明書の期限を確認した", Body: "VPN証明書の期限を確認した", Status: "open", EvidenceSequenceNos: []int64{1}},
				{ID: "external-vpn-todo", Kind: "todo", Title: "VPN証明書の別環境向け更新手順を確認する", Body: "VPN証明書の別環境向け更新手順を確認する", Status: "open", EvidenceSequenceNos: []int64{2}},
			}
			previousTree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "root"},
				{ID: "topic-agenda-vpn", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN証明書管理", Origin: tc.origin, AgendaRefs: []string{"agenda-vpn"}},
				{ID: "agenda-vpn-fact", Kind: "fact", ParentID: "topic-agenda-vpn", Label: items[0].Title},
			}}
			assignments := []treeAssignment{{NodeID: "external-vpn-todo", ParentTopicID: "topic-external-vpn"}}
			topics := []liveAnalysisTreeNode{{ID: "topic-external-vpn", Kind: "topic", Label: "VPN証明書の別環境対応"}}
			scope := evidenceScopeFromTexts(map[int64]string{1: items[0].Body, 2: items[1].Body}, 1, 2)
			mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-vpn", Title: "VPN証明書管理", Order: 1}}}

			gotAssignments, gotTopics := reconcileDiscourseTopicProposals(
				assignments, topics, previousTree, items, scope,
				[]agendaContextSpan{{Mode: agendaContextModeNoAgenda, StartSequenceNo: 1, EndSequenceNo: 2, Explicit: true}},
				mc, nil,
			)
			if len(gotTopics) != 1 || gotAssignments[0].ParentTopicID != "topic-external-vpn" {
				t.Fatalf("agenda-linked topic absorbed no-agenda proposal: assignments=%+v topics=%+v", gotAssignments, gotTopics)
			}
		})
	}
}

func TestReconcileDiscourseTopicProposalsRejectsDifferentSpeaker(t *testing.T) {
	items := []liveAnalysisItem{
		{ID: "vpn-fact", Kind: "fact", Title: "VPN証明書が期限切れになる", Body: "VPN証明書が期限切れになる", Status: "open", EvidenceSequenceNos: []int64{1}},
		{ID: "vpn-todo", Kind: "todo", Title: "VPN証明書を更新する", Body: "VPN証明書を更新する", Status: "open", EvidenceSequenceNos: []int64{2}},
	}
	previousTree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "root"},
		{ID: "topic-vpn", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN証明書対応", Origin: topicOriginDynamic},
		{ID: "vpn-fact", Kind: "fact", ParentID: "topic-vpn", Label: items[0].Title},
	}}
	scope := vpnDiscourseEvidenceScope()
	scope.Segments[2] = domain.TranscriptSegment{SequenceNo: 2, SpeakerID: "another-speaker", Text: scope.TranscriptText[2], IsFinal: true}
	assignments := []treeAssignment{{NodeID: "vpn-todo", ParentTopicID: "new-vpn-topic"}}
	topics := []liveAnalysisTreeNode{{ID: "new-vpn-topic", Kind: "topic", Label: "VPN証明書更新"}}

	gotAssignments, gotTopics := reconcileDiscourseTopicProposals(
		assignments, topics, previousTree, items, scope,
		[]agendaContextSpan{{Mode: agendaContextModeNoAgenda, StartSequenceNo: 1, EndSequenceNo: 4}},
		&meetingContext{}, nil,
	)
	if len(gotTopics) != 1 || gotAssignments[0].ParentTopicID != "new-vpn-topic" {
		t.Fatalf("different-speaker topic was merged: assignments=%+v topics=%+v", gotAssignments, gotTopics)
	}
}

func TestSemanticKindRelationsLinksConditionalVPNRiskToExpiryFact(t *testing.T) {
	fact := liveAnalysisItem{
		ID: "vpn-fact", Kind: "fact", Title: "VPN装置の証明書が来月末に期限切れになる",
		Body: "VPN装置の証明書が来月末に期限切れになる", Status: "open", EvidenceSequenceNos: []int64{1},
	}
	risk := liveAnalysisItem{
		ID: "vpn-risk", Kind: "risk", Title: "VPN証明書を放置するとリモート接続不能になるリスク",
		Body: "VPN装置の証明書を放置するとリモート接続できなくなる可能性がある", Status: "open", EvidenceSequenceNos: []int64{1, 2},
	}
	relations := semanticKindRelations(risk, fact, vpnDiscourseEvidenceScope())
	if len(relations) != 1 || relations[0].Source != risk.ID || relations[0].Target != fact.ID || relations[0].Kind != itemRelationSupportedBy {
		t.Fatalf("relations=%+v", relations)
	}

	unrelated := fact
	unrelated.ID = "license-fact"
	unrelated.Title = "監視製品のライセンスが来月末に期限切れになる"
	unrelated.Body = unrelated.Title
	if got := semanticKindRelations(risk, unrelated, vpnDiscourseEvidenceScope()); len(got) != 0 {
		t.Fatalf("unrelated relation=%+v, want none", got)
	}
}

func vpnDiscourseEvidenceScope() liveEvidenceScope {
	texts := map[int64]string{
		1: "VPN装置の証明書が来月末に期限切れになります。",
		2: "放置するとリモート接続できなくなる可能性があります。",
		3: "今回の障害とは別の対応事項として管理します。",
		4: "高橋さんがVPN証明書の更新手順と作業可能日を確認します。",
	}
	scope := evidenceScopeFromTexts(texts, 1, 2, 3, 4)
	for sequenceNo, segment := range scope.Segments {
		segment.SpeakerID = "speaker-yamashita"
		scope.Segments[sequenceNo] = segment
	}
	return scope
}
