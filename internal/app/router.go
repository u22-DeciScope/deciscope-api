package app

import (
	"deciscope-core-api/internal/app/middleware"
	"deciscope-core-api/internal/handlers"
	"net/http"

	"firebase.google.com/go/v4/auth"
)

func SetupRouter(authClient *auth.Client) http.Handler {
	mux := http.NewServeMux()

	// 1. 認証が必要なAPI用サブルーター
	apiMux := http.NewServeMux()
	loginHandler := handlers.NewLoginHandler()
	apiMux.HandleFunc("/api/login", loginHandler.HandleLogin)
	// 他の保護したいAPIも apiMux に足していく...

	// 2. 認証が必要なAPIに一括でミドルウェアを適用
	authMiddleware := middleware.FirebaseAuthMiddleware(authClient)
	mux.Handle("/api/", authMiddleware(apiMux))

	// 3. 認証が不要な公開API (Health checkなど)
	mux.HandleFunc("/health", handlers.HandleHealth)

	return mux
}
