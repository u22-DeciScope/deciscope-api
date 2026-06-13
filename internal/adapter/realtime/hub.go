package realtime

import (
	"sync"

	"deciscope-core-api/internal/domain"
)

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*client]struct{}
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]map[*client]struct{})}
}

func (h *Hub) Publish(event domain.Event) {
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
