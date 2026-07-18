package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

func TestMeetingSessionServiceCreatesAndSendsBotCommand(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	commander := &fakeBotJoinCommander{}
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, commander, publisher)

	session, err := service.CreateMeetingSession(context.Background(), application.MeetingSessionCreateInput{
		JoinURL: "https://teams.microsoft.com/l/meetup-join/abc",
		Title:   "週次定例",
	})
	if err != nil {
		t.Fatalf("CreateMeetingSession() error = %v", err)
	}
	if session.Session.Status != domain.MeetingSessionJoining {
		t.Fatalf("session = %+v", session.Session)
	}
	if session.Session.Title != "週次定例" || session.Session.TitleSource != "user_input" {
		t.Fatalf("title metadata = %+v", session.Session)
	}
	if commander.command.SessionID == "" || commander.command.JoinURL != "https://teams.microsoft.com/l/meetup-join/abc" {
		t.Fatalf("command = %+v", commander.command)
	}
	if repository.created.JoinURLHash == "" || repository.created.Status != domain.MeetingSessionRequested || repository.updated.Status != domain.MeetingSessionJoining {
		t.Fatalf("repository created=%+v updated=%+v", repository.created, repository.updated)
	}
	if len(publisher.sessions) != 1 || publisher.sessions[0].Status != domain.MeetingSessionJoining {
		t.Fatalf("published sessions = %+v", publisher.sessions)
	}
}

func TestMeetingSessionServiceStoresPreMeetingContext(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	commander := &fakeBotJoinCommander{}
	service := application.NewMeetingSessionService(repository, commander)

	result, err := service.CreateMeetingSession(context.Background(), application.MeetingSessionCreateInput{
		JoinURL:           "https://teams.microsoft.com/l/meetup-join/abc",
		Purpose:           "意思決定",
		Context:           "前回の宿題を確認済み",
		Agenda:            "論点A\n論点B",
		DecisionPoints:    "リリース可否",
		Concerns:          "期限が近い",
		ExpectedOutput:    "次アクション",
		CustomInstruction: "リスクを強調",
	})
	if err != nil {
		t.Fatalf("CreateMeetingSession() error = %v", err)
	}
	if result.Session.Purpose != "意思決定" || result.Session.Context != "前回の宿題を確認済み" || result.Session.DecisionPoints != "リリース可否" {
		t.Fatalf("pre meeting context = %+v", result.Session)
	}
}

func TestMeetingSessionServiceNotifiesPreparingObserverAfterMetadataSave(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	commander := &fakeBotJoinCommander{}
	observer := &captureMeetingSessionPreparingObserver{}
	service := application.NewMeetingSessionService(repository, commander)
	service.SetMeetingSessionPreparingObserver(observer)

	result, err := service.CreateMeetingSession(context.Background(), application.MeetingSessionCreateInput{
		JoinURL: "https://teams.microsoft.com/l/meetup-join/context-prewarm",
		Title:   "環境評価",
		Agenda:  "鳥類\n騒音",
	})
	if err != nil {
		t.Fatal(err)
	}
	if observer.session.ID != result.Session.ID || observer.session.Title != "環境評価" || observer.session.Agenda != "鳥類\n騒音" {
		t.Fatalf("prepared session=%+v result=%+v", observer.session, result.Session)
	}
}
func TestMeetingSessionServiceUsesCreatedByEmailAsTitleLookupPrincipalName(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	commander := &fakeBotJoinCommander{}
	service := application.NewMeetingSessionService(repository, commander)

	_, err := service.CreateMeetingSession(context.Background(), application.MeetingSessionCreateInput{
		JoinURL:        "https://teams.microsoft.com/l/meetup-join/abc",
		CreatedByEmail: "user@example.com",
	})
	if err != nil {
		t.Fatalf("CreateMeetingSession() error = %v", err)
	}
	if len(commander.command.CandidateUserIDs) != 0 {
		t.Fatalf("candidate user ids = %#v", commander.command.CandidateUserIDs)
	}
	if len(commander.command.CandidateUserPrincipalNames) != 1 || commander.command.CandidateUserPrincipalNames[0] != "user@example.com" {
		t.Fatalf("candidate user principal names = %#v", commander.command.CandidateUserPrincipalNames)
	}
}

func TestMeetingSessionServiceReusesOpenSessionAndSkipsBotCommand(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.reuseSession = domain.MeetingSession{
		ID:          "session_existing",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/abc",
		JoinURLHash: domain.JoinURLHash("https://teams.microsoft.com/l/meetup-join/abc"),
		Status:      domain.MeetingSessionJoining,
		RequestedAt: repository.session.RequestedAt,
		CreatedAt:   repository.session.CreatedAt,
		UpdatedAt:   repository.session.UpdatedAt,
	}
	commander := &fakeBotJoinCommander{}
	service := application.NewMeetingSessionService(repository, commander)

	result, err := service.CreateMeetingSession(context.Background(), application.MeetingSessionCreateInput{
		JoinURL: "https://teams.microsoft.com/l/meetup-join/abc",
	})
	if err != nil {
		t.Fatalf("CreateMeetingSession() error = %v", err)
	}
	if !result.Reused || result.Session.ID != "session_existing" {
		t.Fatalf("result = %+v", result)
	}
	if commander.command.SessionID != "" {
		t.Fatalf("command should not be sent on reuse: %+v", commander.command)
	}
}

func TestMeetingSessionServiceRejectsInvalidJoinURL(t *testing.T) {
	service := application.NewMeetingSessionService(newFakeMeetingSessionRepository(), &fakeBotJoinCommander{})

	if _, err := service.CreateMeetingSession(context.Background(), application.MeetingSessionCreateInput{
		JoinURL: "https://example.com/not-teams",
	}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("CreateMeetingSession() error = %v, want invalid argument", err)
	}
}

func TestMeetingSessionServiceMarksFailedWhenBotCommandFails(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	commander := &fakeBotJoinCommander{err: application.ErrBotControlCommandFailed}
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, commander, publisher)

	session, err := service.CreateMeetingSession(context.Background(), application.MeetingSessionCreateInput{
		JoinURL: "https://teams.microsoft.com/l/meetup-join/abc",
	})

	if !errors.Is(err, application.ErrBotControlCommandFailed) {
		t.Fatalf("CreateMeetingSession() error = %v, want bot command failure", err)
	}
	if session == nil || session.Session.Status != domain.MeetingSessionFailed || session.Session.LastError == "" {
		t.Fatalf("session = %+v", session)
	}
	if repository.updated.Status != domain.MeetingSessionFailed {
		t.Fatalf("updated = %+v", repository.updated)
	}
}

func TestMeetingSessionServiceUpdatesBotStatus(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{}, publisher)

	session, err := service.UpdateMeetingSessionStatus(context.Background(), application.MeetingSessionStatusUpdateInput{
		SessionID: "session_1",
		Status:    domain.MeetingSessionJoined,
		BotCallID: "call-1",
		Message:   "joined successfully",
	})
	if err != nil {
		t.Fatalf("UpdateMeetingSessionStatus() error = %v", err)
	}
	if session.Status != domain.MeetingSessionJoined || session.BotCallID != "call-1" || session.JoinedAt.IsZero() {
		t.Fatalf("session = %+v", session)
	}
	if len(publisher.sessions) != 1 || publisher.sessions[0].Status != domain.MeetingSessionJoined {
		t.Fatalf("published sessions = %+v", publisher.sessions)
	}
}

func TestMeetingSessionServiceUpdatesSpeechThrottledStatus(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionRecording
	repository.session.BotCallID = "call-1"
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{}, publisher)

	session, err := service.UpdateMeetingSessionStatus(context.Background(), application.MeetingSessionStatusUpdateInput{
		SessionID: "session_1",
		Status:    domain.MeetingSessionSpeechThrottled,
		BotCallID: "call-1",
		Reason:    "azure_speech_throttled",
		ErrorCode: "TooManyRequests",
		Source:    "speech_pipeline",
		Message:   "speech recognizer throttled; reconnecting",
	})
	if err != nil {
		t.Fatalf("UpdateMeetingSessionStatus() error = %v", err)
	}
	if session.Status != domain.MeetingSessionSpeechThrottled || session.JoinedAt.IsZero() {
		t.Fatalf("session = %+v", session)
	}
	if !strings.Contains(session.LastError, "reason=azure_speech_throttled") || !strings.Contains(session.LastError, "errorCode=TooManyRequests") {
		t.Fatalf("lastError = %q", session.LastError)
	}
	if len(publisher.sessions) != 1 || publisher.sessions[0].Status != domain.MeetingSessionSpeechThrottled {
		t.Fatalf("published sessions = %+v", publisher.sessions)
	}
}

func TestMeetingSessionServiceSuppressesNonFatalFailedAfterJoined(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionJoined
	repository.session.BotCallID = "call-1"
	repository.session.JoinedAt = repository.session.CreatedAt.Add(time.Second)
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{}, publisher)

	session, err := service.UpdateMeetingSessionStatus(context.Background(), application.MeetingSessionStatusUpdateInput{
		SessionID: "session_1",
		Status:    domain.MeetingSessionFailed,
		BotCallID: "call-1",
		Reason:    "speech_pipeline_not_ready",
		Source:    "speech_pipeline",
		Message:   "SpeechPipelineReady=False AcceptingFrames=False",
	})
	if err != nil {
		t.Fatalf("UpdateMeetingSessionStatus() error = %v", err)
	}
	if session.Status != domain.MeetingSessionJoined {
		t.Fatalf("session status = %s, want joined", session.Status)
	}
	if repository.updated.Status != "" {
		t.Fatalf("repository update should be suppressed, got %+v", repository.updated)
	}
	if len(publisher.sessions) != 0 {
		t.Fatalf("suppressed update should not be published: %+v", publisher.sessions)
	}
}

func TestMeetingSessionServiceAllowsFatalFailedAfterJoined(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionJoined
	repository.session.BotCallID = "call-1"
	repository.session.JoinedAt = repository.session.CreatedAt.Add(time.Second)
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{}, publisher)

	session, err := service.UpdateMeetingSessionStatus(context.Background(), application.MeetingSessionStatusUpdateInput{
		SessionID: "session_1",
		Status:    domain.MeetingSessionFailed,
		BotCallID: "call-1",
		Reason:    "call_disconnected",
		Source:    "graph",
		Message:   "Graph call disconnected",
	})
	if err != nil {
		t.Fatalf("UpdateMeetingSessionStatus() error = %v", err)
	}
	if session.Status != domain.MeetingSessionFailed {
		t.Fatalf("session status = %s, want failed", session.Status)
	}
	if !strings.Contains(session.LastError, "reason=call_disconnected") || !strings.Contains(session.LastError, "source=graph") {
		t.Fatalf("lastError = %q", session.LastError)
	}
	if len(publisher.sessions) != 1 || publisher.sessions[0].Status != domain.MeetingSessionFailed {
		t.Fatalf("published sessions = %+v", publisher.sessions)
	}
}

func TestMeetingSessionServiceSuppressesNonTerminalUpdateAfterEnded(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionEnded
	repository.session.EndedAt = repository.session.CreatedAt.Add(time.Minute)
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{}, publisher)

	session, err := service.UpdateMeetingSessionStatus(context.Background(), application.MeetingSessionStatusUpdateInput{
		SessionID: "session_1",
		Status:    domain.MeetingSessionRecording,
		BotCallID: "call-1",
		Message:   "late recording update",
	})
	if err != nil {
		t.Fatalf("UpdateMeetingSessionStatus() error = %v", err)
	}
	if session.Status != domain.MeetingSessionEnded {
		t.Fatalf("session status = %s, want ended", session.Status)
	}
	if repository.updated.Status != "" {
		t.Fatalf("repository update should be suppressed, got %+v", repository.updated)
	}
	if len(publisher.sessions) != 0 {
		t.Fatalf("suppressed update should not be published: %+v", publisher.sessions)
	}
}

func TestMeetingSessionServiceStoresEndedReason(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionRecording
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{}, publisher)

	session, err := service.UpdateMeetingSessionStatus(context.Background(), application.MeetingSessionStatusUpdateInput{
		SessionID: "session_1",
		Status:    domain.MeetingSessionEnded,
		BotCallID: "call-1",
		Reason:    "teams_call_terminated",
		Source:    "bot_call_state",
	})
	if err != nil {
		t.Fatalf("UpdateMeetingSessionStatus() error = %v", err)
	}
	if session.Status != domain.MeetingSessionEnded || session.EndedAt.IsZero() || session.EndReason != "teams_call_terminated" {
		t.Fatalf("session = %+v", session)
	}
}

func TestMeetingSessionServiceEndsSessionAndSendsBotCommand(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionRecording
	repository.session.BotCallID = "call-1"
	commander := &fakeBotJoinCommander{}
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, commander, publisher)

	session, err := service.EndMeetingSession(context.Background(), application.MeetingSessionEndInput{
		SessionID: "session_1",
		Reason:    "manual_end_requested",
	})
	if err != nil {
		t.Fatalf("EndMeetingSession() error = %v", err)
	}
	if session.Status != domain.MeetingSessionEnded || session.EndedAt.IsZero() {
		t.Fatalf("session = %+v", session)
	}
	if commander.endCommand.SessionID != "session_1" || commander.endCommand.BotCallID != "call-1" {
		t.Fatalf("end command = %+v", commander.endCommand)
	}
	if len(publisher.sessions) != 2 || publisher.sessions[0].Status != domain.MeetingSessionEnding || publisher.sessions[1].Status != domain.MeetingSessionEnded {
		t.Fatalf("published sessions = %+v", publisher.sessions)
	}
}

func TestMeetingSessionServiceEndsSessionWhenBotCommandFails(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionRecording
	repository.session.BotCallID = "call-1"
	commander := &fakeBotJoinCommander{err: errors.New("bot control API returned 500")}
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, commander, publisher)

	session, err := service.EndMeetingSession(context.Background(), application.MeetingSessionEndInput{
		SessionID: "session_1",
		Reason:    "manual_end_requested",
	})
	if err != nil {
		t.Fatalf("EndMeetingSession() error = %v, want session to end even when the bot command fails", err)
	}
	if session.Status != domain.MeetingSessionEnded || session.EndedAt.IsZero() {
		t.Fatalf("session = %+v, want ended despite bot command failure", session)
	}
	if commander.endCommand.SessionID != "session_1" || commander.endCommand.BotCallID != "call-1" {
		t.Fatalf("end command = %+v", commander.endCommand)
	}
	if len(publisher.sessions) != 2 || publisher.sessions[0].Status != domain.MeetingSessionEnding || publisher.sessions[1].Status != domain.MeetingSessionEnded {
		t.Fatalf("published sessions = %+v, want ending then ended on bot command failure", publisher.sessions)
	}
}

func TestMeetingSessionServiceNotifiesEndedObserverOnNonTerminalToEndedTransition(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionRecording
	observer := &fakeMeetingSessionEndedObserver{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{})
	service.SetMeetingSessionEndedObserver(observer)

	if _, err := service.UpdateMeetingSessionStatus(context.Background(), application.MeetingSessionStatusUpdateInput{
		SessionID: "session_1",
		Status:    domain.MeetingSessionEnded,
		Reason:    "manual_end_requested",
	}); err != nil {
		t.Fatalf("UpdateMeetingSessionStatus() error = %v", err)
	}
	waitUntil(t, time.Second, func() bool { return len(observer.snapshot()) == 1 })
	notified := observer.snapshot()
	if len(notified) != 1 || notified[0].Status != domain.MeetingSessionEnding {
		t.Fatalf("notified sessions = %+v", notified)
	}
}

func TestMeetingSessionServiceRetriesFinalizationIdempotentlyWhenAlreadyEnded(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionEnded
	repository.session.EndedAt = repository.session.CreatedAt.Add(time.Minute)
	observer := &fakeMeetingSessionEndedObserver{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{})
	service.SetMeetingSessionEndedObserver(observer)

	if _, err := service.UpdateMeetingSessionStatus(context.Background(), application.MeetingSessionStatusUpdateInput{
		SessionID: "session_1",
		Status:    domain.MeetingSessionEnded,
		Reason:    "duplicate_end",
	}); err != nil {
		t.Fatalf("UpdateMeetingSessionStatus() error = %v", err)
	}
	waitUntil(t, time.Second, func() bool { return len(observer.snapshot()) == 1 })
	if notified := observer.snapshot(); len(notified) != 1 {
		t.Fatalf("notified sessions = %+v, want one idempotent retry", notified)
	}
}

func TestMeetingSessionServiceDoesNotNotifyEndedObserverForNonEndedTransitions(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	observer := &fakeMeetingSessionEndedObserver{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{})
	service.SetMeetingSessionEndedObserver(observer)

	if _, err := service.UpdateMeetingSessionStatus(context.Background(), application.MeetingSessionStatusUpdateInput{
		SessionID: "session_1",
		Status:    domain.MeetingSessionJoined,
		BotCallID: "call-1",
	}); err != nil {
		t.Fatalf("UpdateMeetingSessionStatus() error = %v", err)
	}
	if notified := observer.snapshot(); len(notified) != 0 {
		t.Fatalf("notified sessions = %+v, want none", notified)
	}
}

func TestMeetingSessionServiceCoalescesConcurrentEndFinalization(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionRecording
	block := make(chan struct{})
	observer := &fakeMeetingSessionEndedObserver{block: block}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{})
	service.SetMeetingSessionEndedObserver(observer)
	input := application.MeetingSessionStatusUpdateInput{
		SessionID: "session_1", Status: domain.MeetingSessionEnded,
		BotLastForwardedFinalSequence: 27, TranscriptQueueDrained: true,
	}

	first, err := service.UpdateMeetingSessionStatus(context.Background(), input)
	if err != nil || first.Status != domain.MeetingSessionEnding {
		t.Fatalf("first end = %+v, err=%v", first, err)
	}
	second, err := service.UpdateMeetingSessionStatus(context.Background(), input)
	if err != nil || second.Status != domain.MeetingSessionEnding {
		t.Fatalf("second end = %+v, err=%v", second, err)
	}
	waitUntil(t, time.Second, func() bool { return len(observer.snapshot()) == 1 })
	time.Sleep(20 * time.Millisecond)
	if got := len(observer.snapshot()); got != 1 {
		t.Fatalf("finalizer calls = %d, want 1", got)
	}
	close(block)
	waitUntil(t, time.Second, func() bool {
		session, _ := repository.GetMeetingSession(context.Background(), "session_1")
		return session.Status == domain.MeetingSessionEnded
	})
}

func TestMeetingSessionServiceRecordsHeartbeatWithoutPublishing(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionRecording
	repository.session.LastBotStatusAt = repository.session.CreatedAt
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{}, publisher)

	session, err := service.RecordMeetingSessionHeartbeat(context.Background(), "session_1")
	if err != nil {
		t.Fatalf("RecordMeetingSessionHeartbeat() error = %v", err)
	}
	if session.LastBotStatusAt.Equal(repository.session.CreatedAt) {
		t.Fatalf("LastBotStatusAt was not updated: %+v", session)
	}
	if session.Status != domain.MeetingSessionRecording {
		t.Fatalf("status changed unexpectedly: %+v", session)
	}
	if repository.touchCallCount != 1 || repository.touchedSessionID != "session_1" {
		t.Fatalf("touch call = count=%d sessionId=%s", repository.touchCallCount, repository.touchedSessionID)
	}
	if len(publisher.sessions) != 0 {
		t.Fatalf("heartbeat must not publish a status change event, got %+v", publisher.sessions)
	}
}

func TestMeetingSessionServiceHeartbeatDoesNotReviveTerminalSession(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	repository.session.Status = domain.MeetingSessionEnded
	repository.session.EndedAt = repository.session.CreatedAt.Add(time.Minute)
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{}, publisher)

	session, err := service.RecordMeetingSessionHeartbeat(context.Background(), "session_1")
	if err != nil {
		t.Fatalf("RecordMeetingSessionHeartbeat() error = %v", err)
	}
	if session.Status != domain.MeetingSessionEnded {
		t.Fatalf("terminal session should not be revived: %+v", session)
	}
	if !session.LastBotStatusAt.IsZero() {
		t.Fatalf("terminal session LastBotStatusAt should not be touched: %+v", session)
	}
	if len(publisher.sessions) != 0 {
		t.Fatalf("heartbeat must not publish, got %+v", publisher.sessions)
	}
}

func TestMeetingSessionServiceHeartbeatRejectsEmptySessionID(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{})

	if _, err := service.RecordMeetingSessionHeartbeat(context.Background(), "   "); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("RecordMeetingSessionHeartbeat() error = %v, want invalid argument", err)
	}
	if repository.touchCallCount != 0 {
		t.Fatalf("repository should not be called for an empty sessionId, touchCallCount=%d", repository.touchCallCount)
	}
}

func TestMeetingSessionServiceHeartbeatReturnsNotFoundForUnknownSession(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	service := application.NewMeetingSessionService(repository, &fakeBotJoinCommander{})

	if _, err := service.RecordMeetingSessionHeartbeat(context.Background(), "session_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RecordMeetingSessionHeartbeat() error = %v, want not found", err)
	}
}

func isFakeTerminalMeetingSessionStatus(status domain.MeetingSessionStatus) bool {
	switch status {
	case domain.MeetingSessionEnded, domain.MeetingSessionFailed, domain.MeetingSessionStale:
		return true
	default:
		return false
	}
}

type fakeMeetingSessionEndedObserver struct {
	mu       sync.Mutex
	sessions []domain.MeetingSession
	block    <-chan struct{}
}

func (f *fakeMeetingSessionEndedObserver) FinalizeMeetingSession(_ context.Context, session domain.MeetingSession, _ application.MeetingSessionFinalizationRequest) error {
	f.mu.Lock()
	f.sessions = append(f.sessions, session)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return nil
}

func (f *fakeMeetingSessionEndedObserver) snapshot() []domain.MeetingSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.MeetingSession(nil), f.sessions...)
}

type captureMeetingSessionPreparingObserver struct {
	session domain.MeetingSession
}

func (o *captureMeetingSessionPreparingObserver) PrepareMeetingSession(session domain.MeetingSession) {
	o.session = session
}

type fakeMeetingSessionRepository struct {
	mu               sync.Mutex
	created          domain.MeetingSession
	updated          domain.MeetingSessionStatusUpdate
	session          domain.MeetingSession
	reuseSession     domain.MeetingSession
	watchdogSessions []domain.MeetingSession
	touchedSessionID string
	touchedSeenAt    time.Time
	touchCallCount   int
}

func newFakeMeetingSessionRepository() *fakeMeetingSessionRepository {
	now := time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)
	return &fakeMeetingSessionRepository{
		session: domain.MeetingSession{
			ID:          "session_1",
			JoinURL:     "https://teams.microsoft.com/l/meetup-join/abc",
			JoinURLHash: "hash",
			Status:      domain.MeetingSessionPendingJoin,
			RequestedAt: now,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
}

func (f *fakeMeetingSessionRepository) CreateMeetingSession(_ context.Context, session domain.MeetingSession) (*domain.MeetingSession, error) {
	f.created = session
	session.ID = "session_1"
	f.session = session
	return &session, nil
}

func (f *fakeMeetingSessionRepository) CreateOrReuseMeetingSession(ctx context.Context, session domain.MeetingSession) (*domain.MeetingSession, bool, error) {
	if f.reuseSession.ID != "" && domain.IsReusableMeetingSessionStatus(f.reuseSession.Status) {
		return &f.reuseSession, false, nil
	}
	created, err := f.CreateMeetingSession(ctx, session)
	return created, true, err
}

func (f *fakeMeetingSessionRepository) GetMeetingSession(_ context.Context, sessionID string) (*domain.MeetingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sessionID != f.session.ID {
		return nil, domain.ErrNotFound
	}
	// Return a copy, like a real repository query would, so callers that
	// hold on to a previously fetched session are not affected by a later
	// mutation of the fake's internal state (e.g. via UpdateMeetingSessionStatus).
	snapshot := f.session
	return &snapshot, nil
}

func (f *fakeMeetingSessionRepository) ListMeetingSessions(_ context.Context, workspaceID string, _ int) ([]domain.MeetingSession, error) {
	if workspaceID == "" {
		return nil, domain.ErrInvalidArgument
	}
	return []domain.MeetingSession{f.session}, nil
}

func (f *fakeMeetingSessionRepository) MarkStaleMeetingSessions(_ context.Context, _ time.Time, _ time.Time) ([]domain.MeetingSession, error) {
	return nil, nil
}

func (f *fakeMeetingSessionRepository) TouchMeetingSessionBotSeen(_ context.Context, sessionID string, seenAt time.Time) (*domain.MeetingSession, bool, error) {
	f.touchCallCount++
	f.touchedSessionID = sessionID
	f.touchedSeenAt = seenAt
	if sessionID != f.session.ID {
		return nil, false, domain.ErrNotFound
	}
	if isFakeTerminalMeetingSessionStatus(f.session.Status) {
		snapshot := f.session
		return &snapshot, false, nil
	}
	f.session.LastBotStatusAt = seenAt
	f.session.UpdatedAt = seenAt
	snapshot := f.session
	return &snapshot, true, nil
}

func (f *fakeMeetingSessionRepository) ListMeetingSessionsForBotWatchdog(_ context.Context) ([]domain.MeetingSession, error) {
	return f.watchdogSessions, nil
}

func (f *fakeMeetingSessionRepository) ListMeetingSessionDebug(_ context.Context, _ int) ([]domain.MeetingSessionDebug, error) {
	return nil, nil
}

func (f *fakeMeetingSessionRepository) UpdateMeetingSessionStatus(_ context.Context, update domain.MeetingSessionStatusUpdate) (*domain.MeetingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated = update
	f.session.Status = update.Status
	f.session.BotCallID = update.BotCallID
	if update.CommandSentAt != nil {
		f.session.CommandSentAt = *update.CommandSentAt
	}
	if update.JoinedAt != nil {
		f.session.JoinedAt = *update.JoinedAt
	}
	if update.EndedAt != nil {
		f.session.EndedAt = *update.EndedAt
	}
	if update.EndReason != "" {
		f.session.EndReason = update.EndReason
	}
	if update.LastBotStatusAt != nil {
		f.session.LastBotStatusAt = *update.LastBotStatusAt
	}
	if update.Title != "" {
		f.session.Title = update.Title
	}
	if update.TitleSource != "" {
		f.session.TitleSource = update.TitleSource
	}
	f.session.LastError = update.LastError
	f.session.UpdatedAt = update.UpdatedAt
	return &f.session, nil
}

func (f *fakeMeetingSessionRepository) UpdateMeetingSessionMetadata(_ context.Context, update domain.MeetingSessionMetadataUpdate) (*domain.MeetingSession, error) {
	if update.Title != "" {
		f.session.Title = update.Title
	}
	if update.TitleSource != "" {
		f.session.TitleSource = update.TitleSource
	}
	if update.Purpose != "" {
		f.session.Purpose = update.Purpose
	}
	if update.Context != "" {
		f.session.Context = update.Context
	}
	if update.Agenda != "" {
		f.session.Agenda = update.Agenda
	}
	if update.DecisionPoints != "" {
		f.session.DecisionPoints = update.DecisionPoints
	}
	if update.Concerns != "" {
		f.session.Concerns = update.Concerns
	}
	if update.ExpectedOutput != "" {
		f.session.ExpectedOutput = update.ExpectedOutput
	}
	if update.CustomInstruction != "" {
		f.session.CustomInstruction = update.CustomInstruction
	}
	f.session.UpdatedAt = update.UpdatedAt
	return &f.session, nil
}

type fakeBotJoinCommander struct {
	command    application.BotJoinCommand
	endCommand application.BotEndCommand
	err        error
}

func (f *fakeBotJoinCommander) SendJoinCommand(_ context.Context, command application.BotJoinCommand) error {
	f.command = command
	return f.err
}

func (f *fakeBotJoinCommander) EndMeetingSession(_ context.Context, command application.BotEndCommand) error {
	f.endCommand = command
	return f.err
}

type fakeMeetingSessionPublisher struct {
	sessions []domain.MeetingSession
}

func (f *fakeMeetingSessionPublisher) PublishMeetingSessionStatusChanged(session domain.MeetingSession) {
	f.sessions = append(f.sessions, session)
}
