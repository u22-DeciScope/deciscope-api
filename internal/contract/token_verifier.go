package contract

import "context"

type VerifiedIdentity struct {
	Provider    string
	Subject     string
	Email       string
	DisplayName string
	AvatarURL   string
}

type TokenVerifier interface {
	VerifyIDToken(ctx context.Context, idToken string) (VerifiedIdentity, error)
}
