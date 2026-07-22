package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

func TestMeetingSessionWatchdogPublishesUnhealthyOnceAfterLostAfter(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-90 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (second scan) error = %v", err)
	}

	events := publisher.snapshot()
	if len(events) != 1 {
		t.Fatalf("published events = %+v, want exactly one unhealthy event across two scans", events)
	}
	if events[0].sessionID != "session_1" || events[0].healthy {
		t.Fatalf("event = %+v, want unhealthy for session_1", events[0])
	}
	if len(ender.calls) != 0 {
		t.Fatalf("ender should not be called before EndAfter elapses, calls=%+v", ender.calls)
	}
}

func TestMeetingSessionWatchdogEndsSessionAfterEndAfter(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			BotCallID:       "call-1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-200 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if len(ender.calls) != 1 {
		t.Fatalf("ender calls = %+v, want exactly one", ender.calls)
	}
	call := ender.calls[0]
	if call.SessionID != "session_1" || call.Status != domain.MeetingSessionEnded || call.Reason != "bot_unresponsive" || call.Source != "watchdog" || call.BotCallID != "call-1" {
		t.Fatalf("end call = %+v", call)
	}
	if call.Message == "" {
		t.Fatalf("end call message should describe the outage, got empty string")
	}
}

func TestMeetingSessionWatchdogPublishesHealthyOnceOnRecovery(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-90 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (unhealthy scan) error = %v", err)
	}
	if got := publisher.snapshot(); len(got) != 1 || got[0].healthy {
		t.Fatalf("expected one unhealthy event before recovery, got %+v", got)
	}

	// Heartbeat resumes: LastBotStatusAt is now recent.
	repository.mu.Lock()
	repository.sessions[0].LastBotStatusAt = now
	repository.mu.Unlock()

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (recovery scan) error = %v", err)
	}
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (recovery scan repeat) error = %v", err)
	}

	events := publisher.snapshot()
	if len(events) != 2 {
		t.Fatalf("published events = %+v, want exactly one unhealthy + one healthy", events)
	}
	if events[1].sessionID != "session_1" || !events[1].healthy {
		t.Fatalf("recovery event = %+v, want healthy for session_1", events[1])
	}
}

func TestMeetingSessionWatchdogDoesNotPublishOnFirstHealthyObservation(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-5 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (second scan) error = %v", err)
	}

	if events := publisher.snapshot(); len(events) != 0 {
		t.Fatalf("published events = %+v, want none for a session observed healthy for the first time", events)
	}
	if len(ender.calls) != 0 {
		t.Fatalf("ender should not be called, calls=%+v", ender.calls)
	}
}

func TestMeetingSessionWatchdogIgnoresZeroLastBotStatusAt(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:     "session_1",
			Status: domain.MeetingSessionJoined,
			// LastBotStatusAt intentionally left zero.
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(publisher.snapshot()) != 0 || len(ender.calls) != 0 {
		t.Fatalf("a session with zero LastBotStatusAt must be ignored: published=%+v ended=%+v", publisher.snapshot(), ender.calls)
	}
}

func TestMeetingSessionWatchdogIgnoresOutOfScopeStatus(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRequested,
			LastBotStatusAt: now.Add(-300 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(publisher.snapshot()) != 0 || len(ender.calls) != 0 {
		t.Fatalf("a session outside the watched status set must be ignored: published=%+v ended=%+v", publisher.snapshot(), ender.calls)
	}
}

func TestMeetingSessionWatchdogTranscriptHealthStalledAndRecovers(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-5 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	tracker := &fakeTranscriptActivityReader{}
	transcriptPublisher := &fakeTranscriptHealthPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:     15 * time.Second,
		LostAfter:    60 * time.Second,
		EndAfter:     180 * time.Second,
		DelayedAfter: 30 * time.Second,
		StalledAfter: 60 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	watchdog.SetTranscriptActivity(tracker)
	watchdog.SetTranscriptHealthPublisher(transcriptPublisher)

	// (a) transcript last seen 90s ago (>= StalledAfter=60s) while the bot
	// heartbeat is healthy and the session is recording: transcript_stalled
	// must be published exactly once across repeated scans.
	tracker.set("session_1", now.Add(-90*time.Second))
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (stalled scan) error = %v", err)
	}
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (stalled scan repeat) error = %v", err)
	}
	events := transcriptPublisher.snapshot()
	if len(events) != 1 || events[0].sessionID != "session_1" || events[0].health != "transcript_stalled" {
		t.Fatalf("published events = %+v, want exactly one transcript_stalled event for session_1", events)
	}

	// (b) new transcript activity arrives: re-evaluating must publish a
	// recovery event back to "ok".
	tracker.set("session_1", now)
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (recovery scan) error = %v", err)
	}
	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (recovery scan repeat) error = %v", err)
	}
	events = transcriptPublisher.snapshot()
	if len(events) != 2 {
		t.Fatalf("published events = %+v, want exactly one transcript_stalled + one ok", events)
	}
	if events[1].sessionID != "session_1" || events[1].health != "ok" {
		t.Fatalf("recovery event = %+v, want ok for session_1", events[1])
	}
}

func TestMeetingSessionWatchdogDoesNotPublishTranscriptWarningWhenNotRecordingOrUnhealthy(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{
			{
				// Healthy heartbeat, but not "recording": a stalled transcript
				// here must not be reported as a transcript health problem.
				ID:              "session_not_recording",
				Status:          domain.MeetingSessionSpeechThrottled,
				LastBotStatusAt: now.Add(-5 * time.Second),
			},
			{
				// Recording, but the bot heartbeat itself is unhealthy: the bot
				// outage is the primary signal, so no separate transcript
				// warning should be published.
				ID:              "session_unhealthy_bot",
				Status:          domain.MeetingSessionRecording,
				LastBotStatusAt: now.Add(-90 * time.Second),
			},
		},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	tracker := &fakeTranscriptActivityReader{}
	tracker.set("session_not_recording", now.Add(-200*time.Second))
	tracker.set("session_unhealthy_bot", now.Add(-200*time.Second))
	transcriptPublisher := &fakeTranscriptHealthPublisher{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:     15 * time.Second,
		LostAfter:    60 * time.Second,
		EndAfter:     180 * time.Second,
		DelayedAfter: 30 * time.Second,
		StalledAfter: 60 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	watchdog.SetTranscriptActivity(tracker)
	watchdog.SetTranscriptHealthPublisher(transcriptPublisher)

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if events := transcriptPublisher.snapshot(); len(events) != 0 {
		t.Fatalf("published events = %+v, want none (not recording / bot unhealthy must not report transcript warnings)", events)
	}
}

func TestMeetingSessionWatchdogTranscriptHealthSilentWithFreshBotMetrics(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-5 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	tracker := &fakeTranscriptActivityReader{}
	transcriptPublisher := &fakeTranscriptHealthPublisher{}
	metrics := &fakeBotMediaMetricsReader{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:           15 * time.Second,
		LostAfter:          60 * time.Second,
		EndAfter:           180 * time.Second,
		DelayedAfter:       30 * time.Second,
		StalledAfter:       60 * time.Second,
		AudioSilenceAfter:  30 * time.Second,
		AudioStalledAfter:  60 * time.Second,
		SpeechStalledAfter: 60 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	watchdog.SetTranscriptActivity(tracker)
	watchdog.SetTranscriptHealthPublisher(transcriptPublisher)
	watchdog.SetBotMetrics(metrics)

	// Transcript gap of 40s (>= DelayedAfter=30s, < StalledAfter=60s). Bot
	// metrics are fresh: a recent audio frame arrived, but no non-zero audio
	// (no one is speaking) and no stall signal at all: silent.
	tracker.set("session_1", now.Add(-40*time.Second))
	metrics.set("session_1", application.BotMediaMetrics{
		HasMetrics:       true,
		ReceivedAt:       now.Add(-2 * time.Second),
		LastAudioFrameAt: now.Add(-2 * time.Second),
	})

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	events := transcriptPublisher.snapshot()
	if len(events) != 1 || events[0].sessionID != "session_1" || events[0].health != "silent" {
		t.Fatalf("published events = %+v, want exactly one silent event for session_1", events)
	}
}

func TestMeetingSessionWatchdogTranscriptHealthAudioStalledWithFreshBotMetrics(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-5 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	tracker := &fakeTranscriptActivityReader{}
	transcriptPublisher := &fakeTranscriptHealthPublisher{}
	metrics := &fakeBotMediaMetricsReader{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:           15 * time.Second,
		LostAfter:          60 * time.Second,
		EndAfter:           180 * time.Second,
		DelayedAfter:       30 * time.Second,
		StalledAfter:       60 * time.Second,
		AudioSilenceAfter:  30 * time.Second,
		AudioStalledAfter:  60 * time.Second,
		SpeechStalledAfter: 60 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	watchdog.SetTranscriptActivity(tracker)
	watchdog.SetTranscriptHealthPublisher(transcriptPublisher)
	watchdog.SetBotMetrics(metrics)

	tracker.set("session_1", now.Add(-40*time.Second))
	metrics.set("session_1", application.BotMediaMetrics{
		HasMetrics:   true,
		ReceivedAt:   now.Add(-2 * time.Second),
		AudioStalled: true,
	})

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	events := transcriptPublisher.snapshot()
	if len(events) != 1 || events[0].sessionID != "session_1" || events[0].health != "audio_stalled" {
		t.Fatalf("published events = %+v, want exactly one audio_stalled event for session_1 (AudioStalled=true)", events)
	}
}

func TestMeetingSessionWatchdogTranscriptHealthAudioStalledFromRecentReceiveStall(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-5 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	tracker := &fakeTranscriptActivityReader{}
	transcriptPublisher := &fakeTranscriptHealthPublisher{}
	metrics := &fakeBotMediaMetricsReader{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:           15 * time.Second,
		LostAfter:          60 * time.Second,
		EndAfter:           180 * time.Second,
		DelayedAfter:       30 * time.Second,
		StalledAfter:       60 * time.Second,
		AudioSilenceAfter:  30 * time.Second,
		AudioStalledAfter:  60 * time.Second,
		SpeechStalledAfter: 60 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	watchdog.SetTranscriptActivity(tracker)
	watchdog.SetTranscriptHealthPublisher(transcriptPublisher)
	watchdog.SetBotMetrics(metrics)

	tracker.set("session_1", now.Add(-40*time.Second))
	// AudioStalled is false, but a receive-stall event was reported recently
	// (10s ago, within StalledAfter=60s): still audio_stalled.
	metrics.set("session_1", application.BotMediaMetrics{
		HasMetrics:                    true,
		ReceivedAt:                    now.Add(-2 * time.Second),
		LastAudioFrameAt:              now.Add(-2 * time.Second),
		LastAudioSocketReceiveStallAt: now.Add(-10 * time.Second),
	})

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	events := transcriptPublisher.snapshot()
	if len(events) != 1 || events[0].sessionID != "session_1" || events[0].health != "audio_stalled" {
		t.Fatalf("published events = %+v, want exactly one audio_stalled event for session_1 (recent receive stall)", events)
	}
}

func TestMeetingSessionWatchdogTranscriptHealthSpeechStalledWithFreshBotMetrics(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-5 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	tracker := &fakeTranscriptActivityReader{}
	transcriptPublisher := &fakeTranscriptHealthPublisher{}
	metrics := &fakeBotMediaMetricsReader{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:           15 * time.Second,
		LostAfter:          60 * time.Second,
		EndAfter:           180 * time.Second,
		DelayedAfter:       30 * time.Second,
		StalledAfter:       60 * time.Second,
		AudioSilenceAfter:  30 * time.Second,
		AudioStalledAfter:  60 * time.Second,
		SpeechStalledAfter: 60 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	watchdog.SetTranscriptActivity(tracker)
	watchdog.SetTranscriptHealthPublisher(transcriptPublisher)
	watchdog.SetBotMetrics(metrics)

	// Transcript gap of 40s. Bot metrics: recent non-zero audio (someone is
	// speaking), but the last non-empty transcript is 90s old (>=
	// SpeechStalledAfter=60s): speech recognition itself appears stuck.
	tracker.set("session_1", now.Add(-40*time.Second))
	metrics.set("session_1", application.BotMediaMetrics{
		HasMetrics:               true,
		ReceivedAt:               now.Add(-2 * time.Second),
		LastAudioFrameAt:         now.Add(-2 * time.Second),
		LastNonZeroAudioAt:       now.Add(-5 * time.Second),
		LastNonEmptyTranscriptAt: now.Add(-90 * time.Second),
	})

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	events := transcriptPublisher.snapshot()
	if len(events) != 1 || events[0].sessionID != "session_1" || events[0].health != "speech_stalled" {
		t.Fatalf("published events = %+v, want exactly one speech_stalled event for session_1", events)
	}
}

func TestMeetingSessionWatchdogTranscriptHealthUnhealthyHeartbeatIgnoresBotMetrics(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-90 * time.Second), // unhealthy: elapsed >= LostAfter
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	tracker := &fakeTranscriptActivityReader{}
	transcriptPublisher := &fakeTranscriptHealthPublisher{}
	metrics := &fakeBotMediaMetricsReader{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:           15 * time.Second,
		LostAfter:          60 * time.Second,
		EndAfter:           180 * time.Second,
		DelayedAfter:       30 * time.Second,
		StalledAfter:       60 * time.Second,
		AudioSilenceAfter:  30 * time.Second,
		AudioStalledAfter:  60 * time.Second,
		SpeechStalledAfter: 60 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	watchdog.SetTranscriptActivity(tracker)
	watchdog.SetTranscriptHealthPublisher(transcriptPublisher)
	watchdog.SetBotMetrics(metrics)

	tracker.set("session_1", now.Add(-200*time.Second))
	// Metrics would classify as audio_stalled if consulted, but the bot
	// heartbeat itself is unhealthy, so the bot outage must remain the only
	// signal: no transcript health warning at all.
	metrics.set("session_1", application.BotMediaMetrics{
		HasMetrics:   true,
		ReceivedAt:   now.Add(-2 * time.Second),
		AudioStalled: true,
	})

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if events := transcriptPublisher.snapshot(); len(events) != 0 {
		t.Fatalf("published events = %+v, want none (bot heartbeat unhealthy must take priority over bot metrics)", events)
	}
}

func TestMeetingSessionWatchdogTranscriptHealthFallsBackWhenBotMetricsStale(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-5 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	tracker := &fakeTranscriptActivityReader{}
	transcriptPublisher := &fakeTranscriptHealthPublisher{}
	metrics := &fakeBotMediaMetricsReader{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:           15 * time.Second,
		LostAfter:          60 * time.Second,
		EndAfter:           180 * time.Second,
		DelayedAfter:       30 * time.Second,
		StalledAfter:       60 * time.Second,
		AudioSilenceAfter:  30 * time.Second,
		AudioStalledAfter:  60 * time.Second,
		SpeechStalledAfter: 60 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	watchdog.SetTranscriptActivity(tracker)
	watchdog.SetTranscriptHealthPublisher(transcriptPublisher)
	watchdog.SetBotMetrics(metrics)

	// Transcript gap of 70s (>= StalledAfter=60s): would be transcript_stalled
	// by the plain fallback. Bot metrics say AudioStalled=true, which would
	// normally win as audio_stalled, but they were received 120s ago — older
	// than the freshness limit (LostAfter=60s) — so they must be ignored and
	// the gap must safely degrade to the plain threshold classification.
	tracker.set("session_1", now.Add(-70*time.Second))
	metrics.set("session_1", application.BotMediaMetrics{
		HasMetrics:   true,
		ReceivedAt:   now.Add(-120 * time.Second),
		AudioStalled: true,
	})

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	events := transcriptPublisher.snapshot()
	if len(events) != 1 || events[0].sessionID != "session_1" || events[0].health != "transcript_stalled" {
		t.Fatalf("published events = %+v, want exactly one transcript_stalled event (stale bot metrics must be ignored)", events)
	}
}

func TestMeetingSessionWatchdogTranscriptHealthFallsBackWhenBotMetricsNeverRecorded(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	repository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-5 * time.Second),
		}},
	}
	ender := &fakeWatchdogEnder{}
	publisher := &fakeWatchdogPublisher{}
	tracker := &fakeTranscriptActivityReader{}
	transcriptPublisher := &fakeTranscriptHealthPublisher{}
	// A bot metrics reader is configured, but this session never reported
	// any metrics (Get returns ok=false): the watchdog must still safely
	// degrade to the plain transcript_delayed/transcript_stalled threshold
	// classification instead of erroring or reporting "ok".
	metrics := &fakeBotMediaMetricsReader{}
	watchdog := application.NewMeetingSessionWatchdog(repository, ender, publisher, application.MeetingSessionWatchdogConfig{
		Interval:           15 * time.Second,
		LostAfter:          60 * time.Second,
		EndAfter:           180 * time.Second,
		DelayedAfter:       30 * time.Second,
		StalledAfter:       60 * time.Second,
		AudioSilenceAfter:  30 * time.Second,
		AudioStalledAfter:  60 * time.Second,
		SpeechStalledAfter: 60 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	watchdog.SetTranscriptActivity(tracker)
	watchdog.SetTranscriptHealthPublisher(transcriptPublisher)
	watchdog.SetBotMetrics(metrics)

	tracker.set("session_1", now.Add(-40*time.Second)) // >= DelayedAfter(30s), < StalledAfter(60s)

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	events := transcriptPublisher.snapshot()
	if len(events) != 1 || events[0].sessionID != "session_1" || events[0].health != "transcript_delayed" {
		t.Fatalf("published events = %+v, want exactly one transcript_delayed event (no bot metrics recorded)", events)
	}
}

func TestMeetingSessionWatchdogForgetsBotMetricsOnEndSessionAndForgetMissing(t *testing.T) {
	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	endedRepository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_ended",
			BotCallID:       "call-1",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-200 * time.Second), // >= EndAfter
		}},
	}
	metrics := &fakeBotMediaMetricsReader{}
	metrics.set("session_ended", application.BotMediaMetrics{HasMetrics: true, ReceivedAt: now})
	watchdog := application.NewMeetingSessionWatchdog(endedRepository, &fakeWatchdogEnder{}, &fakeWatchdogPublisher{}, application.MeetingSessionWatchdogConfig{
		Interval:  15 * time.Second,
		LostAfter: 60 * time.Second,
		EndAfter:  180 * time.Second,
	})
	watchdog.SetNow(func() time.Time { return now })
	watchdog.SetBotMetrics(metrics)

	if err := watchdog.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (end scan) error = %v", err)
	}
	if _, ok := metrics.Get("session_ended"); ok {
		t.Fatalf("endSession must forget the session's bot metrics")
	}

	// Separately: a session that simply disappears from the watchdog's
	// listing (e.g. it left the watched status set) must also have its bot
	// metrics forgotten via forgetMissing.
	missingRepository := &fakeWatchdogRepository{
		sessions: []domain.MeetingSession{{
			ID:              "session_missing",
			Status:          domain.MeetingSessionRecording,
			LastBotStatusAt: now.Add(-5 * time.Second),
		}},
	}
	tracker := &fakeTranscriptActivityReader{}
	metrics2 := &fakeBotMediaMetricsReader{}
	metrics2.set("session_missing", application.BotMediaMetrics{HasMetrics: true, ReceivedAt: now})
	watchdog2 := application.NewMeetingSessionWatchdog(missingRepository, &fakeWatchdogEnder{}, &fakeWatchdogPublisher{}, application.MeetingSessionWatchdogConfig{
		Interval:     15 * time.Second,
		LostAfter:    60 * time.Second,
		EndAfter:     180 * time.Second,
		DelayedAfter: 30 * time.Second,
		StalledAfter: 60 * time.Second,
	})
	watchdog2.SetNow(func() time.Time { return now })
	watchdog2.SetTranscriptActivity(tracker)
	watchdog2.SetBotMetrics(metrics2)

	if err := watchdog2.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (first scan) error = %v", err)
	}
	if _, ok := metrics2.Get("session_missing"); !ok {
		t.Fatalf("precondition failed: session_missing bot metrics should still be recorded after the first scan")
	}

	missingRepository.mu.Lock()
	missingRepository.sessions = nil
	missingRepository.mu.Unlock()

	if err := watchdog2.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() (second scan, session missing) error = %v", err)
	}
	if _, ok := metrics2.Get("session_missing"); ok {
		t.Fatalf("forgetMissing must forget bot metrics for a session that dropped out of the watchdog listing")
	}
}

type fakeBotMediaMetricsReader struct {
	mu      sync.Mutex
	metrics map[string]application.BotMediaMetrics
}

func (f *fakeBotMediaMetricsReader) set(sessionID string, m application.BotMediaMetrics) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.metrics == nil {
		f.metrics = make(map[string]application.BotMediaMetrics)
	}
	f.metrics[sessionID] = m
}

func (f *fakeBotMediaMetricsReader) Get(sessionID string) (application.BotMediaMetrics, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.metrics[sessionID]
	return m, ok
}

func (f *fakeBotMediaMetricsReader) Forget(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.metrics, sessionID)
}

type fakeTranscriptActivityReader struct {
	mu       sync.Mutex
	activity map[string]application.TranscriptActivity
}

func (f *fakeTranscriptActivityReader) EnsureSeen(sessionID string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.activity == nil {
		f.activity = make(map[string]application.TranscriptActivity)
	}
	if _, ok := f.activity[sessionID]; ok {
		return
	}
	f.activity[sessionID] = application.TranscriptActivity{LastTranscriptAt: at}
}

func (f *fakeTranscriptActivityReader) Activity(sessionID string) (application.TranscriptActivity, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	activity, ok := f.activity[sessionID]
	return activity, ok
}

func (f *fakeTranscriptActivityReader) Forget(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.activity, sessionID)
}

// set directly seeds an activity entry for a test, bypassing EnsureSeen's
// "do not overwrite" rule.
func (f *fakeTranscriptActivityReader) set(sessionID string, lastTranscriptAt time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.activity == nil {
		f.activity = make(map[string]application.TranscriptActivity)
	}
	f.activity[sessionID] = application.TranscriptActivity{LastTranscriptAt: lastTranscriptAt}
}

type transcriptHealthEvent struct {
	sessionID string
	health    string
	seconds   int
}

type fakeTranscriptHealthPublisher struct {
	mu     sync.Mutex
	events []transcriptHealthEvent
}

func (f *fakeTranscriptHealthPublisher) PublishMeetingSessionTranscriptHealth(session domain.MeetingSession, transcriptHealth string, secondsSinceLastTranscript int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, transcriptHealthEvent{sessionID: session.ID, health: transcriptHealth, seconds: secondsSinceLastTranscript})
}

func (f *fakeTranscriptHealthPublisher) snapshot() []transcriptHealthEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]transcriptHealthEvent{}, f.events...)
}

type fakeWatchdogRepository struct {
	mu       sync.Mutex
	sessions []domain.MeetingSession
}

func (f *fakeWatchdogRepository) ListMeetingSessionsForBotWatchdog(context.Context) ([]domain.MeetingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.MeetingSession{}, f.sessions...), nil
}

func (f *fakeWatchdogRepository) CreateMeetingSession(context.Context, domain.MeetingSession) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) CreateOrReuseMeetingSession(context.Context, domain.MeetingSession) (*domain.MeetingSession, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) GetMeetingSession(context.Context, string) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) ListMeetingSessions(context.Context, string, int) ([]domain.MeetingSession, error) {
	return nil, nil
}

func (f *fakeWatchdogRepository) UpdateMeetingSessionStatus(context.Context, domain.MeetingSessionStatusUpdate) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) UpdateMeetingSessionMetadata(context.Context, domain.MeetingSessionMetadataUpdate) (*domain.MeetingSession, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) MarkStaleMeetingSessions(context.Context, time.Time, time.Time) ([]domain.MeetingSession, error) {
	return nil, nil
}

func (f *fakeWatchdogRepository) ListMeetingSessionDebug(context.Context, int) ([]domain.MeetingSessionDebug, error) {
	return nil, nil
}

func (f *fakeWatchdogRepository) TouchMeetingSessionBotSeen(context.Context, string, time.Time) (*domain.MeetingSession, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (f *fakeWatchdogRepository) DeleteMeetingSession(context.Context, string) error {
	return errors.New("not implemented")
}

type fakeWatchdogEnder struct {
	mu    sync.Mutex
	calls []application.MeetingSessionStatusUpdateInput
}

func (f *fakeWatchdogEnder) UpdateMeetingSessionStatus(_ context.Context, input application.MeetingSessionStatusUpdateInput) (*domain.MeetingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, input)
	return &domain.MeetingSession{ID: input.SessionID, Status: input.Status}, nil
}

type watchdogHealthEvent struct {
	sessionID string
	healthy   bool
}

type fakeWatchdogPublisher struct {
	mu     sync.Mutex
	events []watchdogHealthEvent
}

func (f *fakeWatchdogPublisher) PublishMeetingSessionBotHealth(session domain.MeetingSession, healthy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, watchdogHealthEvent{sessionID: session.ID, healthy: healthy})
}

func (f *fakeWatchdogPublisher) snapshot() []watchdogHealthEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]watchdogHealthEvent{}, f.events...)
}
