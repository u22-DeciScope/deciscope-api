package httpadapter

import (
	"context"
	"errors"
	"net/http"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

type AccessUseCases interface {
	GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error)
	CanAccessMeeting(ctx context.Context, userID, meetingID string) error
	CanAccessJob(ctx context.Context, userID, jobID string) error
}

func requireWorkspaceAccess(service AccessUseCases) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, _ := authmiddleware.SessionFromContext(r.Context())
			if session == nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
				return
			}
			if _, err := service.GetWorkspace(r.Context(), session.User.ID, chi.URLParam(r, "workspace_code")); err != nil {
				writeAccessError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func requireMeetingAccess(service AccessUseCases) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, _ := authmiddleware.SessionFromContext(r.Context())
			if session == nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
				return
			}
			if err := service.CanAccessMeeting(r.Context(), session.User.ID, chi.URLParam(r, "meeting_id")); err != nil {
				writeAccessError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func requireRealtimeAccess(service AccessUseCases) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, _ := authmiddleware.SessionFromContext(r.Context())
			if session == nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
				return
			}
			if err := service.CanAccessMeeting(r.Context(), session.User.ID, r.URL.Query().Get("meeting_id")); err != nil {
				writeAccessError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func requireJobAccess(service AccessUseCases) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, _ := authmiddleware.SessionFromContext(r.Context())
			if session == nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
				return
			}
			if err := service.CanAccessJob(r.Context(), session.User.ID, chi.URLParam(r, "job_id")); err != nil {
				writeAccessError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeAccessError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "access_check_failed", err.Error())
}
