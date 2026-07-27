package memory

import (
	"sync"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

type MemoryStore struct {
	mu       sync.Mutex
	meetings map[string]domain.Meeting
	nextSeq  map[string]int64
	events   map[string][]domain.Event
	segments map[string][]domain.Segment
	jobs     map[string]domain.Job
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		meetings: make(map[string]domain.Meeting), nextSeq: make(map[string]int64),
		events: make(map[string][]domain.Event), segments: make(map[string][]domain.Segment),
		jobs: make(map[string]domain.Job),
	}
}

var _ application.MeetingRepository = (*MemoryStore)(nil)
var _ application.EventRepository = (*MemoryStore)(nil)
var _ application.JobRepository = (*MemoryStore)(nil)
