package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"time"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/domain"
)

type AuthUseCases interface {
	Login(ctx context.Context, idToken string) (*appauth.LoginResult, error)
	Logout(ctx context.Context, sessionID string) error
	SetCurrentWorkspace(ctx context.Context, sessionID, userID, workspaceID string) error
}

type AuthAPI struct {
	service      AuthUseCases
	cookieSecure bool
	connections  ConnectionCloser
}

type ConnectionCloser interface {
	CloseSession(sessionID string)
	CloseWorkspaceMember(workspaceID, userID string)
}

func NewAuthAPI(service AuthUseCases, cookieSecure bool, connections ConnectionCloser) *AuthAPI {
	return &AuthAPI{service: service, cookieSecure: cookieSecure, connections: connections}
}

func (api *AuthAPI) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDToken string `json:"idToken"`
	}
	if err := decodeJSON(r, &req); err != nil || req.IDToken == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "idToken is required")
		return
	}
	result, err := api.service.Login(r.Context(), req.IDToken)
	if errors.Is(err, appauth.ErrUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable", err.Error())
		return
	}
	if errors.Is(err, appauth.ErrInvalidToken) || errors.Is(err, appauth.ErrEmailRequired) {
		writeError(w, http.StatusUnauthorized, "invalid_token", err.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	api.setSessionCookie(w, result.Token, result.Session.ExpiresAt)
	writeJSON(w, http.StatusOK, sessionResponse(result.User, result.Workspaces, result.Session))
}

func (api *AuthAPI) Me(w http.ResponseWriter, r *http.Request) {
	session, ok := authmiddleware.SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(session.User, session.Workspaces, session.Session))
}

func (api *AuthAPI) Logout(w http.ResponseWriter, r *http.Request) {
	session, ok := authmiddleware.SessionFromContext(r.Context())
	if ok {
		_ = api.service.Logout(r.Context(), session.Session.ID)
		if api.connections != nil {
			api.connections.CloseSession(session.Session.ID)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: authmiddleware.SessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: api.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (api *AuthAPI) SetCurrentWorkspace(w http.ResponseWriter, r *http.Request) {
	session, ok := authmiddleware.SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := decodeJSON(r, &req); err != nil || req.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "workspace_id is required")
		return
	}
	if err := api.service.SetCurrentWorkspace(r.Context(), session.Session.ID, session.User.ID, req.WorkspaceID); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *AuthAPI) setSessionCookie(w http.ResponseWriter, token, expiresAt string) {
	expires, _ := time.Parse(time.RFC3339, expiresAt)
	http.SetCookie(w, &http.Cookie{
		Name: authmiddleware.SessionCookieName, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: api.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func sessionResponse(user *domain.User, workspaces []domain.Workspace, session *domain.Session) map[string]any {
	return map[string]any{
		"user": user, "workspaces": workspaces, "current_workspace_id": session.CurrentWorkspaceID,
		"expires_at": session.ExpiresAt,
	}
}
