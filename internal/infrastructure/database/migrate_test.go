package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateSQLiteIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Config{Driver: "sqlite", URL: filepath.Join(t.TempDir(), "test.sqlite")})
	if err != nil {
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

	var legacyTableExists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 't_Users')`).Scan(&legacyTableExists); err != nil {
		t.Fatalf("check legacy table: %v", err)
	}
	if legacyTableExists {
		t.Fatal("legacy t_Users table still exists")
	}
}

func TestBindPlaceholders(t *testing.T) {
	query := "SELECT * FROM schema_migrations WHERE version = ? OR version = ?"
	if got := bindPlaceholders("sqlite", query); got != query {
		t.Fatalf("sqlite query = %q, want unchanged", got)
	}
	want := "SELECT * FROM schema_migrations WHERE version = $1 OR version = $2"
	if got := bindPlaceholders("postgres", query); got != want {
		t.Fatalf("postgres query = %q, want %q", got, want)
	}
}
