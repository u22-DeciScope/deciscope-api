package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"deciscope-core-api/internal/core"
	"deciscope-core-api/internal/fixture"

	"github.com/go-chi/chi/v5"
)

func TestCoreAPIHTTPContract(t *testing.T) {
	service := core.NewService(core.RepositoriesFromMemory(core.NewMemoryStore()), nil)
	api := NewCoreAPI(service, fixture.NewManager(service, t.TempDir()), t.TempDir())
	router := chi.NewRouter()
	router.Post("/meetings", api.CreateMeeting)
	router.Get("/meetings/{meeting_id}", api.GetMeeting)
	router.Get("/meetings/{meeting_id}/report", api.GetReport)

	create := serveJSON(t, router, http.MethodPost, "/meetings", map[string]any{"title": "HTTP contract"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}
	var meeting core.Meeting
	if err := json.Unmarshal(create.Body.Bytes(), &meeting); err != nil {
		t.Fatalf("decode meeting: %v", err)
	}

	get := serveJSON(t, router, http.MethodGet, "/meetings/"+meeting.ID, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", get.Code, get.Body.String())
	}

	reportReq := httptest.NewRequest(http.MethodGet, "/meetings/"+meeting.ID+"/report", nil)
	reportReq.Header.Set("Accept", "text/markdown")
	report := httptest.NewRecorder()
	router.ServeHTTP(report, reportReq)
	if report.Code != http.StatusOK || report.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Fatalf("report response = %d %q", report.Code, report.Header().Get("Content-Type"))
	}
	if !bytes.Contains(report.Body.Bytes(), []byte("# HTTP contract")) {
		t.Fatalf("report body = %s", report.Body.String())
	}

	missing := serveJSON(t, router, http.MethodGet, "/meetings/missing", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, body = %s", missing.Code, missing.Body.String())
	}
}

func serveJSON(t *testing.T, handler http.Handler, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatalf("encode payload: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}
