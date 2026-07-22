package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

// AgendaProgressOverridesStore is an in-memory
// application.MeetingAgendaProgressOverridesRepository implementation, used
// by tests that do not need a real Postgres database.
type AgendaProgressOverridesStore struct {
	mu   sync.Mutex
	byID map[string]storedAgendaProgressOverrides
}

type storedAgendaProgressOverrides struct {
	payload   json.RawMessage
	updatedAt time.Time
}

func NewAgendaProgressOverridesStore() *AgendaProgressOverridesStore {
	return &AgendaProgressOverridesStore{byID: make(map[string]storedAgendaProgressOverrides)}
}

func (s *AgendaProgressOverridesStore) GetAgendaProgressOverrides(_ context.Context, sessionID string) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.byID[sessionID]
	if !ok {
		return nil, fmt.Errorf("%w: agenda progress overrides not found", domain.ErrNotFound)
	}
	return append(json.RawMessage(nil), stored.payload...), nil
}

func (s *AgendaProgressOverridesStore) UpsertAgendaProgressOverrides(_ context.Context, sessionID string, payload json.RawMessage, updatedAt time.Time) error {
	if !json.Valid(payload) {
		return fmt.Errorf("agenda progress overrides payload is not valid json")
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[sessionID] = storedAgendaProgressOverrides{payload: append(json.RawMessage(nil), payload...), updatedAt: updatedAt.UTC()}
	return nil
}

// DeleteAgendaProgressOverrides removes the session's override row, mirroring
// the postgres MeetingSessionRepository.DeleteMeetingSession transaction.
func (s *AgendaProgressOverridesStore) DeleteAgendaProgressOverrides(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, sessionID)
	return nil
}

var _ application.MeetingAgendaProgressOverridesRepository = (*AgendaProgressOverridesStore)(nil)
