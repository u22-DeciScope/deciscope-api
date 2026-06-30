package httpadapter

import (
	"context"
	"net/http"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

type WorkspaceAccessUseCases interface {
	GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error)
}

type ResourceAccessUseCases interface {
	CanAccessMeeting(ctx context.Context, userID, meetingID string) error
	CanAccessJob(ctx context.Context, userID, jobID string) error
}

func requireWorkspaceAccess(service WorkspaceAccessUseCases) func(http.Handler) http.Handler {
	return requireAccess(func(r *http.Request, session *appauth.SessionResult) error {
		_, err := service.GetWorkspace(r.Context(), session.User.ID, chi.URLParam(r, "workspace_code"))
		return err
	})
}

func requireWorkspaceOwner(service WorkspaceAccessUseCases) func(http.Handler) http.Handler {
	return requireWorkspaceRole(service, domain.IsWorkspaceOwner)
}

func requireWorkspaceAdminOrOwner(service WorkspaceAccessUseCases) func(http.Handler) http.Handler {
	return requireWorkspaceRole(service, domain.CanManageMeetingSessions)
}

func requireWorkspaceRole(service WorkspaceAccessUseCases, allowed func(string) bool) func(http.Handler) http.Handler {
	return requireAccess(func(r *http.Request, session *appauth.SessionResult) error {
		workspace, err := service.GetWorkspace(r.Context(), session.User.ID, chi.URLParam(r, "workspace_code"))
		if err != nil {
			return err
		}
		if !allowed(workspace.Role) {
			return domain.ErrForbidden
		}
		return nil
	})
}

func requireMeetingAccess(service ResourceAccessUseCases) func(http.Handler) http.Handler {
	return requireAccess(func(r *http.Request, session *appauth.SessionResult) error {
		return service.CanAccessMeeting(r.Context(), session.User.ID, chi.URLParam(r, "meeting_id"))
	})
}

func requireRealtimeAccess(service ResourceAccessUseCases) func(http.Handler) http.Handler {
	return requireAccess(func(r *http.Request, session *appauth.SessionResult) error {
		return service.CanAccessMeeting(r.Context(), session.User.ID, r.URL.Query().Get("meeting_id"))
	})
}

func requireJobAccess(service ResourceAccessUseCases) func(http.Handler) http.Handler {
	return requireAccess(func(r *http.Request, session *appauth.SessionResult) error {
		return service.CanAccessJob(r.Context(), session.User.ID, chi.URLParam(r, "job_id"))
	})
}

func requireAccess(check func(*http.Request, *appauth.SessionResult) error) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, _ := authmiddleware.SessionFromContext(r.Context())
			if session == nil || session.User == nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
				return
			}
			if err := check(r, session); err != nil {
				writeStoreError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
