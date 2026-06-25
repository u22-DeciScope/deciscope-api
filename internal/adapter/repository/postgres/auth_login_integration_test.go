package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	postgresrepository "deciscope-core-api/internal/adapter/repository/postgres"
	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/infrastructure/database"
)

type loginVerifier struct {
	identity *appauth.Identity
}

func (v loginVerifier) VerifyIDToken(context.Context, string) (*appauth.Identity, error) {
	return v.identity, nil
}

func TestLoginPersistsAccountToPostgreSQL(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set")
	}
	db, err := database.Open(ctx, database.Config{URL: databaseURL})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if _, err := db.Exec(`
		TRUNCATE TABLE uploads, jobs, meeting_reports, meeting_segments, meeting_events,
			meetings, user_sessions, workspace_invitations, workspace_members, workspaces,
			user_emails, user_identities, users RESTART IDENTITY CASCADE
	`); err != nil {
		t.Fatalf("reset test database: %v", err)
	}

	service := appauth.NewService(
		postgresrepository.NewAuthWorkspaceRepository(db),
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
