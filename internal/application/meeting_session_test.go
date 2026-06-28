package application_test

import (
	"context"
	"errors"
	"strings"
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

	session, err := service.CreateMeetingSession(context.Background(), "https://teams.microsoft.com/l/meetup-join/abc")
	if err != nil {
		t.Fatalf("CreateMeetingSession() error = %v", err)
	}
	if session.Session.Status != domain.MeetingSessionJoining {
		t.Fatalf("session = %+v", session.Session)
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

	result, err := service.CreateMeetingSession(context.Background(), "https://teams.microsoft.com/l/meetup-join/abc")
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

	if _, err := service.CreateMeetingSession(context.Background(), "https://example.com/not-teams"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("CreateMeetingSession() error = %v, want invalid argument", err)
	}
}

func TestMeetingSessionServiceMarksFailedWhenBotCommandFails(t *testing.T) {
	repository := newFakeMeetingSessionRepository()
	commander := &fakeBotJoinCommander{err: application.ErrBotControlCommandFailed}
	publisher := &fakeMeetingSessionPublisher{}
	service := application.NewMeetingSessionService(repository, commander, publisher)

	session, err := service.CreateMeetingSession(context.Background(), "https://teams.microsoft.com/l/meetup-join/abc")

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

type fakeMeetingSessionRepository struct {
	created      domain.MeetingSession
	updated      domain.MeetingSessionStatusUpdate
	session      domain.MeetingSession
	reuseSession domain.MeetingSession
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
	if sessionID != f.session.ID {
		return nil, domain.ErrNotFound
	}
	return &f.session, nil
}

func (f *fakeMeetingSessionRepository) MarkStaleMeetingSessions(_ context.Context, _ time.Time, _ time.Time) ([]domain.MeetingSession, error) {
	return nil, nil
}

func (f *fakeMeetingSessionRepository) ListMeetingSessionDebug(_ context.Context, _ int) ([]domain.MeetingSessionDebug, error) {
	return nil, nil
}

func (f *fakeMeetingSessionRepository) UpdateMeetingSessionStatus(_ context.Context, update domain.MeetingSessionStatusUpdate) (*domain.MeetingSession, error) {
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
	f.session.LastError = update.LastError
	f.session.UpdatedAt = update.UpdatedAt
	return &f.session, nil
}

type fakeBotJoinCommander struct {
	command application.BotJoinCommand
	err     error
}

func (f *fakeBotJoinCommander) SendJoinCommand(_ context.Context, command application.BotJoinCommand) error {
	f.command = command
	return f.err
}

type fakeMeetingSessionPublisher struct {
	sessions []domain.MeetingSession
}

func (f *fakeMeetingSessionPublisher) PublishMeetingSessionStatusChanged(session domain.MeetingSession) {
	f.sessions = append(f.sessions, session)
}
