package realtime

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
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

func TestTranscriptWebSocketConfigChecksTokenAndOrigin(t *testing.T) {
	config := TranscriptWebSocketConfig{
		ClientToken:    "client-token",
		AllowedOrigins: "http://localhost:3000,http://127.0.0.1:3000",
	}
	if !config.authorized("client-token") {
		t.Fatal("authorized token rejected")
	}
	if config.authorized("wrong-token") {
		t.Fatal("wrong token accepted")
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
		message.Data.SpeakerID != "speaker-1" || message.Data.SpeakerName != "佐藤さん" || message.Data.Duplicate {
		t.Fatalf("message data = %+v", message.Data)
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

type fakeEventStore struct {
	events []domain.Event
}

func (f fakeEventStore) ListEvents(context.Context, string, int64) ([]domain.Event, error) {
	return f.events, nil
}

func (fakeEventStore) GetMeeting(context.Context, string) (*domain.Meeting, error) {
	return &domain.Meeting{}, nil
}
