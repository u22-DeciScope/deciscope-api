package fixture

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"deciscope-core-api/internal/core"
)

type Service interface {
	AppendAndPublish(ctx context.Context, meetingID, eventType string, payload any) (*core.Event, error)
	EndMeeting(ctx context.Context, meetingID string) (*core.Report, []core.Event, error)
	Store() *core.Store
}

type Manager struct {
	service    Service
	fixtureDir string
	mu         sync.Mutex
	runs       map[string]*runState
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

type RunStatus struct {
	MeetingID string `json:"meeting_id"`
	Fixture   string `json:"fixture"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at,omitempty"`
}

type FixtureInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type line struct {
	WaitMS  int             `json:"wait_ms"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func NewManager(service Service, fixtureDir string) *Manager {
	if fixtureDir == "" {
		fixtureDir = "./fixtures/meetings"
	}
	return &Manager{
		service:    service,
		fixtureDir: fixtureDir,
		runs:       make(map[string]*runState),
	}
}

func (m *Manager) FixtureDir() string {
	return m.fixtureDir
}

func (m *Manager) ListFixtures() ([]FixtureInfo, error) {
	entries, err := os.ReadDir(m.fixtureDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []FixtureInfo{}, nil
		}
		return nil, err
	}
	var fixtures []FixtureInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		fixtures = append(fixtures, FixtureInfo{
			Name: entry.Name(),
			Path: filepath.Join(m.fixtureDir, entry.Name()),
		})
	}
	return fixtures, nil
}

func (m *Manager) Start(ctx context.Context, meetingID, fixtureName string) (*RunStatus, error) {
	fixtureName = core.NormalizeFixtureName(fixtureName)
	path, err := m.safeFixturePath(fixtureName)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("fixture not found: %s", fixtureName)
		}
		return nil, err
	}

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
	m.mu.Unlock()

	go m.run(runCtx, state, path)

	return statusFromState(state), nil
}

func (m *Manager) Pause(meetingID string) (*RunStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.runs[meetingID]
	if !ok {
		return nil, core.ErrNotFound
	}
	state.paused = true
	state.status = "paused"
	return statusFromState(state), nil
}

func (m *Manager) Resume(meetingID string) (*RunStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.runs[meetingID]
	if !ok {
		return nil, core.ErrNotFound
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

	if err := m.service.Store().ResetMeeting(ctx, meetingID); err != nil {
		return err
	}
	_, err := m.service.AppendAndPublish(ctx, meetingID, core.EventMeetingState, map[string]any{
		"status":       "created",
		"recording":    false,
		"analyzing":    false,
		"participants": []string{},
	})
	return err
}

func (m *Manager) run(ctx context.Context, state *runState, path string) {
	defer func() {
		m.mu.Lock()
		if current, ok := m.runs[state.meetingID]; ok && current == state {
			state.status = "completed"
			delete(m.runs, state.meetingID)
		}
		m.mu.Unlock()
	}()

	_, _ = m.service.AppendAndPublish(ctx, state.meetingID, core.EventMeetingState, map[string]any{
		"status":       "started",
		"recording":    true,
		"analyzing":    true,
		"participants": []string{"Speaker A", "Speaker B", "Speaker C"},
	})

	file, err := os.Open(path)
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
	_, _ = m.service.AppendAndPublish(ctx, meetingID, core.EventError, map[string]any{
		"code":      code,
		"message":   message,
		"retryable": false,
	})
}

func (m *Manager) safeFixturePath(name string) (string, error) {
	name = core.NormalizeFixtureName(name)
	if strings.Contains(name, "..") {
		return "", errors.New("invalid fixture name")
	}
	fullPath := filepath.Join(m.fixtureDir, name)
	base, err := filepath.Abs(m.fixtureDir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}
	if resolved != base && !strings.HasPrefix(resolved, base+string(os.PathSeparator)) {
		return "", errors.New("fixture path escapes fixture directory")
	}
	return resolved, nil
}

func statusFromState(state *runState) *RunStatus {
	status := &RunStatus{
		MeetingID: state.meetingID,
		Fixture:   state.fixture,
		Status:    state.status,
	}
	if !state.startedAt.IsZero() {
		status.StartedAt = state.startedAt.Format(time.RFC3339)
	}
	return status
}
