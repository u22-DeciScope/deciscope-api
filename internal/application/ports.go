package application

import (
	"context"
	"io"
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

type UploadRepository interface {
	SaveUpload(ctx context.Context, workspaceID, filename, mediaType, path, jobID string) (*domain.Upload, error)
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
	UpdateMeetingSessionStatus(ctx context.Context, update domain.MeetingSessionStatusUpdate) (*domain.MeetingSession, error)
	UpdateMeetingSessionMetadata(ctx context.Context, update domain.MeetingSessionMetadataUpdate) (*domain.MeetingSession, error)
	MarkStaleMeetingSessions(ctx context.Context, staleBefore time.Time, updatedAt time.Time) ([]domain.MeetingSession, error)
	ListMeetingSessionDebug(ctx context.Context, limit int) ([]domain.MeetingSessionDebug, error)
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

type BotJoinCommander interface {
	SendJoinCommand(ctx context.Context, command BotJoinCommand) error
}

type MeetingSessionPublisher interface {
	PublishMeetingSessionStatusChanged(session domain.MeetingSession)
}

type Publisher interface {
	Publish(event domain.Event)
}

type ObjectStorage interface {
	Save(ctx context.Context, key string, src io.Reader) (string, error)
}
