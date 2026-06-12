package httpadapter

import (
	"net/http"
	"strings"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

type RouterDependencies struct {
	CoreAPI      *CoreAPI
	AuthAPI      *AuthAPI
	Realtime     http.HandlerFunc
	AuthVerifier authmiddleware.TokenVerifier
	CORS         CORSConfig
}

type CORSConfig struct {
	FrontendURL    string
	AllowedOrigins string
}

func NewRouter(deps RouterDependencies) http.Handler {
	r := chi.NewRouter()
	r.Use(corsMiddleware(deps.CORS))
	r.Use(chimiddleware.AllowContentType("application/json", "multipart/form-data"))

	r.Route("/v1", func(r chi.Router) {
		r.Get("/health", deps.CoreAPI.Health)
		r.Post("/auth/login", deps.AuthAPI.Login)
		r.Group(func(r chi.Router) {
			r.Use(authmiddleware.FirebaseAuthMiddleware(deps.AuthVerifier))
			r.Get("/auth/me", MeHandler)
			r.Get("/auth/health", Health)
		})
		r.Get("/meetings", deps.CoreAPI.ListMeetings)
		r.Post("/meetings", deps.CoreAPI.CreateMeeting)
		r.Get("/meetings/{meeting_id}", deps.CoreAPI.GetMeeting)
		r.Post("/meetings/{meeting_id}/join-token", deps.CoreAPI.CreateJoinToken)
		r.Post("/meetings/{meeting_id}/end", deps.CoreAPI.EndMeeting)
		r.Get("/meetings/{meeting_id}/events", deps.CoreAPI.ListEvents)
		r.Get("/meetings/{meeting_id}/segments", deps.CoreAPI.ListSegments)
		r.Get("/meetings/{meeting_id}/report", deps.CoreAPI.GetReport)
		r.Get("/fixtures", deps.CoreAPI.ListFixtures)
		r.Post("/meetings/{meeting_id}/replay/start", deps.CoreAPI.ReplayStart)
		r.Post("/meetings/{meeting_id}/replay/pause", deps.CoreAPI.ReplayPause)
		r.Post("/meetings/{meeting_id}/replay/resume", deps.CoreAPI.ReplayResume)
		r.Post("/meetings/{meeting_id}/replay/reset", deps.CoreAPI.ReplayReset)
		r.Post("/uploads", deps.CoreAPI.Upload)
		r.Get("/jobs/{job_id}", deps.CoreAPI.GetJob)
		r.Get("/realtime", deps.Realtime)
	})
	return r
}

func corsMiddleware(config CORSConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowedOrigin := config.FrontendURL
			if allowedOrigin == "" {
				allowedOrigin = "http://localhost:5193"
			}
			responseOrigin := allowedOrigin
			if list := config.AllowedOrigins; list != "" {
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
}
