package application

import (
	"strings"
	"testing"
)

// The observed defect: an STT-mangled noun list became a formal fact node whose
// label ("有線LAN車内有線LANファイルサーバー") cannot be understood on its own.
const barelabelEnumerationEvidence = "影響を受けたのは、有線LAN車内有線LANファイルサーバー、社内システムへの接続です。"

func labelQualityTimeline(scope liveEvidenceScope) discourseTimeline {
	return classifyDiscourseTimeline(scope)
}

// §19.1 bare enumeration.
func TestBareEnumerationLabelIsNotMaterializedVerbatim(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{4: barelabelEnumerationEvidence}, 4)
	item := liveAnalysisItem{
		ID: "fact-network-scope", Kind: "fact",
		Title:               "有線LAN車内有線LANファイルサーバー",
		Body:                "有線LAN車内有線LANファイルサーバー",
		EvidenceSequenceNos: []int64{4},
		GroundingDecision:   "accepted",
	}

	quality := evaluateItemLabelQuality(item)
	if !quality.LabelIsBareEnumeration {
		t.Fatalf("quality=%+v, want a bare enumeration detection", quality)
	}
	if quality.LabelHasPredicateOrRelation || quality.LabelIsStandaloneProposition {
		t.Fatalf("quality=%+v, want the label rejected as a non-proposition", quality)
	}
	if !quality.LabelContainsRepeatedAdjacentTerms {
		t.Fatalf("quality=%+v, want the repeated 有線LAN run detected", quality)
	}
	if got := incompleteItemLabelEnding(item); got != labelQualityEndingBareEnumeration {
		t.Fatalf("ending=%q, want %q", got, labelQualityEndingBareEnumeration)
	}

	repaired, decision, changed := repairIncompleteItemLabel(item, scope, labelQualityTimeline(scope))
	if !changed || decision.FinalDecision == "retained_degraded" {
		t.Fatalf("decision=%+v changed=%t, want a rewrite rather than keeping the enumeration", decision, changed)
	}
	if repaired.Title == item.Title {
		t.Fatalf("label unchanged: %q", repaired.Title)
	}
	if incompleteItemLabelEnding(repaired) != "" {
		t.Fatalf("repaired label %q is still not a standalone proposition", repaired.Title)
	}
	// §13: an STT mishearing must never be "corrected" into a plausible word.
	if strings.Contains(repaired.Title, "社内無線LAN") || strings.Contains(repaired.Title, "車内有線LAN") {
		t.Fatalf("label %q contains an unsupported correction or unresolved STT noise", repaired.Title)
	}
	if !strings.Contains(repaired.Title, "影響") {
		t.Fatalf("label %q dropped the relation the evidence supports", repaired.Title)
	}
}

// §19.2 noun-only fact rewritten from supporting evidence.
func TestNounOnlyFactLabelIsRewrittenFromEvidence(t *testing.T) {
	const evidence = "ルーターとファイアウォールには異常がありませんでした"
	scope := evidenceScopeFromTexts(map[int64]string{6: evidence}, 6)
	item := liveAnalysisItem{
		ID: "fact-router", Kind: "fact",
		Title: "ルーターとファイアウォール", Body: "ルーターとファイアウォール",
		EvidenceSequenceNos: []int64{6}, GroundingDecision: "accepted",
	}
	if got := incompleteItemLabelEnding(item); got == "" {
		t.Fatalf("ending=%q, want the noun-only label detected", got)
	}
	repaired, decision, changed := repairIncompleteItemLabel(item, scope, labelQualityTimeline(scope))
	if !changed || !strings.Contains(repaired.Title, "異常がありませんでした") {
		t.Fatalf("repaired=%q decision=%+v changed=%t", repaired.Title, decision, changed)
	}
	if incompleteItemLabelEnding(repaired) != "" {
		t.Fatalf("repaired label %q is still incomplete", repaired.Title)
	}
}

// §19.3 an enumerated issue label is rewritten to name what is still
// unconfirmed.
func TestUnresolvedIssueEnumerationLabelIsRewrittenFromEvidence(t *testing.T) {
	const evidence = "2階の遅延まで今回のVLAN設定漏れで説明できるかは未確認です"
	scope := evidenceScopeFromTexts(map[int64]string{9: evidence}, 9)
	item := liveAnalysisItem{
		ID: "issue-second-floor", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title: "2階の遅延とVLAN設定", Body: "2階の遅延とVLAN設定",
		EvidenceSequenceNos: []int64{9}, GroundingDecision: "accepted",
	}
	if got := incompleteItemLabelEnding(item); got != labelQualityEndingBareEnumeration {
		t.Fatalf("ending=%q, want the enumerated issue label detected", got)
	}
	repaired, _, changed := repairIncompleteItemLabel(item, scope, labelQualityTimeline(scope))
	if !changed || !strings.Contains(repaired.Title, "未確認") {
		t.Fatalf("repaired=%q changed=%t, want the unresolved state preserved", repaired.Title, changed)
	}
}

// §19.4 an enumerated risk label is rewritten to condition + possible harm.
func TestRiskEnumerationLabelIsRewrittenFromEvidence(t *testing.T) {
	const evidence = "VPN証明書を更新しないとリモート接続ができなくなる可能性があります"
	scope := evidenceScopeFromTexts(map[int64]string{12: evidence}, 12)
	item := liveAnalysisItem{
		ID: "risk-vpn", Kind: "risk",
		Title: "VPN証明書とリモート接続", Body: "VPN証明書とリモート接続",
		EvidenceSequenceNos: []int64{12}, GroundingDecision: "accepted",
	}
	if got := incompleteItemLabelEnding(item); got != labelQualityEndingBareEnumeration {
		t.Fatalf("ending=%q, want the enumerated risk label detected", got)
	}
	repaired, _, changed := repairIncompleteItemLabel(item, scope, labelQualityTimeline(scope))
	if !changed || !strings.Contains(repaired.Title, "可能性") {
		t.Fatalf("repaired=%q changed=%t, want the possible harm stated", repaired.Title, changed)
	}
}

// §19.6 hallucination prevention: no plausible-but-unsupported entity.
func TestLabelRewriteNeverInventsAPlausibleEntity(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{4: barelabelEnumerationEvidence}, 4)
	item := liveAnalysisItem{
		ID: "fact-network-scope", Kind: "fact",
		Title: "有線LAN車内有線LANファイルサーバー", Body: "有線LAN車内有線LANファイルサーバー",
		EvidenceSequenceNos: []int64{4}, GroundingDecision: "accepted",
	}
	repaired, _, _ := repairIncompleteItemLabel(item, scope, labelQualityTimeline(scope))
	for _, forbidden := range []string{"社内無線LAN", "無線LAN", "車内"} {
		if strings.Contains(repaired.Title, forbidden) {
			t.Fatalf("label %q contains %q, which the evidence does not support", repaired.Title, forbidden)
		}
	}
}

// §19.7 an unrecoverable enumeration must not reach an active detail node.
func TestUnrecoverableBareLabelIsRejectedInsteadOfMaterialized(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{3: "監視間隔と通知条件"}, 3)
	item := liveAnalysisItem{
		ID: "fact-monitoring", Kind: "fact",
		Title: "監視間隔と通知条件", Body: "監視間隔と通知条件",
		EvidenceSequenceNos: []int64{3}, GroundingDecision: "accepted",
	}
	_, decision, changed := repairIncompleteItemLabel(item, scope, labelQualityTimeline(scope))
	if changed || decision.FinalDecision != "rejected" {
		t.Fatalf("decision=%+v changed=%t, want a rejection when no grounded proposition exists", decision, changed)
	}

	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{item},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: "root", Kind: "topic", Label: "根"},
			{ID: item.ID, Kind: item.Kind, Label: item.Title, ParentID: "root"},
		}},
	}
	repairIncompletePersistedItemLabels(&state, scope, labelQualityTimeline(scope), 3, &liveAnalysisTreeMergeStats{})
	if node := liveTreeNodeByID(state.Tree, item.ID); node != nil {
		t.Fatalf("tree still exposes the unrecoverable node: %+v", node)
	}
}

// §19.8 an existing active item is repaired in place, keeping its identity.
func TestPersistedBareEnumerationItemIsRepairedInPlace(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{4: barelabelEnumerationEvidence}, 4)
	item := liveAnalysisItem{
		ID: "fact-network-scope", Kind: "fact",
		Title: "有線LAN車内有線LANファイルサーバー", Body: "有線LAN車内有線LANファイルサーバー",
		EvidenceSequenceNos: []int64{4}, GroundingDecision: "accepted",
	}
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{item},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: "root", Kind: "topic", Label: "根"},
			{ID: item.ID, Kind: item.Kind, Label: item.Title, ParentID: "root"},
		}},
	}
	stats := &liveAnalysisTreeMergeStats{}
	repairIncompletePersistedItemLabels(&state, scope, labelQualityTimeline(scope), 5, stats)

	if len(state.Items) != 1 || state.Items[0].ID != "fact-network-scope" {
		t.Fatalf("items=%+v, want the same canonical id kept", state.Items)
	}
	repaired := state.Items[0]
	if repaired.Title == item.Title {
		t.Fatalf("label unchanged: %q", repaired.Title)
	}
	if len(repaired.EvidenceSequenceNos) != 1 || repaired.EvidenceSequenceNos[0] != 4 {
		t.Fatalf("evidence=%v, want the original evidence preserved", repaired.EvidenceSequenceNos)
	}
	node := liveTreeNodeByID(state.Tree, item.ID)
	if node == nil || node.Label != repaired.Title || node.ParentID != "root" {
		t.Fatalf("node=%+v, want the rewritten label under the same parent", node)
	}
}

// §19.9 a manually edited label is never rewritten automatically.
func TestManuallyEditedBareLabelIsPreserved(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{3: "監視条件について改めて決めます"}, 3)
	item := liveAnalysisItem{
		ID: "fact-manual", Kind: "fact",
		Title: "監視条件", Body: "監視条件",
		EvidenceSequenceNos: []int64{3}, GroundingDecision: "accepted",
	}
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{item},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: "root", Kind: "topic", Label: "根"},
			{
				ID: item.ID, Kind: item.Kind, Label: item.Title, ParentID: "root",
				LastParentChangeSource: "manual",
			},
		}},
	}
	repairIncompletePersistedItemLabels(&state, scope, labelQualityTimeline(scope), 4, &liveAnalysisTreeMergeStats{})
	node := liveTreeNodeByID(state.Tree, item.ID)
	if node == nil || node.Label != "監視条件" {
		t.Fatalf("node=%+v, want the manual label preserved untouched", node)
	}
}

// A well formed label must stay accepted: the gate may not reclassify existing
// good propositions as bare noun phrases.
func TestStandalonePropositionLabelsStayAccepted(t *testing.T) {
	accepted := []struct {
		kind  string
		label string
	}{
		{"fact", "複数のネットワークサービスが障害の影響を受けた"},
		{"fact", "VLAN30が許可VLAN一覧から漏れていた"},
		{"fact", "ルーターとファイアウォールには異常がなかった"},
		{"issue", "監視間隔と通知条件が未決定である"},
		{"risk", "VPN証明書が来月末に期限切れになる"},
		{"issue", "2階の遅延までVLAN設定で説明できるか未確認である"},
		{"todo", "高橋さんがVPN証明書の更新手順と作業可能日を確認する"},
		{"decision", "VPN証明書更新を今回の障害とは別の対応事項として管理する"},
		{"fact", "2階の一部でも通信遅延が発生していた"},
	}
	for _, tt := range accepted {
		item := liveAnalysisItem{Kind: tt.kind, Title: tt.label, Body: tt.label}
		quality := evaluateItemLabelQuality(item)
		if !quality.LabelIsStandaloneProposition {
			t.Fatalf("kind=%s label=%q rejected as a proposition: %+v", tt.kind, tt.label, quality)
		}
		if got := incompleteItemLabelEnding(item); got != "" {
			t.Fatalf("kind=%s label=%q ending=%q, want accepted", tt.kind, tt.label, got)
		}
	}
}

func TestBareEnumerationLabelsAreRejectedByKind(t *testing.T) {
	rejected := []struct {
		kind  string
		label string
	}{
		{"fact", "有線LAN車内有線LANファイルサーバー"},
		{"fact", "ルーターとファイアウォール"},
		{"issue", "監視間隔と通知条件"},
		{"risk", "VPN証明書とリモート接続"},
		{"todo", "証明書更新と作業日程"},
	}
	for _, tt := range rejected {
		item := liveAnalysisItem{Kind: tt.kind, Title: tt.label, Body: tt.label}
		quality := evaluateItemLabelQuality(item)
		if quality.LabelIsStandaloneProposition || !quality.LabelIsBareEnumeration {
			t.Fatalf("kind=%s label=%q accepted as a proposition: %+v", tt.kind, tt.label, quality)
		}
		if got := incompleteItemLabelEnding(item); got != labelQualityEndingBareEnumeration {
			t.Fatalf("kind=%s label=%q ending=%q, want bare_enumeration", tt.kind, tt.label, got)
		}
	}
}

// A predicate-less label that is not a list is reported through the metrics but
// deliberately not rejected. Japanese headline labels legitimately end in a verb
// stem or a state noun, so acting on that class would drop correct propositions
// from the tree. See the "未対応" section of the change report.
func TestPredicatelessNonListLabelsAreReportedButNotRejected(t *testing.T) {
	reportedOnly := []struct {
		kind  string
		label string
	}{
		{"fact", "VLAN30の設定"},
		{"issue", "監視条件"},
		{"risk", "来月末の証明書"},
		{"fact", "旧スイッチへ切り戻し"},
	}
	for _, tt := range reportedOnly {
		item := liveAnalysisItem{Kind: tt.kind, Title: tt.label, Body: tt.label}
		quality := evaluateItemLabelQuality(item)
		if quality.LabelIsStandaloneProposition {
			t.Fatalf("kind=%s label=%q reported as a proposition: %+v", tt.kind, tt.label, quality)
		}
		if got := incompleteItemLabelEnding(item); got != "" {
			t.Fatalf("kind=%s label=%q ending=%q, want no rejection for this class", tt.kind, tt.label, got)
		}
	}
}
