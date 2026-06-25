package httpadapter

import (
	"context"
	"log"
	"net/http"
	"time"
)

type HealthCheckFunc func(context.Context) error

type HealthAPI struct {
	check HealthCheckFunc
}

func NewHealthAPI(check HealthCheckFunc) *HealthAPI {
	return &HealthAPI{check: check}
}

func (api *HealthAPI) Healthz(w http.ResponseWriter, r *http.Request) {
	if api.check != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := api.check(ctx); err != nil {
			log.Printf("Health check failed: %v", err)
			writeError(w, http.StatusInternalServerError, "unhealthy", "health check failed")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
