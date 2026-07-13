package application_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

func TestServiceCoreUseCasesWithFakePorts(t *testing.T) {
	ctx := context.Background()
	ports := newFakePorts()
	service := application.NewService(ports, ports, ports, ports, ports)

	meeting, err := service.CreateMeeting(ctx, "w_test", "Service use cases", "fixture_replay")
	if err != nil {
		t.Fatalf("CreateMeeting() error = %v", err)
	}
	if len(ports.published) != 1 || ports.published[0].Type != domain.EventMeetingState {
		t.Fatalf("published events = %+v", ports.published)
	}

	token, err := service.CreateJoinToken(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("CreateJoinToken() error = %v", err)
	}
	if !strings.HasPrefix(token.Token, "local."+meeting.ID+".") {
		t.Fatalf("token = %q, want meeting-scoped local token", token.Token)
	}

	report, err := service.GetOrCreateReport(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("GetOrCreateReport() error = %v", err)
	}
	if report.MeetingID != meeting.ID || !strings.Contains(report.Content, "# Service use cases") {
		t.Fatalf("report = %+v", report)
	}
}

type fakePorts struct {
	meeting   *domain.Meeting
	events    []domain.Event
	reports   []domain.Report
	jobs      map[string]domain.Job
	published []domain.Event
}

func newFakePorts() *fakePorts {
	return &fakePorts{jobs: make(map[string]domain.Job)}
}

func (f *fakePorts) CreateMeeting(_ context.Context, workspaceID, title, source string) (*domain.Meeting, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	f.meeting = &domain.Meeting{ID: "m_test", WorkspaceID: workspaceID, Title: title, Status: "created", Source: source, CreatedAt: now, UpdatedAt: now}
	return f.meeting, nil
}

func (f *fakePorts) ListMeetings(context.Context, string) ([]domain.Meeting, error) {
	if f.meeting == nil {
		return nil, nil
	}
	return []domain.Meeting{*f.meeting}, nil
}

func (f *fakePorts) GetMeeting(_ context.Context, meetingID string) (*domain.Meeting, error) {
	if f.meeting == nil || f.meeting.ID != meetingID {
		return nil, domain.ErrNotFound
	}
	copy := *f.meeting
	return &copy, nil
}

func (f *fakePorts) ResetMeeting(_ context.Context, meetingID string) error {
	if f.meeting == nil || f.meeting.ID != meetingID {
		return domain.ErrNotFound
	}
	f.events = nil
	f.meeting.Status = "created"
	return nil
}

func (f *fakePorts) AppendEvent(_ context.Context, meetingID, eventType string, payload any) (*domain.Event, error) {
	if f.meeting == nil || f.meeting.ID != meetingID {
		return nil, domain.ErrNotFound
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	event := domain.Event{MeetingID: meetingID, Type: eventType, Seq: int64(len(f.events) + 1), TsMS: domain.NowMS(), Payload: payloadJSON}
	f.events = append(f.events, event)
	return &event, nil
}

func (f *fakePorts) ListEvents(_ context.Context, meetingID string, afterSeq int64) ([]domain.Event, error) {
	if f.meeting == nil || f.meeting.ID != meetingID {
		return nil, domain.ErrNotFound
	}
	var events []domain.Event
	for _, event := range f.events {
		if event.Seq > afterSeq {
			events = append(events, event)
		}
	}
	return events, nil
}

func (f *fakePorts) ListSegments(context.Context, string, int64) ([]domain.Segment, error) {
	return nil, nil
}

func (f *fakePorts) SaveReport(_ context.Context, meetingID, content string) (*domain.Report, error) {
	report := domain.Report{ArtifactID: "art_test", MeetingID: meetingID, Format: "markdown", Content: content}
	f.reports = append(f.reports, report)
	return &report, nil
}

func (f *fakePorts) LatestReport(context.Context, string) (*domain.Report, error) {
	if len(f.reports) == 0 {
		return nil, domain.ErrNotFound
	}
	report := f.reports[len(f.reports)-1]
	return &report, nil
}

func (f *fakePorts) CreateJob(_ context.Context, workspaceID, jobType, meetingID, status string) (*domain.Job, error) {
	job := domain.Job{ID: "job_test", WorkspaceID: workspaceID, Type: jobType, MeetingID: meetingID, Status: status}
	f.jobs[job.ID] = job
	return &job, nil
}

func (f *fakePorts) CompleteJob(_ context.Context, jobID string, result any) error {
	job, ok := f.jobs[jobID]
	if !ok {
		return domain.ErrNotFound
	}
	job.Status = "completed"
	job.Result, _ = json.Marshal(result)
	f.jobs[jobID] = job
	return nil
}

func (f *fakePorts) FailJob(_ context.Context, jobID, message string) error {
	job, ok := f.jobs[jobID]
	if !ok {
		return domain.ErrNotFound
	}
	job.Status, job.Error = "failed", message
	f.jobs[jobID] = job
	return nil
}

func (f *fakePorts) GetJob(_ context.Context, jobID string) (*domain.Job, error) {
	job, ok := f.jobs[jobID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &job, nil
}

func (f *fakePorts) Publish(event domain.Event) {
	f.published = append(f.published, event)
}
