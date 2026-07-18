package application

import (
	"encoding/json"
	"testing"
)

func TestLowInformationValidatorRejectsMetaItemsAndAcceptsConcreteShortItems(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "ここで別の問題があります。",
		2: "公開方針を承認します。",
		3: "承認します。",
		4: "佐藤さんに来週火曜までの確認をお願いします。",
		5: "先ほどの期限を金曜日に訂正します。",
		6: "VPN証明書の期限切れでリモート接続が停止する可能性があります。",
	}, 1, 2, 3, 4, 5, 6)
	timeline := classifyDiscourseTimelineWithModel(scope, []liveUtteranceRoleRef{
		{SequenceNo: 1, Role: liveUtteranceDiscourseTransition},
		{SequenceNo: 2, Role: liveUtteranceSubstantive},
		{SequenceNo: 3, Role: liveUtteranceSubstantive},
		{SequenceNo: 4, Role: liveUtteranceSubstantive},
		{SequenceNo: 5, Role: liveUtteranceCorrection},
		{SequenceNo: 6, Role: liveUtteranceSubstantive},
	})
	timeline.Roles[2] = liveEvidenceReferenceRecap
	timeline.DetectedRoles[2] = liveUtteranceRecap
	cases := []struct {
		name         string
		item         liveAnalysisItem
		wantRejected bool
	}{
		{"discourse evidence", liveAnalysisItem{ID: "item-meta", Kind: "todo", Title: "別の問題の存在を確認", Body: "追加論点", EvidenceSequenceNos: []int64{1}, evidenceSpecified: true}, true},
		{"recap-only new item", liveAnalysisItem{ID: "item-recap", Kind: "fact", Title: "VPN証明書の期限", Body: "既出内容のまとめ", EvidenceSequenceNos: []int64{2}, evidenceSpecified: true}, true},
		{"empty todo", liveAnalysisItem{ID: "item-empty-todo", Kind: "todo", Title: "対応事項", EvidenceSequenceNos: nil, evidenceSpecified: true}, true},
		{"empty decision", liveAnalysisItem{ID: "item-empty-decision", Kind: "decision", Title: "決定事項", EvidenceSequenceNos: nil, evidenceSpecified: true}, true},
		{"contextual short decision", liveAnalysisItem{ID: "item-approval", Kind: "decision", Title: "承認します", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true}, false},
		{"assignee deadline todo", liveAnalysisItem{ID: "item-short-todo", Kind: "todo", Title: "佐藤さんにお願いします", Body: "来週火曜まで", EvidenceSequenceNos: []int64{4}, evidenceSpecified: true}, false},
		{"correction", liveAnalysisItem{ID: "item-correction", Kind: "fact", Title: "期限を金曜日に訂正", EvidenceSequenceNos: []int64{5}, evidenceSpecified: true}, false},
		{"specific risk", liveAnalysisItem{ID: "item-risk", Kind: "risk", Title: "証明書期限切れ", Body: "リモート接続不能の可能性", EvidenceSequenceNos: []int64{6}, evidenceSpecified: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, _ := validateLiveItemInformation(tc.item, false, timeline, scope)
			if (reason != "") != tc.wantRejected {
				t.Fatalf("reason=%q item=%+v", reason, tc.item)
			}
		})
	}
}

func TestTombstonePreventsEquivalentItemButAllowsExplicitReopenAndNewInformation(t *testing.T) {
	original := liveAnalysisItem{ID: "item-old", Kind: "todo", Title: "VPN証明書を更新", Body: "更新手順を確認", Status: "open", PropositionKey: "prop-vpn-update", EvidenceSequenceNos: []int64{10}, Inactive: true}
	newState := func(reason, mergedInto string) liveAnalysisPayload {
		state := liveAnalysisPayload{TreeVersion: 4, Items: []liveAnalysisItem{original}}
		addItemTombstone(&state, original, reason, mergedInto, "tree_auditor", "audit-1", 4, 5, "candidate-vpn")
		return state
	}
	scope := evidenceScopeFromTexts(map[int64]string{10: "VPN証明書を更新します。", 11: "未解決として再オープンします。", 12: "高橋さんが金曜日までに更新します。"}, 10, 11, 12)

	state := newState("discourse_only", "")
	blocked, _ := filterTombstoneResurrections(&state, []liveAnalysisItem{{ID: "item-new", modelReference: "same", Kind: "todo", Title: original.Title, Body: original.Body, EvidenceSequenceNos: []int64{10}}}, nil, nil, scope, 6, &liveAnalysisTreeMergeStats{})
	if len(blocked) != 0 {
		t.Fatalf("equivalent tombstoned item was allowed: %+v", blocked)
	}

	matchingCases := []struct {
		name        string
		item        liveAnalysisItem
		assignments []treeAssignment
	}{
		{name: "canonical id", item: liveAnalysisItem{ID: "item-old", Kind: "todo", Title: original.Title, Body: original.Body, EvidenceSequenceNos: []int64{12}}},
		{name: "proposition key", item: liveAnalysisItem{ID: "item-new-prop", Kind: "todo", Title: original.Title, Body: original.Body, PropositionKey: "prop-vpn-update", EvidenceSequenceNos: []int64{12}}},
		{name: "evidence fingerprint", item: liveAnalysisItem{ID: "item-new-evidence", Kind: "todo", Title: original.Title, Body: original.Body, PropositionKey: "prop-other", EvidenceSequenceNos: []int64{10}}},
		{name: "candidate alias and semantics", item: liveAnalysisItem{ID: "item-new-alias", modelReference: "alias-ref", Kind: "todo", Title: "VPN証明書の更新", Body: "更新手順を確認する", PropositionKey: "prop-other-2", EvidenceSequenceNos: []int64{12}}, assignments: []treeAssignment{{NodeID: "alias-ref", ParentTopicID: "candidate-vpn"}}},
	}
	for _, tc := range matchingCases {
		t.Run(tc.name, func(t *testing.T) {
			candidateState := newState("merged", "item-survivor")
			kept, _ := filterTombstoneResurrections(&candidateState, []liveAnalysisItem{tc.item}, tc.assignments, nil, scope, 6, nil)
			if len(kept) != 0 {
				t.Fatalf("matching tombstone was bypassed: %+v", kept)
			}
		})
	}

	reopenedState := newState("discourse_only", "")
	reopened, _ := filterTombstoneResurrections(&reopenedState, []liveAnalysisItem{{ID: "item-old", modelReference: "item-old", Kind: "todo", Title: original.Title, Body: original.Body, EvidenceSequenceNos: []int64{11}}}, nil, []resolutionUpdate{{ItemID: "item-old", Status: "open", EvidenceSequenceNos: []int64{11}, Reason: "未解決として再オープン"}}, scope, 6, nil)
	if len(reopened) != 1 || !reopened[0].reopenFromTombstone {
		t.Fatalf("explicit reopen was not allowed: %+v", reopened)
	}
	encodedReopen, err := json.Marshal(reopenedState)
	if err != nil {
		t.Fatalf("marshal reopened tombstone state: %v", err)
	}
	var reloadedReopen liveAnalysisPayload
	if err := json.Unmarshal(encodedReopen, &reloadedReopen); err != nil {
		t.Fatalf("unmarshal reopened tombstone state: %v", err)
	}
	if len(reloadedReopen.ItemTombstones) != 1 || reloadedReopen.ItemTombstones[0].ReopenedAtVersion != 6 || reloadedReopen.ItemTombstones[0].ReopenReason != "explicit_reopen" {
		t.Fatalf("reopen history did not survive payload round-trip: %+v", reloadedReopen.ItemTombstones)
	}

	newInfoState := newState("discourse_only", "")
	newInfo, _ := filterTombstoneResurrections(&newInfoState, []liveAnalysisItem{{ID: "item-old", Kind: "todo", Title: original.Title, Body: "高橋さんが金曜日までに更新", EvidenceSequenceNos: []int64{12}}}, nil, nil, scope, 6, nil)
	if len(newInfo) != 1 || !newInfo[0].reopenFromTombstone {
		t.Fatalf("material new assignee/deadline was not allowed: %+v", newInfo)
	}

	mergedTargetState := newState("merged", "item-survivor")
	mergedTargetState.Items = append(mergedTargetState.Items, liveAnalysisItem{ID: "item-survivor", Inactive: true})
	mergedTargetReopen, _ := filterTombstoneResurrections(&mergedTargetState, []liveAnalysisItem{{ID: "item-old", Kind: "todo", Title: original.Title, Body: original.Body, EvidenceSequenceNos: []int64{12}}}, nil, nil, scope, 6, nil)
	if len(mergedTargetReopen) != 1 || !mergedTargetReopen[0].reopenFromTombstone {
		t.Fatalf("inactive merge target did not permit reopen: %+v", mergedTargetReopen)
	}

	otherSession := liveAnalysisPayload{}
	other, _ := filterTombstoneResurrections(&otherSession, []liveAnalysisItem{{ID: "item-old", Kind: "todo", Title: original.Title, Body: original.Body, EvidenceSequenceNos: []int64{10}}}, nil, nil, scope, 1, nil)
	if len(other) != 1 {
		t.Fatal("tombstone leaked across session payloads")
	}
}

func TestHistoricalDiscourseRepairRetainsAuditorInactiveItemAndProvenance(t *testing.T) {
	item := liveAnalysisItem{
		ID: "item-discourse", Kind: "todo", Title: "別の問題の存在を確認",
		Body: "アジェンダ外の別問題があるとの紹介", Status: "open",
		EvidenceSequenceNos: []int64{1}, Inactive: true,
	}
	state := liveAnalysisPayload{TreeVersion: 2, Items: []liveAnalysisItem{item}}
	addItemTombstone(&state, item, "discourse_only", "", "tree_auditor", "audit-1", 1, 2, "group-additional")
	timeline := classifyDiscourseTimeline(evidenceScopeFromTexts(map[int64]string{1: "ここで、アジェンダにはなかった別の問題があります。"}, 1))
	repairHistoricalDiscourseItems(&state, timeline, &liveAnalysisTreeMergeStats{})
	if len(state.Items) != 1 || state.Items[0].ID != item.ID || !state.Items[0].Inactive {
		t.Fatalf("historical repair erased auditor-retained item: %+v", state.Items)
	}
	if len(state.ItemTombstones) != 1 {
		t.Fatalf("tombstones = %+v", state.ItemTombstones)
	}
	tombstone := state.ItemTombstones[0]
	if tombstone.CreatedBy != "tree_auditor" || tombstone.SourceTreeVersion != 1 || tombstone.CreatedAtVersion != 2 || tombstone.AuditRunID != "audit-1" || tombstone.Reason != "discourse_only" {
		t.Fatalf("historical repair changed tombstone provenance: %+v", tombstone)
	}
}
