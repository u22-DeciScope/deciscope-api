package postgres

import (
	"context"
	"testing"

	appauth "deciscope-core-api/internal/application/auth"
)

func TestAuthWorkspaceRepositoryCreatesAndFindsIdentity(t *testing.T) {
	store := newTestStore(t)
	repository := NewAuthWorkspaceRepository(store.db)
	ctx := context.Background()

	created, err := repository.FindOrCreateUser(ctx, appauth.Identity{UID: "firebase-uid", Email: "user@example.com", Name: "First Name"})
	if err != nil {
		t.Fatalf("FindOrCreateUser() create error = %v", err)
	}
	found, err := repository.FindOrCreateUser(ctx, appauth.Identity{UID: "firebase-uid", Email: "user@example.com", Name: "Changed Name"})
	if err != nil {
		t.Fatalf("FindOrCreateUser() find error = %v", err)
	}
	if found.ID != created.ID || found.DisplayName != "First Name" {
		t.Fatalf("found user = %+v, want original user %+v", found, created)
	}
}
