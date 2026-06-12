package httpadapter

import (
	"encoding/json"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

type meetingResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	EndedAt   string `json:"ended_at,omitempty"`
}

type eventResponse struct {
	Type      string          `json:"type"`
	MeetingID string          `json:"meeting_id"`
	Seq       int64           `json:"seq,omitempty"`
	TsMS      int64           `json:"ts_ms"`
	Payload   json.RawMessage `json:"payload"`
}

type segmentResponse struct {
	MeetingID    string `json:"meeting_id"`
	Seq          int64  `json:"seq"`
	SegmentID    string `json:"segment_id"`
	SpeakerLabel string `json:"speaker_label"`
	Text         string `json:"text"`
	StartMS      int64  `json:"start_ms"`
	EndMS        int64  `json:"end_ms"`
	CreatedAt    string `json:"created_at"`
}

type reportResponse struct {
	ArtifactID string `json:"artifact_id"`
	MeetingID  string `json:"meeting_id"`
	Format     string `json:"format"`
	Content    string `json:"content"`
	CreatedAt  string `json:"created_at"`
}

type jobResponse struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	MeetingID string          `json:"meeting_id,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

type uploadResponse struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Path      string `json:"path"`
	JobID     string `json:"job_id"`
	CreatedAt string `json:"created_at"`
}

type replayStatusResponse struct {
	MeetingID string `json:"meeting_id"`
	Fixture   string `json:"fixture"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at,omitempty"`
}

type fixtureResponse struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func meetingDTO(v domain.Meeting) meetingResponse {
	return meetingResponse(v)
}

func eventDTO(v domain.Event) eventResponse {
	return eventResponse(v)
}

func segmentDTO(v domain.Segment) segmentResponse {
	return segmentResponse(v)
}

func reportDTO(v domain.Report) reportResponse {
	return reportResponse(v)
}

func jobDTO(v domain.Job) jobResponse {
	return jobResponse(v)
}

func uploadDTO(v domain.Upload) uploadResponse {
	return uploadResponse(v)
}

func replayStatusDTO(v application.ReplayStatus) replayStatusResponse {
	return replayStatusResponse(v)
}

func fixtureDTOs(values []application.FixtureInfo) []fixtureResponse {
	if values == nil {
		return nil
	}
	result := make([]fixtureResponse, len(values))
	for i, value := range values {
		result[i] = fixtureResponse(value)
	}
	return result
}

func meetingDTOs(values []domain.Meeting) []meetingResponse {
	if values == nil {
		return nil
	}
	result := make([]meetingResponse, len(values))
	for i, value := range values {
		result[i] = meetingDTO(value)
	}
	return result
}

func eventDTOs(values []domain.Event) []eventResponse {
	if values == nil {
		return nil
	}
	result := make([]eventResponse, len(values))
	for i, value := range values {
		result[i] = eventDTO(value)
	}
	return result
}

func segmentDTOs(values []domain.Segment) []segmentResponse {
	if values == nil {
		return nil
	}
	result := make([]segmentResponse, len(values))
	for i, value := range values {
		result[i] = segmentDTO(value)
	}
	return result
}
