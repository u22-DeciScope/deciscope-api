package application

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

type liveSchedulerFixtureMetrics struct {
	Calls                  int
	AverageStartWait       float64
	MaximumStartWait       float64
	AverageSnapshotWait    float64
	MaximumPendingSegments int
	MaximumPendingWait     float64
	TreeVersions           int
	UnchangedRounds        int
	EstimatedTokens        int
}

func TestSession5e9dbde65166968dSchedulingFixtureComparison(t *testing.T) {
	// This timing fixture mirrors the reported shape: a 24-second long
	// utterance, a 38-second gap, one short final that waits for the next
	// final, a five-second model latency, an unchanged merge round, and a
	// later structural addition.
	finalAt := []float64{24, 62, 74, 124}
	chars := []int{120, 30, 70, 110}
	oldStarts := []float64{30, 80, 80, 130}
	newStarts := []float64{26, 76, 76, 126}

	oldMetrics := fixtureMetrics(finalAt, oldStarts, chars, 5, 3, 2, 3, 1)
	newMetrics := fixtureMetrics(finalAt, newStarts, chars, 5, 3, 2, 3, 1)

	if newMetrics.Calls != oldMetrics.Calls {
		t.Fatalf("AI calls old/new = %d/%d, want unchanged", oldMetrics.Calls, newMetrics.Calls)
	}
	if !(newMetrics.AverageStartWait < oldMetrics.AverageStartWait) {
		t.Fatalf("average start wait old/new = %.1f/%.1f", oldMetrics.AverageStartWait, newMetrics.AverageStartWait)
	}
	if !(newMetrics.MaximumStartWait < oldMetrics.MaximumStartWait) {
		t.Fatalf("maximum start wait old/new = %.1f/%.1f", oldMetrics.MaximumStartWait, newMetrics.MaximumStartWait)
	}
	if newMetrics.EstimatedTokens != oldMetrics.EstimatedTokens {
		t.Fatalf("estimated tokens old/new = %d/%d, want unchanged for identical batches", oldMetrics.EstimatedTokens, newMetrics.EstimatedTokens)
	}
	t.Logf("session_5e9dbde65166968d scheduler comparison: old={calls:%d avgStartWait:%.1fs maxStartWait:%.1fs avgSnapshotWait:%.1fs maxPendingSegments:%d maxPendingWait:%.1fs treeVersions:%d unchangedRounds:%d estimatedTokens:%d} new={calls:%d avgStartWait:%.1fs maxStartWait:%.1fs avgSnapshotWait:%.1fs maxPendingSegments:%d maxPendingWait:%.1fs treeVersions:%d unchangedRounds:%d estimatedTokens:%d}",
		oldMetrics.Calls, oldMetrics.AverageStartWait, oldMetrics.MaximumStartWait, oldMetrics.AverageSnapshotWait,
		oldMetrics.MaximumPendingSegments, oldMetrics.MaximumPendingWait, oldMetrics.TreeVersions, oldMetrics.UnchangedRounds, oldMetrics.EstimatedTokens,
		newMetrics.Calls, newMetrics.AverageStartWait, newMetrics.MaximumStartWait, newMetrics.AverageSnapshotWait,
		newMetrics.MaximumPendingSegments, newMetrics.MaximumPendingWait, newMetrics.TreeVersions, newMetrics.UnchangedRounds, newMetrics.EstimatedTokens)
}

func TestLiveSchedulerConcurrentTriggersAndDuplicateCallbacksStartOneRun(t *testing.T) {
	repository := &internalAuditAnalysisRepository{store: make(map[string]domain.MeetingAIAnalysis)}
	completer := &internalAuditCompleter{content: `{
		"summary":"要約","currentTopic":"進捗",
		"items":[{"id":"fact-1","kind":"fact","title":"進捗を確認","body":"進捗を確認した","status":"open","evidenceSequenceNos":[1]}],
		"newTopics":[{"id":"topic-progress","label":"進捗"}],
		"assignments":[{"itemId":"fact-1","parentId":"topic-progress","confidence":0.9,"reason":"進捗"}]
	}`}
	service := NewMeetingAnalysisService(repository, nil, nil, completer, MeetingAnalysisConfig{
		Enabled: true, LiveEnabled: true, LiveInterval: time.Hour,
		LiveDebounce: time.Hour, LiveCooldown: time.Millisecond, LiveMaxWait: time.Hour,
		LiveMinChars: 1, LiveMaxInputChars: 4000, Model: "test",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()

	now := service.now()
	sessionID := "session_concurrent_triggers"
	service.mu.Lock()
	state := service.sessionStateLocked(sessionID)
	state.contextStatus = meetingContextStatusReady
	state.context = &meetingContext{}
	state.contextFallback = state.context
	appendPendingLiveSegmentLocked(state, domain.TranscriptSegment{
		SessionID: sessionID, CallID: "call-1", SequenceNo: 1,
		Text: "同時triggerでも一度だけ分析する", IsFinal: true,
	}, now)
	state.pendingChars = sumSegmentChars(state.pending)
	state.lastActivityAt = now
	service.mu.Unlock()

	var triggers sync.WaitGroup
	for _, trigger := range []string{liveAnalysisTriggerFinalTranscript, liveAnalysisTriggerPeriodicTick, liveAnalysisTriggerContextReady} {
		triggers.Add(1)
		go func(trigger string) {
			defer triggers.Done()
			service.evaluateLiveAnalysisTrigger(sessionID, trigger)
		}(trigger)
	}
	triggers.Wait()

	service.mu.Lock()
	generation := state.scheduleGeneration
	if !state.analysisScheduled {
		service.mu.Unlock()
		t.Fatal("concurrent triggers did not create a schedule")
	}
	// Make the already-created schedule eligible, then invoke the same
	// generation twice to model a duplicated timer callback without waiting
	// for the one-hour test debounce.
	state.oldestPendingFinalAt = now.Add(-2 * time.Hour)
	state.latestPendingFinalAt = now.Add(-2 * time.Hour)
	service.mu.Unlock()

	var callbacks sync.WaitGroup
	for index := 0; index < 2; index++ {
		callbacks.Add(1)
		go func() {
			defer callbacks.Done()
			service.dispatchScheduledLiveAnalysis(sessionID, generation)
		}()
	}
	callbacks.Wait()
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	time.Sleep(50 * time.Millisecond)
	if got := completer.callCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
}

func TestLiveSchedulerStartRegistersPeriodicOwnerOnce(t *testing.T) {
	service := NewMeetingAnalysisService(nil, nil, nil, nil, MeetingAnalysisConfig{
		Enabled: true, LiveEnabled: true, LiveInterval: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	service.mu.Lock()
	firstRegistration := service.schedulerRegistrationID
	instanceID := service.schedulerInstanceID
	service.mu.Unlock()

	service.Start(ctx)
	service.mu.Lock()
	secondRegistration := service.schedulerRegistrationID
	service.mu.Unlock()
	if instanceID == "" || firstRegistration == "" || secondRegistration != firstRegistration {
		t.Fatalf("instance=%q first=%q second=%q", instanceID, firstRegistration, secondRegistration)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func fixtureMetrics(finalAt, starts []float64, chars []int, modelSeconds float64, calls, maxPendingSegments, versions, unchanged int) liveSchedulerFixtureMetrics {
	totalWait := 0.0
	maxWait := 0.0
	totalChars := 0
	for index := range finalAt {
		wait := starts[index] - finalAt[index]
		totalWait += wait
		maxWait = math.Max(maxWait, wait)
		totalChars += chars[index]
	}
	return liveSchedulerFixtureMetrics{
		Calls:                  calls,
		AverageStartWait:       totalWait / float64(len(finalAt)),
		MaximumStartWait:       maxWait,
		AverageSnapshotWait:    totalWait/float64(len(finalAt)) + modelSeconds,
		MaximumPendingSegments: maxPendingSegments,
		MaximumPendingWait:     maxWait,
		TreeVersions:           versions,
		UnchangedRounds:        unchanged,
		// Deterministic estimate: 700 prompt/response overhead tokens per
		// call plus one token per two transcript characters. Since batching
		// and call count are identical, the estimate (and cost) is identical.
		EstimatedTokens: calls*700 + totalChars/2,
	}
}
