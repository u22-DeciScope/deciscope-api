package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"deciscope-core-api/internal/domain"
)

func insertSegmentFromPayload(ctx context.Context, tx *sql.Tx, meetingID string, seq int64, payload []byte, nowText string) error {
	var segment domain.TranscriptFinalPayload
	if err := json.Unmarshal(payload, &segment); err != nil {
		return fmt.Errorf("parse transcript.final payload: %w", err)
	}
	if segment.SegmentID == "" {
		segment.SegmentID = fmt.Sprintf("seg_%06d", seq)
	}
	if segment.SpeakerLabel == "" {
		segment.SpeakerLabel = "Speaker"
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO meeting_segments (meeting_id, seq, segment_id, speaker_label, text, start_ms, end_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(meeting_id, segment_id) DO UPDATE SET
			seq = excluded.seq, speaker_label = excluded.speaker_label, text = excluded.text,
			start_ms = excluded.start_ms, end_ms = excluded.end_ms
	`, meetingID, seq, segment.SegmentID, segment.SpeakerLabel, segment.Text, segment.StartMS, segment.EndMS, nowText)
	return err
}

func jsonPayload(payload any) (json.RawMessage, error) {
	switch p := payload.(type) {
	case nil:
		return json.RawMessage(`{}`), nil
	case json.RawMessage:
		if len(p) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return p, nil
	case []byte:
		if len(p) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return json.RawMessage(p), nil
	default:
		return json.Marshal(payload)
	}
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
