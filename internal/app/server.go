package app

import (
	"fmt"
	"net/http"
	"os"

	"deciscope-core-api/internal/app/middleware"
	"deciscope-core-api/internal/db"
	"deciscope-core-api/internal/firebase"
	"deciscope-core-api/internal/handlers"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

func NewServer() (http.Handler, error) {
	// Firebase 初期化（global AuthClient を初期化）
	firebase.Init()

	// SQLite 初期化
	if err := db.InitSQLite(); err != nil {
		return nil, fmt.Errorf("init sqlite: %w", err)
	}

	// Firebase Auth クライアント（グローバル初期化済みのものを取得）
	authClient, err := firebase.AuthClient()
	if err != nil {
		return nil, fmt.Errorf("get firebase auth client: %w", err)
	}

	r := chi.NewRouter()

	// CORS
	r.Use(corsMiddleware)
	r.Use(chimiddleware.AllowContentType("application/json"))

	// 認証不要
	r.HandleFunc("/register", handlers.Register)
	r.HandleFunc("/register-form", handlers.RegisterForm)
	r.HandleFunc("/health", handlers.Health)
	r.HandleFunc("/login", handlers.Login)

	// 認証が必要
	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.FirebaseAuthMiddleware(authClient))
		r.Get("/me", handlers.MeHandler)
		r.Post("/login", handlers.Login)
		r.Get("/health", handlers.Health)
	})

	return r, nil
}

// CORSミドルウェア
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originAllowed := os.Getenv("FRONTEND_URL")
		if originAllowed == "" {
			originAllowed = "http://localhost:5173"
		}
		w.Header().Set("Access-Control-Allow-Origin", originAllowed)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
