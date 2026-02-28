package auth

import (
	"context"
	"errors"
	"strings"

	"deciscope-core-api/internal/contract"
)

const defaultPrefix = "dev:"

type DevelopmentTokenVerifier struct {
	prefix string
}

func NewDevelopmentTokenVerifier(prefix string) DevelopmentTokenVerifier {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" {
		trimmed = defaultPrefix
	}

	return DevelopmentTokenVerifier{prefix: trimmed}
}

func (v DevelopmentTokenVerifier) VerifyIDToken(_ context.Context, idToken string) (contract.VerifiedIdentity, error) {
	token := strings.TrimSpace(idToken)
	if !strings.HasPrefix(token, v.prefix) {
		return contract.VerifiedIdentity{}, errors.New("invalid firebase token")
	}

	subject := strings.TrimSpace(strings.TrimPrefix(token, v.prefix))
	if subject == "" {
		return contract.VerifiedIdentity{}, errors.New("invalid firebase token")
	}

	return contract.VerifiedIdentity{
		Provider:    "firebase",
		Subject:     subject,
		Email:       subject + "@example.local",
		DisplayName: subject,
	}, nil
}
