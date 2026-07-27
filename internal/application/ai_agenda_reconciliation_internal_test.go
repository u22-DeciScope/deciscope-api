package application

import (
	"encoding/json"
	"testing"

	"deciscope-core-api/internal/domain"
)

func agendaReconciliationFixtureContext() *meetingContext {
	return &meetingContext{
		Title: "ネットワーク障害レビュー",
		Agenda: []agendaItem{
			{
				ID: "agenda-1", Title: "障害状況と原因の確認",
				Description: "発生した障害の症状と原因を確認する", Goal: "原因を共有する",
				SemanticHints: []string{"障害状況", "障害原因"}, Order: 1, Role: agendaRolePrimary,
			},
			{
				ID: "agenda-2", Title: "復旧対応の確認",
				Description: "切り戻し、トランク設定、許可VLANの修正と各サービスの正常化を確認する",
				Goal:        "実施した復旧作業と結果を共有する",
				SemanticHints: []string{
					"旧スイッチ", "切り戻し", "トランク設定", "許可VLAN", "有線LAN", "無線LAN",
				},
				Order: 2, Role: agendaRolePrimary,
			},
			{
				ID: "agenda-3", Title: "今後の対応",
				Description: "再発防止と次の対応を確認する", Goal: "今後の担当と対応を整理する",
				SemanticHints: []string{"再発防止", "次の対応"}, Order: 3, Role: agendaRolePrimary,
			},
		},
	}
}

func agendaReconciliationScope(texts map[int64]string, current ...int64) liveEvidenceScope {
	scope := liveEvidenceScope{
		Allowed: make(map[int64]struct{}), CurrentRound: make(map[int64]struct{}),
		TranscriptText: make(map[int64]string), Segments: make(map[int64]domain.TranscriptSegment),
	}
	for sequenceNo, transcript := range texts {
		scope.Allowed[sequenceNo] = struct{}{}
		scope.TranscriptText[sequenceNo] = transcript
		scope.Segments[sequenceNo] = domain.TranscriptSegment{
			SequenceNo: sequenceNo, Text: transcript, IsFinal: true,
		}
		if sequenceNo > scope.CoveredThrough {
			scope.CoveredThrough = sequenceNo
		}
	}
	for _, sequenceNo := range current {
		scope.CurrentRound[sequenceNo] = struct{}{}
	}
	return scope
}

func reconciliationProgressEntryByID(state *agendaProgressState, id string) *agendaProgressEntry {
	if state == nil {
		return nil
	}
	for index := range state.Entries {
		if state.Entries[index].ID == id {
			return &state.Entries[index]
		}
	}
	return nil
}

func TestPlannedAgendaReconciliationRepairsSingleTurnDynamicCandidate(t *testing.T) {
	mc := agendaReconciliationFixtureContext()
	texts := map[int64]string{
		1: "障害状況は通信断で、原因は新スイッチの設定不備でした。",
		2: "復旧対応として、旧スイッチへの切り戻し、トランク設定と許可VLANの修正、有線LAN、無線LAN、ファイルサーバーの正常化確認を行いました。",
		3: "今後の対応についてです。",
	}
	round1 := `{"summary":"原因確認","currentTopic":"障害状況と原因","items":[{"id":"fact-cause","kind":"fact","severity":"high","title":"新スイッチの設定不備が原因","body":"通信断の原因を確認した","status":"open","evidenceSequenceNos":[1]}],"newTopics":[],"assignments":[{"nodeId":"fact-cause","parentTopicId":"agenda-1","confidence":0.9}]}`
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
		round1, nil, mc, 1, []int64{1}, agendaReconciliationScope(texts, 1), TreeClassificationConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}

	round2 := `{"summary":"復旧完了","currentTopic":"現場復旧手順標準化","items":[{"id":"fact-recovery","kind":"fact","severity":"high","title":"切り戻しと設定修正で各サービスを正常化","body":"旧スイッチへ切り戻し、トランク設定と許可VLANを修正して有線LAN、無線LAN、ファイルサーバーの正常化を確認した","status":"open","evidenceSequenceNos":[2]}],"newTopics":[{"id":"topic-unknown-1","label":"現場復旧手順標準化","description":"切り戻しと設定修正によるサービス復旧"}],"assignments":[{"nodeId":"fact-recovery","parentTopicId":"topic-unknown-1","confidence":0.88,"reason":"new subject"}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(
		round2, raw, mc, 2, []int64{2}, agendaReconciliationScope(texts, 2),
		TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}, stats,
	)
	if err != nil {
		t.Fatal(err)
	}
	state2 := previousLiveAnalysisState(raw)
	agenda2Topic := agendaTopicNodeByRef(state2.Tree, "agenda-2")
	recovery := findItemByID(state2.Items, "fact-recovery")
	if agenda2Topic == nil || agenda2Topic.ID == "agenda-2" || recovery == nil ||
		itemTopicID(state2.Tree, recovery.ID) != agenda2Topic.ID {
		t.Fatalf("agenda2Topic=%+v recovery=%+v tree=%+v", agenda2Topic, recovery, state2.Tree)
	}
	if recovery.CandidateTopicID != "" || len(state2.EmergingTopics) != 0 {
		t.Fatalf("dynamic candidate remained after planned-agenda reconciliation: item=%+v candidates=%+v", recovery, state2.EmergingTopics)
	}
	if reconciliationProgressEntryByID(state2.AgendaProgress, "agenda-2").ComputedStatus == agendaProgressNotStarted {
		t.Fatalf("agenda-2 progress=%+v", state2.AgendaProgress)
	}
	foundDecision := false
	for _, decision := range stats.AgendaReconciliations {
		if decision.Trigger == agendaReconciliationDynamicCandidate &&
			decision.ItemID == "fact-recovery" && decision.SelectedAgendaID == "agenda-2" {
			foundDecision = true
		}
	}
	if !foundDecision {
		t.Fatalf("reconciliation decision missing: %+v", stats.AgendaReconciliations)
	}

	round3 := `{"summary":"今後の対応へ移行","currentTopic":"今後の対応","items":[],"newTopics":[],"assignments":[]}`
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(
		round3, raw, mc, 3, []int64{3}, agendaReconciliationScope(texts, 3), TreeClassificationConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}
	state3 := previousLiveAnalysisState(raw)
	entry2 := reconciliationProgressEntryByID(state3.AgendaProgress, "agenda-2")
	if entry2 == nil || entry2.ComputedStatus != agendaProgressDiscussed {
		t.Fatalf("agenda-2 did not complete before explicit agenda-3 transition: %+v", state3.AgendaProgress)
	}
	if got := itemTopicID(state3.Tree, "fact-recovery"); got != agenda2Topic.ID {
		t.Fatalf("recovery item moved to %q, want %q", got, agenda2Topic.ID)
	}
	for _, ref := range []string{"agenda-1", "agenda-3"} {
		if containsExactString(agenda2Topic.AgendaRefs, ref) {
			t.Fatalf("agenda-2 topic was contaminated with %s: %+v", ref, agenda2Topic)
		}
	}
	if integrity := validateTreeIntegrity(state3.Tree, state3.Items, mc, state3.AgendaAnchors); !integrity.Valid {
		t.Fatalf("tree integrity=%+v", integrity)
	}
}

func TestSkippedAgendaBackfillRequiresSubstantiveMatchingEvidence(t *testing.T) {
	mc := agendaReconciliationFixtureContext()
	candidateID := "candidate-recovery"
	dynamicTopicID := "topic-recovery"
	previous := liveAnalysisPayload{
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Origin: topicOriginSystem},
			{ID: dynamicTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "復旧作業", Origin: topicOriginDynamic},
			{ID: "item-recent", Kind: "fact", ParentID: dynamicTopicID, Label: "直前の発言"},
		}},
		AgendaProgress: &agendaProgressState{
			ComputedCurrentTopicID: "agenda-1",
			Entries: []agendaProgressEntry{
				{ID: "agenda-1", ComputedStatus: agendaProgressDiscussing},
				{ID: "agenda-2", ComputedStatus: agendaProgressNotStarted},
				{ID: "agenda-3", ComputedStatus: agendaProgressNotStarted},
			},
		},
		EmergingTopics: []emergingTopicCandidate{{
			ID: candidateID, Label: "復旧作業", EvidenceItemIDs: []string{"item-recent"},
		}},
	}
	spans := []agendaContextSpan{
		{Mode: agendaContextModeFixed, AgendaID: "agenda-1", StartSequenceNo: 1, Explicit: true},
		{Mode: agendaContextModeFixed, AgendaID: "agenda-3", StartSequenceNo: 3, Explicit: true},
	}
	tests := []struct {
		name       string
		title      string
		body       string
		transcript string
		wantAgenda bool
	}{
		{
			name:       "substantive recovery",
			title:      "切り戻しと設定修正で通信を正常化",
			body:       "旧スイッチへ切り戻し、トランク設定と許可VLANを修正した",
			transcript: "復旧対応として旧スイッチへ切り戻し、トランク設定と許可VLANを修正して通信を正常化しました。",
			wantAgenda: true,
		},
		{name: "unrelated", title: "懇親会の会場候補", body: "駅前の店を予約する", transcript: "懇親会は駅前の店を予約します。"},
		{name: "name only", title: "復旧対応の確認", body: "復旧対応の確認", transcript: "復旧対応の確認。"},
		{name: "preview only", title: "復旧対応は後で確認する", body: "後で話す予定", transcript: "復旧対応は後で話します。"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := liveAnalysisItem{
				ID: "item-recent", Kind: "fact", Title: tt.title, Body: tt.body,
				ClassificationStatus: classificationTentative, CandidateTopicID: candidateID,
				EvidenceSequenceNos: []int64{2},
			}
			scope := agendaReconciliationScope(map[int64]string{
				2: tt.transcript, 3: "今後の対応についてです。",
			}, 3)
			timeline := classifyDiscourseTimeline(scope)
			stats := &liveAnalysisTreeMergeStats{}
			got := backfillSkippedAgendaAssignments(
				[]treeAssignment{{NodeID: item.ID, ParentTopicID: candidateID}},
				previous, []liveAnalysisItem{item}, mc, spans, []int64{3}, timeline, scope, stats,
			)
			parent := ""
			for _, assignment := range got {
				if assignment.nodeID() == item.ID {
					parent = assignment.ParentTopicID
				}
			}
			if tt.wantAgenda && parent != "agenda-2" {
				t.Fatalf("parent=%q decisions=%+v", parent, stats.AgendaReconciliations)
			}
			if tt.wantAgenda && (len(stats.AgendaReconciliations) != 1 ||
				!stats.AgendaReconciliations[0].TransitionDirect ||
				!stats.AgendaReconciliations[0].BackfillPerformed ||
				!containsExactString(stats.AgendaReconciliations[0].SkippedAgendaIDs, "agenda-2")) {
				t.Fatalf("transition/backfill observability=%+v", stats.AgendaReconciliations)
			}
			if !tt.wantAgenda && parent == "agenda-2" {
				t.Fatalf("weak evidence backfilled agenda-2: decisions=%+v", stats.AgendaReconciliations)
			}
		})
	}
}

func TestDynamicCandidateReconciliationPreservesIndependentAndAmbiguousTopics(t *testing.T) {
	tests := []struct {
		name string
		mc   *meetingContext
		item string
	}{
		{
			name: "independent topic",
			mc:   agendaReconciliationFixtureContext(),
			item: `{"id":"todo-lunch","kind":"todo","severity":"low","title":"懇親会会場を予約する","body":"駅前の店へ参加人数を連絡する","status":"open","evidenceSequenceNos":[4]}`,
		},
		{
			name: "ambiguous planned agendas",
			mc: &meetingContext{Agenda: []agendaItem{
				{ID: "agenda-1", Title: "ネットワーク復旧作業", Description: "スイッチ設定と通信復旧を確認する", Order: 1, Role: agendaRolePrimary},
				{ID: "agenda-2", Title: "通信復旧対応", Description: "スイッチ設定と通信復旧を確認する", Order: 2, Role: agendaRolePrimary},
			}},
			item: `{"id":"fact-switch","kind":"fact","severity":"high","title":"スイッチ設定を修正して通信を復旧","body":"設定修正後に通信の正常化を確認した","status":"open","evidenceSequenceNos":[4]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := `{"summary":"","currentTopic":"追加論点","items":[` + tt.item + `],"newTopics":[{"id":"topic-unknown-4","label":"独立した追加論点","description":"予定外の具体的な検討"}],"assignments":[{"nodeId":"` +
				func() string {
					if tt.name == "independent topic" {
						return "todo-lunch"
					}
					return "fact-switch"
				}() + `","parentTopicId":"topic-unknown-4","confidence":0.9}]}`
			scope := agendaReconciliationScope(map[int64]string{4: "スイッチ設定の確認と、懇親会会場の予約について話しました。"}, 4)
			raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
				content, nil, tt.mc, 1, []int64{4}, scope,
				TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2},
			)
			if err != nil {
				t.Fatal(err)
			}
			state := previousLiveAnalysisState(raw)
			if len(state.EmergingTopics) != 1 {
				t.Fatalf("candidate was forced into a planned agenda: tree=%+v candidates=%+v", state.Tree.Nodes, state.EmergingTopics)
			}
		})
	}
}

func TestExistingDynamicAndUnassignedImportantItemsAreReconsidered(t *testing.T) {
	mc := agendaReconciliationFixtureContext()
	previous := liveAnalysisPayload{Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem},
		{ID: "topic-existing-dynamic", Kind: "topic", ParentID: treeRootNodeID, Label: "現場復旧手順標準化", Origin: topicOriginDynamic},
	}}}
	previousRaw, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		assignment string
	}{
		{name: "existing dynamic topic", assignment: `{"nodeId":"fact-recovery","parentTopicId":"topic-existing-dynamic","confidence":0.9}`},
		{name: "important item without parent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assignments := ""
			if tt.assignment != "" {
				assignments = tt.assignment
			}
			content := `{"summary":"復旧","currentTopic":"現場復旧","items":[{"id":"fact-recovery","kind":"fact","severity":"high","title":"切り戻しと設定修正で通信を正常化","body":"旧スイッチへ切り戻し、トランク設定と許可VLANを修正した","status":"open","evidenceSequenceNos":[10]}],"newTopics":[],"assignments":[` + assignments + `]}`
			scope := agendaReconciliationScope(map[int64]string{
				10: "復旧対応として旧スイッチへ切り戻し、トランク設定と許可VLANを修正して通信を正常化しました。",
			}, 10)
			raw, parseErr := parseAndMergeLiveAnalysisPayloadWithEvidence(
				content, previousRaw, mc, 2, []int64{10}, scope, TreeClassificationConfig{},
			)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			state := previousLiveAnalysisState(raw)
			topic := agendaTopicNodeByRef(state.Tree, "agenda-2")
			if topic == nil || itemTopicID(state.Tree, "fact-recovery") != topic.ID {
				t.Fatalf("important item was not reconsidered: tree=%+v items=%+v", state.Tree.Nodes, state.Items)
			}
		})
	}
}

func finalReconciliationFixture(t *testing.T, itemTitle, itemBody, transcript string) (json.RawMessage, *meetingContext, []domain.TranscriptSegment) {
	t.Helper()
	mc := &meetingContext{Title: "復旧レビュー", Agenda: []agendaItem{{
		ID: "agenda-2", Title: "復旧対応の確認",
		Description: "切り戻し、トランク設定、許可VLANの修正とサービス正常化を確認する",
		Goal:        "復旧作業の結果を共有する", SemanticHints: []string{"旧スイッチ", "許可VLAN", "正常化"},
		Order: 1, Role: agendaRolePrimary,
	}}}
	candidateID := "candidate-recovery"
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{{
			ID: "fact-recovery", Kind: "fact", Title: itemTitle, Body: itemBody, Status: "open",
			InformationStatus:    informationStatusGrounded,
			ClassificationStatus: classificationTentative, CandidateTopicID: candidateID,
			EvidenceSequenceNos: []int64{10},
		}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "復旧レビュー", Origin: topicOriginSystem},
			{ID: "topic-dynamic-recovery", Kind: "topic", ParentID: treeRootNodeID, Label: "現場復旧手順標準化", Origin: topicOriginDynamic},
			{ID: "fact-recovery", Kind: "fact", ParentID: "topic-dynamic-recovery", Label: itemTitle, Description: itemBody},
		}},
		EmergingTopics: []emergingTopicCandidate{{
			ID: candidateID, Label: "現場復旧手順標準化", EvidenceItemIDs: []string{"fact-recovery"},
		}},
		AgendaAnchors: []agendaAnchor{{
			AgendaID: "agenda-2", OriginalTitle: "復旧対応の確認", Order: 1,
			Role: agendaRolePrimary, Status: agendaStatusNotDiscussed,
		}},
		AgendaProgress: &agendaProgressState{Entries: []agendaProgressEntry{{
			ID: "agenda-2", SourceType: agendaProgressSourceFixed, Title: "復旧対応の確認",
			ComputedStatus: agendaProgressNotStarted,
		}}},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return raw, mc, []domain.TranscriptSegment{{
		SessionID: "session-fixture", SequenceNo: 10, Text: transcript, IsFinal: true,
	}}
}

func TestFinalizationReconciliationRepairsTreeProgressAndIsIdempotent(t *testing.T) {
	raw, mc, segments := finalReconciliationFixture(
		t,
		"切り戻しと設定修正でサービスを正常化",
		"旧スイッチへ切り戻し、トランク設定と許可VLANを修正した",
		"復旧対応として旧スイッチへ切り戻し、トランク設定と許可VLANを修正して各サービスの正常化を確認しました。",
	)
	finalized, decisions, err := finalizeAgendaLifecyclePayloadWithEvidence(raw, mc, 12, segments)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(finalized)
	topic := agendaTopicNodeByRef(state.Tree, "agenda-2")
	entry := reconciliationProgressEntryByID(state.AgendaProgress, "agenda-2")
	if topic == nil || topic.ID == "agenda-2" || itemTopicID(state.Tree, "fact-recovery") != topic.ID {
		t.Fatalf("topic=%+v tree=%+v", topic, state.Tree)
	}
	if entry == nil || entry.ComputedStatus != agendaProgressDiscussed ||
		len(entry.MaterializedTopicIDs) != 1 || entry.MaterializedTopicIDs[0] != topic.ID {
		t.Fatalf("progress=%+v", state.AgendaProgress)
	}
	if item := findItemByID(state.Items, "fact-recovery"); item == nil ||
		item.CandidateTopicID != "" || item.AssignmentReason != agendaReconciliationFinalization {
		t.Fatalf("item=%+v", item)
	}
	if treeNodeByID(state.Tree, "topic-dynamic-recovery") != nil || len(state.EmergingTopics) != 0 {
		t.Fatalf("empty dynamic structures were not pruned: tree=%+v candidates=%+v", state.Tree.Nodes, state.EmergingTopics)
	}
	if len(decisions) != 1 || decisions[0].SelectedAgendaID != "agenda-2" ||
		!decisions[0].AgendaRefsRepaired || !decisions[0].ItemMoved {
		t.Fatalf("decisions=%+v", decisions)
	}
	if integrity := validateTreeIntegrity(state.Tree, state.Items, mc, state.AgendaAnchors); !integrity.Valid {
		t.Fatalf("integrity=%+v", integrity)
	}

	again, secondDecisions, err := finalizeAgendaLifecyclePayloadWithEvidence(finalized, mc, 12, segments)
	if err != nil {
		t.Fatal(err)
	}
	stateAgain := previousLiveAnalysisState(again)
	if len(stateAgain.Tree.Nodes) != len(state.Tree.Nodes) ||
		agendaTopicNodeByRef(stateAgain.Tree, "agenda-2").ID != topic.ID ||
		len(secondDecisions) != 0 {
		t.Fatalf("final reconciliation is not idempotent: nodes=%d/%d decisions=%+v", len(stateAgain.Tree.Nodes), len(state.Tree.Nodes), secondDecisions)
	}
}

func TestFinalizationReconciliationRejectsWeakMatchAndManualStatusWins(t *testing.T) {
	raw, mc, segments := finalReconciliationFixture(
		t, "製品担当者の予定", "同じ担当者が別製品を説明した", "復旧対応は後で確認します。",
	)
	finalized, decisions, err := finalizeAgendaLifecyclePayloadWithEvidence(raw, mc, 12, segments)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(finalized)
	if topic := agendaTopicNodeByRef(state.Tree, "agenda-2"); topic != nil {
		t.Fatalf("weak/preview-only evidence repaired agenda: %+v", topic)
	}
	entry := reconciliationProgressEntryByID(state.AgendaProgress, "agenda-2")
	if entry == nil || entry.ComputedStatus != agendaProgressNotStarted {
		t.Fatalf("progress=%+v", state.AgendaProgress)
	}
	if len(decisions) != 1 || decisions[0].RejectedReason == "" {
		t.Fatalf("no-change finalization decision missing: %+v", decisions)
	}

	// Automatic final reconciliation may update ComputedStatus, but the
	// durable owner/admin override remains the delivery-time authority.
	entry.ComputedStatus = agendaProgressDiscussed
	notStartedOverride := &AgendaProgressOverrides{StatusOverrides: map[string]string{
		"agenda-2": agendaProgressNotStarted,
	}}
	stamped := applyAgendaProgressOverrides(state.AgendaProgress, notStartedOverride)
	if got := reconciliationProgressEntryByID(stamped, "agenda-2"); got == nil ||
		got.ManualStatus != agendaProgressNotStarted || got.EffectiveStatus != agendaProgressNotStarted {
		t.Fatalf("manual not-started override lost: %+v", stamped)
	}
	entry.ComputedStatus = agendaProgressNotStarted
	discussedOverride := &AgendaProgressOverrides{StatusOverrides: map[string]string{
		"agenda-2": agendaProgressDiscussed,
	}}
	stamped = applyAgendaProgressOverrides(state.AgendaProgress, discussedOverride)
	if got := reconciliationProgressEntryByID(stamped, "agenda-2"); got == nil ||
		got.ManualStatus != agendaProgressDiscussed || got.EffectiveStatus != agendaProgressDiscussed {
		t.Fatalf("manual discussed override lost: %+v", stamped)
	}
	annotated := annotateAgendaReconciliationManualOverrides(decisions, discussedOverride)
	if len(annotated) != 1 || !annotated[0].ManualOverride {
		t.Fatalf("manual override was not observable: %+v", annotated)
	}
}

func TestFinalizationReconciliationRejectsRecapOnlyEvidence(t *testing.T) {
	raw, mc, segments := finalReconciliationFixture(
		t,
		"復旧対応の振り返り",
		"旧スイッチへの切り戻しと許可VLAN修正を再掲した",
		"以上をまとめます。復旧対応では旧スイッチへの切り戻しと許可VLAN修正を行いました。",
	)
	finalized, decisions, err := finalizeAgendaLifecyclePayloadWithEvidence(raw, mc, 12, segments)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(finalized)
	if topic := agendaTopicNodeByRef(state.Tree, "agenda-2"); topic != nil {
		t.Fatalf("recap-only evidence repaired agenda: %+v", topic)
	}
	if entry := reconciliationProgressEntryByID(state.AgendaProgress, "agenda-2"); entry == nil ||
		entry.ComputedStatus != agendaProgressNotStarted {
		t.Fatalf("progress=%+v decisions=%+v", state.AgendaProgress, decisions)
	}
}
