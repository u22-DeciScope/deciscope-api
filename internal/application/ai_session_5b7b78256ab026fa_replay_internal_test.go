package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

// Regression replay for the persisted 33-segment transcript of
// session_5b7b78256ab026fa. The starting state keeps the observed IDs and the
// three semantic defects: false no-agenda parents, a generic VPN topic, and a
// leading-particle decision. No external model or database write is needed.
func TestSession5b7b78256ab026faSemanticReplay(t *testing.T) {
	texts := session5b7b78256ab026faTranscript()
	scope := evidenceScopeFromTexts(texts, sequenceRange(1, 33)...)
	scope.Segments = make(map[int64]domain.TranscriptSegment, len(texts))
	segments := make([]domain.TranscriptSegment, 0, len(texts))
	for _, sequenceNo := range sequenceRange(1, 33) {
		segment := domain.TranscriptSegment{SessionID: "session_5b7b78256ab026fa", SequenceNo: sequenceNo, SpeakerID: "414", Text: texts[sequenceNo], IsFinal: true}
		scope.Segments[sequenceNo] = segment
		segments = append(segments, segment)
	}
	mc := &meetingContext{
		Title:      "名古屋支社ネットワーク障害の振り返りと再発防止会議",
		Purpose:    "名古屋支社で発生した社内ネットワーク障害について、影響範囲、直接原因、復旧対応を整理し、再発防止策、未解決の調査事項、担当者と期限を含むTODOを明確にする",
		Background: "名古屋支社の一部フロアで有線LANと無線LANが不安定となり、前夜に交換した3階アクセススイッチのVLAN設定に問題があった可能性が高い",
		Agenda: []agendaItem{
			{ID: "agenda-1", Title: "障害の影響範囲と発生時刻", Order: 1, Role: agendaRolePrimary},
			{ID: "agenda-2", Title: "原因調査と復旧対応", Order: 2, Role: agendaRolePrimary},
			{ID: "agenda-3", Title: "再発防止策", Order: 3, Role: agendaRolePrimary},
			{ID: "agenda-4", Title: "未解決事項と次回までの対応確認", Order: 4, Role: agendaRoleActionSummary},
		},
	}

	timeline := classifyDiscourseTimeline(scope)
	spanStats := &liveAnalysisTreeMergeStats{}
	spans := detectAgendaContextSpans(scope, mc, spanStats, timeline)
	var noAgendaStarts []int64
	for _, span := range spans {
		if span.Mode == agendaContextModeNoAgenda {
			noAgendaStarts = append(noAgendaStarts, span.StartSequenceNo)
		}
	}
	if len(noAgendaStarts) != 1 || noAgendaStarts[0] != 22 {
		t.Fatalf("no-agenda starts=%v spans=%+v", noAgendaStarts, spans)
	}
	for sequenceNo := int64(13); sequenceNo <= 21; sequenceNo++ {
		mode, agendaID, _ := agendaContextForEvidence([]int64{sequenceNo}, spans)
		if mode != agendaContextModeFixed || agendaID != "agenda-3" {
			t.Fatalf("sequence %d context=(%q,%q), spans=%+v", sequenceNo, mode, agendaID, spans)
		}
	}
	if mode, _, _ := agendaContextForEvidence([]int64{23}, spans); mode != agendaContextModeNoAgenda {
		t.Fatalf("VPN evidence lost explicit no-agenda protection: spans=%+v", spans)
	}

	decisionCandidates := detectDecisionCandidates(segments)
	// セグ14「…設定内容を確認するダブルチェックを必須にします」は
	// decisionPositivePatternの「を必須にします」追加により新たに検出される
	// (対象会議のseq12と同種の文言)。セグ15+16の参照解決由来の候補は引き続き
	// 2件目に現れる。
	if len(decisionCandidates) != 2 || decisionCandidates[0].SequenceNo != 14 {
		t.Fatalf("decision candidates=%+v", decisionCandidates)
	}
	repaired := decisionCandidates[1]
	if repaired.SequenceNo != 16 || !equalInt64s(repaired.SourceSequenceNos, []int64{15, 16}) {
		t.Fatalf("decision candidates=%+v", decisionCandidates)
	}
	if title := decisionCandidateTitle(repaired.Statement); strings.HasPrefix(title, "の") || !strings.Contains(title, "チェックリストの運用") {
		t.Fatalf("repaired decision title=%q", title)
	}

	previous := session5b7b78256ab026faDefectiveState()
	previousRaw, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
		`{"summary":"名古屋支社ネットワーク障害の振り返り","currentTopic":"会議終了","utteranceRoles":[],"items":[],"newTopics":[],"assignments":[]}`,
		previousRaw, mc, 18, sequenceRange(1, 33), scope,
		TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}, stats,
	)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	diagnostics := validateTreeIntegrity(state.Tree, state.Items, mc, state.AgendaAnchors)
	if !diagnostics.Valid || len(diagnostics.AgendaTopicIDCollisions) != 0 || len(diagnostics.UnknownAgendaRefs) != 0 || len(diagnostics.OrphanMaterializedTopicIDs) != 0 || len(diagnostics.EmptyAgendaTopicIDs) != 0 {
		t.Fatalf("integrity=%+v", diagnostics)
	}

	for _, itemID := range []string{"item-todo-e07c19f5d764", "decision-auto-214fc3e3f79b", "item-todo-89517b0a037e", "item-issue-discussion-1d3dd6e0a0e9", "item-risk-1b09fe292026"} {
		if topicID := treeItemTopic(state.Tree, itemID); topicID != "topic-agenda-7dd3ab9e5ea9" {
			t.Fatalf("agenda item %s topic=%s", itemID, topicID)
		}
	}
	vpnTopicID := treeItemTopic(state.Tree, "item-risk-feb31492a4f1")
	if vpnTopicID != "candidate-98301c302f8b" || treeItemTopic(state.Tree, "item-risk-5d2472d3ccae") != vpnTopicID || treeItemTopic(state.Tree, "item-todo-c3d48c965d89") != vpnTopicID {
		t.Fatalf("VPN parents risk=%s impact=%s todo=%s", vpnTopicID, treeItemTopic(state.Tree, "item-risk-5d2472d3ccae"), treeItemTopic(state.Tree, "item-todo-c3d48c965d89"))
	}
	vpnTopic := treeNodeByID(state.Tree, vpnTopicID)
	if vpnTopic == nil || genericTopicLabel(vpnTopic.Label) || !strings.Contains(vpnTopic.Label, "VPN証明書") {
		t.Fatalf("VPN topic=%+v", vpnTopic)
	}
	if treeItemTopic(state.Tree, "item-risk-1b09fe292026") == vpnTopicID {
		t.Fatal("unrelated monitoring alert risk was folded into VPN topic")
	}
	decision := findItemByID(state.Items, "decision-auto-214fc3e3f79b")
	if decision == nil || decisionStatementNeedsReferent(decision.Title) || strings.HasPrefix(decision.Title, "の") || !strings.Contains(decision.Title, "適用") || !equalInt64s(decision.EvidenceSequenceNos, []int64{15, 16}) {
		t.Fatalf("historical decision=%+v", decision)
	}
	for _, item := range state.Items {
		if item.Kind == "decision" && (isMeetingEndOnlyItem(item.Title, item.Body) || decisionStatementNeedsReferent(item.Title+" "+item.Body)) {
			t.Fatalf("invalid visible decision=%+v", item)
		}
	}
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" && node.ID != treeUnclassifiedTopicID && genericTopicLabel(node.Label) {
			t.Fatalf("generic visible topic=%+v", node)
		}
	}
	var correctionFact *liveAnalysisItem
	for index := range state.Items {
		if !state.Items[index].Inactive && state.Items[index].MergedIntoID == "" &&
			containsInt64(state.Items[index].EvidenceSequenceNos, 3) {
			correctionFact = &state.Items[index]
			break
		}
	}
	if correctionFact == nil || correctionFact.Kind != "fact" ||
		!strings.Contains(correctionFact.Title, "2階") || !strings.Contains(correctionFact.Title, "通信遅延") ||
		treeItemTopic(state.Tree, correctionFact.ID) != "topic-agenda-a5f8fcd0c7a2" {
		t.Fatalf("correction fact=%+v topic=%q reconciliations=%+v", correctionFact,
			treeItemTopic(state.Tree, correctionFact.ID), stats.AgendaReconciliations)
	}
	if node := treeNodeByID(state.Tree, treeUnclassifiedTopicID); node != nil {
		var children []liveAnalysisTreeNode
		for _, candidate := range state.Tree.Nodes {
			if candidate.ParentID == treeUnclassifiedTopicID {
				children = append(children, candidate)
			}
		}
		t.Fatalf("empty generic unclassified topic survived: %+v children=%+v", node, children)
	}
	for _, finding := range deterministicTreeAuditPrecheck(state, mc, classifyTreeAuditEvidence(state, segments), TreeAuditConfig{}) {
		if finding.Type == TreeAuditCrossAgendaContamination || finding.Type == TreeAuditRiskTodoSubjectFragmentation || finding.Type == TreeAuditLeadingParticleFragment || finding.Type == TreeAuditGenericTopicLabel {
			t.Fatalf("replay defect remained: %+v", finding)
		}
	}
	if stats.GenericTopicLabelsRewritten == 0 || stats.SubjectFragmentationRepairs == 0 || stats.SemanticParentCorrected+stats.SubjectFragmentationRepairs < 2 || stats.LowInformationItemsRewritten == 0 {
		t.Fatalf("repair stats=%+v", stats)
	}
	vpnRiskNode, vpnImpactNode, vpnTodoNode := treeNodeByID(state.Tree, "item-risk-feb31492a4f1"), treeNodeByID(state.Tree, "item-risk-5d2472d3ccae"), treeNodeByID(state.Tree, "item-todo-c3d48c965d89")
	t.Logf("session_5b7 replay treeIntegrityValid=true agendaTopicIdCollisions=0 unknownAgendaRefs=0 orphanMaterializedTopicIds=0 emptyAgendaTopicsAfter=0 falseNoAgendaStarts=0 genericVisibleTopicLabels=0 leadingParticleVisibleItems=0 incompleteDecisionItems=0 vpnRiskTodoFragmentation=0 crossAgendaContaminationAfter=0 noAgendaStarts=%v vpnTopicId=%s vpnTopicLabel=%q vpnDirectParents=[%s %s %s] decisionId=%s decisionTitle=%q decisionEvidence=%v", noAgendaStarts, vpnTopicID, vpnTopic.Label, vpnRiskNode.ParentID, vpnImpactNode.ParentID, vpnTodoNode.ParentID, decision.ID, decision.Title, decision.EvidenceSequenceNos)
}

func session5b7b78256ab026faDefectiveState() liveAnalysisPayload {
	items := []liveAnalysisItem{
		{ID: "item-todo-e07c19f5d764", Kind: "todo", Severity: "high", Title: "スイッチ交換チェックリスト案の作成", Body: "山下さんが今週金曜日までにチェックリスト案を作成する", Status: "open", ClassificationStatus: classificationAssigned, AssignmentSource: assignmentSourceNoAgendaSpan, AssignmentConfidence: .8, EvidenceSequenceNos: []int64{12, 13, 14, 15, 16, 17}, RelatedAgendaIDs: []string{"agenda-4"}},
		{ID: "decision-auto-214fc3e3f79b", Kind: "decision", Severity: "high", Title: "の運用を次回の機器交換から適用する", Body: "の運用を次回の機器交換から適用することにします", Status: "open", ClassificationStatus: classificationAssigned, AssignmentSource: assignmentSourceNoAgendaSpan, AssignmentConfidence: .8, EvidenceSequenceNos: []int64{16}},
		{ID: "item-todo-89517b0a037e", Kind: "todo", Severity: "medium", Title: "監視項目へVLAN単位の通信確認を追加する案", Body: "VLAN単位の通信異常を早期検知する監視項目を追加する", Status: "open", ClassificationStatus: classificationAssigned, AssignmentSource: assignmentSourceNoAgendaSpan, AssignmentConfidence: .8, EvidenceSequenceNos: []int64{18, 19}, RelatedAgendaIDs: []string{"agenda-4"}},
		{ID: "item-issue-discussion-1d3dd6e0a0e9", Kind: "issue", Subtype: "discussion", Severity: "medium", Title: "監視通知の間隔と条件の検討", Body: "アラート過多を避ける通知条件を次回までに検討する", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{20, 21}},
		{ID: "item-risk-1b09fe292026", Kind: "risk", Severity: "medium", Title: "監視対象追加によるアラート過多", Body: "監視対象を増やすとアラートが多くなりすぎる可能性", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{20}},
		{ID: "item-risk-feb31492a4f1", Kind: "risk", Severity: "high", Title: "VPN証明書の有効期限が来月末", Body: "VPN証明書の期限切れによりリモート接続へ影響する", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{23}},
		{ID: "item-risk-5d2472d3ccae", Kind: "risk", Severity: "high", Title: "放置するとリモート接続ができなくなる", Body: "今回のネットワーク障害とは直接関係しないVPN証明書期限切れの影響", Status: "open", ClassificationStatus: classificationUnclassified, EvidenceSequenceNos: []int64{24}},
		{ID: "item-todo-c3d48c965d89", Kind: "todo", Severity: "high", Title: "VPN証明書の更新手順と作業日程の確定", Body: "小林さんが今週中にVPN証明書の更新手順と作業可能日を確認する", Status: "open", ClassificationStatus: classificationAssigned, AssignmentSource: assignmentSourceRule, AssignmentConfidence: .45, EvidenceSequenceNos: []int64{25, 26}, RelatedAgendaIDs: []string{"agenda-4"}},
	}
	nodes := []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "名古屋支社ネットワーク障害の振り返りと再発防止会議", Origin: topicOriginSystem},
		{ID: "topic-agenda-7dd3ab9e5ea9", Kind: "topic", ParentID: treeRootNodeID, Label: "再発防止策", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-3"}, Materialized: true},
		{ID: "candidate-6d104a91f1c2", Kind: "topic", ParentID: treeRootNodeID, Label: "運用プロセスの標準化", Origin: topicOriginDynamic},
		{ID: "candidate-98301c302f8b", Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginDynamic},
		{ID: "candidate-cefa0e520723", Kind: "topic", ParentID: treeRootNodeID, Label: "監視強化と設計見直し", Origin: topicOriginDynamic},
		{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: treeUnclassifiedTopicLabel, Origin: topicOriginSystem},
	}
	parents := map[string]string{
		"item-todo-e07c19f5d764":             "candidate-6d104a91f1c2",
		"decision-auto-214fc3e3f79b":         "candidate-6d104a91f1c2",
		"item-todo-89517b0a037e":             "candidate-cefa0e520723",
		"item-issue-discussion-1d3dd6e0a0e9": "candidate-cefa0e520723",
		"item-risk-1b09fe292026":             "candidate-cefa0e520723",
		"item-risk-feb31492a4f1":             "candidate-98301c302f8b",
		"item-risk-5d2472d3ccae":             treeUnclassifiedTopicID,
		"item-todo-c3d48c965d89":             "candidate-cefa0e520723",
	}
	for _, item := range items {
		nodes = append(nodes, liveAnalysisTreeNode{ID: item.ID, Kind: item.Kind, Subtype: item.Subtype, ParentID: parents[item.ID], Label: item.Title, Description: item.Body, Status: item.Status})
	}
	tree := &liveAnalysisTree{Nodes: nodes}
	rebuildTreeAuditEdges(tree)
	return liveAnalysisPayload{Summary: "名古屋支社ネットワーク障害の振り返り", CurrentTopic: "VPN証明書", Items: items, Tree: tree, TreeVersion: 17, CoveredThroughSequenceNo: 33}
}

func session5b7b78256ab026faTranscript() map[int64]string {
	lines := []string{
		"それには、名古屋支社で発生したネットワーク障害について振り返ります。",
		"本日午前9時20分ごろ、名古屋支社の3階を中心に社内ネットワークへ接続できないという報告がありました。",
		"当初は3回だけの障害だと考えていましたが、正確には2階の一部でも通信遅延が発生していました。",
		"影響を受けたのは、有線LAN、車内無線LAN、ファイルサーバー、社内設備への接続です。",
		"インターネットが完全に停止したわけではなく、接続できる端末を接続できない端末が混在していました。",
		"障害発生後、最初にルーターとファイアウォールを確認しましたが、どちらにも明確な異常はありませんでした。",
		"その後、前日の夜に交換した3階のアクセススイッチを確認したところ、 v段20棟、 v段30の通信が不安定になっていました。",
		"で、正確には完全なアクセスポート設定ではありません。トランク設定自体は入っていましたが、許可するvランの一覧からvラン30が漏れていました。",
		"現時点では、この設定漏れがちょ障害の直接原因である可能性が最も高いと考えています。ただし、 2階で発生した通信遅延まで、このvラン設定だけで説明できるかは確認できていません。この点は未解決の調査事項として残します。",
		"復旧対応としては、午前9時52分に旧スイッチへ一度切り戻し、その後、新しいスイッチのトランク設定と初株イランを修正しました。",
		"午前10時5分に有線LAN、無線LANファイルサーバへの接続が正常になったことを確認しています。",
		"今後の対応についてです。",
		"まず、ネットワーク機器を交換する際は、作業者とは別の担当者が。",
		"設定内容を確認するダブルチェックを必須にします。",
		"また、交換前後でvランごとの疎通確認を実施するチェックリストを作成します。",
		"の運用を次回の機器交換から適用することにします。",
		"私が今週金曜日までにスイッチ交換用のチェックリスト案を作成します。佐藤さんには、来週火曜日までに今回のスイッチ設定と標準設定との差分を確認してもらいます。",
		"さらに、 vランごとの通信欄を早期に検知できるよう。",
		"監視項目へvラン単位の共通確認を追加する案もあります。",
		"ただし、感謝対象を増やすとアラートが多くなりすぎる可能性があります。",
		"監視感覚と通知条件については、次回までに検討が必要です。",
		"ここで、アジェンダにはなかった別の問題があります。",
		"今回ログを確認したところ、 VPN装置の証明書が来月末に期限切れになることが分かりました。",
		"今回のC社ネットワーク障害とは直接関係ありませんが、放置するとリモート接続ができなくなる可能性があります。",
		"VPN証明書の更新は、今回のv難証書とは別の新しい対応事項として管理します。",
		"小林さんに今週中に証明書の更新手順と作業可能日を確認してもらいます。",
		"最後に、ここまでをまとめます。今回の障害は、交換したアクセススイッチでvラン30の許可設定が漏れていたことが主な原因と考えられます。",
		"復旧のため、九スイッチへの切り戻しと新しいスイッチのトランク設定修正を実施しました。",
		"再発防止として、設定のダブルチェックとvランごとの疎通確認を別にします。",
		"私は今週金曜日までにチェックリスト案を作成し、佐藤さんは来週火曜日までに標準設定との差分を確認します。",
		"2回の通信遅延の原因と監視アラートの条件は、未解決事項として残します。",
		"VPNの証明書の更新は、今回の紹介とは別の新しい対応事項として進めます。",
		"以上で振り返りを終了します。",
	}
	result := make(map[int64]string, len(lines))
	for index, line := range lines {
		result[int64(index+1)] = strings.TrimSpace(line)
	}
	return result
}
