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

// MeetingSessionWatchdogConfig configures the scan interval and the
// thresholds the watchdog reacts to. LostAfter and EndAfter are measured
// against domain.MeetingSession.LastBotStatusAt. DelayedAfter and
// StalledAfter are measured against TranscriptActivity.LastTranscriptAt and
// only apply while the bot heartbeat itself is healthy (see
// evaluateTranscriptHealth). AudioSilenceAfter, AudioStalledAfter and
// SpeechStalledAfter are measured against the bot-reported BotMediaMetrics
// timestamps and are only consulted once a transcript gap (>= DelayedAfter)
// is already observed, to further classify it as silent/audio_stalled/
// speech_stalled instead of the generic transcript_delayed/transcript_stalled.
type MeetingSessionWatchdogConfig struct {
	Interval           time.Duration
	LostAfter          time.Duration
	EndAfter           time.Duration
	DelayedAfter       time.Duration
	StalledAfter       time.Duration
	AudioSilenceAfter  time.Duration
	AudioStalledAfter  time.Duration
	SpeechStalledAfter time.Duration
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

	transcriptActivity        TranscriptActivityReader
	transcriptHealthPublisher MeetingSessionTranscriptHealthPublisher
	botMetrics                BotMediaMetricsReader

	mu               sync.Mutex
	healthy          map[string]bool   // sessionID -> last published bot health state
	transcriptHealth map[string]string // sessionID -> last published transcript health state ("ok" by default)
}

func NewMeetingSessionWatchdog(repository MeetingSessionRepository, ender MeetingSessionEnder, publisher MeetingSessionBotHealthPublisher, config MeetingSessionWatchdogConfig) *MeetingSessionWatchdog {
	return &MeetingSessionWatchdog{
		repository:       repository,
		ender:            ender,
		publisher:        publisher,
		config:           config,
		now:              time.Now,
		healthy:          make(map[string]bool),
		transcriptHealth: make(map[string]string),
	}
}

// SetNow overrides the clock used to evaluate elapsed time since
// LastBotStatusAt. It exists for tests that need to drive the watchdog
// deterministically without waiting on wall-clock time.
func (w *MeetingSessionWatchdog) SetNow(now func() time.Time) {
	w.now = now
}

// SetTranscriptActivity injects the transcript activity reader used to
// evaluate transcript health. It is a setter (like SetNow) rather than a
// constructor parameter so existing callers are unaffected; transcript
// health evaluation is a no-op until this is set.
func (w *MeetingSessionWatchdog) SetTranscriptActivity(reader TranscriptActivityReader) {
	w.transcriptActivity = reader
}

// SetTranscriptHealthPublisher injects the publisher notified of transcript
// health transitions. See SetTranscriptActivity.
func (w *MeetingSessionWatchdog) SetTranscriptHealthPublisher(pub MeetingSessionTranscriptHealthPublisher) {
	w.transcriptHealthPublisher = pub
}

// SetBotMetrics injects the bot-reported audio/transcript metrics reader
// used to classify a transcript gap as silent/audio_stalled/speech_stalled
// instead of the generic transcript_delayed/transcript_stalled. It is a
// setter (like SetTranscriptActivity) rather than a constructor parameter so
// existing callers are unaffected; when unset, transcript gaps always fall
// back to the plain threshold classification.
func (w *MeetingSessionWatchdog) SetBotMetrics(reader BotMediaMetricsReader) {
	w.botMetrics = reader
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

	if publish {
		log.Printf("Meeting session bot health changed. sessionId=%s healthy=%t lastBotStatusAt=%s elapsedSeconds=%.0f",
			session.ID, currentlyHealthy, session.LastBotStatusAt.UTC().Format(time.RFC3339Nano), elapsed.Seconds())
		if w.publisher != nil {
			w.publisher.PublishMeetingSessionBotHealth(session, currentlyHealthy)
		}
	}

	w.evaluateTranscriptHealth(session, now, currentlyHealthy)
}

// evaluateTranscriptHealth checks whether transcript segments are still
// flowing for session and publishes a meeting_session.transcript_health_changed
// event on transitions. Bot heartbeat health takes priority: if the bot is
// unhealthy, or the session is not actively recording, a stalled transcript
// is an expected symptom rather than a separate problem, so no transcript
// warning is published (and any previously-published warning is not
// re-evaluated here — it simply stops being refreshed until recording
// resumes).
//
// Once a transcript gap is observed (elapsed time since the last transcript
// segment reaches DelayedAfter), classifyTranscriptGap further distinguishes
// silent/audio_stalled/speech_stalled using the bot-reported BotMediaMetrics
// when they are available and fresh; otherwise (no metrics reader, no
// metrics recorded yet, or metrics too old to trust) it safely degrades to
// the original transcript_delayed/transcript_stalled threshold classification.
func (w *MeetingSessionWatchdog) evaluateTranscriptHealth(session domain.MeetingSession, now time.Time, heartbeatHealthy bool) {
	if w.transcriptActivity == nil {
		return
	}
	desired := "ok"
	seconds := 0
	if heartbeatHealthy && session.Status == domain.MeetingSessionRecording {
		w.transcriptActivity.EnsureSeen(session.ID, now)
		if act, ok := w.transcriptActivity.Activity(session.ID); ok && !act.LastTranscriptAt.IsZero() {
			since := now.Sub(act.LastTranscriptAt.UTC())
			if since < 0 {
				since = 0
			}
			seconds = int(since.Seconds())
			if since >= w.config.DelayedAfter {
				desired = w.classifyTranscriptGap(session.ID, now, since)
			}
		}
	}

	w.mu.Lock()
	prev, known := w.transcriptHealth[session.ID]
	changed := (!known && desired != "ok") || (known && prev != desired)
	w.transcriptHealth[session.ID] = desired
	w.mu.Unlock()

	if !changed {
		return
	}
	log.Printf("Meeting session transcript health changed. sessionId=%s transcriptHealth=%s secondsSinceLastTranscript=%d heartbeatHealthy=%t status=%s",
		session.ID, desired, seconds, heartbeatHealthy, session.Status)
	if w.transcriptHealthPublisher != nil {
		w.transcriptHealthPublisher.PublishMeetingSessionTranscriptHealth(session, desired, seconds)
	}
}

// classifyTranscriptGap decides the transcript health state to report for a
// session whose last transcript segment is at least DelayedAfter old. It
// first tries to classify the gap using fresh bot-reported media metrics
// (silent/audio_stalled/speech_stalled); when that is not possible, it falls
// back to the plain transcript_delayed/transcript_stalled threshold
// classification.
func (w *MeetingSessionWatchdog) classifyTranscriptGap(sessionID string, now time.Time, since time.Duration) string {
	if w.botMetrics != nil {
		if m, ok := w.botMetrics.Get(sessionID); ok && m.HasMetrics && !m.ReceivedAt.IsZero() {
			metricsAge := now.Sub(m.ReceivedAt.UTC())
			if metricsAge < 0 {
				metricsAge = 0
			}
			if w.config.LostAfter > 0 && metricsAge < w.config.LostAfter {
				if state, matched := classifyBotMediaGap(m, now, w.config); matched {
					return state
				}
			}
		}
	}
	switch {
	case w.config.StalledAfter > 0 && since >= w.config.StalledAfter:
		return "transcript_stalled"
	case w.config.DelayedAfter > 0 && since >= w.config.DelayedAfter:
		return "transcript_delayed"
	default:
		return "ok"
	}
}

// classifyBotMediaGap classifies a transcript gap using the bot-reported
// media metrics m as of now. It returns ("", false) when none of the
// audio-based conditions apply, so the caller should fall back to the
// transcript-gap threshold classification instead.
func classifyBotMediaGap(m BotMediaMetrics, now time.Time, config MeetingSessionWatchdogConfig) (string, bool) {
	audioStalled := m.AudioStalled ||
		(!m.LastAudioSocketReceiveStallAt.IsZero() && now.Sub(m.LastAudioSocketReceiveStallAt.UTC()) < config.StalledAfter) ||
		(!m.LastAudioFrameAt.IsZero() && now.Sub(m.LastAudioFrameAt.UTC()) >= config.AudioStalledAfter)
	if audioStalled {
		return "audio_stalled", true
	}

	frameRecent := !m.LastAudioFrameAt.IsZero() && now.Sub(m.LastAudioFrameAt.UTC()) < config.AudioStalledAfter
	nonZeroAudioStale := m.LastNonZeroAudioAt.IsZero() || now.Sub(m.LastNonZeroAudioAt.UTC()) >= config.AudioSilenceAfter
	if nonZeroAudioStale && frameRecent {
		return "silent", true
	}

	nonZeroAudioRecent := !m.LastNonZeroAudioAt.IsZero() && now.Sub(m.LastNonZeroAudioAt.UTC()) < config.AudioSilenceAfter
	transcriptStale := m.LastNonEmptyTranscriptAt.IsZero() || now.Sub(m.LastNonEmptyTranscriptAt.UTC()) >= config.SpeechStalledAfter
	if nonZeroAudioRecent && transcriptStale {
		return "speech_stalled", true
	}

	return "", false
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
	delete(w.transcriptHealth, session.ID)
	w.mu.Unlock()
	if w.transcriptActivity != nil {
		w.transcriptActivity.Forget(session.ID)
	}
	if w.botMetrics != nil {
		w.botMetrics.Forget(session.ID)
	}
}

func (w *MeetingSessionWatchdog) forgetMissing(seen map[string]struct{}) {
	w.mu.Lock()
	var forgotten []string
	for id := range w.healthy {
		if _, ok := seen[id]; !ok {
			delete(w.healthy, id)
		}
	}
	for id := range w.transcriptHealth {
		if _, ok := seen[id]; !ok {
			delete(w.transcriptHealth, id)
			forgotten = append(forgotten, id)
		}
	}
	w.mu.Unlock()
	if w.transcriptActivity != nil {
		for _, id := range forgotten {
			w.transcriptActivity.Forget(id)
		}
	}
	if w.botMetrics != nil {
		for _, id := range forgotten {
			w.botMetrics.Forget(id)
		}
	}
}
