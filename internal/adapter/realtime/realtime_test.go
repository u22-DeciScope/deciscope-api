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
	client := newClient("m_1", server, bufio.NewReader(server), 2)
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

type fakeEventStore struct {
	events []domain.Event
}

func (f fakeEventStore) ListEvents(context.Context, string, int64) ([]domain.Event, error) {
	return f.events, nil
}

func (fakeEventStore) GetMeeting(context.Context, string) (*domain.Meeting, error) {
	return &domain.Meeting{}, nil
}
