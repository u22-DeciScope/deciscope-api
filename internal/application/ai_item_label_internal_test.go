package application

import (
	"strings"
	"testing"
)

func TestIncompleteItemLabelRepairsFromUniqueTranscriptEvidence(t *testing.T) {
	full := "トランク設定自体は入っていましたが、許可するVLANの一覧からVLAN 30が漏れていました。"
	scope := evidenceScopeFromTexts(map[int64]string{8: full}, 8)
	timeline := classifyDiscourseTimeline(scope)
	item := liveAnalysisItem{
		ID: "fact-vlan", Kind: "fact",
		Title:               "トランク設定自体は入っていましたが、許可するVLANの一覧からVLAN 30が漏れてい",
		Body:                "トランク設定自体は入っていましたが、許可するVLANの一覧からVLAN 30が漏れてい",
		EvidenceSequenceNos: []int64{8},
	}

	if got := incompleteItemLabelEnding(item); got != "incomplete_conjugation" {
		t.Fatalf("ending=%q, want incomplete_conjugation", got)
	}
	repaired, decision, changed := repairIncompleteItemLabel(item, scope, timeline)
	if !changed || decision.RewriteResult != "success" ||
		!strings.Contains(repaired.Title, "VLAN 30が漏れていました") ||
		incompleteItemLabelEnding(repaired) != "" {
		t.Fatalf("repaired=%+v decision=%+v changed=%t", repaired, decision, changed)
	}
}

func TestIncompleteItemLabelPreservesCompletePredicatePastPreferredLength(t *testing.T) {
	full := "復旧対応としては、午前9時52分に旧スイッチへ一度切り戻し、その後新しいスイッチのトランク設定と許可VLAN欄を修正しました"
	runes := []rune(full)
	if len(runes) <= liveAnalysisItemLabelPreferredMaxRunes {
		t.Fatalf("fixture is too short: %d runes", len(runes))
	}
	item := liveAnalysisItem{
		ID: "fact-recovery", Kind: "fact",
		Title: string(runes[:liveAnalysisItemLabelPreferredMaxRunes]),
		Body:  full,
	}
	if got := incompleteItemLabelEnding(item); got != "max_length_truncation" {
		t.Fatalf("ending=%q, want max_length_truncation", got)
	}
	repaired, _, changed := repairIncompleteItemLabel(
		item, liveEvidenceScope{}, discourseTimeline{Roles: map[int64]liveEvidenceRole{}},
	)
	if !changed || !strings.Contains(repaired.Title, "許可VLAN欄を修正しました") ||
		len([]rune(repaired.Title)) > liveAnalysisItemLabelPreferredMaxRunes ||
		incompleteItemLabelEnding(repaired) != "" {
		t.Fatalf("repaired=%+v changed=%t", repaired, changed)
	}

	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{item},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{{
			ID: item.ID, Kind: item.Kind, Label: item.Title,
		}}},
	}
	updateFinalItemAndNode(&state, repaired)
	if node := liveTreeNodeByID(state.Tree, item.ID); node == nil || node.Label != repaired.Title {
		t.Fatalf("tree node=%+v", node)
	}
}

func TestIncompleteItemLabelRejectsUnsafeDanglingFragment(t *testing.T) {
	item := liveAnalysisItem{ID: "fact-fragment", Kind: "fact", Title: "その後新しいスイッチの"}
	if got := incompleteItemLabelEnding(item); got != "dangling_particle" {
		t.Fatalf("ending=%q, want dangling_particle", got)
	}
	_, decision, changed := repairIncompleteItemLabel(
		item, liveEvidenceScope{}, discourseTimeline{Roles: map[int64]liveEvidenceRole{}},
	)
	if changed || decision.RewriteResult != "failed" || decision.FinalDecision != "rejected" {
		t.Fatalf("decision=%+v changed=%t", decision, changed)
	}
}

func TestIncompleteItemLabelDetectsTruncatedDeParticle(t *testing.T) {
	item := liveAnalysisItem{
		Kind:  "issue",
		Title: "このVLAN設定だけで説明できるかは確認で",
		Body:  "このVLAN設定だけで説明できるかは確認できていません",
	}
	if got := incompleteItemLabelEnding(item); got != "dangling_particle" {
		t.Fatalf("ending=%q, want dangling_particle", got)
	}
}

func TestSemanticallyCompleteItemLabelPrefersExtendedConcreteClause(t *testing.T) {
	text := "ただし、2階で発生した通信切断まで、このVLAN設定だけで説明できるかは確認できていません。この点は未解決の調査事項として残します"
	got := semanticallyCompleteItemLabel(text, "issue")
	if !strings.Contains(got, "2階で発生した通信切断") ||
		!strings.HasSuffix(got, "確認できていません") ||
		strings.Contains(got, "この点") {
		t.Fatalf("label=%q", got)
	}
}

func TestIncompleteItemLabelAcceptsNaturalDecisionNominalization(t *testing.T) {
	item := liveAnalysisItem{
		ID: "decision-double-check", Kind: "decision",
		Title: "ネットワーク機器交換時のダブルチェック必須化",
	}
	if got := incompleteItemLabelEnding(item); got != "" {
		t.Fatalf("ending=%q, want complete nominal decision", got)
	}
}
