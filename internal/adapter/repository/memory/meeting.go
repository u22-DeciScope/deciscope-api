package memory

import (
	"context"
	"sort"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

func (m *MemoryStore) CreateMeeting(_ context.Context, title, source string) (*domain.Meeting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled meeting"
	}
	if source == "" {
		source = "fixture_replay"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	meeting := domain.Meeting{
		ID: domain.NewID("m"), Title: title, Status: "created", Source: source,
		CreatedAt: now, UpdatedAt: now,
	}
	m.meetings[meeting.ID], m.nextSeq[meeting.ID] = meeting, 1
	return cloneMeeting(meeting), nil
}

func (m *MemoryStore) ListMeetings(_ context.Context) ([]domain.Meeting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meetings := make([]domain.Meeting, 0, len(m.meetings))
	for _, meeting := range m.meetings {
		meetings = append(meetings, meeting)
	}
	sort.Slice(meetings, func(i, j int) bool { return meetings[i].CreatedAt > meetings[j].CreatedAt })
	return meetings, nil
}

func (m *MemoryStore) GetMeeting(_ context.Context, meetingID string) (*domain.Meeting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meeting, ok := m.meetings[meetingID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return cloneMeeting(meeting), nil
}

func (m *MemoryStore) ResetMeeting(_ context.Context, meetingID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	meeting, ok := m.meetings[meetingID]
	if !ok {
		return domain.ErrNotFound
	}
	meeting.Status, meeting.UpdatedAt, meeting.EndedAt = "created", time.Now().UTC().Format(time.RFC3339), ""
	m.meetings[meetingID], m.nextSeq[meetingID] = meeting, 1
	delete(m.events, meetingID)
	delete(m.segments, meetingID)
	delete(m.reports, meetingID)
	return nil
}

func cloneMeeting(meeting domain.Meeting) *domain.Meeting {
	return &meeting
}
