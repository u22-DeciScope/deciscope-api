package httpadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

type recordingDiagnosticsRecorder struct {
	batches []application.ClientDiagnosticsBatchInput
	err     error
}

func (r *recordingDiagnosticsRecorder) Record(_ context.Context, batch application.ClientDiagnosticsBatchInput) (application.ClientDiagnosticsResult, error) {
	if r.err != nil {
		return application.ClientDiagnosticsResult{}, r.err
	}
	r.batches = append(r.batches, batch)
	return application.ClientDiagnosticsResult{Accepted: len(batch.Events)}, nil
}

type fakeDiagnosticsSessionLookup struct {
	workspaceID string
	err         error
}

func (f fakeDiagnosticsSessionLookup) GetMeetingSession(_ context.Context, sessionID string) (*domain.MeetingSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.MeetingSession{ID: sessionID, WorkspaceID: f.workspaceID}, nil
}

func newDiagnosticsRouter(api *ClientDiagnosticsAPI) http.Handler {
	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(authmiddleware.SessionAuth(fakeSessionAuthenticator{}))
		r.Post("/internal/client-diagnostics", api.Ingest)
	})
	return router
}

const diagnosticsBody = `{"workspaceId":"w_test","sessionId":"session_abc","tabId":"tab_1",` +
	`"frontendBuildVersion":"abc1234","events":[{"event":"tree_became_empty","nodeCount":0}]}`

func TestClientDiagnosticsAPIAcceptsAuthorizedBatch(t *testing.T) {
	recorder := &recordingDiagnosticsRecorder{}
	api := NewClientDiagnosticsAPI(recorder, fakeWorkspaceAccess{role: "viewer"}, fakeDiagnosticsSessionLookup{workspaceID: "w_test"})

	response := serveAuthenticatedJSON(t, newDiagnosticsRouter(api), http.MethodPost, "/internal/client-diagnostics", diagnosticsBody)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s, want 202", response.Code, response.Body.String())
	}
	if len(recorder.batches) != 1 {
		t.Fatalf("recorded batches = %d, want 1", len(recorder.batches))
	}
	batch := recorder.batches[0]
	if batch.WorkspaceID != "w_test" || batch.SessionID != "session_abc" {
		t.Errorf("batch scope = %q/%q", batch.WorkspaceID, batch.SessionID)
	}
	if batch.UserID != "u_test" {
		t.Errorf("batch userId = %q, want the authenticated user", batch.UserID)
	}
	var body struct {
		Accepted int `json:"accepted"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Accepted != 1 {
		t.Errorf("accepted = %d, want 1", body.Accepted)
	}
}

func TestClientDiagnosticsAPIRequiresAuthentication(t *testing.T) {
	recorder := &recordingDiagnosticsRecorder{}
	api := NewClientDiagnosticsAPI(recorder, fakeWorkspaceAccess{}, fakeDiagnosticsSessionLookup{workspaceID: "w_test"})
	router := newDiagnosticsRouter(api)

	request := httptest.NewRequest(http.MethodPost, "/internal/client-diagnostics", strings.NewReader(diagnosticsBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	if len(recorder.batches) != 0 {
		t.Fatalf("recorded batches = %d, want none", len(recorder.batches))
	}
}

func TestClientDiagnosticsAPIRejectsUnauthorizedSessionID(t *testing.T) {
	cases := []struct {
		name       string
		workspace  WorkspaceAccessUseCases
		sessions   ClientDiagnosticsSessionLookup
		wantStatus int
		wantCode   string
	}{
		{
			name:       "not a workspace member",
			workspace:  fakeWorkspaceAccess{err: domain.ErrNotFound},
			sessions:   fakeDiagnosticsSessionLookup{workspaceID: "w_test"},
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "workspace access forbidden",
			workspace:  fakeWorkspaceAccess{err: domain.ErrForbidden},
			sessions:   fakeDiagnosticsSessionLookup{workspaceID: "w_test"},
			wantStatus: http.StatusForbidden,
			wantCode:   "forbidden",
		},
		{
			name:       "session belongs to another workspace",
			workspace:  fakeWorkspaceAccess{role: "owner"},
			sessions:   fakeDiagnosticsSessionLookup{workspaceID: "w_other"},
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "session does not exist",
			workspace:  fakeWorkspaceAccess{role: "owner"},
			sessions:   fakeDiagnosticsSessionLookup{err: domain.ErrNotFound},
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &recordingDiagnosticsRecorder{}
			api := NewClientDiagnosticsAPI(recorder, tc.workspace, tc.sessions)

			response := serveAuthenticatedJSON(t, newDiagnosticsRouter(api), http.MethodPost, "/internal/client-diagnostics", diagnosticsBody)
			assertErrorResponse(t, response, tc.wantStatus, tc.wantCode)
			if len(recorder.batches) != 0 {
				t.Fatalf("recorded batches = %d, want none", len(recorder.batches))
			}
		})
	}
}

func TestClientDiagnosticsAPIValidatesRequestShape(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"missing workspaceId", `{"sessionId":"session_abc","events":[{"event":"ws_connected"}]}`, http.StatusBadRequest, "invalid_request"},
		{"missing sessionId", `{"workspaceId":"w_test","events":[{"event":"ws_connected"}]}`, http.StatusBadRequest, "invalid_request"},
		{"no events", `{"workspaceId":"w_test","sessionId":"session_abc","events":[]}`, http.StatusBadRequest, "invalid_request"},
		{"invalid json", `{"workspaceId":`, http.StatusBadRequest, "invalid_json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &recordingDiagnosticsRecorder{}
			api := NewClientDiagnosticsAPI(recorder, fakeWorkspaceAccess{role: "owner"}, fakeDiagnosticsSessionLookup{workspaceID: "w_test"})

			response := serveAuthenticatedJSON(t, newDiagnosticsRouter(api), http.MethodPost, "/internal/client-diagnostics", tc.body)
			assertErrorResponse(t, response, tc.wantStatus, tc.wantCode)
			if len(recorder.batches) != 0 {
				t.Fatalf("recorded batches = %d, want none", len(recorder.batches))
			}
		})
	}
}

func TestClientDiagnosticsAPIRejectsOversizedBody(t *testing.T) {
	recorder := &recordingDiagnosticsRecorder{}
	api := NewClientDiagnosticsAPI(recorder, fakeWorkspaceAccess{role: "owner"}, fakeDiagnosticsSessionLookup{workspaceID: "w_test"})

	oversized := `{"workspaceId":"w_test","sessionId":"session_abc","events":[{"event":"ws_connected","details":{"padding":"` +
		strings.Repeat("a", int(clientDiagnosticsBodyLimitBytes)+1024) + `"}}]}`
	response := serveAuthenticatedJSON(t, newDiagnosticsRouter(api), http.MethodPost, "/internal/client-diagnostics", oversized)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s, want 413", response.Code, response.Body.String())
	}
	if len(recorder.batches) != 0 {
		t.Fatalf("recorded batches = %d, want none", len(recorder.batches))
	}
}

func TestClientDiagnosticsRouteIsRegistered(t *testing.T) {
	recorder := &recordingDiagnosticsRecorder{}
	handler := NewRouter(RouterDependencies{
		ClientDiagnosticsAPI: NewClientDiagnosticsAPI(recorder, fakeWorkspaceAccess{role: "owner"}, fakeDiagnosticsSessionLookup{workspaceID: "w_test"}),
		AuthService:          fakeSessionAuthenticator{},
	})

	response := serveAuthenticatedJSON(t, handler, http.MethodPost, "/internal/client-diagnostics", diagnosticsBody)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s, want 202", response.Code, response.Body.String())
	}
}
