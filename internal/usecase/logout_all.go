package usecase

import (
	"context"
	"time"

	"deciscope-core-api/internal/contract"
)

type LogoutAll struct {
	repository contract.AuthRepository
}

func NewLogoutAll(repository contract.AuthRepository) LogoutAll {
	return LogoutAll{repository: repository}
}

func (u LogoutAll) Execute(ctx context.Context, userID string, revokedAt time.Time) error {
	return u.repository.RevokeAllSessionsByUserID(ctx, userID, revokedAt, "logout_all")
}
