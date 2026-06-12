package domain

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
	ID        string
	Title     string
	Status    string
	Source    string
	CreatedAt string
	UpdatedAt string
	EndedAt   string
}

type Event struct {
	Type      string
	MeetingID string
	Seq       int64
	TsMS      int64
	Payload   json.RawMessage
}

type Segment struct {
	MeetingID    string
	Seq          int64
	SegmentID    string
	SpeakerLabel string
	Text         string
	StartMS      int64
	EndMS        int64
	CreatedAt    string
}

type Job struct {
	ID        string
	Type      string
	Status    string
	MeetingID string
	Result    json.RawMessage
	Error     string
	CreatedAt string
	UpdatedAt string
}

type Report struct {
	ArtifactID string
	MeetingID  string
	Format     string
	Content    string
	CreatedAt  string
}

type Upload struct {
	ID        string
	Filename  string
	MediaType string
	Path      string
	JobID     string
	CreatedAt string
}

type User struct {
	ID    int64
	Name  string
	Email string
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
