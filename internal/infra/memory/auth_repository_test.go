package memory

import (
	"context"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestCreateUserWithIdentityStoresAnchorUser(t *testing.T) {
	repository := NewAuthRepository()

	user, err := repository.CreateUserWithIdentity(context.Background(), domain.IdentityInput{
		Provider: "firebase",
		Subject:  "uid-123",
	}, domain.UserSeed{
		Email:       "user@example.com",
		DisplayName: "display",
		AvatarURL:   "https://example.com/a.png",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if user.ID == "" {
		t.Fatal("expected created user id")
	}
	if user.Status != domain.UserStatusActive {
		t.Fatalf("expected active status, got %q", user.Status)
	}
	if user.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set")
	}
	if user.UpdatedAt.IsZero() {
		t.Fatal("expected updated_at to be set")
	}

	stored, found, err := repository.FindUserByIdentity(context.Background(), domain.IdentityInput{
		Provider: "firebase",
		Subject:  "uid-123",
	})
	if err != nil {
		t.Fatalf("expected no lookup error, got %v", err)
	}
	if !found {
		t.Fatal("expected stored user to be found")
	}
	if stored.ID != user.ID {
		t.Fatalf("expected stored user id %q, got %q", user.ID, stored.ID)
	}
}
