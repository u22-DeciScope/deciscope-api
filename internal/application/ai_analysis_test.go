package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

const (
	liveAnalysisResultJSON  = `{"summary":"要約です","currentTopic":"進捗確認","items":[{"id":"issue-progress","kind":"issue","severity":"medium","title":"進捗遅れ","body":"タスクAが1週間遅延している。","status":"open"}],"tree":{"nodes":[{"id":"topic-progress","kind":"topic","label":"進捗確認"},{"id":"issue-progress","kind":"issue","label":"進捗遅れ"}],"edges":[{"source":"topic-progress","target":"issue-progress"}]}}`
	finalAnalysisResultJSON = `{"suggestedTitle":"週次定例","overview":"概要です","decisions":[],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`
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
	waitUntil(t, 2*time.Second, func() bool { return repository.upsertCount() >= 1 })

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
	if completer.requestSnapshot()[0].System == "" {
		t.Fatal("system prompt should not be empty")
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

	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"})

	waitUntil(t, 2*time.Second, func() bool { return repository.upsertCount() >= 2 })
	saved := repository.lastUpsert()
	if saved.Status != domain.MeetingAIAnalysisCompleted || saved.Type != domain.MeetingAIAnalysisFinal || saved.Version != 1 {
		t.Fatalf("saved final analysis = %+v", saved)
	}

	upserts := repository.upsertSnapshot()
	if upserts[0].Status != domain.MeetingAIAnalysisRunning {
		t.Fatalf("first upsert = %+v, want running status persisted before the AI call", upserts[0])
	}

	if len(completer.requestSnapshot()) != 1 {
		t.Fatalf("completer calls = %d, want 1", len(completer.requestSnapshot()))
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

	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"})

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

	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"})

	time.Sleep(100 * time.Millisecond)
	if got := completer.callCount(); got != 0 {
		t.Fatalf("callCount() = %d, want 0 (empty transcript)", got)
	}
	if got := repository.upsertCount(); got != 0 {
		t.Fatalf("upsertCount() = %d, want 0", got)
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
	service.NotifyMeetingSessionEnded(domain.MeetingSession{ID: "session_1"})

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
		Enabled:            true,
		FinalEnabled:       true,
		FinalMaxInputChars: 12000,
		Model:              "test-deployment",
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
