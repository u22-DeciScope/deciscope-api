package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
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
	meetings, err := api.service.Store().ListMeetings(r.Context())
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
	meeting, err := api.service.Store().GetMeeting(r.Context(), chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meeting)
}

func (api *CoreAPI) CreateJoinToken(w http.ResponseWriter, r *http.Request) {
	meetingID := chi.URLParam(r, "meeting_id")
	if _, err := api.service.Store().GetMeeting(r.Context(), meetingID); err != nil {
		writeStoreError(w, err)
		return
	}
	expiresAt := time.Now().UTC().Add(2 * time.Hour)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      fmt.Sprintf("local.%s.%d", meetingID, expiresAt.Unix()),
		"token_type": "local-dev",
		"expires_at": expiresAt.Format(time.RFC3339),
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
	events, err := api.service.Store().ListEvents(r.Context(), chi.URLParam(r, "meeting_id"), afterSeq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_events_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (api *CoreAPI) ListSegments(w http.ResponseWriter, r *http.Request) {
	afterSeq := parseSeq(r.URL.Query().Get("after_seq"))
	segments, err := api.service.Store().ListSegments(r.Context(), chi.URLParam(r, "meeting_id"), afterSeq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_segments_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"segments": segments})
}

func (api *CoreAPI) GetReport(w http.ResponseWriter, r *http.Request) {
	meetingID := chi.URLParam(r, "meeting_id")
	report, err := api.service.Store().LatestReport(r.Context(), meetingID)
	if errors.Is(err, core.ErrNotFound) {
		content, buildErr := api.service.BuildMarkdownReport(r.Context(), meetingID)
		if buildErr != nil {
			writeStoreError(w, buildErr)
			return
		}
		report, err = api.service.Store().SaveReport(r.Context(), meetingID, content)
	}
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

	if err := os.MkdirAll(api.uploadDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "upload_dir_failed", err.Error())
		return
	}
	filename := sanitizeFilename(header.Filename)
	job, err := api.service.Store().CreateJob(r.Context(), "file.extract_audio", "", "completed")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_job_failed", err.Error())
		return
	}

	dstPath := filepath.Join(api.uploadDir, job.ID+"_"+filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save_upload_failed", err.Error())
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		writeError(w, http.StatusInternalServerError, "write_upload_failed", err.Error())
		return
	}

	mediaType := header.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = mime.TypeByExtension(filepath.Ext(filename))
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	upload, err := api.service.Store().SaveUpload(r.Context(), filename, mediaType, dstPath, job.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "record_upload_failed", err.Error())
		return
	}
	_ = api.service.Store().CompleteJob(r.Context(), job.ID, map[string]any{
		"upload_id": upload.ID,
		"mode":      "mock-local",
	})
	job, _ = api.service.Store().GetJob(r.Context(), job.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"upload": upload,
		"job":    job,
	})
}

func (api *CoreAPI) GetJob(w http.ResponseWriter, r *http.Request) {
	job, err := api.service.Store().GetJob(r.Context(), chi.URLParam(r, "job_id"))
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
