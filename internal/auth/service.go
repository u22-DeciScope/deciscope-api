package auth

import (
	"context"
	"errors"
	"fmt"

	"deciscope-core-api/internal/users"
)

var (
	ErrUnavailable   = errors.New("authentication is unavailable")
	ErrInvalidToken  = errors.New("invalid token")
	ErrEmailRequired = errors.New("email is required")
)

type Identity struct {
	UID   string
	Email string
	Name  string
}

type TokenVerifier interface {
	VerifyIDToken(ctx context.Context, idToken string) (*Identity, error)
}

type LoginResult struct {
	UserID       int64
	UID          string
	Email        string
	Name         string
	AuthProvider string
}

type Service struct {
	users    users.Repository
	verifier TokenVerifier
}

func NewService(userRepository users.Repository, verifier TokenVerifier) *Service {
	return &Service{users: userRepository, verifier: verifier}
}

func (s *Service) Login(ctx context.Context, idToken string) (*LoginResult, error) {
	if s.verifier == nil {
		return nil, ErrUnavailable
	}

	identity, err := s.verifier.VerifyIDToken(ctx, idToken)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if identity.Email == "" {
		return nil, ErrEmailRequired
	}

	result := &LoginResult{
		UID:          identity.UID,
		Email:        identity.Email,
		Name:         identity.Name,
		AuthProvider: "firebase",
	}
	if s.users == nil {
		return result, nil
	}

	user, err := s.users.FindOrCreateFirebaseUser(ctx, identity.Email, identity.Name)
	if err != nil {
		return nil, fmt.Errorf("find or create firebase user: %w", err)
	}
	result.UserID = user.ID
	return result, nil
}
