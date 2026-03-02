package contract

import (
	"context"
	"time"

	"deciscope-core-api/internal/domain"
)

type AuthRepository interface {
	FindUserByIdentity(ctx context.Context, identity domain.IdentityInput) (domain.User, bool, error)
	FindUserByEmail(ctx context.Context, email string) (domain.User, bool, error)
	CreateUserWithIdentity(ctx context.Context, identity domain.IdentityInput, seed domain.UserSeed) (domain.User, error)
	AttachIdentityToUser(ctx context.Context, userID string, identity domain.IdentityInput) error
	FindUserByID(ctx context.Context, userID string) (domain.User, bool, error)
	MarkUserDeleted(ctx context.Context, userID string, deletedAt time.Time) error
	CreateSession(ctx context.Context, seed domain.SessionSeed) (domain.Session, error)
	FindSessionByID(ctx context.Context, sessionID string) (domain.Session, bool, error)
	RevokeSession(ctx context.Context, sessionID string, revokedAt time.Time, reason string) error
	RevokeAllSessionsByUserID(ctx context.Context, userID string, revokedAt time.Time, reason string) error
	ListActiveSessionsByUserID(ctx context.Context, userID string) ([]domain.Session, error)
	TouchSession(ctx context.Context, sessionID string, seenAt time.Time) error
}
