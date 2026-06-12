package application

import (
	"context"
	"fmt"
	"io"

	"deciscope-core-api/internal/domain"
)

func (s *Service) GetJob(ctx context.Context, jobID string) (*domain.Job, error) {
	return s.jobs.GetJob(ctx, jobID)
}

func (s *Service) UploadFile(ctx context.Context, filename, mediaType string, src io.Reader) (*UploadResult, error) {
	job, err := s.jobs.CreateJob(ctx, "file.extract_audio", "", "completed")
	if err != nil {
		return nil, fmt.Errorf("create upload job: %w", err)
	}
	if s.storage == nil {
		return nil, fmt.Errorf("upload storage is unavailable")
	}
	path, err := s.storage.Save(ctx, job.ID+"_"+filename, src)
	if err != nil {
		return nil, err
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	upload, err := s.uploads.SaveUpload(ctx, filename, mediaType, path, job.ID)
	if err != nil {
		return nil, fmt.Errorf("save upload record: %w", err)
	}
	if err := s.jobs.CompleteJob(ctx, job.ID, map[string]any{"upload_id": upload.ID, "mode": "mock-local"}); err != nil {
		return nil, fmt.Errorf("complete upload job: %w", err)
	}
	job, err = s.jobs.GetJob(ctx, job.ID)
	if err != nil {
		return nil, fmt.Errorf("load upload job: %w", err)
	}
	return &UploadResult{Upload: upload, Job: job}, nil
}
