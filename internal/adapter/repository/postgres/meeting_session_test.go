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
		Status:        domain.MeetingSessionCommandSent,
		CommandSentAt: &commandSentAt,
		UpdatedAt:     commandSentAt,
	})
	if err != nil {
		t.Fatalf("UpdateMeetingSessionStatus() error = %v", err)
	}
	if updated.Status != domain.MeetingSessionCommandSent || !updated.CommandSentAt.Equal(commandSentAt) {
		t.Fatalf("updated = %+v", updated)
	}

	got, err := repository.GetMeetingSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetMeetingSession() error = %v", err)
	}
	if got.ID != session.ID || got.JoinURL != session.JoinURL {
		t.Fatalf("got = %+v", got)
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
