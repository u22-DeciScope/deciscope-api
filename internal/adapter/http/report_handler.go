package httpadapter

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

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
	writeJSON(w, http.StatusOK, reportDTO(*report))
}
