package httpadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	appauth "deciscope-core-api/internal/application/auth"
	appworkspace "deciscope-core-api/internal/application/workspace"
	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

func TestWorkspaceAPIMapsValidationAndConflictErrors(t *testing.T) {
	api := NewWorkspaceAPI(fakeWorkspaceUseCases{}, nil)
	router := chi.NewRouter()
	router.Use(authmiddleware.SessionAuth(fakeSessionAuthenticator{}))
	router.Route("/workspaces/{workspace_code}", func(r chi.Router) {
		r.Patch("/", api.Update)
		r.Post("/invitations", api.CreateInvitation)
	})

	update := serveAuthenticatedJSON(t, router, http.MethodPatch, "/workspaces/w_test/", `{"name":""}`)
	assertErrorResponse(t, update, http.StatusBadRequest, "invalid_request")

	invite := serveAuthenticatedJSON(t, router, http.MethodPost, "/workspaces/w_test/invitations", `{"email":"taken@example.com"}`)
	assertErrorResponse(t, invite, http.StatusConflict, "conflict")
}

func TestWorkspaceAPIMapsInvitationErrors(t *testing.T) {
	api := NewWorkspaceAPI(fakeWorkspaceUseCases{}, nil)
	router := chi.NewRouter()
	router.Get("/invitations/preview", api.PreviewInvitation)
	router.Group(func(r chi.Router) {
		r.Use(authmiddleware.SessionAuth(fakeSessionAuthenticator{}))
		r.Post("/invitations/accept", api.AcceptInvitation)
	})

	// preview: 期限切れ → 410 (認証不要で呼べる)
	previewRequest := httptest.NewRequest(http.MethodGet, "/invitations/preview?token=raw", nil)
	previewResponse := httptest.NewRecorder()
	router.ServeHTTP(previewResponse, previewRequest)
	assertErrorResponse(t, previewResponse, http.StatusGone, "invitation_expired")

	// accept: メール不一致 → 403
	accept := serveAuthenticatedJSON(t, router, http.MethodPost, "/invitations/accept", `{"token":"raw"}`)
	assertErrorResponse(t, accept, http.StatusForbidden, "invitation_email_mismatch")
}

func TestWorkspaceRoleMiddlewareEnforcesRoles(t *testing.T) {
	allowed := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	cases := []struct {
		name       string
		role       string
		middleware func(WorkspaceAccessUseCases) func(http.Handler) http.Handler
		wantStatus int
	}{
		{"viewer cannot manage meeting sessions", "viewer", requireWorkspaceAdminOrOwner, http.StatusForbidden},
		{"admin can manage meeting sessions", "admin", requireWorkspaceAdminOrOwner, http.StatusOK},
		{"owner can manage meeting sessions", "owner", requireWorkspaceAdminOrOwner, http.StatusOK},
		{"admin cannot change member roles", "admin", requireWorkspaceOwnerRole, http.StatusForbidden},
		{"owner can change member roles", "owner", requireWorkspaceOwnerRole, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := chi.NewRouter()
			router.Use(authmiddleware.SessionAuth(fakeSessionAuthenticator{}))
			router.Route("/workspaces/{workspace_code}", func(r chi.Router) {
				r.With(tc.middleware(fakeWorkspaceAccess{role: tc.role})).Post("/meeting-sessions", allowed)
			})
			response := serveAuthenticatedJSON(t, router, http.MethodPost, "/workspaces/w_test/meeting-sessions", `{}`)
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, body = %s, want %d", response.Code, response.Body.String(), tc.wantStatus)
			}
		})
	}
}

func TestWorkspaceRoleMiddlewareRejectsNonMembers(t *testing.T) {
	router := chi.NewRouter()
	router.Use(authmiddleware.SessionAuth(fakeSessionAuthenticator{}))
	router.Route("/workspaces/{workspace_code}", func(r chi.Router) {
		r.With(requireWorkspaceAccess(fakeWorkspaceAccess{err: domain.ErrNotFound})).Get("/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})
	response := serveAuthenticatedJSON(t, router, http.MethodGet, "/workspaces/w_other/", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s, want 404", response.Code, response.Body.String())
	}
}

type fakeWorkspaceAccess struct {
	role string
	err  error
}

func (f fakeWorkspaceAccess) GetWorkspace(context.Context, string, string) (*domain.Workspace, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.Workspace{ID: "w_test", Role: f.role}, nil
}

type fakeWorkspaceUseCases struct{}

func (fakeWorkspaceUseCases) ListWorkspaces(context.Context, string) ([]domain.Workspace, error) {
	return nil, nil
}

func (fakeWorkspaceUseCases) GetWorkspace(context.Context, string, string) (*domain.Workspace, error) {
	return &domain.Workspace{ID: "w_test", Role: "owner"}, nil
}

func (fakeWorkspaceUseCases) CreateWorkspace(context.Context, string, string, string) (*domain.Workspace, error) {
	return nil, domain.ErrInvalidArgument
}

func (fakeWorkspaceUseCases) UpdateWorkspace(context.Context, string, string, *string, *string) (*domain.Workspace, error) {
	return nil, domain.ErrInvalidArgument
}

func (fakeWorkspaceUseCases) ListMembers(context.Context, string, string) ([]domain.WorkspaceMember, error) {
	return nil, nil
}

func (fakeWorkspaceUseCases) CreateInvitation(context.Context, string, string, string, string, string) (*domain.WorkspaceInvitation, error) {
	return nil, domain.ErrConflict
}

func (fakeWorkspaceUseCases) PreviewInvitation(context.Context, string) (*appworkspace.InvitationPreview, error) {
	return nil, appworkspace.ErrInvitationExpired
}

func (fakeWorkspaceUseCases) AcceptInvitation(context.Context, string, string, string) (*domain.Workspace, error) {
	return nil, appworkspace.ErrInvitationEmailMismatch
}

func (fakeWorkspaceUseCases) ListInvitations(context.Context, string, string) ([]domain.WorkspaceInvitation, error) {
	return nil, nil
}

func (fakeWorkspaceUseCases) RevokeInvitation(context.Context, string, string, string) error {
	return nil
}

func (fakeWorkspaceUseCases) RemoveMember(context.Context, string, string, string) error {
	return nil
}

func (fakeWorkspaceUseCases) UpdateMemberRole(context.Context, string, string, string, string) (*domain.WorkspaceMember, error) {
	return &domain.WorkspaceMember{}, nil
}

type fakeSessionAuthenticator struct{}

func (fakeSessionAuthenticator) Authenticate(context.Context, string) (*appauth.SessionResult, error) {
	return &appauth.SessionResult{
		User:    &domain.User{ID: "u_test"},
		Session: &domain.Session{ID: "s_test"},
	}, nil
}

func serveAuthenticatedJSON(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: authmiddleware.SessionCookieName, Value: "session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertErrorResponse(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, body = %s, want %d", response.Code, response.Body.String(), status)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != code {
		t.Fatalf("error code = %q, want %q", body.Error.Code, code)
	}
}
