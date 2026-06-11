package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/sqlite/*.sql
var migrationFiles embed.FS

func Migrate(ctx context.Context, db *sql.DB, driver string) error {
	entries, err := fs.Glob(migrationFiles, "migrations/"+driver+"/*.sql")
	if err != nil {
		return fmt.Errorf("list %s migrations: %w", driver, err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no migrations found for driver %q", driver)
	}
	sort.Strings(entries)

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	for _, path := range entries {
		if err := applyMigration(ctx, db, path); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, path string) error {
	var applied bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)
	`, path).Scan(&applied); err != nil {
		return fmt.Errorf("check migration %s: %w", path, err)
	}
	if applied {
		return nil
	}

	sqlBytes, err := migrationFiles.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", path, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", path, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("apply migration %s: %w", path, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version) VALUES (?)
	`, path); err != nil {
		return fmt.Errorf("record migration %s: %w", path, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", path, err)
	}
	return nil
}
