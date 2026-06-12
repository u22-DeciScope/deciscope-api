package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

func TestCoreAPIHTTPContractWithFakeUseCases(t *testing.T) {
	useCases := &fakeCoreUseCases{}
	api := NewCoreAPI(useCases, nil)
	router := chi.NewRouter()
	router.Get("/meetings", api.ListMeetings)
	router.Post("/meetings", api.CreateMeeting)
	router.Get("/meetings/{meeting_id}", api.GetMeeting)
	router.Get("/meetings/{meeting_id}/events", api.ListEvents)
	router.Get("/meetings/{meeting_id}/segments", api.ListSegments)
	router.Get("/meetings/{meeting_id}/report", api.GetReport)
	router.Post("/uploads", api.Upload)

	assertJSONEmptyArray(t, serveJSON(t, router, http.MethodGet, "/meetings", nil), "meetings")
	assertJSONEmptyArray(t, serveJSON(t, router, http.MethodGet, "/meetings/m_http/events", nil), "events")
	assertJSONEmptyArray(t, serveJSON(t, router, http.MethodGet, "/meetings/m_http/segments", nil), "segments")

	create := serveJSON(t, router, http.MethodPost, "/meetings", map[string]any{"title": "HTTP contract"})
	if create.Code != http.StatusCreated || !bytes.Contains(create.Body.Bytes(), []byte(`"id":"m_http"`)) {
		t.Fatalf("create response = %d %s", create.Code, create.Body.String())
	}

	get := serveJSON(t, router, http.MethodGet, "/meetings/m_http", nil)
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte(`"title":"HTTP contract"`)) {
		t.Fatalf("get response = %d %s", get.Code, get.Body.String())
	}

	reportReq := httptest.NewRequest(http.MethodGet, "/meetings/m_http/report", nil)
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

	var uploadBody bytes.Buffer
	writer := multipart.NewWriter(&uploadBody)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="notes.txt"`)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("hello"))
	_ = writer.Close()
	uploadReq := httptest.NewRequest(http.MethodPost, "/uploads", &uploadBody)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	upload := httptest.NewRecorder()
	router.ServeHTTP(upload, uploadReq)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload response = %d %s", upload.Code, upload.Body.String())
	}
	if useCases.uploadMediaType != mime.TypeByExtension(".txt") {
		t.Fatalf("upload media type = %q, want extension-derived type", useCases.uploadMediaType)
	}
}

type fakeCoreUseCases struct {
	meeting         *domain.Meeting
	uploadMediaType string
}

func (f *fakeCoreUseCases) ListMeetings(context.Context) ([]domain.Meeting, error) {
	if f.meeting == nil {
		return nil, nil
	}
	return []domain.Meeting{*f.meeting}, nil
}

func (f *fakeCoreUseCases) CreateMeeting(_ context.Context, title, source string) (*domain.Meeting, error) {
	f.meeting = &domain.Meeting{ID: "m_http", Title: title, Status: "created", Source: source}
	return f.meeting, nil
}

func (f *fakeCoreUseCases) GetMeeting(_ context.Context, meetingID string) (*domain.Meeting, error) {
	if f.meeting == nil || f.meeting.ID != meetingID {
		return nil, domain.ErrNotFound
	}
	return f.meeting, nil
}

func (*fakeCoreUseCases) CreateJoinToken(context.Context, string) (*application.JoinToken, error) {
	return &application.JoinToken{}, nil
}

func (*fakeCoreUseCases) EndMeeting(context.Context, string) (*domain.Report, []domain.Event, error) {
	return &domain.Report{}, nil, nil
}

func (*fakeCoreUseCases) ListEvents(context.Context, string, int64) ([]domain.Event, error) {
	return nil, nil
}

func (*fakeCoreUseCases) ListSegments(context.Context, string, int64) ([]domain.Segment, error) {
	return nil, nil
}

func (f *fakeCoreUseCases) GetOrCreateReport(_ context.Context, meetingID string) (*domain.Report, error) {
	if f.meeting == nil || f.meeting.ID != meetingID {
		return nil, domain.ErrNotFound
	}
	return &domain.Report{MeetingID: meetingID, Format: "markdown", Content: "# " + f.meeting.Title}, nil
}

func (f *fakeCoreUseCases) UploadFile(_ context.Context, filename, mediaType string, _ io.Reader) (*application.UploadResult, error) {
	f.uploadMediaType = mediaType
	return &application.UploadResult{
		Upload: &domain.Upload{ID: "upl_http", Filename: filename, MediaType: mediaType, JobID: "job_http"},
		Job:    &domain.Job{ID: "job_http", Status: "completed"},
	}, nil
}

func (*fakeCoreUseCases) GetJob(context.Context, string) (*domain.Job, error) {
	return &domain.Job{}, nil
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

func assertJSONEmptyArray(t *testing.T, resp *httptest.ResponseRecorder, field string) {
	t.Helper()
	if resp.Code != http.StatusOK {
		t.Fatalf("%s response = %d %s", field, resp.Code, resp.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s response: %v", field, err)
	}
	if string(body[field]) != "[]" {
		t.Fatalf("%s = %s, want []", field, body[field])
	}
}
