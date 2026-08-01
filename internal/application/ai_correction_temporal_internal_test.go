package application

import (
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestSelfContainedCorrectionWithoutTargetReconstructsFact(t *testing.T) {
	text := "完全なアクセスポート設定ではありません。トランク設定自体は入っていましたが、許可するVLANの一覧からVLAN30が漏れていました。"
	scope := evidenceScopeFromTexts(map[int64]string{1: text}, 1)
	stats := &liveAnalysisTreeMergeStats{}

	items := synthesizeCorrectionFactItems(nil, nil, scope, classifyDiscourseTimeline(scope), stats)
	if len(items) != 1 {
		t.Fatalf("reconstructed items=%d, want 1: correction=%t statement=%q selfContained=%t items=%+v stats=%+v",
			len(items), discourseCorrectionPattern.MatchString(text), correctionReplacementStatement(text),
			selfContainedCorrectionFact(correctionReplacementStatement(text), text), items, stats)
	}
	item := items[0]
	if item.Kind != "fact" || !strings.Contains(item.Title, "VLAN30") ||
		!strings.Contains(item.Title, "漏れて") || strings.Contains(item.Title, "完全なアクセスポート") ||
		!equalInt64s(item.EvidenceSequenceNos, []int64{1}) ||
		stats.CorrectionItemsReconstructed != 1 {
		t.Fatalf("reconstructed item=%+v stats=%+v", item, stats)
	}
}

func TestSplitCorrectionContinuationReconstructsDistinctTrunkAndVLANFacts(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{SequenceNo: 1, IsFinal: true, SpeakerName: "田中", Text: "交換後スイッチはアクセスポート設定でした。"},
		{SequenceNo: 2, IsFinal: true, SpeakerName: "田中", Text: "いえ、正確には完全なアクセスポート設定ではありません。"},
		{SequenceNo: 3, IsFinal: true, SpeakerName: "田中", Text: "トランク設定自体は入っていましたが、許可VLAN一覧からVLAN30が漏れていました。"},
	}
	scope, timeline := agendaTimelineFromSegments(segments)
	items := synthesizeCorrectionFactItems(nil, nil, scope, timeline, &liveAnalysisTreeMergeStats{})
	if len(items) != 2 {
		t.Fatalf("split correction replacements=%+v clauses=%+v items=%+v",
			correctionReplacementStatements(2, scope, timeline),
			splitCorrectionContinuationFacts(scope.TranscriptText[3]), items)
	}
	joined := items[0].Title + " " + items[1].Title
	if !strings.Contains(joined, "トランク") || !strings.Contains(joined, "VLAN30") {
		t.Fatalf("split correction facts lost an atomic proposition: %+v", items)
	}
}

func TestReferenceDependentCorrectionWithoutTargetDoesNotCreateItem(t *testing.T) {
	for _, text := range []string{
		"いや、違います。",
		"正確にはそうではありません。",
		"その設定ではありません。",
	} {
		t.Run(text, func(t *testing.T) {
			scope := evidenceScopeFromTexts(map[int64]string{1: text}, 1)
			items := synthesizeCorrectionFactItems(nil, nil, scope, classifyDiscourseTimeline(scope), &liveAnalysisTreeMergeStats{})
			if len(items) != 0 {
				t.Fatalf("reference-dependent correction created items: %+v", items)
			}
		})
	}
}

func TestSelfContainedCorrectionDoesNotDuplicateExistingFact(t *testing.T) {
	text := "正確には、許可VLAN一覧からVLAN30が漏れていました。"
	scope := evidenceScopeFromTexts(map[int64]string{4: text}, 4)
	existing := liveAnalysisItem{
		ID: "fact-vlan30", Kind: "fact", Title: "許可VLAN一覧からVLAN30が漏れていた",
		Body: "許可VLAN一覧からVLAN30が漏れていた", Status: "open", EvidenceSequenceNos: []int64{4},
	}
	items := synthesizeCorrectionFactItems([]liveAnalysisItem{existing}, nil, scope, classifyDiscourseTimeline(scope), &liveAnalysisTreeMergeStats{})
	if len(items) != 0 {
		t.Fatalf("duplicate correction fact created: %+v", items)
	}
}

func TestSelfContainedCorrectionRejectsUnsupportedOrQualifierMismatchedReplacement(t *testing.T) {
	tests := []struct {
		name      string
		statement string
		evidence  string
	}{
		{
			name:      "unsupported cause",
			statement: "外部攻撃が原因でした",
			evidence:  "正確には、許可VLAN一覧からVLAN30が漏れていました。",
		},
		{
			name:      "different VLAN",
			statement: "許可VLAN一覧からVLAN20が漏れていました",
			evidence:  "正確には、許可VLAN一覧からVLAN30が漏れていました。",
		},
		{
			name:      "different floor",
			statement: "2階のスイッチで設定漏れがありました",
			evidence:  "正確には、3階のスイッチで設定漏れがありました。",
		},
		{
			name:      "different weekday",
			statement: "証明書の更新期限は月曜日でした",
			evidence:  "正確には、証明書の更新期限は金曜日でした。",
		},
		{
			name:      "different switch",
			statement: "旧スイッチでVLAN30が漏れていました",
			evidence:  "正確には、交換後スイッチでVLAN30が漏れていました。",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if selfContainedCorrectionFact(test.statement, test.evidence) {
				t.Fatalf("unsupported or qualifier-mismatched replacement was accepted: statement=%q evidence=%q",
					test.statement, test.evidence)
			}
		})
	}
}

func TestSelfContainedCorrectionUsesInitialAgendaOnlyBeforeFirstTransition(t *testing.T) {
	context := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-impact", Title: "障害の影響範囲", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-cause", Title: "原因調査", Order: 2, Role: agendaRolePrimary},
	}}
	item := liveAnalysisItem{
		ID: "corrected-fact", Kind: "fact", AssignmentReason: deterministicCorrectionAssignmentReason,
		EvidenceSequenceNos: []int64{3},
	}
	spans := []agendaContextSpan{{Mode: agendaContextModeFixed, AgendaID: "agenda-cause", StartSequenceNo: 8, EndSequenceNo: 12}}
	if agenda := initialAgendaForSelfContainedCorrection(item, nil, spans, context); agenda.ID != "agenda-impact" {
		t.Fatalf("initial agenda fallback=%+v", agenda)
	}
	item.AssignmentReason = "model"
	if agenda := initialAgendaForSelfContainedCorrection(item, nil, spans, context); agenda.ID != "" {
		t.Fatalf("ordinary item received correction fallback=%+v", agenda)
	}
	item.AssignmentReason = deterministicCorrectionAssignmentReason
	item.EvidenceSequenceNos = []int64{9}
	if agenda := initialAgendaForSelfContainedCorrection(item, nil, spans, context); agenda.ID != "" {
		t.Fatalf("correction inside an active span bypassed normal reconciliation=%+v", agenda)
	}
	item.EvidenceSequenceNos = []int64{13}
	if agenda := initialAgendaForSelfContainedCorrection(item, nil, spans, context); agenda.ID != "" {
		t.Fatalf("correction after a recorded transition reused initial agenda=%+v", agenda)
	}
	item.EvidenceSequenceNos = []int64{3}
	companion := liveAnalysisItem{
		ID: "cause-hypothesis", Kind: "issue", Title: "VLAN30設定漏れが障害原因の可能性",
		Body: "VLAN30設定漏れが障害原因の可能性が高い", EvidenceSequenceNos: []int64{4},
		observedInCurrentBatch: true,
	}
	item.Title, item.Body = "許可VLAN一覧からVLAN30が漏れていた", "許可VLAN一覧からVLAN30が漏れていた"
	if agenda := initialAgendaForSelfContainedCorrection(item, []liveAnalysisItem{item, companion}, spans, context); agenda.ID != "" {
		t.Fatalf("same-round semantic companion was split by initial agenda fallback=%+v", agenda)
	}
}

func TestPastObservationIsFactAndCannotBeResolvedByLaterRecovery(t *testing.T) {
	text := "インターネットが完全に停止したわけではなく、接続できる端末と接続できない端末が混在していました。"
	item := liveAnalysisItem{
		ID: "historical-connectivity", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "障害時の端末別接続可否", Body: text, Status: "open", EvidenceSequenceNos: []int64{1},
	}
	scope := evidenceScopeFromTexts(map[int64]string{1: text}, 1)
	decision := evaluateLiveItemKind(item, scope, "past_observation_regression")
	if decision.CanonicalKind != "fact" || decision.Features.TemporalScope != "past" ||
		decision.Features.EpistemicStatus != "confirmed" {
		t.Fatalf("past observation decision=%+v past=%t explicitCurrent=%t", decision,
			kindPastObservationPattern.MatchString(text), kindExplicitCurrentIssuePattern.MatchString(text))
	}
	if recoveryClosureEligibleItem(item, "午前10時5分にすべての端末で接続が正常になりました。") {
		t.Fatal("historical observation remained eligible for resolution")
	}
}

func TestCurrentUnresolvedIssueRemainsResolvable(t *testing.T) {
	text := "現在も一部端末で接続できません。原因はまだ分かっておらず、調査が必要です。"
	item := liveAnalysisItem{
		ID: "current-connectivity", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title: "現在も一部端末で接続できない", Body: text, Status: "open", EvidenceSequenceNos: []int64{1},
	}
	decision := evaluateLiveItemKind(item, evidenceScopeFromTexts(map[int64]string{1: text}, 1), "current_issue_regression")
	if decision.CanonicalKind != "issue" || decision.Features.TemporalScope != "current" ||
		decision.Features.EpistemicStatus != "unresolved" {
		t.Fatalf("current issue decision=%+v", decision)
	}
	if !recoveryClosureEligibleItem(item, "設定を修正し、すべての端末で接続が正常になりました。") {
		t.Fatalf("current unresolved issue was not eligible for matching recovery: features=%+v similarity=%v",
			inferItemSemanticFeatures(item, liveEvidenceScope{}),
			semanticItemSimilarity(item.Title+" "+item.Body, "設定を修正し、すべての端末で接続が正常になりました。"))
	}
}

func TestPastObservationAndCurrentIssueSplitIntoDistinctKinds(t *testing.T) {
	text := "午前9時には接続できる端末とできない端末が混在していました。現在は全端末で接続できません。"
	item := liveAnalysisItem{
		ID: "mixed-connectivity", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: text, Body: text, Status: "open", EvidenceSequenceNos: []int64{1},
	}
	scope := evidenceScopeFromTexts(map[int64]string{1: text}, 1)
	fragments := strongKindFragments(item, scope)
	if len(fragments) != 2 || fragments[0].Kind != "fact" || fragments[1].Kind != "issue" {
		t.Fatalf("mixed temporal fragments=%+v", fragments)
	}
}

func TestHistoricalResolutionStatementIsFactNotCurrentIssue(t *testing.T) {
	text := "昨日発生した通信遅延は、設定修正で解消しました。"
	item := liveAnalysisItem{ID: "historical-resolution", Kind: "issue", Title: text, Body: text, Status: "open", EvidenceSequenceNos: []int64{1}}
	decision := evaluateLiveItemKind(item, evidenceScopeFromTexts(map[int64]string{1: text}, 1), "historical_resolution")
	if decision.CanonicalKind != "fact" || decision.Features.TemporalScope != "past" {
		t.Fatalf("historical resolution decision=%+v", decision)
	}
}

func TestSemanticRelationsSurviveDifferentAgendaBranchesWhenEvidenceIsAdjacent(t *testing.T) {
	items := []liveAnalysisItem{
		{
			ID: "vlan-fact", Kind: "fact", Title: "許可VLAN一覧からVLAN30が漏れていた",
			Body:   "交換後スイッチの許可VLAN一覧からVLAN30が漏れていました。",
			Status: "open", EvidenceSequenceNos: []int64{1},
		},
		{
			ID: "cause-hypothesis", Kind: "issue", Subtype: issueSubtypeInvestigation,
			Title:  "VLAN30設定漏れが3階障害の直接原因である可能性が高い",
			Body:   "現時点では、この設定漏れが3階障害の直接原因である可能性が最も高いと考えています。",
			Status: "open", EvidenceSequenceNos: []int64{2},
		},
		{
			ID: "scope-limit", Kind: "issue", Subtype: issueSubtypeConfirmation,
			Title:  "2階の通信遅延までこの設定漏れで説明できるかは未確認",
			Body:   "ただし2階の通信遅延までこの設定漏れで説明できるかは未確認です。",
			Status: "open", EvidenceSequenceNos: []int64{3},
		},
	}
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: "root", Kind: "root", Label: "議論ツリー"},
		{ID: "agenda-fact", Kind: "topic", ParentID: "root", Label: "確認済み設定"},
		{ID: "agenda-analysis", Kind: "topic", ParentID: "root", Label: "原因分析"},
		{ID: "vlan-fact", Kind: "fact", ParentID: "agenda-fact", Label: items[0].Title},
		{ID: "cause-hypothesis", Kind: "issue", ParentID: "agenda-analysis", Label: items[1].Title},
		{ID: "scope-limit", Kind: "issue", ParentID: "agenda-fact", Label: items[2].Title},
	}}

	reconcileSemanticKindRelations(tree, items, liveEvidenceScope{}, 3, "final_repair")
	relations := make(map[string]bool, len(tree.Relations))
	for _, relation := range tree.Relations {
		relations[relationKey(relation)] = true
	}
	for _, key := range []string{
		"cause-hypothesis\x00" + itemRelationSupportedBy + "\x00vlan-fact",
		"scope-limit\x00" + itemRelationLimits + "\x00cause-hypothesis",
	} {
		if !relations[key] {
			t.Fatalf("cross-agenda semantic relation %q missing: %+v", key, tree.Relations)
		}
	}
}

func TestSafeAdjacentSettingReferentRejectsAmbiguousVLANs(t *testing.T) {
	tests := []struct {
		name     string
		segments []domain.TranscriptSegment
		item     liveAnalysisItem
		want     bool
	}{
		{
			name: "unique VLAN30",
			segments: []domain.TranscriptSegment{
				{SequenceNo: 1, IsFinal: true, SpeakerName: "田中", Text: "許可VLAN一覧からVLAN30が漏れていました。"},
				{SequenceNo: 2, IsFinal: true, SpeakerName: "佐藤", Text: "現時点では、この設定漏れが3階障害の直接原因である可能性が高いです。"},
			},
			item: liveAnalysisItem{Kind: "issue", Title: "この設定漏れが3階障害の直接原因である可能性が高い", EvidenceSequenceNos: []int64{2}},
			want: true,
		},
		{
			name: "two qualifiers in antecedent",
			segments: []domain.TranscriptSegment{
				{SequenceNo: 1, IsFinal: true, SpeakerName: "田中", Text: "VLAN20とVLAN30の設定漏れを確認しました。"},
				{SequenceNo: 2, IsFinal: true, SpeakerName: "佐藤", Text: "この設定漏れが直接原因かは未確認です。"},
			},
			item: liveAnalysisItem{Kind: "issue", Title: "この設定漏れが直接原因かは未確認", EvidenceSequenceNos: []int64{2}},
		},
		{
			name: "competing adjacent antecedents",
			segments: []domain.TranscriptSegment{
				{SequenceNo: 1, IsFinal: true, SpeakerName: "田中", Text: "許可VLAN一覧からVLAN20が漏れていました。"},
				{SequenceNo: 2, IsFinal: true, SpeakerName: "田中", Text: "許可VLAN一覧からVLAN30が漏れていました。"},
				{SequenceNo: 3, IsFinal: true, SpeakerName: "佐藤", Text: "この設定漏れが直接原因かは未確認です。"},
			},
			item: liveAnalysisItem{Kind: "issue", Title: "この設定漏れが直接原因かは未確認", EvidenceSequenceNos: []int64{3}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope, timeline := agendaTimelineFromSegments(test.segments)
			sequenceNo := test.segments[len(test.segments)-1].SequenceNo
			qualifier, got := safeAdjacentSettingReferent(test.item, sequenceNo, scope, timeline)
			if got != test.want {
				t.Fatalf("safe=%t qualifier=%q want safe=%t", got, qualifier, test.want)
			}
			if got && qualifier != "VLAN30" {
				t.Fatalf("qualifier=%q, want VLAN30", qualifier)
			}
		})
	}
}

func TestManualCorrectionRelationIsNotRevalidated(t *testing.T) {
	state := correctionRelationTestState("manual_user_edit")
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "交換後スイッチはアクセスポート設定でした。",
		2: "交換後スイッチの許可VLAN一覧にはVLAN30も含まれていました。",
		3: "正確には、交換後スイッチの許可VLAN一覧からVLAN30が漏れていました。",
	}, 3)

	repairCorrectionSupersessions(&state, scope, classifyDiscourseTimeline(scope), 4, &liveAnalysisTreeMergeStats{})

	if relation := state.CorrectionRelations[0]; relation.TargetItemID != "manual-target" ||
		relation.TargetSequenceNo != 1 || relation.Origin != "manual_user_edit" {
		t.Fatalf("manual correction relation was overwritten: %+v", relation)
	}
}

func correctionRelationTestState(origin string) liveAnalysisPayload {
	items := []liveAnalysisItem{
		{ID: "manual-target", Kind: "fact", Title: "交換後スイッチはアクセスポート設定だった", EvidenceSequenceNos: []int64{1}},
		{ID: "semantic-target", Kind: "fact", Title: "許可VLAN一覧にはVLAN30も含まれていた", EvidenceSequenceNos: []int64{2}},
		{ID: "replacement", Kind: "fact", Title: "許可VLAN一覧からVLAN30が漏れていた", EvidenceSequenceNos: []int64{3}},
	}
	return liveAnalysisPayload{
		Items: items,
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: "root", Kind: "root", Label: "議論ツリー"},
			{ID: "manual-target", Kind: "fact", ParentID: "root", Label: items[0].Title},
			{ID: "semantic-target", Kind: "fact", ParentID: "root", Label: items[1].Title},
			{ID: "replacement", Kind: "fact", ParentID: "root", Label: items[2].Title},
		}},
		CorrectionRelations: []correctionRelation{{
			SourceSequenceNo: 3, TargetSequenceNo: 1, TargetItemID: "manual-target",
			ReplacementItemID: "replacement", Status: "superseded", Confidence: 0.99,
			Locked: true, Origin: origin, EstablishedAtVersion: 2,
		}},
	}
}
