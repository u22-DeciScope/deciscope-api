package domain

import "testing"

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
