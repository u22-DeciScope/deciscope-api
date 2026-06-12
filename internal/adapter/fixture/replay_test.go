package fixture

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

func TestManagerStartPauseResumeAndReset(t *testing.T) {
	service := &fakeReplayService{eventCh: make(chan string, 16), ended: make(chan struct{}, 1)}
	loader := fakeLoader{
		"demo.jsonl": `{"wait_ms":150,"type":"transcript.final","payload":{"text":"hello"}}`,
	}
	manager := NewManager(service, loader)

	status, err := manager.Start(context.Background(), "m_test", "")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if status.Fixture != "demo.jsonl" || status.Status != "running" {
		t.Fatalf("start status = %+v", status)
	}
	if _, err := manager.Pause("m_test"); err != nil {
		t.Fatalf("Pause() error = %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if service.hasEvent(domain.EventTranscriptFinal) {
		t.Fatal("replay published fixture event while paused")
	}

	if _, err := manager.Resume("m_test"); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	awaitEvent(t, service.eventCh, domain.EventTranscriptFinal)
	select {
	case <-service.ended:
	case <-time.After(time.Second):
		t.Fatal("replay did not end meeting")
	}

	if err := manager.Reset(context.Background(), "m_test"); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if service.resetCount != 1 {
		t.Fatalf("reset count = %d, want 1", service.resetCount)
	}
}

func TestManagerRejectsUnknownFixture(t *testing.T) {
	manager := NewManager(&fakeReplayService{}, fakeLoader{})
	if _, err := manager.Start(context.Background(), "m_test", "missing.jsonl"); err == nil {
		t.Fatal("Start() error = nil, want unknown fixture error")
	}
}

type fakeLoader map[string]string

func (fakeLoader) Dir() string { return "/fixtures" }

func (f fakeLoader) List() ([]application.FixtureInfo, error) {
	fixtures := make([]application.FixtureInfo, 0, len(f))
	for name := range f {
		fixtures = append(fixtures, application.FixtureInfo{Name: name, Path: "/fixtures/" + name})
	}
	return fixtures, nil
}

func (f fakeLoader) Open(name string) (io.ReadCloser, error) {
	content, ok := f[name]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

type fakeReplayService struct {
	mu         sync.Mutex
	events     []string
	eventCh    chan string
	ended      chan struct{}
	resetCount int
}

func (f *fakeReplayService) AppendAndPublish(_ context.Context, meetingID, eventType string, _ any) (*domain.Event, error) {
	f.mu.Lock()
	f.events = append(f.events, eventType)
	f.mu.Unlock()
	if f.eventCh != nil {
		f.eventCh <- eventType
	}
	return &domain.Event{MeetingID: meetingID, Type: eventType}, nil
}

func (f *fakeReplayService) EndMeeting(context.Context, string) (*domain.Report, []domain.Event, error) {
	if f.ended != nil {
		f.ended <- struct{}{}
	}
	return &domain.Report{}, nil, nil
}

func (f *fakeReplayService) ResetMeeting(context.Context, string) error {
	f.resetCount++
	return nil
}

func (f *fakeReplayService) hasEvent(eventType string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, value := range f.events {
		if value == eventType {
			return true
		}
	}
	return false
}

func awaitEvent(t *testing.T, events <-chan string, want string) {
	t.Helper()
	timeout := time.After(time.Second)
	for {
		select {
		case got := <-events:
			if got == want {
				return
			}
		case <-timeout:
			t.Fatalf("did not receive event %q", want)
		}
	}
}
