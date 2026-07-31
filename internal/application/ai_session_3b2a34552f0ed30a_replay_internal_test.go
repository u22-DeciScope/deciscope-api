package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestSession3b2a34552f0ed30aClassificationReplay(t *testing.T) {
	segments := session3b2a34552f0ed30aSegments()
	state := session3b2a34552f0ed30aCorruptedState()
	before := activeKindCounts(state.Items)
	beforeHealth := computeTreeHealth(state.Tree)
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	repairedPayload, stats := applyDeterministicFinalTreeRepairs(
		payload, nil, 22,
		finalRepairInput{Segments: segments, Audit: TreeAuditConfig{}},
	)
	if stats.Error != "" || stats.IntegrityRejected {
		t.Fatalf("repair failed: %+v", stats)
	}
	repaired := previousLiveAnalysisState(repairedPayload)
	after := activeKindCounts(repaired.Items)
	for _, item := range repaired.Items {
		t.Logf(
			"item id=%s kind=%s inactive=%t mergedInto=%s evidence=%v title=%q",
			item.ID, item.Kind, item.Inactive, item.MergedIntoID,
			item.EvidenceSequenceNos, item.Title,
		)
	}

	oldPort, ok := itemByIDForSession3b2a(repaired.Items, "item-issue-investigation-f05ca646f9f7")
	if !ok || !oldPort.Inactive || oldPort.MergedIntoID != "item-fact-587776628a9a" {
		t.Fatalf("explicit correction did not supersede old port claim: %+v", oldPort)
	}
	assertSession3b2aActiveKindContainsEvidence(t, repaired.Items, "fact", 11)
	assertSession3b2aKindAndEvidence(
		t, repaired.Items, "item-risk-e65c0665f722", "fact", []int64{19},
	)
	assertSession3b2aKindAndEvidence(
		t, repaired.Items, "item-risk-47a51fe14972", "risk", []int64{19},
	)
	assertSession3b2aKindAndEvidence(
		t, repaired.Items, "item-todo-c17b2307bf05", "todo", []int64{21},
	)
	assertSession3b2aKindAndEvidence(
		t, repaired.Items, "item-todo-538a705a91ba", "issue", []int64{16},
	)

	issueFound := false
	for _, item := range repaired.Items {
		if item.Inactive || item.MergedIntoID != "" || item.Kind != "issue" {
			continue
		}
		if containsInt64(item.EvidenceSequenceNos, 10) &&
			strings.Contains(item.Title+" "+item.Body, "確認できていません") {
			issueFound = true
			break
		}
	}
	if !issueFound {
		t.Fatalf("sequence 10 open issue was not preserved separately: items=%+v", repaired.Items)
	}

	for _, item := range repaired.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		if item.Kind == "todo" && containsInt64(item.EvidenceSequenceNos, 11) {
			t.Fatalf("completed recovery work remained Todo: %+v", item)
		}
		if item.Kind == "todo" && item.ID == "item-risk-e65c0665f722" {
			t.Fatalf("certificate expiry state remained Todo: %+v", item)
		}
		if item.ClassificationStatus == classificationTentative {
			t.Fatalf("unexpected tentative active item: %+v", item)
		}
	}

	integrity := validateTreeIntegrity(repaired.Tree, repaired.Items, nil)
	if !integrity.Valid {
		t.Fatalf("tree integrity failed: %+v", integrity)
	}
	afterHealth := computeTreeHealth(repaired.Tree)
	activeItems, activeNodes := 0, 0
	for _, item := range repaired.Items {
		if !item.Inactive && item.MergedIntoID == "" {
			activeItems++
		}
	}
	for _, node := range repaired.Tree.Nodes {
		if node.Kind != "topic" && node.Kind != "group" {
			activeNodes++
		}
	}
	if activeItems != activeNodes {
		t.Fatalf("tree/assistant visibility mismatch: activeItems=%d activeNodes=%d", activeItems, activeNodes)
	}

	t.Logf(
		"session_3b2a34552f0ed30a classification replay before=%v after=%v "+
			"kindChanges=%d semanticSplits=%d sameKindMerges=%d correctionSuperseded=%d "+
			"evidencePruned=%d issuesRecovered=%d activeItems=%d treeIntegrityValid=%t "+
			"needsReorganizationBefore=%t needsReorganizationAfter=%t reasonsAfter=%v",
		before, after, stats.KindValidationChanges, stats.KindSemanticSplits,
		stats.SameKindDuplicatesMerged, stats.CorrectionItemsSuperseded,
		stats.EvidenceReferencesPruned, stats.IssuesRecoveredFromTodoEvidence,
		activeItems, integrity.Valid, beforeHealth.needsReorganization(),
		afterHealth.needsReorganization(), afterHealth.reorganizationReasons(),
	)
}

func session3b2a34552f0ed30aCorruptedState() liveAnalysisPayload {
	items := []liveAnalysisItem{
		session3b2aItem("decision-auto-d698a2c763f3", "decision", "交換前後のブイランごとの疎通確認チェックリストの運用を次回の機器交換から運用する", "交換前後のブイランごとの疎通確認チェックリストの運用を次回の機器交換から運用することにします", 13),
		session3b2aItem("item-fact-2681bd3919b6", "fact", "影響範囲の再確認", "当初は3回の渉外とみていたが、実際には2回の一部で通信チェーンが発生。影響を受けた対象は有線LAN、車内無線LAN、ファイルサーバー、社内設備への接続。", 3),
		session3b2aItem("item-fact-587776628a9a", "fact", "トランク設定自体は入っていましたが、許可するvランの一覧からvラン30が漏れていた", "トランク設定自体は入っていましたが、許可するvランの一覧からvラン30が漏れていました", 8, 22),
		session3b2aItem("item-fact-75a56fb77016", "fact", "本日午前9時20分ごろ", "本日午前9時20分ごろ", 2),
		session3b2aItem("item-fact-e4eeabfeeabc", "fact", "接続復旧の確認", "午前10時5分に有線LAN、無線LAN、ファイルサーバへの接続が正常になったことを確認。", 12),
		session3b2aItem("item-fact-e9113b362d3a", "fact", "インターネット接続の混在状態", "障害はインターネットが完全停止したわけではなく、接続できる端末とできない端末が混在していた。", 4),
		session3b2aItem("item-issue-investigation-f05ca646f9f7", "issue", "交換したスイッチの上位接続ポートがアクセスポート", "交換したスイッチでは、上位スイッチへ接続するポートの設定が、本来のトランクポートではなくアクセスポートになっていました", 7),
		session3b2aItem("item-risk-46eb37948218", "risk", "監視対象を増やすとアラートが多くなりすぎる可能性", "監視対象を増やすとアラートが多くなりすぎる可能性があります", 18),
		session3b2aItem("item-risk-47a51fe14972", "risk", "放置するとリモート接続ができなくなる可能性", "今回の支社ネットワーク障害と直通関係ありませんが、放置するとリモート接続ができなくなる可能性があります", 19),
		session3b2aItem("issue-investigation-auto-7b15808aa786", "todo", "チェックリスト作成と標準設定差分の確認", "私は今週金曜日までにチェックリストアンを作成し、佐藤さんは来週火曜日までに標準設定との差分を確認します", 10, 24),
		session3b2aItem("item-risk-e65c0665f722", "todo", "VPN装置の証明書が来月末に期限切れ", "今回ログを確認したところ、VPN装置の証明書が来月末に期限切れになることがわかりました", 19),
		session3b2aItem("item-todo-48958bdeb8b0", "todo", "監視項目へvラン単位の疎通確認を追加する案", "監視項目へvラン単位の疎通確認を追加する案もあります", 17),
		session3b2aItem("item-todo-538a705a91ba", "todo", "vランごとの通信ダウンを早期に検知", "さらに、vランごとの通信ダウンを早期に検知できるように", 16),
		session3b2aItem("item-todo-5aa7352237e7", "todo", "復旧対応", "復旧対応としては、午前9時52分に旧スイッチへ一度切り戻し、その後、新しいスイッチのトランク設定と許可ブイランを修正しました", 11),
		session3b2aItem("item-todo-c17b2307bf05", "todo", "証明書更新手順と作業可能日を確認", "高橋さんに今週中に証明書の更新手順と作業可能日を確認してもらいます。", 19, 20, 21),
		session3b2aItem("item-todo-d27bb0beb2a3", "todo", "次回のスイッチ設定と標準設定との差分確認", "佐藤さんには、来週火曜日までに次回のスイッチ設定と標準設定との差分を確認してもらう。", 15),
		session3b2aItem("item-todo-d563f70fa4c2", "todo", "スイッチ交換用チェックリストの作成", "スイッチ交換用チェックリストの作成", 13, 14),
	}
	nodes := []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "名古屋支社ネットワーク障害の振り返りと再発防止"},
		{ID: "topic-agenda-a5f8fcd0c7a2", Kind: "topic", ParentID: treeRootNodeID, Label: "発生時刻と影響範囲"},
		{ID: "topic-agenda-64b761a79cc0", Kind: "topic", ParentID: treeRootNodeID, Label: "VLAN設定と接続状態"},
		{ID: "topic-agenda-7dd3ab9e5ea9", Kind: "topic", ParentID: treeRootNodeID, Label: "再発防止策"},
		{ID: "topic-dynamic-cd083251ae98", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN証明書期限"},
		{ID: "group-c244cc3549a6", Kind: "group", ParentID: "topic-agenda-a5f8fcd0c7a2", Label: "発生時刻と影響範囲の整理"},
		{ID: "group-2ef9ba5d4da6", Kind: "group", ParentID: "topic-agenda-64b761a79cc0", Label: "VLAN設定と接続混在の確認"},
		{ID: "group-101741c2740a", Kind: "group", ParentID: "topic-agenda-7dd3ab9e5ea9", Label: "交換時の確認と監視"},
		{ID: "group-647ae5168fd3", Kind: "group", ParentID: "topic-dynamic-cd083251ae98", Label: "VPN証明書の期限切れ対応"},
	}
	parentIDs := map[string]string{
		"decision-auto-d698a2c763f3":            "group-101741c2740a",
		"item-fact-2681bd3919b6":                "group-c244cc3549a6",
		"item-fact-587776628a9a":                "group-2ef9ba5d4da6",
		"item-fact-75a56fb77016":                "group-c244cc3549a6",
		"item-fact-e4eeabfeeabc":                "group-c244cc3549a6",
		"item-fact-e9113b362d3a":                "group-2ef9ba5d4da6",
		"item-issue-investigation-f05ca646f9f7": "topic-agenda-a5f8fcd0c7a2",
		"item-risk-46eb37948218":                "topic-agenda-7dd3ab9e5ea9",
		"item-risk-47a51fe14972":                "group-647ae5168fd3",
		"issue-investigation-auto-7b15808aa786": "topic-agenda-a5f8fcd0c7a2",
		"item-risk-e65c0665f722":                "group-647ae5168fd3",
		"item-todo-48958bdeb8b0":                "topic-agenda-7dd3ab9e5ea9",
		"item-todo-538a705a91ba":                "group-101741c2740a",
		"item-todo-5aa7352237e7":                "group-2ef9ba5d4da6",
		"item-todo-c17b2307bf05":                "group-647ae5168fd3",
		"item-todo-d27bb0beb2a3":                "group-101741c2740a",
		"item-todo-d563f70fa4c2":                "group-101741c2740a",
	}
	for _, item := range items {
		nodes = append(nodes, liveAnalysisTreeNode{
			ID: item.ID, Kind: item.Kind, Subtype: item.Subtype,
			ParentID: parentIDs[item.ID], Label: item.Title, Description: item.Body,
			Status: item.Status, CreatedAtVersion: 21, UpdatedAtVersion: 21,
		})
	}
	tree := &liveAnalysisTree{Nodes: nodes}
	rebuildTreeAuditEdges(tree)
	return liveAnalysisPayload{
		Summary: "実セッション分類再生", Items: items, Tree: tree,
		TreeVersion: 21, CoveredThroughSequenceNo: 24,
	}
}

func session3b2aItem(id, kind, title, body string, evidence ...int64) liveAnalysisItem {
	subtype := ""
	if kind == "issue" {
		subtype = issueSubtypeInvestigation
	}
	return liveAnalysisItem{
		ID: id, Kind: kind, Subtype: subtype, Severity: "medium",
		Title: title, Body: body, Status: "open",
		ClassificationStatus: classificationAssigned,
		AssignmentConfidence: 0.95, AssignmentSource: "model",
		EvidenceSequenceNos:          append([]int64(nil), evidence...),
		CreatedThroughSequenceNo:     21,
		InitialEvidenceMaxSequenceNo: maxInt64ForSession3b2a(evidence),
	}
}

func maxInt64ForSession3b2a(values []int64) int64 {
	var maximum int64
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

func activeKindCounts(items []liveAnalysisItem) map[string]int {
	counts := make(map[string]int)
	for _, item := range items {
		if !item.Inactive && item.MergedIntoID == "" {
			counts[item.Kind]++
		}
	}
	return counts
}

func itemByIDForSession3b2a(items []liveAnalysisItem, id string) (liveAnalysisItem, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return liveAnalysisItem{}, false
}

func assertSession3b2aKindAndEvidence(
	t *testing.T,
	items []liveAnalysisItem,
	id string,
	wantKind string,
	wantEvidence []int64,
) {
	t.Helper()
	item, ok := itemByIDForSession3b2a(items, id)
	if !ok || item.Inactive || item.MergedIntoID != "" ||
		item.Kind != wantKind || !equalInt64s(item.EvidenceSequenceNos, wantEvidence) {
		t.Fatalf("item %s = %+v, want kind=%s evidence=%v", id, item, wantKind, wantEvidence)
	}
}

func assertSession3b2aActiveKindContainsEvidence(
	t *testing.T,
	items []liveAnalysisItem,
	wantKind string,
	wantEvidence int64,
) {
	t.Helper()
	for _, item := range items {
		if !item.Inactive && item.MergedIntoID == "" &&
			item.Kind == wantKind && containsInt64(item.EvidenceSequenceNos, wantEvidence) {
			return
		}
	}
	t.Fatalf("no active %s contains evidence sequence %d: %+v", wantKind, wantEvidence, items)
}

func session3b2a34552f0ed30aSegments() []domain.TranscriptSegment {
	texts := []string{
		"それでは、名古屋支社で発生したネットワーク紹介について振り返ります。",
		"本日午前9時20分ごろ、名古屋支社の3階を中心に社内ネットワークへ接続できない報告がありました。",
		"当初は3回だけの渉外だと考えていましたが、正確には2回の一部でも通信チェーンが発生していました。影響を受けたのは、有線LAN、車内無線LAN、ファイルサーバー、社内設備への接続です。",
		"インターネットが完全に停止したわけではなく、接続できる端末を接続できない端末が混在していました。",
		"障害発生後、最初にルーターとファイアウォールを確認しましたが、どちらにも明確な異常はありませんでした。",
		"その後、前日の夜に公開した3階のアクセススイッチを確認したところ、vラ20とvラン30の通信が不安定になっていました。",
		"交換したスイッチでは、上位スイッチへ接続するポートの設定が、本来のトランクポートではなくアクセスポートになっていました。",
		"いえ、正確には完全なくスポット設定ではありません。トランク設定自体は入っていましたが、許可するvランの一覧からvラン30が漏れていました。",
		"現時点では、この設定漏れが障害に直接原因である可能性が最も高いとみています。",
		"ただし、2階で発生した通信チームまで、このvラン設定だけで説明できるかは確認できていません。この点は未解決の調査事項として残します。",
		"復旧対応としては、午前9時52分に旧スイッチへ一度切り戻し、その後、新しいスイッチのトランク設定と許可ブイランを修正しました。",
		"午前10時5分に有線LAN、無線LANファイルサーバへの接続が正常になったことを確認しています。",
		"今後の対応についてです。まず、ネットワーク機器を交換する際は調査ええ作業者とは別の担当者が設定内容を確認するじゃダブルチェックオフィスにします。また、交換前後でブイランごとの疎通確認を実施するチェックリストを作成します。この運用を次回の機器交換から運用することにします。",
		"私が今週金曜日までにスイッチ交換用のチェックリストを作成します。",
		"佐藤さんには、来週火曜日までに次回のスイッチ設定と標準設定との差分を確認してもらいます。",
		"さらに、vランごとの通信ダウンを早期に検知できるように。",
		"ええ。監視項目へvラン単位の疎通確認を追加する案もあります。",
		"ただし、監視対象を増やすとアラートが多くなりすぎる可能性があります。監視間隔と通知条件については、次回は全検討が必要です。",
		"ここで、アジェンダにはなかった別の問題があります。今回ログを確認したところ、VPN装置の証明書が来月末に期限切れになることがわかりました。今回の支社ネットワーク障害と直通関係ありませんが、放置するとリモート接続ができなくなる可能性があります。",
		"VPN証明書の更新は、今回のブイラン障害とは別の新しい対応事項として管理します。",
		"高橋さんに今週中に証明書の更新手順と作業可能日を確認してもらいます。",
		"最後に、ここまでをまとめます。今回の障害は、交換したアクセススイッチでvラン30の許可設定が漏れていたことが主な原因と考えられます。復旧のため、旧スイッチの切り戻しと新しいスイッチのトランク設定修正を実施しました。",
		"開発防止として、設定のダブルチェックとvラングごとの疎通確認オフィスにします。",
		"私は今週金曜日までにチェックリストアンを作成し、佐藤さんは来週火曜日までに標準設定との差分を確認します。2回の通信遅延の原因と監視アラートの条件は、未解決事項として残ちます。VPN証明書の今週は、今回の障害とは別の新しい対応事項として進めます。以上で振り返りを終了します。",
	}
	segments := make([]domain.TranscriptSegment, 0, len(texts))
	for index, text := range texts {
		segments = append(segments, finalSegment(int64(index+1), text))
	}
	return segments
}
