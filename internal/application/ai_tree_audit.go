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
	Payload              json.RawMessage
	Version              int64
	ResultingTreeVersion int64
	RunID                string
	SnapshotHash         string
	Result               string
	Applied              bool
	AuditedVersion       int64
	TriggerClass         domain.MeetingTreeAuditTriggerClass
	// ProviderCalled is true once the run reached the AI provider. Runs that
	// fail before this point (repository errors, suppression) must not consume
	// the provider-call min interval so they can retry after recovery.
	ProviderCalled bool
}

// TreeAuditReplayResult is returned by the explicit replay/integration entry
// point. It exposes aggregate safety results without exposing prompts or
// credentials and uses the same provider, validator, transaction, and CAS
// path as scheduled production audits.
type TreeAuditReplayResult struct {
	Payload                 json.RawMessage
	BaseTreeVersion         int64
	ResultTreeVersion       int64
	AuditRunID              string
	Result                  string
	ResultClassification    domain.MeetingTreeAuditResultClassification
	FindingsCount           int
	OperationsProposed      int
	OperationsCanonicalized int
	OperationsValid         int
	OperationsApplied       int
	OperationsRejected      int
	IntegrityValid          bool
}

// ReplayTreeAudit explicitly runs a normal applying audit against a supplied
// persisted live snapshot. It is intentionally not wired to HTTP; the app
// composition root uses it only from opt-in integration tests or a dedicated
// operator CLI.
func (s *MeetingAnalysisService) ReplayTreeAudit(ctx context.Context, sessionID string, payload json.RawMessage, version int64) (TreeAuditReplayResult, error) {
	execution, err := s.runTreeAudit(ctx, sessionID, "manual_replay", aiTaskTreeAudit, payload, version, false)
	result := TreeAuditReplayResult{
		Payload: append(json.RawMessage(nil), execution.Payload...), BaseTreeVersion: previousLiveAnalysisState(payload).TreeVersion,
		ResultTreeVersion: execution.ResultingTreeVersion, AuditRunID: execution.RunID, Result: execution.Result,
	}
	if s.auditRepo != nil {
		if run, latestErr := s.auditRepo.GetLatestMeetingTreeAuditRun(ctx, sessionID); latestErr == nil && run != nil {
			result.AuditRunID = run.ID
			result.ResultClassification = run.ResultClassification
			result.FindingsCount = auditJSONArrayLength(run.Findings)
			result.OperationsProposed = run.OperationsProposed
			result.OperationsCanonicalized = run.OperationsCanonicalized
			result.OperationsValid = run.OperationsValid
			result.OperationsApplied = run.OperationsApplied
			result.OperationsRejected = run.OperationsRejected
			var validator treeAuditValidatorResult
			if json.Unmarshal(run.ValidatorResult, &validator) == nil {
				result.IntegrityValid = validator.TreeIntegrityValid
			}
			if run.ResultingTreeVersion > 0 {
				result.ResultTreeVersion = run.ResultingTreeVersion
			}
		}
	}
	return result, err
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
	treeVersion := previousLiveAnalysisState(payload).TreeVersion
	if treeVersion <= 0 {
		treeVersion = version
	}
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	versionDue := treeVersion-state.lastAuditVersion >= s.config.TreeAudit.IntervalVersions
	s.mu.Unlock()
	if len(reasons) == 0 && !versionDue {
		log.Printf("Tree audit skipped. sessionId=%s analysisVersion=%d treeVersion=%d auditSkipped=true auditSkipReason=interval_not_reached", sessionID, version, treeVersion)
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
	normalizeLegacyAgendaTopicIDs(&previous, mc, nil)
	normalizeLegacyAgendaTopicIDs(&current, mc, nil)
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
			TreeAuditCandidateMixedSubjects, TreeAuditTopicOutlier,
			TreeAuditAgendaReentryMissed, TreeAuditAgendaItemForcedNoAgenda,
			TreeAuditParentChildSameTitle, TreeAuditMeetingEndAsDecision:
			reasons = append(reasons, "semantic_anomaly")
		}
	}
	if previous.Tree != nil && current.Tree != nil {
		oldHealth, newHealth := computeTreeHealth(previous.Tree), computeTreeHealth(current.Tree)
		if newHealth.MaxChildren-oldHealth.MaxChildren >= 4 {
			reasons = append(reasons, "topic_child_surge")
		}
	}
	if computeSemanticTreeHealth(current).NeedsReorganization {
		reasons = append(reasons, "semantic_topic_concentration")
	}
	sort.Strings(reasons)
	return uniqueNonEmptyIDs(reasons)
}

func (s *MeetingAnalysisService) scheduleTreeAudit(ctx context.Context, sessionID, triggerReason string, payload json.RawMessage, version int64) {
	if s == nil || !s.config.TreeAudit.active() || s.auditRepo == nil || len(payload) == 0 || version <= 0 {
		return
	}
	now := s.now()
	treeVersion := previousLiveAnalysisState(payload).TreeVersion
	if treeVersion <= 0 {
		treeVersion = version
	}
	triggerClass := treeAuditTriggerClass(triggerReason, false)
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	if state.auditClosed {
		s.mu.Unlock()
		log.Printf("Tree audit skipped. sessionId=%s analysisVersion=%d treeVersion=%d auditSkipped=true auditSkipReason=session_ending", sessionID, version, treeVersion)
		return
	}
	if state.finalizing {
		state.auditPending = true
		state.auditPendingReason = coalesceTreeAuditReason(state.auditPendingReason, triggerReason)
		s.mu.Unlock()
		log.Printf("Tree audit coalesced. sessionId=%s analysisVersion=%d treeVersion=%d auditCoalesced=true auditPending=true reason=finalizing", sessionID, version, treeVersion)
		return
	}
	if state.auditRunning {
		state.auditPending = true
		state.auditPendingReason = coalesceTreeAuditReason(state.auditPendingReason, triggerReason)
		s.mu.Unlock()
		log.Printf("Tree audit coalesced. sessionId=%s analysisVersion=%d treeVersion=%d auditCoalesced=true auditPending=true reason=single_flight", sessionID, version, treeVersion)
		return
	}
	if state.lastAuditVersion >= treeVersion {
		s.mu.Unlock()
		log.Printf("Tree audit skipped. sessionId=%s analysisVersion=%d treeVersion=%d auditSkipped=true auditSkipReason=tree_version_already_audited", sessionID, version, treeVersion)
		return
	}
	if now.Before(state.auditRepoBackoffUntil) {
		state.auditPending = true
		state.auditPendingReason = coalesceTreeAuditReason(state.auditPendingReason, triggerReason)
		s.mu.Unlock()
		log.Printf("Tree audit backing off after repository failure. sessionId=%s analysisVersion=%d treeVersion=%d auditRepoBackoff=true auditPending=true", sessionID, version, treeVersion)
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
		log.Printf("Tree audit rate limited. sessionId=%s analysisVersion=%d treeVersion=%d auditRateLimited=true auditPending=true", sessionID, version, treeVersion)
		return
	}
	state.auditRunning = true
	state.auditRunningDone = make(chan struct{})
	auditCtx, auditCancel := context.WithCancel(ctx)
	state.auditCancel = auditCancel
	state.auditPending = false
	state.auditPendingReason = ""
	s.mu.Unlock()
	log.Printf("Tree audit scheduled. sessionId=%s analysisVersion=%d treeVersion=%d auditScheduled=true triggerReason=%s", sessionID, version, treeVersion, triggerReason)
	go s.executeScheduledTreeAudit(auditCtx, sessionID, triggerReason, append(json.RawMessage(nil), payload...), version)
}

func (s *MeetingAnalysisService) executeScheduledTreeAudit(ctx context.Context, sessionID, triggerReason string, payload json.RawMessage, version int64) {
	treeVersion := previousLiveAnalysisState(payload).TreeVersion
	if treeVersion <= 0 {
		treeVersion = version
	}
	execution := treeAuditExecution{Payload: payload, Version: version, AuditedVersion: treeVersion, ResultingTreeVersion: treeVersion, TriggerClass: treeAuditTriggerClass(triggerReason, false)}
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
		auditedVersion = previousLiveAnalysisState(execution.Payload).TreeVersion
	}
	if execution.Applied && execution.ResultingTreeVersion > auditedVersion {
		auditedVersion = execution.ResultingTreeVersion
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
	currentTreeVersion := previousLiveAnalysisState(state.lastPayload).TreeVersion
	if currentTreeVersion > auditedVersion && !state.auditClosed {
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
	execution := treeAuditExecution{Payload: payload, Version: version, TriggerClass: triggerClass}
	if s.auditRepo == nil {
		return execution, fmt.Errorf("tree audit repository is not configured")
	}
	state := previousLiveAnalysisState(payload)
	if state.Tree == nil {
		return execution, fmt.Errorf("tree audit payload has no tree")
	}
	analysisVersion := version
	treeVersion := state.TreeVersion
	if treeVersion <= 0 {
		treeVersion = analysisVersion
	}
	execution.AuditedVersion = treeVersion
	execution.ResultingTreeVersion = treeVersion
	transcriptLimit := 2000
	if finalReview {
		transcriptLimit = meetingAnalysisFinalTranscriptLimit
	}
	segments, err := s.transcriptRepo.ListTranscriptSegments(ctx, "", sessionID, transcriptLimit)
	if err != nil {
		return execution, fmt.Errorf("load tree audit transcript: %w", err)
	}
	mc := s.sessionMeetingContext(ctx, sessionID)
	normalizeLegacyAgendaTopicIDs(&state, mc, nil)
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
	if !manualReplay && latestTerminal && latest.Task == string(task) && latest.PromptVersion == task.promptVersion() && latest.Deployment == s.config.modelNameFor(task) && latest.SnapshotHash == snapshot.Hash && (latest.BasedOnTreeVersion == treeVersion || latest.ResultingTreeVersion == treeVersion) {
		log.Printf("Tree audit skipped. sessionId=%s analysisVersion=%d treeVersion=%d snapshotHash=%s auditSkipped=true auditSkipReason=duplicate_snapshot", sessionID, analysisVersion, treeVersion, shortAuditHash(snapshot.Hash))
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
		ID: runID, SessionID: sessionID, BasedOnTreeVersion: treeVersion,
		TriggerReason: truncateRunes(triggerReason, 300),
		TriggerClass:  triggerClass, Task: string(task), Deployment: s.config.modelNameFor(task),
		PromptVersion: task.promptVersion(), SnapshotHash: snapshot.Hash,
		Status: domain.MeetingTreeAuditRunning, Result: "running", Disposition: "none",
		MeetingElapsedSeconds: meetingElapsedSeconds,
		InputSummary:          boundedAuditJSON(inputSummary, s.config.TreeAudit.MaxPersistedJSONBytes),
		InputPayload:          boundedAuditJSON(snapshot.InputJSON, s.config.TreeAudit.MaxPersistedJSONBytes),
		CreatedAt:             now,
	}
	// Non-audited terminal rows (for example rate limiting or provider
	// failures) carry, but never increment, the session-scoped streak. This
	// prevents an intervening skipped run from accidentally erasing the health
	// signal used by the next completed audit.
	if latest != nil && latest.SessionID == sessionID {
		run.ConsecutiveUnappliedRuns = latest.ConsecutiveUnappliedRuns
	}
	claimed, err := s.auditRepo.TryStartMeetingTreeAuditRun(ctx, run)
	if err != nil {
		return execution, err
	}
	if !claimed {
		log.Printf("Tree audit skipped. sessionId=%s analysisVersion=%d treeVersion=%d snapshotHash=%s auditSkipped=true auditSkipReason=duplicate_snapshot", sessionID, analysisVersion, treeVersion, shortAuditHash(snapshot.Hash))
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
		log.Printf("Tree audit rate limited. sessionId=%s analysisVersion=%d treeVersion=%d triggerClass=%s suppressionReason=%s meetingElapsedSeconds=%d", sessionID, analysisVersion, treeVersion, triggerClass, suppressionReason, meetingElapsedSeconds)
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
		classifyTreeAuditRun(&run, latest, s.config.TreeAudit)
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		logTreeAuditRun(run, 0, treeAuditValidatorResult{})
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
	}, treeVersion)
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
	response, parseErr := parseTreeAuditResponse(result.Content, treeVersion)
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
	canonicalizeTreeAuditResponse(response, state)
	dry, validator := validateAndDryRunTreeAuditOperations(state, response.Operations, segments, mc, snapshot.EvidenceRoles, s.config.TreeAudit, runID, treeVersion+1, false)
	validator.ParserElementsRejected = len(response.ParseRejections)
	validator.OperationsCanonicalized = response.CanonicalizedOperationCount
	parserOperationRejections := 0
	for _, rejection := range response.ParseRejections {
		if rejection.ElementType != "operation" {
			continue
		}
		parserOperationRejections++
		validator.Evaluations = append(validator.Evaluations, treeAuditValidatorEvaluation{
			OperationID: rejection.ElementID,
			Result:      "rejected",
			Reason:      "parser_" + rejection.Reason,
		})
	}
	validator.OperationsProposed += parserOperationRejections
	validator.OperationsRejected += parserOperationRejections
	run.Findings = boundedAuditJSON(response.Findings, s.config.TreeAudit.MaxPersistedJSONBytes)
	run.Operations = boundedAuditJSON(response.Operations, s.config.TreeAudit.MaxPersistedJSONBytes)
	run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
	completed := s.now().UTC()
	run.CompletedAt = &completed
	run.Status = domain.MeetingTreeAuditCompleted
	if validator.OperationsValid == 0 {
		logTreeAuditDetails(sessionID, runID, response, validator)
		run.Result = "no_safe_operations"
		run.Disposition = "rejected"
		previousUnapplied := 0
		if latest != nil && latest.SessionID == sessionID {
			previousUnapplied = latest.ConsecutiveUnappliedRuns
		}
		threshold := s.config.TreeAudit.normalized().UnappliedWarningThreshold
		if previousUnapplied+1 >= threshold {
			validator.DeterministicFallbackEvaluated = true
			repairedPayload, repairStats := applyDeterministicFinalTreeRepairs(payload, mc, treeVersion, finalRepairInput{
				Segments: segments,
				Audit:    s.config.TreeAudit,
			})
			validator.DeterministicFallbackReason = dominantAuditValidatorRejection(validator)
			switch {
			case repairStats.Error != "":
				validator.DeterministicFallbackAction = "preserve_last_good"
				validator.DeterministicFallbackReason = "repair_error"
			case repairStats.IntegrityRejected:
				validator.DeterministicFallbackAction = "preserve_last_good"
				validator.DeterministicFallbackReason = "repair_integrity_rejected"
			case !finalRepairStatsChanged(repairStats):
				validator.DeterministicFallbackAction = "no_safe_deterministic_change"
				if validator.DeterministicFallbackReason == "" {
					validator.DeterministicFallbackReason = "no_repairable_precheck"
				}
			case liveTreeHash(state.Tree) == liveTreeHash(previousLiveAnalysisState(repairedPayload).Tree):
				validator.DeterministicFallbackAction = "no_safe_deterministic_change"
				validator.DeterministicFallbackReason = "canonical_tree_no_op"
			default:
				current, currentErr := s.analysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
				if currentErr != nil {
					return execution, s.failTreeAuditRun(ctx, &run, "analysis_repository_error", currentErr)
				}
				switch {
				case ctx.Err() != nil || !s.treeAuditApplyAllowed(sessionID, finalReview):
					validator.DeterministicFallbackAction = "preserve_last_good"
					validator.DeterministicFallbackReason = "session_not_applyable"
				case current.Version != analysisVersion:
					validator.DeterministicFallbackAction = "preserve_newer_version"
					validator.DeterministicFallbackReason = "stale_tree_version"
				default:
					repairedState := previousLiveAnalysisState(repairedPayload)
					repairedState.TreeVersion = treeVersion + 1
					repairedState.ChangeSource = "tree_audit_deterministic_fallback"
					repairedState.AuditRunID = runID
					repairedState.BasedOnTreeVersion = treeVersion
					repairedState.TreeChanges = diffLiveAnalysisTrees(state.Tree, repairedState.Tree, treeVersion+1)
					applyLiveTreeSnapshotMetadata(&repairedState, state.Tree, treeVersion, nil)
					repairedPayload, err = json.Marshal(repairedState)
					if err != nil {
						return execution, s.failTreeAuditRun(ctx, &run, "payload_encode_error", err)
					}
					repairedPayload, err = finalizeCompletedLiveProjection(
						repairedPayload, payload, analysisVersion+1,
						repairedState.HighestAvailableFinalSequenceNo, s.now().UTC(),
					)
					if err != nil {
						return execution, s.failTreeAuditRun(ctx, &run, "payload_encode_error", err)
					}
					validator.DeterministicFallbackApplied = true
					validator.DeterministicFallbackAction = "apply_safe_repair"
					run.Result = "deterministic_fallback_applied"
					run.Disposition = "applied"
					run.ResultingTreeVersion = treeVersion + 1
					run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
					classifyTreeAuditRun(&run, latest, s.config.TreeAudit)
					saved, applied, applyErr := s.auditRepo.ApplyMeetingTreeAudit(ctx, run, analysisVersion, domain.MeetingAIAnalysis{
						SessionID: sessionID, Type: domain.MeetingAIAnalysisLive,
						Status: domain.MeetingAIAnalysisCompleted, Version: analysisVersion + 1,
						Payload: repairedPayload, Model: model,
						SegmentCount: current.SegmentCount, InputChars: current.InputChars,
						UpdatedAt: s.now().UTC(),
					})
					if applyErr != nil {
						return execution, s.failTreeAuditRun(ctx, &run, "apply_transaction_error", applyErr)
					}
					if applied {
						s.publishCompletedLiveAnalysis(*saved, payload)
						execution.Payload = repairedPayload
						execution.Version = analysisVersion + 1
						execution.ResultingTreeVersion = treeVersion + 1
						execution.Applied = true
						execution.Result = run.Result
						log.Printf("Tree audit deterministic fallback evaluated. sessionId=%s auditRunId=%s basedOnTreeVersion=%d resultingTreeVersion=%d consecutiveUnappliedBefore=%d fallbackAction=%s fallbackReason=%s lowInformationRewritten=%d lowInformationMerged=%d lowInformationRejected=%d kindValidationChanges=%d ambiguousKinds=%d kindSemanticSplits=%d kindSplitFragments=%d kindSplitRejected=%d kindRelationsCreated=%d sameEvidenceSynthesisMerged=%d sameKindDuplicatesMerged=%d recapDuplicatesMerged=%d",
							sessionID, runID, treeVersion, treeVersion+1, previousUnapplied,
							validator.DeterministicFallbackAction, validator.DeterministicFallbackReason,
							repairStats.LowInformationItemsRewritten, repairStats.LowInformationItemsMerged,
							repairStats.LowInformationItemsRejected, repairStats.KindValidationChanges,
							repairStats.KindValidationAmbiguous, repairStats.KindSemanticSplits,
							repairStats.KindSplitFragments, repairStats.KindSplitRejected,
							repairStats.KindRelationsCreated,
							repairStats.SameEvidenceSynthesisMerged,
							repairStats.SameKindDuplicatesMerged, repairStats.RecapDuplicatesMerged)
						logTreeAuditRun(run, len(response.Findings), validator)
						return execution, nil
					}
					validator.DeterministicFallbackApplied = false
					validator.DeterministicFallbackAction = "preserve_newer_version"
					validator.DeterministicFallbackReason = "stale_tree_version"
					run.Result = "stale_tree_version"
					run.Disposition = "stale"
					run.ResultingTreeVersion = 0
				}
			}
			log.Printf("Tree audit deterministic fallback evaluated. sessionId=%s auditRunId=%s basedOnTreeVersion=%d consecutiveUnappliedBefore=%d fallbackAction=%s fallbackReason=%s applied=%t",
				sessionID, runID, treeVersion, previousUnapplied, validator.DeterministicFallbackAction,
				validator.DeterministicFallbackReason, validator.DeterministicFallbackApplied)
		} else {
			validator.DeterministicFallbackAction = "below_unapplied_threshold"
		}
		run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
		classifyTreeAuditRun(&run, latest, s.config.TreeAudit)
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		logTreeAuditRun(run, len(response.Findings), validator)
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
		classifyTreeAuditRun(&run, latest, s.config.TreeAudit)
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
	if current.Version != analysisVersion {
		validator.StaleOperationsRejected = validator.OperationsValid
		validator.OperationsApplied = 0
		for index := range validator.Evaluations {
			validator.Evaluations[index].Applied = false
		}
		run.Result = "stale_tree_version"
		run.Disposition = "stale"
		run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
		classifyTreeAuditRun(&run, latest, s.config.TreeAudit)
		logTreeAuditDetails(sessionID, runID, response, validator)
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		logTreeAuditRun(run, len(response.Findings), validator)
		return execution, nil
	}
	auditedPayload, err := marshalAuditedLivePayload(dry)
	if err != nil {
		return execution, s.failTreeAuditRun(ctx, &run, "payload_encode_error", err)
	}
	if liveTreeHash(state.Tree) == liveTreeHash(dry.Tree) {
		validator.OperationsApplied = 0
		for index := range validator.Evaluations {
			validator.Evaluations[index].Applied = false
		}
		run.Result = "no_op"
		run.Disposition = "no_op"
		run.ResultingTreeVersion = treeVersion
		run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
		classifyTreeAuditRun(&run, latest, s.config.TreeAudit)
		if saveErr := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); saveErr != nil {
			return execution, saveErr
		}
		log.Printf("Tree audit no-op suppressed. sessionId=%s analysisVersion=%d treeVersion=%d noOpTreeVersionIncrementCount=0 noOpTreeBroadcastCount=0", sessionID, analysisVersion, treeVersion)
		execution.Result = run.Result
		return execution, nil
	}
	dry.TreeVersion = treeVersion + 1
	dry.AnalysisVersion = analysisVersion + 1
	dry.TreeAnalysisVersion = analysisVersion + 1
	dry.TreeProjectionVersion = treeVersion + 1
	dry.BasedOnTreeVersion = treeVersion
	dry.TreeChanges = diffLiveAnalysisTrees(state.Tree, dry.Tree, treeVersion+1)
	applyLiveTreeSnapshotMetadata(&dry, state.Tree, treeVersion, nil)
	auditedPayload, err = marshalAuditedLivePayload(dry)
	if err != nil {
		return execution, s.failTreeAuditRun(ctx, &run, "payload_encode_error", err)
	}
	auditedPayload, err = finalizeCompletedLiveProjection(
		auditedPayload, payload, analysisVersion+1,
		dry.HighestAvailableFinalSequenceNo, s.now().UTC(),
	)
	if err != nil {
		return execution, s.failTreeAuditRun(ctx, &run, "payload_encode_error", err)
	}
	markTreeAuditValidatorApplied(&validator)
	if validator.OperationsApplied > 0 && validator.OperationsRejected > 0 {
		run.Result = "partial_success"
	} else {
		run.Result = "applied"
	}
	run.Disposition = "applied"
	run.ResultingTreeVersion = treeVersion + 1
	run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
	classifyTreeAuditRun(&run, latest, s.config.TreeAudit)
	saved, applied, applyErr := s.auditRepo.ApplyMeetingTreeAudit(ctx, run, analysisVersion, domain.MeetingAIAnalysis{
		SessionID: sessionID, Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: analysisVersion + 1,
		Payload: auditedPayload, Model: model,
		SegmentCount: current.SegmentCount, InputChars: current.InputChars,
		UpdatedAt: s.now().UTC(),
	})
	if applyErr != nil {
		return execution, s.failTreeAuditRun(ctx, &run, "apply_transaction_error", applyErr)
	}
	if !applied {
		validator.StaleOperationsRejected = validator.OperationsValid
		validator.OperationsApplied = 0
		for index := range validator.Evaluations {
			validator.Evaluations[index].Applied = false
		}
		run.Result = "stale_tree_version"
		run.Disposition = "stale"
		run.ResultingTreeVersion = 0
		run.ValidatorResult = boundedAuditJSON(validator, s.config.TreeAudit.MaxPersistedJSONBytes)
		classifyTreeAuditRun(&run, latest, s.config.TreeAudit)
		logTreeAuditDetails(sessionID, runID, response, validator)
		if err := s.auditRepo.SaveMeetingTreeAuditRun(ctx, run); err != nil {
			return execution, err
		}
		execution.Result = run.Result
		return execution, nil
	}
	s.publishCompletedLiveAnalysis(*saved, payload)
	logTreeAuditDetails(sessionID, runID, response, validator)
	execution.Payload = auditedPayload
	execution.Version = analysisVersion + 1
	execution.ResultingTreeVersion = treeVersion + 1
	execution.Applied = true
	execution.Result = run.Result
	logTreeAuditRun(run, len(response.Findings), validator)
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
		if validator.Evaluations[index].Valid {
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

func classifyTreeAuditRun(run *domain.MeetingTreeAuditRun, previous *domain.MeetingTreeAuditRun, cfg TreeAuditConfig) {
	if run == nil {
		return
	}
	var validator treeAuditValidatorResult
	if len(run.ValidatorResult) > 0 {
		_ = json.Unmarshal(run.ValidatorResult, &validator)
	}
	findingCount := auditJSONArrayLength(run.Findings)
	operationCount := auditJSONArrayLength(run.Operations)
	if validator.OperationsProposed == 0 && operationCount > 0 {
		validator.OperationsProposed = operationCount
	}
	run.OperationsProposed = validator.OperationsProposed
	run.OperationsCanonicalized = validator.OperationsCanonicalized
	run.OperationsValid = validator.OperationsValid
	run.OperationsApplied = validator.OperationsApplied
	run.OperationsRejected = validator.OperationsRejected
	rejectionReasons := make(map[string]int)
	for _, evaluation := range validator.Evaluations {
		if !evaluation.Valid && strings.TrimSpace(evaluation.Reason) != "" {
			rejectionReasons[evaluation.Reason]++
		}
	}
	if len(rejectionReasons) > 0 {
		run.RejectionReasons = boundedAuditJSON(rejectionReasons, cfg.normalized().MaxPersistedJSONBytes)
	} else {
		run.RejectionReasons = nil
	}

	classification := domain.MeetingTreeAuditResultClassification("")
	auditedOutcome := run.ProviderCalled && run.Status == domain.MeetingTreeAuditCompleted
	if run.Result == "no_anomalies" {
		auditedOutcome = true
	}
	if auditedOutcome {
		switch {
		case validator.DeterministicFallbackApplied && run.ResultingTreeVersion > run.BasedOnTreeVersion:
			classification = domain.MeetingTreeAuditResultApplied
		case run.OperationsApplied > 0 && run.ResultingTreeVersion > run.BasedOnTreeVersion:
			classification = domain.MeetingTreeAuditResultApplied
		case findingCount == 0 && run.OperationsProposed == 0:
			classification = domain.MeetingTreeAuditResultClean
		case findingCount > 0 && run.OperationsProposed == 0:
			classification = domain.MeetingTreeAuditResultFindingsOnly
		default:
			classification = domain.MeetingTreeAuditResultRejected
		}
	}
	run.ResultClassification = classification
	previousConsecutive := 0
	if previous != nil && previous.SessionID == run.SessionID {
		previousConsecutive = previous.ConsecutiveUnappliedRuns
	}
	switch classification {
	case domain.MeetingTreeAuditResultFindingsOnly, domain.MeetingTreeAuditResultRejected:
		run.ConsecutiveUnappliedRuns = previousConsecutive + 1
	case domain.MeetingTreeAuditResultClean, domain.MeetingTreeAuditResultApplied:
		run.ConsecutiveUnappliedRuns = 0
	default:
		run.ConsecutiveUnappliedRuns = previousConsecutive
	}

	latestVersion := run.BasedOnTreeVersion
	if run.ResultingTreeVersion > latestVersion {
		latestVersion = run.ResultingTreeVersion
	}
	log.Printf("Tree audit result classified. auditRunId=%s sessionId=%s classification=%s findingsCount=%d operationsProposed=%d operationsCanonicalized=%d operationsValid=%d operationsApplied=%d operationsRejected=%d consecutiveUnappliedRuns=%d resultTreeVersion=%d deterministicFallbackEvaluated=%t deterministicFallbackApplied=%t deterministicFallbackAction=%s deterministicFallbackReason=%s",
		run.ID, run.SessionID, classification, findingCount, run.OperationsProposed,
		run.OperationsCanonicalized, run.OperationsValid, run.OperationsApplied,
		run.OperationsRejected, run.ConsecutiveUnappliedRuns, run.ResultingTreeVersion,
		validator.DeterministicFallbackEvaluated, validator.DeterministicFallbackApplied,
		validator.DeterministicFallbackAction, validator.DeterministicFallbackReason)
	threshold := cfg.normalized().UnappliedWarningThreshold
	if run.ConsecutiveUnappliedRuns == threshold &&
		(classification == domain.MeetingTreeAuditResultFindingsOnly || classification == domain.MeetingTreeAuditResultRejected) {
		log.Printf("Tree audit findings remain unapplied. sessionId=%s consecutiveUnappliedAuditRuns=%d latestFindingsCount=%d latestOperationsProposed=%d latestRejectionReasons=%v dominantRejectionReason=%s deterministicFallbackEvaluated=%t deterministicFallbackApplied=%t deterministicFallbackAction=%s deterministicFallbackReason=%s latestTreeVersion=%d",
			run.SessionID, run.ConsecutiveUnappliedRuns, findingCount,
			run.OperationsProposed, rejectionReasons, dominantAuditRejectionReason(rejectionReasons),
			validator.DeterministicFallbackEvaluated, validator.DeterministicFallbackApplied,
			validator.DeterministicFallbackAction, validator.DeterministicFallbackReason, latestVersion)
	}
}

func dominantAuditRejectionReason(reasons map[string]int) string {
	bestReason, bestCount := "", 0
	for reason, count := range reasons {
		if count > bestCount || (count == bestCount && (bestReason == "" || reason < bestReason)) {
			bestReason, bestCount = reason, count
		}
	}
	return bestReason
}

func dominantAuditValidatorRejection(validator treeAuditValidatorResult) string {
	reasons := make(map[string]int)
	for _, evaluation := range validator.Evaluations {
		if evaluation.Valid || strings.TrimSpace(evaluation.Reason) == "" {
			continue
		}
		reasons[evaluation.Reason]++
	}
	return dominantAuditRejectionReason(reasons)
}

func auditJSONArrayLength(value json.RawMessage) int {
	if len(value) == 0 {
		return 0
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(value, &elements); err != nil {
		return 0
	}
	return len(elements)
}

func logTreeAuditRun(run domain.MeetingTreeAuditRun, findingCount int, validator treeAuditValidatorResult) {
	rejectionReasons := make(map[string]int)
	for _, evaluation := range validator.Evaluations {
		if evaluation.Result != "validated" {
			rejectionReasons[evaluation.Reason]++
		}
	}
	log.Printf("Tree audit completed. sessionId=%s auditRunId=%s triggerReason=%s basedOnTreeVersion=%d resultingTreeVersion=%d snapshotHash=%s deployment=%s model=%s promptVersion=%s status=%s result=%s disposition=%s resultClassification=%s consecutiveUnappliedRuns=%d elapsedMs=%d promptTokens=%d completionTokens=%d findingsCount=%d operationsProposed=%d operationsCanonicalized=%d operationsValid=%d operationsApplied=%d operationsRejected=%d rejectionReasons=%v integrityValid=%t",
		run.SessionID, run.ID, run.TriggerReason, run.BasedOnTreeVersion,
		run.ResultingTreeVersion, shortAuditHash(run.SnapshotHash), run.Deployment,
		run.Model, run.PromptVersion, run.Status, run.Result, run.Disposition, run.ResultClassification, run.ConsecutiveUnappliedRuns, run.ElapsedMilliseconds,
		run.PromptTokens, run.CompletionTokens, findingCount, validator.OperationsProposed,
		validator.OperationsCanonicalized, validator.OperationsValid, validator.OperationsApplied,
		validator.OperationsRejected, rejectionReasons, validator.TreeIntegrityValid)
}

func logTreeAuditDetails(sessionID, auditRunID string, response *treeAuditResponse, validator treeAuditValidatorResult) {
	byType := make(map[string]int)
	bySeverity := make(map[string]int)
	if response != nil {
		for _, finding := range response.Findings {
			byType[string(finding.Type)]++
			bySeverity[finding.Severity]++
			log.Printf("Tree audit finding evaluated. sessionId=%s auditRunId=%s findingId=%s findingFingerprint=%s category=%s severity=%s confidence=%.2f nodeIds=%v evidenceFingerprint=%s",
				sessionID, auditRunID, finding.FindingID, treeAuditFindingFingerprint(finding),
				finding.Type, finding.Severity, finding.Confidence, finding.NodeIDs,
				itemEvidenceFingerprint(liveAnalysisItem{EvidenceSequenceNos: finding.EvidenceSequenceNos}))
		}
	}
	rejected := make(map[string]int)
	for _, evaluation := range validator.Evaluations {
		if !evaluation.Valid {
			rejected[evaluation.Reason]++
		}
	}
	log.Printf("Tree audit findings. sessionId=%s auditRunId=%s findingCount=%d findingCountByType=%v highSeverityFindings=%d mediumSeverityFindings=%d lowSeverityFindings=%d",
		sessionID, auditRunID, len(response.Findings), byType, bySeverity["high"], bySeverity["medium"], bySeverity["low"])
	log.Printf("Tree audit operations. sessionId=%s auditRunId=%s operationsProposed=%d operationsValid=%d operationsApplied=%d operationsRejected=%d operationsRejectedByReason=%v staleOperationsRejected=%d parserElementsRejected=%d operationsCanonicalized=%d",
		sessionID, auditRunID, validator.OperationsProposed, validator.OperationsValid, validator.OperationsApplied,
		validator.OperationsRejected, rejected, validator.StaleOperationsRejected, validator.ParserElementsRejected, validator.OperationsCanonicalized)
	log.Printf("Tree audit quality. sessionId=%s auditRunId=%s topicOutliersBefore=%d topicOutliersAfter=%d candidateFragmentationBefore=%d candidateFragmentationAfter=%d crossAgendaContaminationBefore=%d crossAgendaContaminationAfter=%d nodeCountBefore=%d nodeCountAfter=%d lowInformationItemsBefore=%d lowInformationItemsAfter=%d rewritesApplied=%d mergesApplied=%d reclassificationsApplied=%d deactivationsApplied=%d fixedAgendaMutationsRejected=%d treeIntegrityValid=%t",
		sessionID, auditRunID, validator.TopicOutliersBefore, validator.TopicOutliersAfter,
		validator.CandidateFragmentationBefore, validator.CandidateFragmentationAfter,
		validator.CrossAgendaContaminationBefore, validator.CrossAgendaContaminationAfter,
		validator.NodeCountBefore, validator.NodeCountAfter,
		validator.LowInformationItemsBefore, validator.LowInformationItemsAfter,
		validator.RewritesApplied, validator.MergesApplied, validator.ReclassificationsApplied, validator.DeactivationsApplied,
		rejected["fixed_agenda_immutable"],
		validator.TreeIntegrityValid)
	logTreeAuditOperationDetails(sessionID, auditRunID, response, validator)
}

// logTreeAuditOperationDetails emits one operation-level log line per
// evaluated operation (design D4 / doc section on per-operation logging):
// operationType, target, fromParent/toParent, modelConfidence,
// effectiveConfidence, the validation result+category, and the rejection
// reason. It never logs meeting transcript text, labels, or credentials -
// only IDs and the validator's own decision fields.
func logTreeAuditOperationDetails(sessionID, auditRunID string, response *treeAuditResponse, validator treeAuditValidatorResult) {
	byOperationID := make(map[string]treeAuditOperation)
	if response != nil {
		for _, operation := range response.Operations {
			byOperationID[operation.OperationID] = operation
		}
	}
	for _, evaluation := range validator.Evaluations {
		operation := byOperationID[evaluation.OperationID]
		log.Printf("Tree audit cleanup operation. sessionId=%s auditRunId=%s operationId=%s operationType=%s targetCanonical=%s fromParent=%s toParent=%s modelConfidence=%.2f effectiveConfidence=%.2f effectiveThreshold=%.2f groundingDecision=%s groundingConfidence=%.2f groundingSourceTypes=%s unsupportedAtoms=%v validationResult=%t applicationResult=%t result=%s riskClass=%s rejectionReason=%s",
			sessionID, auditRunID, evaluation.OperationID, evaluation.Type, treeAuditOperationTargetLabel(operation),
			operation.FromParentCanonicalNodeID, operation.ToParentCanonicalNodeID,
			evaluation.ModelConfidence, evaluation.EffectiveConfidence, evaluation.EffectiveThreshold,
			evaluation.GroundingDecision, evaluation.GroundingConfidence,
			formatGroundingSourceTypes(evaluation.GroundingSourceTypes), evaluation.UnsupportedAtoms,
			evaluation.Valid, evaluation.Applied,
			evaluation.Result, evaluation.Category, evaluation.Reason)
	}
}

func treeAuditFindingFingerprint(finding treeAuditFinding) string {
	nodeIDs := append([]string(nil), finding.NodeIDs...)
	relatedIDs := append([]string(nil), finding.RelatedNodeIDs...)
	sequenceNos := append([]int64(nil), finding.EvidenceSequenceNos...)
	sort.Strings(nodeIDs)
	sort.Strings(relatedIDs)
	sort.Slice(sequenceNos, func(i, j int) bool { return sequenceNos[i] < sequenceNos[j] })
	return tombstoneLogHash(fmt.Sprintf("%s|%s|%v|%v|%v",
		finding.Type, finding.Severity, nodeIDs, relatedIDs, sequenceNos))
}

// treeAuditOperationTargetLabel picks whichever target identifier the
// operation actually populated, for logging only.
func treeAuditOperationTargetLabel(operation treeAuditOperation) string {
	switch {
	case operation.TargetCanonicalItemID != "":
		return operation.TargetCanonicalItemID
	case operation.TargetCanonicalNodeID != "":
		return operation.TargetCanonicalNodeID
	case operation.TargetCandidateID != "":
		return operation.TargetCandidateID
	case len(operation.TargetCanonicalItemIDs) > 0:
		return strings.Join(operation.TargetCanonicalItemIDs, ",")
	default:
		return ""
	}
}
