package usecase

import (
	"context"
	"time"

	"deciscope-core-api/internal/contract"
	"deciscope-core-api/internal/domain"
)

type RevokeSession struct {
	repository contract.AuthRepository
}

func NewRevokeSession(repository contract.AuthRepository) RevokeSession {
	return RevokeSession{repository: repository}
}

func (u RevokeSession) Execute(ctx context.Context, currentUserID string, targetSessionID string, revokedAt time.Time) error {
	session, found, err := u.repository.FindSessionByID(ctx, targetSessionID)
	if err != nil {
		return domain.Internal("internal_error")
	}
	if !found || session.UserID != currentUserID {
		return nil
	}

	return u.repository.RevokeSession(ctx, targetSessionID, revokedAt, "revoke")
}
