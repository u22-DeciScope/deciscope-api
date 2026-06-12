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
	ID          string
	WorkspaceID string
	Title       string
	Status      string
	Source      string
	CreatedAt   string
	UpdatedAt   string
	EndedAt     string
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
	ID          string
	WorkspaceID string
	Type        string
	Status      string
	MeetingID   string
	Result      json.RawMessage
	Error       string
	CreatedAt   string
	UpdatedAt   string
}

type Report struct {
	ArtifactID string
	MeetingID  string
	Format     string
	Content    string
	CreatedAt  string
}

type Upload struct {
	ID          string
	WorkspaceID string
	Filename    string
	MediaType   string
	Path        string
	JobID       string
	CreatedAt   string
}

type User struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

type Workspace struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type WorkspaceMember struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	JoinedAt    string `json:"joined_at"`
}

type WorkspaceInvitation struct {
	ID              string `json:"id"`
	WorkspaceID     string `json:"workspace_id"`
	Email           string `json:"email"`
	NormalizedEmail string `json:"-"`
	Role            string `json:"role"`
	Status          string `json:"status"`
	InvitedBy       string `json:"invited_by"`
	CreatedAt       string `json:"created_at"`
}

type Session struct {
	ID                 string
	UserID             string
	TokenHash          string
	CurrentWorkspaceID string
	ExpiresAt          string
	CreatedAt          string
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

func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", time.Now().Unix(), 0, 0x4000, 0x8000, time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(b[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
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
