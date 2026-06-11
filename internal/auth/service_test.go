package auth

import (
	"context"
	"errors"
	"testing"

	"deciscope-core-api/internal/users"
)

type fakeVerifier struct {
	identity *Identity
	err      error
}

func (v fakeVerifier) VerifyIDToken(context.Context, string) (*Identity, error) {
	return v.identity, v.err
}

type fakeUserRepository struct {
	user *users.User
	err  error
}

func (r *fakeUserRepository) FindOrCreateFirebaseUser(context.Context, string, string) (*users.User, error) {
	return r.user, r.err
}

func (r *fakeUserRepository) CreatePasswordUser(context.Context, string, string, string) (*users.User, error) {
	return r.user, r.err
}

func TestLoginUsesVerifierAndUserRepository(t *testing.T) {
	service := NewService(
		&fakeUserRepository{user: &users.User{ID: 42}},
		fakeVerifier{identity: &Identity{UID: "firebase-uid", Email: "user@example.com", Name: "User"}},
	)

	result, err := service.Login(context.Background(), "token")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.UserID != 42 || result.Email != "user@example.com" {
		t.Fatalf("Login() result = %+v", result)
	}
}

func TestLoginWithoutVerifierIsUnavailable(t *testing.T) {
	service := NewService(nil, nil)

	_, err := service.Login(context.Background(), "token")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Login() error = %v, want ErrUnavailable", err)
	}
}
