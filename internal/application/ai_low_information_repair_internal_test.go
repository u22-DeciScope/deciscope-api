package application

import (
	"strings"
	"testing"
)

func TestGenericQuestionWithoutSubjectIsRewrittenFromEvidence(t *testing.T) {
	if !issueTextNeedsReferent("何が原因でしたか") {
		t.Fatal("generic question must require a referent")
	}
	if issueTextNeedsReferent("領収書不足と入力ミスのどちらが主な差し戻し原因か") {
		t.Fatal("specific comparison question must retain its subject")
	}
	scope := evidenceScopeFromTexts(map[int64]string{
		4: "何が原因でしたか？",
		5: "宿泊費が上限を超えた理由を書いていなかったからです。",
	}, 4, 5)
	diff := `{"summary":"差し戻し","currentTopic":"原因","items":[{"clientKey":"cause","kind":"issue","subtype":"question","severity":"medium","title":"何が原因でしたか","body":"","status":"open","evidenceSequenceNos":[4]}],"newTopics":[],"assignments":[{"nodeId":"cause","parentTopicId":"topic-unclassified","confidence":0.6,"reason":"model"}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, nil, nil, 1, []int64{4, 5}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 1 || state.Items[0].Title == "何が原因でしたか" || !strings.Contains(state.Items[0].Title, "宿泊費") || state.Items[0].InformationStatus != informationStatusGrounded {
		t.Fatalf("items=%+v", state.Items)
	}
}

func TestPersistedGenericQuestionIsRewrittenAndRedundantGroupFlattened(t *testing.T) {
	previous := liveAnalysisItem{
		ID: "issue-question-auto-fd5894572c93", Kind: "issue", Subtype: issueSubtypeQuestion,
		Title: "何が原因でしたか", Body: "差し戻しの原因の最終的な特定が必要。", Status: "open",
		InformationStatus: informationStatusGrounded, ClassificationStatus: classificationAssigned,
		EvidenceSequenceNos: []int64{4, 7},
	}
	repaired, _ := repairLowInformationIssueItems([]liveAnalysisItem{previous}, nil, nil, discourseTimeline{}, liveEvidenceScope{}, nil)
	if len(repaired) != 1 || repaired[0].ID != previous.ID || issueTextNeedsReferent(repaired[0].Title) || !containsInt64(repaired[0].EvidenceSequenceNos, 4) || !containsInt64(repaired[0].EvidenceSequenceNos, 7) {
		t.Fatalf("repaired=%+v", repaired)
	}

	groupID, agendaTopicID := "group-4ca7e79c8f64", "topic-agenda-a5f8fcd0c7a2"
	groups := map[string]liveAnalysisTreeNode{groupID: {ID: groupID, Kind: "group", Label: "何が原因でしたか", ParentID: agendaTopicID}}
	details := map[string]liveAnalysisTreeNode{previous.ID: {ID: previous.ID, Kind: "issue", Label: repaired[0].Title, ParentID: groupID}}
	parents := map[string]string{groupID: agendaTopicID, previous.ID: groupID}
	stats := &liveAnalysisTreeMergeStats{}
	order := flattenLowInformationSingleChildGroups(repaired, groups, []string{groupID}, details, parents, stats)
	if len(order) != 0 || len(groups) != 0 || parents[previous.ID] != agendaTopicID || stats.GroupsFlattened != 1 {
		t.Fatalf("order=%v groups=%+v parents=%+v stats=%+v", order, groups, parents, stats)
	}
	if repaired[0].ID != previous.ID || len(repaired[0].EvidenceSequenceNos) != 2 {
		t.Fatalf("canonical evidence changed: %+v", repaired[0])
	}
}

func TestNormalizeSemanticClassificationMigratesLegacyKinds(t *testing.T) {
	tests := []struct {
		name                          string
		kind, subtype, status         string
		wantKind, wantSub, wantStatus string
	}{
		{name: "question", kind: "question", status: "open", wantKind: "issue", wantSub: issueSubtypeQuestion, wantStatus: "open"},
		{name: "open issue", kind: "open_issue", status: "open", wantKind: "issue", wantSub: issueSubtypeDiscussion, wantStatus: "open"},
		{name: "confirmation", kind: "confirmation", status: "open", wantKind: "issue", wantSub: issueSubtypeConfirmation, wantStatus: "open"},
		{name: "investigation", kind: "investigation", status: "open", wantKind: "issue", wantSub: issueSubtypeInvestigation, wantStatus: "open"},
		{name: "state as kind", kind: "resolved", wantKind: "issue", wantSub: issueSubtypeDiscussion, wantStatus: "resolved"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, subtype, status, changed := normalizeSemanticClassification(test.kind, test.subtype, test.status)
			if !changed || kind != test.wantKind || subtype != test.wantSub || status != test.wantStatus {
				t.Fatalf("classification=(%q,%q,%q,%t), want (%q,%q,%q,true)", kind, subtype, status, changed, test.wantKind, test.wantSub, test.wantStatus)
			}
		})
	}
}

func TestLowInformationRepairSplitsCollapsedIssueAndCopiesAssignment(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "2階のネットワーク障害では、原因と監視アラートの条件が未解決事項として残ります。",
	}, 1)
	timeline := classifyDiscourseTimeline(scope)
	stats := &liveAnalysisTreeMergeStats{}
	items, assignments := repairLowInformationIssueItems(nil, []liveAnalysisItem{{
		ID: "issue-network", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "原因とアラート条件が未解決", Status: "open", EvidenceSequenceNos: []int64{1},
	}}, []treeAssignment{{NodeID: "issue-network", ParentTopicID: "agenda-2", Confidence: .9}}, timeline, scope, stats)

	if len(items) != 2 || stats.LowInformationItemsSplit != 1 {
		t.Fatalf("items=%+v stats=%+v", items, stats)
	}
	if items[0].Title != "原因が特定できていない" || items[1].Title != "監視アラートの発報条件が確定していない" {
		t.Fatalf("split titles=%q / %q", items[0].Title, items[1].Title)
	}
	for _, item := range items {
		if len(item.EvidenceSequenceNos) != 1 || item.EvidenceSequenceNos[0] != 1 {
			t.Fatalf("split item lost source evidence: %+v", item)
		}
	}
	if len(assignments) != 2 || assignments[0].nodeID() != "issue-network-split-1" || assignments[1].nodeID() != "issue-network-split-2" {
		t.Fatalf("assignments=%+v", assignments)
	}
}

func TestLowInformationRepairRejectsBrokenSplitFragmentAndKeepsValidSibling(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "2階の通信遅延の原因と関心あなたの条件が未解決事項として残ります。",
	}, 1)
	timeline := classifyDiscourseTimeline(scope)
	stats := &liveAnalysisTreeMergeStats{}
	items, assignments := repairLowInformationIssueItems(nil, []liveAnalysisItem{{
		ID: "issue-recap", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "2階の原因と通知条件が未解決", Status: "open", EvidenceSequenceNos: []int64{1},
	}}, []treeAssignment{{NodeID: "issue-recap", ParentTopicID: "agenda-2"}}, timeline, scope, stats)

	if len(items) != 1 || !strings.Contains(items[0].Title, "2階") {
		t.Fatalf("items=%+v, want only the coherent fragment", items)
	}
	if len(assignments) != 1 || assignments[0].nodeID() != "issue-recap-split-1" {
		t.Fatalf("assignments=%+v", assignments)
	}
	if stats.LowInformationSplitFragmentsRejected != 1 ||
		len(stats.LowInformationRejections) != 1 ||
		stats.LowInformationRejections[0].Reason != "split_fragment_semantically_incoherent" {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestLowInformationRepairRejectsAllReferentOnlySplitFragments(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "この問題と当該条件が未解決事項として残ります。",
	}, 1)
	stats := &liveAnalysisTreeMergeStats{}
	items, assignments := repairLowInformationIssueItems(nil, []liveAnalysisItem{{
		ID: "issue-referents", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "この問題と当該条件が未解決", Status: "open", EvidenceSequenceNos: []int64{1},
	}}, []treeAssignment{{NodeID: "issue-referents", ParentTopicID: "agenda-2"}},
		classifyDiscourseTimeline(scope), scope, stats)

	if len(items) != 0 || len(assignments) != 0 {
		t.Fatalf("items=%+v assignments=%+v, want no independently displayed fragment", items, assignments)
	}
	if stats.LowInformationSplitFragmentsRejected != 2 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestIssueReferentGateCoversDemonstrativeForms(t *testing.T) {
	for _, text := range []string{
		"この点は未解決",
		"その問題を確認する",
		"上記について対応する",
		"当該条件が未確定",
		"前述の件を検討する",
	} {
		if !issueTextNeedsReferent(text) {
			t.Errorf("%q must require a referent", text)
		}
	}
	if issueTextNeedsReferent("2階の通信遅延の原因を確認する") {
		t.Fatal("concrete subject was rejected")
	}
}

func TestLowInformationRepairRewritesAnaphoraWithoutCrossingTopicTransition(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "強風日の基準風速は何m/sか決まっていません。",
		2: "この点は引き続き検討します。",
		3: "続いて、住民説明資料についてです。",
		4: "住民説明会の開催日が未確定です。",
	}, 1, 2, 3, 4)
	timeline := classifyDiscourseTimelineWithModel(scope, []liveUtteranceRoleRef{
		{SequenceNo: 1, Role: liveUtteranceSubstantive},
		{SequenceNo: 2, Role: liveUtteranceSubstantive},
		{SequenceNo: 3, Role: liveUtteranceDiscourseTransition},
		{SequenceNo: 4, Role: liveUtteranceSubstantive},
	})
	stats := &liveAnalysisTreeMergeStats{}
	items, _ := repairLowInformationIssueItems(nil, []liveAnalysisItem{{
		ID: "issue-wind", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "この点は引き続き検討", Status: "open", EvidenceSequenceNos: []int64{2},
	}}, nil, timeline, scope, stats)

	if len(items) != 1 || items[0].InformationStatus != informationStatusGrounded || stats.LowInformationItemsRewritten != 1 {
		t.Fatalf("items=%+v stats=%+v", items, stats)
	}
	if !strings.Contains(items[0].Title, "強風日") || strings.Contains(items[0].Title, "住民説明") {
		t.Fatalf("anaphora was not grounded in the preceding issue: %+v", items[0])
	}
}

func TestLowInformationRepairKeepsTentativeThenPromotesSameItem(t *testing.T) {
	initialScope := evidenceScopeFromTexts(map[int64]string{1: "この点は確認が必要です。"}, 1)
	initialTimeline := classifyDiscourseTimeline(initialScope)
	initial, _ := repairLowInformationIssueItems(nil, []liveAnalysisItem{{
		ID: "issue-tentative", Kind: "issue", Subtype: issueSubtypeConfirmation,
		Title: "この点は確認が必要", Status: "open", EvidenceSequenceNos: []int64{1},
	}}, nil, initialTimeline, initialScope, nil)
	if len(initial) != 1 || initial[0].InformationStatus != informationStatusTentative || initial[0].Inactive {
		t.Fatalf("initial tentative=%+v", initial)
	}

	nextScope := evidenceScopeFromTexts(map[int64]string{
		1: "この点は確認が必要です。",
		2: "ベンダー側で同時刻に障害が発生していたか確認します。",
	}, 1, 2)
	nextScope.CurrentRound = map[int64]struct{}{2: {}}
	nextTimeline := classifyDiscourseTimeline(nextScope)
	promoted, _ := repairLowInformationIssueItems(initial, nil, nil, nextTimeline, nextScope, nil)
	if len(promoted) != 1 || promoted[0].ID != "issue-tentative" || promoted[0].Subtype != issueSubtypeConfirmation || promoted[0].InformationStatus != informationStatusGrounded || promoted[0].Status != "updated" || !containsInt64(promoted[0].EvidenceSequenceNos, 2) || !strings.Contains(promoted[0].Title, "ベンダー側") {
		t.Fatalf("promoted=%+v", promoted)
	}
}

func TestDiscourseSummaryUtteranceDoesNotCreateSemanticNode(t *testing.T) {
	diff := `{"summary":"まとめ","items":[{"clientKey":"summary-only","kind":"issue","subtype":"discussion","severity":"medium","title":"以上をまとめます","body":"以上をまとめます","status":"open","evidenceSequenceNos":[1]}],"assignments":[]}`
	scope := evidenceScopeFromTexts(map[int64]string{1: "以上をまとめます。"}, 1)
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, nil, nil, 1, []int64{1}, scope, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 0 {
		t.Fatalf("summary utterance became semantic items: %+v", state.Items)
	}
}
