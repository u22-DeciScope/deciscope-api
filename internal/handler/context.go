package handler

import (
	"context"

	"deciscope-core-api/internal/domain"
)

type authContextKey struct{}

func withAuthContext(ctx context.Context, authContext domain.AuthContext) context.Context {
	return context.WithValue(ctx, authContextKey{}, authContext)
}

func currentAuthContext(ctx context.Context) (domain.AuthContext, bool) {
	authContext, ok := ctx.Value(authContextKey{}).(domain.AuthContext)
	return authContext, ok
}
