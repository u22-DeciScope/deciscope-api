package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open(ctx context.Context, config Config) (*sql.DB, error) {
	if config.Path == "" {
		return nil, fmt.Errorf("DECISCOPE_GO_SQLITE_PATH is required")
	}
	if dir := filepath.Dir(config.Path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create sqlite directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", config.Path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set sqlite busy_timeout: %w", err)
	}
	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL;").Scan(&journalMode); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set sqlite journal_mode: %w", err)
	}
	return db, nil
}
