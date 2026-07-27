package httpadapter

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (api *CoreAPI) ListMeetings(w http.ResponseWriter, r *http.Request) {
	meetings, err := api.service.ListMeetings(r.Context(), chi.URLParam(r, "workspace_code"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_meetings_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"meetings": meetingDTOs(meetings)})
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
	meeting, err := api.service.CreateMeeting(r.Context(), chi.URLParam(r, "workspace_code"), req.Title, req.Source)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_meeting_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, meetingDTO(*meeting))
}

func (api *CoreAPI) GetMeeting(w http.ResponseWriter, r *http.Request) {
	meeting, err := api.service.GetMeeting(r.Context(), chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meetingDTO(*meeting))
}

func (api *CoreAPI) CreateJoinToken(w http.ResponseWriter, r *http.Request) {
	token, err := api.service.CreateJoinToken(r.Context(), chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token.Token, "token_type": token.TokenType, "expires_at": token.ExpiresAt,
	})
}

func (api *CoreAPI) EndMeeting(w http.ResponseWriter, r *http.Request) {
	events, err := api.service.EndMeeting(r.Context(), chi.URLParam(r, "meeting_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": eventDTOs(events)})
}

func (api *CoreAPI) ListEvents(w http.ResponseWriter, r *http.Request) {
	events, err := api.service.ListEvents(r.Context(), chi.URLParam(r, "meeting_id"), parseSeq(r.URL.Query().Get("after_seq")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_events_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": eventDTOs(events)})
}

func (api *CoreAPI) ListSegments(w http.ResponseWriter, r *http.Request) {
	segments, err := api.service.ListSegments(r.Context(), chi.URLParam(r, "meeting_id"), parseSeq(r.URL.Query().Get("after_seq")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_segments_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"segments": segmentDTOs(segments)})
}
