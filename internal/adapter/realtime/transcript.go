package realtime

import (
	"crypto/sha256"
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"deciscope-core-api/internal/domain"
)

const transcriptSegmentCreatedType = "transcript_segment.created"

var defaultTranscriptAllowedOrigins = []string{
	"http://localhost:3000",
	"http://localhost:5173",
	"http://localhost:5193",
	"http://127.0.0.1:3000",
	"http://127.0.0.1:5173",
	"http://127.0.0.1:5193",
}

type TranscriptWebSocketConfig struct {
	ClientToken    string
	AllowedOrigins string
}

type TranscriptHub struct {
	mu      sync.RWMutex
	clients map[*transcriptClient]struct{}
	now     func() time.Time
}

func NewTranscriptHub() *TranscriptHub {
	return &TranscriptHub{
		clients: make(map[*transcriptClient]struct{}),
		now:     time.Now,
	}
}

func (h *TranscriptHub) PublishTranscriptSegment(segment domain.TranscriptSegment) {
	h.mu.RLock()
	clients := make([]*transcriptClient, 0, len(h.clients))
	for c := range h.clients {
		if c.callID == "" || c.callID == segment.CallID {
			clients = append(clients, c)
		}
	}
	h.mu.RUnlock()

	log.Printf("Transcript segment broadcast. eventId=%s callId=%s sequenceNo=%d clients=%d", segment.EventID, segment.CallID, segment.SequenceNo, len(clients))
	for _, c := range clients {
		c.enqueue(segment)
	}
}

func (h *TranscriptHub) ServeTranscriptSegments(config TranscriptWebSocketConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !config.authorized(r.URL.Query().Get("token")) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
			return
		}
		if !config.originAllowed(r.Header.Get("Origin")) {
			writeError(w, http.StatusForbidden, "forbidden_origin", "origin is not allowed")
			return
		}

		callID := strings.TrimSpace(r.URL.Query().Get("callId"))
		conn, reader, err := accept(w, r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "websocket_upgrade_failed", err.Error())
			return
		}
		defer conn.Close()

		c := newTranscriptClient(callID, conn, reader)
		h.subscribe(c)
		defer h.unsubscribe(c)

		go c.readLoop(h)
		c.writeLoop(h)
	}
}

func (h *TranscriptHub) subscribe(c *transcriptClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	count := len(h.clients)
	h.mu.Unlock()
	log.Printf("Transcript websocket connected. callId=%s clients=%d", c.callID, count)
}

func (h *TranscriptHub) unsubscribe(c *transcriptClient) {
	c.closeOnce.Do(func() {
		h.mu.Lock()
		delete(h.clients, c)
		count := len(h.clients)
		h.mu.Unlock()
		close(c.done)
		log.Printf("Transcript websocket disconnected. callId=%s clients=%d", c.callID, count)
	})
}

func (config TranscriptWebSocketConfig) authorized(token string) bool {
	secret := strings.TrimSpace(config.ClientToken)
	if secret == "" {
		return true
	}
	if strings.TrimSpace(token) == "" {
		return false
	}
	got := sha256.Sum256([]byte(token))
	want := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

func (config TranscriptWebSocketConfig) originAllowed(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	for _, allowed := range transcriptAllowedOrigins(config.AllowedOrigins) {
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	return false
}

func transcriptAllowedOrigins(value string) []string {
	var origins []string
	for _, origin := range strings.Split(value, ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return defaultTranscriptAllowedOrigins
	}
	return origins
}

type transcriptClient struct {
	callID    string
	conn      netConn
	reader    frameReader
	send      chan domain.TranscriptSegment
	done      chan struct{}
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func newTranscriptClient(callID string, conn netConn, reader frameReader) *transcriptClient {
	return &transcriptClient{
		callID: callID,
		conn:   conn,
		reader: reader,
		send:   make(chan domain.TranscriptSegment, 128),
		done:   make(chan struct{}),
	}
}

func (c *transcriptClient) enqueue(segment domain.TranscriptSegment) {
	select {
	case c.send <- segment:
	default:
		select {
		case <-c.send:
		default:
		}
		c.send <- segment
	}
}

func (c *transcriptClient) writeLoop(h *TranscriptHub) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case segment := <-c.send:
			if err := c.writeSegment(h, segment); err != nil {
				log.Printf("Transcript websocket write failed. eventId=%s callId=%s sequenceNo=%d error=%v", segment.EventID, segment.CallID, segment.SequenceNo, err)
				h.unsubscribe(c)
				_ = c.conn.Close()
				return
			}
		case <-ticker.C:
			c.writeMu.Lock()
			err := writePing(c.conn)
			c.writeMu.Unlock()
			if err != nil {
				log.Printf("Transcript websocket ping failed. subscribedCallId=%s error=%v", c.callID, err)
				h.unsubscribe(c)
				_ = c.conn.Close()
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *transcriptClient) readLoop(h *TranscriptHub) {
	defer h.unsubscribe(c)
	defer c.conn.Close()
	for {
		opcode, payload, err := readFrame(c.reader)
		if err != nil {
			return
		}
		switch opcode {
		case opClose:
			c.writeMu.Lock()
			_ = writeFrame(c.conn, opClose, nil)
			c.writeMu.Unlock()
			return
		case opPing:
			c.writeMu.Lock()
			_ = writePong(c.conn, payload)
			c.writeMu.Unlock()
		}
	}
}

func (c *transcriptClient) writeSegment(h *TranscriptHub, segment domain.TranscriptSegment) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeJSON(c.conn, transcriptSegmentProtocolMessage(segment, h.now()))
}

type transcriptSegmentMessage struct {
	Type      string                `json:"type"`
	SentAtUTC string                `json:"sentAtUtc"`
	Data      transcriptSegmentData `json:"data"`
}

type transcriptSegmentData struct {
	EventID         string `json:"eventId"`
	CallID          string `json:"callId"`
	SequenceNo      int64  `json:"sequenceNo"`
	RecognizedAtUTC string `json:"recognizedAtUtc"`
	OffsetTicks     int64  `json:"offsetTicks"`
	DurationTicks   int64  `json:"durationTicks"`
	Text            string `json:"text"`
	Duplicate       bool   `json:"duplicate"`
}

func transcriptSegmentProtocolMessage(segment domain.TranscriptSegment, sentAt time.Time) transcriptSegmentMessage {
	return transcriptSegmentMessage{
		Type:      transcriptSegmentCreatedType,
		SentAtUTC: sentAt.UTC().Format(time.RFC3339Nano),
		Data: transcriptSegmentData{
			EventID:         segment.EventID,
			CallID:          segment.CallID,
			SequenceNo:      segment.SequenceNo,
			RecognizedAtUTC: segment.RecognizedAtUTC.UTC().Format(time.RFC3339Nano),
			OffsetTicks:     segment.OffsetTicks,
			DurationTicks:   segment.DurationTicks,
			Text:            segment.Text,
			Duplicate:       false,
		},
	}
}
