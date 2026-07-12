package application

import (
	"context"
	"time"

	"deciscope-core-api/internal/domain"
)

type MeetingRepository interface {
	CreateMeeting(ctx context.Context, workspaceID, title, source string) (*domain.Meeting, error)
	ListMeetings(ctx context.Context, workspaceID string) ([]domain.Meeting, error)
	GetMeeting(ctx context.Context, meetingID string) (*domain.Meeting, error)
	ResetMeeting(ctx context.Context, meetingID string) error
}

type EventRepository interface {
	AppendEvent(ctx context.Context, meetingID, eventType string, payload any) (*domain.Event, error)
	ListEvents(ctx context.Context, meetingID string, afterSeq int64) ([]domain.Event, error)
	ListSegments(ctx context.Context, meetingID string, afterSeq int64) ([]domain.Segment, error)
}

type ReportRepository interface {
	SaveReport(ctx context.Context, meetingID, content string) (*domain.Report, error)
	LatestReport(ctx context.Context, meetingID string) (*domain.Report, error)
}

type JobRepository interface {
	CreateJob(ctx context.Context, workspaceID, jobType, meetingID, status string) (*domain.Job, error)
	CompleteJob(ctx context.Context, jobID string, result any) error
	FailJob(ctx context.Context, jobID, message string) error
	GetJob(ctx context.Context, jobID string) (*domain.Job, error)
}

type TranscriptSegmentRepository interface {
	SaveTranscriptSegment(ctx context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error)
	ListTranscriptSegments(ctx context.Context, callID, sessionID string, limit int) ([]domain.TranscriptSegment, error)
}

type TranscriptSegmentPublisher interface {
	PublishTranscriptSegment(segment domain.TranscriptSegment)
}

type MeetingSessionRepository interface {
	CreateMeetingSession(ctx context.Context, session domain.MeetingSession) (*domain.MeetingSession, error)
	CreateOrReuseMeetingSession(ctx context.Context, session domain.MeetingSession) (*domain.MeetingSession, bool, error)
	GetMeetingSession(ctx context.Context, sessionID string) (*domain.MeetingSession, error)
	ListMeetingSessions(ctx context.Context, workspaceID string, limit int) ([]domain.MeetingSession, error)
	UpdateMeetingSessionStatus(ctx context.Context, update domain.MeetingSessionStatusUpdate) (*domain.MeetingSession, error)
	UpdateMeetingSessionMetadata(ctx context.Context, update domain.MeetingSessionMetadataUpdate) (*domain.MeetingSession, error)
	MarkStaleMeetingSessions(ctx context.Context, staleBefore time.Time, updatedAt time.Time) ([]domain.MeetingSession, error)
	ListMeetingSessionDebug(ctx context.Context, limit int) ([]domain.MeetingSessionDebug, error)
	// TouchMeetingSessionBotSeen records that a heartbeat was received from the
	// bot for sessionID at seenAt. It updates last_bot_status_at and updated_at
	// only; it never changes status. The returned bool reports whether the
	// session was actually updated: terminal sessions (ended/failed/stale) are
	// left untouched and the bool is false, but the current session is still
	// returned so callers can respond with it. A missing session returns
	// domain.ErrNotFound.
	TouchMeetingSessionBotSeen(ctx context.Context, sessionID string, seenAt time.Time) (*domain.MeetingSession, bool, error)
	// ListMeetingSessionsForBotWatchdog returns the sessions the watchdog needs
	// to evaluate: those whose status is one the bot could still be attached to
	// (joined/active/recording/speech_error/speech_throttled) and that have a
	// non-zero LastBotStatusAt.
	ListMeetingSessionsForBotWatchdog(ctx context.Context) ([]domain.MeetingSession, error)
}

type BotJoinCommand struct {
	SessionID                   string
	JoinURL                     string
	CanonicalJoinWebURL         string
	JoinMeetingID               string
	CandidateUserIDs            []string
	CandidateUserPrincipalNames []string
	CreatedByMicrosoftUserID    string
	CreatedByEmail              string
}

type BotEndCommand struct {
	SessionID string
	BotCallID string
	Reason    string
}

type BotJoinCommander interface {
	SendJoinCommand(ctx context.Context, command BotJoinCommand) error
	EndMeetingSession(ctx context.Context, command BotEndCommand) error
}

type MeetingSessionPublisher interface {
	PublishMeetingSessionStatusChanged(session domain.MeetingSession)
}

// MeetingSessionBotHealthPublisher is notified by the watchdog whenever a
// session's bot connectivity transitions between healthy and unhealthy. It is
// deliberately separate from MeetingSessionPublisher because bot health
// changes are not meeting_session.status_changed events.
type MeetingSessionBotHealthPublisher interface {
	PublishMeetingSessionBotHealth(session domain.MeetingSession, healthy bool)
}

// TranscriptActivityReader is the read side of TranscriptActivityTracker that
// the watchdog depends on. It is defined narrowly here, alongside the other
// small port interfaces, so the watchdog does not depend on the tracker's
// concrete type.
type TranscriptActivityReader interface {
	EnsureSeen(sessionID string, at time.Time)
	Activity(sessionID string) (TranscriptActivity, bool)
	Forget(sessionID string)
}

// MeetingSessionTranscriptHealthPublisher is notified by the watchdog
// whenever a session's transcript health (as opposed to bot heartbeat health)
// transitions between ok/delayed/stalled. It is deliberately separate from
// MeetingSessionBotHealthPublisher because a stalled transcript is not the
// same signal as a lost bot heartbeat.
type MeetingSessionTranscriptHealthPublisher interface {
	PublishMeetingSessionTranscriptHealth(session domain.MeetingSession, transcriptHealth string, secondsSinceLastTranscript int)
}

// BotMediaMetricsReader is the read side of BotMediaMetricsStore that the
// watchdog depends on. It is defined narrowly here, alongside the other
// small port interfaces, so the watchdog does not depend on the store's
// concrete type.
type BotMediaMetricsReader interface {
	Get(sessionID string) (BotMediaMetrics, bool)
	Forget(sessionID string)
}

// MeetingSessionEndedObserver is notified when a meeting session transitions
// into the Ended status. It is used to trigger the asynchronous AI final
// summary without giving MeetingSessionService a direct dependency on the AI
// analysis service.
type MeetingSessionEndedObserver interface {
	NotifyMeetingSessionEnded(session domain.MeetingSession)
}

type MeetingAIAnalysisRepository interface {
	UpsertMeetingAIAnalysis(ctx context.Context, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, error)
	GetMeetingAIAnalysis(ctx context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error)
}

type MeetingAIAnalysisPublisher interface {
	PublishMeetingAIAnalysis(analysis domain.MeetingAIAnalysis)
}

// AIChatRequest and AIChatResult keep the Azure OpenAI wire format out of
// Application. Infrastructure adapters translate to/from the provider SDK.
type AIChatRequest struct {
	System    string
	User      string
	MaxTokens int
	// Deployment optionally overrides the adapter's default deployment for
	// this call (per-task model routing). Empty uses the default.
	Deployment string
}

type AIChatResult struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
}

type AIChatCompleter interface {
	Complete(ctx context.Context, request AIChatRequest) (AIChatResult, error)
}

type Publisher interface {
	Publish(event domain.Event)
}
