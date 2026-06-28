package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/infrastructure/database"
)

func TestMeetingSessionRepositoryCreatesGetsAndUpdates(t *testing.T) {
	repository, db := newTestMeetingSessionRepository(t)
	resetTestDatabase(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)
	session := domain.MeetingSession{
		ID:          "session_test",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/abc",
		JoinURLHash: "hash",
		Status:      domain.MeetingSessionPendingJoin,
		RequestedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	created, err := repository.CreateMeetingSession(ctx, session)
	if err != nil {
		t.Fatalf("CreateMeetingSession() error = %v", err)
	}
	if created.ID != "session_test" || created.JoinURLHash != "hash" || created.Status != domain.MeetingSessionPendingJoin {
		t.Fatalf("created = %+v", created)
	}

	commandSentAt := now.Add(time.Second)
	updated, err := repository.UpdateMeetingSessionStatus(ctx, domain.MeetingSessionStatusUpdate{
		SessionID:     session.ID,
		Status:        domain.MeetingSessionJoining,
		BotCallID:     "call-1",
		CommandSentAt: &commandSentAt,
		UpdatedAt:     commandSentAt,
	})
	if err != nil {
		t.Fatalf("UpdateMeetingSessionStatus(joining) error = %v", err)
	}
	if updated.Status != domain.MeetingSessionJoining || updated.BotCallID != "call-1" || !updated.CommandSentAt.Equal(commandSentAt) {
		t.Fatalf("updated = %+v", updated)
	}

	joinedAt := now.Add(2 * time.Second)
	updated, err = repository.UpdateMeetingSessionStatus(ctx, domain.MeetingSessionStatusUpdate{
		SessionID: session.ID,
		Status:    domain.MeetingSessionJoined,
		BotCallID: "call-1",
		JoinedAt:  &joinedAt,
		UpdatedAt: joinedAt,
	})
	if err != nil {
		t.Fatalf("UpdateMeetingSessionStatus(joined) error = %v", err)
	}
	if updated.Status != domain.MeetingSessionJoined || updated.BotCallID != "call-1" || !updated.JoinedAt.Equal(joinedAt) {
		t.Fatalf("joined updated = %+v", updated)
	}

	recordingAt := now.Add(3 * time.Second)
	updated, err = repository.UpdateMeetingSessionStatus(ctx, domain.MeetingSessionStatusUpdate{
		SessionID: session.ID,
		Status:    domain.MeetingSessionRecording,
		BotCallID: "call-1",
		JoinedAt:  &recordingAt,
		UpdatedAt: recordingAt,
	})
	if err != nil {
		t.Fatalf("UpdateMeetingSessionStatus(recording) error = %v", err)
	}
	if updated.Status != domain.MeetingSessionRecording || updated.BotCallID != "call-1" || !updated.UpdatedAt.Equal(recordingAt) {
		t.Fatalf("recording updated = %+v", updated)
	}

	got, err := repository.GetMeetingSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetMeetingSession() error = %v", err)
	}
	if got.ID != session.ID || got.JoinURL != session.JoinURL || got.Status != domain.MeetingSessionRecording || got.BotCallID != "call-1" {
		t.Fatalf("got = %+v", got)
	}
}

func TestMeetingSessionRepositoryCreateOrReuseReturnsOpenSession(t *testing.T) {
	repository, db := newTestMeetingSessionRepository(t)
	resetTestDatabase(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)
	existing := domain.MeetingSession{
		ID:          "session_existing",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/abc",
		JoinURLHash: "same-hash",
		Status:      domain.MeetingSessionJoining,
		RequestedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := repository.CreateMeetingSession(ctx, existing); err != nil {
		t.Fatalf("CreateMeetingSession(existing) error = %v", err)
	}

	candidate := domain.MeetingSession{
		ID:          "session_new",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/abc",
		JoinURLHash: "same-hash",
		Status:      domain.MeetingSessionRequested,
		RequestedAt: now.Add(time.Minute),
		CreatedAt:   now.Add(time.Minute),
		UpdatedAt:   now.Add(time.Minute),
	}
	got, created, err := repository.CreateOrReuseMeetingSession(ctx, candidate)
	if err != nil {
		t.Fatalf("CreateOrReuseMeetingSession() error = %v", err)
	}
	if created || got.ID != existing.ID {
		t.Fatalf("got=%+v created=%v, want existing reused", got, created)
	}
}

func TestMeetingSessionRepositoryMarksStaleSessions(t *testing.T) {
	repository, db := newTestMeetingSessionRepository(t)
	resetTestDatabase(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	session := domain.MeetingSession{
		ID:          "session_stale",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/stale",
		JoinURLHash: "stale-hash",
		Status:      domain.MeetingSessionJoining,
		RequestedAt: now.Add(-24 * time.Hour),
		CreatedAt:   now.Add(-24 * time.Hour),
		UpdatedAt:   now.Add(-24 * time.Hour),
	}
	if _, err := repository.CreateMeetingSession(ctx, session); err != nil {
		t.Fatalf("CreateMeetingSession() error = %v", err)
	}

	stale, err := repository.MarkStaleMeetingSessions(ctx, now.Add(-12*time.Hour), now)
	if err != nil {
		t.Fatalf("MarkStaleMeetingSessions() error = %v", err)
	}
	if len(stale) != 1 || stale[0].Status != domain.MeetingSessionStale {
		t.Fatalf("stale = %+v", stale)
	}
}

func newTestMeetingSessionRepository(t *testing.T) (*MeetingSessionRepository, *sql.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), database.Config{URL: testDatabaseURL(t)})
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	return NewMeetingSessionRepository(db), db
}
