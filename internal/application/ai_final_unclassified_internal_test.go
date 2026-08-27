package application

import (
	"sort"
	"strings"
	"testing"
)

// 名古屋支社ネットワーク障害の振り返り会議(session_5de87bd0e121089e)で観測した
// 「関連する追加論点が統合されず、追加論点の箱の中で孤立する」欠陥の再現。
// 同じVPN証明書対応が Fact / Risk / Decision / Todo に分かれ、話者と発話位置が
// 離れているため live 経路の discourse 条件では束ねられない。
func unclassifiedVPNCertificateState() liveAnalysisPayload {
	items := []liveAnalysisItem{
		{
			ID: "item-fact-cert", Kind: "fact", Title: "VPN装置の証明書が来月末に期限切れになる",
			Body: "VPN装置の証明書が来月末に期限切れになる", Status: "open", Severity: "medium",
			ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-cert-a",
			EvidenceSequenceNos: []int64{31}, GroundingDecision: "accepted",
			InformationStatus: informationStatusGrounded,
		},
		{
			ID: "item-risk-cert", Kind: "risk", Title: "証明書を放置するとリモート接続が不能になる",
			Body: "証明書を放置するとリモート接続が不能になる恐れがある", Status: "open", Severity: "high",
			ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-cert-b",
			EvidenceSequenceNos: []int64{33}, GroundingDecision: "accepted",
			InformationStatus: informationStatusGrounded,
		},
		{
			ID: "item-decision-cert", Kind: "decision", Title: "VPN装置の証明書更新は別チケットで管理する",
			Body: "VPN装置の証明書更新は別チケットで管理する", Status: "open", Severity: "medium",
			ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-cert-c",
			EvidenceSequenceNos: []int64{37}, GroundingDecision: "accepted",
			InformationStatus: informationStatusGrounded,
		},
		{
			ID: "item-todo-cert", Kind: "todo", Title: "田中が証明書の更新手順と作業日を確認する",
			Body: "田中が証明書の更新手順と作業日を今週中に確認する", Status: "open", Severity: "medium",
			ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-cert-d",
			EvidenceSequenceNos: []int64{40}, GroundingDecision: "accepted",
			InformationStatus: informationStatusGrounded,
		},
		// negative: 同じ「接続」という語を含むが別問題(拠点間回線)。
		{
			ID: "item-issue-line", Kind: "issue", Title: "名古屋と本社の拠点間回線の接続が不安定",
			Body: "名古屋と本社の拠点間回線の接続が不安定な時間帯がある", Status: "open", Severity: "medium",
			ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-line",
			EvidenceSequenceNos: []int64{34}, GroundingDecision: "accepted",
			InformationStatus: informationStatusGrounded,
		},
		// negative: sequenceが近いだけの別論点(会議室の備品)。
		{
			ID: "item-issue-room", Kind: "issue", Title: "第二会議室のプロジェクタが映らない",
			Body: "第二会議室のプロジェクタが映らないことがある", Status: "open", Severity: "low",
			ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-room",
			EvidenceSequenceNos: []int64{38}, GroundingDecision: "accepted",
			InformationStatus: informationStatusGrounded,
		},
	}
	nodes := []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "名古屋支社ネットワーク障害の振り返りと再発防止会議", Origin: topicOriginSystem},
		{
			ID: "topic-agenda-outage", Kind: "topic", ParentID: treeRootNodeID,
			Label: "障害の経緯と原因", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"},
			Materialized: true,
		},
		{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginSystem},
	}
	for _, item := range items {
		nodes = append(nodes, liveAnalysisTreeNode{
			ID: item.ID, Kind: item.Kind, ParentID: treeUnclassifiedTopicID,
			Label: item.Title, Description: item.Body,
		})
	}
	candidates := []emergingTopicCandidate{
		{ID: "candidate-cert-a", Label: "証明書の期限", EvidenceItemIDs: []string{"item-fact-cert"}, FirstRound: 12, LastRound: 12, RoundCount: 1},
		{ID: "candidate-cert-b", Label: "接続不能リスク", EvidenceItemIDs: []string{"item-risk-cert"}, FirstRound: 13, LastRound: 13, RoundCount: 1},
		{ID: "candidate-cert-c", Label: "証明書更新の管理", EvidenceItemIDs: []string{"item-decision-cert"}, FirstRound: 15, LastRound: 15, RoundCount: 1},
		{ID: "candidate-cert-d", Label: "更新手順の確認", EvidenceItemIDs: []string{"item-todo-cert"}, FirstRound: 16, LastRound: 16, RoundCount: 1},
		{ID: "candidate-line", Label: "拠点間回線", EvidenceItemIDs: []string{"item-issue-line"}, FirstRound: 14, LastRound: 14, RoundCount: 1},
		{ID: "candidate-room", Label: "会議室の備品", EvidenceItemIDs: []string{"item-issue-room"}, FirstRound: 15, LastRound: 15, RoundCount: 1},
	}
	tree := &liveAnalysisTree{Nodes: nodes}
	rebuildTreeAuditEdges(tree)
	return liveAnalysisPayload{Items: items, Tree: tree, EmergingTopics: candidates, TreeVersion: 26}
}

func topicIDForItem(state liveAnalysisPayload, itemID string) string {
	return treeItemTopic(state.Tree, itemID)
}

func dynamicTopicIDs(state liveAnalysisPayload) []string {
	ids := make([]string, 0, 4)
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" && node.ID != treeRootNodeID && node.ID != treeUnclassifiedTopicID &&
			deriveTopicOrigin(node) == topicOriginDynamic {
			ids = append(ids, node.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func itemByIDForTest(state liveAnalysisPayload, itemID string) liveAnalysisItem {
	for _, item := range state.Items {
		if item.ID == itemID {
			return item
		}
	}
	return liveAnalysisItem{}
}

func TestFinalUnclassifiedRepairMergesOneAdditionalSubjectIntoOneDynamicTopic(t *testing.T) {
	state := unclassifiedVPNCertificateState()
	stats := &finalRepairStats{}

	repairFinalUnclassifiedItems(&state, nil, 26, stats)

	certificateItems := []string{"item-fact-cert", "item-risk-cert", "item-decision-cert", "item-todo-cert"}
	topicID := topicIDForItem(state, certificateItems[0])
	if topicID == "" || topicID == treeUnclassifiedTopicID {
		t.Fatalf("certificate fact was not moved out of the staging topic: %q", topicID)
	}
	for _, itemID := range certificateItems {
		if got := topicIDForItem(state, itemID); got != topicID {
			t.Fatalf("%s joined %q instead of the shared additional topic %q", itemID, got, topicID)
		}
		item := itemByIDForTest(state, itemID)
		if item.ClassificationStatus != classificationAssigned {
			t.Fatalf("%s remained %q after materialization", itemID, item.ClassificationStatus)
		}
	}

	if ids := dynamicTopicIDs(state); len(ids) != 1 || ids[0] != topicID {
		t.Fatalf("expected exactly one materialized dynamic topic, got %v", ids)
	}
	if stats.UnclassifiedTopicsMaterialized != 1 {
		t.Fatalf("materialized topic count = %d, want 1", stats.UnclassifiedTopicsMaterialized)
	}
	if stats.UnclassifiedItemsReparented != len(certificateItems) {
		t.Fatalf("reparented item count = %d, want %d", stats.UnclassifiedItemsReparented, len(certificateItems))
	}

	// 論点全体を表すtopic名であること: 特定item一件の文章でも、活用途中で
	// 切れた断片でもない。
	topic := liveTreeNodeByID(state.Tree, topicID)
	if topic == nil {
		t.Fatalf("materialized topic %q is missing from the tree", topicID)
	}
	if strings.Contains(topic.Label, "来月末") || topic.Label == itemByIDForTest(state, "item-fact-cert").Title {
		t.Fatalf("dynamic topic label copied a single item sentence: %q", topic.Label)
	}
	if !strings.Contains(topic.Label, "証明書") {
		t.Fatalf("dynamic topic label lost the shared business object: %q", topic.Label)
	}
	if dynamicTopicLabelNeedsRepair(topic.Label, topic.Label) {
		t.Fatalf("dynamic topic label is grammatically truncated: %q", topic.Label)
	}
	if topic.ParentID != treeRootNodeID {
		t.Fatalf("materialized topic parent = %q, want root", topic.ParentID)
	}
}

func TestFinalUnclassifiedRepairDoesNotMergeUnrelatedAdditionalPoints(t *testing.T) {
	state := unclassifiedVPNCertificateState()
	stats := &finalRepairStats{}

	repairFinalUnclassifiedItems(&state, nil, 26, stats)

	certificateTopic := topicIDForItem(state, "item-fact-cert")
	for _, itemID := range []string{"item-issue-line", "item-issue-room"} {
		if got := topicIDForItem(state, itemID); got == certificateTopic {
			t.Fatalf("%s was merged into the certificate topic on a shared word alone", itemID)
		}
	}
	// 同じ話者・近接sequenceだけでは束ねない。
	if topicIDForItem(state, "item-issue-line") == topicIDForItem(state, "item-issue-room") &&
		topicIDForItem(state, "item-issue-line") != "" &&
		topicIDForItem(state, "item-issue-line") != treeUnclassifiedTopicID {
		t.Fatalf("two unrelated additional points were merged into one topic")
	}
}

func TestFinalUnclassifiedRepairLeavesNoRelatedOrphanUnderStagingTopic(t *testing.T) {
	state := unclassifiedVPNCertificateState()
	stats := &finalRepairStats{}

	repairFinalUnclassifiedItems(&state, nil, 26, stats)

	// 同じ追加論点に属するitemは1件も追加論点の箱へ残らない。
	for _, itemID := range []string{"item-fact-cert", "item-risk-cert", "item-decision-cert", "item-todo-cert"} {
		if got := topicIDForItem(state, itemID); got == treeUnclassifiedTopicID {
			t.Fatalf("%s is still isolated under the staging topic", itemID)
		}
	}
	for _, item := range state.Items {
		if item.ClassificationStatus == classificationAssigned &&
			topicIDForItem(state, item.ID) == "" {
			t.Fatalf("%s became an orphan without a topic ancestor", item.ID)
		}
	}
	// topic名を作れない単独論点は、非表示化せずそのまま残す(recall優先)。
	for _, itemID := range []string{"item-issue-line", "item-issue-room"} {
		item := itemByIDForTest(state, itemID)
		if item.ID == "" {
			t.Fatalf("%s was dropped from the final items", itemID)
		}
		if item.Title == "" {
			t.Fatalf("%s lost its proposition text", itemID)
		}
	}
	if stats.UnclassifiedItemsRetained != 2 {
		t.Fatalf("retained item count = %d, want 2", stats.UnclassifiedItemsRetained)
	}
}

func TestFinalUnclassifiedRepairIsIdempotent(t *testing.T) {
	state := unclassifiedVPNCertificateState()
	first := &finalRepairStats{}
	repairFinalUnclassifiedItems(&state, nil, 26, first)
	topicsAfterFirst := dynamicTopicIDs(state)

	second := &finalRepairStats{}
	repairFinalUnclassifiedItems(&state, nil, 27, second)

	if got := dynamicTopicIDs(state); len(got) != len(topicsAfterFirst) {
		t.Fatalf("second pass changed the dynamic topic set: %v -> %v", topicsAfterFirst, got)
	}
	if second.UnclassifiedTopicsMaterialized != 0 || second.UnclassifiedItemsReparented != 0 {
		t.Fatalf("second pass was not a no-op: %+v", second)
	}
}

func TestFinalUnclassifiedRepairGroundsIntoAnExistingAgendaTopic(t *testing.T) {
	state := unclassifiedVPNCertificateState()
	// 既存アジェンダtopicが同じ具体的対象を扱っている場合は、新規topicを作らず
	// そこへ接地する。
	for index := range state.Tree.Nodes {
		if state.Tree.Nodes[index].ID == "topic-agenda-outage" {
			state.Tree.Nodes[index].Label = "VPN証明書の期限切れ対応"
			state.Tree.Nodes[index].Description = "VPN装置の証明書の期限切れと更新対応"
		}
	}
	stats := &finalRepairStats{}
	repairFinalUnclassifiedItems(&state, nil, 26, stats)

	if got := topicIDForItem(state, "item-fact-cert"); got != "topic-agenda-outage" {
		t.Fatalf("certificate fact did not ground into the existing topic: %q", got)
	}
	if len(dynamicTopicIDs(state)) != 0 {
		t.Fatalf("a redundant dynamic topic was materialized: %v", dynamicTopicIDs(state))
	}
	if stats.UnclassifiedItemsReparented == 0 {
		t.Fatalf("no item was reparented into the existing topic")
	}
}

func TestFinalUnclassifiedRepairKeepsManuallyPlacedItems(t *testing.T) {
	state := unclassifiedVPNCertificateState()
	for index := range state.Tree.Nodes {
		if state.Tree.Nodes[index].ID == "item-fact-cert" {
			state.Tree.Nodes[index].LastParentChangeSource = "user"
		}
	}
	stats := &finalRepairStats{}
	repairFinalUnclassifiedItems(&state, nil, 26, stats)

	if got := topicIDForItem(state, "item-fact-cert"); got != treeUnclassifiedTopicID {
		t.Fatalf("manually placed item was moved by the deterministic repair: %q", got)
	}
}
