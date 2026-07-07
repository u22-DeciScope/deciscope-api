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

func TestMeetingSessionWatchdogPublishesUnhealthyOnceAfterLostAfter(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-90 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (second scan) error = %v", err)
	}

	events := publisher.snapshot()
	if len(events) != 1 {
		t.Fatalf("published events = %+v, want exactly one unhealthy event across two scans", events)
	}
	if events[0].sessionID != "session_1" || events[0].healthy {
		t.Fatalf("event = %+v, want unhealthy for session_1", events[0])
	}
	if len(ender.calls) != 0 {
		t.Fatalf("ender should not be called before EndAfter elapses, calls=%+v", ender.calls)
	}
}

func TestMeetingSessionWatchdogEndsSessionAfterEndAfter(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			BotCallID:       "call-1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-200 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if len(ender.calls) != 1 {
		t.Fatalf("ender calls = %+v, want exactly one", ender.calls)
	}
	call := ender.calls[0]
	if call.SessionID != "session_1" || call.Status != domain.MeetingSessionEnded || call.Reason != "bot_unresponsive" || call.Source != "watchdog" || call.BotCallID != "call-1" {
		t.Fatalf("end call = %+v", call)
	}
	if call.Message == "" {
		t.Fatalf("end call message should describe the outage, got empty string")
	}
}

func TestMeetingSessionWatchdogPublishesHealthyOnceOnRecovery(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-90 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (unhealthy scan) error = %v", err)
	}
	if got := publisher.snapshot(); len(got) != 1 || got[0].healthy {
		t.Fatalf("expected one unhealthy event before recovery, got %+v", got)
	}

	// Heartbeat resumes: LastBotStatusAt is now recent.
	repository.mu.Lock()
	repository.sessions[0].LastBotStatusAt = now
	repository.mu.Unlock()

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (recovery scan) error = %v", err)
	}
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (recovery scan repeat) error = %v", err)
	}

	events := publisher.snapshot()
	if len(events) != 2 {
		t.Fatalf("published events = %+v, want exactly one unhealthy + one healthy", events)
	}
	if events[1].sessionID != "session_1" || !events[1].healthy {
		t.Fatalf("recovery event = %+v, want healthy for session_1", events[1])
	}
}

func TestMeetingSessionWatchdogDoesNotPublishOnFirstHealthyObservation(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-5 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (second scan) error = %v", err)
	}

	if events := publisher.snapshot(); len(events) != 0 {
		t.Fatalf("published events = %+v, want none for a session observed healthy for the first time", events)
	}
	if len(ender.calls) != 0 {
		t.Fatalf("ender should not be called, calls=%+v", ender.calls)
	}
}

func TestMeetingSessionWatchdogIgnoresZeroLastBotStatusAt(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:     "session_1",
			Status: domain.MeetingSessionJoined,
			// LastBotStatusAt intentionally left zero.
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(publisher.snapshot()) != 0 || len(ender.calls) != 0 {
		t.Fatalf("a session with zero LastBotStatusAt must be ignored: published=%+v ended=%+v", publisher.snapshot(), ender.calls)
	}
}

func TestMeetingSessionWatchdogIgnoresOutOfScopeStatus(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRequested,
			LastBotStatusAt: now.Add(-300 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(publisher.snapshot()) != 0 || len(ender.calls) != 0 {
		t.Fatalf("a session outside the watched status set must be ignored: published=%+v ended=%+v", publisher.snapshot(), ender.calls)
	}
}

type fakeWatchdogRepository struct {
	mu       sync.Mutex
	sessions []domain.MeetingSession
}

func (f *fakeWatchdogRepository) ListMeetingSessionsForBotWatchdog(context.Context) ([]domain.MeetingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.MeetingSession{}, f.sessions...), nil
}

func (f *fakeWatchdogRepository) CreateMeetingSession(context.Context, domain.MeetingSession) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) CreateOrReuseMeetingSession(context.Context, domain.MeetingSession) (*domain.MeetingSession, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) GetMeetingSession(context.Context, string) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) ListMeetingSessions(context.Context, string, int) ([]domain.MeetingSession, error) {
	return nil, nil
}

func (f *fakeWatchdogRepository) UpdateMeetingSessionStatus(context.Context, domain.MeetingSessionStatusUpdate) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) UpdateMeetingSessionMetadata(context.Context, domain.MeetingSessionMetadataUpdate) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) MarkStaleMeetingSessions(context.Context, time.Time, time.Time) ([]domain.MeetingSession, error) {
	return nil, nil
}

func (f *fakeWatchdogRepository) ListMeetingSessionDebug(context.Context, int) ([]domain.MeetingSessionDebug, error) {
	return nil, nil
}

func (f *fakeWatchdogRepository) TouchMeetingSessionBotSeen(context.Context, string, time.Time) (*domain.MeetingSession, bool, error) {
	return nil, false, errors.New("not implemented")
}

type fakeWatchdogEnder struct {
	mu    sync.Mutex
	calls []application.MeetingSessionStatusUpdateInput
}

func (f *fakeWatchdogEnder) UpdateMeetingSessionStatus(_ context.Context, input application.MeetingSessionStatusUpdateInput) (*domain.MeetingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, input)
	return &domain.MeetingSession{ID: input.SessionID, Status: input.Status}, nil
}

type watchdogHealthEvent struct {
	sessionID string
	healthy   bool
}

type fakeWatchdogPublisher struct {
	mu     sync.Mutex
	events []watchdogHealthEvent
}

func (f *fakeWatchdogPublisher) PublishMeetingSessionBotHealth(session domain.MeetingSession, healthy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, watchdogHealthEvent{sessionID: session.ID, healthy: healthy})
}

func (f *fakeWatchdogPublisher) snapshot() []watchdogHealthEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]watchdogHealthEvent{}, f.events...)
}
