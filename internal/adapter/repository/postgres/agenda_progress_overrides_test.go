package postgres

import (
	"context"
	"database/sql"
	"testing"

	"deciscope-core-api/internal/adapter/repository/contracttest"
	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/infrastructure/database"
)

func TestAgendaProgressOverridesRepositoryContract(t *testing.T) {
	contracttest.RunAgendaProgressOverrides(t, func(t *testing.T) application.MeetingAgendaProgressOverridesRepository {
		t.Helper()
		repository, _ := newTestAgendaProgressOverridesRepository(t)
		return repository
	})
}

func newTestAgendaProgressOverridesRepository(t *testing.T) (*MeetingAgendaProgressOverridesRepository, *sql.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), database.Config{URL: testDatabaseURL(t)})
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	resetTestDatabase(t, db)
	return NewMeetingAgendaProgressOverridesRepository(db), db
}
