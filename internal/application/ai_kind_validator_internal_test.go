package application

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestCommonItemKindValidatorSemanticBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		original    string
		text        string
		wantKind    string
		wantSubtype string
	}{
		{name: "confirmed fact", original: "risk", text: "許可VLAN一覧からVLAN30が漏れていました。", wantKind: "fact"},
		{name: "causal hypothesis", original: "risk", text: "この設定漏れが障害の直接原因である可能性が最も高いです。", wantKind: "issue", wantSubtype: issueSubtypeInvestigation},
		{name: "current unresolved", original: "fact", text: "2階の通信遅延までこの設定だけで説明できるかは確認できていません。", wantKind: "issue", wantSubtype: issueSubtypeInvestigation},
		{name: "committed action", original: "risk", text: "VLAN単位の疎通確認を監視項目へ追加します。", wantKind: "todo"},
		{name: "future adverse risk", original: "issue", text: "監視対象を増やすとアラートが多くなりすぎる可能性があります。", wantKind: "risk"},
		{name: "ongoing adverse risk", original: "issue", text: "断続的に接続が切れる可能性があります。", wantKind: "risk"},
		{name: "owner deadline todo", original: "risk", text: "高橋さんが今週中にVPN証明書の更新手順と作業可能日を確認します。", wantKind: "todo"},
		{name: "confirmed recovery", original: "issue", text: "設定修正後に全端末で接続が正常になったことを確認しました。", wantKind: "fact"},
		{name: "possibility alone is not risk", original: "risk", text: "この構成が原因である可能性があります。", wantKind: "issue", wantSubtype: issueSubtypeInvestigation},
		{name: "current outage", original: "risk", text: "現在、2階では社内ネットワークへ接続できていません。", wantKind: "issue", wantSubtype: issueSubtypeDiscussion},
		{name: "uncommitted proposal", original: "risk", text: "VLAN単位の疎通監視を追加する案です。", wantKind: "issue", wantSubtype: issueSubtypeDiscussion},
		{name: "mitigation action", original: "risk", text: "通信切断を早期検知するためVLAN監視を追加します。", wantKind: "todo"},
		{name: "reported number", original: "issue", text: "接続不能端末は12台と報告されました。", wantKind: "fact"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := liveAnalysisItem{
				ID: "item-test", Kind: test.original, Subtype: issueSubtypeDiscussion,
				Title: test.text, Body: test.text, Status: "open", EvidenceSequenceNos: []int64{1},
			}
			decision := evaluateLiveItemKind(item, liveEvidenceScope{}, "test")
			if decision.CanonicalKind != test.wantKind || decision.CanonicalSubtype != test.wantSubtype {
				t.Fatalf("decision=%+v, want kind=%s subtype=%s", decision, test.wantKind, test.wantSubtype)
			}
			if decision.Confidence < itemKindValidationThreshold(itemKindValidationLive) {
				t.Fatalf("decision confidence=%.2f below live threshold", decision.Confidence)
			}
		})
	}
}

func TestJapaneseActionAspectAndDeadlineAttachment(t *testing.T) {
	tests := []struct {
		name         string
		originalKind string
		text         string
		wantKind     string
		wantDeadline bool
	}{
		{name: "completed configuration", originalKind: "todo", text: "設定を修正しました。", wantKind: "fact"},
		{name: "completed rollback", originalKind: "todo", text: "旧機器へ切り戻しました。", wantKind: "fact"},
		{name: "completed connectivity check", originalKind: "todo", text: "疎通を確認しました。", wantKind: "fact"},
		{name: "future configuration", originalKind: "fact", text: "設定を修正します。", wantKind: "todo"},
		{name: "imperative configuration", originalKind: "fact", text: "設定を修正してください。", wantKind: "todo"},
		{name: "future check with deadline", originalKind: "fact", text: "明日までに設定を確認します。", wantKind: "todo", wantDeadline: true},
		{name: "delegated check", originalKind: "fact", text: "佐藤さんに確認してもらいます。", wantKind: "todo"},
		{name: "object expiry", originalKind: "todo", text: "証明書が来月末に期限切れになります。", wantKind: "fact"},
		{name: "contract end date", originalKind: "todo", text: "契約は3月31日に終了します。", wantKind: "fact"},
		{name: "update deadline", originalKind: "fact", text: "来月末までに証明書を更新します。", wantKind: "todo", wantDeadline: true},
		{name: "assigned action deadline", originalKind: "fact", text: "佐藤さんが火曜日までに設定差分を確認します。", wantKind: "todo", wantDeadline: true},
		{name: "need is open issue", originalKind: "todo", text: "設定を修正する必要があります。", wantKind: "issue"},
		{name: "recommendation is proposal", originalKind: "todo", text: "設定を確認した方がよさそうです。", wantKind: "issue"},
		{name: "incomplete purpose is discussion", originalKind: "todo", text: "通信断を早期に検知できるように。", wantKind: "issue"},
		{name: "committed check", originalKind: "issue", text: "設定を確認することになりました。", wantKind: "todo"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := liveAnalysisItem{
				ID: "item-aspect", Kind: test.originalKind, Subtype: issueSubtypeDiscussion,
				Title: test.text, Body: test.text, Status: "open",
			}
			decision := evaluateLiveItemKind(item, liveEvidenceScope{}, "aspect_test")
			if decision.CanonicalKind != test.wantKind ||
				decision.Confidence < itemKindValidationThreshold(itemKindValidationLive) {
				t.Fatalf("decision=%+v, want kind=%s", decision, test.wantKind)
			}
			if decision.Features.DeadlinePresent != test.wantDeadline {
				t.Fatalf("deadline=%t, want=%t decision=%+v", decision.Features.DeadlinePresent, test.wantDeadline, decision)
			}
			if (test.name == "object expiry" || test.name == "contract end date") &&
				(!decision.Features.EventDatePresent || decision.Features.DeadlinePresent) {
				t.Fatalf("event date was not separated from action deadline: %+v", decision)
			}
		})
	}

	ongoing := liveAnalysisItem{
		ID: "item-ongoing", Kind: "fact", Title: "設定を確認しています。",
		Body: "設定を確認しています。", Status: "open",
	}
	decision := evaluateLiveItemKind(ongoing, liveEvidenceScope{}, "aspect_test")
	if decision.CanonicalKind == "todo" && decision.Confidence >= itemKindValidationThreshold(itemKindValidationLive) {
		t.Fatalf("ongoing aspect was forced to TODO without commitment context: %+v", decision)
	}
}

func TestFactRiskTodoCompositeUsesFragmentLocalSemantics(t *testing.T) {
	text := "証明書が来月末に期限切れになります。放置すると接続できなくなる可能性があります。高橋さんが今週中に更新手順を確認します。"
	scope := evidenceScopeFromTexts(map[int64]string{19: text}, 19)
	item := liveAnalysisItem{
		ID: "item-composite-three", Kind: "todo", Severity: "high",
		Title: "証明書対応", Body: text, Status: "open",
		EvidenceSequenceNos: []int64{19}, EvidenceSnippets: []string{
			"証明書が来月末に期限切れになります",
			"放置すると接続できなくなる可能性があります",
			"高橋さんが今週中に更新手順を確認します",
		}, evidenceSpecified: true,
	}
	items, _ := splitAndValidateLiveItemKinds(
		nil, []liveAnalysisItem{item}, nil, scope,
		itemKindValidationLive, "three_way_split", &liveAnalysisTreeMergeStats{},
	)
	if len(items) != 3 {
		t.Fatalf("items=%+v, want three semantic fragments", items)
	}
	byKind := make(map[string]liveAnalysisItem)
	for _, split := range items {
		byKind[split.Kind] = split
		if !equalInt64s(split.EvidenceSequenceNos, []int64{19}) {
			t.Fatalf("fragment evidence=%v", split.EvidenceSequenceNos)
		}
		if split.Title != split.Body || strings.Contains(split.Title, "。") {
			t.Fatalf("fragment label/body was not regenerated locally: %+v", split)
		}
	}
	if len(byKind) != 3 || byKind["fact"].ID == "" || byKind["risk"].ID == "" || byKind["todo"].ID == "" {
		t.Fatalf("kinds=%v items=%+v", byKind, items)
	}
	factFeatures := inferItemSemanticFeatures(byKind["fact"], liveEvidenceScope{})
	riskFeatures := inferItemSemanticFeatures(byKind["risk"], liveEvidenceScope{})
	todoFeatures := inferItemSemanticFeatures(byKind["todo"], liveEvidenceScope{})
	if factFeatures.DeadlinePresent || riskFeatures.OwnerPresent || riskFeatures.DeadlinePresent ||
		!todoFeatures.OwnerPresent || !todoFeatures.DeadlinePresent {
		t.Fatalf("fragment metadata leaked: fact=%+v risk=%+v todo=%+v", factFeatures, riskFeatures, todoFeatures)
	}
}

func TestIssueTodoCrossKindUpdateIsDetached(t *testing.T) {
	previous := []liveAnalysisItem{{
		ID: "item-issue-delay", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title: "2階の通信遅延原因", Body: "2階の通信遅延原因はまだ分かっていません。",
		Status: "open", EvidenceSequenceNos: []int64{10},
	}}
	diff := []liveAnalysisItem{{
		ClientKey: "item-issue-delay", Kind: "todo", Severity: "high",
		Title: "設定差分の確認", Body: "佐藤さんが火曜日までに設定差分を確認します。",
		Status: "open", EvidenceSequenceNos: []int64{24},
	}}
	assignments := []treeAssignment{{NodeID: "item-issue-delay", ParentTopicID: "topic-network"}}
	stats := &liveAnalysisTreeMergeStats{}
	diff, assignments = detachCrossKindActionUpdates(previous, diff, assignments, liveEvidenceScope{}, stats)
	if stats.CrossKindUpdatesDetached != 1 || diff[0].ClientKey == "item-issue-delay" ||
		assignments[0].NodeID != diff[0].ClientKey {
		t.Fatalf("diff=%+v assignments=%+v stats=%+v", diff, assignments, stats)
	}
	resolver := itemReferenceResolver(previous, diff, nil, stats)
	_ = resolver
	if diff[0].ID == "" || diff[0].ID == previous[0].ID {
		t.Fatalf("detached item did not receive a distinct canonical id: %+v", diff[0])
	}
	merged := mergeLiveAnalysisItems(previous, diff, nil)
	if len(merged) != 2 || merged[0].Kind != "issue" || merged[1].Kind != "todo" {
		t.Fatalf("issue/todo did not coexist: %+v", merged)
	}
}

func TestUpdatedTodoEvidenceIsLocalizedToAction(t *testing.T) {
	previous := []liveAnalysisItem{{
		ID: "item-todo-certificate", Kind: "todo", Title: "証明書対応",
		Body: "証明書対応を検討する", EvidenceSequenceNos: []int64{19, 20},
	}}
	diff := []liveAnalysisItem{{
		ID: "item-todo-certificate", Kind: "todo",
		Title: "証明書の更新手順を確認", Body: "高橋さんが今週中に証明書の更新手順と作業可能日を確認します。",
		EvidenceSequenceNos: []int64{21}, evidenceSpecified: true,
	}}
	merged := mergeLiveAnalysisItems(previous, diff, nil)
	appendItemEvidenceSequenceNos(merged, diff, []int64{21})
	scope := evidenceScopeFromTexts(map[int64]string{
		19: "証明書が来月末に期限切れになることがわかりました。放置すると接続できなくなる可能性があります。",
		20: "証明書の更新は別件として扱います。",
		21: "高橋さんに今週中に、証明書の更新手順と作業可能日を確認してもらいます。",
	}, 19, 20, 21)
	stats := &liveAnalysisTreeMergeStats{}
	localizeUpdatedItemEvidence(previous, merged, diff, scope, stats)
	if !equalInt64s(merged[0].EvidenceSequenceNos, []int64{21}) ||
		stats.EvidenceReferencesPruned != 2 {
		t.Fatalf("item=%+v stats=%+v", merged[0], stats)
	}
}

func TestExplicitCorrectionSupersedesPriorItem(t *testing.T) {
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{
			{
				ID: "item-old-port", Kind: "issue", Subtype: issueSubtypeInvestigation,
				Title: "上位接続ポートがアクセスポート", Body: "上位スイッチへの接続ポートがトランクではなくアクセスポートでした。",
				Status: "open", EvidenceSequenceNos: []int64{7},
			},
			{
				ID: "item-corrected-port", Kind: "fact",
				Title: "トランク許可一覧の設定漏れ", Body: "正確にはトランク設定で、許可一覧からVLAN30だけが漏れていました。",
				Status: "open", EvidenceSequenceNos: []int64{8},
			},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
			{ID: "topic-network", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク"},
			{ID: "item-old-port", Kind: "issue", ParentID: "topic-network", Label: "上位接続ポートがアクセスポート"},
			{ID: "item-corrected-port", Kind: "fact", ParentID: "topic-network", Label: "トランク許可一覧の設定漏れ"},
		}},
	}
	rebuildTreeAuditEdges(state.Tree)
	scope := evidenceScopeFromTexts(map[int64]string{
		7: "上位スイッチへ接続するポートは、トランクポートではなくアクセスポートになっていました。",
		8: "いえ、正確には完全なアクセスポート設定ではありません。トランク設定自体は入っていましたが、許可するVLANの一覧からVLAN30が漏れていました。",
	}, 7, 8)
	stats := &liveAnalysisTreeMergeStats{}
	repairCorrectionSupersessions(&state, scope, classifyDiscourseTimeline(scope), 2, stats)
	if stats.CorrectionItemsSuperseded != 1 || !state.Items[0].Inactive ||
		state.Items[0].MergedIntoID != "item-corrected-port" ||
		liveTreeNodeByID(state.Tree, "item-old-port") != nil ||
		liveTreeNodeByID(state.Tree, "item-corrected-port") == nil {
		t.Fatalf("state=%+v stats=%+v", state, stats)
	}
	if len(state.ItemTombstones) != 1 || state.ItemTombstones[0].Reason != "superseded" {
		t.Fatalf("tombstones=%+v", state.ItemTombstones)
	}
}

func TestLaterConfirmedEvidenceCanResolveAnOpenKindClassification(t *testing.T) {
	item := liveAnalysisItem{
		ID: "issue-vlan-cause", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title: "VLAN30の設定漏れが直接原因か", Body: "VLAN30の設定漏れが原因である可能性を確認中です。",
		Status: "resolved", EvidenceSequenceNos: []int64{1, 2},
		CreatedThroughSequenceNo: 1, InitialEvidenceMaxSequenceNo: 1,
	}
	scope := liveEvidenceScope{TranscriptText: map[int64]string{
		1: "VLAN30の設定漏れが障害原因である可能性を調査します。",
		2: "VLAN30の設定漏れを修正した後に全端末が正常化したため、直接原因と判断しました。",
	}}
	decision := evaluateLiveItemKind(item, scope, "test")
	if decision.CanonicalKind != "fact" || !decision.Features.ConfirmationSupersedesOpen ||
		decision.Reason != "later_confirmed_evidence_supersedes_open_state" {
		t.Fatalf("decision=%+v", decision)
	}
	validated := validateLiveItemKinds([]liveAnalysisItem{item}, scope, itemKindValidationFinal, "test", nil)
	if validated[0].Kind != "fact" || !equalInt64s(validated[0].EvidenceSequenceNos, []int64{1, 2}) {
		t.Fatalf("validated=%+v", validated[0])
	}
}

func TestCommonItemKindValidatorLeavesAmbiguousAndBrokenSTTUnchanged(t *testing.T) {
	for _, text := range []string{
		"監視構成の見直しについて。",
		"Vらんさんじゅう、せって、かくに、たぶん。",
	} {
		item := liveAnalysisItem{ID: "item-ambiguous", Kind: "risk", Title: text, Body: text, Status: "open"}
		decision := evaluateLiveItemKind(item, liveEvidenceScope{}, "test")
		if decision.Confidence >= itemKindValidationThreshold(itemKindValidationLive) &&
			decision.CanonicalKind != item.Kind {
			t.Fatalf("ambiguous text was force-reclassified: %+v", decision)
		}
	}
}

func TestCommonItemKindValidatorSplitsCompositeSemanticRoles(t *testing.T) {
	tests := []struct {
		name      string
		original  string
		body      string
		wantKinds []string
	}{
		{
			name: "risk and todo", original: "risk",
			body:      "VPN証明書を放置すると期限切れにより接続できなくなる可能性があります。高橋さんが今週中に更新手順を確認します。",
			wantKinds: []string{"risk", "todo"},
		},
		{
			name: "fact and issue", original: "issue",
			body:      "許可VLAN一覧からVLAN30が漏れていました。2階の遅延までこの設定だけで説明できるかは未確認です。",
			wantKinds: []string{"fact", "issue"},
		},
		{
			name: "issue and todo", original: "issue",
			body:      "監視間隔と通知条件が決まっていません。次回会議までに通知条件を検討します。",
			wantKinds: []string{"issue", "todo"},
		},
		{
			name: "three roles", original: "risk",
			body:      "許可VLAN一覧からVLAN30が漏れていました。原因をこの設定だけで説明できるかは未確認です。担当者が今週中に追加調査を実施します。",
			wantKinds: []string{"fact", "issue", "todo"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := liveAnalysisItem{
				ID: "item-composite", Kind: test.original, Subtype: issueSubtypeDiscussion,
				Title: "複合命題", Body: test.body, Status: "open",
				EvidenceSequenceNos: []int64{1}, evidenceSpecified: true,
			}
			items, assignments := splitAndValidateLiveItemKinds(
				nil, []liveAnalysisItem{item},
				[]treeAssignment{{NodeID: item.ID, ParentTopicID: "topic-network"}},
				liveEvidenceScope{}, itemKindValidationLive, "test_split", &liveAnalysisTreeMergeStats{},
			)
			got := make([]string, 0, len(items))
			for _, split := range items {
				got = append(got, split.Kind)
				if len(split.EvidenceSequenceNos) != 1 || split.EvidenceSequenceNos[0] != 1 {
					t.Fatalf("split evidence was not preserved: %+v", split)
				}
			}
			sort.Strings(got)
			want := append([]string(nil), test.wantKinds...)
			sort.Strings(want)
			if len(got) != len(want) {
				t.Fatalf("kinds=%v, want=%v items=%+v", got, want, items)
			}
			for index := range want {
				if got[index] != want[index] {
					t.Fatalf("kinds=%v, want=%v items=%+v", got, want, items)
				}
			}
			if len(items) > 1 && len(assignments) != len(items) {
				t.Fatalf("assignments=%+v items=%+v", assignments, items)
			}
		})
	}
}

func TestCommonItemKindRelationsPreserveDistinctPropositions(t *testing.T) {
	items := []liveAnalysisItem{
		{ID: "risk-vpn", Kind: "risk", Title: "VPN証明書期限切れによる接続不能", EvidenceSequenceNos: []int64{6}},
		{ID: "todo-vpn", Kind: "todo", Title: "VPN証明書の更新手順を確認", EvidenceSequenceNos: []int64{6}},
		{ID: "issue-vlan", Kind: "issue", Title: "VLAN30が障害原因か未確認", EvidenceSequenceNos: []int64{1, 2}},
		{ID: "fact-vlan", Kind: "fact", Title: "VLAN30が許可一覧から漏れていた", EvidenceSequenceNos: []int64{1}},
		{ID: "todo-vlan", Kind: "todo", Title: "VLAN30の監視を追加", EvidenceSequenceNos: []int64{2}},
	}
	tree := &liveAnalysisTree{}
	if created := appendSemanticKindRelations(tree, items); created != 3 {
		t.Fatalf("created=%d relations=%+v", created, tree.Relations)
	}
	got := map[string]string{}
	for _, relation := range tree.Relations {
		got[relation.Source+"->"+relation.Target] = relation.Kind
	}
	for pair, kind := range map[string]string{
		"todo-vpn->risk-vpn":    "mitigates",
		"fact-vlan->issue-vlan": "supports",
		"todo-vlan->issue-vlan": "addresses",
	} {
		if got[pair] != kind {
			t.Fatalf("relation %s=%q, want=%q all=%+v", pair, got[pair], kind, tree.Relations)
		}
	}
	if created := appendSemanticKindRelations(tree, items); created != 0 {
		t.Fatalf("relations duplicated: created=%d relations=%+v", created, tree.Relations)
	}
}

func TestItemKindDistributionWarnsWithoutForcingFactCount(t *testing.T) {
	state := liveAnalysisPayload{Items: []liveAnalysisItem{
		{ID: "risk-confirmed", Kind: "risk", Title: "VLAN30許可漏れ", Body: "VLAN30が許可一覧から漏れていました。", EvidenceSequenceNos: []int64{1}},
		{ID: "risk-action", Kind: "risk", Title: "更新手順確認", Body: "高橋さんが今週中に更新手順を確認します。", EvidenceSequenceNos: []int64{2}},
	}}
	stats := &liveAnalysisTreeMergeStats{}
	recordItemKindDistribution(&state, liveEvidenceScope{}, stats)
	if stats.ConfirmedEvidenceCandidates != 1 || stats.AssignedActionRiskCandidates != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	got := map[string]bool{}
	for _, warning := range stats.KindDistributionWarnings {
		got[warning] = true
	}
	if !got["confirmed_evidence_without_fact"] || !got["risk_distribution_contains_non_risk_roles"] {
		t.Fatalf("warnings=%v stats=%+v", stats.KindDistributionWarnings, stats)
	}
	if state.Items[0].Kind != "risk" || state.Items[1].Kind != "risk" {
		t.Fatalf("distribution observation mutated items: %+v", state.Items)
	}
}

func TestTreeAuditKindReclassificationUsesCommonSemanticValidator(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantKind string
	}{
		{name: "risk to todo", text: "高橋さんが今週中にVPN証明書の更新手順を確認します。", wantKind: "todo"},
		{name: "risk to issue", text: "VLAN30の設定漏れが障害原因である可能性があります。", wantKind: "issue"},
		{name: "risk to fact", text: "許可VLAN一覧からVLAN30が漏れていました。", wantKind: "fact"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := liveAnalysisPayload{
				Items: []liveAnalysisItem{{
					ID: "item-risk", Kind: "risk", Title: test.text, Body: test.text,
					Status: "resolved", EvidenceSequenceNos: []int64{1},
				}},
				Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
					{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
					{ID: "topic-network", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク"},
					{ID: "item-risk", Kind: "risk", ParentID: "topic-network", Label: test.text, Status: "resolved"},
				}},
			}
			rebuildTreeAuditEdges(state.Tree)
			operation := treeAuditOperation{
				OperationID: "op-kind", Type: TreeAuditReclassifyKind,
				TargetCanonicalItemID: "item-risk", Kind: test.wantKind, Confidence: 1,
				EvidenceSequenceNos: []int64{1},
			}
			_, _, reason := applyOneTreeAuditOperation(
				&state, operation, map[int64]string{1: test.text},
				map[int64]treeAuditEvidenceRole{1: treeAuditEvidencePrimary},
				nil, TreeAuditConfig{}.normalized(), "audit-kind", 2, nil,
			)
			if reason != "" {
				t.Fatalf("reason=%q", reason)
			}
			if state.Items[0].Kind != test.wantKind || state.Tree.Nodes[2].Kind != test.wantKind {
				t.Fatalf("item=%+v node=%+v", state.Items[0], state.Tree.Nodes[2])
			}
			if !equalInt64s(state.Items[0].EvidenceSequenceNos, []int64{1}) {
				t.Fatalf("evidence changed: %+v", state.Items[0])
			}
			if test.wantKind == "todo" && state.Items[0].Status != "resolved" {
				t.Fatalf("resolvable manual status changed: %+v", state.Items[0])
			}
		})
	}

	ambiguous := liveAnalysisPayload{
		Items: []liveAnalysisItem{{ID: "item-risk", Kind: "risk", Title: "監視構成の見直しについて", Body: "監視構成の見直しについて", Status: "open", EvidenceSequenceNos: []int64{1}}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
			{ID: "topic-network", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク"},
			{ID: "item-risk", Kind: "risk", ParentID: "topic-network", Label: "監視構成の見直しについて", Status: "open"},
		}},
	}
	rebuildTreeAuditEdges(ambiguous.Tree)
	_, _, reason := applyOneTreeAuditOperation(
		&ambiguous,
		treeAuditOperation{
			OperationID: "op-ambiguous", Type: TreeAuditReclassifyKind,
			TargetCanonicalItemID: "item-risk", Kind: "todo", Confidence: 1,
			EvidenceSequenceNos: []int64{1},
		},
		map[int64]string{1: "監視構成の見直しについて"},
		map[int64]treeAuditEvidenceRole{1: treeAuditEvidencePrimary},
		nil, TreeAuditConfig{}.normalized(), "audit-kind", 2, nil,
	)
	if reason != "semantic_reclassification_not_grounded" || ambiguous.Items[0].Kind != "risk" {
		t.Fatalf("ambiguous reclassification reason=%q item=%+v", reason, ambiguous.Items[0])
	}
}

func TestSession2345b804296ee2a1EquivalentFinalKindRepair(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{SequenceNo: 1, Text: "許可VLAN一覧からVLAN30が漏れていました。", IsFinal: true},
		{SequenceNo: 2, Text: "この設定漏れが障害の直接原因である可能性が最も高いです。", IsFinal: true},
		{SequenceNo: 3, Text: "2階の通信遅延まで、この設定だけで説明できるかは確認できていません。", IsFinal: true},
		{SequenceNo: 4, Text: "VLAN単位の疎通確認を監視項目へ追加します。", IsFinal: true},
		{SequenceNo: 5, Text: "監視対象を増やすとアラートが多くなりすぎる可能性があります。次回会議までに通知条件を検討します。", IsFinal: true},
		{SequenceNo: 6, Text: "VPN証明書が来月末に期限切れになるため、放置すると接続できなくなる可能性があります。高橋さんは今週中に更新手順と作業可能日を確認してください。", IsFinal: true},
		{SequenceNo: 7, Text: "設定を修正した後、全端末で接続が正常になったことを確認しました。", IsFinal: true},
		{SequenceNo: 8, Text: "交換時の設定チェックリストを作成します。", IsFinal: true},
		{SequenceNo: 9, Text: "端末台帳を更新します。", IsFinal: true},
		{SequenceNo: 10, Text: "監視ログを保存します。", IsFinal: true},
		{SequenceNo: 11, Text: "作業前後の疎通確認を実施します。", IsFinal: true},
		{SequenceNo: 12, Text: "復旧報告書を関係部署へ共有します。", IsFinal: true},
	}
	items := []liveAnalysisItem{
		{ID: "issue-vlan-confirmed", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "VLAN30許可漏れ", Body: segments[0].Text, Status: "open", EvidenceSequenceNos: []int64{1}},
		{ID: "issue-floor-delay", Kind: "issue", Subtype: issueSubtypeInvestigation, Title: "2階の遅延原因", Body: segments[2].Text, Status: "open", EvidenceSequenceNos: []int64{3}},
		{ID: "issue-alert-condition", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "通知条件が未確定", Body: "監視間隔と通知条件が決まっていません。", Status: "open", EvidenceSequenceNos: []int64{5}},
		{ID: "issue-recovery", Kind: "issue", Subtype: issueSubtypeConfirmation, Title: "復旧確認", Body: segments[6].Text, Status: "resolved", EvidenceSequenceNos: []int64{7}},
		{ID: "todo-alert-condition", Kind: "todo", Title: "通知条件の検討", Body: "次回会議までに通知条件を検討します。", Status: "open", EvidenceSequenceNos: []int64{5}},
		{ID: "todo-checklist", Kind: "todo", Title: "設定チェックリスト作成", Body: segments[7].Text, Status: "open", EvidenceSequenceNos: []int64{8}},
		{ID: "todo-device-inventory", Kind: "todo", Title: "端末台帳更新", Body: segments[8].Text, Status: "open", EvidenceSequenceNos: []int64{9}},
		{ID: "todo-log-save", Kind: "todo", Title: "監視ログ保存", Body: segments[9].Text, Status: "open", EvidenceSequenceNos: []int64{10}},
		{ID: "todo-connectivity-check", Kind: "todo", Title: "作業前後の疎通確認", Body: segments[10].Text, Status: "open", EvidenceSequenceNos: []int64{11}},
		{ID: "todo-report-share", Kind: "todo", Title: "復旧報告書共有", Body: segments[11].Text, Status: "open", EvidenceSequenceNos: []int64{12}},
		{ID: "decision-review", Kind: "decision", Title: "交換時は別担当者がレビューする", Body: "交換作業時は別担当者による設定レビューを必須とします。", Status: "open", EvidenceSequenceNos: []int64{8}},
		{ID: "risk-vlan-cause", Kind: "risk", Title: "VLAN30が障害原因の可能性", Body: segments[1].Text, Status: "open", EvidenceSequenceNos: []int64{2}},
		{ID: "risk-vlan-monitoring", Kind: "risk", Title: "VLAN監視追加", Body: segments[3].Text, Status: "open", EvidenceSequenceNos: []int64{4}},
		{ID: "risk-alert-overload", Kind: "risk", Title: "アラート過多", Body: "監視対象を増やすとアラートが多くなりすぎる可能性があります。", Status: "open", EvidenceSequenceNos: []int64{5}},
		{ID: "risk-vpn-expiry", Kind: "risk", Title: "VPN証明書期限切れ", Body: "VPN証明書を放置すると期限切れにより接続できなくなる可能性があります。", Status: "open", EvidenceSequenceNos: []int64{6}},
		{ID: "risk-vpn-owner", Kind: "risk", Title: "VPN更新手順確認", Body: "高橋さんは今週中に更新手順と作業可能日を確認します。", Status: "open", EvidenceSequenceNos: []int64{6}},
	}
	state := liveAnalysisPayload{
		Items: items, TreeVersion: 13,
		ItemTombstones: []liveAnalysisItemTombstone{{
			CanonicalItemID: "item-old", Reason: "manual_test", CreatedBy: "test", CreatedAtVersion: 1,
		}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
			{ID: "topic-network", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク障害と再発防止", Origin: topicOriginDynamic},
		}},
	}
	for _, item := range items {
		state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
			ID: item.ID, Kind: item.Kind, Subtype: item.Subtype, ParentID: "topic-network",
			Label: item.Title, Description: item.Body, Status: item.Status,
		})
	}
	rebuildTreeAuditEdges(state.Tree)
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	beforeCounts := livePayloadItemKindCounts(payload)
	for kind, want := range map[string]int{"issue": 4, "todo": 6, "decision": 1, "fact": 0, "risk": 5} {
		if beforeCounts[kind] != want {
			t.Fatalf("before %s=%d, want=%d counts=%v", kind, beforeCounts[kind], want, beforeCounts)
		}
	}
	roles := classifyTreeAuditEvidence(state, segments)
	beforeFindings := deterministicTreeAuditPrecheck(state, nil, roles, TreeAuditConfig{}.normalized())
	beforeKindFindings := countTreeAuditPrechecks(beforeFindings, TreeAuditSemanticKindMismatch)

	repairedPayload, stats := applyDeterministicFinalTreeRepairs(payload, nil, 13, finalRepairInput{
		Segments: segments, Audit: TreeAuditConfig{},
	})
	if stats.Error != "" || stats.IntegrityRejected {
		t.Fatalf("repair stats=%+v", stats)
	}
	repaired := previousLiveAnalysisState(repairedPayload)
	afterCounts := livePayloadItemKindCounts(repairedPayload)
	for kind, want := range map[string]int{"issue": 3, "todo": 8, "decision": 1, "fact": 2, "risk": 2} {
		if afterCounts[kind] != want {
			t.Fatalf("after %s=%d, want=%d counts=%v stats=%+v", kind, afterCounts[kind], want, afterCounts, stats)
		}
	}
	for itemID, wantKind := range map[string]string{
		"issue-vlan-confirmed": "fact",
		"issue-recovery":       "fact",
		"risk-vlan-cause":      "issue",
		"risk-vlan-monitoring": "todo",
		"risk-vpn-owner":       "todo",
		"risk-alert-overload":  "risk",
		"risk-vpn-expiry":      "risk",
	} {
		item := findItemByID(repaired.Items, itemID)
		if item == nil || item.Kind != wantKind {
			t.Fatalf("%s=%+v, want kind=%s", itemID, item, wantKind)
		}
		original := findItemByID(items, itemID)
		if original == nil || !equalInt64s(item.EvidenceSequenceNos, original.EvidenceSequenceNos) {
			t.Fatalf("%s evidence changed: before=%+v after=%+v", itemID, original, item)
		}
	}
	if stats.KindValidationChanges != 5 {
		t.Fatalf("kind changes=%d, want=5 stats=%+v", stats.KindValidationChanges, stats)
	}
	oldTombstonePreserved := false
	for _, tombstone := range repaired.ItemTombstones {
		if tombstone.CanonicalItemID == "item-old" && tombstone.Reason == "manual_test" {
			oldTombstonePreserved = true
			break
		}
	}
	if !oldTombstonePreserved {
		t.Fatalf("tombstones changed: %+v", repaired.ItemTombstones)
	}
	afterFindings := deterministicTreeAuditPrecheck(repaired, nil, classifyTreeAuditEvidence(repaired, segments), TreeAuditConfig{}.normalized())
	afterKindFindings := countTreeAuditPrechecks(afterFindings, TreeAuditSemanticKindMismatch)
	if beforeKindFindings < 5 || afterKindFindings != 0 {
		t.Fatalf("semantic kind findings before=%d after=%d", beforeKindFindings, afterKindFindings)
	}
	if stats.KindRelationsCreated < 3 {
		t.Fatalf("relations created=%d relations=%+v", stats.KindRelationsCreated, repaired.Tree.Relations)
	}
	relationKinds := map[string]string{}
	for _, relation := range repaired.Tree.Relations {
		relationKinds[relation.Source+"->"+relation.Target] = relation.Kind
	}
	for pair, wantKind := range map[string]string{
		"todo-alert-condition->risk-alert-overload":   "mitigates",
		"risk-vpn-owner->risk-vpn-expiry":             "mitigates",
		"todo-alert-condition->issue-alert-condition": "addresses",
	} {
		if relationKinds[pair] != wantKind {
			t.Fatalf("relation %s=%q, want=%q all=%+v", pair, relationKinds[pair], wantKind, repaired.Tree.Relations)
		}
	}
	if integrity := validateTreeIntegrity(repaired.Tree, repaired.Items, nil); !integrity.Valid {
		t.Fatalf("tree integrity=%+v", integrity)
	}
	t.Logf("session_2345 equivalent kind metrics before=%v after=%v correctRisks=2->2 riskToIssue=1 riskToFact=0 riskToTodo=2 issueToFact=2 issueToTodo=0 sameSubjectRiskTodoPairs=1->2 duplicateRisks=0->0 ambiguousKinds=%d tentative=0->0 relations=%d auditKindFindings=%d->%d finalAuditApplied=%d aiCalls=0 tokens=0",
		beforeCounts, afterCounts, stats.KindValidationAmbiguous, stats.KindRelationsCreated,
		beforeKindFindings, afterKindFindings, stats.KindValidationChanges)
}

func TestCommonItemKindValidatorPureFutureRiskIsStrong(t *testing.T) {
	text := "放置すると、リモート接続ができなくなる可能性があります。"
	item := liveAnalysisItem{
		ID: "item-risk", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "リモート接続への影響懸念", Body: text, Status: "open",
		EvidenceSequenceNos: []int64{18},
	}
	decision := evaluateLiveItemKind(item, liveEvidenceScope{}, "test")
	if decision.CanonicalKind != "risk" || decision.Confidence < itemKindValidationThreshold(itemKindValidationLive) {
		t.Fatalf("decision=%+v", decision)
	}
	if !sameSentenceDiscussionIssueEvidence(item, text, 18) {
		t.Fatalf("sameSentenceDiscussionIssueEvidence=false")
	}
}
