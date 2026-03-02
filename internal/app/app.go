package app

import (
	"context"
	"net/http"
	"os"
	"strings"

	"deciscope-core-api/internal/handler"
	"deciscope-core-api/internal/infra/auth"
	"deciscope-core-api/internal/infra/postgres"
	"deciscope-core-api/internal/infra/system"
	"deciscope-core-api/internal/usecase"
)

func NewServer() (http.Handler, error) {
	if err := LoadLocalEnv(); err != nil {
		return nil, err
	}

	clock := system.RealClock{}
	repository, err := postgres.NewAuthRepository(os.Getenv("DATABASE_URL"))
	if err != nil {
		return nil, err
	}

	verifier, err := auth.NewTokenVerifier(context.Background(), auth.VerifierConfig{
		CredentialsJSON: os.Getenv("FIREBASE_CREDENTIALS_JSON"),
		CredentialsPath: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
		ProjectID:       os.Getenv("FIREBASE_PROJECT_ID"),
		DevTokenPrefix:  os.Getenv("AUTH_DEV_TOKEN_PREFIX"),
	})
	if err != nil {
		return nil, err
	}

	exchangeSession := usecase.NewExchangeSession(repository, verifier, clock)
	getMe := usecase.NewGetMe()
	deleteAccount := usecase.NewDeleteAccount(repository)
	logout := usecase.NewLogout(repository)
	logoutAll := usecase.NewLogoutAll(repository)
	listSessions := usecase.NewListSessions(repository)
	revokeSession := usecase.NewRevokeSession(repository)

	authHandler := handler.NewAuthHandler(
		exchangeSession,
		getMe,
		deleteAccount,
		logout,
		logoutAll,
		listSessions,
		revokeSession,
		repository,
		clock,
		parseAllowedOrigins(os.Getenv("ALLOWED_ORIGINS")),
	)

	return authHandler.Router(), nil
}

func parseAllowedOrigins(raw string) map[string]struct{} {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	allowlist := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		allowlist[trimmed] = struct{}{}
	}

	if len(allowlist) == 0 {
		return nil
	}

	return allowlist
}
