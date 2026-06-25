package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

const createTranscriptSegmentsTableSQL = `
CREATE TABLE IF NOT EXISTS transcript_segments (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id          TEXT    NOT NULL,
    call_id           TEXT    NOT NULL,
    sequence_no       INTEGER NOT NULL,
    recognized_at_utc TEXT    NOT NULL,
    offset_ticks      INTEGER NOT NULL,
    duration_ticks    INTEGER NOT NULL,
    text              TEXT    NOT NULL,
    received_at_utc   TEXT    NOT NULL,

    UNIQUE (event_id),
    UNIQUE (call_id, sequence_no)
);`

const createTranscriptSegmentsCallOrderIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_transcript_segments_call_order
    ON transcript_segments (call_id, sequence_no);`

type TranscriptSegmentRepository struct {
	db *sql.DB
}

func NewTranscriptSegmentRepository(db *sql.DB) *TranscriptSegmentRepository {
	return &TranscriptSegmentRepository{db: db}
}

func InitializeTranscriptSegments(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, createTranscriptSegmentsTableSQL); err != nil {
		return fmt.Errorf("create transcript_segments table: %w", err)
	}
	if _, err := db.ExecContext(ctx, createTranscriptSegmentsCallOrderIndexSQL); err != nil {
		return fmt.Errorf("create transcript_segments call order index: %w", err)
	}
	return nil
}

func (r *TranscriptSegmentRepository) SaveTranscriptSegment(ctx context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("begin transcript segment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	record := transcriptSegmentRecordFromDomain(segment)
	existing, found, err := findTranscriptSegmentByEventID(ctx, tx, record.EventID)
	if err != nil {
		return domain.TranscriptSegmentStoreResult{}, err
	}
	if found {
		if existing.sameContent(record) {
			if err := tx.Commit(); err != nil {
				return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("commit transcript segment duplicate transaction: %w", err)
			}
			return domain.TranscriptSegmentStoreResult{Status: domain.TranscriptSegmentAlreadyExists, EventID: record.EventID}, nil
		}
		return domain.TranscriptSegmentStoreResult{}, transcriptSegmentConflictError()
	}

	existing, found, err = findTranscriptSegmentByCallSequence(ctx, tx, record.CallID, record.SequenceNo)
	if err != nil {
		return domain.TranscriptSegmentStoreResult{}, err
	}
	if found && existing.EventID != record.EventID {
		return domain.TranscriptSegmentStoreResult{}, transcriptSegmentConflictError()
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO transcript_segments (
			event_id, call_id, sequence_no, recognized_at_utc,
			offset_ticks, duration_ticks, text, received_at_utc
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, record.EventID, record.CallID, record.SequenceNo, record.RecognizedAtUTC,
		record.OffsetTicks, record.DurationTicks, record.Text, record.ReceivedAtUTC); err != nil {
		return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("insert transcript segment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("commit transcript segment transaction: %w", err)
	}
	return domain.TranscriptSegmentStoreResult{Status: domain.TranscriptSegmentCreated, EventID: record.EventID}, nil
}

type transcriptSegmentRecord struct {
	EventID         string
	CallID          string
	SequenceNo      int64
	RecognizedAtUTC string
	OffsetTicks     int64
	DurationTicks   int64
	Text            string
	ReceivedAtUTC   string
}

func transcriptSegmentRecordFromDomain(segment domain.TranscriptSegment) transcriptSegmentRecord {
	return transcriptSegmentRecord{
		EventID:         segment.EventID,
		CallID:          segment.CallID,
		SequenceNo:      segment.SequenceNo,
		RecognizedAtUTC: segment.RecognizedAtUTC.UTC().Format(time.RFC3339Nano),
		OffsetTicks:     segment.OffsetTicks,
		DurationTicks:   segment.DurationTicks,
		Text:            segment.Text,
		ReceivedAtUTC:   segment.ReceivedAtUTC.UTC().Format(time.RFC3339Nano),
	}
}

func (record transcriptSegmentRecord) sameContent(other transcriptSegmentRecord) bool {
	return record.EventID == other.EventID &&
		record.CallID == other.CallID &&
		record.SequenceNo == other.SequenceNo &&
		record.RecognizedAtUTC == other.RecognizedAtUTC &&
		record.OffsetTicks == other.OffsetTicks &&
		record.DurationTicks == other.DurationTicks &&
		record.Text == other.Text
}

func findTranscriptSegmentByEventID(ctx context.Context, tx *sql.Tx, eventID string) (transcriptSegmentRecord, bool, error) {
	return scanTranscriptSegment(tx.QueryRowContext(ctx, `
		SELECT event_id, call_id, sequence_no, recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
		FROM transcript_segments
		WHERE event_id = ?
	`, eventID))
}

func findTranscriptSegmentByCallSequence(ctx context.Context, tx *sql.Tx, callID string, sequenceNo int64) (transcriptSegmentRecord, bool, error) {
	return scanTranscriptSegment(tx.QueryRowContext(ctx, `
		SELECT event_id, call_id, sequence_no, recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
		FROM transcript_segments
		WHERE call_id = ? AND sequence_no = ?
	`, callID, sequenceNo))
}

type transcriptSegmentScanner interface {
	Scan(dest ...any) error
}

func scanTranscriptSegment(row transcriptSegmentScanner) (transcriptSegmentRecord, bool, error) {
	var record transcriptSegmentRecord
	err := row.Scan(
		&record.EventID, &record.CallID, &record.SequenceNo, &record.RecognizedAtUTC,
		&record.OffsetTicks, &record.DurationTicks, &record.Text, &record.ReceivedAtUTC,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return transcriptSegmentRecord{}, false, nil
	}
	if err != nil {
		return transcriptSegmentRecord{}, false, fmt.Errorf("query transcript segment: %w", err)
	}
	return record, true, nil
}

func transcriptSegmentConflictError() error {
	return fmt.Errorf("%w: transcript segment conflict", domain.ErrConflict)
}

var _ application.TranscriptSegmentRepository = (*TranscriptSegmentRepository)(nil)
