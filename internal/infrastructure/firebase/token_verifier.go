package firebase

import (
	"context"

	appauth "deciscope-core-api/internal/application/auth"

	firebaseauth "firebase.google.com/go/v4/auth"
)

type TokenVerifier struct {
	client *firebaseauth.Client
}

func NewTokenVerifier(client *firebaseauth.Client) *TokenVerifier {
	if client == nil {
		return nil
	}
	return &TokenVerifier{client: client}
}

func (v *TokenVerifier) VerifyIDToken(ctx context.Context, idToken string) (*appauth.Identity, error) {
	token, err := v.client.VerifyIDToken(ctx, idToken)
	if err != nil {
		return nil, err
	}
	email, _ := token.Claims["email"].(string)
	name, _ := token.Claims["name"].(string)
	emailVerified, _ := token.Claims["email_verified"].(bool)
	provider := ""
	if firebaseClaim, ok := token.Claims["firebase"].(map[string]interface{}); ok {
		provider, _ = firebaseClaim["sign_in_provider"].(string)
	}
	return &appauth.Identity{
		UID: token.UID, Email: email, Name: name, EmailVerified: emailVerified, Provider: provider,
	}, nil
}
