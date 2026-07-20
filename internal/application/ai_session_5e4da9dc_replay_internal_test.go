package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

// This fixture is a compact, offline reproduction of the persisted defects
// observed in session_5e4da9dc40d50940 (名古屋支社ネットワーク障害の振り返り会議):
// two near-duplicate VLAN issue siblings that the pre-existing dedup rules
// missed, a missing risk extraction for explicit future-adverse-impact
// wording, and a recovery statement that should close the VLAN
// issue/connectivity item without touching the root-cause investigation or
// the certificate-renewal todo. No provider or database access is performed.
func TestSession5e4da9dc40d50940OfflineQualityReplay(t *testing.T) {
	mc := &meetingContext{Title: "名古屋支社ネットワーク障害の振り返りと再発防止会議", Agenda: []agendaItem{
		{ID: "agenda-1", Title: "障害の影響範囲と発生時刻", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "原因調査と復旧対応", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "再発防止策", Order: 3, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "未解決事項と次回までの対応確認", Order: 4, Role: agendaRoleActionSummary},
	}}
	items := []liveAnalysisItem{
		{ID: "item-issue-discussion-39aa3681095d", Kind: "issue", Subtype: issueSubtypeDiscussion, Severity: "high",
			Title:  "発生時刻と影響範囲の拡張確認",
			Body:   "3階だけでなく2階の一部にも通信遅延・障害があり、影響を受けたのは有線LAN、車内無線LAN、ファイルサーバー、社内説明への接続。関連する監視ログの不足を踏まえ、原因確定には追加調査が必要。",
			Status: "open", EvidenceSequenceNos: []int64{1, 2, 3}},
		{ID: "item-issue-discussion-8a91d2b7edb2", Kind: "issue", Subtype: issueSubtypeDiscussion, Severity: "high",
			Title:  "3階アクセススイッチのVLAN設定不一致",
			Body:   "前日の夜に交換した3階のアクセススイッチのVLAN20とVLAN30の通信が不安定。正しいトランク設定と上位機器との接続形態の再確認が必要。",
			Status: "open", EvidenceSequenceNos: []int64{6}},
		{ID: "item-issue-discussion-598289c07dd0", Kind: "issue", Subtype: issueSubtypeDiscussion, Severity: "high",
			Title:  "3階スイッチVLAN設定不一致の再確認",
			Body:   "3階スイッチのVLAN設定不一致が障害原因候補として挙がっている。現状、再現性と監視ログの不足から確定には至っていない。追加の監視と設定チェックを行う必要あり。",
			Status: "open", EvidenceSequenceNos: []int64{8}},
		{ID: "item-issue-investigation-c4a8496b7ca2", Kind: "issue", Subtype: issueSubtypeInvestigation, Severity: "medium",
			Title:  "インターネット接続状況の混在と影響範囲の再整理",
			Body:   "インターネット接続状況が混在しており、影響範囲の再整理が必要。",
			Status: "open", EvidenceSequenceNos: []int64{4, 5}},
		{ID: "item-issue-investigation-60fc3a3fa0da", Kind: "issue", Subtype: issueSubtypeInvestigation, Severity: "medium",
			Title:  "ブイラン(VLAN)別の疎通監視案の検討",
			Body:   "VLAN別の疎通監視案を検討する。",
			Status: "open", EvidenceSequenceNos: []int64{17, 18}},
		{ID: "item-todo-352ec0de092a", Kind: "todo", Severity: "medium",
			Title:  "証明書更新の手順と作業可能日を確定",
			Body:   "VPN証明書更新の手順と作業可能日を確定する。",
			Status: "open", EvidenceSequenceNos: []int64{22}},
	}
	containers := []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: mc.Title, Origin: topicOriginSystem},
		{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: mc.Agenda[0].Title, Origin: topicOriginAgenda},
		{ID: "agenda-2", Kind: "topic", ParentID: treeRootNodeID, Label: mc.Agenda[1].Title, Origin: topicOriginAgenda},
		{ID: "agenda-3", Kind: "topic", ParentID: treeRootNodeID, Label: mc.Agenda[2].Title, Origin: topicOriginAgenda},
		{ID: "agenda-4", Kind: "topic", ParentID: treeRootNodeID, Label: mc.Agenda[3].Title, Origin: topicOriginAgenda, AgendaRole: agendaRoleActionSummary},
		{ID: "candidate-636123661622", Kind: "topic", ParentID: "agenda-2", Label: "スイッチ設定とネットワークトポロジー確認", Origin: topicOriginDynamic},
	}
	parents := map[string]string{
		"item-issue-discussion-39aa3681095d":    "agenda-1",
		"item-issue-discussion-8a91d2b7edb2":    "candidate-636123661622",
		"item-issue-discussion-598289c07dd0":    "candidate-636123661622",
		"item-issue-investigation-c4a8496b7ca2": "agenda-1",
		"item-issue-investigation-60fc3a3fa0da": "agenda-3",
		"item-todo-352ec0de092a":                "agenda-3",
	}
	tree := &liveAnalysisTree{Nodes: append([]liveAnalysisTreeNode(nil), containers...)}
	for i := range items {
		items[i].ClassificationStatus = classificationAssigned
		tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: items[i].ID, Kind: liveAnalysisTreeNodeKindForItem(items[i].Kind), ParentID: parents[items[i].ID], Label: items[i].Title, Status: items[i].Status})
	}
	rebuildTreeAuditEdges(tree)
	state := liveAnalysisPayload{Summary: "名古屋支社ネットワーク障害の振り返り", Items: items, Tree: tree, TreeVersion: 12, CoveredThroughSequenceNo: 11}
	previous, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	texts := map[int64]string{
		6:  "その後、前日の夜に交換した3階のアクセススイッチを確認したところ、vラン20とvラン30の通信が不安定になっていました。",
		8:  "いえ、正確には完全なアクセスポート設定ではありません。トランク設定自体は入っていましたが、許可するvランの一覧からvラン30が漏れていました。",
		9:  "現時点では、この設定漏れが障害の直接原因である可能性が最も高いと考えています。",
		12: "復旧対応としては、午前9時52分に旧スイッチ一度切り戻し、その後新しいスイッチのトランク設定と許可v欄を修正しました。午前10時5分に有線LAN無線ランファイルサーバーへの接続が正常になったことを確認しています。",
		18: "ただし、間接対象を増やすとアラートが多くなりすぎるという可能性があります。監視間隔と通知条件については、次回までに検討が必要です。",
		21: "今回の支社ネットワーク障害とは直接関係ありませんが、放置するとリモート接続ができなくなる可能性があります。VPN証明書の更新は、今回のvラン障害とは別の新しい対応事項として管理します。",
		22: "高橋さんに今週中に証明書の更新手順と作業可能日を。ええ更新。ええ、確認してもらいます?",
		25: "2階の通信遅延の原因と監視アラートの条件は、未解決事項として残します。",
	}
	scope := liveEvidenceScope{
		Allowed:        map[int64]struct{}{},
		CurrentRound:   map[int64]struct{}{12: {}, 18: {}, 21: {}, 25: {}},
		TranscriptText: texts,
		Segments:       map[int64]domain.TranscriptSegment{},
		CoveredThrough: 25,
	}
	for sequenceNo, text := range texts {
		scope.Allowed[sequenceNo] = struct{}{}
		scope.Segments[sequenceNo] = domain.TranscriptSegment{SequenceNo: sequenceNo, SpeakerID: "speaker-1", Text: text, IsFinal: true}
	}

	diff := `{"summary":"名古屋支社ネットワーク障害の振り返り(続き)","currentTopic":"","resolvedIds":[],"resolutionUpdates":[],"items":[],"newTopics":[],"assignments":[]}`
	stats := &liveAnalysisTreeMergeStats{}
	replayed, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, previous, mc, 13, []int64{12, 18, 21, 25}, scope, TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}, stats)
	if err != nil {
		t.Fatal(err)
	}
	after := previousLiveAnalysisState(replayed)

	// 1. VLAN sibling dedup: exactly one surviving VLAN item, evidence union
	// of 6 and 8, tombstone recorded with the sibling-specific source.
	vlanItems := 0
	var survivor liveAnalysisItem
	for _, item := range after.Items {
		if item.Kind != "issue" || item.Subtype != issueSubtypeDiscussion {
			continue
		}
		if strings.Contains(item.Title+item.Body, "VLAN") || strings.Contains(item.Title+item.Body, "vラン") {
			vlanItems++
			survivor = item
		}
	}
	if vlanItems != 1 {
		t.Fatalf("expected exactly 1 surviving VLAN item after sibling dedup, got %d items=%+v", vlanItems, after.Items)
	}
	hasSix, hasEight := false, false
	for _, sequenceNo := range survivor.EvidenceSequenceNos {
		if sequenceNo == 6 {
			hasSix = true
		}
		if sequenceNo == 8 {
			hasEight = true
		}
	}
	if !hasSix || !hasEight {
		t.Fatalf("expected surviving VLAN item evidence to include 6 and 8, got %v", survivor.EvidenceSequenceNos)
	}
	siblingTombstoneFound := false
	for _, tombstone := range after.ItemTombstones {
		if tombstone.CreatedBy == "sibling_semantic_dedup" {
			siblingTombstoneFound = true
		}
	}
	if !siblingTombstoneFound {
		t.Fatalf("expected a sibling_semantic_dedup tombstone, got %+v", after.ItemTombstones)
	}
	if stats.SiblingDuplicateItemsMerged < 1 {
		t.Fatalf("expected SiblingDuplicateItemsMerged >= 1, got %d", stats.SiblingDuplicateItemsMerged)
	}

	// 2. Risk extraction: 1-2 synthesized risk items, semantically about the
	// alert-volume or connectivity concern raised at seq18/seq21.
	riskItems := make([]liveAnalysisItem, 0, 2)
	for _, item := range after.Items {
		if item.Kind == "risk" {
			riskItems = append(riskItems, item)
		}
	}
	if len(riskItems) < 1 || len(riskItems) > 2 {
		t.Fatalf("expected 1-2 risk items, got %d items=%+v", len(riskItems), riskItems)
	}
	riskSubjectFound := false
	for _, item := range riskItems {
		if strings.Contains(item.Title+item.Body, "アラート") || strings.Contains(item.Title+item.Body, "接続") {
			riskSubjectFound = true
		}
	}
	if !riskSubjectFound {
		t.Fatalf("expected at least one risk item about alert volume or connectivity, got %+v", riskItems)
	}

	// 3. Recovery closure resolves the VLAN issue.
	resolvedIssueCount := 0
	for _, item := range after.Items {
		if item.Kind == "issue" && item.Status == "resolved" {
			resolvedIssueCount++
		}
	}
	if resolvedIssueCount != 1 {
		t.Fatalf("expected resolvedIssueCount == 1 (only the surviving VLAN item), got %d items=%+v", resolvedIssueCount, after.Items)
	}
	survivorAfterResolution := itemByID(after.Items, survivor.ID)
	if survivorAfterResolution == nil || survivorAfterResolution.Status != "resolved" {
		t.Fatalf("expected surviving VLAN item %s to be resolved by the recovery closure, got %+v", survivor.ID, survivorAfterResolution)
	}

	// 3b. Over-resolution regression guard: item-issue-discussion-39aa3681095d
	// ("発生時刻と影響範囲の拡張確認", 2階を含む影響範囲) explicitly says its own
	// root cause still needs additional investigation ("原因確定には追加調査が
	// 必要") and the same subject is reiterated as unresolved at seq25. The
	// seq12 recovery closure must not resolve it even though its subject
	// (LAN/wired connectivity) is loosely similar to the recovery statement.
	extendedImpactItem := itemByID(after.Items, "item-issue-discussion-39aa3681095d")
	if extendedImpactItem == nil {
		t.Fatalf("item-issue-discussion-39aa3681095d lost")
	}
	if extendedImpactItem.Status == "resolved" {
		t.Fatalf("item-issue-discussion-39aa3681095d must not be auto-resolved by the recovery closure (its own body states root-cause confirmation still needs additional investigation): %+v", extendedImpactItem)
	}

	// 4. Investigation items stay open: recovery != root-cause resolution.
	for _, id := range []string{"item-issue-investigation-c4a8496b7ca2", "item-issue-investigation-60fc3a3fa0da"} {
		investigationItem := itemByID(after.Items, id)
		if investigationItem == nil {
			t.Fatalf("investigation item %s lost", id)
		}
		if investigationItem.Status == "resolved" {
			t.Fatalf("investigation item %s must not be auto-resolved by a recovery closure: %+v", id, investigationItem)
		}
	}

	// 5. Todo item is not lost and stays open.
	todoItem := itemByID(after.Items, "item-todo-352ec0de092a")
	if todoItem == nil {
		t.Fatalf("todo item-todo-352ec0de092a lost")
	}
	if todoItem.Status != "open" {
		t.Fatalf("expected item-todo-352ec0de092a to remain open, got status=%s", todoItem.Status)
	}

	// 6. Tree integrity.
	diagnostics := validateTreeIntegrity(after.Tree, after.Items, mc)
	if !diagnostics.Valid {
		t.Fatalf("post-replay integrity invalid: %+v", diagnostics)
	}
	if len(diagnostics.EmptyAgendaTopicIDs) != 0 {
		t.Fatalf("unexpected EmptyAgendaTopicIDs: %v", diagnostics.EmptyAgendaTopicIDs)
	}
	if len(diagnostics.AgendaTopicIDCollisions) != 0 {
		t.Fatalf("unexpected AgendaTopicIDCollisions: %v", diagnostics.AgendaTopicIDCollisions)
	}
	if len(diagnostics.UnknownAgendaRefs) != 0 {
		t.Fatalf("unexpected UnknownAgendaRefs: %v", diagnostics.UnknownAgendaRefs)
	}
	if len(diagnostics.OrphanMaterializedTopicIDs) != 0 {
		t.Fatalf("unexpected OrphanMaterializedTopicIDs: %v", diagnostics.OrphanMaterializedTopicIDs)
	}

	// 7. Tree health does not need reorganization.
	health := computeTreeHealth(after.Tree)
	if health.needsReorganization() {
		t.Fatalf("expected tree to not need reorganization, reasons=%v health=%s", health.reorganizationReasons(), health.String())
	}

	riskTitles := make([]string, 0, len(riskItems))
	for _, item := range riskItems {
		riskTitles = append(riskTitles, item.ID+":"+item.Title)
	}
	t.Logf("session_5e4da9dc40d50940 replay: survivor=%s evidence=%v resolvedIssueCount=%d riskItems=%v siblingDuplicateItemsMerged=%d riskItemsSynthesized=%d",
		survivor.ID, survivor.EvidenceSequenceNos, resolvedIssueCount, riskTitles, stats.SiblingDuplicateItemsMerged, stats.RiskItemsSynthesized)
}
