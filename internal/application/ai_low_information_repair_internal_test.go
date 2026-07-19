package application

import (
	"strings"
	"testing"
)

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
