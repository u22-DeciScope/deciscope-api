package memory_test

import (
	"testing"

	"deciscope-core-api/internal/adapter/repository/contracttest"
	"deciscope-core-api/internal/adapter/repository/memory"
	"deciscope-core-api/internal/application"
)

func TestMemoryRepositoryContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) application.Repositories {
		t.Helper()
		return memory.Repositories(memory.NewMemoryStore())
	})
}
