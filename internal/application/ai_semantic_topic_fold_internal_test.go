package application

import (
	"encoding/json"
	"strings"
	"testing"
)

// このファイルは、既存topicとの意味照合(W1: semanticExistingTopicID)、
// candidate新規作成前のfold(W2)、昇格資格厳格化+assigned-evidence fold(W3)
// のテストを持つ。対象は session_125e3cc511ee69bb のVPN topic重複バグ
// (candidate-865555e2234eが昇格済みなのに、candidate-4ecf877f6d5f「vpnと証明書」
// が別途新規作成・昇格された)。

func TestSemanticExistingTopicIDMatchesSharedLatinSubjectTerm(t *testing.T) {
	topics := map[string]liveAnalysisTreeNode{
		"topic-vpn-cert": {ID: "topic-vpn-cert", Kind: "topic", Label: "VPN装置証明書の期限切れ対策の検討", Origin: topicOriginDynamic},
	}
	if got := semanticExistingTopicID("vpnと証明書の対応", "", topics); got != "topic-vpn-cert" {
		t.Fatalf("semanticExistingTopicID = %q, want topic-vpn-cert (shared vpn/証明書 subject term)", got)
	}
}

func TestSemanticExistingTopicIDRejectsUnrelatedSubject(t *testing.T) {
	topics := map[string]liveAnalysisTreeNode{
		"topic-alert": {ID: "topic-alert", Kind: "topic", Label: "監視アラート条件", Origin: topicOriginDynamic},
	}
	if got := semanticExistingTopicID("VPN証明書更新", "", topics); got != "" {
		t.Fatalf("semanticExistingTopicID = %q, want no match against unrelated topic", got)
	}
}

func TestSemanticExistingTopicIDRejectsLatinTokenConflict(t *testing.T) {
	topics := map[string]liveAnalysisTreeNode{
		"topic-vpn-cert": {ID: "topic-vpn-cert", Kind: "topic", Label: "VPN装置証明書の期限切れ対策", Origin: topicOriginDynamic},
	}
	if got := semanticExistingTopicID("SSL証明書の管理", "", topics); got != "" {
		t.Fatalf("semanticExistingTopicID = %q, want no match (ssl vs vpn latin token conflict)", got)
	}
}

func TestLatinTokenConflictDetectsAcronymMismatchOnly(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"same acronym", "VPN証明書", "vpnと証明書", false},
		{"different acronym", "SSL証明書", "VPN証明書", true},
		{"no acronym on either side", "証明書の更新", "証明書の管理", false},
		{"acronym only on one side", "証明書の更新", "VPN証明書", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := latinTokenConflict(c.a, c.b); got != c.want {
				t.Fatalf("latinTokenConflict(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestCandidateCreationFoldsIntoExistingPromotedTopicBySubject reproduces the
// W2 fix: a model newTopics proposal ("vpnと証明書") whose label only loosely
// overlaps an already-promoted dynamic topic's full label must be routed into
// that existing topic instead of spawning a new emerging candidate. The
// assigned item's own actor/due-date wording is untouched (folding only
// redirects the parent, per the existing hysteresis/assignment path).
func TestCandidateCreationFoldsIntoExistingPromotedTopicBySubject(t *testing.T) {
	previous := liveAnalysisPayload{
		Summary: "previous",
		Items: []liveAnalysisItem{
			{ID: "item-risk-vpn", Kind: "risk", Severity: "medium", Title: "VPN証明書期限切れリスク", Body: "VPN証明書が来月末に期限切れになる", Status: "open", EvidenceSequenceNos: []int64{15}},
		},
		Tree: &liveAnalysisTree{
			Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
				{ID: "candidate-existingvpn", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN装置証明書の期限切れ対策の検討", Origin: topicOriginDynamic},
				{ID: "item-risk-vpn", Kind: "risk", ParentID: "candidate-existingvpn", Label: "VPN証明書期限切れリスク"},
			},
		},
	}
	previousJSON, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	diff := `{
		"summary": "更新",
		"currentTopic": "vpnと証明書の対応",
		"items": [{"id": "item-todo-vpn-update", "kind": "todo", "severity": "medium", "title": "証明書更新手順の確認", "body": "高橋さんに今週中に証明書の更新手順と作業可能日を確認してもらう", "status": "open", "evidenceSequenceNos": [16]}],
		"newTopics": [{"id": "topic-xxxxxx", "label": "vpnと証明書の対応"}],
		"assignments": [{"nodeId": "item-todo-vpn-update", "parentTopicId": "topic-xxxxxx", "confidence": 0.8, "reason": "vpn証明書の対応"}]
	}`
	scope := evidenceScopeFromTexts(map[int64]string{
		15: "VPN証明書が来月末に期限切れになる可能性があります。",
		16: "高橋さんに今週中に証明書の更新手順と作業可能日を確認してもらいます。",
	}, 16)
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
		diff, previousJSON, nil, 2, []int64{16}, scope, TreeClassificationConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}
	merged := previousLiveAnalysisState(raw)
	assertTreeInvariants(t, merged.Tree)
	if len(merged.EmergingTopics) != 0 {
		t.Fatalf("emergingTopics = %+v, want none: proposal must fold into the existing VPN topic instead of creating a candidate", merged.EmergingTopics)
	}
	if treeNodeByID(merged.Tree, "topic-xxxxxx") != nil {
		t.Fatalf("proposed topic-xxxxxx must not be created: %+v", merged.Tree.Nodes)
	}
	node := treeNodeByID(merged.Tree, "item-todo-vpn-update")
	if node == nil || node.ParentID != "candidate-existingvpn" {
		t.Fatalf("node = %+v, want folded under the existing VPN topic candidate-existingvpn", node)
	}
	item := itemByID(merged.Items, "item-todo-vpn-update")
	if item == nil || !strings.Contains(item.Body, "高橋さん") || !strings.Contains(item.Body, "今週中") {
		t.Fatalf("item = %+v, want actor/due wording preserved verbatim", item)
	}
}

// TestPromoteEmergingCandidatesFoldsWhenEvidenceAlreadyPlacedInExistingTopic
// reproduces the W3 fix: candidate-4ecf877f6d5f's origin item was folded into
// an existing topic's item by an earlier semantic-dedup pass (so it is
// "placed", not genuinely new evidence). Once no other unplaced evidence
// remains, the stable, evidence-eligible candidate must fold into that
// existing topic instead of being promoted as a duplicate dynamic topic.
func TestPromoteEmergingCandidatesFoldsWhenEvidenceAlreadyPlacedInExistingTopic(t *testing.T) {
	topics := map[string]liveAnalysisTreeNode{
		"topic-existing": {ID: "topic-existing", Kind: "topic", Label: "VPN装置証明書の期限切れ対策の検討", Origin: topicOriginDynamic},
	}
	parents := map[string]string{
		"topic-existing": treeRootNodeID,
		// item-a is the candidate's sole evidence, but it was already reparented
		// onto the existing topic by an earlier semantic-dedup merge -- exactly
		// the "already placed" shape candidatePromotionEvidenceCount used to
		// miscount as fresh promotion evidence.
		"item-a": "topic-existing",
	}
	details := map[string]liveAnalysisTreeNode{
		"item-a": {ID: "item-a", Kind: "todo", ParentID: "topic-existing"},
	}
	itemRecord := &liveAnalysisItem{ID: "item-a", Kind: "todo", ClassificationStatus: classificationAssigned}
	candidates := []emergingTopicCandidate{
		{ID: "candidate-vpn2", Label: "vpnと証明書", EvidenceItemIDs: []string{"item-a"}, OriginItemIDs: []string{"item-a"}, FirstRound: 1, LastRound: 3, RoundCount: 3},
	}
	dynamicCount := 1
	stats := &liveAnalysisTreeMergeStats{}
	result := promoteEmergingCandidates(promotionContext{
		candidates: candidates,
		parents:    parents,
		details:    details,
		topics:     topics,
		labelIndex: map[string]string{normalizeForMatch("VPN装置証明書の期限切れ対策の検討"): "topic-existing"},
		addTopic: func(node liveAnalysisTreeNode) {
			t.Fatalf("addTopic must not be called; candidate must fold instead: %+v", node)
		},
		dynamicTopicCount: &dynamicCount,
		itemAt:            func(id string) *liveAnalysisItem { return itemRecord },
		round:             3,
		cfg:               TreeClassificationConfig{}.normalized(),
		stats:             stats,
	})
	if len(result) != 0 {
		t.Fatalf("candidates after promotion = %+v, want folded away (none remaining)", result)
	}
	if parents["item-a"] != "topic-existing" {
		t.Fatalf("item-a parent = %q, want unchanged (still under the existing topic)", parents["item-a"])
	}
	if stats.CandidateFoldedIntoAgenda == 0 {
		t.Fatalf("stats = %+v, want CandidateFoldedIntoAgenda incremented", stats)
	}
	if dynamicCount != 1 {
		t.Fatalf("dynamicTopicCount = %d, want unchanged (no new topic promoted)", dynamicCount)
	}
}
