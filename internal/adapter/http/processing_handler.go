package httpadapter

import (
	"mime"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"
)

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
	if mediaType == "" {
		mediaType = mime.TypeByExtension(filepath.Ext(filename))
	}
	result, err := api.service.UploadFile(r.Context(), filename, mediaType, file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "upload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"upload": uploadDTO(*result.Upload), "job": jobDTO(*result.Job),
	})
}

func (api *CoreAPI) GetJob(w http.ResponseWriter, r *http.Request) {
	job, err := api.service.GetJob(r.Context(), chi.URLParam(r, "job_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDTO(*job))
}
