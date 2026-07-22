package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

// MeetingAgendaProgressOverridesRepository persists one manual-override row
// per session in meeting_session_agenda_progress_overrides. There is no
// history: every save is an upsert in place.
type MeetingAgendaProgressOverridesRepository struct {
	db *sql.DB
}

func NewMeetingAgendaProgressOverridesRepository(db *sql.DB) *MeetingAgendaProgressOverridesRepository {
	return &MeetingAgendaProgressOverridesRepository{db: db}
}

func (r *MeetingAgendaProgressOverridesRepository) GetAgendaProgressOverrides(ctx context.Context, sessionID string) (json.RawMessage, error) {
	var payload []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT payload FROM meeting_session_agenda_progress_overrides WHERE session_id = $1
	`, sessionID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: agenda progress overrides not found", domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("query agenda progress overrides: %w", err)
	}
	return append(json.RawMessage(nil), payload...), nil
}

func (r *MeetingAgendaProgressOverridesRepository) UpsertAgendaProgressOverrides(ctx context.Context, sessionID string, payload json.RawMessage, updatedAt time.Time) error {
	if !json.Valid(payload) {
		return fmt.Errorf("agenda progress overrides payload is not valid json")
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO meeting_session_agenda_progress_overrides (session_id, payload, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (session_id) DO UPDATE SET
			payload = EXCLUDED.payload,
			updated_at = EXCLUDED.updated_at
	`, sessionID, []byte(payload), updatedAt.UTC())
	if err != nil {
		return fmt.Errorf("upsert agenda progress overrides: %w", err)
	}
	return nil
}

var _ application.MeetingAgendaProgressOverridesRepository = (*MeetingAgendaProgressOverridesRepository)(nil)
