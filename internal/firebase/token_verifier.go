package firebase

import (
	"context"

	appauth "deciscope-core-api/internal/auth"

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
	return &appauth.Identity{
		UID:   token.UID,
		Email: email,
		Name:  name,
	}, nil
}
