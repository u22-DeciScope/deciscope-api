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
	reports  map[string][]domain.Report
	jobs     map[string]domain.Job
	uploads  map[string]domain.Upload
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		meetings: make(map[string]domain.Meeting), nextSeq: make(map[string]int64),
		events: make(map[string][]domain.Event), segments: make(map[string][]domain.Segment),
		reports: make(map[string][]domain.Report), jobs: make(map[string]domain.Job),
		uploads: make(map[string]domain.Upload),
	}
}

var _ application.MeetingRepository = (*MemoryStore)(nil)
var _ application.EventRepository = (*MemoryStore)(nil)
var _ application.ReportRepository = (*MemoryStore)(nil)
var _ application.JobRepository = (*MemoryStore)(nil)
var _ application.UploadRepository = (*MemoryStore)(nil)
