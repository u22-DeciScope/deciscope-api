package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestServerFailsWithoutDatabaseURL(t *testing.T) {
	setRequiredTranscriptEnv(t)
	t.Setenv("DATABASE_URL", "")
	if _, err := NewServerRuntime(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("NewServerRuntime() error = %v, want missing DATABASE_URL error", err)
	}
}

func TestServerFailsWhenPostgresIsUnavailable(t *testing.T) {
	setRequiredTranscriptEnv(t)
	t.Setenv("DECISCOPE_TRANSCRIPT_ONLY", "false")
	t.Setenv("DATABASE_URL", "postgres://deciscope:deciscope@127.0.0.1:1/deciscope?sslmode=disable&connect_timeout=1")
	if _, err := NewServerRuntime(); err == nil || !strings.Contains(err.Error(), "ping postgres") {
		t.Fatalf("NewServerRuntime() error = %v, want postgres connection error", err)
	}
}

func TestTranscriptOnlyServerFailsWhenPostgresIsUnavailable(t *testing.T) {
	setRequiredTranscriptEnv(t)
	t.Setenv("DECISCOPE_TRANSCRIPT_ONLY", "true")
	t.Setenv("DATABASE_URL", "postgres://deciscope:deciscope@127.0.0.1:1/deciscope?sslmode=disable&connect_timeout=1")

	if _, err := NewServerRuntime(); err == nil || !strings.Contains(err.Error(), "open transcript postgres") {
		t.Fatalf("NewServerRuntime() error = %v, want transcript postgres connection error", err)
	}
}

func TestServerFailsWhenIngestAPIKeyIsPlaceholder(t *testing.T) {
	t.Setenv("DECISCOPE_INGEST_API_KEY", ingestAPIKeyPlaceholder)
	t.Setenv("DATABASE_URL", "postgres://deciscope:deciscope@127.0.0.1:1/deciscope?sslmode=disable&connect_timeout=1")
	if _, err := NewServerRuntime(); err == nil || !strings.Contains(err.Error(), "must be replaced") {
		t.Fatalf("NewServerRuntime() error = %v, want ingest api key placeholder error", err)
	}
}

func TestServerProtectsWorkspaceAndMeetingAPIs(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set")
	}
	setRequiredTranscriptEnv(t)
	t.Setenv("DATABASE_URL", databaseURL)
	runtime, err := NewServerRuntime()
	if err != nil {
		t.Fatalf("NewServerRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	for _, path := range []string{"/v1/workspaces", "/v1/workspaces/w_test/meetings", "/v1/meetings/m_test"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		runtime.Handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want %d", path, resp.Code, http.StatusUnauthorized)
		}
	}
}

func setRequiredTranscriptEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DECISCOPE_INGEST_API_KEY", "0123456789abcdef0123456789abcdef")
}
