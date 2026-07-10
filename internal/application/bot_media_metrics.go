package application

import (
	"sync"
	"time"
)

// BotMediaMetrics captures the audio/transcript liveness metrics the bot
// reports on a heartbeat. It exists so the watchdog can distinguish
// silent/audio_stalled/speech_stalled transcript health states from a plain
// transcript gap, using the bot's own view of audio activity as a signal
// separate from both bot heartbeat health and transcript arrival.
type BotMediaMetrics struct {
	// ReceivedAt is when the API received this heartbeat (not a bot-reported
	// timestamp); the watchdog uses it to decide whether these metrics are
	// still fresh enough to trust.
	ReceivedAt                    time.Time
	LastAudioFrameAt              time.Time
	LastNonZeroAudioAt            time.Time
	LastNonEmptyTranscriptAt      time.Time
	LastFinalTranscriptAt         time.Time
	LastPeakAmplitude             int
	LastRmsAmplitude              float64
	AudioFrameCount               int64
	FramesSinceLastNonZeroAudio   int64
	SecondsSinceLastNonZeroAudio  int
	ActiveSpeakerRecognizerCount  int
	MixedFallbackActive           bool
	UnmixedAudioSeen              bool
	LastAudioSocketReceiveStallAt time.Time
	AudioSocketReceiveStallCount  int64
	AudioStalled                  bool
	// HasMetrics reports whether this value actually carries at least one
	// audio/transcript metric, as opposed to a bare/empty heartbeat. The
	// watchdog must not classify against an all-zero value.
	HasMetrics bool
}

// BotMediaMetricsStore is an in-memory, per-session store for the most
// recently reported BotMediaMetrics. There is deliberately no DB
// persistence: these are short-lived liveness metrics for the watchdog to
// react to, not history to retain.
type BotMediaMetricsStore struct {
	mu      sync.Mutex
	metrics map[string]BotMediaMetrics
}

func NewBotMediaMetricsStore() *BotMediaMetricsStore {
	return &BotMediaMetricsStore{metrics: make(map[string]BotMediaMetrics)}
}

// Record stores m for sessionID, stamping ReceivedAt with the current time
// so callers do not need to set it themselves. A blank sessionID is ignored.
func (s *BotMediaMetricsStore) Record(sessionID string, m BotMediaMetrics) {
	if sessionID == "" {
		return
	}
	m.ReceivedAt = time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics[sessionID] = m
}

func (s *BotMediaMetricsStore) Get(sessionID string) (BotMediaMetrics, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.metrics[sessionID]
	return m, ok
}

func (s *BotMediaMetricsStore) Forget(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.metrics, sessionID)
}
