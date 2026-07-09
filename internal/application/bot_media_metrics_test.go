package application_test

import (
	"testing"
	"time"

	"deciscope-core-api/internal/application"
)

func TestBotMediaMetricsStoreGetUnknownSessionReturnsFalse(t *testing.T) {
	store := application.NewBotMediaMetricsStore()
	if _, ok := store.Get("missing"); ok {
		t.Fatalf("Get() on an unknown session should return ok=false")
	}
}

func TestBotMediaMetricsStoreRecordIgnoresBlankSessionID(t *testing.T) {
	store := application.NewBotMediaMetricsStore()
	store.Record("", application.BotMediaMetrics{HasMetrics: true, AudioStalled: true})
	if _, ok := store.Get(""); ok {
		t.Fatalf("Record() with a blank sessionID must be a no-op")
	}
}

func TestBotMediaMetricsStoreRecordStampsReceivedAtAndRoundTrips(t *testing.T) {
	store := application.NewBotMediaMetricsStore()
	before := time.Now().UTC()
	store.Record("session_1", application.BotMediaMetrics{
		HasMetrics:        true,
		LastAudioFrameAt:  before.Add(-1 * time.Second),
		LastPeakAmplitude: 42,
		LastRmsAmplitude:  0.5,
		AudioFrameCount:   100,
	})
	after := time.Now().UTC()

	got, ok := store.Get("session_1")
	if !ok {
		t.Fatalf("Get() after Record() should return ok=true")
	}
	if !got.HasMetrics {
		t.Fatalf("HasMetrics = false, want true")
	}
	if got.LastPeakAmplitude != 42 || got.AudioFrameCount != 100 || got.LastRmsAmplitude != 0.5 {
		t.Fatalf("stored metrics = %+v, want the recorded field values preserved", got)
	}
	if got.ReceivedAt.Before(before) || got.ReceivedAt.After(after) {
		t.Fatalf("ReceivedAt = %s, want between %s and %s (stamped by Record)", got.ReceivedAt, before, after)
	}
}

func TestBotMediaMetricsStoreRecordOverwritesPreviousValue(t *testing.T) {
	store := application.NewBotMediaMetricsStore()
	store.Record("session_1", application.BotMediaMetrics{HasMetrics: true, AudioFrameCount: 1})
	store.Record("session_1", application.BotMediaMetrics{HasMetrics: true, AudioFrameCount: 2})

	got, ok := store.Get("session_1")
	if !ok || got.AudioFrameCount != 2 {
		t.Fatalf("Get() = (%+v, %t), want the latest recorded value (AudioFrameCount=2)", got, ok)
	}
}

func TestBotMediaMetricsStoreForgetRemovesEntry(t *testing.T) {
	store := application.NewBotMediaMetricsStore()
	store.Record("session_1", application.BotMediaMetrics{HasMetrics: true})
	if _, ok := store.Get("session_1"); !ok {
		t.Fatalf("precondition failed: session_1 should be recorded")
	}

	store.Forget("session_1")

	if _, ok := store.Get("session_1"); ok {
		t.Fatalf("Get() after Forget() should return ok=false")
	}
}
