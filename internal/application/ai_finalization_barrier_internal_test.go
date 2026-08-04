package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

// finalizationBarrierCompleter routes every pipeline task by its per-task
// deployment name so one fake can serve live extraction, final tree review and
// final summary inside a single finalization run. liveRelease lets a test hold
// an in-flight live round open across the finalization wait deadline without a
// timing sleep.
type finalizationBarrierCompleter struct {
	mu           sync.Mutex
	calls        map[string]int
	liveStarted  chan struct{}
	liveRelease  chan struct{}
	liveOnce     sync.Once
	liveContent  string
	reviewResult string
	summary      string
}

const (
	barrierLiveDeployment    = "deployment-live"
	barrierReviewDeployment  = "deployment-final-review"
	barrierSummaryDeployment = "deployment-final-summary"
	barrierAuditDeployment   = "deployment-tree-audit"
	barrierReorgDeployment   = "deployment-tree-reorganizer"
)

func newFinalizationBarrierCompleter() *finalizationBarrierCompleter {
	return &finalizationBarrierCompleter{
		calls:        make(map[string]int),
		liveContent:  `{"summary":"進捗","currentTopic":"障害対応","items":[],"assignments":[]}`,
		reviewResult: `{"basedOnTreeVersion":12,"operations":[]}`,
		summary:      `{"suggestedTitle":"障害レビュー","overview":"最終要約","decisions":[],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`,
	}
}

func (c *finalizationBarrierCompleter) Complete(ctx context.Context, request AIChatRequest) (AIChatResult, error) {
	c.mu.Lock()
	c.calls[request.Deployment]++
	c.mu.Unlock()
	switch request.Deployment {
	case barrierLiveDeployment:
		c.liveOnce.Do(func() {
			if c.liveStarted != nil {
				close(c.liveStarted)
			}
		})
		if c.liveRelease != nil {
			select {
			case <-c.liveRelease:
			case <-ctx.Done():
				return AIChatResult{}, ctx.Err()
			}
		}
		return AIChatResult{Content: c.liveContent}, nil
	case barrierReviewDeployment, barrierAuditDeployment, barrierReorgDeployment:
		return AIChatResult{Content: c.reviewResult}, nil
	default:
		return AIChatResult{Content: c.summary}, nil
	}
}

func (c *finalizationBarrierCompleter) callsFor(deployment string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[deployment]
}

func finalizationBarrierConfig() MeetingAnalysisConfig {
	return MeetingAnalysisConfig{
		Enabled: true, LiveEnabled: true, FinalEnabled: true,
		FinalMaxInputChars:      12000,
		FinalizationWaitTimeout: 60 * time.Millisecond,
		FinalizationQuietPeriod: time.Millisecond,
		FinalFlushMaxAttempts:   1,
		Model:                   "deployment-default",
		TaskModels: AITaskModels{
			LiveExtraction:  barrierLiveDeployment,
			FinalTreeReview: barrierReviewDeployment,
			FinalSummary:    barrierSummaryDeployment,
			TreeAudit:       barrierAuditDeployment,
			TreeReorganizer: barrierReorgDeployment,
		},
	}
}

func seedStableLiveSnapshot(t *testing.T, repository *contextBarrierRepository, sessionID string) {
	t.Helper()
	state := previousLiveAnalysisState(failureBoundaryLivePayload(t))
	state.CoveredThroughSequenceNo = 10
	state.AnalyzedFinalSegments = []analyzedFinalSegmentRef{{SequenceNo: 10}}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{
		SessionID: sessionID, Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 12, Payload: payload,
		UpdatedAt: time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
}

func barrierTranscripts(sessionID string) *finalizationFailureTranscriptRepository {
	return &finalizationFailureTranscriptRepository{segments: []domain.TranscriptSegment{{
		SessionID: sessionID, SequenceNo: 10, IsFinal: true,
		Text: "復旧対応として旧スイッチへ切り戻し、許可VLANを修正して正常化を確認しました。",
	}}}
}

func finalizationProgressOf(t *testing.T, repository *contextBarrierRepository, sessionID string) (*domain.MeetingAIAnalysis, finalizationProgressPayload) {
	t.Helper()
	analysis, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisFinalization)
	if err != nil || analysis == nil {
		return nil, finalizationProgressPayload{}
	}
	var payload finalizationProgressPayload
	if len(analysis.Payload) > 0 {
		if err := json.Unmarshal(analysis.Payload, &payload); err != nil {
			t.Fatalf("finalization progress payload unmarshal failed: %v", err)
		}
	}
	return analysis, payload
}

// TestFinalizationSurvivesLiveRoundCompletingAfterWaitDeadline reproduces
// session_c01a27bf3197e2c1: the sealed live round (tree_reorganizer included)
// finished about two seconds after the ten second in-flight wait expired, and
// finalization was abandoned without ever starting the final summary.
func TestFinalizationSurvivesLiveRoundCompletingAfterWaitDeadline(t *testing.T) {
	const sessionID = "session-barrier-late-live-round"
	repository := newContextBarrierRepository()
	seedStableLiveSnapshot(t, repository, sessionID)
	completer := newFinalizationBarrierCompleter()
	service := NewMeetingAnalysisService(
		repository, barrierTranscripts(sessionID), nil, completer, finalizationBarrierConfig(),
	)

	// One live round is sealed and still running when the meeting ends. Only
	// closing runningDone reports the whole round (extraction, projection,
	// audit scheduling and tree reorganization) as complete.
	service.mu.Lock()
	state := service.sessionStateLocked(sessionID)
	state.running = true
	state.runningDone = make(chan struct{})
	inFlight := state.runningDone
	service.mu.Unlock()

	finalized := make(chan error, 1)
	go func() {
		finalized <- service.FinalizeMeetingSession(
			context.Background(), domain.MeetingSession{ID: sessionID},
			MeetingSessionFinalizationRequest{TranscriptQueueDrained: true},
		)
	}()

	waitForInternal(t, 5*time.Second, func() bool {
		analysis, payload := finalizationProgressOf(t, repository, sessionID)
		return analysis != nil && (payload.LiveWaitTimedOut || payload.WaitTimedOut ||
			analysis.Status == domain.MeetingAIAnalysisFailed)
	})
	// The sealed round finishes just after the deadline, exactly as the
	// production tree_reorganizer did.
	service.mu.Lock()
	state = service.sessionStateLocked(sessionID)
	state.running = false
	if state.runningDone == inFlight {
		state.runningDone = nil
	}
	service.mu.Unlock()
	close(inFlight)

	select {
	case <-finalized:
	case <-time.After(10 * time.Second):
		t.Fatal("finalization never returned")
	}

	summary, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisFinal)
	if err != nil || summary == nil || summary.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("final summary=%+v error=%v, want a completed summary after the late live round", summary, err)
	}
	if calls := completer.callsFor(barrierSummaryDeployment); calls != 1 {
		t.Fatalf("final summary provider calls=%d, want exactly one", calls)
	}
	analysis, payload := finalizationProgressOf(t, repository, sessionID)
	if analysis == nil {
		t.Fatal("finalization progress row missing")
	}
	if payload.FinalizationStatus == finalizationStatusWaitingForLiveAnalysis {
		t.Fatalf("finalization stayed in %s; a delayed live round must not strand it", payload.FinalizationStatus)
	}
	if payload.Stage != "completed" {
		t.Fatalf("finalization stage=%q status=%s, want the pipeline to reach completed", payload.Stage, payload.FinalizationStatus)
	}
}

// TestFinalizationFallsBackWhenLiveRoundNeverCompletes covers §17.2: the
// in-flight round is never released, so finalization must supersede it, fall
// back to the latest fully projected persisted snapshot and still reach a
// terminal state instead of running forever.
func TestFinalizationFallsBackWhenLiveRoundNeverCompletes(t *testing.T) {
	const sessionID = "session-barrier-stuck-live-round"
	repository := newContextBarrierRepository()
	seedStableLiveSnapshot(t, repository, sessionID)
	completer := newFinalizationBarrierCompleter()
	service := NewMeetingAnalysisService(
		repository, barrierTranscripts(sessionID), nil, completer, finalizationBarrierConfig(),
	)

	service.mu.Lock()
	state := service.sessionStateLocked(sessionID)
	state.running = true
	state.runningDone = make(chan struct{})
	service.mu.Unlock()

	finalized := make(chan error, 1)
	go func() {
		finalized <- service.FinalizeMeetingSession(
			context.Background(), domain.MeetingSession{ID: sessionID},
			MeetingSessionFinalizationRequest{TranscriptQueueDrained: true},
		)
	}()
	select {
	case <-finalized:
	case <-time.After(10 * time.Second):
		t.Fatal("finalization never returned while the live round stayed in flight")
	}

	summary, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisFinal)
	if err != nil || summary == nil || summary.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("final summary=%+v error=%v, want the latest stable snapshot to produce a summary", summary, err)
	}
	snapshot, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisTree)
	if err != nil || snapshot == nil {
		t.Fatalf("final tree snapshot=%+v error=%v, want the stable snapshot persisted", snapshot, err)
	}
	analysis, payload := finalizationProgressOf(t, repository, sessionID)
	if analysis == nil || analysis.Status == domain.MeetingAIAnalysisRunning {
		t.Fatalf("finalization progress=%+v, want a terminal state", analysis)
	}
	if !payload.LiveWaitTimedOut {
		t.Fatalf("progress payload=%+v, want the awaited live round recorded as timed out", payload)
	}
	if payload.SourceTreeVersion != 12 {
		t.Fatalf("sourceTreeVersion=%d, want the latest fully projected persisted snapshot (12)", payload.SourceTreeVersion)
	}
	if len(payload.WaitingOperations) == 0 {
		t.Fatalf("progress payload=%+v, want the awaited operation identified", payload)
	}
}

// TestSupersededLiveRoundDoesNotRewindFinalizedTree covers §17.3: a real live
// round is still calling the provider when the barrier gives up. Once it
// finally returns, its result must be discarded instead of overwriting the
// snapshot finalization already used, and no second summary may be generated.
func TestSupersededLiveRoundDoesNotRewindFinalizedTree(t *testing.T) {
	const sessionID = "session-barrier-stale-round"
	repository := newContextBarrierRepository()
	seedStableLiveSnapshot(t, repository, sessionID)
	completer := newFinalizationBarrierCompleter()
	completer.liveStarted = make(chan struct{})
	completer.liveRelease = make(chan struct{})
	service := NewMeetingAnalysisService(
		repository, barrierTranscripts(sessionID), nil, completer, finalizationBarrierConfig(),
	)

	segments := []domain.TranscriptSegment{{
		SessionID: sessionID, SequenceNo: 11, IsFinal: true,
		Text: "追加で監視間隔の見直しについても検討が必要です。",
	}}
	service.mu.Lock()
	state := service.sessionStateLocked(sessionID)
	state.lastPayload = append(json.RawMessage(nil), mustLiveAnalysisPayload(t, repository, sessionID)...)
	state.lastVersion = 12
	state.versionSeeded = true
	beginLiveRunLocked(state, liveAnalysisTriggerFinalTranscript, 11, 11)
	service.mu.Unlock()
	liveDone := make(chan struct{})
	go func() {
		defer close(liveDone)
		service.runLiveAnalysis(context.Background(), sessionID, segments)
	}()
	select {
	case <-completer.liveStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("live round never called the provider")
	}

	// The superseded round leaves a coverage gap, which the caller reports on
	// the session. The pipeline itself must still finish.
	if err := service.FinalizeMeetingSession(
		context.Background(), domain.MeetingSession{ID: sessionID},
		MeetingSessionFinalizationRequest{TranscriptQueueDrained: true},
	); err != nil && !strings.Contains(err.Error(), finalizationErrorCodeLiveWaitTimeout) {
		t.Fatalf("finalization failed while a live round was in flight: %v", err)
	}
	finalizedLive, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisLive)
	if err != nil || finalizedLive == nil {
		t.Fatalf("live row missing after finalization: %+v error=%v", finalizedLive, err)
	}
	finalizedVersion := finalizedLive.Version

	close(completer.liveRelease)
	select {
	case <-liveDone:
	case <-time.After(10 * time.Second):
		t.Fatal("superseded live round never returned")
	}

	afterLive, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisLive)
	if err != nil || afterLive == nil {
		t.Fatalf("live row missing after the superseded round: %+v error=%v", afterLive, err)
	}
	if afterLive.Version != finalizedVersion {
		t.Fatalf("live version=%d after the superseded round, want the finalized %d preserved", afterLive.Version, finalizedVersion)
	}
	if calls := completer.callsFor(barrierSummaryDeployment); calls != 1 {
		t.Fatalf("final summary provider calls=%d, want exactly one", calls)
	}
	analysis, payload := finalizationProgressOf(t, repository, sessionID)
	if analysis == nil || payload.FinalizationStatus != finalizationStatusCompleted {
		t.Fatalf("finalization=%+v payload=%+v, want completed and unchanged by the late round", analysis, payload)
	}
}

func mustLiveAnalysisPayload(t *testing.T, repository *contextBarrierRepository, sessionID string) json.RawMessage {
	t.Helper()
	analysis, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisLive)
	if err != nil || analysis == nil {
		t.Fatalf("seeded live analysis missing: %v", err)
	}
	return analysis.Payload
}

// TestFinalizationRetryCompletesEndedIncompleteSession covers §17.4.
func TestFinalizationRetryCompletesEndedIncompleteSession(t *testing.T) {
	const sessionID = "session-barrier-retry"
	repository := newContextBarrierRepository()
	seedStableLiveSnapshot(t, repository, sessionID)
	completer := newFinalizationBarrierCompleter()
	service := NewMeetingAnalysisService(
		repository, barrierTranscripts(sessionID), nil, completer, finalizationBarrierConfig(),
	)
	persistFailedFinalization(t, service, repository, sessionID)

	if err := service.RetryMeetingSessionFinalization(context.Background(), sessionID); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	summary, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisFinal)
	if err != nil || summary == nil || summary.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("final summary=%+v error=%v after retry", summary, err)
	}
	_, payload := finalizationProgressOf(t, repository, sessionID)
	if payload.FinalizationStatus != finalizationStatusCompleted || payload.FinalizationIncomplete {
		t.Fatalf("progress=%+v, want completed and no longer incomplete", payload)
	}
	if payload.AttemptCount < 2 {
		t.Fatalf("attemptCount=%d, want the retry attempt counted", payload.AttemptCount)
	}

	// A second retry on a completed finalization must not start a new attempt.
	if err := service.RetryMeetingSessionFinalization(context.Background(), sessionID); !errors.Is(err, ErrMeetingFinalizationAlreadyCompleted) {
		t.Fatalf("second retry error=%v, want ErrMeetingFinalizationAlreadyCompleted", err)
	}
	if calls := completer.callsFor(barrierSummaryDeployment); calls != 1 {
		t.Fatalf("final summary provider calls=%d, want exactly one across both retries", calls)
	}
}

// TestConcurrentFinalizationRetryIsSingleFlight covers §17.5.
func TestConcurrentFinalizationRetryIsSingleFlight(t *testing.T) {
	const sessionID = "session-barrier-concurrent-retry"
	repository := newContextBarrierRepository()
	seedStableLiveSnapshot(t, repository, sessionID)
	completer := newFinalizationBarrierCompleter()
	service := NewMeetingAnalysisService(
		repository, barrierTranscripts(sessionID), nil, completer, finalizationBarrierConfig(),
	)
	persistFailedFinalization(t, service, repository, sessionID)

	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = service.RetryMeetingSessionFinalization(context.Background(), sessionID)
		}()
	}
	group.Wait()

	if calls := completer.callsFor(barrierSummaryDeployment); calls != 1 {
		t.Fatalf("final summary provider calls=%d, want exactly one under concurrent retries", calls)
	}
	summary, err := repository.GetMeetingAIAnalysis(context.Background(), sessionID, domain.MeetingAIAnalysisFinal)
	if err != nil || summary == nil || summary.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("final summary=%+v error=%v", summary, err)
	}
}

// TestStaleRunningFinalizationIsReportedRetryable covers §17.6: a process
// restart leaves a durable running finalization row that no goroutine owns.
// Reading the session must not present it as still generating.
func TestStaleRunningFinalizationIsReportedRetryable(t *testing.T) {
	const sessionID = "session-barrier-restart"
	repository := newContextBarrierRepository()
	seedStableLiveSnapshot(t, repository, sessionID)
	now := time.Date(2026, 7, 26, 5, 0, 0, 0, time.UTC)
	stale, err := json.Marshal(finalizationProgressPayload{
		FinalizationID: "finalization-restart", Stage: "final_summary_running",
		FinalizationStatus: finalizationStatusGeneratingSummary, AttemptCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{
		SessionID: sessionID, Type: domain.MeetingAIAnalysisFinalization,
		Status: domain.MeetingAIAnalysisRunning, Version: 1, Payload: stale,
		UpdatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	service := NewMeetingAnalysisService(
		repository, barrierTranscripts(sessionID), nil, newFinalizationBarrierCompleter(),
		finalizationBarrierConfig(),
	)
	service.now = func() time.Time { return now }

	snapshot, err := service.GetMeetingAIAnalyses(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Finalization == nil {
		t.Fatal("finalization row missing from snapshot")
	}
	if snapshot.Finalization.Status != domain.MeetingAIAnalysisFailed {
		t.Fatalf("finalization status=%s, want an abandoned attempt reported as failed", snapshot.Finalization.Status)
	}
	var payload finalizationProgressPayload
	if err := json.Unmarshal(snapshot.Finalization.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Retryable || !payload.FinalizationIncomplete {
		t.Fatalf("payload=%+v, want retryable and incomplete", payload)
	}
	if payload.FinalizationStatus != finalizationStatusFailed {
		t.Fatalf("finalizationStatus=%s, want failed", payload.FinalizationStatus)
	}
	if !strings.Contains(payload.FinalizationErrorCode, "abandoned") {
		t.Fatalf("errorCode=%q, want the abandoned attempt named", payload.FinalizationErrorCode)
	}
}

// persistFailedFinalization drives one finalization whose in-flight live round
// never completes and whose summary provider is unavailable, leaving exactly
// the durable "ended, incomplete, no final summary" state the retry endpoint
// must recover from.
func persistFailedFinalization(t *testing.T, service *MeetingAnalysisService, repository *contextBarrierRepository, sessionID string) {
	t.Helper()
	failed, err := json.Marshal(finalizationProgressPayload{
		FinalizationID: "finalization-previous", Stage: "final_flush_failed",
		FinalizationStatus: finalizationStatusFailed, FinalizationIncomplete: true,
		Retryable: true, AttemptCount: 1,
		FinalizationErrorCode: finalizationErrorCodeLiveWaitTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{
		SessionID: sessionID, Type: domain.MeetingAIAnalysisFinalization,
		Status: domain.MeetingAIAnalysisFailed, Version: 1, Payload: failed,
		LastError: "wait for in-flight live analysis: context deadline exceeded",
		UpdatedAt: time.Date(2026, 7, 26, 4, 30, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	state := service.sessionStateLocked(sessionID)
	state.stopped = true
	state.finalizing = false
	service.mu.Unlock()
}
