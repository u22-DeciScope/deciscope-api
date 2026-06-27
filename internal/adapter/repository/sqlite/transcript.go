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
    session_id        TEXT,
    event_id          TEXT    NOT NULL,
    call_id           TEXT    NOT NULL,
    sequence_no       INTEGER NOT NULL,
    speaker_id        TEXT,
    speaker_name      TEXT,
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

const createTranscriptSegmentsSessionOrderIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_transcript_segments_session_order
    ON transcript_segments (session_id, sequence_no);`

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
	if err := ensureTranscriptSessionIDColumn(ctx, db); err != nil {
		return err
	}
	if err := ensureTranscriptSpeakerIDColumn(ctx, db); err != nil {
		return err
	}
	if err := ensureTranscriptSpeakerNameColumn(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, createTranscriptSegmentsCallOrderIndexSQL); err != nil {
		return fmt.Errorf("create transcript_segments call order index: %w", err)
	}
	if _, err := db.ExecContext(ctx, createTranscriptSegmentsSessionOrderIndexSQL); err != nil {
		return fmt.Errorf("create transcript_segments session order index: %w", err)
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
			session_id, event_id, call_id, sequence_no, speaker_id, speaker_name, recognized_at_utc,
			offset_ticks, duration_ticks, text, received_at_utc
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableString(record.SessionID), record.EventID, record.CallID, record.SequenceNo, nullableString(record.SpeakerID), nullableString(record.SpeakerName), record.RecognizedAtUTC,
		record.OffsetTicks, record.DurationTicks, record.Text, record.ReceivedAtUTC); err != nil {
		return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("insert transcript segment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("commit transcript segment transaction: %w", err)
	}
	return domain.TranscriptSegmentStoreResult{Status: domain.TranscriptSegmentCreated, EventID: record.EventID}, nil
}

func (r *TranscriptSegmentRepository) ListTranscriptSegments(ctx context.Context, callID, sessionID string, limit int) ([]domain.TranscriptSegment, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(session_id, ''), event_id, call_id, sequence_no, COALESCE(speaker_id, ''), COALESCE(speaker_name, ''), recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
		FROM transcript_segments
		WHERE (? = '' OR call_id = ?)
		  AND (? = '' OR session_id = ?)
		ORDER BY call_id ASC, sequence_no ASC, recognized_at_utc ASC
		LIMIT ?
	`, callID, callID, sessionID, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list transcript segments: %w", err)
	}
	defer rows.Close()

	var segments []domain.TranscriptSegment
	for rows.Next() {
		record, err := scanTranscriptSegmentRow(rows)
		if err != nil {
			return nil, err
		}
		segment, err := record.toDomain()
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transcript segments: %w", err)
	}
	return segments, nil
}

type transcriptSegmentRecord struct {
	SessionID       string
	EventID         string
	CallID          string
	SequenceNo      int64
	SpeakerID       string
	SpeakerName     string
	RecognizedAtUTC string
	OffsetTicks     int64
	DurationTicks   int64
	Text            string
	ReceivedAtUTC   string
}

func transcriptSegmentRecordFromDomain(segment domain.TranscriptSegment) transcriptSegmentRecord {
	return transcriptSegmentRecord{
		SessionID:       segment.SessionID,
		EventID:         segment.EventID,
		CallID:          segment.CallID,
		SequenceNo:      segment.SequenceNo,
		SpeakerID:       segment.SpeakerID,
		SpeakerName:     segment.SpeakerName,
		RecognizedAtUTC: segment.RecognizedAtUTC.UTC().Format(time.RFC3339Nano),
		OffsetTicks:     segment.OffsetTicks,
		DurationTicks:   segment.DurationTicks,
		Text:            segment.Text,
		ReceivedAtUTC:   segment.ReceivedAtUTC.UTC().Format(time.RFC3339Nano),
	}
}

func (record transcriptSegmentRecord) toDomain() (domain.TranscriptSegment, error) {
	recognizedAt, err := time.Parse(time.RFC3339Nano, record.RecognizedAtUTC)
	if err != nil {
		return domain.TranscriptSegment{}, fmt.Errorf("parse transcript recognized_at_utc: %w", err)
	}
	receivedAt, err := time.Parse(time.RFC3339Nano, record.ReceivedAtUTC)
	if err != nil {
		return domain.TranscriptSegment{}, fmt.Errorf("parse transcript received_at_utc: %w", err)
	}
	return domain.TranscriptSegment{
		SessionID:       record.SessionID,
		EventID:         record.EventID,
		CallID:          record.CallID,
		SequenceNo:      record.SequenceNo,
		SpeakerID:       record.SpeakerID,
		SpeakerName:     record.SpeakerName,
		RecognizedAtUTC: recognizedAt.UTC(),
		OffsetTicks:     record.OffsetTicks,
		DurationTicks:   record.DurationTicks,
		Text:            record.Text,
		ReceivedAtUTC:   receivedAt.UTC(),
	}, nil
}

func (record transcriptSegmentRecord) sameContent(other transcriptSegmentRecord) bool {
	return record.EventID == other.EventID &&
		record.SessionID == other.SessionID &&
		record.CallID == other.CallID &&
		record.SequenceNo == other.SequenceNo &&
		record.SpeakerID == other.SpeakerID &&
		record.SpeakerName == other.SpeakerName &&
		record.RecognizedAtUTC == other.RecognizedAtUTC &&
		record.OffsetTicks == other.OffsetTicks &&
		record.DurationTicks == other.DurationTicks &&
		record.Text == other.Text
}

func findTranscriptSegmentByEventID(ctx context.Context, tx *sql.Tx, eventID string) (transcriptSegmentRecord, bool, error) {
	return scanTranscriptSegment(tx.QueryRowContext(ctx, `
		SELECT COALESCE(session_id, ''), event_id, call_id, sequence_no, COALESCE(speaker_id, ''), COALESCE(speaker_name, ''), recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
		FROM transcript_segments
		WHERE event_id = ?
	`, eventID))
}

func findTranscriptSegmentByCallSequence(ctx context.Context, tx *sql.Tx, callID string, sequenceNo int64) (transcriptSegmentRecord, bool, error) {
	return scanTranscriptSegment(tx.QueryRowContext(ctx, `
		SELECT COALESCE(session_id, ''), event_id, call_id, sequence_no, COALESCE(speaker_id, ''), COALESCE(speaker_name, ''), recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
		FROM transcript_segments
		WHERE call_id = ? AND sequence_no = ?
	`, callID, sequenceNo))
}

type transcriptSegmentScanner interface {
	Scan(dest ...any) error
}

func scanTranscriptSegment(row transcriptSegmentScanner) (transcriptSegmentRecord, bool, error) {
	record, err := scanTranscriptSegmentRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return transcriptSegmentRecord{}, false, nil
	}
	if err != nil {
		return transcriptSegmentRecord{}, false, err
	}
	return record, true, nil
}

func scanTranscriptSegmentRow(row transcriptSegmentScanner) (transcriptSegmentRecord, error) {
	var record transcriptSegmentRecord
	err := row.Scan(
		&record.SessionID, &record.EventID, &record.CallID, &record.SequenceNo, &record.SpeakerID, &record.SpeakerName, &record.RecognizedAtUTC,
		&record.OffsetTicks, &record.DurationTicks, &record.Text, &record.ReceivedAtUTC,
	)
	if err != nil {
		return transcriptSegmentRecord{}, fmt.Errorf("query transcript segment: %w", err)
	}
	return record, nil
}

func transcriptSegmentConflictError() error {
	return fmt.Errorf("%w: transcript segment conflict", domain.ErrConflict)
}

var _ application.TranscriptSegmentRepository = (*TranscriptSegmentRepository)(nil)

func ensureTranscriptSessionIDColumn(ctx context.Context, db *sql.DB) error {
	return ensureTranscriptColumn(ctx, db, "session_id", `ALTER TABLE transcript_segments ADD COLUMN session_id TEXT`)
}

func ensureTranscriptSpeakerIDColumn(ctx context.Context, db *sql.DB) error {
	return ensureTranscriptColumn(ctx, db, "speaker_id", `ALTER TABLE transcript_segments ADD COLUMN speaker_id TEXT`)
}

func ensureTranscriptSpeakerNameColumn(ctx context.Context, db *sql.DB) error {
	return ensureTranscriptColumn(ctx, db, "speaker_name", `ALTER TABLE transcript_segments ADD COLUMN speaker_name TEXT`)
}

func ensureTranscriptColumn(ctx context.Context, db *sql.DB, columnName, alterSQL string) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(transcript_segments)`)
	if err != nil {
		return fmt.Errorf("inspect transcript_segments columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scan transcript_segments column: %w", err)
		}
		if name == columnName {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate transcript_segments columns: %w", err)
	}
	if _, err := db.ExecContext(ctx, alterSQL); err != nil {
		return fmt.Errorf("add transcript_segments %s column: %w", columnName, err)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
