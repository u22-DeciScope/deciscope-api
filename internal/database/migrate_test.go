package database

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateSQLiteIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Config{Driver: "sqlite", URL: filepath.Join(t.TempDir(), "test.sqlite")})
	if err != nil {
		if strings.Contains(err.Error(), "go-sqlite3 requires cgo") {
			t.Skipf("sqlite runtime requires CGO: %v", err)
		}
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatalf("Migrate() first error = %v", err)
	}
	if err := Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatalf("Migrate() second error = %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("migration count = %d, want 1", count)
	}
}
