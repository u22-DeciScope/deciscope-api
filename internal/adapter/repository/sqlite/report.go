package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"deciscope-core-api/internal/domain"
)

func (s *Store) SaveReport(ctx context.Context, meetingID, content string) (*domain.Report, error) {
	report := &domain.Report{
		ArtifactID: domain.NewID("art"), MeetingID: meetingID, Format: "markdown",
		Content: content, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meeting_reports (artifact_id, meeting_id, format, content, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, report.ArtifactID, report.MeetingID, report.Format, report.Content, report.CreatedAt)
	return report, err
}

func (s *Store) LatestReport(ctx context.Context, meetingID string) (*domain.Report, error) {
	var report domain.Report
	err := s.db.QueryRowContext(ctx, `
		SELECT artifact_id, meeting_id, format, content, created_at FROM meeting_reports
		WHERE meeting_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1
	`, meetingID).Scan(&report.ArtifactID, &report.MeetingID, &report.Format, &report.Content, &report.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &report, err
}
