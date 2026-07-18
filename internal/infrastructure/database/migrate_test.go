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

func TestMigration00013UpgradesExistingAuditRowsAndCanReapply(t *testing.T) {
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
		t.Fatalf("Migrate() initial error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO meeting_sessions (id, join_url, join_url_hash, status, requested_at, created_at, updated_at)
		VALUES ('session_migration_00013', 'https://example.test/migration', 'migration-00013', 'ended', now(), now(), now())
		ON CONFLICT (id) DO NOTHING
	`); err != nil {
		t.Fatalf("seed pre-00013 meeting session: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO meeting_tree_audit_runs (
			id, session_id, based_on_tree_version, trigger_reason, trigger_class,
			task, deployment, prompt_version, snapshot_hash, status, result, disposition
		) VALUES (
			'audit_migration_00013', 'session_migration_00013', 7, 'migration_test', 'normal',
			'tree_audit', 'test-deployment', 'v3', 'snapshot-7', 'completed', 'no_safe_operations', 'rejected'
		) ON CONFLICT (id) DO NOTHING
	`); err != nil {
		t.Fatalf("seed existing audit run: %v", err)
	}

	const upPath = "migrations/postgres/00013_meeting_tree_audit_result_classification.up.sql"
	downSQL, err := migrationFiles.ReadFile("migrations/postgres/00013_meeting_tree_audit_result_classification.down.sql")
	if err != nil {
		t.Fatalf("read 00013 down migration: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin 00013 rollback: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply 00013 down migration: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=$1`, upPath); err != nil {
		t.Fatalf("unrecord 00013 migration: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit 00013 rollback: %v", err)
	}
	var existingRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM meeting_tree_audit_runs WHERE id='audit_migration_00013'`).Scan(&existingRows); err != nil || existingRows != 1 {
		t.Fatalf("existing audit row after down = %d err=%v", existingRows, err)
	}
	var classification string
	if err := db.QueryRowContext(ctx, `SELECT result_classification FROM meeting_tree_audit_runs WHERE id='audit_migration_00013'`).Scan(&classification); err == nil {
		t.Fatal("result_classification unexpectedly remained after 00013 down")
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() existing-schema upgrade error = %v", err)
	}
	var proposed, canonicalized, valid, applied, rejected, consecutive int
	if err := db.QueryRowContext(ctx, `
		SELECT result_classification, operations_proposed, operations_canonicalized,
			operations_valid, operations_applied, operations_rejected, consecutive_unapplied_runs
		FROM meeting_tree_audit_runs WHERE id='audit_migration_00013'
	`).Scan(&classification, &proposed, &canonicalized, &valid, &applied, &rejected, &consecutive); err != nil {
		t.Fatalf("read upgraded existing audit run: %v", err)
	}
	if classification != "" || proposed != 0 || canonicalized != 0 || valid != 0 || applied != 0 || rejected != 0 || consecutive != 0 {
		t.Fatalf("upgraded audit defaults = classification:%q proposed:%d canonicalized:%d valid:%d applied:%d rejected:%d consecutive:%d",
			classification, proposed, canonicalized, valid, applied, rejected, consecutive)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() after 00013 reapply error = %v", err)
	}
}
