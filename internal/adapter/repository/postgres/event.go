package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"deciscope-core-api/internal/domain"
)

func (s *Store) AppendEvent(ctx context.Context, meetingID, eventType string, payload any) (*domain.Event, error) {
	payloadBytes, err := jsonPayload(payload)
	if err != nil {
		return nil, err
	}
	if !domain.IsDurableEventType(eventType) {
		return &domain.Event{Type: eventType, MeetingID: meetingID, TsMS: domain.NowMS(), Payload: payloadBytes}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339)
	var seq int64
	if err := tx.QueryRowContext(ctx, `
		UPDATE meetings SET next_seq = next_seq + 1, updated_at = $1
		WHERE id = $2 RETURNING next_seq - 1
	`, nowText, meetingID).Scan(&seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meeting_events (meeting_id, seq, type, ts_ms, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, meetingID, seq, eventType, now.UnixMilli(), string(payloadBytes), nowText); err != nil {
		return nil, err
	}
	if eventType == domain.EventTranscriptFinal {
		if err := insertSegmentFromPayload(ctx, tx, meetingID, seq, payloadBytes, nowText); err != nil {
			return nil, err
		}
	}
	if eventType == domain.EventMeetingState {
		if err := updateMeetingState(ctx, tx, meetingID, payloadBytes, nowText); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &domain.Event{Type: eventType, MeetingID: meetingID, Seq: seq, TsMS: now.UnixMilli(), Payload: payloadBytes}, nil
}

func (s *Store) ListEvents(ctx context.Context, meetingID string, afterSeq int64) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT type, meeting_id, seq, ts_ms, payload FROM meeting_events
		WHERE meeting_id = $1 AND seq > $2 ORDER BY seq ASC
	`, meetingID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.Event
	for rows.Next() {
		var event domain.Event
		var payload string
		if err := rows.Scan(&event.Type, &event.MeetingID, &event.Seq, &event.TsMS, &payload); err != nil {
			return nil, err
		}
		event.Payload = json.RawMessage(payload)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) ListSegments(ctx context.Context, meetingID string, afterSeq int64) ([]domain.Segment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT meeting_id, seq, segment_id, speaker_label, text, start_ms, end_ms, created_at
		FROM meeting_segments WHERE meeting_id = $1 AND seq > $2 ORDER BY seq ASC
	`, meetingID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var segments []domain.Segment
	for rows.Next() {
		var segment domain.Segment
		if err := rows.Scan(&segment.MeetingID, &segment.Seq, &segment.SegmentID, &segment.SpeakerLabel, &segment.Text, &segment.StartMS, &segment.EndMS, &segment.CreatedAt); err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	return segments, rows.Err()
}

func updateMeetingState(ctx context.Context, tx *sql.Tx, meetingID string, payload []byte, nowText string) error {
	var state struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(payload, &state); err != nil || state.Status == "" {
		return nil
	}
	if state.Status == "ended" {
		_, err := tx.ExecContext(ctx, `UPDATE meetings SET status = $1, updated_at = $2, ended_at = $3 WHERE id = $4`, state.Status, nowText, nowText, meetingID)
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE meetings SET status = $1, updated_at = $2 WHERE id = $3`, state.Status, nowText, meetingID)
	return err
}
