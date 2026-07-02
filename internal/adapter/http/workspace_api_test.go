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

type fakeWorkspaceUseCases struct{}

func (fakeWorkspaceUseCases) ListWorkspaces(context.Context, string) ([]domain.Workspace, error) {
	return nil, nil
}

func (fakeWorkspaceUseCases) GetWorkspace(context.Context, string, string) (*domain.Workspace, error) {
	return &domain.Workspace{ID: "w_test", Role: "owner"}, nil
}

func (fakeWorkspaceUseCases) UpdateWorkspaceName(context.Context, string, string, string) (*domain.Workspace, error) {
	return nil, domain.ErrInvalidArgument
}

func (fakeWorkspaceUseCases) ListMembers(context.Context, string, string) ([]domain.WorkspaceMember, error) {
	return nil, nil
}

func (fakeWorkspaceUseCases) CreateInvitation(context.Context, string, string, string, string) (*domain.WorkspaceInvitation, error) {
	return nil, domain.ErrConflict
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
