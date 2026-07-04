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

func TestMeetingAIAnalysisRepositoryUpsertsInPlace(t *testing.T) {
	repository, _ := newTestMeetingAIAnalysisRepository(t)
	ctx := context.Background()

	first, err := repository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID:    "session_test",
		Type:         domain.MeetingAIAnalysisLive,
		Status:       domain.MeetingAIAnalysisCompleted,
		Version:      1,
		Payload:      json.RawMessage(`{"summary":"最初の要約です。"}`),
		Model:        "gpt-4o-mini",
		SegmentCount: 3,
		InputChars:   120,
		UpdatedAt:    time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("UpsertMeetingAIAnalysis() error = %v", err)
	}
	if first.Version != 1 || first.Status != domain.MeetingAIAnalysisCompleted || first.Model != "gpt-4o-mini" {
		t.Fatalf("first = %+v", first)
	}
	if first.CreatedAt.IsZero() {
		t.Fatalf("first.CreatedAt is zero")
	}

	second, err := repository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID:    "session_test",
		Type:         domain.MeetingAIAnalysisLive,
		Status:       domain.MeetingAIAnalysisCompleted,
		Version:      2,
		Payload:      json.RawMessage(`{"summary":"更新後の要約です。"}`),
		Model:        "gpt-4o-mini",
		SegmentCount: 5,
		InputChars:   200,
		UpdatedAt:    time.Date(2026, 6, 27, 0, 0, 10, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("UpsertMeetingAIAnalysis(update) error = %v", err)
	}
	if second.Version != 2 || second.SegmentCount != 5 {
		t.Fatalf("second = %+v", second)
	}
	if second.CreatedAt != first.CreatedAt {
		t.Fatalf("created_at changed on update: first=%v second=%v", first.CreatedAt, second.CreatedAt)
	}
	if got := meetingAIAnalysisRowCount(t, repository.db); got != 1 {
		t.Fatalf("row count = %d, want 1", got)
	}

	got, err := repository.GetMeetingAIAnalysis(ctx, "session_test", domain.MeetingAIAnalysisLive)
	if err != nil {
		t.Fatalf("GetMeetingAIAnalysis() error = %v", err)
	}
	if got.Version != 2 || string(got.Payload) != `{"summary":"更新後の要約です。"}` {
		t.Fatalf("got = %+v payload=%s", got, string(got.Payload))
	}
}

func TestMeetingAIAnalysisRepositoryKeepsPayloadOnFailure(t *testing.T) {
	repository, _ := newTestMeetingAIAnalysisRepository(t)
	ctx := context.Background()

	if _, err := repository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: "session_test",
		Type:      domain.MeetingAIAnalysisLive,
		Status:    domain.MeetingAIAnalysisCompleted,
		Version:   1,
		Payload:   json.RawMessage(`{"summary":"成功した要約"}`),
		UpdatedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("UpsertMeetingAIAnalysis(success) error = %v", err)
	}

	failed, err := repository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: "session_test",
		Type:      domain.MeetingAIAnalysisLive,
		Status:    domain.MeetingAIAnalysisFailed,
		Version:   1,
		Payload:   json.RawMessage(`{"summary":"成功した要約"}`),
		LastError: "azure openai timeout",
		UpdatedAt: time.Date(2026, 6, 27, 0, 0, 15, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("UpsertMeetingAIAnalysis(failed) error = %v", err)
	}
	if failed.Status != domain.MeetingAIAnalysisFailed || failed.LastError != "azure openai timeout" {
		t.Fatalf("failed = %+v", failed)
	}
	if string(failed.Payload) != `{"summary":"成功した要約"}` {
		t.Fatalf("failed payload = %s, want previous payload retained", string(failed.Payload))
	}
}

func TestMeetingAIAnalysisRepositoryGetReturnsNotFound(t *testing.T) {
	repository, _ := newTestMeetingAIAnalysisRepository(t)
	ctx := context.Background()

	_, err := repository.GetMeetingAIAnalysis(ctx, "session_missing", domain.MeetingAIAnalysisFinal)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetMeetingAIAnalysis() error = %v, want not found", err)
	}
}

func TestMeetingAIAnalysisRepositoryTracksLiveAndFinalSeparately(t *testing.T) {
	repository, _ := newTestMeetingAIAnalysisRepository(t)
	ctx := context.Background()

	if _, err := repository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: "session_test",
		Type:      domain.MeetingAIAnalysisLive,
		Status:    domain.MeetingAIAnalysisCompleted,
		Version:   3,
		Payload:   json.RawMessage(`{"summary":"ライブ"}`),
		UpdatedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("UpsertMeetingAIAnalysis(live) error = %v", err)
	}
	if _, err := repository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: "session_test",
		Type:      domain.MeetingAIAnalysisFinal,
		Status:    domain.MeetingAIAnalysisCompleted,
		Version:   1,
		Payload:   json.RawMessage(`{"overview":"最終"}`),
		UpdatedAt: time.Date(2026, 6, 27, 0, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("UpsertMeetingAIAnalysis(final) error = %v", err)
	}

	live, err := repository.GetMeetingAIAnalysis(ctx, "session_test", domain.MeetingAIAnalysisLive)
	if err != nil {
		t.Fatalf("GetMeetingAIAnalysis(live) error = %v", err)
	}
	final, err := repository.GetMeetingAIAnalysis(ctx, "session_test", domain.MeetingAIAnalysisFinal)
	if err != nil {
		t.Fatalf("GetMeetingAIAnalysis(final) error = %v", err)
	}
	if live.Version != 3 || final.Version != 1 {
		t.Fatalf("live = %+v final = %+v", live, final)
	}
	if got := meetingAIAnalysisRowCount(t, repository.db); got != 2 {
		t.Fatalf("row count = %d, want 2", got)
	}
}

func newTestMeetingAIAnalysisRepository(t *testing.T) (*MeetingAIAnalysisRepository, *sql.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), database.Config{URL: testDatabaseURL(t)})
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	resetTestDatabase(t, db)
	return NewMeetingAIAnalysisRepository(db), db
}

func meetingAIAnalysisRowCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM meeting_session_ai_analyses").Scan(&count); err != nil {
		t.Fatalf("count meeting_session_ai_analyses: %v", err)
	}
	return count
}
