package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
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
	// JSONBはキー間の空白を正規化して返すため、文字列一致ではなく意味比較する。
	if got.Version != 2 || !jsonPayloadEqual(t, got.Payload, `{"summary":"更新後の要約です。"}`) {
		t.Fatalf("got = %+v payload=%s", got, string(got.Payload))
	}
}

func TestMeetingAIAnalysisRepositoryListsForSessions(t *testing.T) {
	repository, _ := newTestMeetingAIAnalysisRepository(t)
	ctx := context.Background()

	if _, err := repository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: "session_a", Type: domain.MeetingAIAnalysisFinal, Status: domain.MeetingAIAnalysisCompleted,
		Version: 1, Payload: json.RawMessage(`{"overview":"Aの概要"}`), UpdatedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed session_a final: %v", err)
	}
	if _, err := repository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: "session_a", Type: domain.MeetingAIAnalysisLive, Status: domain.MeetingAIAnalysisCompleted,
		Version: 1, Payload: json.RawMessage(`{"summary":"ライブ要約"}`), UpdatedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed session_a live: %v", err)
	}
	if _, err := repository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: "session_b", Type: domain.MeetingAIAnalysisFinal, Status: domain.MeetingAIAnalysisCompleted,
		Version: 1, Payload: json.RawMessage(`{"overview":"Bの概要"}`), UpdatedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed session_b final: %v", err)
	}
	// session_c has no rows at all and should simply be absent from the result.

	results, err := repository.ListMeetingAIAnalysesForSessions(ctx, []string{"session_a", "session_b", "session_c"}, domain.MeetingAIAnalysisFinal)
	if err != nil {
		t.Fatalf("ListMeetingAIAnalysesForSessions() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v, want 2 (final analyses for session_a and session_b only)", results)
	}
	bySessionID := make(map[string]domain.MeetingAIAnalysis, len(results))
	for _, result := range results {
		bySessionID[result.SessionID] = result
	}
	if !jsonPayloadEqual(t, bySessionID["session_a"].Payload, `{"overview":"Aの概要"}`) {
		t.Fatalf("session_a payload = %s", string(bySessionID["session_a"].Payload))
	}
	if !jsonPayloadEqual(t, bySessionID["session_b"].Payload, `{"overview":"Bの概要"}`) {
		t.Fatalf("session_b payload = %s", string(bySessionID["session_b"].Payload))
	}
}

func TestMeetingAIAnalysisRepositoryListForSessionsWithEmptyIDsReturnsEmpty(t *testing.T) {
	repository, _ := newTestMeetingAIAnalysisRepository(t)
	results, err := repository.ListMeetingAIAnalysesForSessions(context.Background(), nil, domain.MeetingAIAnalysisFinal)
	if err != nil {
		t.Fatalf("ListMeetingAIAnalysesForSessions() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want empty", results)
	}
}

func jsonPayloadEqual(t *testing.T, payload json.RawMessage, want string) bool {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(payload, &gotValue); err != nil {
		t.Fatalf("unmarshal payload %s: %v", string(payload), err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("unmarshal expectation %s: %v", want, err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
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
	if !jsonPayloadEqual(t, failed.Payload, `{"summary":"成功した要約"}`) {
		t.Fatalf("failed payload = %s, want previous payload retained", string(failed.Payload))
	}
}

func TestMeetingAIAnalysisRepositoryCASRejectsStaleLiveWrite(t *testing.T) {
	repository, _ := newTestMeetingAIAnalysisRepository(t)
	ctx := context.Background()
	if _, err := repository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: "session_test", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 2,
		Payload: json.RawMessage(`{"treeVersion":2}`), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, applied, err := repository.CompareAndSwapMeetingAIAnalysis(ctx, 1, domain.MeetingAIAnalysis{
		SessionID: "session_test", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 2,
		Payload: json.RawMessage(`{"treeVersion":2,"stale":true}`), UpdatedAt: time.Now().UTC(),
	}); err != nil || applied {
		t.Fatalf("stale CAS applied=%t err=%v", applied, err)
	}
	if saved, applied, err := repository.CompareAndSwapMeetingAIAnalysis(ctx, 2, domain.MeetingAIAnalysis{
		SessionID: "session_test", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 3,
		Payload: json.RawMessage(`{"treeVersion":3}`), UpdatedAt: time.Now().UTC(),
	}); err != nil || !applied || saved.Version != 3 {
		t.Fatalf("current CAS saved=%+v applied=%t err=%v", saved, applied, err)
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

func TestMeetingAIAnalysisRepositoryAppendLiveAnalysisHistoryIsIdempotent(t *testing.T) {
	repository, db := newTestMeetingAIAnalysisRepository(t)
	ctx := context.Background()

	analysis := domain.MeetingAIAnalysis{
		SessionID: "session_test",
		Version:   1,
		Payload:   json.RawMessage(`{"summary":"v1"}`),
		Model:     "gpt-4o-mini",
		UpdatedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
	}
	if err := repository.AppendLiveAnalysisHistory(ctx, analysis); err != nil {
		t.Fatalf("AppendLiveAnalysisHistory() error = %v", err)
	}
	// A duplicate append for the same (session_id, version) must be a no-op,
	// not an error, since a stale retry can call this more than once.
	if err := repository.AppendLiveAnalysisHistory(ctx, analysis); err != nil {
		t.Fatalf("AppendLiveAnalysisHistory(duplicate) error = %v", err)
	}
	if got := meetingAIAnalysisLiveHistoryRowCount(t, db); got != 1 {
		t.Fatalf("row count = %d, want 1 (idempotent on session_id, version)", got)
	}
}

func TestMeetingAIAnalysisRepositoryListLiveAnalysisHistoryOrdersAndLimits(t *testing.T) {
	repository, _ := newTestMeetingAIAnalysisRepository(t)
	ctx := context.Background()

	for version := int64(1); version <= 5; version++ {
		if err := repository.AppendLiveAnalysisHistory(ctx, domain.MeetingAIAnalysis{
			SessionID: "session_test",
			Version:   version,
			Payload:   json.RawMessage(`{"summary":"v"}`),
			UpdatedAt: time.Date(2026, 6, 27, 0, 0, int(version), 0, time.UTC),
		}); err != nil {
			t.Fatalf("AppendLiveAnalysisHistory(version=%d) error = %v", version, err)
		}
	}

	items, err := repository.ListLiveAnalysisHistory(ctx, "session_test", 3)
	if err != nil {
		t.Fatalf("ListLiveAnalysisHistory() error = %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %+v, want 3 (limited to the latest 3 versions)", items)
	}
	// LIMIT picks the 3 most recent versions (3,4,5); the outer query then
	// re-sorts them ascending so callers can replay the progression in order.
	wantVersions := []int64{3, 4, 5}
	for i, want := range wantVersions {
		if items[i].Version != want {
			t.Fatalf("items[%d].Version = %d, want %d (items=%+v)", i, items[i].Version, want, items)
		}
		if items[i].Type != domain.MeetingAIAnalysisLive || items[i].Status != domain.MeetingAIAnalysisCompleted {
			t.Fatalf("items[%d] type/status = %s/%s, want live/completed", i, items[i].Type, items[i].Status)
		}
	}
}

func meetingAIAnalysisLiveHistoryRowCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM meeting_session_ai_analysis_live_history").Scan(&count); err != nil {
		t.Fatalf("count meeting_session_ai_analysis_live_history: %v", err)
	}
	return count
}
