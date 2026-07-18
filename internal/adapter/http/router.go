package httpadapter

import (
	"net/http"
	"strings"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

type RouterDependencies struct {
	CoreAPI           *CoreAPI
	AuthAPI           *AuthAPI
	WorkspaceAPI      *WorkspaceAPI
	TranscriptAPI     *TranscriptAPI
	MeetingSessionAPI *MeetingSessionAPI
	AuthService       authmiddleware.SessionAuthenticator
	Workspace         WorkspaceAccessUseCases
	Access            ResourceAccessUseCases
	Realtime          http.HandlerFunc
	Healthz           http.HandlerFunc
	Readyz            http.HandlerFunc
	CORS              CORSConfig
}

type CORSConfig struct {
	FrontendURL    string
	AllowedOrigins string
}

func NewRouter(deps RouterDependencies) http.Handler {
	r := chi.NewRouter()
	r.Use(corsMiddleware(deps.CORS))
	r.Use(chimiddleware.AllowContentType("application/json"))

	if deps.Healthz != nil {
		r.Get("/healthz", deps.Healthz)
	}
	if deps.Readyz != nil {
		r.Get("/readyz", deps.Readyz)
	}
	if deps.TranscriptAPI != nil {
		r.Post("/api/v1/transcript-segments", deps.TranscriptAPI.Store)
	}
	if deps.MeetingSessionAPI != nil {
		r.Get("/api/v1/debug/meeting-sessions", deps.MeetingSessionAPI.DebugList)
		r.Post("/api/v1/meeting-sessions/cleanup-stale", deps.MeetingSessionAPI.CleanupStale)
		r.Post("/api/v1/meeting-sessions", deps.MeetingSessionAPI.Create)
		r.Get("/api/v1/meeting-sessions/{session_id}", deps.MeetingSessionAPI.Get)
		r.Post("/api/v1/meeting-sessions/{session_id}/end", deps.MeetingSessionAPI.End)
		r.Patch("/api/v1/bot/meeting-sessions/{session_id}/metadata", deps.MeetingSessionAPI.UpdateBotMetadata)
		r.Patch("/api/v1/bot/meeting-sessions/{session_id}/status", deps.MeetingSessionAPI.UpdateBotStatus)
		r.Post("/api/v1/bot/meeting-sessions/{session_id}/heartbeat", deps.MeetingSessionAPI.RecordBotHeartbeat)
	}
	if deps.CoreAPI == nil || deps.AuthAPI == nil || deps.WorkspaceAPI == nil ||
		deps.AuthService == nil || deps.Workspace == nil || deps.Access == nil || deps.Realtime == nil {
		return r
	}
	r.Route("/v1", func(r chi.Router) {
		r.Get("/health", deps.CoreAPI.Health)
		r.Post("/auth/login", deps.AuthAPI.Login)
		// 招待previewは未ログインでも表示できる必要があるため認証不要。
		// tokenが唯一の秘密情報であり、レスポンスは非機密情報のみ。
		r.Get("/invitations/preview", deps.WorkspaceAPI.PreviewInvitation)
		r.Group(func(r chi.Router) {
			r.Use(authmiddleware.SessionAuth(deps.AuthService))
			r.Get("/auth/me", deps.AuthAPI.Me)
			r.Post("/auth/logout", deps.AuthAPI.Logout)
			r.Put("/session/current-workspace", deps.AuthAPI.SetCurrentWorkspace)
			r.Post("/invitations/accept", deps.WorkspaceAPI.AcceptInvitation)
			r.Get("/workspaces", deps.WorkspaceAPI.List)
			r.Post("/workspaces", deps.WorkspaceAPI.Create)
			r.Route("/workspaces/{workspace_code}", func(r chi.Router) {
				r.Use(requireWorkspaceAccess(deps.Workspace))
				r.Get("/", deps.WorkspaceAPI.Get)
				r.With(requireWorkspaceAdminOrOwner(deps.Workspace)).Patch("/", deps.WorkspaceAPI.Update)
				r.Get("/members", deps.WorkspaceAPI.ListMembers)
				r.With(requireWorkspaceOwnerRole(deps.Workspace)).Patch("/members/{member_id}", deps.WorkspaceAPI.UpdateMemberRole)
				r.With(requireWorkspaceAdminOrOwner(deps.Workspace)).Delete("/members/{member_id}", deps.WorkspaceAPI.RemoveMember)
				r.With(requireWorkspaceAdminOrOwner(deps.Workspace)).Get("/invitations", deps.WorkspaceAPI.ListInvitations)
				r.With(requireWorkspaceAdminOrOwner(deps.Workspace)).Post("/invitations", deps.WorkspaceAPI.CreateInvitation)
				r.With(requireWorkspaceAdminOrOwner(deps.Workspace)).Delete("/invitations/{invitation_id}", deps.WorkspaceAPI.RevokeInvitation)
				if deps.MeetingSessionAPI != nil {
					r.Get("/meeting-sessions", deps.MeetingSessionAPI.ListForWorkspace)
					r.With(requireWorkspaceAdminOrOwner(deps.Workspace)).Post("/meeting-sessions", deps.MeetingSessionAPI.CreateForWorkspace)
					r.Route("/meeting-sessions/{session_id}", func(r chi.Router) {
						r.Get("/", deps.MeetingSessionAPI.GetForWorkspace)
						r.With(requireWorkspaceAdminOrOwner(deps.Workspace)).Post("/end", deps.MeetingSessionAPI.EndForWorkspace)
						r.Get("/transcript-segments", deps.MeetingSessionAPI.ListWorkspaceTranscriptSegments)
						r.Get("/transcript-stream", deps.MeetingSessionAPI.StreamWorkspaceTranscriptSegments)
						r.Get("/ai-analyses", deps.MeetingSessionAPI.GetWorkspaceAIAnalyses)
					})
				}
				r.Get("/meetings", deps.CoreAPI.ListMeetings)
				r.Post("/meetings", deps.CoreAPI.CreateMeeting)
			})
			r.Route("/meetings/{meeting_id}", func(r chi.Router) {
				r.Use(requireMeetingAccess(deps.Access))
				r.Get("/", deps.CoreAPI.GetMeeting)
				r.Post("/join-token", deps.CoreAPI.CreateJoinToken)
				r.Post("/end", deps.CoreAPI.EndMeeting)
				r.Get("/events", deps.CoreAPI.ListEvents)
				r.Get("/segments", deps.CoreAPI.ListSegments)
				r.Get("/report", deps.CoreAPI.GetReport)
			})
			r.With(requireRealtimeAccess(deps.Access)).Get("/realtime", deps.Realtime)
		})
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
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Upgrade, Connection, X-DeciScope-Api-Key")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
