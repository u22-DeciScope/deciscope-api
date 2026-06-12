package memory

import (
	"context"
	"time"

	"deciscope-core-api/internal/domain"
)

func (m *MemoryStore) SaveReport(_ context.Context, meetingID, content string) (*domain.Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.meetings[meetingID]; !ok {
		return nil, domain.ErrNotFound
	}
	report := domain.Report{
		ArtifactID: domain.NewID("art"), MeetingID: meetingID, Format: "markdown",
		Content: content, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	m.reports[meetingID] = append(m.reports[meetingID], report)
	return cloneReport(report), nil
}

func (m *MemoryStore) LatestReport(_ context.Context, meetingID string) (*domain.Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	reports := m.reports[meetingID]
	if len(reports) == 0 {
		return nil, domain.ErrNotFound
	}
	return cloneReport(reports[len(reports)-1]), nil
}

func cloneReport(report domain.Report) *domain.Report {
	return &report
}
