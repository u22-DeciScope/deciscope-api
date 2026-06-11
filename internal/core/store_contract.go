package core

import "context"

type MeetingStore interface {
	CreateMeeting(ctx context.Context, title, source string) (*Meeting, error)
	ListMeetings(ctx context.Context) ([]Meeting, error)
	GetMeeting(ctx context.Context, meetingID string) (*Meeting, error)
	AppendEvent(ctx context.Context, meetingID, eventType string, payload any) (*Event, error)
	ListEvents(ctx context.Context, meetingID string, afterSeq int64) ([]Event, error)
	ListSegments(ctx context.Context, meetingID string, afterSeq int64) ([]Segment, error)
	ResetMeeting(ctx context.Context, meetingID string) error
	CreateJob(ctx context.Context, jobType, meetingID, status string) (*Job, error)
	CompleteJob(ctx context.Context, jobID string, result any) error
	FailJob(ctx context.Context, jobID, message string) error
	GetJob(ctx context.Context, jobID string) (*Job, error)
	SaveReport(ctx context.Context, meetingID, content string) (*Report, error)
	LatestReport(ctx context.Context, meetingID string) (*Report, error)
	SaveUpload(ctx context.Context, filename, mediaType, path, jobID string) (*Upload, error)
}

var _ MeetingStore = (*MemoryStore)(nil)
