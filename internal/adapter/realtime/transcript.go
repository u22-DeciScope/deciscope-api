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

const (
	transcriptSegmentCreatedType    = "transcript_segment.created"
	meetingSessionStatusChangedType = "meeting_session.status_changed"
)

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
	totalSubscriberCount := len(h.clients)
	for c := range h.clients {
		if c.matchesSegment(segment) {
			clients = append(clients, c)
		}
	}
	h.mu.RUnlock()

	log.Printf("Transcript broadcasted. sessionId=%s eventId=%s callId=%s sequenceNo=%d isFinal=%t speakerId=%s speakerName=%s textLength=%d subscriberCount=%d totalSubscriberCount=%d",
		segment.SessionID, segment.EventID, segment.CallID, segment.SequenceNo, transcriptSegmentIsFinal(segment), segment.SpeakerID, segment.SpeakerName, len([]rune(strings.TrimSpace(segment.Text))), len(clients), totalSubscriberCount)
	for _, c := range clients {
		c.enqueueSegment(segment)
	}
}

func (h *TranscriptHub) PublishMeetingSessionStatusChanged(session domain.MeetingSession) {
	h.mu.RLock()
	clients := make([]*transcriptClient, 0, len(h.clients))
	totalSubscriberCount := len(h.clients)
	for c := range h.clients {
		if c.matchesSession(session) {
			clients = append(clients, c)
		}
	}
	h.mu.RUnlock()

	log.Printf("Meeting session status broadcast. sessionId=%s status=%s botCallId=%s subscriberCount=%d totalSubscriberCount=%d", session.ID, session.Status, session.BotCallID, len(clients), totalSubscriberCount)
	for _, c := range clients {
		c.enqueueSession(session)
	}
}

func (h *TranscriptHub) ServeTranscriptSegments(config TranscriptWebSocketConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		callID := strings.TrimSpace(r.URL.Query().Get("callId"))
		sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
		log.Printf("Transcript websocket request received. path=%s callId=%s sessionId=%s origin=%s remoteAddr=%s", path, callID, sessionID, origin, r.RemoteAddr)

		if !config.authorized(r.URL.Query().Get("token")) {
			log.Printf("Transcript websocket request rejected. path=%s callId=%s sessionId=%s origin=%s reason=unauthorized", path, callID, sessionID, origin)
			writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
			return
		}
		if !config.originAllowed(origin) {
			log.Printf("Transcript websocket request rejected. path=%s callId=%s sessionId=%s origin=%s reason=forbidden_origin", path, callID, sessionID, origin)
			writeError(w, http.StatusForbidden, "forbidden_origin", "origin is not allowed")
			return
		}

		conn, reader, err := accept(w, r)
		if err != nil {
			log.Printf("Transcript websocket upgrade failed. path=%s callId=%s sessionId=%s origin=%s error=%v", path, callID, sessionID, origin, err)
			writeError(w, http.StatusBadRequest, "websocket_upgrade_failed", err.Error())
			return
		}
		defer conn.Close()
		log.Printf("Transcript websocket upgrade accepted. path=%s callId=%s sessionId=%s origin=%s", path, callID, sessionID, origin)

		c := newTranscriptClient(callID, sessionID, conn, reader)
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
	log.Printf("Transcript websocket subscriber added. callId=%s sessionId=%s subscriberCount=%d", c.callID, c.sessionID, count)
}

func (h *TranscriptHub) unsubscribe(c *transcriptClient) {
	c.closeOnce.Do(func() {
		h.mu.Lock()
		delete(h.clients, c)
		count := len(h.clients)
		h.mu.Unlock()
		close(c.done)
		log.Printf("Transcript websocket subscriber removed. callId=%s sessionId=%s subscriberCount=%d", c.callID, c.sessionID, count)
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
	sessionID string
	conn      netConn
	reader    frameReader
	send      chan transcriptOutboundEvent
	done      chan struct{}
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func newTranscriptClient(callID, sessionID string, conn netConn, reader frameReader) *transcriptClient {
	return &transcriptClient{
		callID:    callID,
		sessionID: sessionID,
		conn:      conn,
		reader:    reader,
		send:      make(chan transcriptOutboundEvent, 128),
		done:      make(chan struct{}),
	}
}

func (c *transcriptClient) enqueueSegment(segment domain.TranscriptSegment) {
	c.enqueue(transcriptOutboundEvent{segment: &segment})
}

func (c *transcriptClient) enqueueSession(session domain.MeetingSession) {
	c.enqueue(transcriptOutboundEvent{session: &session})
}

func (c *transcriptClient) enqueue(event transcriptOutboundEvent) {
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

func (c *transcriptClient) writeLoop(h *TranscriptHub) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case event := <-c.send:
			if err := c.writeEvent(h, event); err != nil {
				log.Printf("Transcript websocket write failed. callId=%s sessionId=%s error=%v", c.callID, c.sessionID, err)
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

func (c *transcriptClient) matchesSegment(segment domain.TranscriptSegment) bool {
	if c.callID != "" && c.callID != segment.CallID {
		return false
	}
	if c.sessionID != "" && c.sessionID != segment.SessionID {
		return false
	}
	return true
}

func (c *transcriptClient) matchesSession(session domain.MeetingSession) bool {
	if c.sessionID != "" {
		return c.sessionID == session.ID
	}
	if c.callID != "" {
		return c.callID == session.BotCallID
	}
	return true
}

func (c *transcriptClient) writeEvent(h *TranscriptHub, event transcriptOutboundEvent) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	switch {
	case event.segment != nil:
		return writeJSON(c.conn, transcriptSegmentProtocolMessage(*event.segment, h.now()))
	case event.session != nil:
		return writeJSON(c.conn, meetingSessionStatusProtocolMessage(*event.session, h.now()))
	default:
		return nil
	}
}

type transcriptOutboundEvent struct {
	segment *domain.TranscriptSegment
	session *domain.MeetingSession
}

type transcriptSegmentMessage struct {
	Type      string                `json:"type"`
	SentAtUTC string                `json:"sentAtUtc"`
	Data      transcriptSegmentData `json:"data"`
}

type transcriptSegmentData struct {
	SessionID       string `json:"sessionId,omitempty"`
	EventID         string `json:"eventId"`
	CallID          string `json:"callId"`
	SequenceNo      int64  `json:"sequenceNo"`
	SpeakerID       string `json:"speakerId,omitempty"`
	SpeakerName     string `json:"speakerName,omitempty"`
	RecognizedAtUTC string `json:"recognizedAtUtc"`
	OffsetTicks     int64  `json:"offsetTicks"`
	DurationTicks   int64  `json:"durationTicks"`
	Text            string `json:"text"`
	Duplicate       bool   `json:"duplicate"`
	IsFinal         bool   `json:"isFinal"`
}

func transcriptSegmentProtocolMessage(segment domain.TranscriptSegment, sentAt time.Time) transcriptSegmentMessage {
	return transcriptSegmentMessage{
		Type:      transcriptSegmentCreatedType,
		SentAtUTC: sentAt.UTC().Format(time.RFC3339Nano),
		Data: transcriptSegmentData{
			SessionID:       segment.SessionID,
			EventID:         segment.EventID,
			CallID:          segment.CallID,
			SequenceNo:      segment.SequenceNo,
			SpeakerID:       segment.SpeakerID,
			SpeakerName:     segment.SpeakerName,
			RecognizedAtUTC: segment.RecognizedAtUTC.UTC().Format(time.RFC3339Nano),
			OffsetTicks:     segment.OffsetTicks,
			DurationTicks:   segment.DurationTicks,
			Text:            segment.Text,
			Duplicate:       false,
			IsFinal:         transcriptSegmentIsFinal(segment),
		},
	}
}

func transcriptSegmentIsFinal(segment domain.TranscriptSegment) bool {
	return segment.IsFinal || segment.SequenceNo > 0
}

type meetingSessionStatusMessage struct {
	Type      string                   `json:"type"`
	SentAtUTC string                   `json:"sentAtUtc"`
	Data      meetingSessionStatusData `json:"data"`
}

type meetingSessionStatusData struct {
	SessionID                   string `json:"sessionId"`
	Title                       string `json:"title,omitempty"`
	DisplayTitle                string `json:"displayTitle,omitempty"`
	TitleSource                 string `json:"titleSource,omitempty"`
	UserProvidedTitle           string `json:"userProvidedTitle,omitempty"`
	GraphTitle                  string `json:"graphTitle,omitempty"`
	Provider                    string `json:"provider,omitempty"`
	ExternalMeetingID           string `json:"externalMeetingId,omitempty"`
	JoinMeetingID               string `json:"joinMeetingId,omitempty"`
	JoinWebURL                  string `json:"joinWebUrl,omitempty"`
	CanonicalJoinWebURL         string `json:"canonicalJoinWebUrl,omitempty"`
	ThreadID                    string `json:"threadId,omitempty"`
	OrganizerID                 string `json:"organizerId,omitempty"`
	OrganizerName               string `json:"organizerName,omitempty"`
	OrganizerEmail              string `json:"organizerEmail,omitempty"`
	TitleResolutionErrorCode    string `json:"titleResolutionErrorCode,omitempty"`
	TitleResolutionErrorMessage string `json:"titleResolutionErrorMessage,omitempty"`
	Status                      string `json:"status"`
	BotCallID                   string `json:"botCallId,omitempty"`
	EndedAt                     string `json:"endedAt,omitempty"`
	EndReason                   string `json:"endReason,omitempty"`
	LastError                   string `json:"lastError,omitempty"`
}

func meetingSessionStatusProtocolMessage(session domain.MeetingSession, sentAt time.Time) meetingSessionStatusMessage {
	return meetingSessionStatusMessage{
		Type:      meetingSessionStatusChangedType,
		SentAtUTC: sentAt.UTC().Format(time.RFC3339Nano),
		Data: meetingSessionStatusData{
			SessionID:                   session.ID,
			Title:                       session.Title,
			DisplayTitle:                session.Title,
			TitleSource:                 session.TitleSource,
			UserProvidedTitle:           session.UserProvidedTitle,
			GraphTitle:                  session.GraphTitle,
			Provider:                    session.Provider,
			ExternalMeetingID:           session.ExternalMeetingID,
			JoinMeetingID:               session.JoinMeetingID,
			JoinWebURL:                  session.JoinWebURL,
			CanonicalJoinWebURL:         session.CanonicalJoinWebURL,
			ThreadID:                    session.ThreadID,
			OrganizerID:                 session.OrganizerID,
			OrganizerName:               session.OrganizerName,
			OrganizerEmail:              session.OrganizerEmail,
			TitleResolutionErrorCode:    session.TitleResolutionErrorCode,
			TitleResolutionErrorMessage: session.TitleResolutionErrorMessage,
			Status:                      string(session.Status),
			BotCallID:                   session.BotCallID,
			EndedAt:                     optionalProtocolTime(session.EndedAt),
			EndReason:                   session.EndReason,
			LastError:                   session.LastError,
		},
	}
}

func optionalProtocolTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
