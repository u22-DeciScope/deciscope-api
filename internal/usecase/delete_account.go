package usecase

import (
	"context"
	"time"

	"deciscope-core-api/internal/contract"
)

type DeleteAccount struct {
	repository contract.AuthRepository
}

func NewDeleteAccount(repository contract.AuthRepository) DeleteAccount {
	return DeleteAccount{repository: repository}
}

func (u DeleteAccount) Execute(ctx context.Context, userID string, deletedAt time.Time) error {
	if err := u.repository.MarkUserDeleted(ctx, userID, deletedAt); err != nil {
		return err
	}

	return u.repository.RevokeAllSessionsByUserID(ctx, userID, deletedAt, "user_deleted")
}
