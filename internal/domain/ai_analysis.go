package domain

import (
	"encoding/json"
	"time"
)

type MeetingAIAnalysisType string

const (
	MeetingAIAnalysisLive  MeetingAIAnalysisType = "live"
	MeetingAIAnalysisFinal MeetingAIAnalysisType = "final"
	// MeetingAIAnalysisContext is the structured pre-meeting context
	// (purpose/background/agenda items/AI directives) normalized once at
	// meeting start and shared by every AI task.
	MeetingAIAnalysisContext MeetingAIAnalysisType = "context"
	// MeetingAIAnalysisTree is the durable discussion tree snapshot written
	// at meeting end (and on manual regeneration), so the history view never
	// depends on the live payload alone.
	MeetingAIAnalysisTree MeetingAIAnalysisType = "tree"
	// MeetingAIAnalysisFinalization records the durable progress and outcome
	// of the meeting-end pipeline. It is exposed as an additive API field so a
	// client can distinguish "bot stopped" from "final artifacts are ready".
	MeetingAIAnalysisFinalization MeetingAIAnalysisType = "finalization"
)

type MeetingAIAnalysisStatus string

const (
	MeetingAIAnalysisRunning   MeetingAIAnalysisStatus = "running"
	MeetingAIAnalysisCompleted MeetingAIAnalysisStatus = "completed"
	MeetingAIAnalysisFailed    MeetingAIAnalysisStatus = "failed"
)

// MeetingAIAnalysis is the persisted AI analysis state for a meeting session.
// One row exists per (SessionID, Type); there is no history, each analysis is
// upserted in place. Payload keeps the most recent successful result even when
// Status is Failed, so consumers can keep showing the latest good analysis.
type MeetingAIAnalysis struct {
	SessionID    string
	Type         MeetingAIAnalysisType
	Status       MeetingAIAnalysisStatus
	Version      int64
	Payload      json.RawMessage
	Model        string
	SegmentCount int
	InputChars   int
	LastError    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// IntervalSeconds is the live analysis check interval in seconds. It is
	// a broadcast hint for clients ("next update in about N seconds") set by
	// the application service on published live analyses; it is not
	// persisted.
	IntervalSeconds int
	// The following fields are ephemeral live-running status metadata. They
	// identify the sealed request without attaching the previous item/tree
	// snapshot to a running event and are not persisted by repositories.
	RequestedAnalysisVersion int64
	TargetFromSequenceNo     int64
	TargetThroughSequenceNo  int64
	StartedAt                time.Time
}
