package usecase

import (
	"context"

	"deciscope-core-api/internal/contract"
	"deciscope-core-api/internal/domain"
)

type ListSessions struct {
	repository contract.AuthRepository
}

func NewListSessions(repository contract.AuthRepository) ListSessions {
	return ListSessions{repository: repository}
}

func (u ListSessions) Execute(ctx context.Context, userID string) ([]domain.Session, error) {
	sessions, err := u.repository.ListActiveSessionsByUserID(ctx, userID)
	if err != nil {
		return nil, domain.Internal("internal_error")
	}

	return sessions, nil
}
