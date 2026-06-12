package realtime

import (
	"context"
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
			http.Error(w, "missing meeting_id", http.StatusBadRequest)
			return
		}
		meeting, err := store.GetMeeting(r.Context(), meetingID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, domain.ErrNotFound) {
				status = http.StatusNotFound
			}
			http.Error(w, "meeting not found", status)
			return
		}
		identity, ok := resolveIdentity(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		conn, reader, err := accept(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
