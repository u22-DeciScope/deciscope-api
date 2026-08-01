package application

import (
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

type fakeMediaHealthPublisher struct {
	events []BotMediaHealthState
}

func (f *fakeMediaHealthPublisher) PublishMeetingSessionMediaHealth(_ domain.MeetingSession, health BotMediaHealthState) {
	f.events = append(f.events, health)
}

func TestBotMediaHealthServicePublishesOneStartAndOneRecovery(t *testing.T) {
	publisher := &fakeMediaHealthPublisher{}
	service := NewBotMediaHealthService(publisher)
	now := time.Date(2026, 8, 1, 0, 50, 20, 0, time.UTC)
	service.SetNow(func() time.Time { return now })
	session := domain.MeetingSession{ID: "session-1", BotCallID: "call-1"}
	started := BotMediaHealthUpdate{
		EventID: "stall-1-start", BotCallID: "call-1", State: BotMediaHealthAudioReceiveStalled,
		Event: BotMediaHealthEventStarted, Source: "audio_frame_watchdog", OccurredAt: now,
		LastAudioFrameAt: now.Add(-5 * time.Second),
	}
	state, changed, err := service.Record(session, started)
	if err != nil || !changed || state.State != BotMediaHealthAudioReceiveStalled {
		t.Fatalf("start state=%+v changed=%t err=%v", state, changed, err)
	}
	if _, changed, err = service.Record(session, started); err != nil || changed {
		t.Fatalf("duplicate start changed=%t err=%v", changed, err)
	}
	now = now.Add(43 * time.Second)
	state, changed, err = service.Record(session, BotMediaHealthUpdate{
		EventID: "stall-1-recovered", BotCallID: "call-1", State: BotMediaHealthOK,
		Event: BotMediaHealthEventRecovered, Source: "audio_frame_watchdog", OccurredAt: now,
	})
	if err != nil || !changed || state.DurationMilliseconds != 43000 || len(publisher.events) != 2 {
		t.Fatalf("recovery state=%+v changed=%t err=%v events=%+v", state, changed, err, publisher.events)
	}
}

func TestBotMediaHealthServiceRejectsInvalidAndOutOfOrderTransitions(t *testing.T) {
	service := NewBotMediaHealthService(nil)
	now := time.Date(2026, 8, 1, 0, 50, 20, 0, time.UTC)
	service.SetNow(func() time.Time { return now })
	session := domain.MeetingSession{ID: "session-1"}
	if _, _, err := service.Record(session, BotMediaHealthUpdate{State: BotMediaHealthOK, Event: BotMediaHealthEventStarted}); err == nil {
		t.Fatal("invalid start transition was accepted")
	}
	newer, _, err := service.Record(session, BotMediaHealthUpdate{
		State: BotMediaHealthAudioReceiveStalled, Event: BotMediaHealthEventStarted, OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	older, changed, err := service.Record(session, BotMediaHealthUpdate{
		State: BotMediaHealthOK, Event: BotMediaHealthEventRecovered, OccurredAt: now.Add(-time.Second),
	})
	if err != nil || changed || older.State != newer.State {
		t.Fatalf("out-of-order state=%+v changed=%t err=%v", older, changed, err)
	}
}
