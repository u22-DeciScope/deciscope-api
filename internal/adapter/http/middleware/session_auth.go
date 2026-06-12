package middleware

import (
	"context"
	"errors"
	"net/http"

	appauth "deciscope-core-api/internal/application/auth"
)

const SessionCookieName = "deciscope_session"

type contextKey string

const sessionContextKey contextKey = "session"

type SessionAuthenticator interface {
	Authenticate(ctx context.Context, rawToken string) (*appauth.SessionResult, error)
}

func SessionAuth(authenticator SessionAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			session, err := authenticator.Authenticate(r.Context(), cookie.Value)
			if errors.Is(err, appauth.ErrUnauthorized) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if err != nil {
				http.Error(w, "authentication failed", http.StatusInternalServerError)
				return
			}
			ctx := context.WithValue(r.Context(), sessionContextKey, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func SessionFromContext(ctx context.Context) (*appauth.SessionResult, bool) {
	value, ok := ctx.Value(sessionContextKey).(*appauth.SessionResult)
	return value, ok
}
