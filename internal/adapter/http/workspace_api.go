package httpadapter

import (
	"context"
	"net/http"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

type WorkspaceUseCases interface {
	ListWorkspaces(ctx context.Context, userID string) ([]domain.Workspace, error)
	GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error)
	UpdateWorkspaceName(ctx context.Context, userID, workspaceID, name string) (*domain.Workspace, error)
	ListMembers(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceMember, error)
	CreateInvitation(ctx context.Context, userID, workspaceID, email, role string) (*domain.WorkspaceInvitation, error)
	ListInvitations(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceInvitation, error)
	RevokeInvitation(ctx context.Context, userID, workspaceID, invitationID string) error
	RemoveMember(ctx context.Context, userID, workspaceID, memberID string) error
	UpdateMemberRole(ctx context.Context, userID, workspaceID, memberID, role string) (*domain.WorkspaceMember, error)
}

type WorkspaceAPI struct {
	service     WorkspaceUseCases
	connections ConnectionCloser
}

func NewWorkspaceAPI(service WorkspaceUseCases, connections ConnectionCloser) *WorkspaceAPI {
	return &WorkspaceAPI{service: service, connections: connections}
}

func (api *WorkspaceAPI) List(w http.ResponseWriter, r *http.Request) {
	values, err := api.service.ListWorkspaces(r.Context(), currentUserID(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if values == nil {
		values = []domain.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": values})
}

func (api *WorkspaceAPI) Get(w http.ResponseWriter, r *http.Request) {
	value, err := api.service.GetWorkspace(r.Context(), currentUserID(r), chi.URLParam(r, "workspace_code"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *WorkspaceAPI) Update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	value, err := api.service.UpdateWorkspaceName(r.Context(), currentUserID(r), chi.URLParam(r, "workspace_code"), req.Name)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *WorkspaceAPI) ListMembers(w http.ResponseWriter, r *http.Request) {
	values, err := api.service.ListMembers(r.Context(), currentUserID(r), chi.URLParam(r, "workspace_code"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if values == nil {
		values = []domain.WorkspaceMember{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": values})
}

func (api *WorkspaceAPI) CreateInvitation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	value, err := api.service.CreateInvitation(r.Context(), currentUserID(r), chi.URLParam(r, "workspace_code"), req.Email, req.Role)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (api *WorkspaceAPI) ListInvitations(w http.ResponseWriter, r *http.Request) {
	values, err := api.service.ListInvitations(r.Context(), currentUserID(r), chi.URLParam(r, "workspace_code"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if values == nil {
		values = []domain.WorkspaceInvitation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": values})
}

func (api *WorkspaceAPI) RevokeInvitation(w http.ResponseWriter, r *http.Request) {
	err := api.service.RevokeInvitation(r.Context(), currentUserID(r), chi.URLParam(r, "workspace_code"), chi.URLParam(r, "invitation_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *WorkspaceAPI) RemoveMember(w http.ResponseWriter, r *http.Request) {
	err := api.service.RemoveMember(r.Context(), currentUserID(r), chi.URLParam(r, "workspace_code"), chi.URLParam(r, "member_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if api.connections != nil {
		api.connections.CloseWorkspaceMember(chi.URLParam(r, "workspace_code"), chi.URLParam(r, "member_id"))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *WorkspaceAPI) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	value, err := api.service.UpdateMemberRole(r.Context(), currentUserID(r), chi.URLParam(r, "workspace_code"), chi.URLParam(r, "member_id"), req.Role)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func currentUserID(r *http.Request) string {
	session, _ := authmiddleware.SessionFromContext(r.Context())
	if session == nil || session.User == nil {
		return ""
	}
	return session.User.ID
}

func currentUserEmail(r *http.Request) string {
	session, _ := authmiddleware.SessionFromContext(r.Context())
	if session == nil || session.User == nil {
		return ""
	}
	return session.User.Email
}
