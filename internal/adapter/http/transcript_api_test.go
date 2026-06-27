package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

const testTranscriptAPIKey = "0123456789abcdef0123456789abcdef"

func TestTranscriptAPIStoresJapaneseSegment(t *testing.T) {
	service := &fakeTranscriptIngestUseCases{status: domain.TranscriptSegmentCreated}
	api := NewTranscriptAPI(service, testTranscriptAPIKey)

	resp := serveTranscriptSegment(api, testTranscriptAPIKey, "application/json; charset=utf-8", validTranscriptSegmentJSON(t, nil))

	if resp.Code != http.StatusCreated {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if !service.called {
		t.Fatal("StoreTranscriptSegment was not called")
	}
	if service.segment.Text != "本日の会議を開始します。" {
		t.Fatalf("stored text = %q", service.segment.Text)
	}
	if service.segment.RecognizedAtUTC.Format("2006-01-02T15:04:05.999999999Z07:00") != "2026-06-25T13:20:01.1234567Z" {
		t.Fatalf("recognizedAtUTC = %s", service.segment.RecognizedAtUTC)
	}
	var body transcriptSegmentResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "created" || body.Duplicate || body.EventID != "06008080-91e3-4b88-a8ff-9af629265ced:1" {
		t.Fatalf("response body = %+v", body)
	}
}

func TestTranscriptAPIDuplicateResponse(t *testing.T) {
	service := &fakeTranscriptIngestUseCases{status: domain.TranscriptSegmentAlreadyExists}
	api := NewTranscriptAPI(service, testTranscriptAPIKey)

	resp := serveTranscriptSegment(api, testTranscriptAPIKey, "application/json", validTranscriptSegmentJSON(t, nil))

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	var body transcriptSegmentResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "already_exists" || !body.Duplicate {
		t.Fatalf("response body = %+v", body)
	}
}

func TestTranscriptAPIAcceptsOptionalSessionID(t *testing.T) {
	service := &fakeTranscriptIngestUseCases{status: domain.TranscriptSegmentCreated}
	api := NewTranscriptAPI(service, testTranscriptAPIKey)

	resp := serveTranscriptSegment(api, testTranscriptAPIKey, "application/json", validTranscriptSegmentJSON(t, func(payload map[string]any) {
		payload["sessionId"] = "session_1"
	}))

	if resp.Code != http.StatusCreated {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.segment.SessionID != "session_1" {
		t.Fatalf("sessionID = %q, want session_1", service.segment.SessionID)
	}
}

func TestTranscriptAPIConflictResponse(t *testing.T) {
	service := &fakeTranscriptIngestUseCases{err: domain.ErrConflict}
	api := NewTranscriptAPI(service, testTranscriptAPIKey)

	resp := serveTranscriptSegment(api, testTranscriptAPIKey, "application/json", validTranscriptSegmentJSON(t, nil))

	if resp.Code != http.StatusConflict {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestTranscriptAPIRequiresAPIKey(t *testing.T) {
	api := NewTranscriptAPI(&fakeTranscriptIngestUseCases{}, testTranscriptAPIKey)

	missing := serveTranscriptSegment(api, "", "application/json", validTranscriptSegmentJSON(t, nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing key response = %d %s", missing.Code, missing.Body.String())
	}
	wrong := serveTranscriptSegment(api, "wrong-key", "application/json", validTranscriptSegmentJSON(t, nil))
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key response = %d %s", wrong.Code, wrong.Body.String())
	}
}

func TestTranscriptAPIListsSegmentsWithOptionalClientToken(t *testing.T) {
	service := &fakeTranscriptIngestUseCases{
		segments: []domain.TranscriptSegment{{
			EventID:         "call-1:1",
			SessionID:       "session_1",
			CallID:          "call-1",
			SequenceNo:      1,
			RecognizedAtUTC: mustTime(t, "2026-06-27T00:00:00Z"),
			OffsetTicks:     10,
			DurationTicks:   20,
			Text:            "履歴です。",
			ReceivedAtUTC:   mustTime(t, "2026-06-27T00:00:01Z"),
		}},
	}
	api := NewTranscriptAPI(service, testTranscriptAPIKey, "client-token")

	unauthorized := httptest.NewRecorder()
	api.List(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/transcript-segments?callId=call-1", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized response = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	resp := httptest.NewRecorder()
	api.List(resp, httptest.NewRequest(http.MethodGet, "/api/v1/transcript-segments?callId=call-1&sessionId=session_1&limit=5&token=client-token", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.listCallID != "call-1" || service.listSessionID != "session_1" || service.listLimit != 5 {
		t.Fatalf("list args = callID:%q sessionID:%q limit:%d", service.listCallID, service.listSessionID, service.listLimit)
	}
	var body transcriptSegmentListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].SessionID != "session_1" || body.Items[0].EventID != "call-1:1" || body.Items[0].Text != "履歴です。" {
		t.Fatalf("body = %+v", body)
	}
}

func TestTranscriptAPIListAllowsDevelopmentModeWithoutClientToken(t *testing.T) {
	service := &fakeTranscriptIngestUseCases{}
	api := NewTranscriptAPI(service, testTranscriptAPIKey)

	resp := httptest.NewRecorder()
	api.List(resp, httptest.NewRequest(http.MethodGet, "/api/v1/transcript-segments", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.listLimit != 100 {
		t.Fatalf("default limit = %d, want 100", service.listLimit)
	}
}

func TestTranscriptAPIListRejectsInvalidLimit(t *testing.T) {
	api := NewTranscriptAPI(&fakeTranscriptIngestUseCases{}, testTranscriptAPIKey)

	resp := httptest.NewRecorder()
	api.List(resp, httptest.NewRequest(http.MethodGet, "/api/v1/transcript-segments?limit=0", nil))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestTranscriptAPIValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        int
	}{
		{name: "invalid json", contentType: "application/json", body: `{"eventId":`, want: http.StatusBadRequest},
		{name: "blank text", contentType: "application/json", body: validTranscriptSegmentJSON(t, func(payload map[string]any) { payload["text"] = " \n\t " }), want: http.StatusBadRequest},
		{name: "sequence zero", contentType: "application/json", body: validTranscriptSegmentJSON(t, func(payload map[string]any) { payload["sequenceNo"] = 0 }), want: http.StatusBadRequest},
		{name: "negative offset", contentType: "application/json", body: validTranscriptSegmentJSON(t, func(payload map[string]any) { payload["offsetTicks"] = -1 }), want: http.StatusBadRequest},
		{name: "invalid datetime", contentType: "application/json", body: validTranscriptSegmentJSON(t, func(payload map[string]any) { payload["recognizedAtUtc"] = "not-a-time" }), want: http.StatusBadRequest},
		{name: "non utc datetime", contentType: "application/json", body: validTranscriptSegmentJSON(t, func(payload map[string]any) { payload["recognizedAtUtc"] = "2026-06-25T22:20:01+09:00" }), want: http.StatusBadRequest},
		{name: "unsupported content type", contentType: "text/plain", body: validTranscriptSegmentJSON(t, nil), want: http.StatusUnsupportedMediaType},
		{name: "body too large", contentType: "application/json", body: validTranscriptSegmentJSON(t, func(payload map[string]any) {
			payload["text"] = strings.Repeat("a", int(transcriptSegmentBodyLimitBytes))
		}), want: http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeTranscriptIngestUseCases{status: domain.TranscriptSegmentCreated}
			api := NewTranscriptAPI(service, testTranscriptAPIKey)

			resp := serveTranscriptSegment(api, testTranscriptAPIKey, tt.contentType, tt.body)

			if resp.Code != tt.want {
				t.Fatalf("response = %d %s, want %d", resp.Code, resp.Body.String(), tt.want)
			}
			if tt.want != http.StatusRequestEntityTooLarge && service.called {
				t.Fatalf("service called for invalid request")
			}
		})
	}
}

func TestHealthzIsProcessOnly(t *testing.T) {
	failing := NewHealthAPI(func(context.Context) error { return errors.New("database unavailable") })
	resp := httptest.NewRecorder()
	failing.Healthz(resp, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"status":"ok"`) {
		t.Fatalf("healthz response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestReadyzChecksDependency(t *testing.T) {
	ok := NewHealthAPI(func(context.Context) error { return nil })
	okResp := httptest.NewRecorder()
	ok.Readyz(okResp, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if okResp.Code != http.StatusOK || !strings.Contains(okResp.Body.String(), `"status":"ok"`) {
		t.Fatalf("ok response = %d %s", okResp.Code, okResp.Body.String())
	}

	failing := NewHealthAPI(func(context.Context) error { return errors.New("database unavailable") })
	failResp := httptest.NewRecorder()
	failing.Readyz(failResp, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if failResp.Code != http.StatusServiceUnavailable {
		t.Fatalf("failure response = %d %s", failResp.Code, failResp.Body.String())
	}
}

type fakeTranscriptIngestUseCases struct {
	status        domain.TranscriptSegmentStoreStatus
	err           error
	called        bool
	segment       domain.TranscriptSegment
	segments      []domain.TranscriptSegment
	listCallID    string
	listSessionID string
	listLimit     int
}

func (f *fakeTranscriptIngestUseCases) StoreTranscriptSegment(_ context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	f.called = true
	f.segment = segment
	if f.err != nil {
		return domain.TranscriptSegmentStoreResult{}, f.err
	}
	return domain.TranscriptSegmentStoreResult{Status: f.status, EventID: segment.EventID}, nil
}

func (f *fakeTranscriptIngestUseCases) ListTranscriptSegments(_ context.Context, callID, sessionID string, limit int) ([]domain.TranscriptSegment, error) {
	f.listCallID = callID
	f.listSessionID = sessionID
	f.listLimit = limit
	return f.segments, f.err
}

func serveTranscriptSegment(api *TranscriptAPI, apiKey, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcript-segments", strings.NewReader(body))
	if apiKey != "" {
		req.Header.Set("X-DeciScope-Api-Key", apiKey)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp := httptest.NewRecorder()
	api.Store(resp, req)
	return resp
}

func validTranscriptSegmentJSON(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	payload := map[string]any{
		"eventId":         "06008080-91e3-4b88-a8ff-9af629265ced:1",
		"callId":          "06008080-91e3-4b88-a8ff-9af629265ced",
		"sequenceNo":      1,
		"recognizedAtUtc": "2026-06-25T13:20:01.1234567+00:00",
		"offsetTicks":     20300000,
		"durationTicks":   18000000,
		"text":            "本日の会議を開始します。",
	}
	if mutate != nil {
		mutate(payload)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(data)
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return parsed
}
