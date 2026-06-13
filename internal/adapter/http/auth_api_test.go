package httpadapter

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	appauth "deciscope-core-api/internal/application/auth"
)

func TestAuthAPIUsesAuthUseCase(t *testing.T) {
	api := NewAuthAPI(fakeAuthUseCases{result: &appauth.LoginResult{
		UserID: 42, UID: "firebase-uid", Email: "user@example.com", Name: "User", AuthProvider: "firebase",
	}})
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"idToken":"token"}`))
	response := httptest.NewRecorder()

	api.Login(response, request)

	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"id":42`)) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestAuthAPIMapsInvalidToken(t *testing.T) {
	api := NewAuthAPI(fakeAuthUseCases{err: appauth.ErrInvalidToken})
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

var _ AuthUseCases = fakeAuthUseCases{}
