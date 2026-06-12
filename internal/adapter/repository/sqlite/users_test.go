package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestUserRepositoryCreatesAndFindsFirebaseUser(t *testing.T) {
	repository := newTestSQLiteRepository(t)
	ctx := context.Background()

	created, err := repository.FindOrCreateFirebaseUser(ctx, "user@example.com", "First Name")
	if err != nil {
		t.Fatalf("FindOrCreateFirebaseUser() create error = %v", err)
	}
	found, err := repository.FindOrCreateFirebaseUser(ctx, "user@example.com", "Changed Name")
	if err != nil {
		t.Fatalf("FindOrCreateFirebaseUser() find error = %v", err)
	}
	if found.ID != created.ID || found.Name != "First Name" {
		t.Fatalf("found user = %+v, want original user %+v", found, created)
	}
}

func newTestSQLiteRepository(t *testing.T) *UserRepository {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "users.sqlite"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if _, err := db.Exec(`
		CREATE TABLE t_Users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			password TEXT NOT NULL
		)
	`); err != nil {
		if strings.Contains(err.Error(), "go-sqlite3 requires cgo") {
			t.Skipf("sqlite runtime requires CGO: %v", err)
		}
		t.Fatalf("create users table: %v", err)
	}
	return NewUserRepository(db)
}
