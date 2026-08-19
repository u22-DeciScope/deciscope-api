package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func labelEvidenceScopeFromSegments(segments ...domain.TranscriptSegment) liveEvidenceScope {
	scope := liveEvidenceScope{
		Allowed: map[int64]struct{}{}, CurrentRound: map[int64]struct{}{},
		TranscriptText: map[int64]string{}, Segments: map[int64]domain.TranscriptSegment{},
	}
	for _, segment := range segments {
		scope.Allowed[segment.SequenceNo] = struct{}{}
		scope.CurrentRound[segment.SequenceNo] = struct{}{}
		scope.TranscriptText[segment.SequenceNo] = segment.Text
		scope.Segments[segment.SequenceNo] = segment
		if segment.SequenceNo > scope.CoveredThrough {
			scope.CoveredThrough = segment.SequenceNo
		}
	}
	return scope
}

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
	if node := liveTreeNodeByID(state.Tree, item.ID); node == nil || node.Label != repaired.Title ||
		node.LabelResolution == nil || node.LabelResolution.Status != "rewritten" {
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

func TestIncompleteRiskLabelFallsBackToCompleteGroundedProposition(t *testing.T) {
	scope := labelEvidenceScopeFromSegments(
		domain.TranscriptSegment{SequenceNo: 1, SpeakerID: "speaker-a", Text: "VPN証明書は来週失効する。", IsFinal: true},
		domain.TranscriptSegment{SequenceNo: 2, SpeakerID: "speaker-a", Text: "放置するとリモート接続できなくなる可能性がある。", IsFinal: true},
	)
	timeline := classifyDiscourseTimeline(scope)
	item := liveAnalysisItem{
		ID: "risk-vpn", Kind: "risk",
		Title:               "VPN証明書を放置するとリモート接続ができてい",
		Body:                "VPN証明書を放置するとリモート接続ができてい",
		EvidenceSequenceNos: []int64{1, 2},
		GroundingDecision:   "accepted",
	}

	repaired, decision, changed := repairIncompleteItemLabel(item, scope, timeline)
	if !changed || decision.FinalDecision == "rejected" {
		t.Fatalf("repair failed: item=%+v decision=%+v changed=%t", repaired, decision, changed)
	}
	if !strings.Contains(repaired.Title, "VPN証明書") ||
		!strings.Contains(repaired.Title, "リモート接続") ||
		!strings.Contains(repaired.Title, "可能性") ||
		incompleteItemLabelEnding(repaired) != "" {
		t.Fatalf("fallback lost the grounded risk proposition: %+v decision=%+v", repaired, decision)
	}
	if strings.Contains(repaired.Title, "来週失効") {
		t.Fatalf("fallback copied the antecedent fact instead of only its referent: %+v", repaired)
	}
}

func TestGroundingRewriteRiskRestoresAdjacentReferentEvidence(t *testing.T) {
	scope := labelEvidenceScopeFromSegments(
		domain.TranscriptSegment{SequenceNo: 1, SpeakerID: "speaker-a", Text: "VPN証明書は来週失効します。", IsFinal: true},
		domain.TranscriptSegment{SequenceNo: 2, SpeakerID: "speaker-a", Text: "放置すると全社員がリモート接続できなくなる可能性があります。", IsFinal: true},
	)
	timeline := classifyDiscourseTimeline(scope)
	item := liveAnalysisItem{
		ID: "risk-vpn", Kind: "risk",
		Title:               "放置すると全社員がリモート接続できなくなる可能性があります",
		Body:                "放置すると全社員がリモート接続できなくなる可能性があります",
		EvidenceSequenceNos: []int64{2}, GroundingDecision: "rewritten",
	}
	repaired, decision, changed := repairIncompleteItemLabel(item, scope, timeline)
	if !changed || !equalInt64s(repaired.EvidenceSequenceNos, []int64{1, 2}) ||
		!strings.Contains(repaired.Title, "VPN証明書") || decision.FinalDecision != "fallback_applied" {
		t.Fatalf("repaired=%+v decision=%+v changed=%t", repaired, decision, changed)
	}
}

func TestAntecedentFallbackRequiresSafeUniqueSameSpeakerReferent(t *testing.T) {
	tests := []struct {
		name          string
		segments      []domain.TranscriptSegment
		itemEvidence  []int64
		forbiddenText []string
		wantEvidence  []int64
	}{
		{
			name: "speaker switch",
			segments: []domain.TranscriptSegment{
				{SequenceNo: 1, SpeakerID: "speaker-a", Text: "VPN証明書は来週失効します。", IsFinal: true},
				{SequenceNo: 2, SpeakerID: "speaker-b", Text: "放置すると接続不能になる可能性があります。", IsFinal: true},
			},
			itemEvidence: []int64{1, 2}, forbiddenText: []string{"VPN証明書"}, wantEvidence: []int64{2},
		},
		{
			name: "unrelated speaker switch",
			segments: []domain.TranscriptSegment{
				{SequenceNo: 1, SpeakerID: "speaker-a", Text: "DB容量は来週上限に達します。", IsFinal: true},
				{SequenceNo: 2, SpeakerID: "speaker-b", Text: "放置すると全社員がリモート接続できなくなる可能性があります。", IsFinal: true},
			},
			itemEvidence: []int64{1, 2}, forbiddenText: []string{"DB容量"}, wantEvidence: []int64{2},
		},
		{
			name: "same speaker topic switch",
			segments: []domain.TranscriptSegment{
				{SequenceNo: 1, SpeakerID: "speaker-a", Text: "VPN証明書は来週失効します。", IsFinal: true},
				{SequenceNo: 2, SpeakerID: "speaker-a", Text: "DB容量も不足しています。", IsFinal: true},
				{SequenceNo: 3, SpeakerID: "speaker-a", Text: "放置するとリモート接続できなくなる可能性があります。", IsFinal: true},
			},
			itemEvidence: []int64{1, 2, 3}, forbiddenText: []string{"DB容量", "VPN証明書"}, wantEvidence: []int64{3},
		},
		{
			name: "multiple antecedent subjects",
			segments: []domain.TranscriptSegment{
				{SequenceNo: 1, SpeakerID: "speaker-a", Text: "VPN証明書とDB容量は来週確認が必要です。", IsFinal: true},
				{SequenceNo: 2, SpeakerID: "speaker-a", Text: "放置するとサービスが停止する可能性があります。", IsFinal: true},
			},
			itemEvidence: []int64{1, 2}, forbiddenText: []string{"VPN証明書", "DB容量"}, wantEvidence: []int64{2},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope := labelEvidenceScopeFromSegments(test.segments...)
			item := liveAnalysisItem{
				ID: "risk-conditional", Kind: "risk",
				Title:               strings.Trim(scope.TranscriptText[test.itemEvidence[len(test.itemEvidence)-1]], "。"),
				Body:                strings.Trim(scope.TranscriptText[test.itemEvidence[len(test.itemEvidence)-1]], "。"),
				EvidenceSequenceNos: append([]int64(nil), test.itemEvidence...), GroundingDecision: "rewritten",
			}
			repaired, decision, changed := repairIncompleteItemLabel(item, scope, classifyDiscourseTimeline(scope))
			if !changed || repaired.Kind != "risk" || !strings.Contains(repaired.Title, "可能性") {
				t.Fatalf("safe current-clause fallback missing: repaired=%+v decision=%+v changed=%t", repaired, decision, changed)
			}
			for _, forbidden := range test.forbiddenText {
				if strings.Contains(repaired.Title, forbidden) {
					t.Fatalf("ambiguous antecedent %q was synthesized: repaired=%+v decision=%+v", forbidden, repaired, decision)
				}
			}
			if !equalInt64s(repaired.EvidenceSequenceNos, test.wantEvidence) {
				t.Fatalf("unused antecedent evidence was retained: got=%v want=%v repaired=%+v", repaired.EvidenceSequenceNos, test.wantEvidence, repaired)
			}
			if repaired.LabelResolution == nil || repaired.LabelResolution.Status != "fallback_applied" ||
				!equalInt64s(repaired.LabelResolution.SourceEvidenceSequenceNos, test.wantEvidence) {
				t.Fatalf("fallback provenance missing: %+v", repaired.LabelResolution)
			}
		})
	}
}

func TestAntecedentFallbackAllowsUniqueContinuousSameSpeakerRisk(t *testing.T) {
	scope := labelEvidenceScopeFromSegments(
		domain.TranscriptSegment{SequenceNo: 1, SpeakerID: "speaker-a", Text: "VPN証明書は来週失効します。", IsFinal: true},
		domain.TranscriptSegment{SequenceNo: 2, SpeakerID: "speaker-a", Text: "放置すると全社員がリモート接続できなくなる可能性があります。", IsFinal: true},
	)
	item := liveAnalysisItem{
		ID: "risk-vpn", Kind: "risk",
		Title:               "放置すると全社員がリモート接続できなくなる可能性があります",
		Body:                "放置すると全社員がリモート接続できなくなる可能性があります",
		EvidenceSequenceNos: []int64{2}, GroundingDecision: "rewritten",
	}
	repaired, decision, changed := repairIncompleteItemLabel(item, scope, classifyDiscourseTimeline(scope))
	if !changed || repaired.Kind != "risk" || !strings.Contains(repaired.Title, "VPN証明書") ||
		!strings.Contains(repaired.Title, "可能性") || !equalInt64s(repaired.EvidenceSequenceNos, []int64{1, 2}) {
		t.Fatalf("unique antecedent was not safely restored: repaired=%+v decision=%+v changed=%t", repaired, decision, changed)
	}
	if repaired.LabelResolution == nil || repaired.LabelResolution.Status != "fallback_applied" ||
		!equalInt64s(repaired.LabelResolution.SourceEvidenceSequenceNos, []int64{1, 2}) {
		t.Fatalf("antecedent provenance missing: %+v", repaired.LabelResolution)
	}
}

func TestAntecedentFallbackCurrentClauseRiskRemainsSelfContained(t *testing.T) {
	item := liveAnalysisItem{
		ID: "risk-current-only", Kind: "risk",
		Title:               "放置すると接続不能になる可能性があります",
		Body:                "放置すると接続不能になる可能性があります",
		EvidenceSequenceNos: []int64{2}, GroundingDecision: "rewritten",
	}
	scope := evidenceScopeFromTexts(map[int64]string{2: item.Title + "。"}, 2)
	label := deterministicCurrentConditionalRiskLabel(item.Title)
	if !itemLabelCandidatePreservesSemantics(item, label, scope) {
		features := inferItemSemanticFeatures(liveAnalysisItem{Kind: "risk", Title: label, Body: label}, liveEvidenceScope{})
		t.Fatalf("current-only label rejected: label=%q ending=%q needsReferent=%t features=%+v", label,
			incompleteItemLabelEnding(liveAnalysisItem{Kind: "risk", Title: label, Body: label}),
			liveItemTextNeedsReferent(liveAnalysisItem{Kind: "risk", Title: label, Body: label}), features)
	}
}

func TestAntecedentFallbackRejectsQualifierAndSubjectConflicts(t *testing.T) {
	tests := []struct {
		name       string
		antecedent string
		current    string
		forbidden  string
	}{
		{name: "floor mismatch", antecedent: "3階のVPN証明書は来週失効します。", current: "放置すると2階で接続不能になる可能性があります。", forbidden: "3階"},
		{name: "weekday mismatch", antecedent: "VPN証明書は月曜日に失効します。", current: "放置すると金曜日に接続不能になる可能性があります。", forbidden: "月曜日"},
		{name: "subject mismatch", antecedent: "DB容量は来週上限に達します。", current: "VPN証明書を放置すると接続不能になる可能性があります。", forbidden: "DB容量"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope := labelEvidenceScopeFromSegments(
				domain.TranscriptSegment{SequenceNo: 1, SpeakerID: "speaker-a", Text: test.antecedent, IsFinal: true},
				domain.TranscriptSegment{SequenceNo: 2, SpeakerID: "speaker-a", Text: test.current, IsFinal: true},
			)
			item := liveAnalysisItem{ID: "risk-conflict", Kind: "risk", Title: strings.Trim(test.current, "。"), Body: strings.Trim(test.current, "。"), EvidenceSequenceNos: []int64{1, 2}, GroundingDecision: "rewritten"}
			repaired, _, _ := repairIncompleteItemLabel(item, scope, classifyDiscourseTimeline(scope))
			if strings.Contains(repaired.Title, test.forbidden) || !equalInt64s(repaired.EvidenceSequenceNos, []int64{2}) {
				t.Fatalf("conflicting antecedent was used: repaired=%+v", repaired)
			}
		})
	}
}

func TestContextDependentFactLabelFallsBackToConcreteEvidence(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "完全なアクセスポート設定ではありません。",
		2: "トランク設定は入っていました。",
		3: "交換後スイッチの許可VLAN一覧からVLAN30が漏れていました。",
	}, 1, 2, 3)
	timeline := classifyDiscourseTimeline(scope)
	item := liveAnalysisItem{
		ID: "fact-vlan", Kind: "fact",
		Title:               "完全なアクセスポート設定ではありません",
		Body:                "完全なアクセスポート設定ではありません",
		EvidenceSequenceNos: []int64{1, 2, 3},
		GroundingDecision:   "accepted",
	}

	repaired, decision, changed := repairIncompleteItemLabel(item, scope, timeline)
	if !changed || !strings.Contains(repaired.Title, "交換後スイッチ") ||
		!strings.Contains(repaired.Title, "VLAN30") ||
		!strings.Contains(repaired.Title, "漏れて") {
		t.Fatalf("context-dependent label was not replaced: item=%+v decision=%+v changed=%t", repaired, decision, changed)
	}
}

func TestTruncatedCausalHypothesisFallsBackWithoutBecomingFact(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "交換後スイッチの許可VLAN一覧からVLAN30が漏れていました。",
		2: "現時点では、この設定漏れが3階障害の直接原因である可能性が最も高いと考えています。",
	}, 1, 2)
	timeline := classifyDiscourseTimeline(scope)
	item := liveAnalysisItem{
		ID: "cause-hypothesis", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title:               "現時点では、この設定漏れが障害の",
		Body:                "現時点では、この設定漏れが障害の",
		EvidenceSequenceNos: []int64{1, 2},
		GroundingDecision:   "accepted",
	}

	repaired, decision, changed := repairIncompleteItemLabel(item, scope, timeline)
	features := inferItemSemanticFeatures(repaired, liveEvidenceScope{})
	if !changed || !features.CausalHypothesisPresent ||
		!strings.Contains(repaired.Title, "3階") ||
		!strings.Contains(repaired.Title, "可能性") ||
		incompleteItemLabelEnding(repaired) != "" {
		t.Fatalf("hypothesis semantics were not preserved: item=%+v features=%+v decision=%+v changed=%t", repaired, features, decision, changed)
	}
}

func TestLabelFallbackDoesNotRescueInvalidOrChangeQualifiers(t *testing.T) {
	tests := []struct {
		name  string
		item  liveAnalysisItem
		texts map[int64]string
	}{
		{
			name:  "unsupported risk",
			item:  liveAnalysisItem{ID: "unsupported", Kind: "risk", Title: "未発話の損失を確認してい", Body: "未発話の損失を確認してい", EvidenceSequenceNos: []int64{1}, GroundingDecision: "rejected"},
			texts: map[int64]string{1: "売上は計画どおりでした。"},
		},
		{
			name:  "different subject risk",
			item:  liveAnalysisItem{ID: "risk-vpn", Kind: "risk", Title: "VPN証明書が原因で接続できてい", Body: "VPN証明書が原因で接続できてい", EvidenceSequenceNos: []int64{1}, GroundingDecision: "accepted"},
			texts: map[int64]string{1: "DB容量を放置すると検索が停止する可能性がある。"},
		},
		{
			name:  "subjectless uncertainty fragment",
			item:  liveAnalysisItem{ID: "fragment", Kind: "risk", Title: "かもしれないの", Body: "かもしれないの", EvidenceSequenceNos: []int64{1}, GroundingDecision: "accepted"},
			texts: map[int64]string{1: "かもしれない。"},
		},
		{
			name:  "floor mismatch",
			item:  liveAnalysisItem{ID: "floor", Kind: "issue", Subtype: issueSubtypeInvestigation, Title: "3階障害の原因である可能性が", Body: "3階障害の原因である可能性が", EvidenceSequenceNos: []int64{1}, GroundingDecision: "accepted"},
			texts: map[int64]string{1: "2階障害の原因である可能性が高い。"},
		},
		{
			name:  "weekday mismatch",
			item:  liveAnalysisItem{ID: "weekday", Kind: "todo", Title: "高橋さんが月曜日に更新を実施して", Body: "高橋さんが月曜日に更新を実施して", EvidenceSequenceNos: []int64{1}, GroundingDecision: "accepted"},
			texts: map[int64]string{1: "高橋さんが金曜日に更新を実施します。"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope := evidenceScopeFromTexts(test.texts, 1)
			timeline := classifyDiscourseTimeline(scope)
			repaired, decision, changed := repairIncompleteItemLabel(test.item, scope, timeline)
			if changed || repaired.Title != test.item.Title || decision.FinalDecision != "rejected" {
				t.Fatalf("invalid proposition was rescued or changed: repaired=%+v decision=%+v changed=%t", repaired, decision, changed)
			}
		})
	}
}

func TestLabelFailureRetentionRequiresGroundingAndSupportedEvidence(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "VPN証明書は来週失効し、放置すると接続不能になる可能性がある。",
	}, 1)
	timeline := classifyDiscourseTimeline(scope)
	base := liveAnalysisItem{
		ID: "risk-vpn", Kind: "risk",
		Title: "VPN証明書を放置すると接続できてい", Body: "VPN証明書を放置すると接続できてい",
		EvidenceSequenceNos: []int64{1}, GroundingDecision: "accepted",
	}
	if !labelFailureRetentionEligible(base, scope, timeline) {
		t.Fatal("grounded, kind-valid risk should be eligible for degraded retention")
	}
	for _, mutate := range []func(*liveAnalysisItem){
		func(item *liveAnalysisItem) { item.GroundingDecision = "rejected" },
		func(item *liveAnalysisItem) { item.GroundingUnsupportedAtomHashes = []string{"unsupported"} },
		func(item *liveAnalysisItem) { item.EvidenceSequenceNos = []int64{99} },
	} {
		item := base
		mutate(&item)
		if labelFailureRetentionEligible(item, scope, timeline) {
			t.Fatalf("invalid item became eligible: %+v", item)
		}
	}
}

func TestLabelResolutionPersistsRetainedDegradedWithoutChangingExistingEnums(t *testing.T) {
	scope := labelEvidenceScopeFromSegments(domain.TranscriptSegment{
		SequenceNo: 2, SpeakerID: "speaker-a",
		Text: "その場合は接続できなくなる可能性があります。", IsFinal: true,
	})
	item := liveAnalysisItem{
		ID: "risk-retained", Kind: "risk", Status: "open",
		ClassificationStatus: classificationAssigned, GroundingDecision: "accepted",
		Title:               "その場合は接続できなくなる可能性があります",
		Body:                "その場合は接続できなくなる可能性があります",
		EvidenceSequenceNos: []int64{2},
	}
	repaired, decision, changed := repairIncompleteItemLabel(item, scope, classifyDiscourseTimeline(scope))
	if !changed || decision.FinalDecision != "retained_degraded" || repaired.LabelResolution == nil ||
		repaired.LabelResolution.Status != "retained_degraded" || repaired.Status != "open" ||
		repaired.ClassificationStatus != classificationAssigned || repaired.GroundingDecision != "accepted" ||
		!equalInt64s(repaired.LabelResolution.SourceEvidenceSequenceNos, []int64{2}) {
		t.Fatalf("retained metadata/enums = item=%+v decision=%+v changed=%t", repaired, decision, changed)
	}
	raw, err := json.Marshal(repaired)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded liveAnalysisItem
	if err := json.Unmarshal(raw, &reloaded); err != nil || reloaded.LabelResolution == nil ||
		reloaded.LabelResolution.Status != "retained_degraded" {
		t.Fatalf("label resolution did not survive JSON reload: item=%+v err=%v raw=%s", reloaded, err, raw)
	}
}
