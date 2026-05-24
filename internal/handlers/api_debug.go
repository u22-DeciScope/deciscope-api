package handlers

import (
	_ "embed"
	"net/http"
)

//go:embed api_debug.html
var apiDebugHTML string

func APIDebug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(apiDebugHTML))
}
