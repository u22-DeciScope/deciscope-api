package application

import "testing"

// realVLANSiblingPair reproduces the exact duplicate pair persisted in
// session_5e4da9dc40d50940: two independently worded issue items about the
// same VLAN misconfiguration, evidenced two utterances apart, that neither
// sameKindSemanticDuplicate (title similarity < 0.90, evidence distance 2
// with title similarity ~0.65 < 0.70) nor sameKindSequentialProposition
// (evidence distance <= 1) catches.
func realVLANSiblingPair() (liveAnalysisItem, liveAnalysisItem) {
	a := liveAnalysisItem{
		ID: "item-issue-discussion-8a91d2b7edb2", Kind: "issue", Subtype: issueSubtypeDiscussion, Status: "open",
		Title:               "3階アクセススイッチのVLAN設定不一致",
		Body:                "前日の夜に交換した3階のアクセススイッチのVLAN20とVLAN30の通信が不安定。正しいトランク設定と上位機器との接続形態の再確認が必要。",
		EvidenceSequenceNos: []int64{6},
	}
	b := liveAnalysisItem{
		ID: "item-issue-discussion-598289c07dd0", Kind: "issue", Subtype: issueSubtypeDiscussion, Status: "open",
		Title:               "3階スイッチVLAN設定不一致の再確認",
		Body:                "3階スイッチのVLAN設定不一致が障害原因候補として挙がっている。現状、再現性と監視ログの不足から確定には至っていない。追加の監視と設定チェックを行う必要あり。",
		EvidenceSequenceNos: []int64{8},
	}
	return a, b
}

func TestSameSubjectSiblingDuplicateRealPairMatches(t *testing.T) {
	a, b := realVLANSiblingPair()
	parentOf := map[string]string{a.ID: "candidate-636123661622", b.ID: "candidate-636123661622"}
	matched, score := sameSubjectSiblingDuplicate(a, b, parentOf)
	if !matched {
		t.Fatalf("expected real VLAN sibling pair to match, score=%v", score)
	}
	if score < 0.55 {
		t.Fatalf("expected score >= 0.55 threshold, got %v", score)
	}
	// sameKindSemanticDuplicate/sameKindSequentialProposition must both still
	// miss this pair; otherwise the pair was not exercising the new rule.
	if matched, _ := sameKindSemanticDuplicate(a, b); matched {
		t.Fatalf("fixture pair unexpectedly matched by sameKindSemanticDuplicate; this test no longer exercises the new rule")
	}
	if matched, _ := sameKindSequentialProposition(a, b); matched {
		t.Fatalf("fixture pair unexpectedly matched by sameKindSequentialProposition; this test no longer exercises the new rule")
	}
	t.Logf("real VLAN sibling pair score=%v", score)
}

func TestSameSubjectSiblingDuplicateRejectsDifferentFloor(t *testing.T) {
	a := liveAnalysisItem{
		ID: "item-3f", Kind: "issue", Subtype: issueSubtypeDiscussion, Status: "open",
		Title: "3階スイッチのVLAN30許可漏れ", Body: "3階のアクセススイッチでVLAN30が許可されていなかった。", EvidenceSequenceNos: []int64{6},
	}
	b := liveAnalysisItem{
		ID: "item-2f", Kind: "issue", Subtype: issueSubtypeDiscussion, Status: "open",
		Title: "2階の通信遅延の原因調査", Body: "2階の通信遅延の原因を調査する。", EvidenceSequenceNos: []int64{7},
	}
	parentOf := map[string]string{a.ID: "candidate-636123661622", b.ID: "candidate-636123661622"}
	if matched, score := sameSubjectSiblingDuplicate(a, b, parentOf); matched {
		t.Fatalf("expected cross-floor pair NOT to match, score=%v", score)
	}
	// The title-level numericSignature guard alone must reject this pair
	// ("3" vs "2"), independent of the looser title+body stage.
	if !numericSignatureIncompatible(a.Title, b.Title) {
		t.Fatalf("expected title-level numeric signatures to be incompatible: %q vs %q", numericSignature(a.Title), numericSignature(b.Title))
	}
}

func TestSameSubjectSiblingDuplicateRequiresSameParent(t *testing.T) {
	a, b := realVLANSiblingPair()
	b.Title, b.Body = a.Title, a.Body // identical text, different parent
	parentOf := map[string]string{a.ID: "candidate-636123661622", b.ID: "candidate-other"}
	if matched, score := sameSubjectSiblingDuplicate(a, b, parentOf); matched {
		t.Fatalf("expected pair under different parents NOT to match, score=%v", score)
	}
}

func TestSameSubjectSiblingDuplicateRejectsMixedResolvedStatus(t *testing.T) {
	a, b := realVLANSiblingPair()
	a.Status = "resolved"
	parentOf := map[string]string{a.ID: "candidate-636123661622", b.ID: "candidate-636123661622"}
	if matched, score := sameSubjectSiblingDuplicate(a, b, parentOf); matched {
		t.Fatalf("expected pair with only one resolved side NOT to match, score=%v", score)
	}
}

func TestSameSubjectSiblingDuplicateExcludesRiskDecisionFact(t *testing.T) {
	a, b := realVLANSiblingPair()
	parentOf := map[string]string{a.ID: "candidate-636123661622", b.ID: "candidate-636123661622"}
	for _, kind := range []string{"risk", "decision", "fact"} {
		a.Kind, b.Kind = kind, kind
		a.Subtype, b.Subtype = "", ""
		if matched, score := sameSubjectSiblingDuplicate(a, b, parentOf); matched {
			t.Fatalf("expected kind=%s to stay out of scope, score=%v", kind, score)
		}
	}
}

// TestDeduplicateExistingLiveStateMergesSiblingPairAndTombstones exercises the
// integration point in deduplicateExistingLiveState: the real VLAN pair
// should collapse to one canonical item with unioned evidence, and the
// tombstone should be attributed to the sibling-specific dedup source so it
// stays distinguishable from the pre-existing title/proposition rules.
func TestDeduplicateExistingLiveStateMergesSiblingPairAndTombstones(t *testing.T) {
	a, b := realVLANSiblingPair()
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "root"},
		{ID: "candidate-636123661622", Kind: "topic", ParentID: treeRootNodeID, Label: "スイッチ設定とネットワークトポロジー確認", Origin: topicOriginDynamic},
		{ID: a.ID, Kind: "issue", ParentID: "candidate-636123661622", Label: a.Title},
		{ID: b.ID, Kind: "issue", ParentID: "candidate-636123661622", Label: b.Title},
	}}
	state := &liveAnalysisPayload{Items: []liveAnalysisItem{a, b}, Tree: tree, TreeVersion: 12}
	stats := &liveAnalysisTreeMergeStats{}
	remap := deduplicateExistingLiveState(state, stats)
	if len(state.Items) != 1 {
		t.Fatalf("expected 1 surviving item, got %d: %+v", len(state.Items), state.Items)
	}
	if remap[b.ID] != a.ID {
		t.Fatalf("expected %s to remap onto %s, remap=%v", b.ID, a.ID, remap)
	}
	survivor := state.Items[0]
	if !containsExactInt64(survivor.EvidenceSequenceNos, 6) || !containsExactInt64(survivor.EvidenceSequenceNos, 8) {
		t.Fatalf("expected merged evidence to include 6 and 8, got %v", survivor.EvidenceSequenceNos)
	}
	if stats.SiblingDuplicateItemsMerged != 1 {
		t.Fatalf("expected SiblingDuplicateItemsMerged=1, got %d", stats.SiblingDuplicateItemsMerged)
	}
	found := false
	for _, tombstone := range state.ItemTombstones {
		if tombstone.CanonicalItemID == b.ID && tombstone.MergedIntoItemID == a.ID && tombstone.CreatedBy == "sibling_semantic_dedup" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a sibling_semantic_dedup tombstone for %s merged into %s, got %+v", b.ID, a.ID, state.ItemTombstones)
	}
}

func containsExactInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
