package handlers

import (
	"deciscope-core-api/internal/app/middleware"
	"encoding/json"
	"net/http"
)

func MeHandler(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(middleware.UIDContextKey).(string)

	json.NewEncoder(w).Encode(map[string]any{
		"uid":    uid,
		"status": "ok",
	})
}
