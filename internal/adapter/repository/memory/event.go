package memory

import (
	"context"
	"encoding/json"
	"time"

	"deciscope-core-api/internal/domain"
)

func (m *MemoryStore) AppendEvent(_ context.Context, meetingID, eventType string, payload any) (*domain.Event, error) {
	payloadBytes, err := jsonPayload(payload)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.meetings[meetingID]; !ok {
		return nil, domain.ErrNotFound
	}
	event := domain.Event{Type: eventType, MeetingID: meetingID, TsMS: domain.NowMS(), Payload: append(json.RawMessage(nil), payloadBytes...)}
	if !domain.IsDurableEventType(eventType) {
		return &event, nil
	}
	event.Seq = m.nextSeq[meetingID]
	m.nextSeq[meetingID]++
	m.events[meetingID] = append(m.events[meetingID], event)
	if eventType == domain.EventTranscriptFinal {
		m.appendSegment(meetingID, event.Seq, payloadBytes)
	}
	if eventType == domain.EventMeetingState {
		m.updateMeetingState(meetingID, payloadBytes)
	}
	return cloneEvent(event), nil
}

func (m *MemoryStore) ListEvents(_ context.Context, meetingID string, afterSeq int64) ([]domain.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.meetings[meetingID]; !ok {
		return nil, domain.ErrNotFound
	}
	var events []domain.Event
	for _, event := range m.events[meetingID] {
		if event.Seq > afterSeq {
			events = append(events, *cloneEvent(event))
		}
	}
	return events, nil
}

func (m *MemoryStore) ListSegments(_ context.Context, meetingID string, afterSeq int64) ([]domain.Segment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.meetings[meetingID]; !ok {
		return nil, domain.ErrNotFound
	}
	var segments []domain.Segment
	for _, segment := range m.segments[meetingID] {
		if segment.Seq > afterSeq {
			segments = append(segments, segment)
		}
	}
	return segments, nil
}

func (m *MemoryStore) appendSegment(meetingID string, seq int64, payload []byte) {
	var segment domain.TranscriptFinalPayload
	if json.Unmarshal(payload, &segment) != nil {
		return
	}
	if segment.SegmentID == "" {
		segment.SegmentID = "seg_" + time.Now().UTC().Format("150405.000000000")
	}
	if segment.SpeakerLabel == "" {
		segment.SpeakerLabel = "Speaker"
	}
	m.segments[meetingID] = append(m.segments[meetingID], domain.Segment{
		MeetingID: meetingID, Seq: seq, SegmentID: segment.SegmentID, SpeakerLabel: segment.SpeakerLabel,
		Text: segment.Text, StartMS: segment.StartMS, EndMS: segment.EndMS, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func (m *MemoryStore) updateMeetingState(meetingID string, payload []byte) {
	var state struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(payload, &state) != nil || state.Status == "" {
		return
	}
	meeting := m.meetings[meetingID]
	meeting.Status, meeting.UpdatedAt = state.Status, time.Now().UTC().Format(time.RFC3339)
	if state.Status == "ended" {
		meeting.EndedAt = meeting.UpdatedAt
	}
	m.meetings[meetingID] = meeting
}

func cloneEvent(event domain.Event) *domain.Event {
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	return &event
}
