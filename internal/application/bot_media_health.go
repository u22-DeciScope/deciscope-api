package application

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"deciscope-core-api/internal/domain"
)

const (
	BotMediaHealthOK                  = "ok"
	BotMediaHealthAudioReceiveStalled = "audio_receive_stalled"
	BotMediaHealthEventStarted        = "started"
	BotMediaHealthEventRecovered      = "recovered"
)

type BotMediaHealthUpdate struct {
	EventID              string
	BotCallID            string
	State                string
	Event                string
	Source               string
	OccurredAt           time.Time
	StartedAt            time.Time
	LastAudioFrameAt     time.Time
	DurationMilliseconds int64
}

type BotMediaHealthState struct {
	SessionID            string    `json:"sessionId"`
	EventID              string    `json:"eventId,omitempty"`
	BotCallID            string    `json:"botCallId,omitempty"`
	State                string    `json:"state"`
	Event                string    `json:"event"`
	Source               string    `json:"source,omitempty"`
	OccurredAt           time.Time `json:"occurredAtUtc"`
	StartedAt            time.Time `json:"startedAtUtc,omitempty,omitzero"`
	LastAudioFrameAt     time.Time `json:"lastAudioFrameAtUtc,omitempty,omitzero"`
	DurationMilliseconds int64     `json:"durationMs,omitempty"`
	UpdatedAt            time.Time `json:"updatedAtUtc"`
}

// BotMediaHealthService owns transient, per-session transport health. It is
// deliberately in-memory: unlike meeting status this state must not trigger
// finalization or survive an API restart.
type BotMediaHealthService struct {
	mu        sync.RWMutex
	states    map[string]BotMediaHealthState
	publisher MeetingSessionMediaHealthPublisher
	now       func() time.Time
}

func NewBotMediaHealthService(publisher MeetingSessionMediaHealthPublisher) *BotMediaHealthService {
	return &BotMediaHealthService{
		states: make(map[string]BotMediaHealthState), publisher: publisher, now: time.Now,
	}
}

func (s *BotMediaHealthService) Record(
	session domain.MeetingSession,
	update BotMediaHealthUpdate,
) (BotMediaHealthState, bool, error) {
	if strings.TrimSpace(session.ID) == "" {
		return BotMediaHealthState{}, false, fmt.Errorf("%w: session id is required", domain.ErrInvalidArgument)
	}
	update.State = strings.ToLower(strings.TrimSpace(update.State))
	update.Event = strings.ToLower(strings.TrimSpace(update.Event))
	if (update.State != BotMediaHealthOK && update.State != BotMediaHealthAudioReceiveStalled) ||
		(update.Event != BotMediaHealthEventStarted && update.Event != BotMediaHealthEventRecovered) ||
		(update.Event == BotMediaHealthEventStarted && update.State != BotMediaHealthAudioReceiveStalled) ||
		(update.Event == BotMediaHealthEventRecovered && update.State != BotMediaHealthOK) {
		return BotMediaHealthState{}, false, fmt.Errorf("%w: invalid media health transition", domain.ErrInvalidArgument)
	}
	now := s.now().UTC()
	if update.OccurredAt.IsZero() {
		update.OccurredAt = now
	} else {
		update.OccurredAt = update.OccurredAt.UTC()
	}

	s.mu.Lock()
	previous, exists := s.states[session.ID]
	if exists && ((update.EventID != "" && update.EventID == previous.EventID) ||
		update.OccurredAt.Before(previous.OccurredAt)) {
		s.mu.Unlock()
		return previous, false, nil
	}
	if update.Event == BotMediaHealthEventRecovered {
		if update.StartedAt.IsZero() {
			update.StartedAt = previous.StartedAt
		}
		if update.DurationMilliseconds <= 0 && !update.StartedAt.IsZero() {
			update.DurationMilliseconds = update.OccurredAt.Sub(update.StartedAt).Milliseconds()
		}
	}
	if update.Event == BotMediaHealthEventStarted && update.StartedAt.IsZero() {
		update.StartedAt = update.OccurredAt
	}
	state := BotMediaHealthState{
		SessionID: session.ID, EventID: strings.TrimSpace(update.EventID),
		BotCallID: strings.TrimSpace(update.BotCallID), State: update.State, Event: update.Event,
		Source: strings.TrimSpace(update.Source), OccurredAt: update.OccurredAt,
		StartedAt: update.StartedAt.UTC(), LastAudioFrameAt: update.LastAudioFrameAt.UTC(),
		DurationMilliseconds: update.DurationMilliseconds, UpdatedAt: now,
	}
	changed := !exists || previous.State != state.State || previous.Event != state.Event ||
		(previous.EventID != "" && previous.EventID != state.EventID)
	s.states[session.ID] = state
	s.mu.Unlock()

	if changed && s.publisher != nil {
		s.publisher.PublishMeetingSessionMediaHealth(session, state)
	}
	return state, changed, nil
}

func (s *BotMediaHealthService) Get(sessionID string) BotMediaHealthState {
	s.mu.RLock()
	state, ok := s.states[strings.TrimSpace(sessionID)]
	s.mu.RUnlock()
	if ok {
		return state
	}
	now := s.now().UTC()
	return BotMediaHealthState{
		SessionID: strings.TrimSpace(sessionID), State: BotMediaHealthOK,
		Event: "snapshot", OccurredAt: now, UpdatedAt: now,
	}
}

func (s *BotMediaHealthService) Forget(sessionID string) {
	s.mu.Lock()
	delete(s.states, strings.TrimSpace(sessionID))
	s.mu.Unlock()
}

func (s *BotMediaHealthService) SetNow(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}
