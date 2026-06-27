package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerFailsWithoutDatabaseURL(t *testing.T) {
	setRequiredTranscriptEnv(t)
	t.Setenv("DATABASE_URL", "")
	if _, err := NewServer(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("NewServer() error = %v, want missing DATABASE_URL error", err)
	}
}

func TestServerFailsWhenPostgresIsUnavailable(t *testing.T) {
	setRequiredTranscriptEnv(t)
	t.Setenv("DECISCOPE_TRANSCRIPT_ONLY", "false")
	t.Setenv("DATABASE_URL", "postgres://deciscope:deciscope@127.0.0.1:1/deciscope?sslmode=disable&connect_timeout=1")
	if _, err := NewServer(); err == nil || !strings.Contains(err.Error(), "ping postgres") {
		t.Fatalf("NewServer() error = %v, want postgres connection error", err)
	}
}

func TestTranscriptOnlyServerStartsWithoutPostgres(t *testing.T) {
	setRequiredTranscriptEnv(t)
	t.Setenv("DECISCOPE_TRANSCRIPT_ONLY", "true")
	t.Setenv("DATABASE_URL", "postgres://deciscope:deciscope@127.0.0.1:1/deciscope?sslmode=disable&connect_timeout=1")

	runtime, err := NewServerRuntime()
	if err != nil {
		t.Fatalf("NewServerRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	health := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("/healthz response = %d %s", health.Code, health.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcript-segments", strings.NewReader(`{
		"eventId":"manual-test-call:1",
		"callId":"manual-test-call",
		"sequenceNo":1,
		"recognizedAtUtc":"2026-06-25T13:20:01.1234567+00:00",
		"offsetTicks":0,
		"durationTicks":10000000,
		"text":"Go APIへの保存テストです。"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", "0123456789abcdef0123456789abcdef")
	resp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("transcript response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestServerFailsWhenIngestAPIKeyIsPlaceholder(t *testing.T) {
	t.Setenv("DECISCOPE_GO_SQLITE_PATH", filepath.Join(t.TempDir(), "deciscope-go.db"))
	t.Setenv("DECISCOPE_INGEST_API_KEY", ingestAPIKeyPlaceholder)
	t.Setenv("DATABASE_URL", "postgres://deciscope:deciscope@127.0.0.1:1/deciscope?sslmode=disable&connect_timeout=1")
	if _, err := NewServer(); err == nil || !strings.Contains(err.Error(), "must be replaced") {
		t.Fatalf("NewServer() error = %v, want ingest api key placeholder error", err)
	}
}

func TestServerProtectsWorkspaceAndMeetingAPIs(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set")
	}
	setRequiredTranscriptEnv(t)
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

func setRequiredTranscriptEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DECISCOPE_GO_SQLITE_PATH", filepath.Join(t.TempDir(), "deciscope-go.db"))
	t.Setenv("DECISCOPE_INGEST_API_KEY", "0123456789abcdef0123456789abcdef")
}
