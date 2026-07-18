package domain

import (
	"encoding/json"
	"time"
)

type MeetingTreeAuditStatus string

const (
	MeetingTreeAuditRunning   MeetingTreeAuditStatus = "running"
	MeetingTreeAuditCompleted MeetingTreeAuditStatus = "completed"
	MeetingTreeAuditSkipped   MeetingTreeAuditStatus = "skipped"
	MeetingTreeAuditFailed    MeetingTreeAuditStatus = "failed"
)

type MeetingTreeAuditTriggerClass string

const (
	MeetingTreeAuditTriggerNormal MeetingTreeAuditTriggerClass = "normal"
	MeetingTreeAuditTriggerHigh   MeetingTreeAuditTriggerClass = "high"
	MeetingTreeAuditTriggerFinal  MeetingTreeAuditTriggerClass = "final"
)

// MeetingTreeAuditRun is an append-only audit history entry. Potentially
// large, model-produced details are bounded by the application before this
// entity reaches a repository adapter.
type MeetingTreeAuditRun struct {
	ID                    string
	SessionID             string
	BasedOnTreeVersion    int64
	ResultingTreeVersion  int64
	TriggerReason         string
	TriggerClass          MeetingTreeAuditTriggerClass
	Task                  string
	Deployment            string
	Model                 string
	PromptVersion         string
	SnapshotHash          string
	Status                MeetingTreeAuditStatus
	Result                string
	Disposition           string
	SuppressionReason     string
	ProviderCalled        bool
	MeetingElapsedSeconds int64
	InputSummary          json.RawMessage
	InputPayload          json.RawMessage
	RawResponse           string
	Findings              json.RawMessage
	Operations            json.RawMessage
	ValidatorResult       json.RawMessage
	PromptTokens          int
	CompletionTokens      int
	ElapsedMilliseconds   int64
	ErrorCode             string
	ErrorMessage          string
	CreatedAt             time.Time
	CompletedAt           *time.Time
}
