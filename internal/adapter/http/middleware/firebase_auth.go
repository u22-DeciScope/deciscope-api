package middleware

import (
	"context"
	"net/http"
	"strings"

	"firebase.google.com/go/v4/auth"
)

type contextKey string

const UserContextKey contextKey = "firebase_user"
const UIDContextKey contextKey = "uid"

func FirebaseAuthMiddleware(authClient *auth.Client) func(http.Handler) http.Handler {
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
				ctx := context.WithValue(r.Context(), UIDContextKey, uid)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			if authClient == nil {
				http.Error(w, "Unauthorized: Firebase is disabled locally; use Bearer dev:<uid>", http.StatusUnauthorized)
				return
			}

			// Firebase Admin SDK でトークン検証
			token, err := authClient.VerifyIDToken(r.Context(), idToken)
			if err != nil {
				http.Error(w, "Unauthorized: Invalid token", http.StatusUnauthorized)
				return
			}

			// 後続のハンドラーでユーザー情報(UID等)を使えるように Context に仕込む
			ctx := context.WithValue(r.Context(), UserContextKey, token)
			ctx = context.WithValue(ctx, UIDContextKey, token.UID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
