package domain

import (
	"errors"
	"testing"
)

func TestDurableEventTypes(t *testing.T) {
	if !IsDurableEventType(EventTranscriptFinal) {
		t.Fatal("transcript.final must be durable")
	}
	if IsDurableEventType(EventTranscriptPartial) {
		t.Fatal("transcript.partial must remain ephemeral")
	}
}

func TestNormalizeFixtureName(t *testing.T) {
	if got := NormalizeFixtureName(`\nested\demo.jsonl`); got != "nested/demo.jsonl" {
		t.Fatalf("NormalizeFixtureName() = %q", got)
	}
	if got := NormalizeFixtureName(""); got != "demo.jsonl" {
		t.Fatalf("empty fixture = %q", got)
	}
}

func TestNormalizeTeamsJoinURL(t *testing.T) {
	got, err := NormalizeTeamsJoinURL(" https://teams.microsoft.com/l/meetup-join/abc ")
	if err != nil {
		t.Fatalf("NormalizeTeamsJoinURL() error = %v", err)
	}
	if got != "https://teams.microsoft.com/l/meetup-join/abc" {
		t.Fatalf("NormalizeTeamsJoinURL() = %q", got)
	}
	got, err = NormalizeTeamsJoinURL("https://TEAMS.MICROSOFT.COM/l/meetup-join/abc///#ignored")
	if err != nil {
		t.Fatalf("NormalizeTeamsJoinURL() error = %v", err)
	}
	if got != "https://teams.microsoft.com/l/meetup-join/abc" {
		t.Fatalf("NormalizeTeamsJoinURL() = %q", got)
	}

	for _, value := range []string{
		"",
		"http://teams.microsoft.com/l/meetup-join/abc",
		"https://example.com/l/meetup-join/abc",
		"https://teams.microsoft.com/not-a-meeting",
	} {
		if _, err := NormalizeTeamsJoinURL(value); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("NormalizeTeamsJoinURL(%q) error = %v, want invalid argument", value, err)
		}
	}
}
