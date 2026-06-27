package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

var ErrBotControlNotConfigured = errors.New("bot control is not configured")
var ErrBotControlCommandFailed = errors.New("bot control command failed")

type MeetingSessionService struct {
	repository MeetingSessionRepository
	commander  BotJoinCommander
	publisher  MeetingSessionPublisher
	now        func() time.Time
}

type MeetingSessionStatusUpdateInput struct {
	SessionID string
	Status    domain.MeetingSessionStatus
	BotCallID string
	Message   string
}

func NewMeetingSessionService(repository MeetingSessionRepository, commander BotJoinCommander, publisher ...MeetingSessionPublisher) *MeetingSessionService {
	var statusPublisher MeetingSessionPublisher
	if len(publisher) > 0 {
		statusPublisher = publisher[0]
	}
	return &MeetingSessionService{
		repository: repository,
		commander:  commander,
		publisher:  statusPublisher,
		now:        time.Now,
	}
}

func (s *MeetingSessionService) CreateMeetingSession(ctx context.Context, joinURL string) (*domain.MeetingSession, error) {
	normalizedJoinURL, err := domain.NormalizeTeamsJoinURL(joinURL)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	session := domain.MeetingSession{
		ID:          domain.NewID("session"),
		JoinURL:     normalizedJoinURL,
		JoinURLHash: domain.JoinURLHash(normalizedJoinURL),
		Status:      domain.MeetingSessionPendingJoin,
		RequestedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	created, err := s.repository.CreateMeetingSession(ctx, session)
	if err != nil {
		return nil, err
	}

	if s.commander == nil {
		failed, updateErr := s.markCommandFailed(ctx, created.ID, ErrBotControlNotConfigured)
		if updateErr != nil {
			return created, updateErr
		}
		return failed, ErrBotControlNotConfigured
	}
	if err := s.commander.SendJoinCommand(ctx, BotJoinCommand{SessionID: created.ID, JoinURL: normalizedJoinURL}); err != nil {
		failed, updateErr := s.markCommandFailed(ctx, created.ID, err)
		if updateErr != nil {
			return created, updateErr
		}
		if errors.Is(err, ErrBotControlNotConfigured) {
			return failed, err
		}
		return failed, fmt.Errorf("%w: %v", ErrBotControlCommandFailed, err)
	}

	commandSentAt := s.now().UTC()
	updated, err := s.repository.UpdateMeetingSessionStatus(ctx, domain.MeetingSessionStatusUpdate{
		SessionID:     created.ID,
		Status:        domain.MeetingSessionCommandSent,
		CommandSentAt: &commandSentAt,
		LastError:     "",
		UpdatedAt:     commandSentAt,
	})
	if err != nil {
		return created, err
	}
	s.publishStatusChanged(*updated)
	return updated, nil
}

func (s *MeetingSessionService) GetMeetingSession(ctx context.Context, sessionID string) (*domain.MeetingSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: sessionId is required", domain.ErrInvalidArgument)
	}
	return s.repository.GetMeetingSession(ctx, sessionID)
}

func (s *MeetingSessionService) UpdateMeetingSessionStatus(ctx context.Context, input MeetingSessionStatusUpdateInput) (*domain.MeetingSession, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: sessionId is required", domain.ErrInvalidArgument)
	}
	status := domain.MeetingSessionStatus(strings.TrimSpace(string(input.Status)))
	if !domain.ValidMeetingSessionStatus(status) {
		return nil, fmt.Errorf("%w: status is invalid", domain.ErrInvalidArgument)
	}

	now := s.now().UTC()
	update := domain.MeetingSessionStatusUpdate{
		SessionID: sessionID,
		Status:    status,
		BotCallID: strings.TrimSpace(input.BotCallID),
		UpdatedAt: now,
	}
	switch status {
	case domain.MeetingSessionCommandSent:
		update.CommandSentAt = &now
	case domain.MeetingSessionJoined, domain.MeetingSessionRecording:
		update.JoinedAt = &now
	case domain.MeetingSessionEnded:
		update.EndedAt = &now
	case domain.MeetingSessionFailed:
		update.LastError = strings.TrimSpace(input.Message)
	}
	updated, err := s.repository.UpdateMeetingSessionStatus(ctx, update)
	if err != nil {
		return nil, err
	}
	s.publishStatusChanged(*updated)
	return updated, nil
}

func (s *MeetingSessionService) markCommandFailed(ctx context.Context, sessionID string, cause error) (*domain.MeetingSession, error) {
	now := s.now().UTC()
	failed, err := s.repository.UpdateMeetingSessionStatus(ctx, domain.MeetingSessionStatusUpdate{
		SessionID: sessionID,
		Status:    domain.MeetingSessionFailed,
		LastError: summarizeBotControlError(cause),
		UpdatedAt: now,
	})
	if err != nil {
		return nil, err
	}
	s.publishStatusChanged(*failed)
	return failed, nil
}

func (s *MeetingSessionService) publishStatusChanged(session domain.MeetingSession) {
	if s.publisher != nil {
		s.publisher.PublishMeetingSessionStatusChanged(session)
	}
}

func summarizeBotControlError(err error) string {
	if errors.Is(err, ErrBotControlNotConfigured) {
		return "bot control is not configured"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "bot control command failed"
	}
	if len(message) > 300 {
		return message[:300]
	}
	return message
}
