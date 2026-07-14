package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

const (
	liveAnalysisResultJSON   = `{"summary":"要約です","currentTopic":"進捗確認","items":[{"id":"issue-progress","kind":"issue","severity":"medium","title":"進捗遅れ","body":"タスクAが1週間遅延している。","status":"open"}],"tree":{"nodes":[{"id":"topic-progress","kind":"topic","label":"進捗確認"},{"id":"issue-progress","kind":"issue","label":"進捗遅れ"}],"edges":[{"source":"topic-progress","target":"issue-progress"}]}}`
	finalAnalysisResultJSON  = `{"suggestedTitle":"週次定例","overview":"概要です","decisions":[],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`
	contextPlannerResultJSON = `{"purpose":"品質確認","agendaItems":[{"title":"文字起こし精度","order":1,"role":"primary"},{"title":"AI分析の制御","order":2,"role":"primary"},{"title":"進行中作業の横断一覧","order":3,"role":"action_summary"}],"aiDirectives":[]}`
)

func TestMeetingAnalysisServiceIgnoresPartialAndEmptySegments(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	completer := &fakeAIChatCompleter{}
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, completer,
		testLiveOnlyConfig(10*time.Millisecond, 1),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()

	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", Text: "確定していない発言", IsFinal: false})
	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", Text: "   ", IsFinal: true})
	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "", Text: "セッションIDがない発言", IsFinal: true})

	time.Sleep(80 * time.Millisecond)
	if got := completer.callCount(); got != 0 {
		t.Fatalf("callCount() = %d, want 0", got)
	}
	if got := repository.upsertCount(); got != 0 {
		t.Fatalf("upsertCount() = %d, want 0", got)
	}
}

func TestMeetingAnalysisServiceSkipsBelowMinChars(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	completer := &fakeAIChatCompleter{}
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, completer,
		testLiveOnlyConfig(10*time.Millisecond, 100),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()

	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", Text: "短い発言", IsFinal: true})

	time.Sleep(80 * time.Millisecond)
	if got := completer.callCount(); got != 0 {
		t.Fatalf("callCount() = %d, want 0 (below min chars)", got)
	}
}

func TestMeetingAnalysisServiceRunsLiveAnalysisAndPublishes(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	publisher := &fakeAIAnalysisPublisher{}
	completer := &fakeAIChatCompleter{results: []application.AIChatResult{{Content: liveAnalysisResultJSON, PromptTokens: 10, CompletionTokens: 5}}}
	sessionRepo := &fakeAnalysisSessionRepository{session: &domain.MeetingSession{ID: "session_1", Purpose: "意思決定"}}
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, sessionRepo, completer,
		testLiveOnlyConfig(10*time.Millisecond, 1),
		publisher,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()

	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", SpeakerName: "田中さん", Text: "本日の議題は価格改定です。", IsFinal: true})

	waitUntil(t, 2*time.Second, func() bool { return completer.callCount() >= 1 })
	waitUntil(t, 2*time.Second, func() bool {
		_, err := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisLive)
		return err == nil
	})

	saved := repository.lastUpsert()
	if saved.Status != domain.MeetingAIAnalysisCompleted || saved.Version != 1 || saved.Type != domain.MeetingAIAnalysisLive {
		t.Fatalf("saved analysis = %+v", saved)
	}
	if len(saved.Payload) == 0 {
		t.Fatalf("saved payload is empty")
	}

	waitUntil(t, time.Second, func() bool { return len(publisher.snapshot()) >= 2 })
	published := publisher.snapshot()
	// The ephemeral running notification is broadcast right before the Azure
	// call and must never be written to the database.
	if published[0].Status != domain.MeetingAIAnalysisRunning || published[0].Version != 0 {
		t.Fatalf("first published = %+v, want ephemeral running event with previous version", published[0])
	}
	if published[len(published)-1].Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("published = %+v", published)
	}
	for _, upsert := range repository.upsertSnapshot() {
		if upsert.Type == domain.MeetingAIAnalysisLive && upsert.Status == domain.MeetingAIAnalysisRunning {
			t.Fatalf("upserts = %+v, live running state must not be persisted", repository.upsertSnapshot())
		}
	}

	if len(completer.requestSnapshot()) == 0 {
		t.Fatal("completer should have received a request")
	}
	var request *application.AIChatRequest
	requests := completer.requestSnapshot()
	for i := range requests {
		if requests[i].ResponseSchema != nil && requests[i].ResponseSchema.Name == "live_analysis_diff" {
			request = &requests[i]
			break
		}
	}
	if request == nil {
		t.Fatalf("requests = %+v, want strict live_analysis_diff request", requests)
	}
	if request.System == "" {
		t.Fatal("system prompt should not be empty")
	}
	if !request.ResponseSchema.Strict {
		t.Fatalf("response schema = %+v, want strict live_analysis_diff", request.ResponseSchema)
	}
	if !strings.Contains(string(request.ResponseSchema.Schema), `"evidenceSequenceNos"`) || !strings.Contains(string(request.ResponseSchema.Schema), `"type": "integer"`) {
		t.Fatalf("response schema does not constrain evidenceSequenceNos to integers: %s", request.ResponseSchema.Schema)
	}
}

func TestMeetingAnalysisServicePublishesIntervalSecondsOnLiveEvents(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	publisher := &fakeAIAnalysisPublisher{}
	completer := &fakeAIChatCompleter{results: []application.AIChatResult{{Content: liveAnalysisResultJSON}}}
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, completer,
		testLiveOnlyConfig(time.Second, 1),
		publisher,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()

	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", Text: "十分に長い発言です。", IsFinal: true})

	waitUntil(t, 4*time.Second, func() bool { return len(publisher.snapshot()) >= 2 })
	for _, analysis := range publisher.snapshot() {
		if analysis.IntervalSeconds != 1 {
			t.Fatalf("published analysis = %+v, want intervalSeconds 1 on every live event", analysis)
		}
	}
}

func TestMeetingAnalysisServiceSnapshotCarriesLiveIntervalSeconds(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, &fakeAIChatCompleter{},
		testLiveOnlyConfig(10*time.Second, 1),
	)

	snapshot, err := service.GetMeetingAIAnalyses(context.Background(), "session_1")
	if err != nil {
		t.Fatalf("GetMeetingAIAnalyses() error = %v", err)
	}
	if snapshot.LiveIntervalSeconds != 10 {
		t.Fatalf("LiveIntervalSeconds = %d, want 10", snapshot.LiveIntervalSeconds)
	}

	disabled := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, &fakeAIChatCompleter{},
		application.MeetingAnalysisConfig{Enabled: false, LiveEnabled: true, LiveInterval: 10 * time.Second},
	)
	disabledSnapshot, err := disabled.GetMeetingAIAnalyses(context.Background(), "session_1")
	if err != nil {
		t.Fatalf("GetMeetingAIAnalyses(disabled) error = %v", err)
	}
	if disabledSnapshot.LiveIntervalSeconds != 0 {
		t.Fatalf("disabled LiveIntervalSeconds = %d, want 0", disabledSnapshot.LiveIntervalSeconds)
	}
}

func TestMeetingAnalysisServiceSkipsSchedulingWhileRunning(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	completer := &fakeAIChatCompleter{
		block:   make(chan struct{}),
		results: []application.AIChatResult{{Content: liveAnalysisResultJSON}, {Content: liveAnalysisResultJSON}},
	}
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, completer,
		testLiveOnlyConfig(15*time.Millisecond, 5),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()

	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", Text: "最初のまとまった発言です。", IsFinal: true})
	waitUntil(t, time.Second, func() bool { return completer.callCount() >= 1 })

	// While the first call is blocked (still "running"), enough new text
	// arrives to clear the min-chars gate again; the scheduler must not
	// start a second job until the first one finishes.
	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", Text: "実行中に届いた2番目の発言です。", IsFinal: true})
	time.Sleep(80 * time.Millisecond)
	if got := completer.callCount(); got != 1 {
		t.Fatalf("callCount() while running = %d, want 1", got)
	}

	close(completer.block)
	waitUntil(t, 2*time.Second, func() bool { return completer.callCount() >= 2 })
}

func TestMeetingAnalysisServiceBackoffAndPendingRestoreOnFailure(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	completer := &fakeAIChatCompleter{
		errs:    []error{errors.New("azure openai unavailable")},
		results: []application.AIChatResult{{}, {Content: liveAnalysisResultJSON}},
	}
	config := testLiveOnlyConfig(15*time.Millisecond, 1)
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, completer, config,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()

	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", Text: "失敗する最初の分析対象の発言です。", IsFinal: true})

	waitUntil(t, 2*time.Second, func() bool { return completer.callCount() >= 1 })
	waitUntil(t, time.Second, func() bool {
		last := repository.lastUpsert()
		return last.Status == domain.MeetingAIAnalysisFailed
	})
	failed := repository.lastUpsert()
	if failed.LastError == "" {
		t.Fatalf("failed analysis = %+v, want lastError set", failed)
	}
	if len(failed.Payload) != 0 {
		t.Fatalf("failed analysis payload = %s, want empty (no previous success)", string(failed.Payload))
	}

	// The failed segment must be retried (pending restored) once the
	// exponential backoff window elapses, without any new segment arriving.
	waitUntil(t, 3*time.Second, func() bool { return completer.callCount() >= 2 })
	waitUntil(t, time.Second, func() bool {
		last := repository.lastUpsert()
		return last.Status == domain.MeetingAIAnalysisCompleted
	})
	completedAnalysis := repository.lastUpsert()
	if completedAnalysis.Version != 1 {
		t.Fatalf("completed analysis = %+v, want version 1", completedAnalysis)
	}
}

func TestMeetingAnalysisServiceDoesNotRetrySchemaFailureWithoutNewTranscript(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	completer := &fakeAIChatCompleter{results: []application.AIChatResult{{Content: `not json`}}}
	config := testLiveOnlyConfig(15*time.Millisecond, 1)
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, completer, config,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()

	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", SequenceNo: 1, Text: "schema不一致になる発言", IsFinal: true})
	waitUntil(t, 2*time.Second, func() bool { return completer.callCount() >= 1 })
	waitUntil(t, time.Second, func() bool { return repository.lastUpsert().Status == domain.MeetingAIAnalysisFailed })
	time.Sleep(100 * time.Millisecond)
	if got := completer.callCount(); got != 1 {
		t.Fatalf("callCount() after deterministic schema failure = %d, want 1", got)
	}

	// New information changes the prompt and deliberately unblocks one new
	// attempt; the original pending segment remains included.
	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", SequenceNo: 2, Text: "新しい発言", IsFinal: true})
	waitUntil(t, 2*time.Second, func() bool { return completer.callCount() >= 2 })
}

func TestMeetingAnalysisServiceNotifyEndedGeneratesFinalSummary(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	publisher := &fakeAIAnalysisPublisher{}
	completer := &fakeAIChatCompleter{results: []application.AIChatResult{{Content: finalAnalysisResultJSON}}}
	transcriptRepo := &fakeAnalysisTranscriptRepository{segments: []domain.TranscriptSegment{
		{SpeakerName: "田中さん", Text: "議論を始めましょう。"},
		{SpeakerName: "佐藤さん", Text: "賛成です。"},
	}}
	sessionRepo := &fakeAnalysisSessionRepository{session: &domain.MeetingSession{ID: "session_1", Agenda: "価格改定"}}
	config := testFinalOnlyConfig()
	service := application.NewMeetingAnalysisService(repository, transcriptRepo, sessionRepo, completer, config, publisher)

	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{TranscriptQueueDrained: true})

	waitUntil(t, 2*time.Second, func() bool {
		saved, err := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisFinal)
		return err == nil && saved.Status == domain.MeetingAIAnalysisCompleted
	})
	saved, _ := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisFinal)
	if saved.Status != domain.MeetingAIAnalysisCompleted || saved.Type != domain.MeetingAIAnalysisFinal || saved.Version != 1 {
		t.Fatalf("saved final analysis = %+v", saved)
	}

	upserts := repository.upsertSnapshot()
	if upserts[0].Status != domain.MeetingAIAnalysisRunning {
		t.Fatalf("first upsert = %+v, want running status persisted before the AI call", upserts[0])
	}

	if len(completer.requestSnapshot()) != 2 {
		t.Fatalf("completer calls = %d, want context planner + final summary", len(completer.requestSnapshot()))
	}
}

// TestMeetingAnalysisServiceNotifyEndedConcurrentCallsGenerateFinalSummaryOnce
// reproduces the TOCTOU race where two MeetingSessionEnded notifications for
// the same session fire almost simultaneously (e.g. a bot "ended" status
// PATCH and the watchdog ending the session at nearly the same time). Each
// notification launches generateFinalSummary in its own goroutine; without
// the in-flight guard, both goroutines could pass the existing-analysis DB
// check before either persisted the "running" row, producing two final
// summaries. A gated completer blocks on the first Complete call so the test
// can deterministically observe a call in flight before releasing it,
// creating the race window regardless of goroutine scheduling.
func TestMeetingAnalysisServiceNotifyEndedConcurrentCallsGenerateFinalSummaryOnce(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	completer := newGatedAIChatCompleter(application.AIChatResult{Content: finalAnalysisResultJSON})
	transcriptRepo := &fakeAnalysisTranscriptRepository{segments: []domain.TranscriptSegment{
		{SpeakerName: "田中さん", Text: "議論を始めましょう。"},
		{SpeakerName: "佐藤さん", Text: "賛成です。"},
	}}
	config := testFinalOnlyConfig()
	service := application.NewMeetingAnalysisService(repository, transcriptRepo, &fakeAnalysisSessionRepository{}, completer, config)

	// Simulate the two near-simultaneous MeetingSessionEnded notifications;
	// each call launches its own generateFinalSummary goroutine.
	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{TranscriptQueueDrained: true})
	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{TranscriptQueueDrained: true})

	// At this point, if the in-flight guard is missing, both goroutines could
	// have reached the completer and be blocked there; if it is present, only
	// the first one could have.
	waitUntil(t, 2*time.Second, func() bool { return completer.startedCount() >= 1 })

	completer.release()

	waitUntil(t, 2*time.Second, func() bool {
		last, err := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisFinal)
		return err == nil && last.Status == domain.MeetingAIAnalysisCompleted
	})

	// Give a (incorrectly) unguarded second goroutine time to reach the
	// completer too, so the assertions below are not just checking the first
	// completion to land.
	time.Sleep(150 * time.Millisecond)

	if got := completer.callCount(); got != 1 {
		t.Fatalf("completer callCount() = %d, want 1 (final summary must be generated only once for concurrent NotifyMeetingSessionEnded calls)", got)
	}
	completedFinalUpserts := 0
	for _, upsert := range repository.upsertSnapshot() {
		if upsert.Type == domain.MeetingAIAnalysisFinal && upsert.Status == domain.MeetingAIAnalysisCompleted {
			completedFinalUpserts++
		}
	}
	if completedFinalUpserts != 1 {
		t.Fatalf("completed final upserts = %d, want 1", completedFinalUpserts)
	}
}

func TestMeetingAnalysisServiceNotifyEndedSkipsWhenFinalAlreadyExists(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	repository.seed(domain.MeetingAIAnalysis{
		SessionID: "session_1",
		Type:      domain.MeetingAIAnalysisFinal,
		Status:    domain.MeetingAIAnalysisCompleted,
		Version:   1,
	})
	completer := &fakeAIChatCompleter{}
	transcriptRepo := &fakeAnalysisTranscriptRepository{segments: []domain.TranscriptSegment{{Text: "発言"}}}
	config := testFinalOnlyConfig()
	service := application.NewMeetingAnalysisService(repository, transcriptRepo, &fakeAnalysisSessionRepository{}, completer, config)

	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{TranscriptQueueDrained: true})

	time.Sleep(100 * time.Millisecond)
	if got := completer.callCount(); got != 0 {
		t.Fatalf("callCount() = %d, want 0 (final summary already exists)", got)
	}
	if got := repository.upsertCount(); got != 0 {
		t.Fatalf("upsertCount() = %d, want 0", got)
	}
}

func TestMeetingAnalysisServiceNotifyEndedSkipsWhenTranscriptEmpty(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	completer := &fakeAIChatCompleter{}
	config := testFinalOnlyConfig()
	service := application.NewMeetingAnalysisService(repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, completer, config)

	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{TranscriptQueueDrained: true})

	time.Sleep(100 * time.Millisecond)
	if got := completer.callCount(); got != 0 {
		t.Fatalf("callCount() = %d, want 0 (empty transcript)", got)
	}
	if progress, err := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisFinalization); err != nil || progress.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("finalization progress = %+v, err=%v, want completed", progress, err)
	}
}

func TestMeetingAnalysisServiceDisabledConfigIsNoOp(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	completer := &fakeAIChatCompleter{}
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{segments: []domain.TranscriptSegment{{Text: "発言"}}}, &fakeAnalysisSessionRepository{}, completer,
		application.MeetingAnalysisConfig{Enabled: false, LiveEnabled: true, FinalEnabled: true, LiveInterval: 10 * time.Millisecond, LiveMinChars: 1},
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()

	service.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", Text: "十分に長い発言です。", IsFinal: true})
	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{TranscriptQueueDrained: true})

	time.Sleep(100 * time.Millisecond)
	if got := completer.callCount(); got != 0 {
		t.Fatalf("callCount() = %d, want 0 when AI is disabled", got)
	}
	if got := repository.upsertCount(); got != 0 {
		t.Fatalf("upsertCount() = %d, want 0 when AI is disabled", got)
	}
}

func TestMeetingAnalysisServiceGetMeetingAIAnalysesReturnsNullsWhenMissing(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, &fakeAIChatCompleter{},
		testLiveOnlyConfig(time.Second, 1),
	)

	snapshot, err := service.GetMeetingAIAnalyses(context.Background(), "session_missing")
	if err != nil {
		t.Fatalf("GetMeetingAIAnalyses() error = %v", err)
	}
	if snapshot.Live != nil || snapshot.Final != nil {
		t.Fatalf("snapshot = %+v, want nil live/final", snapshot)
	}
}

func TestMeetingAnalysisServiceGetMeetingAIAnalysesReturnsStoredValues(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	repository.seed(domain.MeetingAIAnalysis{SessionID: "session_1", Type: domain.MeetingAIAnalysisLive, Status: domain.MeetingAIAnalysisCompleted, Version: 2})
	repository.seed(domain.MeetingAIAnalysis{SessionID: "session_1", Type: domain.MeetingAIAnalysisFinal, Status: domain.MeetingAIAnalysisCompleted, Version: 1})
	service := application.NewMeetingAnalysisService(
		repository, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, &fakeAIChatCompleter{},
		testLiveOnlyConfig(time.Second, 1),
	)

	snapshot, err := service.GetMeetingAIAnalyses(context.Background(), "session_1")
	if err != nil {
		t.Fatalf("GetMeetingAIAnalyses() error = %v", err)
	}
	if snapshot.Live == nil || snapshot.Live.Version != 2 || snapshot.Final == nil || snapshot.Final.Version != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func testLiveOnlyConfig(interval time.Duration, minChars int) application.MeetingAnalysisConfig {
	return application.MeetingAnalysisConfig{
		Enabled:           true,
		LiveEnabled:       true,
		LiveInterval:      interval,
		LiveMinChars:      minChars,
		LiveMaxInputChars: 4000,
		Model:             "test-deployment",
	}
}

func testFinalOnlyConfig() application.MeetingAnalysisConfig {
	return application.MeetingAnalysisConfig{
		Enabled:                 true,
		FinalEnabled:            true,
		FinalMaxInputChars:      12000,
		FinalizationQuietPeriod: time.Millisecond,
		Model:                   "test-deployment",
	}
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !condition() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

type fakeAIAnalysisRepository struct {
	mu      sync.Mutex
	store   map[string]domain.MeetingAIAnalysis
	upserts []domain.MeetingAIAnalysis
}

func newFakeAIAnalysisRepository() *fakeAIAnalysisRepository {
	return &fakeAIAnalysisRepository{store: make(map[string]domain.MeetingAIAnalysis)}
}

func aiAnalysisKey(sessionID string, analysisType domain.MeetingAIAnalysisType) string {
	return sessionID + "|" + string(analysisType)
}

func (f *fakeAIAnalysisRepository) seed(analysis domain.MeetingAIAnalysis) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[aiAnalysisKey(analysis.SessionID, analysis.Type)] = analysis
}

func (f *fakeAIAnalysisRepository) UpsertMeetingAIAnalysis(_ context.Context, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, analysis)
	f.store[aiAnalysisKey(analysis.SessionID, analysis.Type)] = analysis
	saved := analysis
	return &saved, nil
}

func (f *fakeAIAnalysisRepository) GetMeetingAIAnalysis(_ context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	analysis, ok := f.store[aiAnalysisKey(sessionID, analysisType)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	saved := analysis
	return &saved, nil
}

func (f *fakeAIAnalysisRepository) upsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.upserts)
}

func (f *fakeAIAnalysisRepository) lastUpsert() domain.MeetingAIAnalysis {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.upserts) == 0 {
		return domain.MeetingAIAnalysis{}
	}
	return f.upserts[len(f.upserts)-1]
}

func (f *fakeAIAnalysisRepository) upsertSnapshot() []domain.MeetingAIAnalysis {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.MeetingAIAnalysis{}, f.upserts...)
}

type fakeAIChatCompleter struct {
	mu       sync.Mutex
	requests []application.AIChatRequest
	results  []application.AIChatResult
	errs     []error
	block    chan struct{}
	calls    int
}

func (f *fakeAIChatCompleter) Complete(ctx context.Context, request application.AIChatRequest) (application.AIChatResult, error) {
	f.mu.Lock()
	index := f.calls
	f.calls++
	f.requests = append(f.requests, request)
	block := f.block
	f.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return application.AIChatResult{}, ctx.Err()
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	var err error
	if index < len(f.errs) {
		err = f.errs[index]
	}
	var result application.AIChatResult
	switch {
	case index < len(f.results):
		result = f.results[index]
	case len(f.results) > 0:
		result = f.results[len(f.results)-1]
	}
	return result, err
}

func (f *fakeAIChatCompleter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeAIChatCompleter) requestSnapshot() []application.AIChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]application.AIChatRequest{}, f.requests...)
}

// gatedAIChatCompleter blocks every Complete call until release() is called,
// and lets a test observe how many calls have started blocking via
// startedCount(). Unlike fakeAIChatCompleter's block channel (which requires
// the test to already know how many calls to expect before it can safely
// close the channel), this counts starts as they happen, so a test can wait
// for "at least one call is in flight" without assuming how many calls will
// arrive -- which is exactly what's needed to reliably create the race
// window for the final-summary in-flight guard.
type gatedAIChatCompleter struct {
	mu      sync.Mutex
	started int
	calls   int
	result  application.AIChatResult

	releaseOnce sync.Once
	releaseCh   chan struct{}
}

func newGatedAIChatCompleter(result application.AIChatResult) *gatedAIChatCompleter {
	return &gatedAIChatCompleter{result: result, releaseCh: make(chan struct{})}
}

func (g *gatedAIChatCompleter) Complete(ctx context.Context, _ application.AIChatRequest) (application.AIChatResult, error) {
	g.mu.Lock()
	g.started++
	g.mu.Unlock()

	select {
	case <-g.releaseCh:
	case <-ctx.Done():
		return application.AIChatResult{}, ctx.Err()
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return g.result, nil
}

func (g *gatedAIChatCompleter) release() {
	g.releaseOnce.Do(func() { close(g.releaseCh) })
}

func (g *gatedAIChatCompleter) startedCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.started
}

func (g *gatedAIChatCompleter) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

type fakeAIAnalysisPublisher struct {
	mu       sync.Mutex
	analyses []domain.MeetingAIAnalysis
}

func (f *fakeAIAnalysisPublisher) PublishMeetingAIAnalysis(analysis domain.MeetingAIAnalysis) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.analyses = append(f.analyses, analysis)
}

func (f *fakeAIAnalysisPublisher) snapshot() []domain.MeetingAIAnalysis {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.MeetingAIAnalysis{}, f.analyses...)
}

type fakeAnalysisSessionRepository struct {
	session *domain.MeetingSession
}

func (f *fakeAnalysisSessionRepository) CreateMeetingSession(context.Context, domain.MeetingSession) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeAnalysisSessionRepository) CreateOrReuseMeetingSession(context.Context, domain.MeetingSession) (*domain.MeetingSession, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (f *fakeAnalysisSessionRepository) GetMeetingSession(_ context.Context, sessionID string) (*domain.MeetingSession, error) {
	if f.session == nil || f.session.ID != sessionID {
		return nil, domain.ErrNotFound
	}
	return f.session, nil
}

func (f *fakeAnalysisSessionRepository) ListMeetingSessions(context.Context, string, int) ([]domain.MeetingSession, error) {
	return nil, nil
}

func (f *fakeAnalysisSessionRepository) UpdateMeetingSessionStatus(context.Context, domain.MeetingSessionStatusUpdate) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeAnalysisSessionRepository) UpdateMeetingSessionMetadata(context.Context, domain.MeetingSessionMetadataUpdate) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeAnalysisSessionRepository) MarkStaleMeetingSessions(context.Context, time.Time, time.Time) ([]domain.MeetingSession, error) {
	return nil, nil
}

func (f *fakeAnalysisSessionRepository) ListMeetingSessionDebug(context.Context, int) ([]domain.MeetingSessionDebug, error) {
	return nil, nil
}

func (f *fakeAnalysisSessionRepository) TouchMeetingSessionBotSeen(context.Context, string, time.Time) (*domain.MeetingSession, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (f *fakeAnalysisSessionRepository) ListMeetingSessionsForBotWatchdog(context.Context) ([]domain.MeetingSession, error) {
	return nil, nil
}

type fakeAnalysisTranscriptRepository struct {
	segments []domain.TranscriptSegment
}

func (f *fakeAnalysisTranscriptRepository) SaveTranscriptSegment(context.Context, domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	return domain.TranscriptSegmentStoreResult{}, errors.New("not implemented")
}

func (f *fakeAnalysisTranscriptRepository) ListTranscriptSegments(context.Context, string, string, int) ([]domain.TranscriptSegment, error) {
	return f.segments, nil
}

// TestMeetingSessionEndedPersistsDurableTreeSnapshot verifies Task F: at
// meeting end, the last live tree goes through one reorganization pass and
// is persisted as a durable "tree" analysis row (reason=meeting_ended,
// final=true), independent of the live payload row.
func TestMeetingSessionEndedPersistsDurableTreeSnapshot(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	livePayload := `{
		"summary": "要約",
		"currentTopic": "進捗確認",
		"items": [{"id": "issue-a", "kind": "issue", "severity": "medium", "title": "課題A", "body": "", "status": "open"}],
		"tree": {
			"nodes": [
				{"id": "root", "kind": "topic", "label": "会議"},
				{"id": "topic-progress", "kind": "topic", "parentId": "root", "label": "進捗確認"},
				{"id": "issue-a", "kind": "issue", "parentId": "topic-progress", "label": "課題A"}
			],
			"edges": [
				{"source": "root", "target": "topic-progress"},
				{"source": "topic-progress", "target": "issue-a"}
			]
		},
		"treeVersion": 7
	}`
	repository.seed(domain.MeetingAIAnalysis{
		SessionID: "session_1",
		Type:      domain.MeetingAIAnalysisLive,
		Status:    domain.MeetingAIAnalysisCompleted,
		Version:   7,
		Payload:   json.RawMessage(livePayload),
	})
	// 1回目=最終再編成(Task F)、2回目=最終要約。
	completer := &fakeAIChatCompleter{results: []application.AIChatResult{
		{Content: `{"basedOnTreeVersion": 7, "operations": [{"type":"rename_topic","topicId":"topic-progress","label":"進捗と課題"}]}`},
		{Content: finalAnalysisResultJSON},
	}}
	transcriptRepo := &fakeAnalysisTranscriptRepository{segments: []domain.TranscriptSegment{
		{SpeakerName: "田中さん", Text: "議論を始めましょう。"},
	}}
	config := testFinalOnlyConfig()
	service := application.NewMeetingAnalysisService(repository, transcriptRepo, &fakeAnalysisSessionRepository{}, completer, config)

	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{TranscriptQueueDrained: true})

	waitUntil(t, 2*time.Second, func() bool {
		last, err := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisFinal)
		return err == nil && last.Status == domain.MeetingAIAnalysisCompleted
	})

	snapshot, err := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisTree)
	if err != nil || snapshot == nil {
		t.Fatalf("tree snapshot row missing: %v", err)
	}
	var envelope struct {
		TreeVersion int64  `json:"treeVersion"`
		Reason      string `json:"reason"`
		Final       bool   `json:"final"`
		Tree        struct {
			Nodes []struct {
				ID       string `json:"id"`
				ParentID string `json:"parentId"`
				Label    string `json:"label"`
			} `json:"nodes"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(snapshot.Payload, &envelope); err != nil {
		t.Fatalf("Unmarshal snapshot payload: %v", err)
	}
	if envelope.Reason != "meeting_ended" || !envelope.Final || envelope.TreeVersion != 7 {
		t.Fatalf("snapshot envelope = %+v, want meeting_ended/final/treeVersion=7", envelope)
	}
	renamed := false
	for _, node := range envelope.Tree.Nodes {
		if node.ID == "topic-progress" && node.Label == "進捗と課題" {
			renamed = true
		}
	}
	if !renamed {
		t.Fatalf("snapshot tree = %+v, want meeting-end reorganization applied", envelope.Tree.Nodes)
	}
}

// TestLiveAndFinalTasksShareMeetingContext verifies that the extraction task
// and the final-summary task see the same structured meeting context (same
// stable agenda topic ids), even though they may run on different models.
func TestLiveAndFinalTasksShareMeetingContext(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	// 1回目=コンテキスト設計、2回目=ライブ抽出、3回目=会議終了時の
	// 最終再編成(Task F)、4回目=最終要約。
	completer := &fakeAIChatCompleter{results: []application.AIChatResult{
		{Content: contextPlannerResultJSON},
		{Content: liveAnalysisResultJSON},
		{Content: `{"basedOnTreeVersion": 1, "operations": []}`},
		{Content: finalAnalysisResultJSON},
	}}
	transcriptRepo := &fakeAnalysisTranscriptRepository{segments: []domain.TranscriptSegment{
		{SpeakerName: "田中さん", Text: "議論を始めましょう。"},
	}}
	sessionRepo := &fakeAnalysisSessionRepository{session: &domain.MeetingSession{
		ID:      "session_1",
		Title:   "定例",
		Purpose: "品質確認",
		Agenda:  "1. 文字起こし精度\n2. AI分析の制御\n3. 進行中作業の横断一覧",
	}}
	config := testLiveOnlyConfig(10*time.Millisecond, 1)
	config.FinalEnabled = true
	config.FinalMaxInputChars = 12000
	config.TaskModels = application.AITaskModels{FinalSummary: "gpt-final-strong"}
	service := application.NewMeetingAnalysisService(repository, transcriptRepo, sessionRepo, completer, config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()

	service.PublishTranscriptSegment(domain.TranscriptSegment{
		SessionID: "session_1", IsFinal: true, SpeakerName: "田中さん", Text: "話者識別が不安定になります。",
	})
	waitUntil(t, 2*time.Second, func() bool { return completer.callCount() >= 2 })
	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{TranscriptQueueDrained: true})
	waitUntil(t, 2*time.Second, func() bool { return completer.callCount() >= 4 })

	requests := completer.requestSnapshot()
	if len(requests) < 4 {
		t.Fatalf("requests = %d, want context + live + reorganize + final", len(requests))
	}
	contextRequest, liveRequest, finalRequest := requests[0], requests[1], requests[3]
	if contextRequest.Deployment != "" {
		t.Fatalf("context request deployment = %q, want shared default", contextRequest.Deployment)
	}
	for i, request := range []application.AIChatRequest{liveRequest, finalRequest} {
		if !strings.Contains(request.User, "agenda-1") || !strings.Contains(request.User, "文字起こし精度") {
			t.Fatalf("request[%d] does not include the shared agenda context:\n%s", i, request.User)
		}
	}
	if liveRequest.Deployment != "" {
		t.Fatalf("live request deployment = %q, want shared default", liveRequest.Deployment)
	}
	storedContext, err := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisContext)
	if err != nil {
		t.Fatalf("stored context error = %v", err)
	}
	if !strings.Contains(string(storedContext.Payload), `"role":"action_summary"`) {
		t.Fatalf("stored context = %s, want semantic action_summary role", storedContext.Payload)
	}
	if finalRequest.Deployment != "gpt-final-strong" {
		t.Fatalf("final request deployment = %q, want per-task override", finalRequest.Deployment)
	}
}

func TestMeetingFinalizationFlushesOnlyUnanalyzedTailAndAlignsCoverage(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	repository.seed(domain.MeetingAIAnalysis{
		SessionID: "session_1", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 9, Payload: coveredLivePayload(t, 24),
	})
	segments := finalSegmentsThrough(27)
	transcriptRepo := &fakeAnalysisTranscriptRepository{segments: segments}
	completer := &fakeAIChatCompleter{results: []application.AIChatResult{
		{Content: liveAnalysisResultJSON},
		{Content: `{"basedOnTreeVersion":"10","operations":[]}`},
		{Content: finalAnalysisResultJSON},
	}}
	config := testLiveOnlyConfig(time.Hour, 1)
	config.FinalEnabled = true
	config.FinalMaxInputChars = 12000
	config.FinalizationQuietPeriod = time.Millisecond
	service := application.NewMeetingAnalysisService(repository, transcriptRepo, &fakeAnalysisSessionRepository{}, completer, config)

	err := service.FinalizeMeetingSession(context.Background(), domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{
		BotLastForwardedFinalSequence: 27,
		TranscriptQueueDrained:        true,
	})
	if err != nil {
		t.Fatalf("FinalizeMeetingSession() error = %v", err)
	}
	requests := completer.requestSnapshot()
	if len(requests) != 3 {
		t.Fatalf("AI requests = %d, want final flush + reorganizer + summary", len(requests))
	}
	if !strings.Contains(requests[0].User, "seq-25") || !strings.Contains(requests[0].User, "seq-27") || strings.Contains(requests[0].User, "seq-24") {
		t.Fatalf("final flush prompt has wrong range:\n%s", requests[0].User)
	}

	live, _ := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisLive)
	var liveEnvelope struct {
		CoveredThroughSequenceNo int `json:"coveredThroughSequenceNo"`
		AnalyzedFinalSegments    []struct {
			SequenceNo int `json:"sequenceNo"`
		} `json:"analyzedFinalSegments"`
	}
	if err := json.Unmarshal(live.Payload, &liveEnvelope); err != nil {
		t.Fatalf("unmarshal live coverage: %v", err)
	}
	if live.Version != 10 || liveEnvelope.CoveredThroughSequenceNo != 27 || len(liveEnvelope.AnalyzedFinalSegments) != 27 {
		t.Fatalf("live version/coverage = %d/%+v, want version 10 through 27", live.Version, liveEnvelope)
	}

	for _, analysisType := range []domain.MeetingAIAnalysisType{domain.MeetingAIAnalysisTree, domain.MeetingAIAnalysisFinal} {
		analysis, getErr := repository.GetMeetingAIAnalysis(context.Background(), "session_1", analysisType)
		if getErr != nil {
			t.Fatalf("get %s analysis: %v", analysisType, getErr)
		}
		var envelope struct {
			CoveredThroughSequenceNo int `json:"coveredThroughSequenceNo"`
			SegmentCount             int `json:"segmentCount"`
		}
		if err := json.Unmarshal(analysis.Payload, &envelope); err != nil {
			t.Fatalf("unmarshal %s coverage: %v", analysisType, err)
		}
		if envelope.CoveredThroughSequenceNo != 27 || envelope.SegmentCount != 27 {
			t.Fatalf("%s coverage = %+v, want through/count 27", analysisType, envelope)
		}
	}
}

func TestMeetingFinalizationWaitsForInFlightLiveAnalysis(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	transcriptRepo := &fakeAnalysisTranscriptRepository{segments: finalSegmentsThrough(2)}
	block := make(chan struct{})
	completer := &fakeAIChatCompleter{block: block, results: []application.AIChatResult{
		{Content: liveAnalysisResultJSON},
		{Content: liveAnalysisResultJSON},
		{Content: `{"basedOnTreeVersion":2,"operations":[]}`},
		{Content: finalAnalysisResultJSON},
	}}
	config := testLiveOnlyConfig(5*time.Millisecond, 1)
	config.FinalEnabled = true
	config.FinalMaxInputChars = 12000
	config.FinalizationWaitTimeout = time.Second
	service := application.NewMeetingAnalysisService(repository, transcriptRepo, &fakeAnalysisSessionRepository{}, completer, config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	defer service.Close()
	service.PublishTranscriptSegment(finalSegmentsThrough(1)[0])
	waitUntil(t, time.Second, func() bool { return completer.callCount() == 1 })

	result := make(chan error, 1)
	go func() {
		result <- service.FinalizeMeetingSession(context.Background(), domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{
			BotLastForwardedFinalSequence: 2, TranscriptQueueDrained: true,
		})
	}()
	time.Sleep(40 * time.Millisecond)
	if got := completer.callCount(); got != 1 {
		t.Fatalf("AI calls while live extraction blocked = %d, want 1", got)
	}
	close(block)
	if err := <-result; err != nil {
		t.Fatalf("FinalizeMeetingSession() error = %v", err)
	}
	if got := completer.callCount(); got != 4 {
		t.Fatalf("AI calls = %d, want live + tail flush + reorganizer + summary", got)
	}
}

func TestMeetingFinalizationWaitsForBotAnnouncedDelayedFinal(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	transcriptRepo := &delayedFinalTranscriptRepository{segments: finalSegmentsThrough(26), delayed: finalSegmentsThrough(27)[26]}
	completer := &fakeAIChatCompleter{results: []application.AIChatResult{
		{Content: liveAnalysisResultJSON},
		{Content: `{"basedOnTreeVersion":1,"operations":[]}`},
		{Content: finalAnalysisResultJSON},
	}}
	config := testLiveOnlyConfig(time.Hour, 1)
	config.FinalEnabled = true
	config.FinalMaxInputChars = 12000
	config.FinalizationWaitTimeout = time.Second
	service := application.NewMeetingAnalysisService(repository, transcriptRepo, &fakeAnalysisSessionRepository{}, completer, config)

	err := service.FinalizeMeetingSession(context.Background(), domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{
		BotLastForwardedFinalSequence: 27, TranscriptQueueDrained: true,
	})
	if err != nil {
		t.Fatalf("FinalizeMeetingSession() error = %v", err)
	}
	requests := completer.requestSnapshot()
	if len(requests) < 1 || !strings.Contains(requests[0].User, "seq-27") {
		t.Fatalf("final flush did not include delayed sequence 27: %+v", requests)
	}
	tree, _ := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisTree)
	var envelope struct {
		CoveredThroughSequenceNo int `json:"coveredThroughSequenceNo"`
	}
	if err := json.Unmarshal(tree.Payload, &envelope); err != nil || envelope.CoveredThroughSequenceNo != 27 {
		t.Fatalf("tree coverage = %+v, err=%v, want 27", envelope, err)
	}
}

func TestMeetingFinalizationLegacyBotWaitsForLateFinalBeforeQuietPeriod(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	transcriptRepo := &delayedFinalTranscriptRepository{segments: finalSegmentsThrough(26), delayed: finalSegmentsThrough(27)[26]}
	completer := &fakeAIChatCompleter{results: []application.AIChatResult{
		{Content: liveAnalysisResultJSON},
		{Content: `{"basedOnTreeVersion":1,"operations":[]}`},
		{Content: finalAnalysisResultJSON},
	}}
	config := testLiveOnlyConfig(time.Hour, 1)
	config.FinalEnabled = true
	config.FinalMaxInputChars = 12000
	config.FinalizationQuietPeriod = 20 * time.Millisecond
	config.FinalizationWaitTimeout = time.Second
	service := application.NewMeetingAnalysisService(repository, transcriptRepo, &fakeAnalysisSessionRepository{}, completer, config)

	if err := service.FinalizeMeetingSession(context.Background(), domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{}); err != nil {
		t.Fatalf("FinalizeMeetingSession() error = %v", err)
	}
	tree, _ := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisTree)
	var envelope struct {
		CoveredThroughSequenceNo int `json:"coveredThroughSequenceNo"`
	}
	if err := json.Unmarshal(tree.Payload, &envelope); err != nil || envelope.CoveredThroughSequenceNo != 27 {
		t.Fatalf("legacy fallback tree coverage = %+v, err=%v, want 27", envelope, err)
	}
}

func TestMeetingFinalizationDoesNotAdvanceCoverageWhenFinalFlushFails(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	repository.seed(domain.MeetingAIAnalysis{
		SessionID: "session_1", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 9, Payload: coveredLivePayload(t, 24),
	})
	completer := &fakeAIChatCompleter{errs: []error{errors.New("flush failed 1"), errors.New("flush failed 2"), errors.New("flush failed 3")}}
	config := testLiveOnlyConfig(time.Hour, 1)
	config.FinalEnabled = true
	config.FinalFlushMaxAttempts = 3
	service := application.NewMeetingAnalysisService(repository, &fakeAnalysisTranscriptRepository{segments: finalSegmentsThrough(27)}, &fakeAnalysisSessionRepository{}, completer, config)

	err := service.FinalizeMeetingSession(context.Background(), domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{
		BotLastForwardedFinalSequence: 27, TranscriptQueueDrained: true,
	})
	if err == nil {
		t.Fatal("FinalizeMeetingSession() error = nil, want final flush failure")
	}
	live, _ := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisLive)
	var coverage struct {
		CoveredThroughSequenceNo int `json:"coveredThroughSequenceNo"`
	}
	if unmarshalErr := json.Unmarshal(live.Payload, &coverage); unmarshalErr != nil {
		t.Fatalf("unmarshal live coverage: %v", unmarshalErr)
	}
	if coverage.CoveredThroughSequenceNo != 24 || live.Version != 9 {
		t.Fatalf("coverage/version after failure = %d/%d, want 24/9", coverage.CoveredThroughSequenceNo, live.Version)
	}
	progress, _ := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisFinalization)
	if progress.Status != domain.MeetingAIAnalysisFailed {
		t.Fatalf("finalization status = %s, want failed", progress.Status)
	}
}

func TestMeetingFinalizationDoesNotRetryFinalFlushSchemaFailure(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	repository.seed(domain.MeetingAIAnalysis{
		SessionID: "session_1", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 9, Payload: coveredLivePayload(t, 24),
	})
	completer := &fakeAIChatCompleter{results: []application.AIChatResult{{Content: `not json`}}}
	config := testLiveOnlyConfig(time.Hour, 1)
	config.FinalEnabled = true
	config.FinalFlushMaxAttempts = 3
	service := application.NewMeetingAnalysisService(repository, &fakeAnalysisTranscriptRepository{segments: finalSegmentsThrough(27)}, &fakeAnalysisSessionRepository{}, completer, config)

	err := service.FinalizeMeetingSession(context.Background(), domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{
		BotLastForwardedFinalSequence: 27, TranscriptQueueDrained: true,
	})
	if err == nil || !strings.Contains(err.Error(), "non-retryable schema failure") {
		t.Fatalf("FinalizeMeetingSession() error = %v, want non-retryable schema failure", err)
	}
	if got := completer.callCount(); got != 1 {
		t.Fatalf("final flush calls = %d, want 1 for deterministic schema failure", got)
	}
	live, _ := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisLive)
	if live.Version != 9 {
		t.Fatalf("live version = %d, want last good version 9", live.Version)
	}
}

func TestMeetingFinalizationSnapshotsFlushedTreeWhenReorganizerFails(t *testing.T) {
	repository := newFakeAIAnalysisRepository()
	repository.seed(domain.MeetingAIAnalysis{
		SessionID: "session_1", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 9, Payload: coveredLivePayload(t, 24),
	})
	completer := &fakeAIChatCompleter{
		results: []application.AIChatResult{{Content: liveAnalysisResultJSON}, {}, {Content: finalAnalysisResultJSON}},
		errs:    []error{nil, errors.New("reorganizer schema failure")},
	}
	config := testLiveOnlyConfig(time.Hour, 1)
	config.FinalEnabled = true
	config.FinalMaxInputChars = 12000
	service := application.NewMeetingAnalysisService(repository, &fakeAnalysisTranscriptRepository{segments: finalSegmentsThrough(27)}, &fakeAnalysisSessionRepository{}, completer, config)

	err := service.FinalizeMeetingSession(context.Background(), domain.MeetingSession{ID: "session_1"}, application.MeetingSessionFinalizationRequest{
		BotLastForwardedFinalSequence: 27, TranscriptQueueDrained: true,
	})
	if err != nil {
		t.Fatalf("FinalizeMeetingSession() error = %v", err)
	}
	tree, getErr := repository.GetMeetingAIAnalysis(context.Background(), "session_1", domain.MeetingAIAnalysisTree)
	if getErr != nil || tree.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("tree snapshot = %+v, err=%v", tree, getErr)
	}
	var envelope struct {
		CoveredThroughSequenceNo int `json:"coveredThroughSequenceNo"`
		Tree                     struct {
			Nodes []any `json:"nodes"`
		} `json:"tree"`
	}
	if unmarshalErr := json.Unmarshal(tree.Payload, &envelope); unmarshalErr != nil || envelope.CoveredThroughSequenceNo != 27 || len(envelope.Tree.Nodes) == 0 {
		t.Fatalf("fallback tree envelope = %+v, err=%v", envelope, unmarshalErr)
	}
}

func coveredLivePayload(t *testing.T, through int) json.RawMessage {
	t.Helper()
	refs := make([]map[string]any, 0, through)
	for sequence := 1; sequence <= through; sequence++ {
		refs = append(refs, map[string]any{"callId": "call-1", "sequenceNo": sequence})
	}
	payload := map[string]any{
		"summary": "これまでの要約", "currentTopic": "進捗確認", "items": []any{}, "treeVersion": 9,
		"analyzedFinalSegments": refs, "coveredThroughSequenceNo": through,
		"tree": map[string]any{"nodes": []map[string]any{
			{"id": "root", "kind": "topic", "label": "会議"},
			{"id": "topic-progress", "kind": "topic", "parentId": "root", "label": "進捗確認"},
		}, "edges": []map[string]any{{"source": "root", "target": "topic-progress"}}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal covered live payload: %v", err)
	}
	return encoded
}

func finalSegmentsThrough(through int) []domain.TranscriptSegment {
	segments := make([]domain.TranscriptSegment, 0, through)
	for sequence := 1; sequence <= through; sequence++ {
		segments = append(segments, domain.TranscriptSegment{
			SessionID: "session_1", CallID: "call-1", SequenceNo: int64(sequence),
			SpeakerName: "話者", Text: fmt.Sprintf("seq-%d", sequence), IsFinal: true,
		})
	}
	return segments
}

type delayedFinalTranscriptRepository struct {
	mu       sync.Mutex
	segments []domain.TranscriptSegment
	delayed  domain.TranscriptSegment
	lists    int
}

func (r *delayedFinalTranscriptRepository) SaveTranscriptSegment(context.Context, domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	return domain.TranscriptSegmentStoreResult{}, errors.New("not implemented")
}

func (r *delayedFinalTranscriptRepository) ListTranscriptSegments(context.Context, string, string, int) ([]domain.TranscriptSegment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lists++
	if r.lists == 2 && r.delayed.SequenceNo > 0 {
		r.segments = append(r.segments, r.delayed)
	}
	return append([]domain.TranscriptSegment(nil), r.segments...), nil
}
