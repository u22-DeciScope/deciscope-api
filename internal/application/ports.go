package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"deciscope-core-api/internal/domain"
)

var ErrMeetingTreeAuditMigrationMissing = errors.New("meeting tree audit migration is missing")

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
	// DeleteMeetingSession permanently removes the session and its dependent
	// transcript segments and AI analyses. Callers are responsible for
	// deciding whether deletion is currently allowed (e.g. only terminal
	// sessions); the repository performs the delete unconditionally.
	DeleteMeetingSession(ctx context.Context, sessionID string) error
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

// MeetingSessionEndedObserver owns the synchronous finalization pipeline.
// MeetingSessionService invokes it asynchronously while status=ending and
// persists status=ended only after it returns.
type MeetingSessionEndedObserver interface {
	FinalizeMeetingSession(ctx context.Context, session domain.MeetingSession, request MeetingSessionFinalizationRequest) error
}

// MeetingSessionPreparingObserver is notified after create/reuse metadata is
// durable and before the bot join command is sent. Implementations must
// return quickly; expensive preparation belongs in their own goroutine.
type MeetingSessionPreparingObserver interface {
	PrepareMeetingSession(session domain.MeetingSession)
}

// MeetingSessionFinalizationRequest carries optional drain proof from newer
// bots. Zero values preserve compatibility with bots that only report
// status=ended; the finalizer then uses a bounded DB quiet-period fallback.
type MeetingSessionFinalizationRequest struct {
	BotLastForwardedFinalSequence int64
	TranscriptQueueDrained        bool
}

type MeetingAIAnalysisRepository interface {
	UpsertMeetingAIAnalysis(ctx context.Context, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, error)
	GetMeetingAIAnalysis(ctx context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error)
	// ListMeetingAIAnalysesForSessions bulk-fetches one analysis type across
	// multiple sessions in a single query, for list/dashboard views (e.g. a
	// workspace meeting history) that need a small preview per session
	// without an N+1 fetch per card.
	ListMeetingAIAnalysesForSessions(ctx context.Context, sessionIDs []string, analysisType domain.MeetingAIAnalysisType) ([]domain.MeetingAIAnalysis, error)
}

// MeetingAIAnalysisCompareAndSwapRepository is an optional stronger live-row
// contract. Production repositories implement it so concurrent backend
// instances cannot overwrite a newer live tree with work based on an older
// version. Test fakes and non-live adapters may keep the legacy repository
// interface; the service falls back to UpsertMeetingAIAnalysis for them.
type MeetingAIAnalysisCompareAndSwapRepository interface {
	CompareAndSwapMeetingAIAnalysis(ctx context.Context, expectedVersion int64, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, bool, error)
}

// MeetingTreeAuditRepository owns durable audit history and the transactional
// compare-and-swap used when a validated audit patch creates a new live tree
// version. A stale expected version must return applied=false without changing
// either live analysis or the saved target session.
type MeetingTreeAuditRepository interface {
	CheckMeetingTreeAuditRepository(ctx context.Context) error
	TryStartMeetingTreeAuditRun(ctx context.Context, run domain.MeetingTreeAuditRun) (bool, error)
	SaveMeetingTreeAuditRun(ctx context.Context, run domain.MeetingTreeAuditRun) error
	GetLatestMeetingTreeAuditRun(ctx context.Context, sessionID string) (*domain.MeetingTreeAuditRun, error)
	CountMeetingTreeAuditProviderCalls(ctx context.Context, sessionID string, triggerClass domain.MeetingTreeAuditTriggerClass, since time.Time) (int, error)
	ApplyMeetingTreeAudit(ctx context.Context, run domain.MeetingTreeAuditRun, expectedVersion int64, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, bool, error)
}

type MeetingAIAnalysisPublisher interface {
	PublishMeetingAIAnalysis(analysis domain.MeetingAIAnalysis)
}

// MeetingAgendaProgressOverridesRepository persists one manual-override row
// per session (meeting_session_agenda_progress_overrides). GetAgendaProgressOverrides
// returns domain.ErrNotFound when the session has no overrides yet.
type MeetingAgendaProgressOverridesRepository interface {
	GetAgendaProgressOverrides(ctx context.Context, sessionID string) (json.RawMessage, error)
	UpsertAgendaProgressOverrides(ctx context.Context, sessionID string, payload json.RawMessage, updatedAt time.Time) error
}

// AIChatRequest and AIChatResult keep the Azure OpenAI wire format out of
// Application. Infrastructure adapters translate to/from the provider SDK.
type AIChatRequest struct {
	System    string
	User      string
	MaxTokens int
	// ResponseSchema requests provider-enforced structured output. Adapters
	// that support it translate this provider-neutral description to their
	// wire format and may fall back to JSON mode when a deployment does not
	// support strict schemas.
	ResponseSchema *AIResponseSchema
	// Deployment optionally overrides the adapter's default deployment for
	// this call (per-task model routing). Empty uses the default.
	Deployment string
}

type AIResponseSchema struct {
	Name        string
	Description string
	Strict      bool
	Schema      json.RawMessage
}

type AIChatResult struct {
	Content string
	// Model is the provider-reported model name. It can differ from the Azure
	// deployment alias selected for the task.
	Model            string
	PromptTokens     int
	CompletionTokens int
}

type AIChatCompleter interface {
	Complete(ctx context.Context, request AIChatRequest) (AIChatResult, error)
}

type Publisher interface {
	Publish(event domain.Event)
}
