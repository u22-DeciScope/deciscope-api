package application

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

var ErrBotControlNotConfigured = errors.New("bot control is not configured")
var ErrBotControlCommandFailed = errors.New("bot control command failed")

const DefaultMeetingSessionStaleAfter = 12 * time.Hour

type MeetingSessionService struct {
	repository MeetingSessionRepository
	commander  BotJoinCommander
	publisher  MeetingSessionPublisher
	now        func() time.Time
}

type MeetingSessionCreateResult struct {
	Session *domain.MeetingSession
	Reused  bool
}

type MeetingSessionStatusUpdateInput struct {
	SessionID string
	Status    domain.MeetingSessionStatus
	BotCallID string
	Message   string
	Reason    string
	ErrorCode string
	Source    string
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

func (s *MeetingSessionService) CreateMeetingSession(ctx context.Context, joinURL string) (*MeetingSessionCreateResult, error) {
	normalizedJoinURL, err := domain.NormalizeTeamsJoinURL(joinURL)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if stale, cleanupErr := s.CleanupStaleMeetingSessions(ctx); cleanupErr != nil {
		log.Printf("Meeting session stale cleanup failed before create. error=%v", cleanupErr)
	} else if len(stale) > 0 {
		log.Printf("Meeting session stale cleanup completed before create. count=%d", len(stale))
	}
	session := domain.MeetingSession{
		ID:          domain.NewID("session"),
		JoinURL:     normalizedJoinURL,
		JoinURLHash: domain.JoinURLHash(normalizedJoinURL),
		Status:      domain.MeetingSessionRequested,
		RequestedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	created, isNew, err := s.repository.CreateOrReuseMeetingSession(ctx, session)
	if err != nil {
		return nil, err
	}
	if !isNew {
		log.Printf("Meeting session reuse. sessionId=%s joinUrlHash=%s status=%s createdAt=%s updatedAt=%s", created.ID, created.JoinURLHash, created.Status, created.CreatedAt.UTC().Format(time.RFC3339Nano), created.UpdatedAt.UTC().Format(time.RFC3339Nano))
		return &MeetingSessionCreateResult{Session: created, Reused: true}, nil
	}
	log.Printf("Meeting session create. sessionId=%s joinUrlHash=%s status=%s createdAt=%s", created.ID, created.JoinURLHash, created.Status, created.CreatedAt.UTC().Format(time.RFC3339Nano))

	if s.commander == nil {
		failed, updateErr := s.markCommandFailed(ctx, created.ID, ErrBotControlNotConfigured)
		if updateErr != nil {
			return &MeetingSessionCreateResult{Session: created}, updateErr
		}
		return &MeetingSessionCreateResult{Session: failed}, ErrBotControlNotConfigured
	}
	if err := s.commander.SendJoinCommand(ctx, BotJoinCommand{SessionID: created.ID, JoinURL: normalizedJoinURL}); err != nil {
		failed, updateErr := s.markCommandFailed(ctx, created.ID, err)
		if updateErr != nil {
			return &MeetingSessionCreateResult{Session: created}, updateErr
		}
		if errors.Is(err, ErrBotControlNotConfigured) {
			return &MeetingSessionCreateResult{Session: failed}, err
		}
		return &MeetingSessionCreateResult{Session: failed}, fmt.Errorf("%w: %v", ErrBotControlCommandFailed, err)
	}

	commandSentAt := s.now().UTC()
	updated, err := s.repository.UpdateMeetingSessionStatus(ctx, domain.MeetingSessionStatusUpdate{
		SessionID:     created.ID,
		Status:        domain.MeetingSessionJoining,
		CommandSentAt: &commandSentAt,
		LastError:     "",
		UpdatedAt:     commandSentAt,
	})
	if err != nil {
		return &MeetingSessionCreateResult{Session: created}, err
	}
	log.Printf("Meeting session join command sent. sessionId=%s joinUrlHash=%s status=%s", updated.ID, updated.JoinURLHash, updated.Status)
	s.publishStatusChanged(*updated)
	return &MeetingSessionCreateResult{Session: updated}, nil
}

func (s *MeetingSessionService) GetMeetingSession(ctx context.Context, sessionID string) (*domain.MeetingSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: sessionId is required", domain.ErrInvalidArgument)
	}
	session, err := s.repository.GetMeetingSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	log.Printf("Meeting session fetched. sessionId=%s joinUrlHash=%s status=%s botCallId=%s updatedAt=%s", session.ID, session.JoinURLHash, session.Status, session.BotCallID, session.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return session, nil
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

	previous, previousErr := s.repository.GetMeetingSession(ctx, sessionID)
	if previousErr != nil {
		log.Printf("Meeting session status update could not read previous state. sessionId=%s newStatus=%s botCallId=%s error=%v", sessionID, status, strings.TrimSpace(input.BotCallID), previousErr)
	}
	if previousErr == nil && previous != nil && shouldSuppressMeetingSessionFailure(*previous, input) {
		log.Printf("Meeting session failed status suppressed. sessionId=%s joinUrlHash=%s oldStatus=%s requestedStatus=%s botCallId=%s reason=%s errorCode=%s source=%s message=%s",
			previous.ID, previous.JoinURLHash, previous.Status, status, strings.TrimSpace(input.BotCallID), strings.TrimSpace(input.Reason), strings.TrimSpace(input.ErrorCode), strings.TrimSpace(input.Source), strings.TrimSpace(input.Message))
		return previous, nil
	}

	now := s.now().UTC()
	update := domain.MeetingSessionStatusUpdate{
		SessionID: sessionID,
		Status:    status,
		BotCallID: strings.TrimSpace(input.BotCallID),
		UpdatedAt: now,
	}
	switch status {
	case domain.MeetingSessionCommandSent, domain.MeetingSessionJoining:
		update.CommandSentAt = &now
	case domain.MeetingSessionJoined, domain.MeetingSessionActive, domain.MeetingSessionRecording:
		update.JoinedAt = &now
	case domain.MeetingSessionEnded, domain.MeetingSessionStale:
		update.EndedAt = &now
	case domain.MeetingSessionFailed:
		update.LastError = summarizeMeetingSessionFailure(input)
	}
	updated, err := s.repository.UpdateMeetingSessionStatus(ctx, update)
	if err != nil {
		return nil, err
	}
	oldStatus := domain.MeetingSessionStatus("unknown")
	if previousErr == nil && previous != nil {
		oldStatus = previous.Status
	}
	log.Printf("Meeting session status changed. sessionId=%s joinUrlHash=%s oldStatus=%s newStatus=%s botCallId=%s updatedAt=%s", updated.ID, updated.JoinURLHash, oldStatus, updated.Status, updated.BotCallID, updated.UpdatedAt.UTC().Format(time.RFC3339Nano))
	s.publishStatusChanged(*updated)
	return updated, nil
}

func shouldSuppressMeetingSessionFailure(previous domain.MeetingSession, input MeetingSessionStatusUpdateInput) bool {
	if input.Status != domain.MeetingSessionFailed {
		return false
	}
	if !isJoinedOrBeyondMeetingStatus(previous.Status) {
		return false
	}
	return !isFatalMeetingFailure(input)
}

func isJoinedOrBeyondMeetingStatus(status domain.MeetingSessionStatus) bool {
	switch status {
	case domain.MeetingSessionJoined, domain.MeetingSessionActive, domain.MeetingSessionRecording:
		return true
	default:
		return false
	}
}

func isFatalMeetingFailure(input MeetingSessionStatusUpdateInput) bool {
	text := strings.ToLower(strings.Join([]string{
		input.Reason,
		input.ErrorCode,
		input.Source,
		input.Message,
	}, " "))
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, marker := range []string{
		"speech",
		"transcription",
		"recognizer",
		"pipeline",
		"acceptingframes",
		"accepting_frames",
		"silent frame",
		"audio",
		"not_ready",
		"not ready",
	} {
		if strings.Contains(text, marker) {
			return false
		}
	}
	for _, marker := range []string{
		"call_disconnected",
		"call disconnected",
		"graph_call_disconnected",
		"graph call disconnected",
		"call_terminated",
		"call terminated",
		"call ended",
		"graph_call_ended",
		"permission_denied",
		"permission denied",
		"meeting_not_found",
		"meeting not found",
		"graph_join_failed",
		"join_failed",
		"fatal",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func summarizeMeetingSessionFailure(input MeetingSessionStatusUpdateInput) string {
	parts := make([]string, 0, 4)
	if reason := strings.TrimSpace(input.Reason); reason != "" {
		parts = append(parts, "reason="+reason)
	}
	if errorCode := strings.TrimSpace(input.ErrorCode); errorCode != "" {
		parts = append(parts, "errorCode="+errorCode)
	}
	if source := strings.TrimSpace(input.Source); source != "" {
		parts = append(parts, "source="+source)
	}
	if message := strings.TrimSpace(input.Message); message != "" {
		parts = append(parts, "message="+message)
	}
	if len(parts) == 0 {
		return ""
	}
	summary := strings.Join(parts, " ")
	if len(summary) > 300 {
		return summary[:300]
	}
	return summary
}

func (s *MeetingSessionService) CleanupStaleMeetingSessions(ctx context.Context) ([]domain.MeetingSession, error) {
	now := s.now().UTC()
	staleBefore := now.Add(-DefaultMeetingSessionStaleAfter)
	staleSessions, err := s.repository.MarkStaleMeetingSessions(ctx, staleBefore, now)
	if err != nil {
		return nil, err
	}
	for _, session := range staleSessions {
		log.Printf("Meeting session marked stale. sessionId=%s joinUrlHash=%s status=%s updatedAt=%s staleBefore=%s", session.ID, session.JoinURLHash, session.Status, session.UpdatedAt.UTC().Format(time.RFC3339Nano), staleBefore.Format(time.RFC3339Nano))
		s.publishStatusChanged(session)
	}
	return staleSessions, nil
}

func (s *MeetingSessionService) ListMeetingSessionDebug(ctx context.Context, limit int) ([]domain.MeetingSessionDebug, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	return s.repository.ListMeetingSessionDebug(ctx, limit)
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
