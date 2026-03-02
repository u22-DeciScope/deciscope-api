package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeleteAccountRequiresCSRFToken(t *testing.T) {
	server, repository := newTestAuthServer()
	sessionID, csrfToken := exchangeSession(t, server, "delete-no-csrf")

	request := httptest.NewRequest(http.MethodPost, "/api/auth/delete", nil)
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

func TestDeleteAccountMarksUserDeletedAndRevokesSessions(t *testing.T) {
	server, repository := newTestAuthServer()
	sessionID, csrfToken := exchangeSession(t, server, "delete-ok")

	session, found, err := repository.FindSessionByID(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("expected no repository error, got %v", err)
	}
	if !found {
		t.Fatal("expected session to exist")
	}

	request := httptest.NewRequest(http.MethodPost, "/api/auth/delete", nil)
	request.Header.Set("X-CSRF-Token", csrfToken)
	request.AddCookie(&http.Cookie{Name: "sid", Value: sessionID})
	request.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, response.Code)
	}

	user, found, err := repository.FindUserByID(context.Background(), session.UserID)
	if err != nil {
		t.Fatalf("expected no repository error, got %v", err)
	}
	if !found {
		t.Fatal("expected user to exist")
	}
	if user.NormalizedStatus() != "deleted" {
		t.Fatalf("expected deleted status, got %q", user.NormalizedStatus())
	}

	session, found, err = repository.FindSessionByID(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("expected no repository error, got %v", err)
	}
	if !found {
		t.Fatal("expected session to exist")
	}
	if session.RevokedAt == nil {
		t.Fatal("expected session to be revoked")
	}
	if session.RevokeReason != "user_deleted" {
		t.Fatalf("expected revoke reason user_deleted, got %q", session.RevokeReason)
	}
}
