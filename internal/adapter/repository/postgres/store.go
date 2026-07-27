package postgres

import (
	"database/sql"

	"deciscope-core-api/internal/application"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

var _ application.MeetingRepository = (*Store)(nil)
var _ application.EventRepository = (*Store)(nil)
var _ application.JobRepository = (*Store)(nil)
