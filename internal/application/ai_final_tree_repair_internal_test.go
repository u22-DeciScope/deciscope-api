package application

import (
	"encoding/json"
	"testing"
)

// TestApplyDeterministicFinalTreeRepairsFoldsDuplicateTopicAndMergesCrossKindDuplicate
// reproduces the design background's fixture shape (W7.3): a promoted
// dynamic topic ("vpnと証明書の対応") duplicates another already-promoted
// dynamic topic's subject ("VPN装置証明書の期限切れ対策の検討"), and a group
// holds 4 children, two of which (a risk and a discussion issue) share the
// exact same single-sequence evidence and are clearly the same proposition.
// Before the deterministic repair pass, the tree needs reorganization
// (max_group_children); after both repairs it no longer does, and integrity
// still validates.
func TestApplyDeterministicFinalTreeRepairsFoldsDuplicateTopicAndMergesCrossKindDuplicate(t *testing.T) {
	previous := liveAnalysisPayload{
		Summary: "previous",
		Items: []liveAnalysisItem{
			{ID: "item-issue-dup", Kind: "issue", Subtype: issueSubtypeDiscussion, Severity: "medium", Title: "監視アラート過多の懸念", Body: "間接対象を増やすとアラートが多くなりすぎる可能性がある", Status: "open", EvidenceSequenceNos: []int64{16}},
			{ID: "item-risk-dup", Kind: "risk", Severity: "medium", Title: "監視アラート過多のリスク", Body: "間接対象を増やすとアラートが多くなりすぎる可能性がある", Status: "open", EvidenceSequenceNos: []int64{16}},
			{ID: "item-todo-other", Kind: "todo", Severity: "medium", Title: "監視間隔の見直し", Body: "監視間隔を見直す", Status: "open", EvidenceSequenceNos: []int64{17}},
			{ID: "item-fact-other", Kind: "fact", Severity: "low", Title: "現状の監視間隔", Body: "現状は5分間隔", Status: "open", EvidenceSequenceNos: []int64{18}},
			{ID: "item-risk-vpn", Kind: "risk", Severity: "medium", Title: "VPN証明書期限切れリスク", Body: "VPN証明書が来月末に期限切れになる", Status: "open", EvidenceSequenceNos: []int64{15}},
			{ID: "item-todo-vpn-b", Kind: "todo", Severity: "medium", Title: "VPN証明書の更新手順確認", Body: "高橋さんが証明書の更新手順を確認する", Status: "open", EvidenceSequenceNos: []int64{19}},
		},
		Tree: &liveAnalysisTree{
			Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
				{ID: "topic-network", Kind: "topic", ParentID: treeRootNodeID, Label: "監視アラート運用", Origin: topicOriginDynamic, CreatedAtVersion: 3},
				{ID: "group-quad", Kind: "group", ParentID: "topic-network", Label: "監視関連"},
				{ID: "item-issue-dup", Kind: "issue", Subtype: issueSubtypeDiscussion, ParentID: "group-quad", Label: "監視アラート過多の懸念", Status: "open"},
				{ID: "item-risk-dup", Kind: "risk", ParentID: "group-quad", Label: "監視アラート過多のリスク", Status: "open"},
				{ID: "item-todo-other", Kind: "todo", ParentID: "group-quad", Label: "監視間隔の見直し", Status: "open"},
				{ID: "item-fact-other", Kind: "fact", ParentID: "group-quad", Label: "現状の監視間隔", Status: "open"},
				{ID: "topic-vpn-a", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN装置証明書の期限切れ対策の検討", Origin: topicOriginDynamic, CreatedAtVersion: 5},
				{ID: "item-risk-vpn", Kind: "risk", ParentID: "topic-vpn-a", Label: "VPN証明書期限切れリスク", Status: "open"},
				{ID: "topic-vpn-b", Kind: "topic", ParentID: treeRootNodeID, Label: "vpnと証明書の対応", Origin: topicOriginDynamic, CreatedAtVersion: 8},
				{ID: "item-todo-vpn-b", Kind: "todo", ParentID: "topic-vpn-b", Label: "VPN証明書の更新手順確認", Status: "open"},
			},
		},
	}
	rebuildTreeAuditEdges(previous.Tree)

	before := computeTreeHealth(previous.Tree)
	if !before.needsReorganization() {
		t.Fatalf("fixture health = %+v, want needsReorganization=true before repair (max_group_children)", before)
	}

	payload, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	repaired, stats := applyDeterministicFinalTreeRepairs(payload, nil, 20)
	if stats.Error != "" || stats.IntegrityRejected {
		t.Fatalf("stats = %+v, want a clean repair", stats)
	}
	if stats.PromotedTopicDuplicatesFolded != 1 {
		t.Fatalf("stats = %+v, want exactly 1 duplicate topic fold", stats)
	}
	if stats.CrossKindDuplicatesMerged != 1 {
		t.Fatalf("stats = %+v, want exactly 1 cross-kind duplicate merge", stats)
	}

	state := previousLiveAnalysisState(repaired)
	after := computeTreeHealth(state.Tree)
	if after.needsReorganization() {
		t.Fatalf("health after repair = %+v, want needsReorganization=false", after)
	}
	if len(state.ReorganizationReasons) != 0 {
		t.Fatalf("reorganizationReasons = %v, want none remaining", state.ReorganizationReasons)
	}

	integrity := validateTreeIntegrity(state.Tree, state.Items, nil, state.AgendaAnchors)
	if !integrity.Valid {
		t.Fatalf("integrity = %+v, want valid after repair", integrity)
	}

	if treeNodeByID(state.Tree, "topic-vpn-b") != nil {
		t.Fatalf("topic-vpn-b must be folded away (later duplicate of topic-vpn-a): %+v", state.Tree.Nodes)
	}
	vpnBTodo := treeNodeByID(state.Tree, "item-todo-vpn-b")
	if vpnBTodo == nil || vpnBTodo.ParentID != "topic-vpn-a" {
		t.Fatalf("item-todo-vpn-b = %+v, want reparented under topic-vpn-a (the earlier VPN topic)", vpnBTodo)
	}
	riskItem := findItemByID(state.Items, "item-risk-dup")
	issueItem := findItemByID(state.Items, "item-issue-dup")
	if riskItem == nil || riskItem.Inactive || riskItem.MergedIntoID != "" {
		t.Fatalf("surviving risk item = %+v, want unchanged/active", riskItem)
	}
	if issueItem == nil || issueItem.MergedIntoID != "item-risk-dup" {
		t.Fatalf("merged-away issue item = %+v, want mergedIntoId=item-risk-dup", issueItem)
	}
	if treeNodeByID(state.Tree, "item-issue-dup") != nil {
		t.Fatalf("item-issue-dup's tree node must be removed after the cross-kind merge: %+v", state.Tree.Nodes)
	}
}
