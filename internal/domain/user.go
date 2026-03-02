package domain

import "time"

const (
	UserStatusActive    = "active"
	UserStatusPending   = "pending"
	UserStatusSuspended = "suspended"
	UserStatusDeleted   = "deleted"
)

type IdentityInput struct {
	Provider string
	Subject  string
}

type UserSeed struct {
	Email       string
	DisplayName string
	AvatarURL   string
}

type User struct {
	ID        string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

func (u User) NormalizedStatus() string {
	if u.DeletedAt != nil {
		return UserStatusDeleted
	}

	return u.Status
}

func (u User) CanUse() bool {
	return u.NormalizedStatus() == UserStatusActive
}

func EnsureUserCanUse(user User) *AppError {
	switch user.NormalizedStatus() {
	case UserStatusDeleted:
		return Forbidden("user_deleted")
	case UserStatusSuspended:
		return Forbidden("user_suspended")
	case UserStatusPending:
		return Forbidden("user_pending")
	default:
		return nil
	}
}
