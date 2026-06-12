package application

import "deciscope-core-api/internal/domain"

type Service struct {
	meetings  MeetingRepository
	events    EventRepository
	reports   ReportRepository
	jobs      JobRepository
	uploads   UploadRepository
	publisher Publisher
	storage   ObjectStorage
}

type JoinToken struct {
	Token     string
	TokenType string
	ExpiresAt string
}

type UploadResult struct {
	Upload *domain.Upload
	Job    *domain.Job
}

func NewService(
	meetings MeetingRepository,
	events EventRepository,
	reports ReportRepository,
	jobs JobRepository,
	uploads UploadRepository,
	publisher Publisher,
	storage ObjectStorage,
) *Service {
	return &Service{
		meetings: meetings, events: events, reports: reports, jobs: jobs,
		uploads: uploads, publisher: publisher, storage: storage,
	}
}
