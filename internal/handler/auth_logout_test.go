package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"deciscope-core-api/internal/infra/auth"
	"deciscope-core-api/internal/infra/memory"
	"deciscope-core-api/internal/usecase"
)

type testClock struct {
	now time.Time
}

func (c testClock) Now() time.Time {
	return c.now
}

func TestLogoutRequiresCSRFToken(t *testing.T) {
	server, repository := newTestAuthServer()
	sessionID, csrfToken := exchangeSession(t, server, "alice")

	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: "sid", Value: sessionID})
	request.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, response.Code)
	}

	session, found, err := repository.FindSessionByID(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("expected no repository error, got %v", err)
	}
	if !found {
		t.Fatal("expected session to exist")
	}
	if session.RevokedAt != nil {
		t.Fatal("expected session to remain active")
	}
}

func TestLogoutRevokesCurrentSessionWithCSRFToken(t *testing.T) {
	server, repository := newTestAuthServer()
	sessionID, csrfToken := exchangeSession(t, server, "bob")

	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	request.Header.Set("X-CSRF-Token", csrfToken)
	request.AddCookie(&http.Cookie{Name: "sid", Value: sessionID})
	request.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, response.Code)
	}

	session, found, err := repository.FindSessionByID(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("expected no repository error, got %v", err)
	}
	if !found {
		t.Fatal("expected session to exist")
	}
	if session.RevokedAt == nil {
		t.Fatal("expected session to be revoked")
	}
}

func newTestAuthServer() (http.Handler, *memory.AuthRepository) {
	repository := memory.NewAuthRepository()
	clock := testClock{now: time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)}

	return NewAuthHandler(
		usecase.NewExchangeSession(repository, auth.NewDevelopmentTokenVerifier("dev:"), clock),
		usecase.NewGetMe(),
		usecase.NewLogout(repository),
		usecase.NewLogoutAll(repository),
		usecase.NewListSessions(repository),
		usecase.NewRevokeSession(repository),
		repository,
		clock,
		nil,
	).Router(), repository
}

func exchangeSession(t *testing.T, server http.Handler, subject string) (string, string) {
	t.Helper()

	body, err := json.Marshal(map[string]string{
		"device_type": "web",
		"device_name": "test-device",
	})
	if err != nil {
		t.Fatalf("expected no marshal error, got %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/auth/exchange", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer dev:"+subject)
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, response.Code)
	}

	var sessionID string
	var csrfToken string
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "sid" {
			sessionID = cookie.Value
		}
		if cookie.Name == "csrf" {
			csrfToken = cookie.Value
		}
	}

	if sessionID == "" {
		t.Fatal("expected sid cookie")
	}
	if csrfToken == "" {
		t.Fatal("expected csrf cookie")
	}

	return sessionID, csrfToken
}
