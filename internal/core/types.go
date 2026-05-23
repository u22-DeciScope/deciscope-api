package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	EventMeetingState        = "meeting.state"
	EventTranscriptPartial   = "transcript.partial"
	EventTranscriptFinal     = "transcript.final"
	EventAnalysisDelta       = "analysis.delta"
	EventTreeUpdate          = "tree.update"
	EventSpeakerSummaryDelta = "speaker.summary.delta"
	EventReportReady         = "report.ready"
	EventError               = "error"
)

type Meeting struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	EndedAt   string `json:"ended_at,omitempty"`
}

type Event struct {
	Type      string          `json:"type"`
	MeetingID string          `json:"meeting_id"`
	Seq       int64           `json:"seq,omitempty"`
	TsMS      int64           `json:"ts_ms"`
	Payload   json.RawMessage `json:"payload"`
}

type Segment struct {
	MeetingID    string `json:"meeting_id"`
	Seq          int64  `json:"seq"`
	SegmentID    string `json:"segment_id"`
	SpeakerLabel string `json:"speaker_label"`
	Text         string `json:"text"`
	StartMS      int64  `json:"start_ms"`
	EndMS        int64  `json:"end_ms"`
	CreatedAt    string `json:"created_at"`
}

type Job struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	MeetingID string          `json:"meeting_id,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

type Report struct {
	ArtifactID string `json:"artifact_id"`
	MeetingID  string `json:"meeting_id"`
	Format     string `json:"format"`
	Content    string `json:"content"`
	CreatedAt  string `json:"created_at"`
}

type Upload struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Path      string `json:"path"`
	JobID     string `json:"job_id"`
	CreatedAt string `json:"created_at"`
}

type TranscriptFinalPayload struct {
	SegmentID    string `json:"segment_id"`
	SpeakerLabel string `json:"speaker_label"`
	Text         string `json:"text"`
	StartMS      int64  `json:"start_ms"`
	EndMS        int64  `json:"end_ms"`
}

func IsDurableEventType(eventType string) bool {
	switch eventType {
	case EventTranscriptFinal, EventAnalysisDelta, EventTreeUpdate, EventSpeakerSummaryDelta, EventMeetingState, EventReportReady, EventError:
		return true
	default:
		return false
	}
}

func NowMS() int64 {
	return time.Now().UTC().UnixMilli()
}

func NewID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func NormalizeFixtureName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return "demo.jsonl"
	}
	return name
}
