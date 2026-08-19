package application

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

// ErrMeetingFinalizationAlreadyCompleted is returned when a retry targets a
// finalization that already produced a final summary. Callers translate it to
// an idempotent no-op response.
var ErrMeetingFinalizationAlreadyCompleted = errors.New("meeting finalization already completed")

// ErrMeetingFinalizationRetryUnavailable is returned when final analysis is
// not configured, so no retry can be honored.
var ErrMeetingFinalizationRetryUnavailable = errors.New("meeting finalization retry is unavailable")

// Finalization state machine. The durable row keeps using the three
// domain-level analysis statuses (running/completed/failed); these values live
// inside the finalization payload so the web can tell "still generating" from
// "failed" and from "ended without ever starting the summary" without a schema
// migration.
const (
	finalizationStatusNotStarted                = "not_started"
	finalizationStatusWaitingForTranscriptDrain = "waiting_for_transcript_drain"
	finalizationStatusWaitingForLiveAnalysis    = "waiting_for_live_analysis"
	finalizationStatusBuildingFinalTree         = "building_final_tree"
	finalizationStatusGeneratingSummary         = "generating_summary"
	finalizationStatusCompleted                 = "completed"
	finalizationStatusFailed                    = "failed"
	finalizationStatusIncomplete                = "incomplete"
)

const (
	finalizationErrorCodeLiveWaitTimeout      = "live_wait_timeout"
	finalizationErrorCodeFinalFlushFailed     = "final_flush_failed"
	finalizationErrorCodeSummaryFailed        = "final_summary_failed"
	finalizationErrorCodeSummaryPersistFailed = "final_summary_persist_failed"
	finalizationErrorCodeSummaryLookupFailed  = "final_summary_lookup_failed"
	finalizationErrorCodeCompleterMissing     = "final_summary_completer_missing"
	finalizationErrorCodeIncompleteCoverage   = "incomplete_transcript_coverage"
	finalizationErrorCodeAbandonedAttempt     = "abandoned_finalization_attempt"
)

// finalizationAbandonedAfter bounds how long a durable running finalization row
// may stay untouched before a reader treats it as an attempt whose owning
// process disappeared. It is deliberately much larger than a normal
// finalization (transcript drain + live flush + review + summary) so a slow but
// live attempt is never mislabeled.
const finalizationAbandonedAfter = 15 * time.Minute

// finalizationWaitOperation names one thing the finalization barrier waited
// for. Only identifiers, sequence numbers and timings are recorded so the
// durable payload and the logs never carry meeting text.
type finalizationWaitOperation struct {
	OperationID       string `json:"operationId,omitempty"`
	Type              string `json:"type"`
	Generation        uint64 `json:"generation,omitempty"`
	Trigger           string `json:"trigger,omitempty"`
	FromSequenceNo    int64  `json:"fromSequenceNo,omitempty"`
	ThroughSequenceNo int64  `json:"throughSequenceNo,omitempty"`
	WaitMs            int64  `json:"waitMs"`
	Result            string `json:"result"`
}

const (
	finalizationWaitOperationLiveRound = "live_analysis_round"

	finalizationWaitResultCompleted  = "completed"
	finalizationWaitResultTimedOut   = "timeout"
	finalizationWaitResultSuperseded = "superseded"
)

// finalizationStatusForStage maps the durable stage marker to the state machine
// value. Stages are kept unchanged so existing dashboards and tests that read
// `stage` keep working.
func finalizationStatusForStage(stage string) string {
	switch strings.TrimSpace(stage) {
	case "", "requested":
		return finalizationStatusNotStarted
	case "waiting_for_transcript_drain":
		return finalizationStatusWaitingForTranscriptDrain
	case "waiting_for_live_analysis":
		return finalizationStatusWaitingForLiveAnalysis
	case "final_flush_completed", "final_tree_review_completed", "tree_saved":
		return finalizationStatusBuildingFinalTree
	case "final_summary_running":
		return finalizationStatusGeneratingSummary
	case "completed":
		return finalizationStatusCompleted
	default:
		return finalizationStatusFailed
	}
}

// projectFinalizationForDelivery reports an abandoned attempt as a retryable
// failure instead of leaving the web on a permanent "generating" spinner. It is
// a read-side projection: the durable row is left untouched so a slow attempt
// that is still alive can still finish and overwrite it.
func projectFinalizationForDelivery(
	analysis *domain.MeetingAIAnalysis,
	inFlight bool,
	now time.Time,
) *domain.MeetingAIAnalysis {
	if analysis == nil || analysis.Status != domain.MeetingAIAnalysisRunning || inFlight {
		return analysis
	}
	if analysis.UpdatedAt.IsZero() || now.Sub(analysis.UpdatedAt.UTC()) < finalizationAbandonedAfter {
		return analysis
	}
	var payload finalizationProgressPayload
	if len(analysis.Payload) > 0 {
		if err := json.Unmarshal(analysis.Payload, &payload); err != nil {
			return analysis
		}
	}
	payload.FinalizationStatus = finalizationStatusFailed
	payload.FinalizationIncomplete = true
	payload.Retryable = true
	payload.FinalizationErrorCode = finalizationErrorCodeAbandonedAttempt
	payload.FinalizationErrorMessage = "finalization attempt was abandoned before completion"
	payload.FinalizationFailedAt = now.UTC().Format(time.RFC3339Nano)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return analysis
	}
	projected := *analysis
	projected.Status = domain.MeetingAIAnalysisFailed
	projected.Payload = encoded
	if strings.TrimSpace(projected.LastError) == "" {
		projected.LastError = "finalization attempt was abandoned before completion"
	}
	return &projected
}

// finalizationRetryable reports whether the durable finalization state allows a
// new attempt. A completed finalization never is; anything else that left the
// session without a final summary is.
func finalizationRetryable(progress *domain.MeetingAIAnalysis, final *domain.MeetingAIAnalysis) bool {
	if final != nil && final.Status == domain.MeetingAIAnalysisCompleted && len(final.Payload) > 0 {
		return false
	}
	if progress == nil {
		return true
	}
	if progress.Status == domain.MeetingAIAnalysisCompleted {
		return false
	}
	return true
}
