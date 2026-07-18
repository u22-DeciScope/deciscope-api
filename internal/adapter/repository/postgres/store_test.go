package postgres

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"sync"
	"testing"

	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/infrastructure/database"
)

func TestStoreAppendEventSequencesDurableEvents(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	meeting, err := store.CreateMeeting(ctx, "w_test", "Sequence test", "fixture_replay")
	if err != nil {
		t.Fatalf("CreateMeeting() error = %v", err)
	}

	partial, err := store.AppendEvent(ctx, meeting.ID, domain.EventTranscriptPartial, map[string]any{
		"partial_id":    "p_001",
		"speaker_label": "Speaker A",
		"text":          "hello",
	})
	if err != nil {
		t.Fatalf("AppendEvent(partial) error = %v", err)
	}
	if partial.Seq != 0 {
		t.Fatalf("partial seq = %d, want 0", partial.Seq)
	}

	final, err := store.AppendEvent(ctx, meeting.ID, domain.EventTranscriptFinal, map[string]any{
		"segment_id":    "seg_001",
		"speaker_label": "Speaker A",
		"text":          "hello world",
		"start_ms":      100,
		"end_ms":        900,
	})
	if err != nil {
		t.Fatalf("AppendEvent(final) error = %v", err)
	}
	if final.Seq != 1 {
		t.Fatalf("final seq = %d, want 1", final.Seq)
	}

	analysis, err := store.AppendEvent(ctx, meeting.ID, domain.EventAnalysisDelta, map[string]any{
		"items": []any{},
	})
	if err != nil {
		t.Fatalf("AppendEvent(analysis) error = %v", err)
	}
	if analysis.Seq != 2 {
		t.Fatalf("analysis seq = %d, want 2", analysis.Seq)
	}

	events, err := store.ListEvents(ctx, meeting.ID, 1)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].Seq != 2 {
		t.Fatalf("events after seq 1 = %+v, want only seq 2", events)
	}

	segments, err := store.ListSegments(ctx, meeting.ID, 0)
	if err != nil {
		t.Fatalf("ListSegments() error = %v", err)
	}
	if len(segments) != 1 || segments[0].SegmentID != "seg_001" {
		t.Fatalf("segments = %+v, want seg_001", segments)
	}
}

func TestStoreAppendEventSequencesConcurrentDurableEvents(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	meeting, err := store.CreateMeeting(ctx, "w_test", "Concurrent sequence test", "fixture_replay")
	if err != nil {
		t.Fatalf("CreateMeeting() error = %v", err)
	}

	const eventCount = 20
	seqs := make(chan int64, eventCount)
	errs := make(chan error, eventCount)
	var wg sync.WaitGroup
	for range eventCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event, err := store.AppendEvent(ctx, meeting.ID, domain.EventAnalysisDelta, map[string]any{"items": []any{}})
			if err != nil {
				errs <- err
				return
			}
			seqs <- event.Seq
		}()
	}
	wg.Wait()
	close(seqs)
	close(errs)

	for err := range errs {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	got := make([]int, 0, eventCount)
	for seq := range seqs {
		got = append(got, int(seq))
	}
	sort.Ints(got)
	for i, seq := range got {
		want := i + 1
		if seq != want {
			t.Fatalf("sequences = %v, want contiguous 1..%d", got, eventCount)
		}
	}
}

func TestStoreAppendEventSequencesConcurrentDatabaseConnections(t *testing.T) {
	ctx := context.Background()
	config := database.Config{URL: testDatabaseURL(t)}

	dbA, err := database.Open(ctx, config)
	if err != nil {
		t.Fatalf("database.Open(A) error = %v", err)
	}
	t.Cleanup(func() { _ = dbA.Close() })
	if err := database.Migrate(ctx, dbA); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	resetTestDatabase(t, dbA)

	dbB, err := database.Open(ctx, config)
	if err != nil {
		t.Fatalf("database.Open(B) error = %v", err)
	}
	t.Cleanup(func() { _ = dbB.Close() })

	stores := []*Store{NewStore(dbA), NewStore(dbB)}
	meeting, err := stores[0].CreateMeeting(ctx, "w_test", "Multi-connection sequence test", "fixture_replay")
	if err != nil {
		t.Fatalf("CreateMeeting() error = %v", err)
	}

	const eventCount = 20
	seqs := make(chan int64, eventCount)
	errs := make(chan error, eventCount)
	var wg sync.WaitGroup
	for i := range eventCount {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			event, err := store.AppendEvent(ctx, meeting.ID, domain.EventAnalysisDelta, map[string]any{"items": []any{}})
			if err != nil {
				errs <- err
				return
			}
			seqs <- event.Seq
		}(stores[i%len(stores)])
	}
	wg.Wait()
	close(seqs)
	close(errs)

	for err := range errs {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	assertContiguousSequences(t, seqs, eventCount)
}

func assertContiguousSequences(t *testing.T, seqs <-chan int64, eventCount int) {
	t.Helper()
	got := make([]int, 0, eventCount)
	for seq := range seqs {
		got = append(got, int(seq))
	}
	sort.Ints(got)
	for i, seq := range got {
		want := i + 1
		if seq != want {
			t.Fatalf("sequences = %v, want contiguous 1..%d", got, eventCount)
		}
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(context.Background(), database.Config{URL: testDatabaseURL(t)})
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
	})
	store := NewStore(db)
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	resetTestDatabase(t, db)
	return store
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	value := os.Getenv("DATABASE_TEST_URL")
	if value == "" {
		t.Skip("DATABASE_TEST_URL is not set")
	}
	return value
}

func resetTestDatabase(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		TRUNCATE TABLE meeting_tree_audit_runs, meeting_session_ai_analyses, transcript_segments, meeting_sessions, uploads, jobs, meeting_reports, meeting_segments, meeting_events,
			meetings, user_sessions, workspace_invitations, workspace_members, workspaces,
			user_emails, user_identities, users RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatalf("reset test database: %v", err)
	}
}
