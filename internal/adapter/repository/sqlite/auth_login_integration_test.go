package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	sqliterepository "deciscope-core-api/internal/adapter/repository/sqlite"
	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/infrastructure/database"
)

type loginVerifier struct {
	identity *appauth.Identity
}

func (v loginVerifier) VerifyIDToken(context.Context, string) (*appauth.Identity, error) {
	return v.identity, nil
}

func TestLoginPersistsAccountToSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Driver: "sqlite",
		URL:    filepath.Join(t.TempDir(), "auth.sqlite"),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	service := appauth.NewService(
		sqliterepository.NewAuthWorkspaceRepository(db),
		loginVerifier{identity: &appauth.Identity{
			UID: "firebase-uid", Email: "user@example.com", Name: "User", Provider: "microsoft.com",
		}},
		time.Hour,
	)
	if _, err := service.Login(ctx, "token"); err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	for _, table := range []string{"users", "user_identities", "user_emails", "user_sessions"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", table, count)
		}
	}
}
