package application_test

import (
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

func TestTranscriptActivityTrackerPublishUpdatesLastTimestamps(t *testing.T) {
	tracker := application.NewTranscriptActivityTracker()

	final := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	tracker.PublishTranscriptSegment(domain.TranscriptSegment{
		SessionID:     "session_1",
		Text:          "final text",
		IsFinal:       true,
		ReceivedAtUTC: final,
	})
	activity, ok := tracker.Activity("session_1")
	if !ok {
		t.Fatalf("Activity() ok = false, want true after publish")
	}
	if !activity.LastTranscriptAt.Equal(final) {
		t.Fatalf("LastTranscriptAt = %v, want %v", activity.LastTranscriptAt, final)
	}
	if !activity.LastFinalTranscriptAt.Equal(final) {
		t.Fatalf("LastFinalTranscriptAt = %v, want %v", activity.LastFinalTranscriptAt, final)
	}
	if !activity.LastNonEmptyTranscriptAt.Equal(final) {
		t.Fatalf("LastNonEmptyTranscriptAt = %v, want %v", activity.LastNonEmptyTranscriptAt, final)
	}

	// A later partial with blank text advances LastTranscriptAt only; the
	// final/non-empty timestamps must not move backward or be touched by a
	// blank partial.
	partial := final.Add(5 * time.Second)
	tracker.PublishTranscriptSegment(domain.TranscriptSegment{
		SessionID:     "session_1",
		Text:          "   ",
		IsFinal:       false,
		ReceivedAtUTC: partial,
	})
	activity, ok = tracker.Activity("session_1")
	if !ok {
		t.Fatalf("Activity() ok = false, want true")
	}
	if !activity.LastTranscriptAt.Equal(partial) {
		t.Fatalf("LastTranscriptAt = %v, want %v", activity.LastTranscriptAt, partial)
	}
	if !activity.LastFinalTranscriptAt.Equal(final) {
		t.Fatalf("LastFinalTranscriptAt = %v, want unchanged %v", activity.LastFinalTranscriptAt, final)
	}
	if !activity.LastNonEmptyTranscriptAt.Equal(final) {
		t.Fatalf("LastNonEmptyTranscriptAt = %v, want unchanged %v", activity.LastNonEmptyTranscriptAt, final)
	}
}

func TestTranscriptActivityTrackerPublishIgnoresEmptySessionID(t *testing.T) {
	tracker := application.NewTranscriptActivityTracker()
	tracker.PublishTranscriptSegment(domain.TranscriptSegment{
		SessionID:     "",
		Text:          "hello",
		ReceivedAtUTC: time.Now().UTC(),
	})
	if _, ok := tracker.Activity(""); ok {
		t.Fatalf("Activity(\"\") ok = true, want no entry to be recorded for an empty session id")
	}
}

func TestTranscriptActivityTrackerPublishDefaultsZeroReceivedAtToNow(t *testing.T) {
	tracker := application.NewTranscriptActivityTracker()
	before := time.Now().UTC()
	tracker.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", Text: "hi"})
	after := time.Now().UTC()

	activity, ok := tracker.Activity("session_1")
	if !ok {
		t.Fatalf("Activity() ok = false, want true")
	}
	if activity.LastTranscriptAt.Before(before) || activity.LastTranscriptAt.After(after) {
		t.Fatalf("LastTranscriptAt = %v, want between %v and %v", activity.LastTranscriptAt, before, after)
	}
}

func TestTranscriptActivityTrackerEnsureSeenDoesNotOverwriteExisting(t *testing.T) {
	tracker := application.NewTranscriptActivityTracker()
	original := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	tracker.PublishTranscriptSegment(domain.TranscriptSegment{
		SessionID:     "session_1",
		Text:          "hello",
		ReceivedAtUTC: original,
	})

	tracker.EnsureSeen("session_1", original.Add(time.Hour))
	activity, ok := tracker.Activity("session_1")
	if !ok {
		t.Fatalf("Activity() ok = false, want true")
	}
	if !activity.LastTranscriptAt.Equal(original) {
		t.Fatalf("LastTranscriptAt = %v, want unchanged %v (EnsureSeen must not overwrite an existing baseline)", activity.LastTranscriptAt, original)
	}
}

func TestTranscriptActivityTrackerEnsureSeenEstablishesBaselineOnce(t *testing.T) {
	tracker := application.NewTranscriptActivityTracker()
	baseline := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)

	tracker.EnsureSeen("session_1", baseline)
	activity, ok := tracker.Activity("session_1")
	if !ok {
		t.Fatalf("Activity() ok = false, want true after EnsureSeen establishes a baseline")
	}
	if !activity.LastTranscriptAt.Equal(baseline) {
		t.Fatalf("LastTranscriptAt = %v, want baseline %v", activity.LastTranscriptAt, baseline)
	}
	if !activity.LastFinalTranscriptAt.IsZero() || !activity.LastNonEmptyTranscriptAt.IsZero() {
		t.Fatalf("activity = %+v, want final/non-empty timestamps to stay zero for a baseline-only entry", activity)
	}
}

func TestTranscriptActivityTrackerForgetRemovesActivity(t *testing.T) {
	tracker := application.NewTranscriptActivityTracker()
	tracker.PublishTranscriptSegment(domain.TranscriptSegment{SessionID: "session_1", Text: "hi", ReceivedAtUTC: time.Now().UTC()})
	if _, ok := tracker.Activity("session_1"); !ok {
		t.Fatalf("Activity() ok = false, want true before Forget")
	}

	tracker.Forget("session_1")
	if _, ok := tracker.Activity("session_1"); ok {
		t.Fatalf("Activity() ok = true, want no entry after Forget")
	}
}
