package memory

import (
	"context"
	"time"

	"deciscope-core-api/internal/domain"
)

func (m *MemoryStore) SaveUpload(_ context.Context, workspaceID, filename, mediaType, path, jobID string) (*domain.Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[jobID]; !ok {
		return nil, domain.ErrNotFound
	}
	upload := domain.Upload{
		ID: domain.NewID("upl"), WorkspaceID: workspaceID, Filename: filename, MediaType: mediaType,
		Path: path, JobID: jobID, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	m.uploads[upload.ID] = upload
	return &upload, nil
}
