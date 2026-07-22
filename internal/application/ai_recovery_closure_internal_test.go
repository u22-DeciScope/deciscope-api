package application

import "testing"

// recoverySeq12Text reproduces the exact recovery utterance from
// session_5e4da9dc40d50940: it names the concrete fix already applied
// ("正常になったことを確認しています") and should be treated as a
// "recovery" closure candidate, not an ordinary agreement/decision closure.
const recoverySeq12Text = "復旧対応としては、午前9時52分に旧スイッチ一度切り戻し、その後新しいスイッチのトランク設定と許可v欄を修正しました。午前10時5分に有線LAN無線ランファイルサーバーへの接続が正常になったことを確認しています。"

func TestRecoverySeq12MatchesServerExplicitClosureAndRecoveryPatterns(t *testing.T) {
	if !serverExplicitClosurePattern.MatchString(recoverySeq12Text) {
		t.Fatalf("expected seq12 recovery statement to match serverExplicitClosurePattern")
	}
	if !recoveryClosurePattern.MatchString(recoverySeq12Text) {
		t.Fatalf("expected seq12 recovery statement to match recoveryClosurePattern")
	}
	if resolutionOpenPattern.MatchString(recoverySeq12Text) {
		t.Fatalf("seq12 recovery statement must not also match resolutionOpenPattern")
	}
}

func TestPartialRecoveryStatementMatchesResolutionOpenPattern(t *testing.T) {
	text := "一部端末ではまだ接続できないという状況が続いています。"
	if !resolutionOpenPattern.MatchString(text) {
		t.Fatalf("expected partial-recovery statement to match resolutionOpenPattern")
	}
}

// TestValidateResolutionUpdatesRejectsResolvedContradictedByPartialRecovery
// exercises the full validateResolutionUpdates path: a resolved request
// whose own latest evidence is a "一部...まだ接続できない" statement must be
// rejected as contradicted, not silently applied.
func TestValidateResolutionUpdatesRejectsResolvedContradictedByPartialRecovery(t *testing.T) {
	item := liveAnalysisItem{ID: "item-conn", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "支店の接続障害", Body: "一部拠点で接続に問題が生じている", Status: "open"}
	scope := evidenceScopeFromTexts(map[int64]string{
		10: "支店の接続が復旧しました。",
		12: "一部端末ではまだ接続できないという状況が続いています。",
	}, 10, 12)
	resolver := newCanonicalReferenceResolver(item.ID)
	stats := &liveAnalysisTreeMergeStats{}
	requested := []resolutionUpdate{{ItemID: item.ID, Status: "resolved", EvidenceSequenceNos: []int64{10, 12}, Reason: "復旧確認"}}
	validated := validateResolutionUpdates(requested, resolver, []liveAnalysisItem{item}, nil, scope, 2, stats)
	if _, applied := validated[item.ID]; applied {
		t.Fatalf("expected the resolved request to be rejected as contradicted by the later partial-recovery evidence, validated=%+v", validated)
	}
	rejectedAsContradicted := false
	for _, evaluation := range stats.ResolutionDecisions {
		if evaluation.ItemID == item.ID && evaluation.Result == resolutionRejected && evaluation.Reason == "contradicted_by_latest_evidence" {
			rejectedAsContradicted = true
		}
	}
	if !rejectedAsContradicted {
		t.Fatalf("expected rejection reason=contradicted_by_latest_evidence, decisions=%+v", stats.ResolutionDecisions)
	}
}

// TestRecoveryClosureExcludesInvestigationSubtypeAndTodo confirms recovery
// closures never resolve an investigation-subtype issue (root-cause
// investigation) or a todo, even when the wording is a loose subject match.
func TestRecoveryClosureExcludesInvestigationSubtypeAndTodo(t *testing.T) {
	vlanItem, _ := realVLANSiblingPair()
	investigation := liveAnalysisItem{Kind: "issue", Subtype: issueSubtypeInvestigation, Status: "open",
		Title: vlanItem.Title, Body: vlanItem.Body}
	discussion := liveAnalysisItem{Kind: "issue", Subtype: issueSubtypeDiscussion, Status: "open",
		Title: vlanItem.Title, Body: vlanItem.Body}
	todo := liveAnalysisItem{Kind: "todo", Status: "open",
		Title: vlanItem.Title, Body: vlanItem.Body}
	if recoveryClosureEligibleItem(investigation, recoverySeq12Text) {
		t.Fatalf("investigation-subtype issue must not be recovery-closure eligible")
	}
	if recoveryClosureEligibleItem(todo, recoverySeq12Text) {
		t.Fatalf("todo must not be recovery-closure eligible")
	}
	if !recoveryClosureEligibleItem(discussion, recoverySeq12Text) {
		t.Fatalf("expected discussion-subtype issue about the same subject to be recovery-closure eligible")
	}
}

// TestRecoveryClosureExcludesItemsUnderContinuedInvestigation reproduces the
// over-resolution defect found in session_5e4da9dc40d50940:
// item-issue-discussion-39aa3681095d ("発生時刻と影響範囲の拡張確認", 2階を
// 含む影響範囲) explicitly states in its own body that root-cause
// confirmation still needs additional investigation, and the same subject is
// reiterated as unresolved later in the meeting (seq25). A recovery closure
// sentence must never resolve such an item even though its subject is
// loosely similar (subtype=discussion, so the investigation-subtype
// exclusion alone does not catch it). The two real VLAN-fault issue
// siblings, which have no such continued-investigation language of their
// own, must remain eligible.
func TestRecoveryClosureExcludesItemsUnderContinuedInvestigation(t *testing.T) {
	extendedImpactScope := liveAnalysisItem{
		ID: "item-issue-discussion-39aa3681095d", Kind: "issue", Subtype: issueSubtypeDiscussion, Status: "open",
		Title: "発生時刻と影響範囲の拡張確認",
		Body:  "3階だけでなく2階の一部にも通信遅延・障害があり、影響を受けたのは有線LAN、車内無線LAN、ファイルサーバー、社内説明への接続。関連する監視ログの不足を踏まえ、原因確定には追加調査が必要。",
	}
	vlanMismatch := liveAnalysisItem{
		ID: "item-issue-discussion-8a91d2b7edb2", Kind: "issue", Subtype: issueSubtypeDiscussion, Status: "open",
		Title: "3階アクセススイッチのVLAN設定不一致",
		Body:  "前日の夜に交換した3階のアクセススイッチのVLAN20とVLAN30の通信が不安定。正しいトランク設定と上位機器との接続形態の再確認が必要。",
	}
	vlanMissingAllowlist := liveAnalysisItem{
		ID: "item-issue-discussion-f13b675f24ec", Kind: "issue", Subtype: issueSubtypeDiscussion, Status: "open",
		Title: "VLAN設定漏れが障害原因の候補",
		Body:  "3階のトランク設定は存在したが、許可するVLANの一覧からVLAN30が漏れていた。現時点でこの設定ミスが直接原因の最有力候補。",
	}
	mixedClosureLanguage := liveAnalysisItem{
		ID: "item-issue-mixed-closure-language", Kind: "issue", Subtype: issueSubtypeDiscussion, Status: "open",
		Title: "複合状態の課題",
		Body:  "その他は解決済みだが、この点については追加調査が必要。",
	}
	if recoveryClosureEligibleItem(extendedImpactScope, recoverySeq12Text) {
		t.Fatalf("item-issue-discussion-39aa3681095d (原因確定には追加調査が必要) must not be recovery-closure eligible")
	}
	if !recoveryClosureEligibleItem(vlanMismatch, recoverySeq12Text) {
		t.Fatalf("item-issue-discussion-8a91d2b7edb2 (VLAN mismatch, no continued-investigation language) must remain recovery-closure eligible")
	}
	if !recoveryClosureEligibleItem(vlanMissingAllowlist, recoverySeq12Text) {
		t.Fatalf("item-issue-discussion-f13b675f24ec (VLAN allowlist gap, no continued-investigation language) must remain recovery-closure eligible")
	}
	if recoveryClosureEligibleItem(mixedClosureLanguage, recoverySeq12Text) {
		t.Fatalf("item containing both closure and continued-investigation language must not be recovery-closure eligible (continued-investigation pattern must take priority)")
	}
}

// TestSynthesizeExplicitClosureUpdatesRecoveryTargetsMultipleEligibleItems
// confirms the recovery branch in synthesizeExplicitClosureUpdates resolves
// every eligible issue/risk sharing the recovered subject, while leaving the
// investigation item and the todo untouched.
func TestSynthesizeExplicitClosureUpdatesRecoveryTargetsMultipleEligibleItems(t *testing.T) {
	vlanIssue := liveAnalysisItem{ID: "item-vlan", Kind: "issue", Subtype: issueSubtypeDiscussion, Status: "open",
		Title: "3階アクセススイッチのVLAN設定不一致", Body: "VLAN20とVLAN30の通信が不安定。正しいトランク設定と接続形態の再確認が必要。", EvidenceSequenceNos: []int64{6}}
	investigation := liveAnalysisItem{ID: "item-investigation", Kind: "issue", Subtype: issueSubtypeInvestigation, Status: "open",
		Title: "VLAN別の疎通監視案の検討", Body: "VLAN別の疎通監視案を検討する。", EvidenceSequenceNos: []int64{17, 18}}
	todo := liveAnalysisItem{ID: "item-todo", Kind: "todo", Status: "open",
		Title: "証明書更新の手順と作業可能日を確定", Body: "証明書更新の手順と作業可能日を確定する。", EvidenceSequenceNos: []int64{22}}
	scope := evidenceScopeFromTexts(map[int64]string{12: recoverySeq12Text}, 12)
	stats := &liveAnalysisTreeMergeStats{}
	_, updates := synthesizeExplicitClosureUpdates([]liveAnalysisItem{vlanIssue, investigation, todo}, nil, scope, stats)
	resolvedIDs := map[string]bool{}
	for _, update := range updates {
		resolvedIDs[update.ItemID] = true
	}
	if !resolvedIDs[vlanIssue.ID] {
		t.Fatalf("expected recovery closure to resolve the VLAN issue, updates=%+v", updates)
	}
	if resolvedIDs[investigation.ID] {
		t.Fatalf("recovery closure must not resolve the investigation item, updates=%+v", updates)
	}
	if resolvedIDs[todo.ID] {
		t.Fatalf("recovery closure must not resolve the todo, updates=%+v", updates)
	}
	if stats.ClosureTargetsFound == 0 {
		t.Fatalf("expected ClosureTargetsFound > 0, stats=%+v", stats)
	}
}
