package auth

import (
	"context"
	"errors"
	"strings"

	"deciscope-core-api/internal/contract"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

type VerifierConfig struct {
	CredentialsJSON string
	CredentialsPath string
	ProjectID       string
	DevTokenPrefix  string
}

type FirebaseTokenVerifier struct {
	client *auth.Client
}

func NewTokenVerifier(ctx context.Context, config VerifierConfig) (contract.TokenVerifier, error) {
	if strings.TrimSpace(config.CredentialsJSON) == "" && strings.TrimSpace(config.CredentialsPath) == "" {
		return NewDevelopmentTokenVerifier(config.DevTokenPrefix), nil
	}

	verifier, err := NewFirebaseTokenVerifier(ctx, config)
	if err != nil {
		return nil, err
	}

	return verifier, nil
}

func NewFirebaseTokenVerifier(ctx context.Context, config VerifierConfig) (FirebaseTokenVerifier, error) {
	options := make([]option.ClientOption, 0, 1)

	if strings.TrimSpace(config.CredentialsJSON) != "" {
		options = append(options, option.WithCredentialsJSON([]byte(config.CredentialsJSON)))
	}

	if strings.TrimSpace(config.CredentialsPath) != "" {
		options = append(options, option.WithCredentialsFile(config.CredentialsPath))
	}

	appConfig := &firebase.Config{}
	if strings.TrimSpace(config.ProjectID) != "" {
		appConfig.ProjectID = config.ProjectID
	}

	firebaseApp, err := firebase.NewApp(ctx, appConfig, options...)
	if err != nil {
		return FirebaseTokenVerifier{}, err
	}

	client, err := firebaseApp.Auth(ctx)
	if err != nil {
		return FirebaseTokenVerifier{}, err
	}

	return FirebaseTokenVerifier{client: client}, nil
}

func (v FirebaseTokenVerifier) VerifyIDToken(ctx context.Context, idToken string) (contract.VerifiedIdentity, error) {
	token := strings.TrimSpace(idToken)
	if token == "" {
		return contract.VerifiedIdentity{}, errors.New("missing firebase token")
	}

	verified, err := v.client.VerifyIDToken(ctx, token)
	if err != nil {
		return contract.VerifiedIdentity{}, err
	}

	identity := contract.VerifiedIdentity{
		Provider: "firebase",
		Subject:  verified.UID,
	}

	if email, ok := verified.Claims["email"].(string); ok {
		identity.Email = email
	}
	if displayName, ok := verified.Claims["name"].(string); ok {
		identity.DisplayName = displayName
	}
	if avatarURL, ok := verified.Claims["picture"].(string); ok {
		identity.AvatarURL = avatarURL
	}

	return identity, nil
}
