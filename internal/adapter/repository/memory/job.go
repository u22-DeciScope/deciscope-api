package memory

import (
	"context"
	"encoding/json"
	"time"

	"deciscope-core-api/internal/domain"
)

func (m *MemoryStore) CreateJob(_ context.Context, workspaceID, jobType, meetingID, status string) (*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status == "" {
		status = "queued"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	job := domain.Job{ID: domain.NewID("job"), WorkspaceID: workspaceID, Type: jobType, Status: status, MeetingID: meetingID, CreatedAt: now, UpdatedAt: now}
	m.jobs[job.ID] = job
	return cloneJob(job), nil
}

func (m *MemoryStore) CompleteJob(_ context.Context, jobID string, result any) error {
	resultBytes, err := jsonPayload(result)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return domain.ErrNotFound
	}
	job.Status, job.Result, job.Error, job.UpdatedAt = "completed", append(json.RawMessage(nil), resultBytes...), "", time.Now().UTC().Format(time.RFC3339)
	m.jobs[jobID] = job
	return nil
}

func (m *MemoryStore) FailJob(_ context.Context, jobID, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return domain.ErrNotFound
	}
	job.Status, job.Error, job.UpdatedAt = "failed", message, time.Now().UTC().Format(time.RFC3339)
	m.jobs[jobID] = job
	return nil
}

func (m *MemoryStore) GetJob(_ context.Context, jobID string) (*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return cloneJob(job), nil
}

func cloneJob(job domain.Job) *domain.Job {
	job.Result = append(json.RawMessage(nil), job.Result...)
	return &job
}
