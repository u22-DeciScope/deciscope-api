package httpadapter

import (
	"context"
	"io"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

type CoreUseCases interface {
	ListMeetings(ctx context.Context, workspaceID string) ([]domain.Meeting, error)
	CreateMeeting(ctx context.Context, workspaceID, title, source string) (*domain.Meeting, error)
	GetMeeting(ctx context.Context, meetingID string) (*domain.Meeting, error)
	CreateJoinToken(ctx context.Context, meetingID string) (*application.JoinToken, error)
	EndMeeting(ctx context.Context, meetingID string) (*domain.Report, []domain.Event, error)
	ListEvents(ctx context.Context, meetingID string, afterSeq int64) ([]domain.Event, error)
	ListSegments(ctx context.Context, meetingID string, afterSeq int64) ([]domain.Segment, error)
	GetOrCreateReport(ctx context.Context, meetingID string) (*domain.Report, error)
	UploadFile(ctx context.Context, workspaceID, filename, mediaType string, src io.Reader) (*application.UploadResult, error)
	GetJob(ctx context.Context, jobID string) (*domain.Job, error)
}

type CoreAPI struct {
	service CoreUseCases
}

func NewCoreAPI(service CoreUseCases) *CoreAPI {
	return &CoreAPI{service: service}
}
