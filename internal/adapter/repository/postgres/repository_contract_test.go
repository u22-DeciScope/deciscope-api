package postgres

import (
	"context"
	"os"
	"testing"

	"deciscope-core-api/internal/adapter/repository/contracttest"
	"deciscope-core-api/internal/infrastructure/database"
)

func TestRepositoryContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) contracttest.Repositories {
		t.Helper()
		databaseURL := os.Getenv("DATABASE_TEST_URL")
		if databaseURL == "" {
			t.Skip("DATABASE_TEST_URL is not set")
		}
		db, err := database.Open(context.Background(), database.Config{URL: databaseURL})
		if err != nil {
			t.Fatalf("database.Open() error = %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := database.Migrate(context.Background(), db); err != nil {
			t.Fatalf("database.Migrate() error = %v", err)
		}
		resetTestDatabase(t, db)
		repos := contracttest.FromStore(NewStore(db))
		repos.Auth = NewAuthWorkspaceRepository(db)
		return repos
	})
}
