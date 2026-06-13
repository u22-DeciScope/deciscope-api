package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestServerFailsWithoutDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := NewServer(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("NewServer() error = %v, want missing DATABASE_URL error", err)
	}
}

func TestServerFailsWhenPostgresIsUnavailable(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://deciscope:deciscope@127.0.0.1:1/deciscope?sslmode=disable&connect_timeout=1")
	if _, err := NewServer(); err == nil || !strings.Contains(err.Error(), "ping postgres") {
		t.Fatalf("NewServer() error = %v, want postgres connection error", err)
	}
}

func TestServerProtectsWorkspaceAndMeetingAPIs(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set")
	}
	t.Setenv("DATABASE_URL", databaseURL)
	handler, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	for _, path := range []string{"/v1/workspaces", "/v1/workspaces/w_test/meetings", "/v1/meetings/m_test"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want %d", path, resp.Code, http.StatusUnauthorized)
		}
	}
}
