package domain

import (
	"encoding/json"
	"time"
)

type MeetingAIAnalysisType string

const (
	MeetingAIAnalysisLive  MeetingAIAnalysisType = "live"
	MeetingAIAnalysisFinal MeetingAIAnalysisType = "final"
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
}

func ValidMeetingAIAnalysisType(analysisType MeetingAIAnalysisType) bool {
	switch analysisType {
	case MeetingAIAnalysisLive, MeetingAIAnalysisFinal:
		return true
	default:
		return false
	}
}

func ValidMeetingAIAnalysisStatus(status MeetingAIAnalysisStatus) bool {
	switch status {
	case MeetingAIAnalysisRunning, MeetingAIAnalysisCompleted, MeetingAIAnalysisFailed:
		return true
	default:
		return false
	}
}
