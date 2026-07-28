package realtime

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

func TestHubPublishTargetsOnlyMeetingRoom(t *testing.T) {
	hub := NewHub()
	first := &client{meetingID: "m_1", send: make(chan domain.Event, 1), done: make(chan struct{})}
	second := &client{meetingID: "m_2", send: make(chan domain.Event, 1), done: make(chan struct{})}
	hub.subscribe(first)
	hub.subscribe(second)
	t.Cleanup(func() {
		hub.unsubscribe(first)
		hub.unsubscribe(second)
	})

	event := domain.Event{MeetingID: "m_1", Type: domain.EventMeetingState}
	hub.Publish(event)

	select {
	case got := <-first.send:
		if got.MeetingID != "m_1" {
			t.Fatalf("event = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first room did not receive event")
	}
	select {
	case got := <-second.send:
		t.Fatalf("second room unexpectedly received %+v", got)
	default:
	}
}

func TestClientWritesCatchUpUsingProtocolDTO(t *testing.T) {
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()

	store := fakeEventStore{events: []domain.Event{{
		MeetingID: "m_1", Type: domain.EventTranscriptFinal, Seq: 3, TsMS: 42,
		Payload: json.RawMessage(`{"text":"hello"}`),
	}}}
	client := newClient("m_1", "w_1", "u_1", "s_1", server, bufio.NewReader(server), 2)
	done := make(chan error, 1)
	go func() { done <- client.writeCatchUp(context.Background(), store) }()

	opcode, payload, err := readFrame(peer)
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	if opcode != opText {
		t.Fatalf("opcode = %d, want text", opcode)
	}
	var message eventMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode protocol message: %v", err)
	}
	if message.MeetingID != "m_1" || message.Seq != 3 || message.Type != domain.EventTranscriptFinal {
		t.Fatalf("message = %+v", message)
	}
	if err := <-done; err != nil {
		t.Fatalf("writeCatchUp() error = %v", err)
	}
}

func TestParseSeqRejectsInvalidValues(t *testing.T) {
	for input, want := range map[string]int64{"": 0, "-1": 0, "bad": 0, "12": 12} {
		if got := parseSeq(input); got != want {
			t.Fatalf("parseSeq(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestTranscriptHubPublishFiltersByCallID(t *testing.T) {
	hub := NewTranscriptHub()
	allCalls := &transcriptClient{send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	matching := &transcriptClient{callID: "call-1", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	other := &transcriptClient{callID: "call-2", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	hub.subscribe(allCalls)
	hub.subscribe(matching)
	hub.subscribe(other)
	t.Cleanup(func() {
		hub.unsubscribe(allCalls)
		hub.unsubscribe(matching)
		hub.unsubscribe(other)
	})

	segment := domain.TranscriptSegment{EventID: "call-1:1", CallID: "call-1", SequenceNo: 1}
	hub.PublishTranscriptSegment(segment)

	for name, client := range map[string]*transcriptClient{"all": allCalls, "matching": matching} {
		select {
		case got := <-client.send:
			if got.segment == nil || got.segment.EventID != "call-1:1" {
				t.Fatalf("%s got segment = %+v", name, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s did not receive segment", name)
		}
	}
	select {
	case got := <-other.send:
		t.Fatalf("other call unexpectedly received %+v", got)
	default:
	}
}

func TestTranscriptHubPublishFiltersBySessionID(t *testing.T) {
	hub := NewTranscriptHub()
	allSessions := &transcriptClient{send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	matching := &transcriptClient{sessionID: "session_1", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	other := &transcriptClient{sessionID: "session_2", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	hub.subscribe(allSessions)
	hub.subscribe(matching)
	hub.subscribe(other)
	t.Cleanup(func() {
		hub.unsubscribe(allSessions)
		hub.unsubscribe(matching)
		hub.unsubscribe(other)
	})

	segment := domain.TranscriptSegment{EventID: "session_1:1", SessionID: "session_1", CallID: "call-1", SequenceNo: 1}
	hub.PublishTranscriptSegment(segment)

	for name, client := range map[string]*transcriptClient{"all": allSessions, "matching": matching} {
		select {
		case got := <-client.send:
			if got.segment == nil || got.segment.EventID != "session_1:1" {
				t.Fatalf("%s got segment = %+v", name, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s did not receive segment", name)
		}
	}
	select {
	case got := <-other.send:
		t.Fatalf("other session unexpectedly received %+v", got)
	default:
	}
}

func TestTranscriptWebSocketConfigChecksOrigin(t *testing.T) {
	config := TranscriptWebSocketConfig{
		AllowedOrigins: "http://localhost:3000,http://127.0.0.1:3000",
	}
	if !config.originAllowed("http://localhost:3000") {
		t.Fatal("allowed origin rejected")
	}
	if config.originAllowed("http://example.com") {
		t.Fatal("unexpected origin accepted")
	}
	if !config.originAllowed("") {
		t.Fatal("empty origin should be allowed for non-browser clients")
	}
}

func TestTranscriptSegmentProtocolMessage(t *testing.T) {
	segment := domain.TranscriptSegment{
		EventID:         "call-1:1",
		SessionID:       "session_1",
		CallID:          "call-1",
		SequenceNo:      1,
		SpeakerID:       "speaker-1",
		SpeakerName:     "佐藤さん",
		RecognizedAtUTC: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
		OffsetTicks:     10,
		DurationTicks:   20,
		Text:            "hello",
	}
	message := transcriptSegmentProtocolMessage(segment, time.Date(2026, 6, 27, 0, 0, 1, 0, time.UTC))
	if message.Type != transcriptSegmentCreatedType || message.SentAtUTC != "2026-06-27T00:00:01Z" {
		t.Fatalf("message = %+v", message)
	}
	if message.Data.SessionID != "session_1" || message.Data.EventID != "call-1:1" ||
		message.Data.SpeakerID != "speaker-1" || message.Data.SpeakerName != "佐藤さん" || message.Data.Duplicate || !message.Data.IsFinal {
		t.Fatalf("message data = %+v", message.Data)
	}
}

func TestTranscriptSegmentProtocolMessageMarksPartial(t *testing.T) {
	segment := domain.TranscriptSegment{
		EventID:         "partial:session_1:call-1:speaker-1",
		SessionID:       "session_1",
		CallID:          "call-1",
		SequenceNo:      0,
		SpeakerID:       "speaker-1",
		RecognizedAtUTC: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
		Text:            "hel",
		IsFinal:         false,
	}
	message := transcriptSegmentProtocolMessage(segment, time.Date(2026, 6, 27, 0, 0, 1, 0, time.UTC))
	if message.Data.IsFinal {
		t.Fatalf("message data = %+v", message.Data)
	}
}

func TestTranscriptHubPublishMeetingAIAnalysisFiltersBySessionID(t *testing.T) {
	hub := NewTranscriptHub()
	allSessions := &transcriptClient{send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	matching := &transcriptClient{sessionID: "session_1", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	otherSession := &transcriptClient{sessionID: "session_2", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	callIDOnly := &transcriptClient{callID: "call-1", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	hub.subscribe(allSessions)
	hub.subscribe(matching)
	hub.subscribe(otherSession)
	hub.subscribe(callIDOnly)
	t.Cleanup(func() {
		hub.unsubscribe(allSessions)
		hub.unsubscribe(matching)
		hub.unsubscribe(otherSession)
		hub.unsubscribe(callIDOnly)
	})

	analysis := domain.MeetingAIAnalysis{
		SessionID: "session_1",
		Type:      domain.MeetingAIAnalysisLive,
		Status:    domain.MeetingAIAnalysisCompleted,
		Version:   4,
		Payload:   json.RawMessage(`{"summary":"進行中"}`),
	}
	hub.PublishMeetingAIAnalysis(analysis)

	for name, client := range map[string]*transcriptClient{"all": allSessions, "matching": matching} {
		select {
		case got := <-client.send:
			if got.aiAnalysis == nil || got.aiAnalysis.SessionID != "session_1" {
				t.Fatalf("%s got = %+v", name, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s did not receive ai analysis", name)
		}
	}
	for name, client := range map[string]*transcriptClient{"other session": otherSession, "callId only": callIDOnly} {
		select {
		case got := <-client.send:
			t.Fatalf("%s unexpectedly received %+v", name, got)
		default:
		}
	}
}

func TestTranscriptHubAIAnalysisWriteFailureIsolatedToSubscriber(t *testing.T) {
	hub := NewTranscriptHub()
	server, peer := net.Pipe()
	client := newTranscriptClient("", "session_1", server, bufio.NewReader(server))
	hub.subscribe(client)
	done := make(chan struct{})
	go func() {
		client.writeLoop(hub)
		close(done)
	}()
	// Simulate a normal WebSocket delivery failure after the durable analysis
	// has already been saved. Publishing only enqueues; the writer logs the
	// error, removes this subscriber, and cannot feed an error back into
	// finalization.
	_ = peer.Close()
	hub.PublishMeetingAIAnalysis(domain.MeetingAIAnalysis{
		SessionID: "session_1", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 12,
		Payload: json.RawMessage(`{"treeVersion":12}`),
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failed WebSocket writer did not terminate")
	}
	hub.mu.RLock()
	_, stillSubscribed := hub.clients[client]
	hub.mu.RUnlock()
	if stillSubscribed {
		t.Fatal("failed WebSocket subscriber remained registered")
	}
	_ = server.Close()
}

func TestMeetingAIAnalysisProtocolMessage(t *testing.T) {
	analysis := domain.MeetingAIAnalysis{
		SessionID:       "session_1",
		Type:            domain.MeetingAIAnalysisLive,
		Status:          domain.MeetingAIAnalysisCompleted,
		Version:         4,
		Payload:         json.RawMessage(`{"summary":"進行中です"}`),
		Model:           "gpt-4o-mini",
		UpdatedAt:       time.Date(2026, 6, 27, 0, 0, 3, 0, time.UTC),
		IntervalSeconds: 10,
	}
	message := meetingAIAnalysisProtocolMessage(analysis, time.Date(2026, 6, 27, 0, 0, 4, 0, time.UTC))
	if message.Type != meetingAIAnalysisUpdatedType || message.SentAtUTC != "2026-06-27T00:00:04Z" {
		t.Fatalf("message = %+v", message)
	}
	if message.Data.SessionID != "session_1" || message.Data.AnalysisType != "live" || message.Data.Status != "completed" ||
		message.Data.Version != 4 || message.Data.Model != "gpt-4o-mini" || message.Data.UpdatedAtUTC != "2026-06-27T00:00:03Z" {
		t.Fatalf("message data = %+v", message.Data)
	}
	if message.Data.IntervalSeconds != 10 {
		t.Fatalf("intervalSeconds = %d, want 10", message.Data.IntervalSeconds)
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"intervalSeconds":10`) {
		t.Fatalf("encoded = %s, want intervalSeconds field", string(encoded))
	}
	if !strings.Contains(string(message.Data.Payload), "進行中です") {
		t.Fatalf("payload = %s", string(message.Data.Payload))
	}
}

func TestMeetingAIAnalysisProtocolMessageNullPayloadOnFailure(t *testing.T) {
	analysis := domain.MeetingAIAnalysis{
		SessionID: "session_1",
		Type:      domain.MeetingAIAnalysisLive,
		Status:    domain.MeetingAIAnalysisFailed,
		LastError: "azure openai timeout",
	}
	message := meetingAIAnalysisProtocolMessage(analysis, time.Date(2026, 6, 27, 0, 0, 4, 0, time.UTC))
	if message.Data.Error != "azure openai timeout" {
		t.Fatalf("data.Error = %q", message.Data.Error)
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"payload":null`) {
		t.Fatalf("encoded = %s, want null payload", string(encoded))
	}
	if strings.Contains(string(encoded), "intervalSeconds") {
		t.Fatalf("encoded = %s, want intervalSeconds omitted when zero", string(encoded))
	}
}

func TestMeetingSessionStatusProtocolMessage(t *testing.T) {
	session := domain.MeetingSession{
		ID:        "session_1",
		Status:    domain.MeetingSessionJoined,
		BotCallID: "call-1",
	}
	message := meetingSessionStatusProtocolMessage(session, time.Date(2026, 6, 27, 0, 0, 2, 0, time.UTC))
	if message.Type != meetingSessionStatusChangedType || message.SentAtUTC != "2026-06-27T00:00:02Z" {
		t.Fatalf("message = %+v", message)
	}
	if message.Data.SessionID != "session_1" || message.Data.Status != "joined" || message.Data.BotCallID != "call-1" {
		t.Fatalf("message data = %+v", message.Data)
	}
}

func TestMeetingSessionBotHealthProtocolMessage(t *testing.T) {
	session := domain.MeetingSession{
		ID:              "session_1",
		Status:          domain.MeetingSessionRecording,
		LastBotStatusAt: time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC),
	}
	message := meetingSessionBotHealthProtocolMessage(session, false, time.Date(2026, 7, 7, 0, 1, 0, 0, time.UTC))
	if message.Type != meetingSessionBotHealthType {
		t.Fatalf("message.Type = %q", message.Type)
	}
	if meetingSessionBotHealthType != "meeting_session.bot_health_changed" {
		t.Fatalf("meetingSessionBotHealthType = %q, want meeting_session.bot_health_changed", meetingSessionBotHealthType)
	}
	if message.SentAtUTC != "2026-07-07T00:01:00Z" {
		t.Fatalf("message.SentAtUTC = %q", message.SentAtUTC)
	}
	if message.Data.SessionID != "session_1" || message.Data.Healthy {
		t.Fatalf("message data = %+v", message.Data)
	}
	if message.Data.LastBotStatusAtUTC != "2026-07-07T00:00:00Z" {
		t.Fatalf("message.Data.LastBotStatusAtUTC = %q", message.Data.LastBotStatusAtUTC)
	}

	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"type":"meeting_session.bot_health_changed"`) ||
		!strings.Contains(string(encoded), `"sessionId":"session_1"`) ||
		!strings.Contains(string(encoded), `"healthy":false`) {
		t.Fatalf("encoded = %s", string(encoded))
	}
}

func TestTranscriptHubPublishMeetingSessionBotHealthFiltersBySessionID(t *testing.T) {
	hub := NewTranscriptHub()
	matching := &transcriptClient{sessionID: "session_1", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	other := &transcriptClient{sessionID: "session_2", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	hub.subscribe(matching)
	hub.subscribe(other)
	t.Cleanup(func() {
		hub.unsubscribe(matching)
		hub.unsubscribe(other)
	})

	session := domain.MeetingSession{ID: "session_1", Status: domain.MeetingSessionRecording}
	hub.PublishMeetingSessionBotHealth(session, true)

	select {
	case got := <-matching.send:
		if got.botHealth == nil || got.botHealth.session.ID != "session_1" || !got.botHealth.healthy {
			t.Fatalf("matching got = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("matching client did not receive bot health event")
	}
	select {
	case got := <-other.send:
		t.Fatalf("other session unexpectedly received %+v", got)
	default:
	}
}

func TestMeetingSessionTranscriptHealthProtocolMessage(t *testing.T) {
	session := domain.MeetingSession{
		ID:     "session_1",
		Status: domain.MeetingSessionRecording,
	}
	message := meetingSessionTranscriptHealthProtocolMessage(session, "transcript_stalled", 90, time.Date(2026, 7, 7, 0, 1, 0, 0, time.UTC))
	if message.Type != meetingSessionTranscriptHealthType {
		t.Fatalf("message.Type = %q", message.Type)
	}
	if meetingSessionTranscriptHealthType != "meeting_session.transcript_health_changed" {
		t.Fatalf("meetingSessionTranscriptHealthType = %q, want meeting_session.transcript_health_changed", meetingSessionTranscriptHealthType)
	}
	if message.SentAtUTC != "2026-07-07T00:01:00Z" {
		t.Fatalf("message.SentAtUTC = %q", message.SentAtUTC)
	}
	if message.Data.SessionID != "session_1" || message.Data.TranscriptHealth != "transcript_stalled" || message.Data.SecondsSinceLastTranscript != 90 {
		t.Fatalf("message data = %+v", message.Data)
	}

	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"type":"meeting_session.transcript_health_changed"`) ||
		!strings.Contains(string(encoded), `"sessionId":"session_1"`) ||
		!strings.Contains(string(encoded), `"transcriptHealth":"transcript_stalled"`) ||
		!strings.Contains(string(encoded), `"secondsSinceLastTranscript":90`) {
		t.Fatalf("encoded = %s", string(encoded))
	}
}

func TestTranscriptHubPublishMeetingSessionTranscriptHealthFiltersBySessionID(t *testing.T) {
	hub := NewTranscriptHub()
	matching := &transcriptClient{sessionID: "session_1", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	other := &transcriptClient{sessionID: "session_2", send: make(chan transcriptOutboundEvent, 1), done: make(chan struct{})}
	hub.subscribe(matching)
	hub.subscribe(other)
	t.Cleanup(func() {
		hub.unsubscribe(matching)
		hub.unsubscribe(other)
	})

	session := domain.MeetingSession{ID: "session_1", Status: domain.MeetingSessionRecording}
	hub.PublishMeetingSessionTranscriptHealth(session, "transcript_delayed", 35)

	select {
	case got := <-matching.send:
		if got.transcriptHealth == nil || got.transcriptHealth.session.ID != "session_1" || got.transcriptHealth.transcriptHealth != "transcript_delayed" || got.transcriptHealth.seconds != 35 {
			t.Fatalf("matching got = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("matching client did not receive transcript health event")
	}
	select {
	case got := <-other.send:
		t.Fatalf("other session unexpectedly received %+v", got)
	default:
	}
}

type fakeEventStore struct {
	events []domain.Event
}

func (f fakeEventStore) ListEvents(context.Context, string, int64) ([]domain.Event, error) {
	return f.events, nil
}

func (fakeEventStore) GetMeeting(context.Context, string) (*domain.Meeting, error) {
	return &domain.Meeting{}, nil
}
