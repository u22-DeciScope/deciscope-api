package core_test

import (
	"testing"

	"deciscope-core-api/internal/core"
	"deciscope-core-api/internal/repository/contracttest"
)

func TestMemoryRepositoryContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) core.Repositories {
		t.Helper()
		return core.RepositoriesFromMemory(core.NewMemoryStore())
	})
}
