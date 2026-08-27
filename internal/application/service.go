package application

type Service struct {
	meetings  MeetingRepository
	events    EventRepository
	publisher Publisher
}

type JoinToken struct {
	Token     string
	TokenType string
	ExpiresAt string
}

func NewService(
	meetings MeetingRepository,
	events EventRepository,
	publisher Publisher,
) *Service {
	return &Service{
		meetings: meetings, events: events,
		publisher: publisher,
	}
}
