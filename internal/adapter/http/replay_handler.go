package httpadapter

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (api *CoreAPI) ListFixtures(w http.ResponseWriter, r *http.Request) {
	fixtures, err := api.replay.ListFixtures()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_fixtures_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fixture_dir": api.replay.FixtureDir(), "fixtures": fixtureDTOs(fixtures),
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
	writeJSON(w, http.StatusAccepted, replayStatusDTO(*status))
}

func (api *CoreAPI) ReplayPause(w http.ResponseWriter, r *http.Request) {
	status, err := api.replay.Pause(chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, replayStatusDTO(*status))
}

func (api *CoreAPI) ReplayResume(w http.ResponseWriter, r *http.Request) {
	status, err := api.replay.Resume(chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, replayStatusDTO(*status))
}

func (api *CoreAPI) ReplayReset(w http.ResponseWriter, r *http.Request) {
	if err := api.replay.Reset(r.Context(), chi.URLParam(r, "meeting_id")); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "reset"})
}
