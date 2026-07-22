package application

import "testing"

func TestGenericTopicLabelRewrittenFromVPNChildren(t *testing.T) {
	items := []liveAnalysisItem{
		{ID: "risk-vpn", Kind: "risk", Title: "VPN証明書が来月末に期限切れとなるリスク", Body: "リモート接続ができなくなる可能性", EvidenceSequenceNos: []int64{23, 24}},
		{ID: "todo-vpn", Kind: "todo", Title: "更新手順と作業可能日を確認する", Body: "小林さんが今週中に確認する", EvidenceSequenceNos: []int64{25, 26}},
	}
	topics := map[string]liveAnalysisTreeNode{
		"topic-vpn": {ID: "topic-vpn", Kind: "topic", Label: "追加論点", Origin: topicOriginDynamic, AgendaRefs: []string{"agenda-external"}},
	}
	parents := map[string]string{"risk-vpn": "topic-vpn", "todo-vpn": "topic-vpn"}
	stats := &liveAnalysisTreeMergeStats{}
	repairGenericTopicLabels(items, topics, parents, 8, stats)
	if got := topics["topic-vpn"].Label; got != "VPN証明書の更新対応" {
		t.Fatalf("label=%q", got)
	}
	if topics["topic-vpn"].ID != "topic-vpn" || len(topics["topic-vpn"].AgendaRefs) != 1 {
		t.Fatalf("topic identity/refs changed: %+v", topics["topic-vpn"])
	}
	if stats.GenericTopicLabelsRewritten != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestGenericCandidateLabelUsesSubjectKeyAndEvidence(t *testing.T) {
	candidates := []emergingTopicCandidate{{
		ID: "candidate-vpn", Label: "別件", SubjectKey: "VPN証明書有効期限管理",
		EvidenceItemIDs: []string{"risk-vpn", "todo-vpn"},
	}}
	items := []liveAnalysisItem{
		{ID: "risk-vpn", Kind: "risk", Title: "VPN証明書の期限切れリスク"},
		{ID: "todo-vpn", Kind: "todo", Title: "VPN証明書の更新手順を確認"},
	}
	repairGenericCandidateLabels(candidates, items, nil)
	if candidates[0].Label != "VPN証明書の更新対応" || candidates[0].ID != "candidate-vpn" {
		t.Fatalf("candidate=%+v", candidates[0])
	}
}

func TestRiskAndTodoSubjectFragmentationRepairsOnlyMatchingObject(t *testing.T) {
	items := []liveAnalysisItem{
		{ID: "risk-vpn", Kind: "risk", Title: "VPN証明書期限切れでリモート接続不能になる", Body: "VPN証明書が来月末に期限切れ", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{23, 24}},
		{ID: "todo-vpn", Kind: "todo", Title: "VPN証明書の更新手順と作業日を確認する", Body: "小林さんが今週中に確認", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{25, 26}},
		{ID: "todo-alert", Kind: "todo", Title: "監視アラートの通知間隔を検討する", Body: "通知条件を見直す", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{20, 21}},
	}
	topics := map[string]liveAnalysisTreeNode{
		"topic-vpn":     {ID: "topic-vpn", Kind: "topic", Label: "VPN証明書の期限管理", Origin: topicOriginDynamic},
		"topic-monitor": {ID: "topic-monitor", Kind: "topic", Label: "監視強化と設計見直し", Origin: topicOriginDynamic},
	}
	parents := map[string]string{
		"risk-vpn": "topic-vpn", "todo-vpn": "topic-monitor", "todo-alert": "topic-monitor",
	}
	stats := &liveAnalysisTreeMergeStats{}
	repairRelatedSubjectFragmentation(items, topics, map[string]liveAnalysisTreeNode{}, parents, stats)
	if parents["todo-vpn"] != "topic-vpn" {
		t.Fatalf("VPN TODO parent=%s", parents["todo-vpn"])
	}
	if parents["todo-alert"] != "topic-monitor" {
		t.Fatalf("unrelated monitoring TODO moved to %s", parents["todo-alert"])
	}
	if stats.SubjectFragmentationRepairs != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	if items[1].Kind != "todo" || items[0].Kind != "risk" {
		t.Fatalf("cross-kind items were merged: %+v", items)
	}
}

func TestNearbyReferentialRiskInheritsConcreteDynamicTopic(t *testing.T) {
	items := []liveAnalysisItem{
		{ID: "risk-vpn-expiry", Kind: "risk", Title: "VPN証明書の有効期限が来月末", Body: "リモート接続への影響がある", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{23}},
		{ID: "risk-vpn-impact", Kind: "risk", Title: "放置するとリモート接続ができなくなる", Body: "今回の障害とは直接関係しない", ClassificationStatus: classificationUnclassified, EvidenceSequenceNos: []int64{24}},
	}
	topics := map[string]liveAnalysisTreeNode{
		"topic-vpn":             {ID: "topic-vpn", Kind: "topic", Label: "VPN証明書の期限管理", Origin: topicOriginDynamic},
		treeUnclassifiedTopicID: {ID: treeUnclassifiedTopicID, Kind: "topic", Label: treeUnclassifiedTopicLabel, Origin: topicOriginSystem},
	}
	parents := map[string]string{"risk-vpn-expiry": "topic-vpn", "risk-vpn-impact": treeUnclassifiedTopicID}
	repairRelatedSubjectFragmentation(items, topics, map[string]liveAnalysisTreeNode{}, parents, nil)
	if parents["risk-vpn-impact"] != "topic-vpn" {
		t.Fatalf("referential risk parent=%s", parents["risk-vpn-impact"])
	}
}
