package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	httpadapter "deciscope-core-api/internal/adapter/http"
	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	"deciscope-core-api/internal/application"
	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/infrastructure/clientdiagnostics"
)

// ブラウザ -> POST /internal/client-diagnostics -> 検証/秘匿処理 ->
// logs/client-diagnostics/{sessionId}.jsonl までを通しで確認する。

type stubDiagnosticsAuthenticator struct{}

func (stubDiagnosticsAuthenticator) Authenticate(context.Context, string) (*appauth.SessionResult, error) {
	return &appauth.SessionResult{
		User:    &domain.User{ID: "u_test"},
		Session: &domain.Session{ID: "s_test"},
	}, nil
}

type stubDiagnosticsWorkspace struct{ err error }

func (s stubDiagnosticsWorkspace) GetWorkspace(context.Context, string, string) (*domain.Workspace, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &domain.Workspace{ID: "w_test", Role: "owner"}, nil
}

type stubDiagnosticsSessions struct{ workspaceID string }

func (s stubDiagnosticsSessions) GetMeetingSession(_ context.Context, sessionID string) (*domain.MeetingSession, error) {
	return &domain.MeetingSession{ID: sessionID, WorkspaceID: s.workspaceID}, nil
}

func newDiagnosticsTestServer(t *testing.T, directory string, workspace httpadapter.WorkspaceAccessUseCases) http.Handler {
	t.Helper()
	fileSink, err := clientdiagnostics.NewFileSink(clientdiagnostics.FileSinkConfig{Directory: directory})
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	service := application.NewClientDiagnosticsService(
		application.WithClientDiagnosticsSink("jsonl", fileSink),
		application.WithClientDiagnosticsLimits(application.ClientDiagnosticsLimits{ThrottleWindow: -1}),
	)
	return httpadapter.NewRouter(httpadapter.RouterDependencies{
		ClientDiagnosticsAPI: httpadapter.NewClientDiagnosticsAPI(
			service, workspace, stubDiagnosticsSessions{workspaceID: "w_test"},
		),
		AuthService: stubDiagnosticsAuthenticator{},
	})
}

func postDiagnostics(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/internal/client-diagnostics", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: authmiddleware.SessionCookieName, Value: "session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestClientDiagnosticsEndToEndWritesSessionJSONL(t *testing.T) {
	directory := t.TempDir()
	handler := newDiagnosticsTestServer(t, directory, stubDiagnosticsWorkspace{})

	body := `{
      "workspaceId": "w_test",
      "sessionId": "session_abc",
      "tabId": "tab_1",
      "frontendBuildVersion": "9f2c1ab",
      "events": [
        {
          "timestamp": "2026-07-25T09:00:00.000Z",
          "event": "snapshot_rejected",
          "route": "/w/w_test/meetings/session_abc",
          "treeVersion": 5,
          "analysisVersion": 5,
          "updatedAt": "2026-07-25T08:59:59.000Z",
          "nodeCount": 6,
          "rootNodeId": "root",
          "sessionStatus": "recording",
          "snapshotSource": "rest",
          "sequence": 41,
          "details": {
            "transport": "rest",
            "reason": "ignored_stale",
            "currentTreeVersion": 5,
            "incomingTreeVersion": 3,
            "authorization": "Bearer should-not-be-stored",
            "userEmail": "member@example.com",
            "transcriptText": "会議の発言そのもの"
          }
        },
        {
          "timestamp": "2026-07-25T09:00:01.000Z",
          "event": "tree_became_empty",
          "route": "/w/w_test/meetings/session_abc",
          "treeVersion": null,
          "analysisVersion": 6,
          "nodeCount": 0,
          "sessionStatus": "recording",
          "snapshotSource": "",
          "sequence": 42,
          "details": {"cause": "analysis_event", "previousNodeCount": 6}
        },
        {
          "timestamp": "2026-07-25T09:00:02.000Z",
          "event": "not_a_real_event",
          "nodeCount": 0
        }
      ]
    }`

	response := postDiagnostics(t, handler, body)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s, want 202", response.Code, response.Body.String())
	}
	var result struct {
		Accepted int            `json:"accepted"`
		Rejected int            `json:"rejected"`
		Reasons  map[string]int `json:"reasons"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Accepted != 2 || result.Rejected != 1 {
		t.Fatalf("result = %+v, want 2 accepted / 1 rejected", result)
	}
	if result.Reasons["unknown_event_name"] != 1 {
		t.Fatalf("reasons = %v, want the unknown event name rejected", result.Reasons)
	}

	path := filepath.Join(directory, "session_abc.jsonl")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2 (the unknown event must not be stored)", len(lines))
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first line: %v", err)
	}
	for key, want := range map[string]any{
		"event":                "snapshot_rejected",
		"sessionId":            "session_abc",
		"workspaceId":          "w_test",
		"tabId":                "tab_1",
		"frontendBuildVersion": "9f2c1ab",
		"route":                "/w/w_test/meetings/session_abc",
		"rootNodeId":           "root",
		"sessionStatus":        "recording",
		"snapshotSource":       "rest",
		"userId":               "u_test",
	} {
		if first[key] != want {
			t.Errorf("%s = %v, want %v", key, first[key], want)
		}
	}
	if first["treeVersion"] != float64(5) || first["nodeCount"] != float64(6) {
		t.Errorf("treeVersion/nodeCount = %v/%v", first["treeVersion"], first["nodeCount"])
	}
	if _, ok := first["receivedAt"].(string); !ok {
		t.Errorf("receivedAt missing: %v", first["receivedAt"])
	}

	details, ok := first["details"].(map[string]any)
	if !ok {
		t.Fatalf("details = %T", first["details"])
	}
	if details["reason"] != "ignored_stale" || details["transport"] != "rest" {
		t.Errorf("details = %v, want the adoption reason preserved", details)
	}
	for _, key := range []string{"authorization", "userEmail", "transcriptText"} {
		if details[key] != "[redacted]" {
			t.Errorf("details[%q] = %v, want redacted", key, details[key])
		}
	}
	stored := string(content)
	for _, secret := range []string{"should-not-be-stored", "member@example.com", "会議の発言そのもの"} {
		if strings.Contains(stored, secret) {
			t.Errorf("stored JSONL contains sensitive value %q", secret)
		}
	}

	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("decode second line: %v", err)
	}
	if second["event"] != "tree_became_empty" || second["nodeCount"] != float64(0) {
		t.Errorf("second line = %v", second)
	}
	if second["treeVersion"] != nil {
		t.Errorf("treeVersion = %v, want null", second["treeVersion"])
	}
}

func TestClientDiagnosticsEndToEndRejectsForeignWorkspace(t *testing.T) {
	directory := t.TempDir()
	handler := newDiagnosticsTestServer(t, directory, stubDiagnosticsWorkspace{err: domain.ErrNotFound})

	body := `{"workspaceId":"w_other","sessionId":"session_abc","events":[{"event":"ws_connected"}]}`
	response := postDiagnostics(t, handler, body)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s, want 404", response.Code, response.Body.String())
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory entries = %d, want nothing written for an unauthorized session", len(entries))
	}
}

func TestClientDiagnosticsBuildsFromConfig(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "client-diagnostics")
	api := buildClientDiagnosticsAPI(
		ClientDiagnosticsConfig{
			Enabled:             true,
			Directory:           directory,
			MaxFileBytes:        1024,
			Retention:           time.Hour,
			MaxEventsPerRequest: 10,
			ThrottleWindow:      time.Second,
		},
		stubDiagnosticsWorkspace{},
		stubDiagnosticsSessions{workspaceID: "w_test"},
	)
	if api == nil {
		t.Fatal("buildClientDiagnosticsAPI() = nil, want an API")
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("log directory not created: %v", err)
	}

	if disabled := buildClientDiagnosticsAPI(
		ClientDiagnosticsConfig{Enabled: false},
		stubDiagnosticsWorkspace{},
		stubDiagnosticsSessions{workspaceID: "w_test"},
	); disabled != nil {
		t.Error("buildClientDiagnosticsAPI() with Enabled=false should return nil")
	}
}
