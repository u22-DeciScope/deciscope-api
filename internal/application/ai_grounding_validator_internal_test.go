package application

import (
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func groundingTestScope(texts map[int64]string) liveEvidenceScope {
	scope := liveEvidenceScope{
		Allowed:        make(map[int64]struct{}, len(texts)),
		CurrentRound:   make(map[int64]struct{}, len(texts)),
		TranscriptText: make(map[int64]string, len(texts)),
		Segments:       make(map[int64]domain.TranscriptSegment, len(texts)),
		EvidenceRoles:  make(map[int64]liveEvidenceRole, len(texts)),
	}
	for sequenceNo, text := range texts {
		scope.Allowed[sequenceNo] = struct{}{}
		scope.CurrentRound[sequenceNo] = struct{}{}
		scope.TranscriptText[sequenceNo] = text
		scope.Segments[sequenceNo] = domain.TranscriptSegment{
			SessionID:  "session_df2dfedbb1eef84a",
			SequenceNo: sequenceNo,
			Text:       text,
			IsFinal:    true,
		}
		scope.EvidenceRoles[sequenceNo] = liveEvidencePrimary
		if sequenceNo > scope.CoveredThrough {
			scope.CoveredThrough = sequenceNo
		}
	}
	return scope
}

func groundedTestItem(id, kind, title, body string, evidence ...int64) liveAnalysisItem {
	return liveAnalysisItem{
		ID: id, Kind: kind, Title: title, Body: body, Status: "open",
		EvidenceSequenceNos: append([]int64(nil), evidence...),
		evidenceSpecified:   true,
	}
}

func hasGroundingSource(values []groundingSourceType, want groundingSourceType) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSessionDF2DFEDBB1EEF84AFirstRoundPreventsFutureContextLeakage(t *testing.T) {
	scope := groundingTestScope(map[int64]string{
		1: "名古屋支社のネットワーク障害について振り返ります。",
		2: "午前9時20分ごろ、3階を中心に接続できないという報告がありました。",
	})
	mc := &meetingContext{
		Title:      "名古屋支社ネットワーク障害の振り返り",
		Background: "2階にも通信遅延があり、VLAN30の許可漏れ、スイッチ交換、VLAN単位監視、VPN証明書更新を確認する。",
		Agenda: []agendaItem{{
			ID: "agenda-1", Title: "障害原因と再発防止",
			Description:   "VLAN30、スイッチ交換、VPN証明書を確認する",
			SemanticHints: []string{"VLAN単位監視", "2階の通信遅延"},
		}},
	}

	// Observed-shape replay: two model items became three fragments. Only
	// the first fragment is actually supported by final sequence 1-2.
	fragments := []liveAnalysisItem{
		groundedTestItem(
			"item-grounded", "fact",
			"名古屋支社のネットワーク障害",
			"午前9時20分ごろ、3階を中心に接続できないという報告がありました",
			1, 2,
		),
		groundedTestItem("item-vlan", "fact", "VLAN30の許可漏れ", "VLAN30の許可漏れが直接原因です", 2),
		groundedTestItem("item-vpn", "todo", "VPN証明書の更新", "VPN証明書を来週までに更新します", 2),
	}
	fragments[0].EvidenceSnippets = []string{
		"名古屋支社のネットワーク障害について振り返ります",
		"午前9時20分ごろ、3階を中心に接続できないという報告がありました",
	}
	assignments := []treeAssignment{
		{NodeID: "item-grounded", ParentTopicID: "agenda-1"},
		{NodeID: "item-vlan", ParentTopicID: "agenda-1"},
		{NodeID: "item-vpn", ParentTopicID: "agenda-1"},
	}
	stats := &liveAnalysisTreeMergeStats{}
	visible, visibleAssignments := validateLiveItemGrounding(
		nil, fragments, assignments, scope, mc, "target_first_round", true, stats,
	)
	if len(visible) != 1 || visible[0].ID != "item-grounded" || len(visibleAssignments) != 1 {
		t.Fatalf("visible=%+v assignments=%+v stats=%+v", visible, visibleAssignments, stats)
	}
	if stats.GroundingAccepted != 1 || stats.GroundingCandidateOnly+stats.GroundingRejected+stats.GroundingTentative != 2 {
		t.Fatalf("unexpected grounding counts: %+v", stats)
	}
	if stats.GroundingFutureLeaksPrevented != 2 || stats.GroundingContextOnlyAtoms < 2 {
		t.Fatalf("context leaks were not identified: %+v", stats)
	}
	unsupportedEntities := 0
	for _, decision := range stats.GroundingDecisions {
		for _, hash := range decision.UnsupportedAtomHashes {
			for _, category := range []string{"person:", "location:", "number:", "identifier:", "owner:", "deadline:"} {
				if strings.HasPrefix(hash, category) {
					unsupportedEntities++
					break
				}
			}
		}
	}
	t.Logf("target fixture metrics: finalSequences=2 modelItems=2 splitFragments=3 visible=1 grounded=1 contextOnlyExcluded=2 unsupportedAtoms=%d unsupportedEntities=%d futureLeaksPrevented=%d",
		stats.GroundingUnsupportedAtoms, unsupportedEntities, stats.GroundingFutureLeaksPrevented)
	for _, forbidden := range []string{"VLAN30", "VPN証明書", "2階", "スイッチ交換"} {
		if strings.Contains(visible[0].Title+" "+visible[0].Body, forbidden) {
			t.Fatalf("unspoken context leaked into visible item: %q", forbidden)
		}
	}

	// Once VLAN30 is actually spoken in a later final segment, it becomes
	// eligible; the pre-meeting copy alone never supplied this permission.
	laterScope := groundingTestScope(map[int64]string{
		1: scope.TranscriptText[1],
		2: scope.TranscriptText[2],
		3: "許可VLAN一覧からVLAN30が漏れていました。",
	})
	later := groundedTestItem("item-vlan-later", "fact", "VLAN30の許可漏れ", "許可VLAN一覧からVLAN30が漏れていました", 3)
	decision, _ := evaluateItemGrounding(later, laterScope, buildGroundingContextCatalog(mc, nil), "target_later_round", false)
	if decision.Decision != "accepted" || !hasGroundingSource(decision.SourceTypes, groundingSourceFinalTranscript) {
		t.Fatalf("later spoken detail was not accepted: %+v", decision)
	}
}

func TestGroundingContextSourcesCannotBecomePrimaryEvidence(t *testing.T) {
	scope := groundingTestScope(map[int64]string{1: "名古屋支社のネットワーク障害を振り返ります。"})
	mc := &meetingContext{
		Background: "VLAN30の許可漏れ",
		Agenda: []agendaItem{{
			ID: "agenda-1", Title: "SW900交換",
			Description:   "VPN99証明書を更新する",
			SemanticHints: []string{"SRV77を再起動する"},
		}},
	}
	previous := []liveAnalysisItem{
		groundedTestItem("old-db", "fact", "DB66停止", "DB66が停止した", 1),
	}
	catalog := buildGroundingContextCatalog(mc, previous)
	catalog = append(catalog, groundingContextEntry{Source: groundingSourceAuditFinding, Text: "CACHE44が停止した"})
	tests := []struct {
		name   string
		text   string
		source groundingSourceType
	}{
		{name: "pre meeting", text: "VLAN30の許可漏れ", source: groundingSourcePreMeetingInput},
		{name: "agenda title", text: "SW900交換", source: groundingSourceAgendaTitle},
		{name: "agenda metadata", text: "VPN99証明書を更新する", source: groundingSourceAgendaMetadata},
		{name: "semantic hint", text: "SRV77を再起動する", source: groundingSourceSemanticHint},
		{name: "existing tree", text: "DB66が停止した", source: groundingSourceExistingTree},
		{name: "audit finding", text: "CACHE44が停止した", source: groundingSourceAuditFinding},
		{name: "model inference", text: "FW55が過熱した", source: groundingSourceModelInference},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := groundedTestItem("item-"+test.name, "fact", test.text, test.text, 1)
			decision, _ := evaluateItemGrounding(item, scope, catalog, "context_source_test", false)
			if decision.Decision == "accepted" {
				t.Fatalf("context-only content was accepted: %+v", decision)
			}
			if !hasGroundingSource(decision.SourceTypes, test.source) {
				t.Fatalf("source=%s not classified: %+v", test.source, decision)
			}
		})
	}

	partialScope := groundingTestScope(map[int64]string{2: "2階でも通信遅延が発生しています。"})
	partial := partialScope.Segments[2]
	partial.IsFinal = false
	partialScope.Segments[2] = partial
	delete(partialScope.Allowed, 2)
	item := groundedTestItem("item-partial", "fact", "2階の通信遅延", "2階でも通信遅延が発生しています", 2)
	decision, _ := evaluateItemGrounding(item, partialScope, nil, "partial_source_test", false)
	if decision.Decision == "accepted" || !hasGroundingSource(decision.SourceTypes, groundingSourcePartialTranscript) {
		t.Fatalf("partial transcript was not rejected/classified: %+v", decision)
	}
}

func TestGroundingValidatesEvidenceMeaningAndProtectedDetails(t *testing.T) {
	scope := groundingTestScope(map[int64]string{
		2: "午前9時20分ごろ、3階を中心に接続できないという報告がありました。",
	})
	tests := []struct {
		name      string
		title     string
		body      string
		forbidden string
		accepted  bool
	}{
		{name: "fully grounded", title: "3階を中心に接続不能", body: "午前9時20分ごろ、3階を中心に接続できないと報告されました", accepted: true},
		{name: "subject mismatch", title: "大阪工場の冷却装置", body: "大阪工場の冷却装置が停止しました", forbidden: "大阪工場"},
		{name: "predicate mismatch", title: "3階の接続が復旧", body: "午前9時20分ごろに3階の接続が復旧しました", forbidden: "復旧"},
		{name: "unspoken number", title: "3階を中心に接続不能", body: "午前9時30分ごろに報告されました", forbidden: "9時30分"},
		{name: "unspoken person", title: "3階を中心に接続不能", body: "佐藤さんが接続不能を報告しました", forbidden: "佐藤さん"},
		{name: "unspoken cause", title: "VLAN30が直接原因", body: "VLAN30の許可漏れが接続不能の直接原因です", forbidden: "VLAN30"},
		{name: "unspoken deadline", title: "3階の接続復旧", body: "来週までに3階の接続を復旧します", forbidden: "来週"},
		{name: "partially grounded", title: "3階を中心に接続不能", body: "3階の接続不能はSW800故障が原因です", forbidden: "SW800"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := groundedTestItem("item-"+test.name, "fact", test.title, test.body, 2)
			decision, safe := evaluateItemGrounding(item, scope, nil, "meaning_test", false)
			if test.accepted {
				if decision.Decision != "accepted" {
					t.Fatalf("fully grounded item=%+v safe=%+v", decision, safe)
				}
				return
			}
			if decision.Decision == "accepted" {
				t.Fatalf("unsupported detail was accepted: %+v", decision)
			}
			if decision.Decision == "rewritten" && strings.Contains(safe.Title+" "+safe.Body, test.forbidden) {
				t.Fatalf("safe rewrite retained unsupported detail %q: %+v", test.forbidden, safe)
			}
		})
	}
}

func TestGroundingEvidenceSnippetsAndFragmentEvidenceAreVerified(t *testing.T) {
	scope := groundingTestScope(map[int64]string{
		1: "3階で通信障害が発生しました。",
		2: "VLAN単位の疎通監視を追加します。",
	})
	valid := groundedTestItem("item-snippet", "fact", "3階の通信障害", "3階で通信障害が発生しました", 1)
	valid.EvidenceSnippets = []string{"３階で通信障害が発生しました"}
	if decision, _ := evaluateItemGrounding(valid, scope, nil, "snippet_test", false); decision.Decision != "accepted" {
		t.Fatalf("normalized exact snippet was not accepted: %+v", decision)
	}
	invalid := valid
	invalid.ID = "item-invalid-snippet"
	invalid.EvidenceSnippets = []string{"VLAN30の許可漏れ"}
	if decision, _ := evaluateItemGrounding(invalid, scope, nil, "snippet_test", false); decision.Decision == "accepted" {
		t.Fatalf("snippet absent from cited sequence was accepted: %+v", decision)
	}

	if got := semanticFragmentGroundingSequenceNo("3階で通信障害が発生しました", []int64{1, 2}, scope); got != 1 {
		t.Fatalf("outage fragment evidence=%d, want 1", got)
	}
	if got := semanticFragmentGroundingSequenceNo("VLAN単位の疎通監視を追加します", []int64{1, 2}, scope); got != 2 {
		t.Fatalf("monitoring fragment evidence=%d, want 2", got)
	}
	if got := semanticFragmentGroundingSequenceNo("VLAN30の許可漏れが原因です", []int64{1, 2}, scope); got != 0 {
		t.Fatalf("over-specific fragment inherited unrelated evidence=%d", got)
	}
	if got := semanticFragmentGroundingSequenceNo("VPN証明書を更新します", []int64{1, 2}, scope); got != 0 {
		t.Fatalf("fully ungrounded fragment inherited evidence=%d", got)
	}
}

func TestSemanticSplitGroundsEachFragmentIndependently(t *testing.T) {
	scope := groundingTestScope(map[int64]string{
		1: "3階で通信障害が発生したことを確認しました。",
		2: "VLAN単位の疎通監視を追加します。",
	})
	item := groundedTestItem(
		"item-composite", "fact", "通信障害と監視対応",
		"3階で通信障害が発生したことを確認しました。VLAN単位の疎通監視を追加します。",
		1, 2,
	)
	item.EvidenceSnippets = []string{scope.TranscriptText[1], scope.TranscriptText[2]}
	stats := &liveAnalysisTreeMergeStats{}
	split, assignments := splitLiveItemKinds(
		nil, []liveAnalysisItem{item},
		[]treeAssignment{{NodeID: item.ID, ParentTopicID: "topic-network"}},
		scope, stats,
	)
	split, assignments = validateLiveItemGrounding(
		nil, split, assignments, scope, nil, "split_grounding_test", true, stats,
	)
	if len(split) != 2 || len(assignments) != 2 || stats.KindSemanticSplits != 1 {
		t.Fatalf("split=%+v assignments=%+v stats=%+v", split, assignments, stats)
	}
	byKind := make(map[string]liveAnalysisItem, len(split))
	for _, fragment := range split {
		byKind[fragment.Kind] = fragment
		if len(fragment.EvidenceSequenceNos) != 1 {
			t.Fatalf("fragment inherited all source evidence: %+v", fragment)
		}
	}
	if fact, ok := byKind["fact"]; !ok || fact.EvidenceSequenceNos[0] != 1 {
		t.Fatalf("fact fragment=%+v", fact)
	}
	if todo, ok := byKind["todo"]; !ok || todo.EvidenceSequenceNos[0] != 2 {
		t.Fatalf("todo fragment=%+v", todo)
	}

	onlyFirst := item
	onlyFirst.EvidenceSequenceNos = []int64{1}
	onlyFirst.EvidenceSnippets = []string{scope.TranscriptText[1]}
	one, _ := splitLiveItemKinds(nil, []liveAnalysisItem{onlyFirst}, nil, scope, &liveAnalysisTreeMergeStats{})
	one, _ = validateLiveItemGrounding(nil, one, nil, scope, nil, "split_one_grounded", true, nil)
	for _, fragment := range one {
		if strings.Contains(fragment.Title+" "+fragment.Body, "疎通監視") {
			t.Fatalf("ungrounded split fragment survived: %+v", one)
		}
	}
}

func TestGroundingRunsBeforeKindValidationAndLaterEvidenceIsStrict(t *testing.T) {
	scope := groundingTestScope(map[int64]string{
		1: "名古屋支社のネットワーク障害を振り返ります。",
		2: "3階を中心に接続できないという報告がありました。",
	})
	ungrounded := groundedTestItem("risk-vlan", "risk", "VLAN30の設定漏れ", "VLAN30が直接原因と確認されました", 2)
	grounded, _ := validateLiveItemGrounding(nil, []liveAnalysisItem{ungrounded}, nil, scope, nil, "before_kind", false, nil)
	for _, item := range grounded {
		if strings.Contains(item.Title+" "+item.Body, "VLAN30") || strings.Contains(item.Title+" "+item.Body, "直接原因") {
			t.Fatalf("ungrounded proposition reached kind validator: %+v", grounded)
		}
	}

	firstRound := liveAnalysisItem{
		ID: "issue-first", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title: "3階の接続不能の原因", Body: "原因を調査します", Status: "open",
		EvidenceSequenceNos: []int64{1, 2}, CreatedThroughSequenceNo: 2, InitialEvidenceMaxSequenceNo: 2,
	}
	firstDecision := evaluateLiveItemKind(firstRound, scope, "first_round")
	if firstDecision.Features.ConfirmationSupersedesOpen ||
		firstDecision.Reason == "later_confirmed_evidence_supersedes_open_state" {
		t.Fatalf("first round incorrectly used later evidence: %+v", firstDecision)
	}

	sameSequenceScope := groundingTestScope(map[int64]string{
		2: "VLAN30の設定漏れが直接原因であることを確認しました。",
	})
	sameSequence := firstRound
	sameSequence.Title = "VLAN30の設定漏れが原因か"
	sameSequence.Body = "VLAN30の設定漏れが原因である可能性を確認中です"
	sameSequence.EvidenceSequenceNos = []int64{2}
	if decision := evaluateLiveItemKind(sameSequence, sameSequenceScope, "same_sequence"); decision.Features.ConfirmationSupersedesOpen {
		t.Fatalf("same sequence was treated as later evidence: %+v", decision)
	}

	laterScope := groundingTestScope(map[int64]string{
		1: "VLAN30の設定漏れが原因である可能性を調査します。",
		3: "VLAN30の設定漏れが直接原因であることを確認しました。",
	})
	later := sameSequence
	later.EvidenceSequenceNos = []int64{1, 3}
	later.CreatedThroughSequenceNo = 2
	later.InitialEvidenceMaxSequenceNo = 1
	later.Status = "resolved"
	if decision := evaluateLiveItemKind(later, laterScope, "later_sequence"); !decision.Features.ConfirmationSupersedesOpen ||
		decision.Reason != "later_confirmed_evidence_supersedes_open_state" {
		t.Fatalf("strict later confirmation was not recognized: %+v", decision)
	}
	laterScope.EvidenceRoles[3] = liveEvidenceReferenceRecap
	if decision := evaluateLiveItemKind(later, laterScope, "recap_sequence"); decision.Features.ConfirmationSupersedesOpen {
		t.Fatalf("recap was treated as strict later evidence: %+v", decision)
	}
}

func TestLiveAnalysisGroundingSchemaRequiresEvidenceSnippets(t *testing.T) {
	if liveAnalysisPromptVersion != "v20" {
		t.Fatalf("prompt version=%s, want v20", liveAnalysisPromptVersion)
	}
	for _, value := range []string{liveAnalysisSchemaDescription, liveAnalysisResponseJSONSchema} {
		if !strings.Contains(value, "evidenceSnippets") {
			t.Fatalf("grounding quote contract missing from live schema/prompt")
		}
	}
	if !strings.Contains(liveAnalysisResponseJSONSchema, `"evidenceSequenceNos", "evidenceSnippets"]`) {
		t.Fatalf("evidenceSnippets is not required by the strict response schema")
	}
}

func TestTreeAuditAndFinalRepairPreserveGroundingContract(t *testing.T) {
	t.Run("audit rewrite", func(t *testing.T) {
		payload, segments, mc := targetTreeAuditFixture(t)
		state := previousLiveAnalysisState(payload)
		roles := classifyTreeAuditEvidence(state, segments)
		operation := treeAuditOperation{
			OperationID: "op-unspoken-detail", Type: TreeAuditRewriteItemTitle,
			TargetCanonicalItemID: "item-risk-rare-plants",
			Label:                 "希少植物への影響はVLAN30の設定漏れが直接原因",
			Reason:                "希少植物の表現を具体化",
			Confidence:            1,
			EvidenceSequenceNos:   []int64{22},
		}
		dry, result := validateAndDryRunTreeAuditOperations(
			state, []treeAuditOperation{operation}, segments, mc, roles,
			TreeAuditConfig{}, "audit-grounding", 13, true,
		)
		if result.OperationsValid != 0 || len(result.Evaluations) != 1 ||
			result.Evaluations[0].Reason != "semantic_grounding_not_verified" {
			t.Fatalf("ungrounded audit rewrite was not rejected: %+v", result)
		}
		if item := findItemByID(dry.Items, "item-risk-rare-plants"); item == nil ||
			strings.Contains(item.Title, "VLAN30") {
			t.Fatalf("rejected audit detail leaked into dry-run tree: %+v", item)
		}
		if result.Evaluations[0].GroundingDecision == "" ||
			len(result.Evaluations[0].UnsupportedAtoms) == 0 {
			t.Fatalf("audit grounding metadata missing: %+v", result.Evaluations[0])
		}
	})

	t.Run("final repair", func(t *testing.T) {
		segments := []domain.TranscriptSegment{{
			SessionID: "session_df2dfedbb1eef84a", SequenceNo: 1,
			Text: "3階で通信障害が発生しました。", IsFinal: true,
		}}
		items := []liveAnalysisItem{
			{
				ID: "item-valid", Kind: "fact", Title: "3階の通信障害",
				Body: "3階で通信障害が発生しました", Status: "open",
				EvidenceSequenceNos: []int64{1}, GroundingDecision: "accepted", evidenceSpecified: true,
			},
			{
				ID: "item-context-only", Kind: "todo", Title: "VPN証明書の更新",
				Body: "高橋さんが来週までにVPN証明書を更新します", Status: "open",
				EvidenceSequenceNos: []int64{1}, GroundingDecision: "candidate_only", evidenceSpecified: true,
			},
		}
		state := liveAnalysisPayload{
			Items: items,
			Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "topic", Label: "会議全体", Origin: topicOriginSystem},
				{ID: "topic-network", Kind: "topic", ParentID: treeRootNodeID, Label: "ネットワーク", Origin: topicOriginDynamic},
				{ID: "item-valid", Kind: "fact", ParentID: "topic-network", Label: "3階の通信障害", Status: "open"},
				{ID: "item-context-only", Kind: "todo", ParentID: "topic-network", Label: "VPN証明書の更新", Status: "open"},
			}},
		}
		rebuildTreeAuditEdges(state.Tree)
		stats := &finalRepairStats{}
		repairFinalItemKinds(
			&state, segments,
			&meetingContext{Background: "高橋さんが来週までにVPN証明書を更新する"},
			2, stats,
		)
		if valid := findItemByID(state.Items, "item-valid"); valid == nil || valid.Inactive {
			t.Fatalf("grounded final item was lost: %+v", valid)
		}
		for _, item := range state.Items {
			if item.Inactive || item.MergedIntoID != "" {
				continue
			}
			if strings.Contains(item.Title+" "+item.Body, "VPN証明書") ||
				strings.Contains(item.Title+" "+item.Body, "高橋さん") ||
				strings.Contains(item.Title+" "+item.Body, "来週") {
				t.Fatalf("final repair retained unspoken detail: %+v", item)
			}
		}
		if stats.GroundingRejected+stats.GroundingCandidateOnly+stats.GroundingTentative == 0 {
			t.Fatalf("final grounding rejection was not observed: %+v", stats)
		}
	})
}
