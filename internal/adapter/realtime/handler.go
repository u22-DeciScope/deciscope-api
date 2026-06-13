package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"deciscope-core-api/internal/domain"
)

type EventStore interface {
	ListEvents(ctx context.Context, meetingID string, afterSeq int64) ([]domain.Event, error)
	GetMeeting(ctx context.Context, meetingID string) (*domain.Meeting, error)
}

type ClientIdentity struct {
	UserID    string
	SessionID string
}

type IdentityResolver func(r *http.Request) (ClientIdentity, bool)

func (h *Hub) ServeWS(store EventStore, resolveIdentity IdentityResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		meetingID := r.URL.Query().Get("meeting_id")
		if meetingID == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "missing meeting_id")
			return
		}
		meeting, err := store.GetMeeting(r.Context(), meetingID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, domain.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, "meeting_not_found", "meeting not found")
			return
		}
		identity, ok := resolveIdentity(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
			return
		}

		conn, reader, err := accept(w, r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "websocket_upgrade_failed", err.Error())
			return
		}
		defer conn.Close()

		lastSeq := parseSeq(r.URL.Query().Get("last_seq"))
		if helloSeq, ok := readHello(conn, reader, meetingID); ok {
			lastSeq = helloSeq
		}

		c := newClient(meetingID, meeting.WorkspaceID, identity.UserID, identity.SessionID, conn, reader, lastSeq)
		h.subscribe(c)
		defer h.unsubscribe(c)

		if err := c.writeCatchUp(r.Context(), store); err != nil {
			_ = writeJSON(conn, catchUpErrorMessage())
			return
		}

		go c.readLoop()
		c.writeLoop()
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message}})
}

func (c *client) writeCatchUp(ctx context.Context, store EventStore) error {
	events, err := store.ListEvents(ctx, c.meetingID, c.lastSeq)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := c.writeEvent(event); err != nil {
			return err
		}
	}
	return nil
}
