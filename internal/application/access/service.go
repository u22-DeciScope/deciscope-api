package access

import (
	"context"

	"deciscope-core-api/internal/domain"
)

type Repository interface {
	CanAccessMeeting(ctx context.Context, userID, meetingID string) error
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) CanAccessMeeting(ctx context.Context, userID, meetingID string) error {
	if meetingID == "" {
		return domain.ErrNotFound
	}
	return s.repository.CanAccessMeeting(ctx, userID, meetingID)
}
