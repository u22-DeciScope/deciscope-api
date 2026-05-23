package core

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestStoreAppendEventSequencesDurableEvents(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	meeting, err := store.CreateMeeting(ctx, "Sequence test", "fixture_replay")
	if err != nil {
		t.Fatalf("CreateMeeting() error = %v", err)
	}

	partial, err := store.AppendEvent(ctx, meeting.ID, EventTranscriptPartial, map[string]any{
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

	final, err := store.AppendEvent(ctx, meeting.ID, EventTranscriptFinal, map[string]any{
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

	analysis, err := store.AppendEvent(ctx, meeting.ID, EventAnalysisDelta, map[string]any{
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

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	store := NewStore(db)
	if err := store.Migrate(context.Background()); err != nil {
		if strings.Contains(err.Error(), "go-sqlite3 requires cgo") {
			t.Skipf("sqlite runtime requires CGO: %v", err)
		}
		t.Fatalf("Migrate() error = %v", err)
	}
	return store
}
