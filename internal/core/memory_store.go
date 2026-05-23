package core

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

type memoryStore struct {
	mu       sync.Mutex
	meetings map[string]Meeting
	nextSeq  map[string]int64
	events   map[string][]Event
	segments map[string][]Segment
	reports  map[string][]Report
	jobs     map[string]Job
	uploads  map[string]Upload
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		meetings: make(map[string]Meeting),
		nextSeq:  make(map[string]int64),
		events:   make(map[string][]Event),
		segments: make(map[string][]Segment),
		reports:  make(map[string][]Report),
		jobs:     make(map[string]Job),
		uploads:  make(map[string]Upload),
	}
}

func (m *memoryStore) CreateMeeting(_ context.Context, title, source string) (*Meeting, error) {
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
	meeting := Meeting{
		ID:        NewID("m"),
		Title:     title,
		Status:    "created",
		Source:    source,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.meetings[meeting.ID] = meeting
	m.nextSeq[meeting.ID] = 1
	return cloneMeeting(meeting), nil
}

func (m *memoryStore) ListMeetings(_ context.Context) ([]Meeting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meetings := make([]Meeting, 0, len(m.meetings))
	for _, meeting := range m.meetings {
		meetings = append(meetings, meeting)
	}
	sort.Slice(meetings, func(i, j int) bool {
		return meetings[i].CreatedAt > meetings[j].CreatedAt
	})
	return meetings, nil
}

func (m *memoryStore) GetMeeting(_ context.Context, meetingID string) (*Meeting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meeting, ok := m.meetings[meetingID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneMeeting(meeting), nil
}

func (m *memoryStore) AppendEvent(_ context.Context, meetingID, eventType string, payload any) (*Event, error) {
	payloadBytes, err := jsonPayload(payload)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.meetings[meetingID]; !ok {
		return nil, ErrNotFound
	}

	event := Event{
		Type:      eventType,
		MeetingID: meetingID,
		TsMS:      NowMS(),
		Payload:   append(json.RawMessage(nil), payloadBytes...),
	}
	if !IsDurableEventType(eventType) {
		return &event, nil
	}

	event.Seq = m.nextSeq[meetingID]
	m.nextSeq[meetingID]++
	m.events[meetingID] = append(m.events[meetingID], event)

	if eventType == EventTranscriptFinal {
		var segment TranscriptFinalPayload
		if err := json.Unmarshal(payloadBytes, &segment); err == nil {
			if segment.SegmentID == "" {
				segment.SegmentID = "seg_" + time.Now().UTC().Format("150405.000000000")
			}
			if segment.SpeakerLabel == "" {
				segment.SpeakerLabel = "Speaker"
			}
			m.segments[meetingID] = append(m.segments[meetingID], Segment{
				MeetingID:    meetingID,
				Seq:          event.Seq,
				SegmentID:    segment.SegmentID,
				SpeakerLabel: segment.SpeakerLabel,
				Text:         segment.Text,
				StartMS:      segment.StartMS,
				EndMS:        segment.EndMS,
				CreatedAt:    time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	if eventType == EventMeetingState {
		var state struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(payloadBytes, &state); err == nil && state.Status != "" {
			meeting := m.meetings[meetingID]
			meeting.Status = state.Status
			meeting.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if state.Status == "ended" {
				meeting.EndedAt = meeting.UpdatedAt
			}
			m.meetings[meetingID] = meeting
		}
	}

	return cloneEvent(event), nil
}

func (m *memoryStore) ListEvents(_ context.Context, meetingID string, afterSeq int64) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.meetings[meetingID]; !ok {
		return nil, ErrNotFound
	}
	events := make([]Event, 0)
	for _, event := range m.events[meetingID] {
		if event.Seq > afterSeq {
			events = append(events, *cloneEvent(event))
		}
	}
	return events, nil
}

func (m *memoryStore) ListSegments(_ context.Context, meetingID string, afterSeq int64) ([]Segment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.meetings[meetingID]; !ok {
		return nil, ErrNotFound
	}
	segments := make([]Segment, 0)
	for _, segment := range m.segments[meetingID] {
		if segment.Seq > afterSeq {
			segments = append(segments, segment)
		}
	}
	return segments, nil
}

func (m *memoryStore) ResetMeeting(_ context.Context, meetingID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	meeting, ok := m.meetings[meetingID]
	if !ok {
		return ErrNotFound
	}
	meeting.Status = "created"
	meeting.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	meeting.EndedAt = ""
	m.meetings[meetingID] = meeting
	m.nextSeq[meetingID] = 1
	delete(m.events, meetingID)
	delete(m.segments, meetingID)
	delete(m.reports, meetingID)
	return nil
}

func (m *memoryStore) CreateJob(_ context.Context, jobType, meetingID, status string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status == "" {
		status = "queued"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	job := Job{
		ID:        NewID("job"),
		Type:      jobType,
		Status:    status,
		MeetingID: meetingID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.jobs[job.ID] = job
	return cloneJob(job), nil
}

func (m *memoryStore) CompleteJob(_ context.Context, jobID string, result any) error {
	resultBytes, err := jsonPayload(result)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return ErrNotFound
	}
	job.Status = "completed"
	job.Result = append(json.RawMessage(nil), resultBytes...)
	job.Error = ""
	job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	m.jobs[jobID] = job
	return nil
}

func (m *memoryStore) FailJob(_ context.Context, jobID, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return ErrNotFound
	}
	job.Status = "failed"
	job.Error = message
	job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	m.jobs[jobID] = job
	return nil
}

func (m *memoryStore) GetJob(_ context.Context, jobID string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneJob(job), nil
}

func (m *memoryStore) SaveReport(_ context.Context, meetingID, content string) (*Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.meetings[meetingID]; !ok {
		return nil, ErrNotFound
	}
	report := Report{
		ArtifactID: NewID("art"),
		MeetingID:  meetingID,
		Format:     "markdown",
		Content:    content,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	m.reports[meetingID] = append(m.reports[meetingID], report)
	return cloneReport(report), nil
}

func (m *memoryStore) LatestReport(_ context.Context, meetingID string) (*Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	reports := m.reports[meetingID]
	if len(reports) == 0 {
		return nil, ErrNotFound
	}
	return cloneReport(reports[len(reports)-1]), nil
}

func (m *memoryStore) SaveUpload(_ context.Context, filename, mediaType, path, jobID string) (*Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[jobID]; !ok {
		return nil, ErrNotFound
	}
	upload := Upload{
		ID:        NewID("upl"),
		Filename:  filename,
		MediaType: mediaType,
		Path:      path,
		JobID:     jobID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	m.uploads[upload.ID] = upload
	return &upload, nil
}

func cloneMeeting(meeting Meeting) *Meeting {
	return &meeting
}

func cloneEvent(event Event) *Event {
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	return &event
}

func cloneJob(job Job) *Job {
	job.Result = append(json.RawMessage(nil), job.Result...)
	return &job
}

func cloneReport(report Report) *Report {
	return &report
}
