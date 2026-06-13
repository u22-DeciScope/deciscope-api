package middleware

import (
	"context"
	"net/http"
	"strings"

	appauth "deciscope-core-api/internal/application/auth"
)

type contextKey string

const UserContextKey contextKey = "firebase_user"
const UIDContextKey contextKey = "uid"

type TokenVerifier interface {
	VerifyIDToken(ctx context.Context, idToken string) (*appauth.Identity, error)
}

func FirebaseAuthMiddleware(verifier TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "Unauthorized: Missing token", http.StatusUnauthorized)
				return
			}

			idToken := strings.TrimPrefix(authHeader, "Bearer ")
			if strings.HasPrefix(idToken, "dev:") {
				uid := strings.TrimPrefix(idToken, "dev:")
				if uid == "" {
					uid = "local-dev-user"
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UIDContextKey, uid)))
				return
			}
			if verifier == nil {
				http.Error(w, "Unauthorized: Firebase is disabled locally; use Bearer dev:<uid>", http.StatusUnauthorized)
				return
			}

			identity, err := verifier.VerifyIDToken(r.Context(), idToken)
			if err != nil {
				http.Error(w, "Unauthorized: Invalid token", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), UserContextKey, identity)
			ctx = context.WithValue(ctx, UIDContextKey, identity.UID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
