package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"deciscope-core-api/internal/domain"
)

func (s *Store) CreateJob(ctx context.Context, workspaceID, jobType, meetingID, status string) (*domain.Job, error) {
	if status == "" {
		status = "queued"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	job := &domain.Job{ID: domain.NewID("job"), WorkspaceID: workspaceID, Type: jobType, Status: status, MeetingID: meetingID, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (id, workspace_id, type, status, meeting_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, job.ID, job.WorkspaceID, job.Type, job.Status, nullable(job.MeetingID), job.CreatedAt, job.UpdatedAt)
	return job, err
}

func (s *Store) CompleteJob(ctx context.Context, jobID string, result any) error {
	resultBytes, err := jsonPayload(result)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'completed', result = $1, error = NULL, updated_at = $2 WHERE id = $3`, string(resultBytes), time.Now().UTC().Format(time.RFC3339), jobID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) FailJob(ctx context.Context, jobID, message string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'failed', error = $1, updated_at = $2 WHERE id = $3`, message, time.Now().UTC().Format(time.RFC3339), jobID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) GetJob(ctx context.Context, jobID string) (*domain.Job, error) {
	var job domain.Job
	var result, errText, meetingID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(workspace_id, ''), type, status, meeting_id, result, error, created_at, updated_at
		FROM jobs WHERE id = $1
	`, jobID).Scan(&job.ID, &job.WorkspaceID, &job.Type, &job.Status, &meetingID, &result, &errText, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	job.MeetingID, job.Error = meetingID.String, errText.String
	if result.Valid && result.String != "" {
		job.Result = json.RawMessage(result.String)
	}
	return &job, nil
}
