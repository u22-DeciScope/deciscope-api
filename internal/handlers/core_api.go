package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"deciscope-core-api/internal/core"
	"deciscope-core-api/internal/fixture"

	"github.com/go-chi/chi/v5"
)

type CoreAPI struct {
	service   *core.Service
	replay    *fixture.Manager
	uploadDir string
}

func NewCoreAPI(service *core.Service, replay *fixture.Manager, uploadDir string) *CoreAPI {
	if uploadDir == "" {
		uploadDir = "./uploads"
	}
	return &CoreAPI{service: service, replay: replay, uploadDir: uploadDir}
}

func (api *CoreAPI) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (api *CoreAPI) ListMeetings(w http.ResponseWriter, r *http.Request) {
	meetings, err := api.service.ListMeetings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_meetings_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"meetings": meetings})
}

func (api *CoreAPI) CreateMeeting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title  string `json:"title"`
		Source string `json:"source"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	meeting, err := api.service.CreateMeeting(r.Context(), req.Title, req.Source)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_meeting_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, meeting)
}

func (api *CoreAPI) GetMeeting(w http.ResponseWriter, r *http.Request) {
	meeting, err := api.service.GetMeeting(r.Context(), chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meeting)
}

func (api *CoreAPI) CreateJoinToken(w http.ResponseWriter, r *http.Request) {
	token, err := api.service.CreateJoinToken(r.Context(), chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token.Token,
		"token_type": token.TokenType,
		"expires_at": token.ExpiresAt,
	})
}

func (api *CoreAPI) EndMeeting(w http.ResponseWriter, r *http.Request) {
	report, events, err := api.service.EndMeeting(r.Context(), chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"report": report,
		"events": events,
	})
}

func (api *CoreAPI) ListEvents(w http.ResponseWriter, r *http.Request) {
	afterSeq := parseSeq(r.URL.Query().Get("after_seq"))
	events, err := api.service.ListEvents(r.Context(), chi.URLParam(r, "meeting_id"), afterSeq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_events_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (api *CoreAPI) ListSegments(w http.ResponseWriter, r *http.Request) {
	afterSeq := parseSeq(r.URL.Query().Get("after_seq"))
	segments, err := api.service.ListSegments(r.Context(), chi.URLParam(r, "meeting_id"), afterSeq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_segments_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"segments": segments})
}

func (api *CoreAPI) GetReport(w http.ResponseWriter, r *http.Request) {
	report, err := api.service.GetOrCreateReport(r.Context(), chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(report.Content))
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (api *CoreAPI) ListFixtures(w http.ResponseWriter, r *http.Request) {
	fixtures, err := api.replay.ListFixtures()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_fixtures_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fixture_dir": api.replay.FixtureDir(),
		"fixtures":    fixtures,
	})
}

func (api *CoreAPI) ReplayStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Fixture string `json:"fixture"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
	}
	status, err := api.replay.Start(r.Context(), chi.URLParam(r, "meeting_id"), req.Fixture)
	if err != nil {
		writeError(w, http.StatusBadRequest, "replay_start_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

func (api *CoreAPI) ReplayPause(w http.ResponseWriter, r *http.Request) {
	status, err := api.replay.Pause(chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (api *CoreAPI) ReplayResume(w http.ResponseWriter, r *http.Request) {
	status, err := api.replay.Resume(chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (api *CoreAPI) ReplayReset(w http.ResponseWriter, r *http.Request) {
	if err := api.replay.Reset(r.Context(), chi.URLParam(r, "meeting_id")); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "reset"})
}

func (api *CoreAPI) Upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_multipart", err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_file", "multipart field `file` is required")
		return
	}
	defer file.Close()

	filename := sanitizeFilename(header.Filename)
	mediaType := header.Header.Get("Content-Type")
	result, err := api.service.UploadFile(r.Context(), api.uploadDir, filename, mediaType, file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "upload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"upload": result.Upload,
		"job":    result.Job,
	})
}

func (api *CoreAPI) GetJob(w http.ResponseWriter, r *http.Request) {
	job, err := api.service.GetJob(r.Context(), chi.URLParam(r, "job_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, core.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func parseSeq(value string) int64 {
	if value == "" {
		return 0
	}
	seq, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seq < 0 {
		return 0
	}
	return seq
}

func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "upload.bin"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(name)
}
