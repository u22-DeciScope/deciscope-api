package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"deciscope-core-api/internal/app/middleware"
	appauth "deciscope-core-api/internal/auth"
	"deciscope-core-api/internal/core"
	"deciscope-core-api/internal/database"
	"deciscope-core-api/internal/firebase"
	"deciscope-core-api/internal/fixture"
	"deciscope-core-api/internal/handlers"
	"deciscope-core-api/internal/realtime"
	sqliterepository "deciscope-core-api/internal/repository/sqlite"
	"deciscope-core-api/internal/users"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

func NewServer() (http.Handler, error) {
	// Firebase 初期化（global AuthClient を初期化）
	firebase.Init()

	var repositories core.Repositories
	var userRepository users.Repository
	databaseConfig := database.ConfigFromEnv()
	conn, err := database.Open(context.Background(), databaseConfig)
	if err != nil {
		log.Printf("database unavailable; /v1 uses in-memory local store: %v", err)
		repositories = core.RepositoriesFromMemory(core.NewMemoryStore())
	} else {
		if err := database.Migrate(context.Background(), conn, databaseConfig.Driver); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("migrate database: %w", err)
		}
		repositories = sqliterepository.Repositories(sqliterepository.NewStore(conn))
		userRepository = sqliterepository.NewUserRepository(conn)
	}
	hub := realtime.NewHub()
	service := core.NewService(repositories, hub)
	replay := fixture.NewManager(service, os.Getenv("FIXTURE_DIR"))
	coreAPI := handlers.NewCoreAPI(service, replay, os.Getenv("UPLOAD_DIR"))

	// Firebase Auth クライアント（グローバル初期化済みのものを取得）
	authClient, err := firebase.AuthClient()
	if err != nil {
		log.Printf("firebase auth disabled; protected /v1/auth routes accept Bearer dev:<uid>: %v", err)
	}
	authService := appauth.NewService(userRepository, firebase.NewTokenVerifier(authClient))
	authAPI := handlers.NewAuthAPI(authService)

	r := chi.NewRouter()

	// CORS
	r.Use(corsMiddleware)
	r.Use(chimiddleware.AllowContentType("application/json", "multipart/form-data"))

	r.Route("/v1", func(r chi.Router) {
		r.Get("/health", coreAPI.Health)
		r.Post("/auth/login", authAPI.Login)
		r.Group(func(r chi.Router) {
			r.Use(middleware.FirebaseAuthMiddleware(authClient))
			r.Get("/auth/me", handlers.MeHandler)
			r.Get("/auth/health", handlers.Health)
		})
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
		r.Get("/realtime", hub.ServeWS(service))
	})

	return r, nil
}

// CORSミドルウェア
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowedOrigin := os.Getenv("FRONTEND_URL")
		if allowedOrigin == "" {
			allowedOrigin = "http://localhost:5193"
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
