package mapper

import (
	"testing"

	"deciscope-core-api/internal/contract"
)

func TestMapVerifiedIdentity(t *testing.T) {
	identity, seed := MapVerifiedIdentity(contract.VerifiedIdentity{
		Provider:    "firebase",
		Subject:     "uid-123",
		Email:       " user@example.com ",
		DisplayName: " display ",
		AvatarURL:   " https://example.com/a.png ",
	})

	if identity.Provider != "firebase" {
		t.Fatalf("expected provider to be preserved, got %q", identity.Provider)
	}
	if identity.Subject != "uid-123" {
		t.Fatalf("expected subject to be preserved, got %q", identity.Subject)
	}
	if seed.Email != "user@example.com" {
		t.Fatalf("expected email to be trimmed, got %q", seed.Email)
	}
	if seed.DisplayName != "display" {
		t.Fatalf("expected display name to be trimmed, got %q", seed.DisplayName)
	}
	if seed.AvatarURL != "https://example.com/a.png" {
		t.Fatalf("expected avatar url to be trimmed, got %q", seed.AvatarURL)
	}
}
