package database

import (
	"context"
	"encoding/json"
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

func TestMigration00014NormalizesLegacyIssueKindsIdempotently(t *testing.T) {
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
	const sessionID = "session_migration_00014"
	if _, err := db.ExecContext(ctx, `DELETE FROM meeting_session_ai_analyses WHERE session_id=$1`, sessionID); err != nil {
		t.Fatalf("clear migration fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM meeting_session_ai_analyses WHERE session_id=$1`, sessionID)
	})
	legacyPayload, err := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"id": "open-1", "kind": "open_issue", "status": "open", "evidenceSequenceNos": []int64{4}},
			{"id": "question-1", "kind": "question", "status": "resolved", "evidenceSequenceNos": []int64{5}},
			{"id": "todo-1", "kind": "todo", "status": "open", "evidenceSequenceNos": []int64{6}},
		},
		"tree": map[string]any{"nodes": []map[string]any{
			{"id": "root", "kind": "topic"},
			{"id": "open-1", "kind": "open_issue", "status": "open"},
			{"id": "question-1", "kind": "question", "status": "resolved"},
		}},
	})
	if err != nil {
		t.Fatalf("marshal legacy analysis: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO meeting_session_ai_analyses (session_id, analysis_type, status, version, payload)
		VALUES ($1, 'live', 'completed', 7, $2)
	`, sessionID, legacyPayload); err != nil {
		t.Fatalf("seed legacy analysis: %v", err)
	}
	upSQL, err := migrationFiles.ReadFile("migrations/postgres/00014_issue_subtypes.up.sql")
	if err != nil {
		t.Fatalf("read 00014 up migration: %v", err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := db.ExecContext(ctx, string(upSQL)); err != nil {
			t.Fatalf("apply 00014 attempt %d: %v", attempt, err)
		}
	}
	var openKind, openSubtype, questionKind, questionSubtype, questionStatus, todoKind, openNodeSubtype string
	var openID string
	var evidence int64
	if err := db.QueryRowContext(ctx, `
		SELECT payload #>> '{items,0,id}', payload #>> '{items,0,kind}', payload #>> '{items,0,subtype}',
			(payload #> '{items,0,evidenceSequenceNos,0}')::bigint,
			payload #>> '{items,1,kind}', payload #>> '{items,1,subtype}', payload #>> '{items,1,status}',
			payload #>> '{items,2,kind}', payload #>> '{tree,nodes,1,subtype}'
		FROM meeting_session_ai_analyses WHERE session_id=$1 AND analysis_type='live'
	`, sessionID).Scan(&openID, &openKind, &openSubtype, &evidence, &questionKind, &questionSubtype, &questionStatus, &todoKind, &openNodeSubtype); err != nil {
		t.Fatalf("read normalized analysis: %v", err)
	}
	if openID != "open-1" || evidence != 4 || openKind != "issue" || openSubtype != "discussion" || questionKind != "issue" || questionSubtype != "question" || questionStatus != "resolved" || todoKind != "todo" || openNodeSubtype != "discussion" {
		t.Fatalf("normalized values id=%q evidence=%d open=%s/%s question=%s/%s/%s todo=%s nodeSubtype=%s", openID, evidence, openKind, openSubtype, questionKind, questionSubtype, questionStatus, todoKind, openNodeSubtype)
	}
}
