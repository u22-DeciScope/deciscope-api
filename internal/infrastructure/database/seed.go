package database

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed seed/demo_seed.sql
var demoSeedSQL string

// SeedDemoData は開発用のデモ会議データを冪等に投入する。
// DECISCOPE_SEED_DEMO_DATA が有効なときだけ呼ばれる想定で、本番環境では使用しない。
// SQL はすべて冪等（ON CONFLICT DO NOTHING / 明示 UPDATE）なので、毎回の起動で実行しても安全に
// 不足分（会議・イベント・紐づけ）が補完される。
func SeedDemoData(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, demoSeedSQL); err != nil {
		return fmt.Errorf("seed demo data: %w", err)
	}
	return nil
}
