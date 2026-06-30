package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

type TranscriptSegmentRepository struct {
	db *sql.DB
}

func NewTranscriptSegmentRepository(db *sql.DB) *TranscriptSegmentRepository {
	return &TranscriptSegmentRepository{db: db}
}

func (r *TranscriptSegmentRepository) SaveTranscriptSegment(ctx context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("begin transcript segment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	record := transcriptSegmentRecordFromDomain(segment)
	if record.SessionID != "" {
		exists, err := meetingSessionExists(ctx, tx, record.SessionID)
		if err != nil {
			log.Printf("Transcript session existence check failed. sessionId=%s eventId=%s callId=%s sequenceNo=%d error=%v",
				record.SessionID, record.EventID, record.CallID, record.SequenceNo, err)
		} else if !exists {
			log.Printf("session_id mismatch. transcript sessionId=%s eventId=%s callId=%s sequenceNo=%d reason=meeting_session_not_found",
				record.SessionID, record.EventID, record.CallID, record.SequenceNo)
		}
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO transcript_segments (
			session_id, event_id, call_id, sequence_no, speaker_id, speaker_name, recognized_at_utc,
			offset_ticks, duration_ticks, text, received_at_utc
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT DO NOTHING
	`, nullable(record.SessionID), record.EventID, record.CallID, record.SequenceNo, nullable(record.SpeakerID), nullable(record.SpeakerName), record.RecognizedAtUTC,
		record.OffsetTicks, record.DurationTicks, record.Text, record.ReceivedAtUTC)
	if err != nil {
		return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("insert transcript segment: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("inspect transcript segment insert result: %w", err)
	}
	if rowsAffected == 1 {
		if err := tx.Commit(); err != nil {
			return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("commit transcript segment transaction: %w", err)
		}
		return domain.TranscriptSegmentStoreResult{Status: domain.TranscriptSegmentCreated, EventID: record.EventID}, nil
	}

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
	if found {
		return domain.TranscriptSegmentStoreResult{}, transcriptSegmentConflictError()
	}

	return domain.TranscriptSegmentStoreResult{}, fmt.Errorf("insert transcript segment affected no rows without a visible conflict")
}

func (r *TranscriptSegmentRepository) ListTranscriptSegments(ctx context.Context, callID, sessionID string, limit int) ([]domain.TranscriptSegment, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(session_id, ''), event_id, call_id, sequence_no, COALESCE(speaker_id, ''), COALESCE(speaker_name, ''), recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
		FROM transcript_segments
		WHERE ($1 = '' OR call_id = $1)
		  AND ($2 = '' OR session_id = $2)
		ORDER BY call_id ASC, sequence_no ASC, recognized_at_utc ASC
		LIMIT $3
	`, callID, sessionID, limit)
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
		record.OffsetTicks == other.OffsetTicks &&
		record.DurationTicks == other.DurationTicks &&
		record.Text == other.Text
}

func findTranscriptSegmentByEventID(ctx context.Context, tx *sql.Tx, eventID string) (transcriptSegmentRecord, bool, error) {
	return scanTranscriptSegment(tx.QueryRowContext(ctx, `
		SELECT COALESCE(session_id, ''), event_id, call_id, sequence_no, COALESCE(speaker_id, ''), COALESCE(speaker_name, ''), recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
		FROM transcript_segments
		WHERE event_id = $1
	`, eventID))
}

func findTranscriptSegmentByCallSequence(ctx context.Context, tx *sql.Tx, callID string, sequenceNo int64) (transcriptSegmentRecord, bool, error) {
	return scanTranscriptSegment(tx.QueryRowContext(ctx, `
		SELECT COALESCE(session_id, ''), event_id, call_id, sequence_no, COALESCE(speaker_id, ''), COALESCE(speaker_name, ''), recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
		FROM transcript_segments
		WHERE call_id = $1 AND sequence_no = $2
	`, callID, sequenceNo))
}

func meetingSessionExists(ctx context.Context, tx *sql.Tx, sessionID string) (bool, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM meeting_sessions
			WHERE id = $1
		)
	`, sessionID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check meeting session exists: %w", err)
	}
	return exists, nil
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
