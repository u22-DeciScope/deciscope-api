package app

import (
	"fmt"
	"net/http"

	"deciscope-core-api/internal/app/middleware"
	"deciscope-core-api/internal/db"
	"deciscope-core-api/internal/handlers"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

func NewServer() (http.Handler, error) {
	if err := db.InitSQLite(); err != nil {
		return nil, fmt.Errorf("init sqlite: %w", err)
	}

	r := chi.NewRouter()

	// CORS対応
	r.Use(corsMiddleware)
	r.Use(chimiddleware.AllowContentType("application/json"))

	// 認証不要のエンドポイント
	r.HandleFunc("/register", handlers.Register)
	r.HandleFunc("/register-form", handlers.RegisterForm)
	r.HandleFunc("/health", handlers.Health)
	r.HandleFunc("/login", handlers.Login)

	// 認証が必要なエンドポイント
	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.FirebaseAuthMiddleware(firebaseAuthClient))
		r.Get("/me", handlers.MeHandler)
		r.Post("/login", handlers.Login)  // 元々の「func Login」をそのまま指定
		r.Get("/health", handlers.Health) // health.goの中身が「func Health」の場合。もし違う名前ならそれに合わせてください
	})

	return r, nil
}

// CORSミドルウェア
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
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
