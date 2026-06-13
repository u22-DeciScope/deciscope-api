package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestV1MeetingFixtureReplayFlow(t *testing.T) {
	fixtureDir := t.TempDir()
	fixture := `{"wait_ms":0,"type":"transcript.final","payload":{"segment_id":"seg_001","speaker_label":"Speaker A","text":"hello","start_ms":0,"end_ms":1000}}
{"wait_ms":1,"type":"analysis.delta","payload":{"items":[{"op":"add","item":{"id":"an_001","kind":"question","severity":"medium","title":"Check scope","body":"Scope is unclear.","linked_segment_ids":["seg_001"],"status":"open"}}]}}
`
	if err := os.WriteFile(filepath.Join(fixtureDir, "test.jsonl"), []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("FIXTURE_DIR", fixtureDir)
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "test.sqlite"))

	handler, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	meetingResp := postJSON(t, handler, "/v1/meetings", map[string]any{"title": "Replay flow"})
	if meetingResp.Code != http.StatusCreated {
		t.Fatalf("create meeting status = %d, body = %s", meetingResp.Code, meetingResp.Body.String())
	}
	var meeting struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(meetingResp.Body.Bytes(), &meeting); err != nil {
		t.Fatalf("decode meeting: %v", err)
	}
	if meeting.ID == "" {
		t.Fatal("meeting id is empty")
	}

	startResp := postJSON(t, handler, "/v1/meetings/"+meeting.ID+"/replay/start", map[string]any{"fixture": "test.jsonl"})
	if startResp.Code != http.StatusAccepted {
		t.Fatalf("start replay status = %d, body = %s", startResp.Code, startResp.Body.String())
	}

	var eventsResp *httptest.ResponseRecorder
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/v1/meetings/"+meeting.ID+"/events?after_seq=0", nil)
		eventsResp = httptest.NewRecorder()
		handler.ServeHTTP(eventsResp, req)
		if bytes.Contains(eventsResp.Body.Bytes(), []byte("report.ready")) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if eventsResp == nil || !bytes.Contains(eventsResp.Body.Bytes(), []byte("report.ready")) {
		t.Fatalf("events did not contain report.ready, body = %s", eventsResp.Body.String())
	}

	reportReq := httptest.NewRequest(http.MethodGet, "/v1/meetings/"+meeting.ID+"/report", nil)
	reportResp := httptest.NewRecorder()
	handler.ServeHTTP(reportResp, reportReq)
	if reportResp.Code != http.StatusOK {
		t.Fatalf("report status = %d, body = %s", reportResp.Code, reportResp.Body.String())
	}
	if !bytes.Contains(reportResp.Body.Bytes(), []byte("Replay flow")) {
		t.Fatalf("report body does not include meeting title: %s", reportResp.Body.String())
	}
}

func postJSON(t *testing.T, handler http.Handler, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}
