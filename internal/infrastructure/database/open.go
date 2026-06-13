package database

import (
	"context"
	"database/sql"
	"fmt"
)

func Open(ctx context.Context, config Config) (*sql.DB, error) {
	switch config.Driver {
	case "sqlite":
		return openSQLite(ctx, config.URL)
	default:
		return nil, fmt.Errorf("unsupported database driver %q", config.Driver)
	}
}
