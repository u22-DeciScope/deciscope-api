package memory_test

import (
	"testing"

	"deciscope-core-api/internal/adapter/repository/contracttest"
	"deciscope-core-api/internal/adapter/repository/memory"
)

func TestMemoryRepositoryContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) contracttest.Repositories {
		t.Helper()
		store := memory.NewMemoryStore()
		repos := contracttest.FromStore(store)
		repos.Auth = memory.NewAuthWorkspaceRepository(store)
		return repos
	})
}
