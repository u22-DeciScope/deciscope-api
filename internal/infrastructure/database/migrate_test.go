package database

import (
	"context"
	"os"
	"testing"
)

func TestMigratePostgresIsIdempotent(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, Config{URL: databaseURL})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() first error = %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() second error = %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	paths, err := applicableMigrationPaths()
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if count != len(paths) {
		t.Fatalf("migration count = %d, want %d", count, len(paths))
	}

}
