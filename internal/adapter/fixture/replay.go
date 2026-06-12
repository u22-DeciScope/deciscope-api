package fixture

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

type Service interface {
	AppendAndPublish(ctx context.Context, meetingID, eventType string, payload any) (*domain.Event, error)
	EndMeeting(ctx context.Context, meetingID string) (*domain.Report, []domain.Event, error)
	ResetMeeting(ctx context.Context, meetingID string) error
}

type Manager struct {
	service Service
	loader  Loader
	mu      sync.Mutex
	runs    map[string]*runState
}

type runState struct {
	meetingID string
	fixture   string
	status    string
	cancel    context.CancelFunc
	paused    bool
	cond      *sync.Cond
	startedAt time.Time
}

type line struct {
	WaitMS  int             `json:"wait_ms"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func NewManager(service Service, loader Loader) *Manager {
	return &Manager{
		service: service, loader: loader, runs: make(map[string]*runState),
	}
}

func (m *Manager) FixtureDir() string {
	return m.loader.Dir()
}

func (m *Manager) ListFixtures() ([]application.FixtureInfo, error) {
	return m.loader.List()
}

func (m *Manager) Start(ctx context.Context, meetingID, fixtureName string) (*application.ReplayStatus, error) {
	fixtureName = domain.NormalizeFixtureName(fixtureName)
	file, err := m.loader.Open(fixtureName)
	if err != nil {
		return nil, fmt.Errorf("fixture not found: %s: %w", fixtureName, err)
	}
	_ = file.Close()

	m.mu.Lock()
	if existing, ok := m.runs[meetingID]; ok {
		existing.cancel()
		existing.cond.Broadcast()
		delete(m.runs, meetingID)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	state := &runState{
		meetingID: meetingID,
		fixture:   fixtureName,
		status:    "running",
		cancel:    cancel,
		startedAt: time.Now().UTC(),
	}
	state.cond = sync.NewCond(&m.mu)
	m.runs[meetingID] = state
	status := statusFromState(state)
	m.mu.Unlock()

	go m.run(runCtx, state)

	return status, nil
}

func (m *Manager) Pause(meetingID string) (*application.ReplayStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.runs[meetingID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	state.paused = true
	state.status = "paused"
	return statusFromState(state), nil
}

func (m *Manager) Resume(meetingID string) (*application.ReplayStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.runs[meetingID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	state.paused = false
	state.status = "running"
	state.cond.Broadcast()
	return statusFromState(state), nil
}

func (m *Manager) Reset(ctx context.Context, meetingID string) error {
	m.mu.Lock()
	if existing, ok := m.runs[meetingID]; ok {
		existing.cancel()
		existing.cond.Broadcast()
		delete(m.runs, meetingID)
	}
	m.mu.Unlock()

	if err := m.service.ResetMeeting(ctx, meetingID); err != nil {
		return err
	}
	_, err := m.service.AppendAndPublish(ctx, meetingID, domain.EventMeetingState, map[string]any{
		"status":       "created",
		"recording":    false,
		"analyzing":    false,
		"participants": []string{},
	})
	return err
}

func (m *Manager) run(ctx context.Context, state *runState) {
	defer func() {
		m.mu.Lock()
		if current, ok := m.runs[state.meetingID]; ok && current == state {
			state.status = "completed"
			delete(m.runs, state.meetingID)
		}
		m.mu.Unlock()
	}()

	_, _ = m.service.AppendAndPublish(ctx, state.meetingID, domain.EventMeetingState, map[string]any{
		"status":       "started",
		"recording":    true,
		"analyzing":    true,
		"participants": []string{"Speaker A", "Speaker B", "Speaker C"},
	})

	file, err := m.loader.Open(state.fixture)
	if err != nil {
		m.publishError(ctx, state.meetingID, "fixture_open_failed", err.Error())
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		var item line
		if err := json.Unmarshal([]byte(text), &item); err != nil {
			m.publishError(ctx, state.meetingID, "fixture_parse_failed", err.Error())
			continue
		}
		if item.WaitMS > 0 {
			timer := time.NewTimer(time.Duration(item.WaitMS) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		m.waitIfPaused(ctx, state)
		if err := ctx.Err(); err != nil {
			return
		}
		if item.Type == "" {
			continue
		}
		payload := item.Payload
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		if _, err := m.service.AppendAndPublish(ctx, state.meetingID, item.Type, payload); err != nil {
			m.publishError(ctx, state.meetingID, "fixture_publish_failed", err.Error())
		}
	}
	if err := scanner.Err(); err != nil {
		m.publishError(ctx, state.meetingID, "fixture_read_failed", err.Error())
		return
	}
	_, _, _ = m.service.EndMeeting(ctx, state.meetingID)
}

func (m *Manager) waitIfPaused(ctx context.Context, state *runState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for state.paused && ctx.Err() == nil {
		state.cond.Wait()
	}
}

func (m *Manager) publishError(ctx context.Context, meetingID, code, message string) {
	_, _ = m.service.AppendAndPublish(ctx, meetingID, domain.EventError, map[string]any{
		"code":      code,
		"message":   message,
		"retryable": false,
	})
}

func statusFromState(state *runState) *application.ReplayStatus {
	status := &application.ReplayStatus{
		MeetingID: state.meetingID,
		Fixture:   state.fixture,
		Status:    state.status,
	}
	if !state.startedAt.IsZero() {
		status.StartedAt = state.startedAt.Format(time.RFC3339)
	}
	return status
}
