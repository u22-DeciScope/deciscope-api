package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
	sqliteinfra "deciscope-core-api/internal/infrastructure/sqlite"
)

func TestTranscriptSegmentRepositoryStoresJapaneseSegmentAndHandlesDuplicates(t *testing.T) {
	repository, db := newTranscriptSegmentRepository(t)
	ctx := context.Background()
	segment := validTranscriptSegment()

	result, err := repository.SaveTranscriptSegment(ctx, segment)
	if err != nil {
		t.Fatalf("SaveTranscriptSegment() error = %v", err)
	}
	if result.Status != domain.TranscriptSegmentCreated {
		t.Fatalf("result = %+v", result)
	}
	if got := transcriptSegmentRowCount(t, db); got != 1 {
		t.Fatalf("row count = %d, want 1", got)
	}
	assertStoredTranscriptSegment(t, db, segment)

	duplicate, err := repository.SaveTranscriptSegment(ctx, segment)
	if err != nil {
		t.Fatalf("SaveTranscriptSegment(duplicate) error = %v", err)
	}
	if duplicate.Status != domain.TranscriptSegmentAlreadyExists {
		t.Fatalf("duplicate result = %+v", duplicate)
	}
	if got := transcriptSegmentRowCount(t, db); got != 1 {
		t.Fatalf("row count after duplicate = %d, want 1", got)
	}
}

func TestTranscriptSegmentRepositoryRejectsEventIDConflict(t *testing.T) {
	repository, db := newTranscriptSegmentRepository(t)
	ctx := context.Background()
	segment := validTranscriptSegment()
	if _, err := repository.SaveTranscriptSegment(ctx, segment); err != nil {
		t.Fatalf("SaveTranscriptSegment() error = %v", err)
	}

	changed := segment
	changed.Text = "内容が変わりました。"
	if _, err := repository.SaveTranscriptSegment(ctx, changed); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("SaveTranscriptSegment(conflict) error = %v, want conflict", err)
	}
	if got := transcriptSegmentRowCount(t, db); got != 1 {
		t.Fatalf("row count after conflict = %d, want 1", got)
	}
}

func TestTranscriptSegmentRepositoryRejectsCallSequenceConflict(t *testing.T) {
	repository, db := newTranscriptSegmentRepository(t)
	ctx := context.Background()
	segment := validTranscriptSegment()
	if _, err := repository.SaveTranscriptSegment(ctx, segment); err != nil {
		t.Fatalf("SaveTranscriptSegment() error = %v", err)
	}

	changed := segment
	changed.EventID = "06008080-91e3-4b88-a8ff-9af629265ced:other"
	if _, err := repository.SaveTranscriptSegment(ctx, changed); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("SaveTranscriptSegment(call sequence conflict) error = %v, want conflict", err)
	}
	if got := transcriptSegmentRowCount(t, db); got != 1 {
		t.Fatalf("row count after conflict = %d, want 1", got)
	}
}

func TestTranscriptSegmentRepositoryListsByCallIDAndLimit(t *testing.T) {
	repository, _ := newTranscriptSegmentRepository(t)
	ctx := context.Background()
	first := validTranscriptSegment()
	second := first
	second.EventID = first.CallID + ":2"
	second.SequenceNo = 2
	second.RecognizedAtUTC = second.RecognizedAtUTC.Add(time.Second)
	second.Text = "次の発話です。"
	otherCall := first
	otherCall.EventID = "other-call:1"
	otherCall.CallID = "other-call"
	otherCall.SessionID = "session_other"

	for _, segment := range []domain.TranscriptSegment{second, otherCall, first} {
		if _, err := repository.SaveTranscriptSegment(ctx, segment); err != nil {
			t.Fatalf("SaveTranscriptSegment(%s) error = %v", segment.EventID, err)
		}
	}

	segments, err := repository.ListTranscriptSegments(ctx, first.CallID, "", 10)
	if err != nil {
		t.Fatalf("ListTranscriptSegments() error = %v", err)
	}
	if len(segments) != 2 || segments[0].EventID != first.EventID || segments[1].EventID != second.EventID {
		t.Fatalf("segments = %+v", segments)
	}

	sessionFiltered, err := repository.ListTranscriptSegments(ctx, "", first.SessionID, 10)
	if err != nil {
		t.Fatalf("ListTranscriptSegments(sessionID) error = %v", err)
	}
	if len(sessionFiltered) != 2 {
		t.Fatalf("session filtered segments length = %d, want 2", len(sessionFiltered))
	}

	limited, err := repository.ListTranscriptSegments(ctx, "", "", 1)
	if err != nil {
		t.Fatalf("ListTranscriptSegments(limit) error = %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited segments length = %d, want 1", len(limited))
	}
}

func newTranscriptSegmentRepository(t *testing.T) (*TranscriptSegmentRepository, *sql.DB) {
	t.Helper()
	db, err := sqliteinfra.Open(context.Background(), sqliteinfra.Config{Path: filepath.Join(t.TempDir(), "transcripts.db")})
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := InitializeTranscriptSegments(context.Background(), db); err != nil {
		t.Fatalf("InitializeTranscriptSegments() error = %v", err)
	}
	return NewTranscriptSegmentRepository(db), db
}

func validTranscriptSegment() domain.TranscriptSegment {
	return domain.TranscriptSegment{
		SessionID:       "session_test",
		EventID:         "06008080-91e3-4b88-a8ff-9af629265ced:1",
		CallID:          "06008080-91e3-4b88-a8ff-9af629265ced",
		SequenceNo:      1,
		RecognizedAtUTC: time.Date(2026, 6, 25, 13, 20, 1, 123456700, time.UTC),
		OffsetTicks:     20300000,
		DurationTicks:   18000000,
		Text:            "本日の会議を開始します。",
		ReceivedAtUTC:   time.Date(2026, 6, 25, 13, 20, 2, 0, time.UTC),
	}
}

func transcriptSegmentRowCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM transcript_segments").Scan(&count); err != nil {
		t.Fatalf("count transcript_segments: %v", err)
	}
	return count
}

func assertStoredTranscriptSegment(t *testing.T, db *sql.DB, want domain.TranscriptSegment) {
	t.Helper()
	var sessionID, eventID, callID, recognizedAtUTC, text, receivedAtUTC string
	var sequenceNo, offsetTicks, durationTicks int64
	err := db.QueryRow(`
		SELECT COALESCE(session_id, ''), event_id, call_id, sequence_no, recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
		FROM transcript_segments
		WHERE event_id = ?
	`, want.EventID).Scan(&sessionID, &eventID, &callID, &sequenceNo, &recognizedAtUTC, &offsetTicks, &durationTicks, &text, &receivedAtUTC)
	if err != nil {
		t.Fatalf("query stored transcript segment: %v", err)
	}
	if sessionID != want.SessionID || eventID != want.EventID || callID != want.CallID || sequenceNo != want.SequenceNo ||
		recognizedAtUTC != "2026-06-25T13:20:01.1234567Z" ||
		offsetTicks != want.OffsetTicks || durationTicks != want.DurationTicks ||
		text != want.Text || receivedAtUTC == "" {
		t.Fatalf("stored transcript segment = sessionID=%q eventID=%q callID=%q sequenceNo=%d recognizedAtUTC=%q offsetTicks=%d durationTicks=%d text=%q receivedAtUTC=%q",
			sessionID, eventID, callID, sequenceNo, recognizedAtUTC, offsetTicks, durationTicks, text, receivedAtUTC)
	}
}
