package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

// The repository does not contain the raw session_fba1bbbdb0c819b7 export.
// These regressions use the anonymized production fixture for the same meeting
// and the same sequence-10 double-generation shape.

func TestSessionFBAIssueSynthesisSuppressesSameEvidenceModelRepresentation(t *testing.T) {
	model := `{
		"items":[{
			"id":"todo-floor-2","kind":"todo",
			"title":"2階の通信遅延原因を確認",
			"body":"2階の通信遅延をVLAN設定だけで説明できるかは未解決で、調査を継続する",
			"status":"open","evidenceSequenceNos":[10]
		}],
		"assignments":[{"itemId":"todo-floor-2","parentId":"agenda-2","confidence":0.8,"reason":"原因調査"}]
	}`
	candidates := []issueCandidate{{
		SequenceNo: 10, Subtype: issueSubtypeInvestigation,
		Statement: "2階で発生した通信遅延をVLAN設定だけで説明できるかは確認できていません。この点は未解決の調査事項として残しています",
	}}
	reconciled, audit, err := reconcileIssueCandidates(model, nil, candidates)
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Items) != 1 || diff.Items[0].ID != "todo-floor-2" {
		t.Fatalf("items=%+v, want only the concrete model item", diff.Items)
	}
	if audit.SameEvidenceSynthesisSuppressed != 1 || audit.OpenIssuesAccepted != 0 {
		t.Fatalf("audit=%+v, want one same-evidence suppression and no synthesized issue", audit)
	}
	if len(audit.Decisions) != 1 || audit.Decisions[0].Reason != "model_item_represents_same_unresolved_evidence" {
		t.Fatalf("decisions=%+v", audit.Decisions)
	}
}

func TestIssueSynthesisPreservesIndependentIssuesFromOneEvidence(t *testing.T) {
	model := `{
		"items":[{
			"id":"issue-cause-a","kind":"issue","subtype":"investigation",
			"title":"原因Aが未確定","body":"原因Aが未確定","status":"open",
			"evidenceSequenceNos":[20]
		}]
	}`
	candidates := []issueCandidate{
		{SequenceNo: 20, Subtype: issueSubtypeInvestigation, Statement: "原因Aが未確定"},
		{SequenceNo: 20, Subtype: issueSubtypeDiscussion, Statement: "通知条件Bが未確定"},
	}
	reconciled, audit, err := reconcileIssueCandidates(model, nil, candidates)
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Items) != 2 {
		t.Fatalf("items=%+v, want represented A plus independently synthesized B", diff.Items)
	}
	if audit.SameEvidenceSynthesisSuppressed != 1 || audit.OpenIssuesAccepted != 1 {
		t.Fatalf("audit=%+v", audit)
	}
}

func TestIssueSynthesisEnrichesAbstractModelItemFromConcreteSameEvidence(t *testing.T) {
	model := `{
		"items":[{
			"id":"issue-abstract","kind":"issue","subtype":"investigation",
			"title":"原因が未確定","body":"原因が未確定","status":"open",
			"evidenceSequenceNos":[20]
		}],
		"assignments":[{"itemId":"issue-abstract","parentId":"agenda-2","confidence":0.7}]
	}`
	candidate := issueCandidate{
		SequenceNo: 20, Subtype: issueSubtypeInvestigation,
		Statement: "2階の通信遅延をVLAN設定だけで説明できるか未確定",
	}
	reconciled, audit, err := reconcileIssueCandidates(model, nil, []issueCandidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Items) != 1 || diff.Items[0].ID != "issue-abstract" ||
		!strings.Contains(diff.Items[0].Title, "2階") ||
		diff.Items[0].InformationStatus != informationStatusGrounded {
		t.Fatalf("items=%+v", diff.Items)
	}
	if len(audit.Decisions) != 1 || audit.Decisions[0].Decision != "rewritten" ||
		audit.Decisions[0].Reason != "abstract_model_item_enriched_from_same_evidence" {
		t.Fatalf("audit=%+v", audit)
	}
}

func TestRecapCorruptedUnmatchedSubjectCannotCreateNewNode(t *testing.T) {
	previous := []liveAnalysisItem{{
		ID: "issue-vlan", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title: "VLAN設定漏れの原因確認", Body: "VLAN設定漏れの因果関係を確認する",
		Status: "open", EvidenceSequenceNos: []int64{1},
	}}
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "VLAN設定漏れの因果関係を確認します。",
		2: "ここまでをまとめます。",
		3: "関心あなたの条件が未確定です。",
	}, 3)
	timeline := classifyDiscourseTimeline(scope)
	diff := []liveAnalysisItem{{
		ID: "issue-corrupt-recap", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "関心あなたの条件が未確定", Body: "関心あなたの条件が未確定",
		Status: "open", EvidenceSequenceNos: []int64{3}, evidenceSpecified: true,
	}}
	stats := &liveAnalysisTreeMergeStats{}
	filtered := filterReferenceRecapDiff(previous, diff, []int64{3}, timeline, scope, stats)
	if len(filtered) != 0 {
		t.Fatalf("filtered=%+v, want corrupted unmatched recap rejected", filtered)
	}
	if stats.ReferenceRecapItemsRejected != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestSessionFBASequence28SplitRecapDoesNotDisplayCorruptedFragment(t *testing.T) {
	payload, segments, _ := loadSession2dee3b1da5b72979Fixture(t)
	previous := previousLiveAnalysisState(payload)
	texts := make(map[int64]string, len(segments))
	for _, segment := range segments {
		texts[segment.SequenceNo] = segment.Text
	}
	scope := evidenceScopeFromTexts(texts, 28)
	timeline := classifyDiscourseTimeline(scope)
	if timeline.Roles[28] != liveEvidenceReferenceRecap {
		t.Fatalf("sequence 28 role=%s, want reference recap", timeline.Roles[28])
	}
	diff := []liveAnalysisItem{{
		ID: "issue-sequence-28", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "2階の通信遅延の原因と感謝アラートの条件が未解決",
		Body:  texts[28], Status: "open", EvidenceSequenceNos: []int64{28},
		evidenceSpecified: true,
	}}
	stats := &liveAnalysisTreeMergeStats{}
	split, _ := repairLowInformationIssueItems(previous.Items, diff, nil, timeline, scope, stats)
	splitCount := 0
	for _, item := range split {
		if strings.HasPrefix(item.ID, "issue-sequence-28-split-") {
			splitCount++
		}
	}
	if splitCount != 2 {
		t.Fatalf("split=%+v, want two fragments before recap validation", split)
	}
	filtered := filterReferenceRecapDiff(previous.Items, split, []int64{28}, timeline, scope, stats)
	filteredIDs := make([]string, 0, len(filtered))
	for _, item := range filtered {
		filteredIDs = append(filteredIDs, item.ID)
		if strings.Contains(item.ID, "split-2") || strings.Contains(item.Title, "感謝") {
			t.Fatalf("corrupted recap fragment survived independently: %+v", item)
		}
	}
	t.Logf("sequence28 split decisions: filteredItemIds=%v recapMerged=%d recapRejected=%d splitRejected=%d",
		filteredIDs, stats.ReferenceRecapItemsMerged, stats.ReferenceRecapItemsRejected,
		stats.LowInformationSplitFragmentsRejected)
	if stats.ReferenceRecapItemsMerged+stats.ReferenceRecapItemsRejected < 2 {
		t.Fatalf("stats=%+v, want both fragments decided by recap gate", stats)
	}
}

func TestAmbiguousAgendaMatchFallsBackToCurrentWithoutAdvancing(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-2", Title: "ネットワーク障害の原因調査", Order: 2, Role: agendaRolePrimary, SemanticHints: []string{"VLAN", "スイッチ"}},
		{ID: "agenda-3", Title: "ネットワーク障害の原因調査", Order: 3, Role: agendaRolePrimary, SemanticHints: []string{"VLAN", "スイッチ"}},
	}}
	item := liveAnalysisItem{
		ID: "issue-ambiguous", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title: "VLANスイッチ設定の原因調査", Body: "VLANスイッチ設定と通信障害の因果関係を調査する",
		Status: "open", ClassificationStatus: classificationTentative,
		CandidateTopicID: "topic-model", EvidenceSequenceNos: []int64{30},
	}
	scope := evidenceScopeFromTexts(map[int64]string{30: "VLANスイッチ設定と通信障害の因果関係を詳しく調査します。"}, 30)
	timeline := classifyDiscourseTimeline(scope)
	previous := liveAnalysisPayload{
		AgendaProgress: &agendaProgressState{ComputedCurrentTopicID: "agenda-2", Entries: []agendaProgressEntry{
			{ID: "agenda-2", ComputedStatus: agendaProgressDiscussing},
			{ID: "agenda-3", ComputedStatus: agendaProgressNotStarted},
		}},
	}
	stats := &liveAnalysisTreeMergeStats{}
	assignments := reconcileDynamicCandidateAssignments(
		[]treeAssignment{{NodeID: item.ID, ParentTopicID: "topic-model", Confidence: 0.7}},
		[]liveAnalysisTreeNode{{ID: "topic-model", Kind: "topic", Label: "VLAN原因調査"}},
		previous, []liveAnalysisItem{item}, []liveAnalysisItem{item}, mc, nil, timeline, scope, stats,
	)
	if len(assignments) != 1 || assignments[0].ParentTopicID != "agenda-2" ||
		assignments[0].Reason != agendaReconciliationAmbiguousFallback {
		t.Fatalf("assignments=%+v, want current agenda fallback", assignments)
	}
	if len(stats.AgendaReconciliations) != 1 ||
		stats.AgendaReconciliations[0].RejectedReason != "ambiguous_agenda_match_active_fallback" ||
		stats.AgendaReconciliations[0].SelectedAgendaID != "agenda-2" {
		t.Fatalf("decisions=%+v", stats.AgendaReconciliations)
	}
}

func TestAmbiguousAgendaMatchWithoutCurrentStaysUnclassified(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "障害原因調査", Order: 1, Role: agendaRolePrimary, SemanticHints: []string{"VLAN"}},
		{ID: "agenda-2", Title: "障害原因調査", Order: 2, Role: agendaRolePrimary, SemanticHints: []string{"VLAN"}},
	}}
	item := liveAnalysisItem{
		ID: "issue-ambiguous", Kind: "issue", Subtype: issueSubtypeInvestigation,
		Title: "VLAN障害原因調査", Body: "VLAN障害原因を調査する", Status: "open",
		ClassificationStatus: classificationTentative, CandidateTopicID: "topic-model",
		EvidenceSequenceNos: []int64{30},
	}
	scope := evidenceScopeFromTexts(map[int64]string{30: "VLAN障害原因を詳しく調査します。"}, 30)
	stats := &liveAnalysisTreeMergeStats{}
	assignments := reconcileDynamicCandidateAssignments(
		[]treeAssignment{{NodeID: item.ID, ParentTopicID: "topic-model"}},
		[]liveAnalysisTreeNode{{ID: "topic-model", Kind: "topic", Label: "VLAN原因調査"}},
		liveAnalysisPayload{}, []liveAnalysisItem{item}, []liveAnalysisItem{item}, mc, nil,
		classifyDiscourseTimeline(scope), scope, stats,
	)
	if len(assignments) != 1 || assignments[0].ParentTopicID != treeUnclassifiedTopicID {
		t.Fatalf("assignments=%+v, want unclassified fallback", assignments)
	}
}

func TestSessionFBAFinalRepairRemovesSameEvidenceLowInformationIssue(t *testing.T) {
	payload, segments, mc := loadSession2dee3b1da5b72979Fixture(t)
	repaired, stats := applyDeterministicFinalTreeRepairs(payload, mc, 13, finalRepairInput{
		Segments: segments,
		Audit:    TreeAuditConfig{},
	})
	if stats.Error != "" || stats.IntegrityRejected {
		t.Fatalf("stats=%+v", stats)
	}
	if stats.LowInformationItemsMerged+stats.SameEvidenceSynthesisMerged+stats.LowInformationItemsRejected == 0 {
		t.Fatalf("stats=%+v, want the low-information sequence-10 item repaired", stats)
	}
	state := previousLiveAnalysisState(repaired)
	if treeNodeByID(state.Tree, "open-issue-auto-dde3edac015b") != nil ||
		findItemByID(state.Items, "open-issue-auto-dde3edac015b") != nil {
		t.Fatalf("low-information item survived final repair")
	}
	if findItemByID(state.Items, "item-todo-58ca6b112b46") == nil {
		t.Fatal("concrete sequence-10 canonical item was removed")
	}
	if !stats.ValidatorsRerun {
		t.Fatalf("validators were not rerun: %+v", stats)
	}
}

func TestThirdUnappliedAuditRunsDeterministicFallback(t *testing.T) {
	response := treeAuditResponse{
		BasedOnTreeVersion: 13,
		Findings: []treeAuditFinding{{
			FindingID: "finding-low-info", Type: TreeAuditLowInformationItem,
			Severity: "high", NodeIDs: []string{"open-issue-auto-dde3edac015b"},
			EvidenceSequenceNos: []int64{10}, Reason: "low information", Confidence: 0.95,
		}},
		Operations: []treeAuditOperation{},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	service, repository, auditRepository, _, _, payload := newSession2dee3b1da5b72979RunnerFixture(t, string(encoded))
	service.config.TreeAudit.UnappliedWarningThreshold = 3
	auditRepository.runs = append(auditRepository.runs, domain.MeetingTreeAuditRun{
		ID: "previous-audit", SessionID: session2dee3b1da5b72979ID,
		Task: "older-task", Status: domain.MeetingTreeAuditCompleted,
		ResultClassification:     domain.MeetingTreeAuditResultRejected,
		ConsecutiveUnappliedRuns: 2,
	})
	execution, err := service.runTreeAudit(
		context.Background(), session2dee3b1da5b72979ID, "manual_replay",
		aiTaskTreeAudit, payload, 13, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !execution.Applied || execution.Version != 14 || execution.Result != "deterministic_fallback_applied" {
		t.Fatalf("execution=%+v", execution)
	}
	run := auditRepository.latest()
	var validator treeAuditValidatorResult
	if err := json.Unmarshal(run.ValidatorResult, &validator); err != nil {
		t.Fatal(err)
	}
	if !validator.DeterministicFallbackEvaluated || !validator.DeterministicFallbackApplied ||
		validator.DeterministicFallbackAction != "apply_safe_repair" {
		t.Fatalf("validator=%+v", validator)
	}
	if repository.version(session2dee3b1da5b72979ID) != 14 {
		t.Fatalf("version=%d, want 14", repository.version(session2dee3b1da5b72979ID))
	}
}

func TestFinalSnapshotAndLiveProjectionAreIdenticalAtSameVersionAndTime(t *testing.T) {
	repository := newContextBarrierRepository()
	sessionID := "session-final-projection-contract"
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{{
			ID: "fact-1", Kind: "fact", Title: "障害復旧を確認",
			Body: "障害復旧を確認した", Status: "open", EvidenceSequenceNos: []int64{1},
		}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
			{ID: "topic-recovery", Kind: "topic", ParentID: treeRootNodeID, Label: "復旧確認", Origin: topicOriginDynamic},
			{ID: "fact-1", Kind: "fact", ParentID: "topic-recovery", Label: "障害復旧を確認", Status: "open"},
		}},
		TreeVersion: 7, CoveredThroughSequenceNo: 1,
	}
	rebuildTreeAuditEdges(state.Tree)
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	previousTime := time.Date(2026, 7, 27, 1, 2, 3, 0, time.UTC)
	if _, err := repository.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{
		SessionID: sessionID, Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 7,
		Payload: payload, UpdatedAt: previousTime,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewMeetingAnalysisService(repository, nil, nil, nil, MeetingAnalysisConfig{})
	service.now = func() time.Time { return previousTime.Add(time.Second) }
	if !service.persistFinalTreeSnapshot(context.Background(), sessionID, payload, 7, 1, 1, nil) {
		t.Fatal("persistFinalTreeSnapshot returned false")
	}
	live, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisLive)
	if err != nil {
		t.Fatal(err)
	}
	treeAnalysis, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisTree)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot treeSnapshotPayload
	if err := json.Unmarshal(treeAnalysis.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	liveState := previousLiveAnalysisState(live.Payload)
	if live.Version != treeAnalysis.Version || !live.UpdatedAt.Equal(treeAnalysis.UpdatedAt) {
		t.Fatalf("live={v:%d t:%s} tree={v:%d t:%s}", live.Version, live.UpdatedAt, treeAnalysis.Version, treeAnalysis.UpdatedAt)
	}
	if len(liveState.Tree.Nodes) != len(snapshot.Tree.Nodes) ||
		liveTreeHash(liveState.Tree) != liveTreeHash(snapshot.Tree) {
		t.Fatalf("live nodes/hash=%d/%s tree nodes/hash=%d/%s",
			len(liveState.Tree.Nodes), liveTreeHash(liveState.Tree),
			len(snapshot.Tree.Nodes), liveTreeHash(snapshot.Tree))
	}
}

type projectionFailureRepository struct {
	*contextBarrierRepository
	failType domain.MeetingAIAnalysisType
}

func (r *projectionFailureRepository) UpsertMeetingAIAnalysis(ctx context.Context, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, error) {
	if analysis.Type == r.failType {
		return nil, errors.New("injected projection persistence failure")
	}
	return r.contextBarrierRepository.UpsertMeetingAIAnalysis(ctx, analysis)
}

func TestFinalSnapshotIsNotPublishedWhenLiveProjectionSyncFails(t *testing.T) {
	base := newContextBarrierRepository()
	sessionID, payload := seedProjectionContractLive(t, base)
	repository := &projectionFailureRepository{
		contextBarrierRepository: base,
		failType:                 domain.MeetingAIAnalysisLive,
	}
	service := NewMeetingAnalysisService(repository, nil, nil, nil, MeetingAnalysisConfig{})
	if service.persistFinalTreeSnapshot(context.Background(), sessionID, payload, 7, 1, 1, nil) {
		t.Fatal("snapshot unexpectedly succeeded")
	}
	if _, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisTree); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tree lookup error=%v, want not found", err)
	}
}

func TestFinalSnapshotFailureLeavesSynchronizedLiveWithoutDivergentFinalRow(t *testing.T) {
	base := newContextBarrierRepository()
	sessionID, payload := seedProjectionContractLive(t, base)
	repository := &projectionFailureRepository{
		contextBarrierRepository: base,
		failType:                 domain.MeetingAIAnalysisTree,
	}
	service := NewMeetingAnalysisService(repository, nil, nil, nil, MeetingAnalysisConfig{})
	service.now = func() time.Time { return time.Date(2026, 7, 27, 2, 3, 4, 0, time.UTC) }
	if service.persistFinalTreeSnapshot(context.Background(), sessionID, payload, 7, 1, 1, nil) {
		t.Fatal("snapshot unexpectedly succeeded")
	}
	live, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisLive)
	if err != nil {
		t.Fatal(err)
	}
	liveState := previousLiveAnalysisState(live.Payload)
	if liveState.Tree == nil || liveState.TreeHash != liveTreeHash(liveState.Tree) {
		t.Fatalf("live state not synchronized: %+v", liveState)
	}
	if _, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisTree); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tree lookup error=%v, want not found", err)
	}
}

func seedProjectionContractLive(t *testing.T, repository MeetingAIAnalysisRepository) (string, json.RawMessage) {
	t.Helper()
	sessionID := "session-projection-failure"
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{{
			ID: "fact-1", Kind: "fact", Title: "復旧を確認", Body: "復旧を確認した",
			Status: "open", EvidenceSequenceNos: []int64{1},
		}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
			{ID: "topic-recovery", Kind: "topic", ParentID: treeRootNodeID, Label: "復旧", Origin: topicOriginDynamic},
			{ID: "fact-1", Kind: "fact", ParentID: "topic-recovery", Label: "復旧を確認"},
		}},
		TreeVersion: 7,
	}
	rebuildTreeAuditEdges(state.Tree)
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{
		SessionID: sessionID, Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 7,
		Payload: payload, UpdatedAt: time.Date(2026, 7, 27, 1, 2, 3, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	return sessionID, payload
}

func TestPartialTranscriptDoesNotCreateSchedulerState(t *testing.T) {
	service := NewMeetingAnalysisService(nil, nil, nil, nil, MeetingAnalysisConfig{
		Enabled: true, LiveEnabled: true,
	})
	service.PublishTranscriptSegment(domain.TranscriptSegment{
		SessionID: "session-partial", SequenceNo: 1, Text: "途中", IsFinal: false,
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.sessions) != 0 {
		t.Fatalf("partial transcript created scheduler state: %+v", service.sessions)
	}
}

func TestPeriodicTickEvaluatesEachSessionOnce(t *testing.T) {
	service := NewMeetingAnalysisService(nil, nil, nil, nil, MeetingAnalysisConfig{
		Enabled: true, LiveEnabled: true, LiveInterval: time.Hour,
		LiveDebounce: time.Hour, LiveCooldown: time.Millisecond, LiveMaxWait: time.Hour,
		LiveMinChars: 1,
	})
	sessionID := "session-one-periodic-evaluation"
	now := service.now()
	service.mu.Lock()
	service.runCtx = context.Background()
	state := service.sessionStateLocked(sessionID)
	state.contextStatus = meetingContextStatusReady
	state.lastActivityAt = now
	appendPendingLiveSegmentLocked(state, domain.TranscriptSegment{
		SessionID: sessionID, CallID: "call", SequenceNo: 1,
		Text: "周期fallbackで一度だけ評価する", IsFinal: true,
	}, now)
	state.pendingChars = sumSegmentChars(state.pending)
	service.mu.Unlock()

	service.tick(context.Background())
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return state.analysisScheduled
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	if state.coalescedTriggerCount != 0 {
		t.Fatalf("coalescedTriggerCount=%d, want 0 (one evaluation per tick)", state.coalescedTriggerCount)
	}
	if service.schedulerTickCount != 1 {
		t.Fatalf("schedulerTickCount=%d, want 1", service.schedulerTickCount)
	}
}

type sessionFBAQualityMetrics struct {
	ModelItems                 int
	SynthesizedItems           int
	AnaphoraNodes              int
	SameEvidenceDuplicates     int
	TentativeItems             int
	UnclassifiedChildren       int
	CandidateFragmentation     int
	CrossAgendaContamination   int
	TopicOutliers              int
	DeterministicAuditFindings int
	NodeCount                  int
}

func TestSessionFBAFixtureQualityMetricsImproveWithoutLosingModelItems(t *testing.T) {
	payload, segments, mc := loadSession2dee3b1da5b72979Fixture(t)
	beforeState := previousLiveAnalysisState(payload)
	normalizeLegacyAgendaTopicIDs(&beforeState, mc, nil)
	beforeState.AgendaAnchors = reconcileAgendaAnchors(
		beforeState.AgendaAnchors, mc, beforeState.Tree, beforeState.Items, beforeState.TreeVersion, false,
	)
	before := sessionFBAFixtureQualityMetrics(beforeState, segments, mc)

	repaired, stats := applyDeterministicFinalTreeRepairs(payload, mc, 13, finalRepairInput{
		Segments: segments,
		Audit:    TreeAuditConfig{},
	})
	if stats.Error != "" || stats.IntegrityRejected {
		t.Fatalf("repair stats=%+v", stats)
	}
	after := sessionFBAFixtureQualityMetrics(previousLiveAnalysisState(repaired), segments, mc)
	t.Logf("session_fba equivalent fixture quality before=%+v after=%+v repair=%+v", before, after, stats)

	if after.ModelItems != before.ModelItems {
		t.Fatalf("model items before/after=%d/%d", before.ModelItems, after.ModelItems)
	}
	if after.AnaphoraNodes >= before.AnaphoraNodes ||
		after.SameEvidenceDuplicates >= before.SameEvidenceDuplicates {
		t.Fatalf("low-information quality did not improve: before=%+v after=%+v", before, after)
	}
	if after.CrossAgendaContamination > before.CrossAgendaContamination ||
		after.CandidateFragmentation > before.CandidateFragmentation ||
		after.TopicOutliers > before.TopicOutliers {
		t.Fatalf("structural quality regressed: before=%+v after=%+v", before, after)
	}
}

func sessionFBAFixtureQualityMetrics(state liveAnalysisPayload, segments []domain.TranscriptSegment, mc *meetingContext) sessionFBAQualityMetrics {
	metrics := sessionFBAQualityMetrics{}
	if state.Tree != nil {
		metrics.NodeCount = len(state.Tree.Nodes)
		for _, node := range state.Tree.Nodes {
			if node.ParentID == treeUnclassifiedTopicID {
				metrics.UnclassifiedChildren++
			}
		}
	}
	for _, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		if item.AssignmentReason == issueSynthesisAssignmentReason {
			metrics.SynthesizedItems++
		} else {
			metrics.ModelItems++
		}
		if liveItemTextNeedsReferent(item) {
			metrics.AnaphoraNodes++
		}
		if item.ClassificationStatus == classificationTentative {
			metrics.TentativeItems++
		}
		if finalItemIsLowInformation(item) && len(finalLowInformationMergeTargets(state.Items, item)) == 1 {
			metrics.SameEvidenceDuplicates++
		}
	}
	roles := classifyTreeAuditEvidence(state, segments)
	findings := deterministicTreeAuditPrecheck(state, mc, roles, TreeAuditConfig{}.normalized())
	metrics.DeterministicAuditFindings = len(findings)
	metrics.CandidateFragmentation = countTreeAuditPrechecks(
		findings, TreeAuditCandidateFragmentation, TreeAuditRiskTodoSubjectFragmentation,
		TreeAuditRelatedActionOutsideRiskTopic,
	)
	metrics.CrossAgendaContamination = countTreeAuditPrechecks(findings, TreeAuditCrossAgendaContamination)
	metrics.TopicOutliers = countTreeAuditPrechecks(
		findings, TreeAuditTopicOutlier, TreeAuditSubjectMismatch, TreeAuditGenericTopicLabel,
		TreeAuditTopicLabelNotDerivedFromChildren, TreeAuditSingleChildGenericTopic,
	)
	return metrics
}
