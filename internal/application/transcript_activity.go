package application

import (
	"strings"
	"sync"
	"time"

	"deciscope-core-api/internal/domain"
)

// TranscriptActivity captures when a session last produced transcript
// activity, along with narrower final/non-empty variants that the watchdog
// can use if it ever needs to distinguish "nothing arrived" from "only
// partials/empty text arrived".
type TranscriptActivity struct {
	LastTranscriptAt         time.Time
	LastFinalTranscriptAt    time.Time
	LastNonEmptyTranscriptAt time.Time
}

// TranscriptActivityTracker is an in-memory TranscriptSegmentPublisher that
// records, per session, when transcript segments were last seen. It exists so
// the watchdog can detect "transcript health" (is text still flowing) as a
// signal separate from bot heartbeat health.
type TranscriptActivityTracker struct {
	mu       sync.Mutex
	activity map[string]TranscriptActivity
}

func NewTranscriptActivityTracker() *TranscriptActivityTracker {
	return &TranscriptActivityTracker{activity: make(map[string]TranscriptActivity)}
}

// PublishTranscriptSegment implements application.TranscriptSegmentPublisher.
func (t *TranscriptActivityTracker) PublishTranscriptSegment(segment domain.TranscriptSegment) {
	if segment.SessionID == "" {
		return
	}
	at := segment.ReceivedAtUTC
	if at.IsZero() {
		at = time.Now().UTC()
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.activity[segment.SessionID]
	entry.LastTranscriptAt = at
	if segment.IsFinal {
		entry.LastFinalTranscriptAt = at
	}
	if strings.TrimSpace(segment.Text) != "" {
		entry.LastNonEmptyTranscriptAt = at
	}
	t.activity[segment.SessionID] = entry
}

// EnsureSeen establishes a baseline LastTranscriptAt for sessionID if none is
// recorded yet. It does nothing if activity is already known, so it never
// overwrites a real observation with a baseline.
func (t *TranscriptActivityTracker) EnsureSeen(sessionID string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.activity[sessionID]; ok {
		return
	}
	t.activity[sessionID] = TranscriptActivity{LastTranscriptAt: at}
}

func (t *TranscriptActivityTracker) Activity(sessionID string) (TranscriptActivity, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	activity, ok := t.activity[sessionID]
	return activity, ok
}

func (t *TranscriptActivityTracker) Forget(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.activity, sessionID)
}
