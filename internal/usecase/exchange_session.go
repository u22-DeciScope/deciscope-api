package usecase

import (
	"context"
	"errors"
	"strings"

	"deciscope-core-api/internal/contract"
	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/mapper"
)

type ExchangeSessionInput struct {
	IDToken    string
	DeviceType string
	DeviceName string
}

type ExchangeSessionOutput struct {
	User    domain.User
	Session domain.Session
}

type ExchangeSession struct {
	repository contract.AuthRepository
	verifier   contract.TokenVerifier
	clock      contract.Clock
}

func NewExchangeSession(repository contract.AuthRepository, verifier contract.TokenVerifier, clock contract.Clock) ExchangeSession {
	return ExchangeSession{
		repository: repository,
		verifier:   verifier,
		clock:      clock,
	}
}

func (u ExchangeSession) Execute(ctx context.Context, input ExchangeSessionInput) (ExchangeSessionOutput, error) {
	verified, err := u.verifier.VerifyIDToken(ctx, input.IDToken)
	if err != nil {
		return ExchangeSessionOutput{}, domain.Unauthorized("invalid_firebase_token")
	}

	identity, seed := mapper.MapVerifiedIdentity(verified)
	user, found, err := u.repository.FindUserByIdentity(ctx, identity)
	if err != nil {
		return ExchangeSessionOutput{}, domain.Internal("internal_error")
	}

	if !found {
		user, err = u.repository.CreateUserWithIdentity(ctx, identity, seed)
		if err != nil {
			if errors.Is(err, domain.ErrIdentityConflict) {
				user, found, err = u.repository.FindUserByIdentity(ctx, identity)
				if err != nil || !found {
					return ExchangeSessionOutput{}, domain.Internal("internal_error")
				}
			} else {
				return ExchangeSessionOutput{}, domain.Internal("internal_error")
			}
		}
	}

	if appErr := domain.EnsureUserCanUse(user); appErr != nil {
		return ExchangeSessionOutput{}, appErr
	}

	now := u.clock.Now()
	session, err := u.repository.CreateSession(ctx, domain.SessionSeed{
		UserID:     user.ID,
		DeviceType: normalizeDeviceType(input.DeviceType),
		DeviceName: trimDeviceName(input.DeviceName),
		CreatedAt:  now,
	})
	if err != nil {
		return ExchangeSessionOutput{}, domain.Internal("internal_error")
	}

	return ExchangeSessionOutput{
		User:    user,
		Session: session,
	}, nil
}

func normalizeDeviceType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ios", "android", "desktop", "web":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "web"
	}
}

func trimDeviceName(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > 255 {
		return trimmed[:255]
	}

	return trimmed
}
