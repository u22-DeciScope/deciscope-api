package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

func TestDeterministicTreeAuditPrecheckReplaysTargetSessionAnomalies(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	findings := deterministicTreeAuditPrecheck(state, mc, roles, TreeAuditConfig{})

	assertAuditFindingForNode(t, findings, TreeAuditSubjectMismatch, "item-risk-rare-plants")
	assertAuditFindingForNode(t, findings, TreeAuditCrossAgendaContamination, "item-risk-rare-plants")
	assertAuditFindingForNode(t, findings, TreeAuditCandidateMixedSubjects, "item-todo-wind-standard")
	assertAuditFindingForNode(t, findings, TreeAuditCandidateShouldFoldIntoTopic, "item-todo-wind-standard")
	assertAuditFindingForNode(t, findings, TreeAuditFloatingTentativeCandidate, "item-todo-wind-standard")
	assertAuditFindingForNode(t, findings, TreeAuditCandidateFragmentation, "item-risk-rare-plants")

	for _, finding := range findings {
		if (finding.Type == TreeAuditSubjectMismatch || finding.Type == TreeAuditCrossAgendaContamination) && containsExactString(finding.NodeIDs, "item-decision-public-web") {
			t.Fatalf("correct public-information item was flagged: %+v", finding)
		}
	}
	if roles[28] != treeAuditEvidenceReference {
		t.Fatalf("sequence 28 role = %q, want reference", roles[28])
	}
	if roles[22] != treeAuditEvidencePrimary {
		t.Fatalf("sequence 22 role = %q, want primary", roles[22])
	}
	integrity := validateTreeIntegrity(state.Tree, state.Items, mc)
	if !integrity.Valid {
		t.Fatalf("fixture integrity = %+v", integrity)
	}
	byType := make(map[TreeAuditFindingType]int)
	for _, finding := range findings {
		byType[finding.Type]++
	}
	t.Logf("target replay: treeVersion=%d findings=%d byType=%v nodes=%d edges=%d coverage=%d integrityValid=%t", state.TreeVersion, len(findings), byType, len(state.Tree.Nodes), len(state.Tree.Edges), state.CoveredThroughSequenceNo, integrity.Valid)
}

func TestTreeAuditPatchValidatorAllowsOnlySafeSemanticImprovement(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-move-plant", Type: TreeAuditMoveItem,
		NodeID: "item-risk-rare-plants", FromParentID: "candidate-info-public",
		ToParentID: "candidate-plant-study", Confidence: 0.97,
		Reason: "湿地・希少植物subjectへ戻す", EvidenceSequenceNos: []int64{22},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-1", 13, true)
	if result.OperationsWouldApply != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	if node := treeNodeByID(dry.Tree, operation.NodeID); node == nil || node.ParentID != operation.ToParentID || node.LastParentChangeSource != "tree_auditor" {
		t.Fatalf("moved node = %+v", node)
	}
	if dry.ChangeSource != "tree_auditor" || dry.TreeChanges == nil || dry.TreeChanges.Source != "tree_auditor" {
		t.Fatalf("audit provenance missing: %+v %+v", dry.ChangeSource, dry.TreeChanges)
	}
}

func TestTreeIntegrityLayerRejectsInvalidKindAndHardDepth(t *testing.T) {
	payload, _, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "group-depth-1", Kind: "group", ParentID: "agenda-1", Label: "深さ1"},
		liveAnalysisTreeNode{ID: "group-depth-2", Kind: "group", ParentID: "group-depth-1", Label: "深さ2"},
		liveAnalysisTreeNode{ID: "group-depth-3", Kind: "group", ParentID: "group-depth-2", Label: "深さ3"},
		liveAnalysisTreeNode{ID: "group-depth-4", Kind: "group", ParentID: "group-depth-3", Label: "深さ4"},
		liveAnalysisTreeNode{ID: "invalid-depth-node", Kind: "alien", ParentID: "group-depth-4", Label: "invalid"},
	)
	rebuildTreeAuditEdges(state.Tree)
	integrity := validateTreeIntegrity(state.Tree, state.Items, mc)
	if integrity.Valid || !containsExactString(integrity.InvalidKindNodeIDs, "invalid-depth-node") || !containsExactString(integrity.HardDepthNodeIDs, "invalid-depth-node") {
		t.Fatalf("integrity = %+v", integrity)
	}
}

func TestTreeAuditPrecheckDetectsStrongWindAndMeetingDateUnderPlantTopic(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	for index := range state.Tree.Nodes {
		if state.Tree.Nodes[index].ID == "item-todo-wind-standard" {
			state.Tree.Nodes[index].ParentID = "candidate-plant-study"
		}
	}
	for index := range state.Items {
		if state.Items[index].ID == "item-todo-wind-standard" {
			state.Items[index].ClassificationStatus = classificationAssigned
			state.Items[index].CandidateTopicID = ""
		}
	}
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-question-meeting-date", Kind: "question", Title: "住民説明会の開催日程",
		Body: "自治会から候補日を受け取った後に確定", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: .6,
		EvidenceSequenceNos: []int64{20},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-question-meeting-date", Kind: "question", ParentID: "candidate-plant-study",
		Label: "住民説明会の開催日程", Status: "open",
	})
	rebuildTreeAuditEdges(state.Tree)
	segments = append(segments, domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 20, Text: "住民説明会の開催日は自治会から候補日を受け取った後に確定します。", IsFinal: true})
	roles := classifyTreeAuditEvidence(state, segments)
	findings := deterministicTreeAuditPrecheck(state, mc, roles, TreeAuditConfig{})
	assertAuditFindingForNode(t, findings, TreeAuditSubjectMismatch, "item-todo-wind-standard")
	assertAuditFindingForNode(t, findings, TreeAuditSubjectMismatch, "item-question-meeting-date")
}

func TestTreeAuditPatchValidatorRejectsWeakReferenceAndFixedAgendaMutations(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operations := []treeAuditOperation{
		{OperationID: "weak", Type: TreeAuditMoveItem, NodeID: "item-decision-public-web", FromParentID: "candidate-info-public", ToParentID: "candidate-plant-study", Confidence: 0.99, EvidenceSequenceNos: []int64{17}},
		{OperationID: "reference", Type: TreeAuditMoveItem, NodeID: "item-todo-wind-standard", FromParentID: treeUnclassifiedTopicID, ToParentID: "agenda-2", Confidence: 0.99, EvidenceSequenceNos: []int64{28}},
		{OperationID: "fixed", Type: TreeAuditMoveItem, NodeID: "agenda-2", FromParentID: treeRootNodeID, ToParentID: "candidate-plant-study", Confidence: 1, EvidenceSequenceNos: []int64{13}},
		{OperationID: "self", Type: TreeAuditMoveItem, NodeID: "item-risk-rare-plants", FromParentID: "candidate-info-public", ToParentID: "item-risk-rare-plants", Confidence: 1, EvidenceSequenceNos: []int64{22}},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, operations, segments, mc, roles, TreeAuditConfig{}, "audit-1", 13, true)
	if result.OperationsWouldApply != 0 || result.OperationsRejected != len(operations) {
		t.Fatalf("validator result = %+v", result)
	}
	reasons := make(map[string]string)
	for _, evaluation := range result.Evaluations {
		reasons[evaluation.OperationID] = evaluation.Reason
	}
	if reasons["weak"] != "parent_stickiness_margin" || reasons["reference"] != "reference_evidence_only" || reasons["fixed"] != "fixed_agenda_immutable" || reasons["self"] != "self_parent" {
		t.Fatalf("rejection reasons = %#v", reasons)
	}
}

func TestTreeAuditShadowPersistsWithoutChangingTree(t *testing.T) {
	service, analysisRepo, auditRepo, publisher, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	execution, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if execution.Result != "shadow" || execution.Applied || execution.Version != 12 {
		t.Fatalf("execution = %+v", execution)
	}
	if got := analysisRepo.version("session_26959b9519c5f880"); got != 12 {
		t.Fatalf("live version = %d, want 12", got)
	}
	if len(publisher.snapshot()) != 0 {
		t.Fatal("shadow mode must not publish a changed tree")
	}
	if run := auditRepo.latest(); run == nil || run.Result != "shadow" || len(run.Findings) == 0 || len(run.Operations) == 0 {
		t.Fatalf("saved audit run = %+v", run)
	} else if len(run.InputPayload) == 0 || !json.Valid(run.InputPayload) || run.RawResponse == "" || !run.ProviderCalled {
		t.Fatalf("audit replay payload is incomplete: %+v", run)
	}
	if completer.callCount() != 1 {
		t.Fatalf("completer calls = %d, want 1", completer.callCount())
	}
}

func TestShadowTreeAuditDoesNotBlockLiveExtraction(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	service.config.LiveMinChars = 1
	completer.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.scheduleTreeAudit(ctx, "session_26959b9519c5f880", "semantic_anomaly", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })

	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.pending = []domain.TranscriptSegment{{SessionID: "session_26959b9519c5f880", SequenceNo: 30, Text: "ライブ抽出は継続する", IsFinal: true}}
	state.pendingChars = 20
	service.mu.Unlock()
	service.tick(ctx)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 2 })
	service.mu.Lock()
	running, auditRunning := state.running, state.auditRunning
	service.mu.Unlock()
	if !running || !auditRunning {
		t.Fatalf("shadow concurrency liveRunning=%t auditRunning=%t", running, auditRunning)
	}
}

func TestShadowTreeAuditTimeoutDoesNotBlockLiveExtraction(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	service.config.LiveMinChars = 1
	service.config.TreeAudit.Timeout = 20 * time.Millisecond
	completer.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.scheduleTreeAudit(ctx, "session_26959b9519c5f880", "semantic_anomaly", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.pending = []domain.TranscriptSegment{{SessionID: "session_26959b9519c5f880", SequenceNo: 30, Text: "timeout中のライブ抽出", IsFinal: true}}
	state.pendingChars = 20
	service.mu.Unlock()
	service.tick(ctx)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 2 })
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return !state.auditRunning && state.running
	})
}

func TestTreeAuditPanicReleasesSingleFlight(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	completer.panicOnCall = true
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "test", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return completer.callCount() == 1 && !service.sessionStateLocked("session_26959b9519c5f880").auditRunning
	})
}

func TestShadowAuditRepositoryFailureDoesNotBlockLiveExtraction(t *testing.T) {
	service, _, auditRepo, _, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	service.config.LiveMinChars = 1
	auditRepo.tryStartErr = errors.New(`relation "meeting_tree_audit_runs" does not exist`)
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "test", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return !service.sessionStateLocked("session_26959b9519c5f880").auditRunning
	})
	completer.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.pending = []domain.TranscriptSegment{{SessionID: "session_26959b9519c5f880", SequenceNo: 30, Text: "repository障害後も分析", IsFinal: true}}
	state.pendingChars = 20
	service.mu.Unlock()
	service.tick(ctx)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
}

func TestTreeAuditRatePolicyPreservesLateMeetingAndHighSeverityCapacity(t *testing.T) {
	service, _, auditRepo, _, _, _ := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	service.config.TreeAudit.MinInterval = 5 * time.Minute
	service.config.TreeAudit.MaxRunsPerHour = 12
	service.config.TreeAudit.MaxRunsPerSession = 20
	base := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	lastAudit := time.Time{}
	for elapsed := time.Duration(0); elapsed < time.Hour; elapsed += 10 * time.Second {
		now := base.Add(elapsed)
		if !lastAudit.IsZero() && now.Sub(lastAudit) < service.config.TreeAudit.MinInterval {
			continue
		}
		reason, err := service.treeAuditSuppressionReason(context.Background(), "session_26959b9519c5f880", domain.MeetingTreeAuditTriggerNormal, false, now)
		if err != nil {
			t.Fatal(err)
		}
		if reason != "" {
			continue
		}
		auditRepo.runs = append(auditRepo.runs, domain.MeetingTreeAuditRun{
			ID: domain.NewID("rate"), SessionID: "session_26959b9519c5f880", Task: string(aiTaskTreeAudit),
			TriggerClass: domain.MeetingTreeAuditTriggerNormal, ProviderCalled: true, CreatedAt: now,
		})
		lastAudit = now
	}
	if len(auditRepo.runs) != 12 || lastAudit.Before(base.Add(55*time.Minute)) {
		t.Fatalf("60-minute schedule calls=%d last=%s", len(auditRepo.runs), lastAudit.Sub(base))
	}

	for len(auditRepo.runs) < 20 {
		auditRepo.runs = append(auditRepo.runs, domain.MeetingTreeAuditRun{
			ID: domain.NewID("normal"), SessionID: "session_26959b9519c5f880", Task: string(aiTaskTreeAudit),
			TriggerClass: domain.MeetingTreeAuditTriggerNormal, ProviderCalled: true, CreatedAt: base.Add(-2 * time.Hour),
		})
	}
	normalReason, err := service.treeAuditSuppressionReason(context.Background(), "session_26959b9519c5f880", domain.MeetingTreeAuditTriggerNormal, false, base.Add(2*time.Hour))
	if err != nil || normalReason != "normal_session_limit" {
		t.Fatalf("normal suppression=%q err=%v", normalReason, err)
	}
	highReason, err := service.treeAuditSuppressionReason(context.Background(), "session_26959b9519c5f880", domain.MeetingTreeAuditTriggerHigh, false, base.Add(2*time.Hour))
	if err != nil || highReason != "" {
		t.Fatalf("high severity was suppressed by normal cap: %q err=%v", highReason, err)
	}
	for index := 0; index < service.config.TreeAudit.HighSeverityMaxRunsPerHour; index++ {
		auditRepo.runs = append(auditRepo.runs, domain.MeetingTreeAuditRun{
			ID: domain.NewID("high"), SessionID: "session_26959b9519c5f880", Task: string(aiTaskTreeAudit),
			TriggerClass: domain.MeetingTreeAuditTriggerHigh, ProviderCalled: true, CreatedAt: base.Add(2*time.Hour + time.Duration(index)*time.Minute),
		})
	}
	highReason, err = service.treeAuditSuppressionReason(context.Background(), "session_26959b9519c5f880", domain.MeetingTreeAuditTriggerHigh, false, base.Add(2*time.Hour+10*time.Minute))
	if err != nil || highReason != "high_severity_hourly_limit" {
		t.Fatalf("high severity suppression=%q err=%v", highReason, err)
	}
}

func TestTreeAuditAutoApplyWhitelistRejectsEveryNonMoveOperation(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	types := []TreeAuditOperationType{
		TreeAuditRestorePreviousParent, TreeAuditMergeCandidates, TreeAuditFoldCandidateIntoTopic,
		TreeAuditPromoteCandidate, TreeAuditMarkCandidateTentative, TreeAuditDeactivateCandidate,
		TreeAuditMergeDynamicTopics, TreeAuditCreateGroup, TreeAuditMoveItemsToGroup,
		TreeAuditRenameGroup, TreeAuditRemoveEmptyGroup,
	}
	operations := make([]treeAuditOperation, 0, len(types))
	for index, operationType := range types {
		operations = append(operations, treeAuditOperation{OperationID: fmt.Sprintf("op-%d", index), Type: operationType, Confidence: 1})
	}
	_, validator := validateAndDryRunTreeAuditOperations(state, operations, segments, mc, roles, TreeAuditConfig{}, "audit-whitelist", 13, true)
	if validator.OperationsWouldApply != 0 || validator.OperationsRejected != len(types) {
		t.Fatalf("whitelist validator = %+v", validator)
	}
	for _, evaluation := range validator.Evaluations {
		if evaluation.Reason != "shadow_only_operation" {
			t.Fatalf("operation %s reason=%q", evaluation.Type, evaluation.Reason)
		}
	}
}

func TestEndingSessionDiscardsDelayedLiveAuditApply(t *testing.T) {
	service, analysisRepo, auditRepo, publisher, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditApplyHighConfidence, false)
	completer.block = make(chan struct{})
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "test", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.finalizing = true
	state.auditClosed = true
	service.mu.Unlock()
	close(completer.block)
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return !state.auditRunning
	})
	if analysisRepo.version("session_26959b9519c5f880") != 12 || len(publisher.snapshot()) != 0 {
		t.Fatal("live audit updated or published after ending")
	}
	if run := auditRepo.latest(); run == nil || run.Result != "session_ending_discarded" || run.Disposition != "stale" {
		t.Fatalf("ending audit run = %+v", run)
	}
}

func TestFinalTreeReviewIgnoresLateProviderResponseAfterTimeout(t *testing.T) {
	service, analysisRepo, _, publisher, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditApplyHighConfidence, false)
	service.config.TreeAudit.Timeout = 10 * time.Millisecond
	completer.block = make(chan struct{})
	completer.ignoreContext = true
	type result struct {
		execution treeAuditExecution
		err       error
	}
	done := make(chan result, 1)
	go func() {
		execution, err := service.runFinalTreeReview(context.Background(), "session_26959b9519c5f880", payload, 12)
		done <- result{execution: execution, err: err}
	}()
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	time.Sleep(20 * time.Millisecond)
	close(completer.block)
	got := <-done
	if !errors.Is(got.err, context.DeadlineExceeded) || got.execution.Applied {
		t.Fatalf("late final review result = %+v err=%v", got.execution, got.err)
	}
	if analysisRepo.version("session_26959b9519c5f880") != 12 || len(publisher.snapshot()) != 0 {
		t.Fatal("late final review response changed the tree")
	}
}

func TestFinalTreeReviewUsesSeparateFlightFromCanceledLiveAudit(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	completer.block = make(chan struct{})
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "test", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.auditClosed = true
	service.mu.Unlock()
	done := make(chan struct{})
	go func() {
		_, _ = service.runFinalTreeReview(context.Background(), "session_26959b9519c5f880", payload, 12)
		close(done)
	}()
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 2 })
	close(completer.block)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("final review did not complete")
	}
}

func TestTreeAuditApplyUsesVersionCASAndPublishes(t *testing.T) {
	service, analysisRepo, auditRepo, publisher, _, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditApplyHighConfidence, false)
	execution, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if !execution.Applied || execution.Version != 13 || analysisRepo.version("session_26959b9519c5f880") != 13 {
		t.Fatalf("execution = %+v liveVersion=%d", execution, analysisRepo.version("session_26959b9519c5f880"))
	}
	if len(publisher.snapshot()) != 1 {
		t.Fatalf("publish count = %d, want 1", len(publisher.snapshot()))
	}
	if run := auditRepo.latest(); run == nil || run.Result != "applied" || run.ResultingTreeVersion != 13 {
		t.Fatalf("saved audit run = %+v", run)
	}
}

func TestTreeAuditDurableClaimSuppressesDuplicateSnapshot(t *testing.T) {
	service, _, auditRepo, _, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	first, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil || first.Result != "shadow" {
		t.Fatalf("first audit=%+v err=%v", first, err)
	}
	second, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil || second.Result != "duplicate_snapshot" {
		t.Fatalf("second audit=%+v err=%v", second, err)
	}
	if completer.callCount() != 1 || len(auditRepo.runs) != 1 {
		t.Fatalf("provider calls=%d runs=%d", completer.callCount(), len(auditRepo.runs))
	}
}

func TestTreeAuditStaleCASRejectsOperations(t *testing.T) {
	service, analysisRepo, auditRepo, publisher, _, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditApplyHighConfidence, true)
	execution, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if execution.Result != "stale_tree_version" || execution.Applied || analysisRepo.version("session_26959b9519c5f880") != 12 {
		t.Fatalf("execution = %+v", execution)
	}
	if len(publisher.snapshot()) != 0 {
		t.Fatal("stale audit must not publish")
	}
	var validator treeAuditValidatorResult
	if run := auditRepo.latest(); run == nil || json.Unmarshal(run.ValidatorResult, &validator) != nil || validator.StaleOperationsRejected != 1 {
		t.Fatalf("stale validator result = %+v run=%+v", validator, run)
	}
}

func TestTreeAuditSchedulerCoalescesSingleFlight(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	completer.block = make(chan struct{})
	service.config.TreeAudit.MinInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.scheduleTreeAudit(ctx, "session_26959b9519c5f880", "first", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	service.scheduleTreeAudit(ctx, "session_26959b9519c5f880", "second", payload, 12)
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	pending, running := state.auditPending, state.auditRunning
	service.mu.Unlock()
	if !pending || !running {
		t.Fatalf("single-flight state pending=%t running=%t", pending, running)
	}
	close(completer.block)
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return !service.sessionStateLocked("session_26959b9519c5f880").auditRunning
	})
	if completer.callCount() != 1 {
		t.Fatalf("coalesced audit calls = %d, want 1", completer.callCount())
	}
}

func TestFinalTreeReviewTimeoutKeepsLastKnownGoodTree(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	completer.block = make(chan struct{})
	service.config.TreeAudit.Timeout = 10 * time.Millisecond
	execution, err := service.runFinalTreeReview(context.Background(), "session_26959b9519c5f880", payload, 12)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runFinalTreeReview() error = %v, want deadline exceeded", err)
	}
	if execution.Version != 12 || string(execution.Payload) != string(payload) {
		t.Fatalf("fallback execution changed last-known-good tree: %+v", execution)
	}
}

func TestTaskModelRoutingIncludesTreeAuditAndFinalTreeReview(t *testing.T) {
	config := MeetingAnalysisConfig{Model: "shared", TaskModels: AITaskModels{LiveExtraction: "nano", TreeAudit: "mini-audit", FinalTreeReview: "mini-final"}}
	if got := config.modelNameFor(aiTaskLiveExtraction); got != "nano" {
		t.Fatalf("live deployment = %q", got)
	}
	if got := config.modelNameFor(aiTaskTreeAudit); got != "mini-audit" {
		t.Fatalf("tree audit deployment = %q", got)
	}
	if got := config.modelNameFor(aiTaskFinalTreeReview); got != "mini-final" {
		t.Fatalf("final tree review deployment = %q", got)
	}
	if got := config.modelNameFor(aiTaskFinalSummary); got != "shared" {
		t.Fatalf("final summary fallback deployment = %q", got)
	}
}

func TestTreeAuditAndFinalReviewSendConfiguredDeployments(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, domain.MeetingTreeAuditShadow, false)
	service.config.TaskModels.FinalTreeReview = "final-review-mini"
	if _, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false); err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if _, err := service.runFinalTreeReview(context.Background(), "session_26959b9519c5f880", payload, 12); err != nil {
		t.Fatalf("runFinalTreeReview() error = %v", err)
	}
	requests := completer.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(requests))
	}
	if requests[0].Deployment != "tree-audit-mini" || requests[1].Deployment != "final-review-mini" {
		t.Fatalf("provider deployments = %q, %q", requests[0].Deployment, requests[1].Deployment)
	}
}

func targetTreeAuditFixture(t *testing.T) (json.RawMessage, []domain.TranscriptSegment, *meetingContext) {
	t.Helper()
	mc := &meetingContext{Title: "沿岸部風力発電計画", Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥の調査計画", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定の実施方法", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料の作成", Order: 3, Role: agendaRolePrimary},
	}}
	state := liveAnalysisPayload{
		Summary: "対象セッションfixture", TreeVersion: 12, CoveredThroughSequenceNo: 29,
		Items: []liveAnalysisItem{
			{ID: "item-risk-rare-plants", Kind: "risk", Title: "建設予定地近傍の湿地・希少植物の可能性の調査", Body: "湿地評価と希少植物の調査", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{22}},
			{ID: "item-todo-plant-survey", Kind: "todo", Title: "植物の種類確認の予備調査", Body: "専門家による希少植物の予備調査", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{23, 24}},
			{ID: "item-todo-wind-standard", Kind: "todo", Title: "強風日での風速基準の決定", Body: "騒音測定時の風速基準", Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-plant-video", AssignmentConfidence: 1, EvidenceSequenceNos: []int64{13, 28}},
			{ID: "item-decision-public-web", Kind: "decision", Title: "調査結果を図付きでウェブ公開", Body: "住民説明資料の公開方針", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: .95, EvidenceSequenceNos: []int64{17}},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "沿岸部風力発電計画", Origin: topicOriginSystem},
			{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "渡り鳥の調査計画", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary},
			{ID: "agenda-2", Kind: "topic", ParentID: treeRootNodeID, Label: "騒音測定の実施方法", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary},
			{ID: "agenda-3", Kind: "topic", ParentID: treeRootNodeID, Label: "住民説明資料の作成", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary},
			{ID: "candidate-plant-study", Kind: "topic", ParentID: treeRootNodeID, Label: "植物調査", Description: "湿地・希少植物の生態系調査", Origin: topicOriginDynamic},
			{ID: "candidate-info-public", Kind: "topic", ParentID: treeRootNodeID, Label: "情報公開・説明資料", Description: "公開資料の方針", Origin: topicOriginDynamic},
			{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginSystem},
			{ID: "item-risk-rare-plants", Kind: "risk", ParentID: "candidate-info-public", Label: "建設予定地近傍の湿地・希少植物の可能性の調査", Status: "open"},
			{ID: "item-todo-plant-survey", Kind: "todo", ParentID: "candidate-plant-study", Label: "植物の種類確認の予備調査", Status: "open"},
			{ID: "item-todo-wind-standard", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "強風日での風速基準の決定", Status: "open"},
			{ID: "item-decision-public-web", Kind: "decision", ParentID: "candidate-info-public", Label: "調査結果を図付きでウェブ公開", Status: "open"},
		}},
		EmergingTopics: []emergingTopicCandidate{{ID: "candidate-plant-video", Label: "植物関連資料・動画", Description: "希少植物調査の新規話題", EvidenceItemIDs: []string{"item-todo-wind-standard"}, FirstRound: 12, LastRound: 12, RoundCount: 1}},
	}
	rebuildTreeAuditEdges(state.Tree)
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[int64]string{
		13: "ただし、強風日の測定事項については、どの風速を基準にするか決まっていません。",
		17: "住民が後から確認できるよう、調査結果の概要を団体のウェブサイトで公開します。",
		22: "アジェンダ外ですが、小規模な湿地が見つかり希少な植物が生息している可能性があります。",
		23: "既存の鳥類調査や騒音調査に含めず、新しい植物調査課題として扱います。",
		24: "植物の種類を確認するため、専門家による予備調査を検討します。",
		25: "以上をまとめます。",
		28: "未解決の課題は強風日の風速基準と住民説明会の開催日です。",
		29: "希少植物については、アジェンダ外から生まれた新しい動画として次回も検討します。",
	}
	segments := make([]domain.TranscriptSegment, 0, len(texts))
	for _, sequenceNo := range []int64{13, 17, 22, 23, 24, 25, 28, 29} {
		segments = append(segments, domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", CallID: "call-1", SequenceNo: sequenceNo, SpeakerName: "山下", Text: texts[sequenceNo], IsFinal: true})
	}
	return encoded, segments, mc
}

func assertAuditFindingForNode(t *testing.T, findings []treeAuditPrecheckFinding, findingType TreeAuditFindingType, nodeID string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Type == findingType && containsExactString(finding.NodeIDs, nodeID) {
			return
		}
	}
	t.Fatalf("finding type=%s node=%s not found in %+v", findingType, nodeID, findings)
}

type internalAuditAnalysisRepository struct {
	mu    sync.Mutex
	store map[string]domain.MeetingAIAnalysis
}

func (r *internalAuditAnalysisRepository) UpsertMeetingAIAnalysis(_ context.Context, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[analysis.SessionID] = analysis
	copy := analysis
	return &copy, nil
}

func (r *internalAuditAnalysisRepository) CompareAndSwapMeetingAIAnalysis(_ context.Context, expectedVersion int64, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.store[analysis.SessionID]
	if (exists && current.Version != expectedVersion) || (!exists && expectedVersion != 0) {
		return nil, false, nil
	}
	r.store[analysis.SessionID] = analysis
	copy := analysis
	return &copy, true, nil
}

func (r *internalAuditAnalysisRepository) GetMeetingAIAnalysis(_ context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	analysis, ok := r.store[sessionID]
	if !ok || analysis.Type != analysisType {
		return nil, domain.ErrNotFound
	}
	copy := analysis
	return &copy, nil
}

func (r *internalAuditAnalysisRepository) version(sessionID string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.store[sessionID].Version
}

type internalAuditRepository struct {
	mu          sync.Mutex
	runs        []domain.MeetingTreeAuditRun
	analysis    *internalAuditAnalysisRepository
	staleCAS    bool
	tryStartErr error
	saveErr     error
	countErr    error
}

func (r *internalAuditRepository) CheckMeetingTreeAuditRepository(context.Context) error {
	return nil
}

func (r *internalAuditRepository) TryStartMeetingTreeAuditRun(_ context.Context, run domain.MeetingTreeAuditRun) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tryStartErr != nil {
		return false, r.tryStartErr
	}
	for _, existing := range r.runs {
		if existing.Status == domain.MeetingTreeAuditRunning && existing.SessionID == run.SessionID && existing.Task == run.Task && existing.BasedOnTreeVersion == run.BasedOnTreeVersion && existing.SnapshotHash == run.SnapshotHash && existing.PromptVersion == run.PromptVersion && existing.Deployment == run.Deployment {
			return false, nil
		}
	}
	r.runs = append(r.runs, run)
	return true, nil
}

func (r *internalAuditRepository) SaveMeetingTreeAuditRun(_ context.Context, run domain.MeetingTreeAuditRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	for index := range r.runs {
		if r.runs[index].ID == run.ID {
			r.runs[index] = run
			return nil
		}
	}
	r.runs = append(r.runs, run)
	return nil
}

func (r *internalAuditRepository) GetLatestMeetingTreeAuditRun(context.Context, string) (*domain.MeetingTreeAuditRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.runs) == 0 {
		return nil, domain.ErrNotFound
	}
	copy := r.runs[len(r.runs)-1]
	return &copy, nil
}

func (r *internalAuditRepository) CountMeetingTreeAuditProviderCalls(_ context.Context, _ string, triggerClass domain.MeetingTreeAuditTriggerClass, since time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.countErr != nil {
		return 0, r.countErr
	}
	count := 0
	for _, run := range r.runs {
		if run.Task == string(aiTaskFinalTreeReview) || !run.ProviderCalled {
			continue
		}
		if triggerClass != "" && run.TriggerClass != triggerClass {
			continue
		}
		if !since.IsZero() && run.CreatedAt.Before(since) {
			continue
		}
		count++
	}
	return count, nil
}

func (r *internalAuditRepository) ApplyMeetingTreeAudit(ctx context.Context, run domain.MeetingTreeAuditRun, expectedVersion int64, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, bool, error) {
	if r.staleCAS || r.analysis.version(analysis.SessionID) != expectedVersion {
		return nil, false, nil
	}
	saved, err := r.analysis.UpsertMeetingAIAnalysis(ctx, analysis)
	if err != nil {
		return nil, false, err
	}
	if err := r.SaveMeetingTreeAuditRun(ctx, run); err != nil {
		return nil, false, err
	}
	return saved, true, nil
}

func (r *internalAuditRepository) latest() *domain.MeetingTreeAuditRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.runs) == 0 {
		return nil
	}
	copy := r.runs[len(r.runs)-1]
	return &copy
}

type internalAuditTranscriptRepository struct{ segments []domain.TranscriptSegment }

func (r internalAuditTranscriptRepository) SaveTranscriptSegment(context.Context, domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	return domain.TranscriptSegmentStoreResult{}, errors.New("not implemented")
}
func (r internalAuditTranscriptRepository) ListTranscriptSegments(context.Context, string, string, int) ([]domain.TranscriptSegment, error) {
	return append([]domain.TranscriptSegment(nil), r.segments...), nil
}

type internalAuditPublisher struct {
	mu       sync.Mutex
	analyses []domain.MeetingAIAnalysis
}

func (p *internalAuditPublisher) PublishMeetingAIAnalysis(analysis domain.MeetingAIAnalysis) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.analyses = append(p.analyses, analysis)
}
func (p *internalAuditPublisher) snapshot() []domain.MeetingAIAnalysis {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.MeetingAIAnalysis(nil), p.analyses...)
}

type internalAuditCompleter struct {
	mu            sync.Mutex
	content       string
	calls         int
	requests      []AIChatRequest
	block         chan struct{}
	ignoreContext bool
	panicOnCall   bool
}

func (c *internalAuditCompleter) Complete(ctx context.Context, request AIChatRequest) (AIChatResult, error) {
	c.mu.Lock()
	c.calls++
	c.requests = append(c.requests, request)
	block := c.block
	ignoreContext := c.ignoreContext
	panicOnCall := c.panicOnCall
	c.mu.Unlock()
	if panicOnCall {
		panic("tree audit provider panic")
	}
	if block != nil {
		if ignoreContext {
			<-block
		} else {
			select {
			case <-block:
			case <-ctx.Done():
				return AIChatResult{}, ctx.Err()
			}
		}
	}
	return AIChatResult{Content: c.content, Model: "gpt-5-mini", PromptTokens: 100, CompletionTokens: 50}, nil
}
func (c *internalAuditCompleter) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}
func (c *internalAuditCompleter) requestsSnapshot() []AIChatRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]AIChatRequest(nil), c.requests...)
}

func newTreeAuditRunnerFixture(t *testing.T, mode domain.MeetingTreeAuditMode, staleCAS bool) (*MeetingAnalysisService, *internalAuditAnalysisRepository, *internalAuditRepository, *internalAuditPublisher, *internalAuditCompleter, json.RawMessage) {
	t.Helper()
	payload, segments, mc := targetTreeAuditFixture(t)
	analysisRepo := &internalAuditAnalysisRepository{store: map[string]domain.MeetingAIAnalysis{
		"session_26959b9519c5f880": {SessionID: "session_26959b9519c5f880", Type: domain.MeetingAIAnalysisLive, Status: domain.MeetingAIAnalysisCompleted, Version: 12, Payload: payload, SegmentCount: len(segments)},
	}}
	auditRepo := &internalAuditRepository{analysis: analysisRepo, staleCAS: staleCAS}
	publisher := &internalAuditPublisher{}
	completer := &internalAuditCompleter{content: validAuditMoveResponse()}
	service := NewMeetingAnalysisService(analysisRepo, internalAuditTranscriptRepository{segments: segments}, nil, completer, MeetingAnalysisConfig{
		Enabled: true, LiveEnabled: true, Model: "shared", TaskModels: AITaskModels{TreeAudit: "tree-audit-mini", FinalTreeReview: "tree-audit-mini"},
		TreeAudit: TreeAuditConfig{Enabled: true, Mode: mode, MinInterval: time.Millisecond, Timeout: time.Second},
	}, publisher)
	service.SetMeetingTreeAuditRepository(auditRepo)
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.context = mc
	state.contextFallback = mc
	state.contextStatus = meetingContextStatusReady
	state.contextVersion = 1
	state.lastPayload = payload
	state.lastVersion = 12
	state.lastActivityAt = service.now()
	service.mu.Unlock()
	return service, analysisRepo, auditRepo, publisher, completer, payload
}

func validAuditMoveResponse() string {
	return `{
  "basedOnTreeVersion":12,
  "summary":"湿地・希少植物itemの親が不整合",
  "findings":[{
    "findingId":"finding-1","type":"subject_mismatch","severity":"high",
    "nodeIds":["item-risk-rare-plants"],"currentParentIds":["candidate-info-public"],
    "relatedNodeIds":["candidate-plant-study"],"evidenceSequenceNos":[22],
    "reason":"植物調査topicが意味的に一致","confidence":0.97
  }],
  "operations":[{
    "operationId":"operation-1","type":"move_item","nodeId":"item-risk-rare-plants","nodeIds":[],
    "candidateId":"","fromCandidateId":"","toCandidateId":"","fromParentId":"candidate-info-public",
    "toParentId":"candidate-plant-study","groupId":"","label":"","reason":"植物調査topicへ戻す",
    "confidence":0.97,"evidenceSequenceNos":[22],"dependsOnOperationIds":[]
  }]
}`
}

func waitForInternalAudit(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !condition() {
		t.Fatalf("condition not met within %s", timeout)
	}
}
