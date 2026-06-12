package application

import (
	"encoding/json"

	"deciscope-core-api/internal/domain"
)

type Meeting = domain.Meeting
type Event = domain.Event
type Segment = domain.Segment
type Job = domain.Job
type Report = domain.Report
type Upload = domain.Upload

var ErrNotFound = domain.ErrNotFound
var NormalizeFixtureName = domain.NormalizeFixtureName

const (
	EventMeetingState        = domain.EventMeetingState
	EventTranscriptPartial   = domain.EventTranscriptPartial
	EventTranscriptFinal     = domain.EventTranscriptFinal
	EventAnalysisDelta       = domain.EventAnalysisDelta
	EventTreeUpdate          = domain.EventTreeUpdate
	EventSpeakerSummaryDelta = domain.EventSpeakerSummaryDelta
	EventReportReady         = domain.EventReportReady
	EventError               = domain.EventError
)

func jsonPayload(payload any) (json.RawMessage, error) {
	switch p := payload.(type) {
	case nil:
		return json.RawMessage(`{}`), nil
	case json.RawMessage:
		if len(p) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return p, nil
	case []byte:
		if len(p) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return json.RawMessage(p), nil
	default:
		return json.Marshal(payload)
	}
}
