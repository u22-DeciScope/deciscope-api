package sqlite

import (
	"context"
	"time"

	"deciscope-core-api/internal/domain"
)

func (s *Store) SaveUpload(ctx context.Context, workspaceID, filename, mediaType, path, jobID string) (*domain.Upload, error) {
	upload := &domain.Upload{
		ID: domain.NewID("upl"), WorkspaceID: workspaceID, Filename: filename, MediaType: mediaType,
		Path: path, JobID: jobID, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO uploads (id, workspace_id, filename, media_type, path, job_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, upload.ID, upload.WorkspaceID, upload.Filename, upload.MediaType, upload.Path, upload.JobID, upload.CreatedAt)
	return upload, err
}
