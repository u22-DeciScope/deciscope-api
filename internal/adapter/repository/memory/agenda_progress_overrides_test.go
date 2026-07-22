package memory

import (
	"testing"

	"deciscope-core-api/internal/adapter/repository/contracttest"
	"deciscope-core-api/internal/application"
)

func TestAgendaProgressOverridesStoreContract(t *testing.T) {
	contracttest.RunAgendaProgressOverrides(t, func(t *testing.T) application.MeetingAgendaProgressOverridesRepository {
		t.Helper()
		return NewAgendaProgressOverridesStore()
	})
}
