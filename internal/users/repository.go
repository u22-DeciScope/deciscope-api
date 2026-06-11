package users

import (
	"context"
	"errors"
)

var ErrEmailExists = errors.New("email already exists")

type User struct {
	ID    int64
	Name  string
	Email string
}

type Repository interface {
	FindOrCreateFirebaseUser(ctx context.Context, email, name string) (*User, error)
	CreatePasswordUser(ctx context.Context, name, email, passwordHash string) (*User, error)
}
