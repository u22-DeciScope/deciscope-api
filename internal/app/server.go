package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"deciscope-core-api/internal/app/middleware"
	"deciscope-core-api/internal/core"
	"deciscope-core-api/internal/db"
	"deciscope-core-api/internal/firebase"
	"deciscope-core-api/internal/fixture"
	"deciscope-core-api/internal/handlers"
	"deciscope-core-api/internal/realtime"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

func NewServer() (http.Handler, error) {
	// Firebase 初期化（global AuthClient を初期化）
	firebase.Init()

	var store *core.Store
	if err := db.InitSQLite(); err != nil {
		log.Printf("sqlite unavailable; /v1 uses in-memory local store: %v", err)
		store = core.NewMemoryStore()
	} else {
		store = core.NewStore(db.Conn)
		if err := store.Migrate(context.Background()); err != nil {
			return nil, fmt.Errorf("migrate core schema: %w", err)
		}
	}
	hub := realtime.NewHub()
	service := core.NewService(store, hub)
	replay := fixture.NewManager(service, os.Getenv("FIXTURE_DIR"))
	coreAPI := handlers.NewCoreAPI(service, replay, os.Getenv("UPLOAD_DIR"))

	// Firebase Auth クライアント（グローバル初期化済みのものを取得）
	authClient, err := firebase.AuthClient()
	if err != nil {
		log.Printf("firebase auth disabled; protected /api routes accept Bearer dev:<uid>: %v", err)
	}

	r := chi.NewRouter()

	// CORS
	r.Use(corsMiddleware)
	r.Use(chimiddleware.AllowContentType("application/json", "multipart/form-data"))

	// 認証不要
	r.HandleFunc("/register", handlers.Register)
	r.HandleFunc("/register-form", handlers.RegisterForm)
	r.HandleFunc("/health", handlers.Health)
	r.HandleFunc("/login", handlers.Login)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/health", coreAPI.Health)
		r.Get("/meetings", coreAPI.ListMeetings)
		r.Post("/meetings", coreAPI.CreateMeeting)
		r.Get("/meetings/{meeting_id}", coreAPI.GetMeeting)
		r.Post("/meetings/{meeting_id}/join-token", coreAPI.CreateJoinToken)
		r.Post("/meetings/{meeting_id}/end", coreAPI.EndMeeting)
		r.Get("/meetings/{meeting_id}/events", coreAPI.ListEvents)
		r.Get("/meetings/{meeting_id}/segments", coreAPI.ListSegments)
		r.Get("/meetings/{meeting_id}/report", coreAPI.GetReport)
		r.Get("/fixtures", coreAPI.ListFixtures)
		r.Post("/meetings/{meeting_id}/replay/start", coreAPI.ReplayStart)
		r.Post("/meetings/{meeting_id}/replay/pause", coreAPI.ReplayPause)
		r.Post("/meetings/{meeting_id}/replay/resume", coreAPI.ReplayResume)
		r.Post("/meetings/{meeting_id}/replay/reset", coreAPI.ReplayReset)
		r.Post("/uploads", coreAPI.Upload)
		r.Get("/jobs/{job_id}", coreAPI.GetJob)
		r.Get("/realtime", hub.ServeWS(store))
	})

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
		origin := r.Header.Get("Origin")
		allowedOrigin := os.Getenv("FRONTEND_URL")
		if allowedOrigin == "" {
			allowedOrigin = "http://localhost:5173"
		}
		responseOrigin := allowedOrigin
		if list := os.Getenv("ALLOWED_ORIGINS"); list != "" {
			for _, candidate := range strings.Split(list, ",") {
				candidate = strings.TrimSpace(candidate)
				if candidate == "*" || candidate == origin {
					responseOrigin = candidate
					break
				}
			}
		} else if origin != "" && (origin == allowedOrigin || strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:")) {
			responseOrigin = origin
		}
		w.Header().Set("Access-Control-Allow-Origin", responseOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Upgrade, Connection")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
