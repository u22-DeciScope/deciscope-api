package usecase

import (
	"context"
	"testing"
	"time"

	"deciscope-core-api/internal/contract"
	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/infra/auth"
	"deciscope-core-api/internal/infra/memory"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

func TestExchangeSessionReturnsProviderAgnosticUnauthorizedCode(t *testing.T) {
	usecase := NewExchangeSession(
		memory.NewAuthRepository(),
		auth.NewDevelopmentTokenVerifier("dev:"),
		fixedClock{now: time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)},
	)

	_, err := usecase.Execute(context.Background(), ExchangeSessionInput{
		IDToken: "invalid-token",
	})
	if err == nil {
		t.Fatal("expected unauthorized error")
	}

	appErr, ok := err.(*domain.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "invalid_identity_token" {
		t.Fatalf("expected invalid_identity_token, got %q", appErr.Code)
	}
}

func TestExchangeSessionStoresSessionMetadata(t *testing.T) {
	usecase := NewExchangeSession(
		memory.NewAuthRepository(),
		auth.NewDevelopmentTokenVerifier("dev:"),
		fixedClock{now: time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)},
	)

	output, err := usecase.Execute(context.Background(), ExchangeSessionInput{
		IDToken:     "dev:user-1",
		DeviceType:  "desktop",
		DeviceName:  "Chrome on Windows",
		LoginMethod: "google",
		UserAgent:   "Mozilla/5.0 Test Browser",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if output.Session.DeviceType != "desktop" {
		t.Fatalf("expected desktop device type, got %q", output.Session.DeviceType)
	}
	if output.Session.DeviceName != "Chrome on Windows" {
		t.Fatalf("expected device name to be preserved, got %q", output.Session.DeviceName)
	}
	if output.Session.LoginMethod != "google" {
		t.Fatalf("expected google login method, got %q", output.Session.LoginMethod)
	}
	if output.Session.UserAgent != "Mozilla/5.0 Test Browser" {
		t.Fatalf("expected user agent to be preserved, got %q", output.Session.UserAgent)
	}
}

func TestExchangeSessionLinksNewIdentityToExistingEmailUser(t *testing.T) {
	repository := memory.NewAuthRepository()
	firstUsecase := NewExchangeSession(
		repository,
		auth.NewDevelopmentTokenVerifier("dev:"),
		fixedClock{now: time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)},
	)

	firstOutput, err := firstUsecase.Execute(context.Background(), ExchangeSessionInput{
		IDToken:     "dev:shared-user",
		LoginMethod: "password",
	})
	if err != nil {
		t.Fatalf("expected initial exchange to succeed, got %v", err)
	}

	secondUsecase := NewExchangeSession(
		repository,
		stubTokenVerifier{
			identity: contract.VerifiedIdentity{
				Provider: "firebase",
				Subject:  "google-user",
				Email:    "shared-user@example.local",
			},
		},
		fixedClock{now: time.Date(2026, time.March, 2, 1, 0, 0, 0, time.UTC)},
	)

	secondOutput, err := secondUsecase.Execute(context.Background(), ExchangeSessionInput{
		LoginMethod: "google",
	})
	if err != nil {
		t.Fatalf("expected second exchange to succeed, got %v", err)
	}

	if secondOutput.User.ID != firstOutput.User.ID {
		t.Fatalf("expected linked user id %q, got %q", firstOutput.User.ID, secondOutput.User.ID)
	}
	if secondOutput.Session.LoginMethod != "google" {
		t.Fatalf("expected google login method, got %q", secondOutput.Session.LoginMethod)
	}
}

type stubTokenVerifier struct {
	identity contract.VerifiedIdentity
	err      error
}

func (v stubTokenVerifier) VerifyIDToken(context.Context, string) (contract.VerifiedIdentity, error) {
	if v.err != nil {
		return contract.VerifiedIdentity{}, v.err
	}

	return v.identity, nil
}
