package httpadapter

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/domain"
)

func TestAuthAPISetsSessionCookie(t *testing.T) {
	api := NewAuthAPI(fakeAuthUseCases{result: &appauth.LoginResult{
		User:       &domain.User{ID: "u_test", Email: "user@example.com"},
		Workspaces: []domain.Workspace{{ID: "w_test", Name: "User's Workspace", Role: "owner"}},
		Session:    &domain.Session{ID: "s_test", CurrentWorkspaceID: "w_test", ExpiresAt: "2099-01-01T00:00:00Z"},
		Token:      "session-token",
	}}, false, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"idToken":"token"}`))
	response := httptest.NewRecorder()

	api.Login(response, request)

	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"current_workspace_id":"w_test"`)) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) != 1 || response.Result().Cookies()[0].Value != "session-token" {
		t.Fatalf("cookies = %+v", response.Result().Cookies())
	}
}

func TestAuthAPIMapsInvalidToken(t *testing.T) {
	api := NewAuthAPI(fakeAuthUseCases{err: appauth.ErrInvalidToken}, false, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"idToken":"bad"}`))
	response := httptest.NewRecorder()
	api.Login(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

type fakeAuthUseCases struct {
	result *appauth.LoginResult
	err    error
}

func (f fakeAuthUseCases) Login(context.Context, string) (*appauth.LoginResult, error) {
	return f.result, f.err
}
func (fakeAuthUseCases) Logout(context.Context, string) error { return nil }
func (fakeAuthUseCases) SetCurrentWorkspace(context.Context, string, string, string) error {
	return nil
}

var _ AuthUseCases = fakeAuthUseCases{}
