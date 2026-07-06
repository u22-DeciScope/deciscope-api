package httpadapter

import (
	"context"
	"errors"
	"log"
	"net/http"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	appworkspace "deciscope-core-api/internal/application/workspace"
	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

type WorkspaceUseCases interface {
	ListWorkspaces(ctx context.Context, userID string) ([]domain.Workspace, error)
	GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error)
	CreateWorkspace(ctx context.Context, userID, name, description string) (*domain.Workspace, error)
	UpdateWorkspace(ctx context.Context, userID, workspaceID string, name, description *string) (*domain.Workspace, error)
	ListMembers(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceMember, error)
	CreateInvitation(ctx context.Context, userID, userDisplayName, workspaceID, email, role string) (*domain.WorkspaceInvitation, error)
	ListInvitations(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceInvitation, error)
	RevokeInvitation(ctx context.Context, userID, workspaceID, invitationID string) error
	PreviewInvitation(ctx context.Context, rawToken string) (*appworkspace.InvitationPreview, error)
	AcceptInvitation(ctx context.Context, userID, userEmail, rawToken string) (*domain.Workspace, error)
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

func (api *WorkspaceAPI) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	value, err := api.service.CreateWorkspace(r.Context(), currentUserID(r), req.Name, req.Description)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	log.Printf("workspace created: workspace_id=%q owner_user_id=%q", value.ID, currentUserID(r))
	writeJSON(w, http.StatusCreated, value)
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
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	value, err := api.service.UpdateWorkspace(r.Context(), currentUserID(r), chi.URLParam(r, "workspace_code"), req.Name, req.Description)
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
	value, err := api.service.CreateInvitation(r.Context(), currentUserID(r), currentUserDisplayName(r), chi.URLParam(r, "workspace_code"), req.Email, req.Role)
	if err != nil {
		if errors.Is(err, appworkspace.ErrInvitationEmailDelivery) {
			log.Printf("workspace invitation email delivery failed: workspace_id=%q error=%v", chi.URLParam(r, "workspace_code"), err)
			writeError(w, http.StatusInternalServerError, "invitation_email_failed", "招待メールを送信できませんでした")
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

// PreviewInvitation は招待リンクを開いた際の承認前情報を返す。認証不要。
func (api *WorkspaceAPI) PreviewInvitation(w http.ResponseWriter, r *http.Request) {
	value, err := api.service.PreviewInvitation(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		writeInvitationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *WorkspaceAPI) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	value, err := api.service.AcceptInvitation(r.Context(), currentUserID(r), currentUserEmail(r), req.Token)
	if err != nil {
		writeInvitationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func writeInvitationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appworkspace.ErrInvitationExpired):
		writeError(w, http.StatusGone, "invitation_expired", "この招待リンクは期限切れです")
	case errors.Is(err, appworkspace.ErrInvitationRevoked):
		writeError(w, http.StatusGone, "invitation_revoked", "この招待は取り消されています")
	case errors.Is(err, appworkspace.ErrInvitationAlreadyAccepted):
		writeError(w, http.StatusConflict, "invitation_already_accepted", "この招待リンクは使用済みです")
	case errors.Is(err, appworkspace.ErrInvitationEmailMismatch):
		writeError(w, http.StatusForbidden, "invitation_email_mismatch", "ログイン中のアカウントのメールアドレスが招待先と一致しません")
	default:
		writeStoreError(w, err)
	}
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

func currentUserDisplayName(r *http.Request) string {
	session, _ := authmiddleware.SessionFromContext(r.Context())
	if session == nil || session.User == nil {
		return ""
	}
	return session.User.DisplayName
}
