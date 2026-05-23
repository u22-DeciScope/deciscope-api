package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"deciscope-core-api/internal/core"
)

type EventStore interface {
	ListEvents(ctx context.Context, meetingID string, afterSeq int64) ([]core.Event, error)
	GetMeeting(ctx context.Context, meetingID string) (*core.Meeting, error)
}

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*client]struct{}
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]map[*client]struct{})}
}

func (h *Hub) Publish(event core.Event) {
	h.mu.RLock()
	room := h.rooms[event.MeetingID]
	clients := make([]*client, 0, len(room))
	for c := range room {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, c := range clients {
		c.enqueue(event)
	}
}

func (h *Hub) ServeWS(store EventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		meetingID := r.URL.Query().Get("meeting_id")
		if meetingID == "" {
			http.Error(w, "missing meeting_id", http.StatusBadRequest)
			return
		}
		if _, err := store.GetMeeting(r.Context(), meetingID); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, core.ErrNotFound) {
				status = http.StatusNotFound
			}
			http.Error(w, "meeting not found", status)
			return
		}

		conn, reader, err := accept(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer conn.Close()

		lastSeq := parseInt64(r.URL.Query().Get("last_seq"))
		helloSeq, ok := readHello(conn, reader, meetingID)
		if ok {
			lastSeq = helloSeq
		}

		c := &client{
			meetingID: meetingID,
			conn:      conn,
			reader:    reader,
			send:      make(chan core.Event, 128),
			done:      make(chan struct{}),
			lastSeq:   lastSeq,
		}
		h.subscribe(c)
		defer h.unsubscribe(c)

		missed, err := store.ListEvents(r.Context(), meetingID, lastSeq)
		if err != nil {
			_ = writeJSON(conn, map[string]any{
				"type": "error",
				"payload": map[string]any{
					"code":      "catchup_failed",
					"message":   "failed to load missed events",
					"retryable": true,
				},
			})
			return
		}
		for _, event := range missed {
			if err := c.writeEvent(event); err != nil {
				return
			}
		}

		go c.readLoop()
		c.writeLoop()
	}
}

func (h *Hub) subscribe(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.rooms[c.meetingID]; !ok {
		h.rooms[c.meetingID] = make(map[*client]struct{})
	}
	h.rooms[c.meetingID][c] = struct{}{}
}

func (h *Hub) unsubscribe(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room, ok := h.rooms[c.meetingID]; ok {
		delete(room, c)
		if len(room) == 0 {
			delete(h.rooms, c.meetingID)
		}
	}
	close(c.done)
}

type client struct {
	meetingID string
	conn      netConn
	reader    frameReader
	send      chan core.Event
	done      chan struct{}
	writeMu   sync.Mutex
	lastSeq   int64
}

func (c *client) enqueue(event core.Event) {
	select {
	case c.send <- event:
	default:
		select {
		case <-c.send:
		default:
		}
		c.send <- event
	}
}

func (c *client) writeLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case event := <-c.send:
			if err := c.writeEvent(event); err != nil {
				return
			}
		case <-ticker.C:
			c.writeMu.Lock()
			err := writePing(c.conn)
			c.writeMu.Unlock()
			if err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *client) readLoop() {
	for {
		opcode, payload, err := readFrame(c.reader)
		if err != nil {
			return
		}
		switch opcode {
		case opClose:
			return
		case opPing:
			c.writeMu.Lock()
			_ = writePong(c.conn, payload)
			c.writeMu.Unlock()
		}
	}
}

func (c *client) writeEvent(event core.Event) error {
	if event.Seq > 0 && event.Seq <= c.lastSeq {
		return nil
	}
	if event.Seq > c.lastSeq {
		c.lastSeq = event.Seq
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeJSON(c.conn, event)
}

type clientHello struct {
	Type      string `json:"type"`
	MeetingID string `json:"meeting_id"`
	LastSeq   int64  `json:"last_seq"`
}

func readHello(conn netConn, reader frameReader, meetingID string) (int64, bool) {
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	opcode, payload, err := readFrame(reader)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil || opcode != opText {
		return 0, false
	}
	var hello clientHello
	if err := json.Unmarshal(payload, &hello); err != nil {
		return 0, false
	}
	if hello.Type != "client.hello" || hello.MeetingID != meetingID {
		return 0, false
	}
	return hello.LastSeq, true
}

func parseInt64(value string) int64 {
	if value == "" {
		return 0
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
