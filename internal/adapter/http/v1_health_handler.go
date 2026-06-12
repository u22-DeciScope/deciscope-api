package httpadapter

import (
	"net/http"
	"time"
)

func (api *CoreAPI) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "time": time.Now().UTC().Format(time.RFC3339),
	})
}
