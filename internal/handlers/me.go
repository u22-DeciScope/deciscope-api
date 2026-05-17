package handlers

import (
	"encoding/json"
	"net/http"
)

func MeHandler(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value("uid").(string)

	json.NewEncoder(w).Encode(map[string]any{
		"uid":    uid,
		"status": "ok",
	})
}
