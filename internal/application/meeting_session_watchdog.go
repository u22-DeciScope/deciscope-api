package application

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"deciscope-core-api/internal/domain"
)

// DefaultMeetingSessionWatchdogInterval, DefaultMeetingSessionBotLostAfter and
// DefaultMeetingSessionBotEndAfter mirror the defaults documented for the
// DECISCOPE_SESSION_WATCHDOG_* environment variables in internal/app/config.go.
const (
	DefaultMeetingSessionWatchdogInterval = 15 * time.Second
	DefaultMeetingSessionBotLostAfter     = 60 * time.Second
	DefaultMeetingSessionBotEndAfter      = 180 * time.Second
)

// MeetingSessionEnder is the subset of MeetingSessionService the watchdog
// needs to force-end an unresponsive session. It is defined narrowly here (in
// the application package, alongside the other small port interfaces) so the
// watchdog does not depend on the full MeetingSessionService type.
type MeetingSessionEnder interface {
	UpdateMeetingSessionStatus(ctx context.Context, input MeetingSessionStatusUpdateInput) (*domain.MeetingSession, error)
}

// MeetingSessionWatchdogConfig configures the scan interval and the two
// thresholds the watchdog reacts to. LostAfter and EndAfter are measured
// against domain.MeetingSession.LastBotStatusAt.
type MeetingSessionWatchdogConfig struct {
	Interval  time.Duration
	LostAfter time.Duration
	EndAfter  time.Duration
}

// MeetingSessionWatchdog periodically scans in-flight meeting sessions and
// reacts when a bot stops sending heartbeats: it publishes a
// meeting_session.bot_health_changed event when connectivity is lost or
// recovers, and force-ends the session once the outage is long enough that
// the bot is presumed dead (e.g. the VM was killed and can never call back).
type MeetingSessionWatchdog struct {
	repository MeetingSessionRepository
	ender      MeetingSessionEnder
	publisher  MeetingSessionBotHealthPublisher
	config     MeetingSessionWatchdogConfig
	now        func() time.Time

	mu      sync.Mutex
	healthy map[string]bool // sessionID -> last published health state
}

func NewMeetingSessionWatchdog(repository MeetingSessionRepository, ender MeetingSessionEnder, publisher MeetingSessionBotHealthPublisher, config MeetingSessionWatchdogConfig) *MeetingSessionWatchdog {
	return &MeetingSessionWatchdog{
		repository: repository,
		ender:      ender,
		publisher:  publisher,
		config:     config,
		now:        time.Now,
		healthy:    make(map[string]bool),
	}
}

// SetNow overrides the clock used to evaluate elapsed time since
// LastBotStatusAt. It exists for tests that need to drive the watchdog
// deterministically without waiting on wall-clock time.
func (w *MeetingSessionWatchdog) SetNow(now func() time.Time) {
	w.now = now
}

// Start launches the periodic scan goroutine. It returns immediately; the
// goroutine stops when ctx is cancelled.
func (w *MeetingSessionWatchdog) Start(ctx context.Context) {
	interval := w.config.Interval
	if interval <= 0 {
		interval = DefaultMeetingSessionWatchdogInterval
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.RunOnce(ctx); err != nil {
					log.Printf("Meeting session watchdog scan failed. error=%v", err)
				}
			}
		}
	}()
}

// RunOnce performs a single scan. It is exported so tests can drive the
// watchdog deterministically with a fake clock instead of waiting on a
// ticker.
func (w *MeetingSessionWatchdog) RunOnce(ctx context.Context) error {
	sessions, err := w.repository.ListMeetingSessionsForBotWatchdog(ctx)
	if err != nil {
		return fmt.Errorf("list meeting sessions for bot watchdog: %w", err)
	}
	now := w.now().UTC()
	seen := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		seen[session.ID] = struct{}{}
		w.evaluate(ctx, session, now)
	}
	w.forgetMissing(seen)
	return nil
}

func (w *MeetingSessionWatchdog) evaluate(ctx context.Context, session domain.MeetingSession, now time.Time) {
	// Defense in depth: the repository query already filters by status and by
	// LastBotStatusAt being non-zero, but re-check here in case a caller wires
	// in a repository implementation that does not.
	if !isJoinedOrBeyondMeetingStatus(session.Status) {
		return
	}
	if session.LastBotStatusAt.IsZero() {
		return
	}

	elapsed := now.Sub(session.LastBotStatusAt.UTC())
	if elapsed >= w.config.EndAfter {
		w.endSession(ctx, session, elapsed)
		return
	}

	currentlyHealthy := elapsed < w.config.LostAfter
	w.mu.Lock()
	previouslyHealthy, known := w.healthy[session.ID]
	// Publish on a known state transition (healthy<->unhealthy), and on the
	// first observation of a session only if it is already unhealthy (e.g.
	// heartbeats had already stopped before an API restart or before the
	// first scan after join, so newly-subscribed clients still learn about
	// the outage). The very common case of a healthy session's first
	// observation (join, or API restart while the bot is fine) must not
	// publish a redundant healthy=true event. The map is always updated to
	// the current value regardless of whether we publish.
	publish := (known && previouslyHealthy != currentlyHealthy) || (!known && !currentlyHealthy)
	w.healthy[session.ID] = currentlyHealthy
	w.mu.Unlock()

	if !publish {
		return
	}
	log.Printf("Meeting session bot health changed. sessionId=%s healthy=%t lastBotStatusAt=%s elapsedSeconds=%.0f",
		session.ID, currentlyHealthy, session.LastBotStatusAt.UTC().Format(time.RFC3339Nano), elapsed.Seconds())
	if w.publisher != nil {
		w.publisher.PublishMeetingSessionBotHealth(session, currentlyHealthy)
	}
}

func (w *MeetingSessionWatchdog) endSession(ctx context.Context, session domain.MeetingSession, elapsed time.Duration) {
	log.Printf("Meeting session watchdog ending unresponsive session. sessionId=%s elapsedSeconds=%.0f endAfter=%s",
		session.ID, elapsed.Seconds(), w.config.EndAfter)
	_, err := w.ender.UpdateMeetingSessionStatus(ctx, MeetingSessionStatusUpdateInput{
		SessionID: session.ID,
		Status:    domain.MeetingSessionEnded,
		BotCallID: session.BotCallID,
		Reason:    "bot_unresponsive",
		Source:    "watchdog",
		Message:   fmt.Sprintf("bot heartbeat not received for %s", w.config.EndAfter),
	})
	if err != nil {
		log.Printf("Meeting session watchdog end failed. sessionId=%s error=%v", session.ID, err)
		return
	}
	w.mu.Lock()
	delete(w.healthy, session.ID)
	w.mu.Unlock()
}

func (w *MeetingSessionWatchdog) forgetMissing(seen map[string]struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id := range w.healthy {
		if _, ok := seen[id]; !ok {
			delete(w.healthy, id)
		}
	}
}
