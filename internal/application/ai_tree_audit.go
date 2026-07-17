package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

type treeAuditExecution struct {
	Payload        json.RawMessage
	Version        int64
	RunID          string
	SnapshotHash   string
	Result         string
	Applied        bool
	AuditedVersion int64
	TriggerClass   domain.MeetingTreeAuditTriggerClass
	// ProviderCalled is true once the run reached the AI provider. Runs that
	// fail before this point (repository errors, suppression) must not consume
	// the provider-call min interval so they can retry after recovery.
	ProviderCalled bool
}

// treeAuditRepositoryFailureBackoff is the short delay applied after an audit
// attempt that failed without calling the provider. It prevents a tight retry
// loop against a failing repository without consuming provider rate limits.
const treeAuditRepositoryFailureBackoff = 10 * time.Second

func (s *MeetingAnalysisService) considerTreeAudit(ctx context.Context, sessionID string, previousPayload, payload json.RawMessage, version int64) {
	if s == nil || !s.config.TreeAudit.active() || s.auditRepo == nil || version <= 0 || len(payload) == 0 {
		return
	}
	reasons := treeAuditConditionalTriggerReasons(previousPayload, payload, s.sessionMeetingContext(ctx, sessionID), s.config.TreeAudit)
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	versionDue := version-state.lastAuditVersion >= s.config.TreeAudit.IntervalVersions
	s.mu.Unlock()
	if len(reasons) == 0 && !versionDue {
		log.Printf("Tree audit skipped. sessionId=%s treeVersion=%d auditSkipped=true auditSkipReason=interval_not_reached", sessionID, version)
		return
	}
	if versionDue {
		reasons = append(reasons, "interval_versions")
	}
	s.scheduleTreeAudit(ctx, sessionID, strings.Join(uniqueNonEmptyIDs(reasons), ","), payload, version)
}

func treeAuditConditionalTriggerReasons(previousPayload, payload json.RawMessage, mc *meetingContext, cfg TreeAuditConfig) []string {
	previous := previousLiveAnalysisState(previousPayload)
	current := previousLiveAnalysisState(payload)
	var reasons []string
	if current.TreeChanges != nil {
		if len(current.TreeChanges.NewNodeIDs) > 0 {
			reasons = append(reasons, "new_tree_node")
		}
		if len(current.TreeChanges.ReparentedNodeIDs) > 0 {
			reasons = append(reasons, "item_reparented")
		}
		if len(current.TreeChanges.PromotedNodeIDs) > 0 {
			reasons = append(reasons, "candidate_promoted")
		}
	}
	if len(current.EmergingTopics) > len(previous.EmergingTopics) {
		reasons = append(reasons, "dynamic_candidate_created")
	}
	precheck := deterministicTreeAuditPrecheck(current, mc, nil, cfg)
	if current.Tree != nil && !validateTreeIntegrity(current.Tree, current.Items, mc).Valid {
		reasons = append(reasons, "integrity_invalid")
	}
	for _, finding := range precheck {
		switch finding.Type {
		case TreeAuditCandidateFragmentation:
			reasons = append(reasons, "similar_candidates")
		case TreeAuditFloatingTentativeCandidate:
			reasons = append(reasons, "floating_tentative_candidate")
		case TreeAuditStaleTentative:
			reasons = append(reasons, "stale_tentative")
		case TreeAuditParentLowConfidence:
			reasons = append(reasons, "parent_low_confidence")
		case TreeAuditSubjectMismatch, TreeAuditCrossAgendaContamination,
			TreeAuditCandidateMixedSubjects, TreeAuditTopicOutlier:
			reasons = append(reasons, "semantic_anomaly")
		}
	}
	if previous.Tree != nil && current.Tree != nil {
		oldHealth, newHealth := computeTreeHealth(previous.Tree), computeTreeHealth(current.Tree)
		if newHealth.MaxChildren-oldHealth.MaxChildren >= 4 {
			reasons = append(reasons, "topic_child_surge")
		}
	}
	sort.Strings(reasons)
	return uniqueNonEmptyIDs(reasons)
}

func (s *MeetingAnalysisService) scheduleTreeAudit(ctx context.Context, sessionID, triggerReason string, payload json.RawMessage, version int64) {
	if s == nil || !s.config.TreeAudit.active() || s.auditRepo == nil || len(payload) == 0 || version <= 0 {
		return
	}
	now := s.now()
	triggerClass := treeAuditTriggerClass(triggerReason, false)
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	if state.auditClosed {
		s.mu.Unlock()
		log.Printf("Tree audit skipped. sessionId=%s treeVersion=%d auditSkipped=true auditSkipReason=session_ending", sessionID, version)
		return
	}
	if state.finalizing {
		state.auditPending = true
		state.auditPendingReason = coalesceTreeAuditReason(state.auditPendingReason, triggerReason)
		s.mu.Unlock()
		log.Printf("Tree audit coalesced. sessionId=%s treeVersion=%d auditCoalesced=true auditPending=true reason=finalizing", sessionID, version)
		return
	}
	if state.auditRunning {
		state.auditPending = true
		state.auditPendingReason = coalesceTreeAuditReason(state.auditPendingReason, triggerReason)
		s.mu.Unlock()
		log.Printf("Tree audit coalesced. sessionId=%s treeVersion=%d auditCoalesced=true auditPending=true reason=single_flight", sessionID, version)
		return
	}
	if state.lastAuditVersion >= version {
		s.mu.Unlock()
		log.Printf("Tree audit skipped. sessionId=%s treeVersion=%d auditSkipped=true auditSkipReason=tree_version_already_audited", sessionID, version)
		return
	}
	if now.Before(state.auditRepoBackoffUntil) {
		state.auditPending = true
		state.auditPendingReason = coalesceTreeAuditReason(state.auditPendingReason, triggerReason)
		s.mu.Unlock()
		log.Printf("Tree audit backing off after repository failure. sessionId=%s treeVersion=%d auditRepoBackoff=true auditPending=true", sessionID, version)
		return
	}
	lastRateLimitedAt := state.lastAuditAt
	minimumInterval := s.config.TreeAudit.MinInterval
	if triggerClass == domain.MeetingTreeAuditTriggerHigh {
		lastRateLimitedAt = state.lastHighSeverityAuditAt
		minimumInterval = s.config.TreeAudit.HighSeverityMinInterval
	}
	if !lastRateLimitedAt.IsZero() && now.Sub(lastRateLimitedAt) < minimumInterval {
		state.auditPending = true
		state.auditPendingReason = coalesceTreeAuditReason(state.auditPendingReason, triggerReason)
		s.mu.Unlock()
		log.Printf("Tree audit rate limited. sessionId=%s treeVersion=%d auditRateLimited=true auditPending=true", sessionID, version)
		return
	}
	state.auditRunning = true
	state.auditRunningDone = make(chan struct{})
	auditCtx, auditCancel := context.WithCancel(ctx)
	state.auditCancel = auditCancel
	state.auditPending = false
	state.auditPendingReason = ""
	s.mu.Unlock()
	log.Printf("Tree audit scheduled. sessionId=%s treeVersion=%d auditScheduled=true triggerReason=%s", sessionID, version, triggerReason)
	go s.executeScheduledTreeAudit(auditCtx, sessionID, triggerReason, append(json.RawMessage(nil), payload...), version)
}

func (s *MeetingAnalysisService) executeScheduledTreeAudit(ctx context.Context, sessionID, triggerReason string, payload json.RawMessage, version int64) {
	execution := treeAuditExecution{Payload: payload, Version: version, AuditedVersion: version, TriggerClass: treeAuditTriggerClass(triggerReason, false)}
	defer func() {
		if recovered := recover(); recovered != nil {
			execution.Result = "panic"
			log.Printf("Tree audit panic recovered. sessionId=%s treeVersion=%d panic=%v", sessionID, version, recovered)
		}
		s.finishTreeAuditFlight(ctx, sessionID, execution, version)
	}()
	var err error
	execution, err = s.runTreeAudit(ctx, sessionID, triggerReason, aiTaskTreeAudit, payload, version, false)
	if err != nil {
		if execution.Result == "" {
			execution.Result = "failed"
		}
		log.Printf("Tree audit execution failed. sessionId=%s treeVersion=%d triggerReason=%s error=%v", sessionID, version, triggerReason, err)
	}
}

func (s *MeetingAnalysisService) finishTreeAuditFlight(ctx context.Context, sessionID string, execution treeAuditExecution, requestedVersion int64) {
	now := s.now()
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	state.auditRunning = false
	if state.auditCancel != nil {
		state.auditCancel()
		state.auditCancel = nil
	}
	if state.auditRunningDone != nil {
		close(state.auditRunningDone)
		state.auditRunningDone = nil
	}
	auditedVersion := execution.AuditedVersion
	if auditedVersion == 0 {
		auditedVersion = requestedVersion
	}
	if execution.Applied && execution.Version > auditedVersion {
		auditedVersion = execution.Version
	}
	auditFailed := execution.Result == "failed" || execution.Result == "timeout" || execution.Result == "canceled" || execution.Result == "invalid_schema" || execution.Result == "panic"
	// Failures that never reached the provider (repository errors, panics
	// before the call) must not consume the provider min interval; a short
	// backoff prevents a tight retry loop while the repository is down.
	if auditFailed && !execution.ProviderCalled {
		state.auditRepoBackoffUntil = now.Add(treeAuditRepositoryFailureBackoff)
	} else {
		state.lastAuditAt = now
		state.auditRepoBackoffUntil = time.Time{}
	}
	if !auditFailed && auditedVersion > state.lastAuditVersion {
		state.lastAuditVersion = auditedVersion
	}
	if auditFailed && !state.auditClosed {
		state.auditPending = true
		state.auditPendingReason = coalesceTreeAuditReason(state.auditPendingReason, "retry_failed_audit")
	}
	if execution.TriggerClass == domain.MeetingTreeAuditTriggerHigh && !auditFailed {
		state.lastHighSeverityAuditAt = now
	}
	state.lastAuditHash = execution.SnapshotHash
	if execution.Applied && state.lastVersion == requestedVersion {
		state.lastPayload = append(json.RawMessage(nil), execution.Payload...)
		state.lastVersion = execution.Version
	}
	if state.lastVersion > auditedVersion && !state.auditClosed {
		state.auditPending = true
		state.auditPendingReason = coalesceTreeAuditReason(state.auditPendingReason, "newer_tree_version")
	}
	pending := state.auditPending && !state.finalizing && !state.auditClosed
	pendingReason := state.auditPendingReason
	pendingPayload := append(json.RawMessage(nil), state.lastPayload...)
	pendingVersion := state.lastVersion
	pendingSince := state.lastAuditAt
	pendingInterval := s.config.TreeAudit.MinInterval
	if treeAuditTriggerClass(pendingReason, false) == domain.MeetingTreeAuditTriggerHigh {
		pendingSince = state.lastHighSeverityAuditAt
		pendingInterval = s.config.TreeAudit.HighSeverityMinInterval
	}
	if pending && !now.Before(state.auditRepoBackoffUntil) &&
		(pendingSince.IsZero() || now.Sub(pendingSince) >= pendingInterval) {
		state.auditPending = false
		state.auditPendingReason = ""
	} else {
		pending = false
	}
	s.mu.Unlock()
	if pending {
		s.scheduleTreeAudit(ctx, sessionID, pendingReason, pendingPayload, pendingVersion)
	}
}

func treeAuditTriggerClass(reason string, finalReview bool) domain.MeetingTreeAuditTriggerClass {
	if finalReview {
		return domain.MeetingTreeAuditTriggerFinal
	}
	for _, part := range strings.Split(reason, ",") {
		switch strings.TrimSpace(part) {
		case "semantic_anomaly", "cross_agenda_contamination", "integrity_invalid":
			return domain.MeetingTreeAuditTriggerHigh
		}
	}
	return domain.MeetingTreeAuditTriggerNormal
}

func coalesceTreeAuditReason(left, right string) string {
	parts := strings.FieldsFunc(left+","+right, func(r rune) bool { return r == ',' })
	sort.Strings(parts)
	return strings.Join(uniqueNonEmptyIDs(parts), ",")
}

func (s *MeetingAnalysisService) runTreeAudit(ctx context.Context, sessionID, triggerReason string, task aiTask, payload json.RawMessage, version int64, finalReview bool) (treeAuditExecution, error) {
	triggerClass := treeAuditTriggerClass(triggerReason, finalReview)
	execution := treeAuditExecution{Payload: payload, Version: version, AuditedVersion: version, TriggerClass: triggerClass}
	if s.auditRepo == nil {
		return execution, fmt.Errorf("tree audit repository is not configured")
	}
	state := previousLiveAnalysisState(payload)
	if state.Tree == nil || state.TreeVersion != version {
		return execution, fmt.Errorf("tree audit payload version=%d, expected %d", state.TreeVersion, version)
	}
	transcriptLimit := 2000
	if finalReview {
		transcriptLimit = meetingAnalysisFinalTranscriptLimit
	}
	segments, err := s.transcriptRepo.ListTranscriptSegments(ctx, "", sessionID, transcriptLimit)
	if err != nil {
		return execution, fmt.Errorf("load tree audit transcript: %w", err)
	}
	mc := s.sessionMeetingContext(ctx, sessionID)
	snapshot, err := buildTreeAuditSnapshot(sessionID, payload, segments, mc, s.config.TreeAudit)
	if err != nil {
		return execution, err
	}
	execution.SnapshotHash = snapshot.Hash
	latest, latestErr := s.auditRepo.GetLatestMeetingTreeAuditRun(ctx, sessionID)
	if latestErr != nil && !errors.Is(latestErr, domain.ErrNotFound) {
		return execution, latestErr
	}
	latestTerminal := latest != nil && (latest.Status == domain.MeetingTreeAuditCompleted || latest.Status == domain.MeetingTreeAuditSkipped)
	manualReplay := strings.Contains(triggerReason, "manual_replay")
	if !manualReplay && latestTerminal && latest.Task == string(task) && latest.PromptVersion == task.promptVersion() && latest.Deployment == s.config.modelNameFor(task) && latest.SnapshotHash == snapshot.Hash && (latest.BasedOnTreeVersion == version || latest.ResultingTreeVersion == version) {
		log.Printf("Tree audit skipped. sessionId=%s treeVersion=%d snapshotHash=%s auditSkipped=true auditSkipReason=duplicate_snapshot", sessionID, version, shortAuditHash(snapshot.Hash))
		execution.Result = "duplicate_snapshot"
		return execution, nil
	}
	runID := domain.NewID("tree-audit")
	execution.RunID = runID
	now := s.now().UTC()
	inputSummary := map[string]any{
		"snapshotHash": snapshot.Hash, "nodeCount": len(snapshot.Snapshot.Nodes),
		"candidateCount":       len(snapshot.Snapshot.Candidates),
		"precheckFindingCount": len(snapshot.Snapshot.PrecheckFindings),
		"evidenceSegmentCount": len(snapshot.Snapshot.EvidenceSegments),
		"recentSegmentCount":   len(snapshot.Snapshot.RecentTranscript),
		"compressed":           snapshot.Snapshot.Compressed,
		"triggerClass":         triggerClass,
	}
	meetingElapsedSeconds := s.treeAuditMeetingElapsedSeconds(ctx, sessionID, now)
	inputSummary["meetingElapsedSeconds"] = meetingElapsedSeconds
	run := domain.MeetingTreeAuditRun{
		ID: runID, SessionID: sessionID, BasedOnTreeVersion: version,
		Mode: s.config.TreeAudit.Mode, TriggerReason: truncateRunes(triggerReason, 300),
		TriggerClass: triggerClass, Task: string(task), Deployment: s.config.modelNameFor(task),
		PromptVersion: task.promptVersion(), SnapshotHash: snapshot.Hash,
		Status: domain.MeetingTreeAuditRunning, Result: "running", Disposition: "none",
		MeetingElapsedSeconds: meetingElapsedSeconds,
		InputSummary:          boundedAuditJSON(inputSummary, s.config.TreeAudit.MaxPersistedJSONBytes),
		InputPayload:          boundedAuditJSON(snapshot.InputJSON, s.config.TreeAudit.MaxPersistedJSONBytes),
		CreatedAt:             now,
	}
	claimed, err := s.auditRepo.TryStartMeetingTreeAuditRun(ctx, run)
	if err != nil {
		return execution, err
	}
	if !claimed {
		log.Printf("Tree audit skipped. sessionId=%s treeVersion=%d snapshotHash=%s auditSkipped=true auditSkipReason=duplicate_snapshot", sessionID, version, shortAuditHash(snapshot.Hash))
		execution.Result = "duplicate_snapshot"
		return execution, nil
	}
	suppressionReason, err := s.treeAuditSuppressionReason(ctx, sessionID, triggerClass, finalReview, now)
	if err != nil {
		return execution, s.failTreeAuditRun(ctx, &run, "repository_error", err)
	}
	if suppressionReason != "" {
		completed := s.now().UTC()
		run.Status = domain.MeetingTreeAuditSkipped
		run.Result = "rate_limited"
		run.Disposition = "suppressed"
		run.SuppressionReason = suppressionReason
		run.CompletedAt = &completed
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		log.Printf("Tree audit rate limited. sessionId=%s treeVersion=%d triggerClass=%s suppressionReason=%s meetingElapsedSeconds=%d", sessionID, version, triggerClass, suppressionReason, meetingElapsedSeconds)
		return execution, nil
	}
	integrity := validateTreeIntegrity(state.Tree, state.Items, mc)
	if !integrity.Valid {
		completed := s.now().UTC()
		run.Status = domain.MeetingTreeAuditSkipped
		run.Result = "integrity_invalid"
		run.Disposition = "rejected"
		run.ValidatorResult = boundedAuditJSON(integrity, s.config.TreeAudit.MaxPersistedJSONBytes)
		run.CompletedAt = &completed
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		return execution, nil
	}
	if len(snapshot.Snapshot.PrecheckFindings) == 0 && !finalReview {
		completed := s.now().UTC()
		run.Status = domain.MeetingTreeAuditSkipped
		run.Result = "no_anomalies"
		run.Disposition = "none"
		run.Findings = boundedAuditJSON([]treeAuditFinding{}, s.config.TreeAudit.MaxPersistedJSONBytes)
		run.Operations = boundedAuditJSON([]treeAuditOperation{}, s.config.TreeAudit.MaxPersistedJSONBytes)
		run.CompletedAt = &completed
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		logTreeAuditRun(run, 0, 0, 0)
		return execution, nil
	}

	run.ProviderCalled = true
	if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
		return execution, err
	}
	execution.ProviderCalled = true
	auditCtx, cancel := context.WithTimeout(ctx, s.config.TreeAudit.Timeout)
	defer cancel()
	started := s.now()
	result, model, callErr := s.completeTask(auditCtx, task, AIChatRequest{
		System:         treeAuditSystemPrompt,
		User:           buildTreeAuditUserPrompt(snapshot.InputJSON, finalReview),
		MaxTokens:      s.config.TreeAudit.MaxOutputTokens,
		ResponseSchema: &AIResponseSchema{Name: "discussion_tree_audit", Description: "Semantic findings and minimal validated tree patch operations", Strict: true, Schema: json.RawMessage(treeAuditResponseJSONSchema)},
	}, version)
	elapsed := s.now().Sub(started)
	run.Model = model
	run.RawResponse = boundedAuditText(result.Content, s.config.TreeAudit.MaxPersistedJSONBytes)
	run.PromptTokens = result.PromptTokens
	run.CompletionTokens = result.CompletionTokens
	run.ElapsedMilliseconds = elapsed.Milliseconds()
	if callErr == nil && auditCtx.Err() != nil {
		callErr = auditCtx.Err()
	}
	if callErr != nil {
		completed := s.now().UTC()
		run.Status = domain.MeetingTreeAuditFailed
		run.Result = treeAuditFailureResult(callErr)
		run.Disposition = "rejected"
		run.ErrorCode = run.Result
		run.ErrorMessage = truncateErrorMessage(callErr, 1000)
		run.CompletedAt = &completed
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		return execution, callErr
	}
	nodeIDs := make(map[string]struct{}, len(state.Tree.Nodes))
	for _, node := range state.Tree.Nodes {
		nodeIDs[node.ID] = struct{}{}
	}
	candidateIDs := make(map[string]struct{}, len(state.EmergingTopics))
	for _, candidate := range state.EmergingTopics {
		candidateIDs[candidate.ID] = struct{}{}
	}
	response, parseErr := parseTreeAuditResponse(result.Content, version, nodeIDs, candidateIDs)
	logTaskSchemaResult(task, sessionID, parseErr)
	if parseErr != nil {
		completed := s.now().UTC()
		run.Status = domain.MeetingTreeAuditFailed
		run.Result = "invalid_schema"
		run.Disposition = "rejected"
		run.ErrorCode = run.Result
		run.ErrorMessage = truncateErrorMessage(parseErr, 1000)
		run.CompletedAt = &completed
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		return execution, parseErr
	}
	applyMode := s.config.TreeAudit.Mode == domain.MeetingTreeAuditApplyHighConfidence
	dry, validator := validateAndDryRunTreeAuditOperations(state, response.Operations, segments, mc, snapshot.EvidenceRoles, s.config.TreeAudit, runID, version+1, false)
	run.Findings = boundedAuditJSON(response.Findings, s.config.TreeAudit.MaxPersistedJSONBytes)
	run.Operations = boundedAuditJSON(response.Operations, s.config.TreeAudit.MaxPersistedJSONBytes)
	run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
	completed := s.now().UTC()
	run.CompletedAt = &completed
	run.Status = domain.MeetingTreeAuditCompleted
	if !applyMode {
		logTreeAuditDetails(sessionID, response, validator)
		run.Result = "shadow"
		if validator.OperationsWouldApply > 0 {
			run.Disposition = "would_apply"
		} else {
			run.Disposition = "rejected"
		}
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		logTreeAuditRun(run, len(response.Findings), validator.OperationsWouldApply, 0)
		return execution, nil
	}
	if validator.OperationsWouldApply == 0 {
		logTreeAuditDetails(sessionID, response, validator)
		run.Result = "no_safe_operations"
		run.Disposition = "rejected"
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		logTreeAuditRun(run, len(response.Findings), 0, 0)
		return execution, nil
	}
	if ctx.Err() != nil || !s.treeAuditApplyAllowed(sessionID, finalReview) {
		run.Result = "session_ending_discarded"
		run.Disposition = "stale"
		if ctx.Err() != nil {
			run.Result = treeAuditFailureResult(ctx.Err())
			run.ErrorCode = run.Result
			run.ErrorMessage = truncateErrorMessage(ctx.Err(), 1000)
		}
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		return execution, nil
	}
	current, currentErr := s.analysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
	if currentErr != nil {
		return execution, s.failTreeAuditRun(ctx, &run, "analysis_repository_error", currentErr)
	}
	if current.Version != version {
		validator.StaleOperationsRejected = validator.OperationsWouldApply
		validator.OperationsApplied = 0
		for index := range validator.Evaluations {
			validator.Evaluations[index].Applied = false
		}
		run.Result = "stale_tree_version"
		run.Disposition = "stale"
		run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
		logTreeAuditDetails(sessionID, response, validator)
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		logTreeAuditRun(run, len(response.Findings), validator.OperationsWouldApply, 0)
		return execution, nil
	}
	auditedPayload, err := marshalAuditedLivePayload(dry)
	if err != nil {
		return execution, s.failTreeAuditRun(ctx, &run, "payload_encode_error", err)
	}
	run.Result = "applied"
	run.Disposition = "applied"
	run.ResultingTreeVersion = version + 1
	markTreeAuditValidatorApplied(&validator)
	run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
	saved, applied, applyErr := s.auditRepo.ApplyMeetingTreeAudit(ctx, run, version, domain.MeetingAIAnalysis{
		SessionID: sessionID, Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: version + 1,
		Payload: auditedPayload, Model: model,
		SegmentCount: current.SegmentCount, InputChars: current.InputChars,
		UpdatedAt: s.now().UTC(),
	})
	if applyErr != nil {
		return execution, s.failTreeAuditRun(ctx, &run, "apply_transaction_error", applyErr)
	}
	if !applied {
		validator.StaleOperationsRejected = validator.OperationsWouldApply
		validator.OperationsApplied = 0
		for index := range validator.Evaluations {
			validator.Evaluations[index].Applied = false
		}
		run.Result = "stale_tree_version"
		run.Disposition = "stale"
		run.ResultingTreeVersion = 0
		run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
		logTreeAuditDetails(sessionID, response, validator)
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		return execution, nil
	}
	s.publishAnalysis(*saved)
	logTreeAuditDetails(sessionID, response, validator)
	execution.Payload = auditedPayload
	execution.Version = version + 1
	execution.Applied = true
	execution.Result = "applied"
	logTreeAuditRun(run, len(response.Findings), validator.OperationsWouldApply, validator.OperationsApplied)
	return execution, nil
}

func (s *MeetingAnalysisService) runFinalTreeReview(ctx context.Context, sessionID string, payload json.RawMessage, version int64) (execution treeAuditExecution, err error) {
	execution = treeAuditExecution{Payload: payload, Version: version, AuditedVersion: version, TriggerClass: domain.MeetingTreeAuditTriggerFinal}
	if !s.config.TreeAudit.active() {
		execution.Result = "disabled"
		reason := strings.TrimSpace(s.config.TreeAuditUnavailableReason)
		if reason == "" {
			reason = "tree_audit_disabled"
		}
		log.Printf("Final tree review skipped. sessionId=%s reason=%s", sessionID, reason)
		return execution, nil
	}
	if s.auditRepo == nil {
		execution.Result = "disabled"
		log.Printf("Final tree review skipped. sessionId=%s reason=repository_not_ready", sessionID)
		return execution, nil
	}
	// Ending closes and cancels the live-audit lane. Final review has its own
	// durable duplicate claim, so it does not wait behind a slow live provider
	// call and cannot be starved by the live single-flight.
	log.Printf("Final tree review started. sessionId=%s task=%s deployment=%s treeVersion=%d", sessionID, aiTaskFinalTreeReview, s.config.modelNameFor(aiTaskFinalTreeReview), version)
	defer func() {
		if recovered := recover(); recovered != nil {
			execution.Result = "panic"
			err = fmt.Errorf("final tree review panic: %v", recovered)
			log.Printf("Final tree review panic recovered. sessionId=%s treeVersion=%d panic=%v", sessionID, version, recovered)
		}
	}()
	execution, err = s.runTreeAudit(ctx, sessionID, "meeting_ended", aiTaskFinalTreeReview, payload, version, true)
	if err != nil && execution.Result == "" {
		execution.Result = "failed"
	}
	return execution, err
}

func (s *MeetingAnalysisService) treeAuditSuppressionReason(ctx context.Context, sessionID string, triggerClass domain.MeetingTreeAuditTriggerClass, finalReview bool, now time.Time) (string, error) {
	if finalReview {
		return "", nil
	}
	if triggerClass == domain.MeetingTreeAuditTriggerHigh {
		count, err := s.auditRepo.CountMeetingTreeAuditProviderCalls(ctx, sessionID, domain.MeetingTreeAuditTriggerHigh, now.Add(-time.Hour))
		if err != nil {
			return "", err
		}
		if count >= s.config.TreeAudit.HighSeverityMaxRunsPerHour {
			return "high_severity_hourly_limit", nil
		}
		return "", nil
	}
	total, err := s.auditRepo.CountMeetingTreeAuditProviderCalls(ctx, sessionID, domain.MeetingTreeAuditTriggerNormal, time.Time{})
	if err != nil {
		return "", err
	}
	if total >= s.config.TreeAudit.MaxRunsPerSession {
		return "normal_session_limit", nil
	}
	hourly, err := s.auditRepo.CountMeetingTreeAuditProviderCalls(ctx, sessionID, domain.MeetingTreeAuditTriggerNormal, now.Add(-time.Hour))
	if err != nil {
		return "", err
	}
	if hourly >= s.config.TreeAudit.MaxRunsPerHour {
		return "normal_hourly_limit", nil
	}
	return "", nil
}

func (s *MeetingAnalysisService) treeAuditMeetingElapsedSeconds(ctx context.Context, sessionID string, now time.Time) int64 {
	if s.sessionRepo == nil {
		return 0
	}
	session, err := s.sessionRepo.GetMeetingSession(ctx, sessionID)
	if err != nil || session == nil {
		return 0
	}
	started := session.JoinedAt
	if started.IsZero() {
		started = session.RequestedAt
	}
	if started.IsZero() {
		started = session.CreatedAt
	}
	if started.IsZero() || started.After(now) {
		return 0
	}
	return int64(now.Sub(started).Seconds())
}

func (s *MeetingAnalysisService) failTreeAuditRun(ctx context.Context, run *domain.MeetingTreeAuditRun, code string, cause error) error {
	if run == nil {
		return cause
	}
	completed := s.now().UTC()
	run.Status = domain.MeetingTreeAuditFailed
	run.Result = "failed"
	run.Disposition = "rejected"
	run.ErrorCode = code
	run.ErrorMessage = truncateErrorMessage(cause, 1000)
	run.CompletedAt = &completed
	if saveErr := s.auditRepo.SaveMeetingTreeAuditRun(ctx, *run); saveErr != nil {
		return fmt.Errorf("%v; persist tree audit failure: %w", cause, saveErr)
	}
	return cause
}

func (s *MeetingAnalysisService) treeAuditApplyAllowed(sessionID string, finalReview bool) bool {
	if finalReview {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.sessionStateLocked(sessionID)
	return !state.finalizing && !state.auditClosed
}

func markTreeAuditValidatorApplied(validator *treeAuditValidatorResult) {
	if validator == nil {
		return
	}
	validator.OperationsApplied = 0
	for index := range validator.Evaluations {
		if validator.Evaluations[index].WouldApply {
			validator.Evaluations[index].Applied = true
			validator.OperationsApplied++
		}
	}
}

func treeAuditFailureResult(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "failed"
}

func shortAuditHash(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func logTreeAuditRun(run domain.MeetingTreeAuditRun, findingCount, wouldApply, applied int) {
	log.Printf("Tree audit completed. sessionId=%s auditRunId=%s mode=%s triggerReason=%s basedOnTreeVersion=%d resultingTreeVersion=%d snapshotHash=%s deployment=%s model=%s promptVersion=%s status=%s result=%s disposition=%s elapsedMs=%d promptTokens=%d completionTokens=%d findingCount=%d operationsWouldApply=%d operationsApplied=%d",
		run.SessionID, run.ID, run.Mode, run.TriggerReason, run.BasedOnTreeVersion,
		run.ResultingTreeVersion, shortAuditHash(run.SnapshotHash), run.Deployment,
		run.Model, run.PromptVersion, run.Status, run.Result, run.Disposition, run.ElapsedMilliseconds,
		run.PromptTokens, run.CompletionTokens, findingCount, wouldApply, applied)
}

func logTreeAuditDetails(sessionID string, response *treeAuditResponse, validator treeAuditValidatorResult) {
	byType := make(map[string]int)
	bySeverity := make(map[string]int)
	if response != nil {
		for _, finding := range response.Findings {
			byType[string(finding.Type)]++
			bySeverity[finding.Severity]++
		}
	}
	rejected := make(map[string]int)
	for _, evaluation := range validator.Evaluations {
		if !evaluation.WouldApply {
			rejected[evaluation.Reason]++
		}
	}
	log.Printf("Tree audit findings. sessionId=%s findingCount=%d findingCountByType=%v highSeverityFindings=%d mediumSeverityFindings=%d lowSeverityFindings=%d",
		sessionID, len(response.Findings), byType, bySeverity["high"], bySeverity["medium"], bySeverity["low"])
	log.Printf("Tree audit operations. sessionId=%s operationsProposed=%d operationsWouldApply=%d operationsApplied=%d operationsRejected=%d operationsRejectedByReason=%v staleOperationsRejected=%d",
		sessionID, validator.OperationsProposed, validator.OperationsWouldApply, validator.OperationsApplied,
		validator.OperationsRejected, rejected, validator.StaleOperationsRejected)
	log.Printf("Tree audit quality. sessionId=%s topicOutliersBefore=%d topicOutliersAfter=%d candidateFragmentationBefore=%d candidateFragmentationAfter=%d crossAgendaContaminationBefore=%d crossAgendaContaminationAfter=%d treeIntegrityValid=%t",
		sessionID, validator.TopicOutliersBefore, validator.TopicOutliersAfter,
		validator.CandidateFragmentationBefore, validator.CandidateFragmentationAfter,
		validator.CrossAgendaContaminationBefore, validator.CrossAgendaContaminationAfter,
		validator.TreeIntegrityValid)
}
