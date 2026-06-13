package middleware

import (
	"context"
	"encoding/json"
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
				writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
				return
			}
			session, err := authenticator.Authenticate(r.Context(), cookie.Value)
			if errors.Is(err, appauth.ErrUnauthorized) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "authentication_failed", "authentication failed")
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

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message}})
}
