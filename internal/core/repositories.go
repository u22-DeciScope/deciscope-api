package core

import "context"

type MeetingRepository interface {
	CreateMeeting(ctx context.Context, title, source string) (*Meeting, error)
	ListMeetings(ctx context.Context) ([]Meeting, error)
	GetMeeting(ctx context.Context, meetingID string) (*Meeting, error)
	ResetMeeting(ctx context.Context, meetingID string) error
}

type EventRepository interface {
	// AppendEvent atomically allocates the next durable sequence and persists
	// all event-related state changes in one transaction.
	AppendEvent(ctx context.Context, meetingID, eventType string, payload any) (*Event, error)
	ListEvents(ctx context.Context, meetingID string, afterSeq int64) ([]Event, error)
	ListSegments(ctx context.Context, meetingID string, afterSeq int64) ([]Segment, error)
}

type ReportRepository interface {
	SaveReport(ctx context.Context, meetingID, content string) (*Report, error)
	LatestReport(ctx context.Context, meetingID string) (*Report, error)
}

type JobRepository interface {
	CreateJob(ctx context.Context, jobType, meetingID, status string) (*Job, error)
	CompleteJob(ctx context.Context, jobID string, result any) error
	FailJob(ctx context.Context, jobID, message string) error
	GetJob(ctx context.Context, jobID string) (*Job, error)
}

type UploadRepository interface {
	SaveUpload(ctx context.Context, filename, mediaType, path, jobID string) (*Upload, error)
}

type Repositories struct {
	Meetings MeetingRepository
	Events   EventRepository
	Reports  ReportRepository
	Jobs     JobRepository
	Uploads  UploadRepository
}

func RepositoriesFromMemory(store *MemoryStore) Repositories {
	return Repositories{
		Meetings: store,
		Events:   store,
		Reports:  store,
		Jobs:     store,
		Uploads:  store,
	}
}

var _ MeetingRepository = (*MemoryStore)(nil)
var _ EventRepository = (*MemoryStore)(nil)
var _ ReportRepository = (*MemoryStore)(nil)
var _ JobRepository = (*MemoryStore)(nil)
var _ UploadRepository = (*MemoryStore)(nil)
