package usecase

import (
	"context"
	"testing"
	"time"

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
