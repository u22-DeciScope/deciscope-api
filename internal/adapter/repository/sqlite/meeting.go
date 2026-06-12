package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

func (s *Store) CreateMeeting(ctx context.Context, title, source string) (*domain.Meeting, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled meeting"
	}
	if source == "" {
		source = "fixture_replay"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	meeting := &domain.Meeting{
		ID: domain.NewID("m"), Title: title, Status: "created", Source: source,
		CreatedAt: now, UpdatedAt: now,
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meetings (id, title, status, source, next_seq, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)
	`, meeting.ID, meeting.Title, meeting.Status, meeting.Source, meeting.CreatedAt, meeting.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return meeting, nil
}

func (s *Store) ListMeetings(ctx context.Context) ([]domain.Meeting, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, status, source, created_at, updated_at, COALESCE(ended_at, '')
		FROM meetings ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var meetings []domain.Meeting
	for rows.Next() {
		var meeting domain.Meeting
		if err := rows.Scan(&meeting.ID, &meeting.Title, &meeting.Status, &meeting.Source, &meeting.CreatedAt, &meeting.UpdatedAt, &meeting.EndedAt); err != nil {
			return nil, err
		}
		meetings = append(meetings, meeting)
	}
	return meetings, rows.Err()
}

func (s *Store) GetMeeting(ctx context.Context, meetingID string) (*domain.Meeting, error) {
	var meeting domain.Meeting
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, status, source, created_at, updated_at, COALESCE(ended_at, '')
		FROM meetings WHERE id = ?
	`, meetingID).Scan(&meeting.ID, &meeting.Title, &meeting.Status, &meeting.Source, &meeting.CreatedAt, &meeting.UpdatedAt, &meeting.EndedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &meeting, err
}

func (s *Store) ResetMeeting(ctx context.Context, meetingID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`DELETE FROM meeting_reports WHERE meeting_id = ?`,
		`DELETE FROM meeting_segments WHERE meeting_id = ?`,
		`DELETE FROM meeting_events WHERE meeting_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, meetingID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE meetings SET status = 'created', next_seq = 1, ended_at = NULL, updated_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), meetingID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}
