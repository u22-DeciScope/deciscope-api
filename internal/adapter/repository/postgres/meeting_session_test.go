package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

func TestMeetingSessionRepositoryDeleteRemovesSessionAndDependentData(t *testing.T) {
	repository, db := newTestMeetingSessionRepository(t)
	resetTestDatabase(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)
	session := domain.MeetingSession{
		ID:          "session_test",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/abc",
		JoinURLHash: "hash",
		Status:      domain.MeetingSessionEnded,
		RequestedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := repository.CreateMeetingSession(ctx, session); err != nil {
		t.Fatalf("CreateMeetingSession() error = %v", err)
	}

	transcriptRepository := NewTranscriptSegmentRepository(db)
	if _, err := transcriptRepository.SaveTranscriptSegment(ctx, validTranscriptSegment()); err != nil {
		t.Fatalf("SaveTranscriptSegment() error = %v", err)
	}
	analysisRepository := NewMeetingAIAnalysisRepository(db)
	if _, err := analysisRepository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: "session_test",
		Type:      domain.MeetingAIAnalysisLive,
		Status:    domain.MeetingAIAnalysisCompleted,
		Version:   1,
		Payload:   json.RawMessage(`{"summary":"要約"}`),
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertMeetingAIAnalysis() error = %v", err)
	}
	overridesRepository := NewMeetingAgendaProgressOverridesRepository(db)
	if err := overridesRepository.UpsertAgendaProgressOverrides(ctx, "session_test", json.RawMessage(`{"currentTopicId":"agenda-1"}`), now); err != nil {
		t.Fatalf("UpsertAgendaProgressOverrides() error = %v", err)
	}

	if err := repository.DeleteMeetingSession(ctx, "session_test"); err != nil {
		t.Fatalf("DeleteMeetingSession() error = %v", err)
	}

	if _, err := repository.GetMeetingSession(ctx, "session_test"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetMeetingSession() after delete error = %v, want not found", err)
	}
	if got := transcriptSegmentRowCount(t, db); got != 0 {
		t.Fatalf("transcript_segments row count = %d, want 0", got)
	}
	if got := meetingAIAnalysisRowCount(t, db); got != 0 {
		t.Fatalf("meeting_session_ai_analyses row count = %d, want 0", got)
	}
	if _, err := overridesRepository.GetAgendaProgressOverrides(ctx, "session_test"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetAgendaProgressOverrides() after delete error = %v, want not found", err)
	}
}

func TestMeetingSessionRepositoryDeleteReturnsNotFoundForUnknownSession(t *testing.T) {
	repository, db := newTestMeetingSessionRepository(t)
	resetTestDatabase(t, db)
	ctx := context.Background()

	if err := repository.DeleteMeetingSession(ctx, "session_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteMeetingSession() error = %v, want not found", err)
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

func TestMeetingSessionRepositoryTouchMeetingSessionBotSeenUpdatesActiveSession(t *testing.T) {
	repository, db := newTestMeetingSessionRepository(t)
	resetTestDatabase(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)
	session := domain.MeetingSession{
		ID:          "session_heartbeat",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/heartbeat",
		JoinURLHash: "heartbeat-hash",
		Status:      domain.MeetingSessionRecording,
		RequestedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := repository.CreateMeetingSession(ctx, session); err != nil {
		t.Fatalf("CreateMeetingSession() error = %v", err)
	}

	seenAt := now.Add(20 * time.Second)
	updated, touched, err := repository.TouchMeetingSessionBotSeen(ctx, session.ID, seenAt)
	if err != nil {
		t.Fatalf("TouchMeetingSessionBotSeen() error = %v", err)
	}
	if !touched {
		t.Fatalf("touched = %v, want true for a non-terminal session", touched)
	}
	if updated.Status != domain.MeetingSessionRecording {
		t.Fatalf("status changed unexpectedly: %+v", updated)
	}
	if !updated.LastBotStatusAt.Equal(seenAt) || !updated.UpdatedAt.Equal(seenAt) {
		t.Fatalf("updated = %+v, want lastBotStatusAt/updatedAt = %s", updated, seenAt)
	}
}

func TestMeetingSessionRepositoryTouchMeetingSessionBotSeenDoesNotReviveTerminalSession(t *testing.T) {
	repository, db := newTestMeetingSessionRepository(t)
	resetTestDatabase(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)
	session := domain.MeetingSession{
		ID:          "session_ended",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/ended",
		JoinURLHash: "ended-hash",
		Status:      domain.MeetingSessionRecording,
		RequestedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := repository.CreateMeetingSession(ctx, session); err != nil {
		t.Fatalf("CreateMeetingSession() error = %v", err)
	}
	endedAt := now.Add(time.Minute)
	if _, err := repository.UpdateMeetingSessionStatus(ctx, domain.MeetingSessionStatusUpdate{
		SessionID: session.ID,
		Status:    domain.MeetingSessionEnded,
		EndedAt:   &endedAt,
		UpdatedAt: endedAt,
	}); err != nil {
		t.Fatalf("UpdateMeetingSessionStatus(ended) error = %v", err)
	}

	seenAt := now.Add(2 * time.Minute)
	updated, touched, err := repository.TouchMeetingSessionBotSeen(ctx, session.ID, seenAt)
	if err != nil {
		t.Fatalf("TouchMeetingSessionBotSeen() error = %v", err)
	}
	if touched {
		t.Fatalf("touched = %v, want false for a terminal session", touched)
	}
	if updated.Status != domain.MeetingSessionEnded {
		t.Fatalf("status = %s, want ended (session should not be revived)", updated.Status)
	}
	if updated.LastBotStatusAt.Equal(seenAt) {
		t.Fatalf("LastBotStatusAt should not have been updated for a terminal session: %+v", updated)
	}
}

func TestMeetingSessionRepositoryTouchMeetingSessionBotSeenReturnsNotFoundForUnknownSession(t *testing.T) {
	repository, db := newTestMeetingSessionRepository(t)
	resetTestDatabase(t, db)

	if _, _, err := repository.TouchMeetingSessionBotSeen(context.Background(), "session_missing", time.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("TouchMeetingSessionBotSeen() error = %v, want ErrNotFound", err)
	}
}

func TestMeetingSessionRepositoryListMeetingSessionsForBotWatchdogFiltersByStatusAndLastBotStatusAt(t *testing.T) {
	repository, db := newTestMeetingSessionRepository(t)
	resetTestDatabase(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)

	// In scope: recording status with a heartbeat recorded.
	inScope := domain.MeetingSession{
		ID:          "session_in_scope",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/in-scope",
		JoinURLHash: "in-scope-hash",
		Status:      domain.MeetingSessionRecording,
		RequestedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := repository.CreateMeetingSession(ctx, inScope); err != nil {
		t.Fatalf("CreateMeetingSession(inScope) error = %v", err)
	}
	if _, _, err := repository.TouchMeetingSessionBotSeen(ctx, inScope.ID, now.Add(time.Second)); err != nil {
		t.Fatalf("TouchMeetingSessionBotSeen(inScope) error = %v", err)
	}

	// Out of scope: recording status but no heartbeat recorded yet (zero LastBotStatusAt).
	noHeartbeat := domain.MeetingSession{
		ID:          "session_no_heartbeat",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/no-heartbeat",
		JoinURLHash: "no-heartbeat-hash",
		Status:      domain.MeetingSessionRecording,
		RequestedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := repository.CreateMeetingSession(ctx, noHeartbeat); err != nil {
		t.Fatalf("CreateMeetingSession(noHeartbeat) error = %v", err)
	}

	// Out of scope: terminal status even though it has a heartbeat.
	ended := domain.MeetingSession{
		ID:          "session_ended_scope",
		JoinURL:     "https://teams.microsoft.com/l/meetup-join/ended-scope",
		JoinURLHash: "ended-scope-hash",
		Status:      domain.MeetingSessionRecording,
		RequestedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := repository.CreateMeetingSession(ctx, ended); err != nil {
		t.Fatalf("CreateMeetingSession(ended) error = %v", err)
	}
	if _, _, err := repository.TouchMeetingSessionBotSeen(ctx, ended.ID, now.Add(time.Second)); err != nil {
		t.Fatalf("TouchMeetingSessionBotSeen(ended) error = %v", err)
	}
	endedAt := now.Add(2 * time.Second)
	if _, err := repository.UpdateMeetingSessionStatus(ctx, domain.MeetingSessionStatusUpdate{
		SessionID: ended.ID,
		Status:    domain.MeetingSessionEnded,
		EndedAt:   &endedAt,
		UpdatedAt: endedAt,
	}); err != nil {
		t.Fatalf("UpdateMeetingSessionStatus(ended) error = %v", err)
	}

	sessions, err := repository.ListMeetingSessionsForBotWatchdog(ctx)
	if err != nil {
		t.Fatalf("ListMeetingSessionsForBotWatchdog() error = %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != inScope.ID {
		t.Fatalf("sessions = %+v, want exactly [%s]", sessions, inScope.ID)
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
