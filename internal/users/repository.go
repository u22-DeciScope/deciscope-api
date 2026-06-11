package users

import (
	"context"
)

type User struct {
	ID    int64
	Name  string
	Email string
}

type Repository interface {
	FindOrCreateFirebaseUser(ctx context.Context, email, name string) (*User, error)
}
