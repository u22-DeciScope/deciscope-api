package usecase

import (
	"context"
	"time"

	"deciscope-core-api/internal/contract"
)

type Logout struct {
	repository contract.AuthRepository
}

func NewLogout(repository contract.AuthRepository) Logout {
	return Logout{repository: repository}
}

func (u Logout) Execute(ctx context.Context, sessionID string, revokedAt time.Time) error {
	return u.repository.RevokeSession(ctx, sessionID, revokedAt, "logout")
}
