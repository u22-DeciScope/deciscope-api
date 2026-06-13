package sqlite_test

import (
	"context"
	"strings"
	"testing"

	"deciscope-core-api/internal/adapter/repository/contracttest"
	"deciscope-core-api/internal/adapter/repository/sqlite"
	"deciscope-core-api/internal/infrastructure/database"
)

func TestRepositoryContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) contracttest.Repositories {
		t.Helper()
		db, err := database.Open(context.Background(), database.Config{
			Driver: "sqlite",
			URL:    t.TempDir() + "/contract.sqlite",
		})
		if err != nil {
			if strings.Contains(err.Error(), "go-sqlite3 requires cgo") {
				t.Skipf("sqlite runtime requires CGO: %v", err)
			}
			t.Fatalf("database.Open() error = %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := database.Migrate(context.Background(), db, "sqlite"); err != nil {
			t.Fatalf("database.Migrate() error = %v", err)
		}
		repos := contracttest.FromStore(sqlite.NewStore(db))
		repos.Auth = sqlite.NewAuthWorkspaceRepository(db)
		return repos
	})
}
