package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

var ErrBotControlNotConfigured = errors.New("bot control is not configured")
var ErrBotControlCommandFailed = errors.New("bot control command failed")

const DefaultMeetingSessionStaleAfter = 2 * time.Hour
const defaultMeetingSessionTitle = "Teams会議"

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

type MeetingSessionCreateInput struct {
	WorkspaceID                 string
	CreatedByUserID             string
	MeetingID                   string
	JoinURL                     string
	Title                       string
	UserProvidedTitle           string
	CandidateUserIDs            []string
	CandidateUserPrincipalNames []string
	CreatedByMicrosoftUserID    string
	CreatedByEmail              string
	OrganizerUserID             string
	Purpose                     string
	Context                     string
	Agenda                      string
	DecisionPoints              string
	Concerns                    string
	ExpectedOutput              string
	CustomInstruction           string
}

type MeetingSessionStatusUpdateInput struct {
	SessionID   string
	Status      domain.MeetingSessionStatus
	BotCallID   string
	Message     string
	Reason      string
	ErrorCode   string
	Source      string
	Title       string
	TitleSource string
}

type MeetingSessionEndInput struct {
	SessionID string
	Reason    string
}

type MeetingSessionMetadataUpdateInput struct {
	SessionID                   string
	Title                       string
	TitleSource                 string
	UserProvidedTitle           string
	GraphTitle                  string
	Provider                    string
	ExternalMeetingID           string
	JoinMeetingID               string
	JoinWebURL                  string
	CanonicalJoinWebURL         string
	ThreadID                    string
	OrganizerID                 string
	OrganizerName               string
	OrganizerEmail              string
	ScheduledStartAt            string
	ScheduledEndAt              string
	TitleResolutionErrorCode    string
	TitleResolutionErrorMessage string
	TitleResolvedAt             string
	Purpose                     string
	Context                     string
	Agenda                      string
	DecisionPoints              string
	Concerns                    string
	ExpectedOutput              string
	CustomInstruction           string
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

func (s *MeetingSessionService) CreateMeetingSession(ctx context.Context, input MeetingSessionCreateInput) (*MeetingSessionCreateResult, error) {
	normalizedJoinURL, err := domain.NormalizeTeamsJoinURL(input.JoinURL)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	userProvidedTitle := meetingSessionUserProvidedTitle(input)
	if stale, cleanupErr := s.CleanupStaleMeetingSessions(ctx); cleanupErr != nil {
		log.Printf("Meeting session stale cleanup failed before create. error=%v", cleanupErr)
	} else if len(stale) > 0 {
		log.Printf("Meeting session stale cleanup completed before create. count=%d", len(stale))
	}
	session := domain.MeetingSession{
		ID:                  domain.NewID("session"),
		WorkspaceID:         strings.TrimSpace(input.WorkspaceID),
		CreatedByUserID:     strings.TrimSpace(input.CreatedByUserID),
		MeetingID:           strings.TrimSpace(input.MeetingID),
		JoinURL:             normalizedJoinURL,
		JoinURLHash:         domain.JoinURLHash(normalizedJoinURL),
		Title:               meetingSessionCreateTitle(userProvidedTitle),
		TitleSource:         meetingSessionCreateTitleSource(userProvidedTitle),
		TitleUpdatedAt:      now,
		UserProvidedTitle:   userProvidedTitle,
		JoinWebURL:          normalizedJoinURL,
		CanonicalJoinWebURL: normalizedJoinURL,
		Provider:            "teams",
		Purpose:             strings.TrimSpace(input.Purpose),
		Context:             strings.TrimSpace(input.Context),
		Agenda:              strings.TrimSpace(input.Agenda),
		DecisionPoints:      strings.TrimSpace(input.DecisionPoints),
		Concerns:            strings.TrimSpace(input.Concerns),
		ExpectedOutput:      strings.TrimSpace(input.ExpectedOutput),
		CustomInstruction:   strings.TrimSpace(input.CustomInstruction),
		Status:              domain.MeetingSessionRequested,
		RequestedAt:         now,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	created, isNew, err := s.repository.CreateOrReuseMeetingSession(ctx, session)
	if err != nil {
		return nil, err
	}
	updated, updateErr := s.applyCreateMetadata(ctx, created, input, userProvidedTitle, normalizedJoinURL, now, !isNew)
	if updateErr != nil {
		log.Printf("Meeting session create metadata update failed. sessionId=%s joinUrlHash=%s reused=%t error=%v", created.ID, created.JoinURLHash, !isNew, updateErr)
		if hasMeetingSessionCreateContext(input) {
			return &MeetingSessionCreateResult{Session: created, Reused: !isNew}, updateErr
		}
	} else if updated != nil {
		created = updated
		if !isNew {
			s.publishStatusChanged(*created)
		}
	}
	if !isNew {
		log.Printf("Meeting session reuse. sessionId=%s joinUrlHash=%s status=%s createdAt=%s updatedAt=%s", created.ID, created.JoinURLHash, created.Status, created.CreatedAt.UTC().Format(time.RFC3339Nano), created.UpdatedAt.UTC().Format(time.RFC3339Nano))
		return &MeetingSessionCreateResult{Session: created, Reused: true}, nil
	}
	log.Printf("Meeting session create. sessionId=%s joinUrlHash=%s status=%s title=%q titleSource=%s createdAt=%s", created.ID, created.JoinURLHash, created.Status, created.Title, created.TitleSource, created.CreatedAt.UTC().Format(time.RFC3339Nano))

	if s.commander == nil {
		failed, updateErr := s.markCommandFailed(ctx, created.ID, ErrBotControlNotConfigured)
		if updateErr != nil {
			return &MeetingSessionCreateResult{Session: created}, updateErr
		}
		return &MeetingSessionCreateResult{Session: failed}, ErrBotControlNotConfigured
	}
	candidateUserIDs := meetingTitleLookupCandidateUserIDs(input)
	candidateUserPrincipalNames := meetingTitleLookupCandidateUserPrincipalNames(input)
	joinMeetingID := extractTeamsJoinMeetingID(normalizedJoinURL)
	log.Printf(
		"Meeting title lookup candidates prepared. sessionId=%s joinUrlHash=%s candidateUserIdsCount=%d candidateUserIdsHash=%s candidateUserPrincipalNamesCount=%d candidateUserPrincipalNamesHash=%s joinMeetingId=%s createdByMicrosoftUserIdHash=%s createdByEmailHash=%s",
		created.ID,
		created.JoinURLHash,
		len(candidateUserIDs),
		hashesForLog(candidateUserIDs),
		len(candidateUserPrincipalNames),
		hashesForLog(candidateUserPrincipalNames),
		joinMeetingID,
		hashForLog(input.CreatedByMicrosoftUserID),
		hashForLog(input.CreatedByEmail),
	)
	if err := s.commander.SendJoinCommand(ctx, BotJoinCommand{
		SessionID:                   created.ID,
		JoinURL:                     normalizedJoinURL,
		CanonicalJoinWebURL:         normalizedJoinURL,
		JoinMeetingID:               joinMeetingID,
		CandidateUserIDs:            candidateUserIDs,
		CandidateUserPrincipalNames: candidateUserPrincipalNames,
		CreatedByMicrosoftUserID:    strings.TrimSpace(input.CreatedByMicrosoftUserID),
		CreatedByEmail:              strings.TrimSpace(input.CreatedByEmail),
	}); err != nil {
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
	updated, err = s.repository.UpdateMeetingSessionStatus(ctx, domain.MeetingSessionStatusUpdate{
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
	if stale, cleanupErr := s.CleanupStaleMeetingSessions(ctx); cleanupErr != nil {
		log.Printf("Meeting session stale cleanup failed before get. sessionId=%s error=%v", sessionID, cleanupErr)
	} else if len(stale) > 0 {
		log.Printf("Meeting session stale cleanup completed before get. sessionId=%s count=%d", sessionID, len(stale))
	}
	session, err := s.repository.GetMeetingSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	log.Printf("Meeting session fetched. sessionId=%s joinUrlHash=%s status=%s botCallId=%s updatedAt=%s", session.ID, session.JoinURLHash, session.Status, session.BotCallID, session.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return session, nil
}

func (s *MeetingSessionService) ListMeetingSessions(ctx context.Context, workspaceID string, limit int) ([]domain.MeetingSession, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: workspaceId is required", domain.ErrInvalidArgument)
	}
	if stale, cleanupErr := s.CleanupStaleMeetingSessions(ctx); cleanupErr != nil {
		log.Printf("Meeting session stale cleanup failed before list. workspaceId=%s error=%v", workspaceID, cleanupErr)
	} else if len(stale) > 0 {
		log.Printf("Meeting session stale cleanup completed before list. workspaceId=%s count=%d", workspaceID, len(stale))
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	return s.repository.ListMeetingSessions(ctx, workspaceID, limit)
}

func (s *MeetingSessionService) EndMeetingSession(ctx context.Context, input MeetingSessionEndInput) (*domain.MeetingSession, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: sessionId is required", domain.ErrInvalidArgument)
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "manual_end_requested"
	}

	previous, err := s.repository.GetMeetingSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if isTerminalMeetingSessionStatus(previous.Status) {
		log.Printf("Meeting session end ignored because status is already terminal. sessionId=%s joinUrlHash=%s status=%s botCallId=%s reason=%s", previous.ID, previous.JoinURLHash, previous.Status, previous.BotCallID, reason)
		return previous, nil
	}
	if s.commander == nil {
		return nil, ErrBotControlNotConfigured
	}

	if err := s.commander.EndMeetingSession(ctx, BotEndCommand{
		SessionID: previous.ID,
		BotCallID: previous.BotCallID,
		Reason:    reason,
	}); err != nil {
		if errors.Is(err, ErrBotControlNotConfigured) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrBotControlCommandFailed, err)
	}

	updated, err := s.UpdateMeetingSessionStatus(ctx, MeetingSessionStatusUpdateInput{
		SessionID: previous.ID,
		Status:    domain.MeetingSessionEnded,
		BotCallID: previous.BotCallID,
		Message:   reason,
		Reason:    reason,
		Source:    "frontend_manual_end",
	})
	if err != nil {
		return nil, err
	}
	log.Printf("Meeting session manual end completed. sessionId=%s joinUrlHash=%s oldStatus=%s newStatus=%s botCallId=%s reason=%s", updated.ID, updated.JoinURLHash, previous.Status, updated.Status, updated.BotCallID, reason)
	return updated, nil
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
	if previousErr == nil && previous != nil && shouldSuppressMeetingSessionTerminalRevival(*previous, status) {
		log.Printf("Meeting session status update suppressed because previous status is terminal. sessionId=%s joinUrlHash=%s oldStatus=%s requestedStatus=%s botCallId=%s reason=%s errorCode=%s source=%s message=%s",
			previous.ID, previous.JoinURLHash, previous.Status, status, strings.TrimSpace(input.BotCallID), strings.TrimSpace(input.Reason), strings.TrimSpace(input.ErrorCode), strings.TrimSpace(input.Source), strings.TrimSpace(input.Message))
		return previous, nil
	}

	now := s.now().UTC()
	incomingTitle := strings.TrimSpace(input.Title)
	incomingTitleSource := normalizeMeetingSessionTitleSource(input.TitleSource, input.Title)
	titleDecision := decideMeetingSessionTitleUpdate(previous, incomingTitle, incomingTitleSource)
	updateTitle := ""
	updateTitleSource := ""
	if titleDecision.ApplyTitle {
		updateTitle = incomingTitle
		updateTitleSource = incomingTitleSource
	}
	update := domain.MeetingSessionStatusUpdate{
		SessionID:       sessionID,
		Status:          status,
		BotCallID:       strings.TrimSpace(input.BotCallID),
		Title:           updateTitle,
		TitleSource:     updateTitleSource,
		LastBotStatusAt: &now,
		UpdatedAt:       now,
	}
	switch status {
	case domain.MeetingSessionCommandSent, domain.MeetingSessionJoining:
		update.CommandSentAt = &now
	case domain.MeetingSessionJoined, domain.MeetingSessionActive, domain.MeetingSessionRecording:
		update.JoinedAt = &now
	case domain.MeetingSessionEnded, domain.MeetingSessionStale:
		update.EndedAt = &now
		update.EndReason = summarizeMeetingSessionEndReason(input)
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
	if incomingTitle != "" {
		log.Printf("Meeting session status title update decision. sessionId=%s joinUrlHash=%s incomingTitle=%q incomingTitleSource=%s decision=%s title=%q titleSource=%s",
			updated.ID, updated.JoinURLHash, incomingTitle, incomingTitleSource, titleDecision.Decision, updated.Title, updated.TitleSource)
	}
	s.publishStatusChanged(*updated)
	return updated, nil
}

func (s *MeetingSessionService) UpdateMeetingSessionMetadata(ctx context.Context, input MeetingSessionMetadataUpdateInput) (*domain.MeetingSession, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: sessionId is required", domain.ErrInvalidArgument)
	}
	title := strings.TrimSpace(input.Title)
	if title == "" &&
		strings.TrimSpace(input.UserProvidedTitle) == "" &&
		strings.TrimSpace(input.GraphTitle) == "" &&
		strings.TrimSpace(input.Provider) == "" &&
		strings.TrimSpace(input.ExternalMeetingID) == "" &&
		strings.TrimSpace(input.JoinMeetingID) == "" &&
		strings.TrimSpace(input.JoinWebURL) == "" &&
		strings.TrimSpace(input.CanonicalJoinWebURL) == "" &&
		strings.TrimSpace(input.ThreadID) == "" &&
		strings.TrimSpace(input.OrganizerID) == "" &&
		strings.TrimSpace(input.OrganizerName) == "" &&
		strings.TrimSpace(input.OrganizerEmail) == "" &&
		strings.TrimSpace(input.ScheduledStartAt) == "" &&
		strings.TrimSpace(input.ScheduledEndAt) == "" &&
		strings.TrimSpace(input.TitleResolutionErrorCode) == "" &&
		strings.TrimSpace(input.TitleResolutionErrorMessage) == "" &&
		strings.TrimSpace(input.TitleResolvedAt) == "" &&
		strings.TrimSpace(input.Purpose) == "" &&
		strings.TrimSpace(input.Context) == "" &&
		strings.TrimSpace(input.Agenda) == "" &&
		strings.TrimSpace(input.DecisionPoints) == "" &&
		strings.TrimSpace(input.Concerns) == "" &&
		strings.TrimSpace(input.ExpectedOutput) == "" &&
		strings.TrimSpace(input.CustomInstruction) == "" {
		return nil, fmt.Errorf("%w: metadata is required", domain.ErrInvalidArgument)
	}
	previous, previousErr := s.repository.GetMeetingSession(ctx, sessionID)
	if previousErr != nil {
		log.Printf("Meeting session metadata update could not read previous state. sessionId=%s incomingTitle=%q incomingTitleSource=%s error=%v",
			sessionID, title, strings.TrimSpace(input.TitleSource), previousErr)
	}
	now := s.now().UTC()
	var scheduledStartAtPtr *time.Time
	if input.ScheduledStartAt != "" {
		parsed, err := parseMetadataTime(input.ScheduledStartAt)
		if err != nil {
			return nil, err
		}
		scheduledStartAtPtr = &parsed
	}
	var scheduledEndAtPtr *time.Time
	if input.ScheduledEndAt != "" {
		parsed, err := parseMetadataTime(input.ScheduledEndAt)
		if err != nil {
			return nil, err
		}
		scheduledEndAtPtr = &parsed
	}
	var titleResolvedAtPtr *time.Time
	if input.TitleResolvedAt != "" {
		parsed, err := parseMetadataTime(input.TitleResolvedAt)
		if err != nil {
			return nil, err
		}
		titleResolvedAtPtr = &parsed
	}
	incomingTitleSource := normalizeMeetingSessionTitleSource(input.TitleSource, input.Title)
	decision := decideMeetingSessionTitleUpdate(previous, title, incomingTitleSource)
	updateTitle := ""
	updateTitleSource := ""
	if decision.ApplyTitle {
		updateTitle = title
		updateTitleSource = incomingTitleSource
	}
	updated, err := s.repository.UpdateMeetingSessionMetadata(ctx, domain.MeetingSessionMetadataUpdate{
		SessionID:                   sessionID,
		Title:                       updateTitle,
		TitleSource:                 updateTitleSource,
		UserProvidedTitle:           metadataUserProvidedTitle(input, incomingTitleSource, title),
		GraphTitle:                  metadataGraphTitle(input, incomingTitleSource, title),
		Provider:                    strings.TrimSpace(input.Provider),
		ExternalMeetingID:           strings.TrimSpace(input.ExternalMeetingID),
		JoinMeetingID:               strings.TrimSpace(input.JoinMeetingID),
		JoinWebURL:                  strings.TrimSpace(input.JoinWebURL),
		CanonicalJoinWebURL:         strings.TrimSpace(input.CanonicalJoinWebURL),
		ThreadID:                    strings.TrimSpace(input.ThreadID),
		OrganizerID:                 strings.TrimSpace(input.OrganizerID),
		OrganizerName:               strings.TrimSpace(input.OrganizerName),
		OrganizerEmail:              strings.TrimSpace(input.OrganizerEmail),
		ScheduledStartAt:            scheduledStartAtPtr,
		ScheduledEndAt:              scheduledEndAtPtr,
		TitleResolutionErrorCode:    strings.TrimSpace(input.TitleResolutionErrorCode),
		TitleResolutionErrorMessage: strings.TrimSpace(input.TitleResolutionErrorMessage),
		TitleResolvedAt:             titleResolvedAtPtr,
		Purpose:                     strings.TrimSpace(input.Purpose),
		Context:                     strings.TrimSpace(input.Context),
		Agenda:                      strings.TrimSpace(input.Agenda),
		DecisionPoints:              strings.TrimSpace(input.DecisionPoints),
		Concerns:                    strings.TrimSpace(input.Concerns),
		ExpectedOutput:              strings.TrimSpace(input.ExpectedOutput),
		CustomInstruction:           strings.TrimSpace(input.CustomInstruction),
		UpdatedAt:                   now,
	})
	if err != nil {
		return nil, err
	}
	oldTitle, oldTitleSource := "", ""
	if previousErr == nil && previous != nil {
		oldTitle = previous.Title
		oldTitleSource = previous.TitleSource
	}
	log.Printf("Meeting session title update decision. sessionId=%s joinUrlHash=%s oldTitle=%q oldTitleSource=%s incomingTitle=%q incomingTitleSource=%s decision=%s newTitle=%q newTitleSource=%s",
		updated.ID, updated.JoinURLHash, oldTitle, oldTitleSource, title, incomingTitleSource, decision.Decision, updated.Title, updated.TitleSource)
	log.Printf("Meeting session metadata changed. sessionId=%s joinUrlHash=%s title=%q titleSource=%s provider=%s externalMeetingId=%s joinMeetingId=%s threadId=%s organizerId=%s titleResolutionErrorCode=%s updatedAt=%s",
		updated.ID, updated.JoinURLHash, updated.Title, updated.TitleSource, updated.Provider, updated.ExternalMeetingID, updated.JoinMeetingID, updated.ThreadID, updated.OrganizerID, updated.TitleResolutionErrorCode, updated.UpdatedAt.UTC().Format(time.RFC3339Nano))
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

func shouldSuppressMeetingSessionTerminalRevival(previous domain.MeetingSession, requested domain.MeetingSessionStatus) bool {
	if !isTerminalMeetingSessionStatus(previous.Status) {
		return false
	}
	return !isTerminalMeetingSessionStatus(requested)
}

func isTerminalMeetingSessionStatus(status domain.MeetingSessionStatus) bool {
	switch status {
	case domain.MeetingSessionEnded, domain.MeetingSessionFailed, domain.MeetingSessionStale:
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

func summarizeMeetingSessionEndReason(input MeetingSessionStatusUpdateInput) string {
	for _, value := range []string{input.Reason, input.Message, input.Source} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			if len(trimmed) > 300 {
				return trimmed[:300]
			}
			return trimmed
		}
	}
	return ""
}

func meetingSessionUserProvidedTitle(input MeetingSessionCreateInput) string {
	for _, value := range []string{input.UserProvidedTitle, input.Title} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *MeetingSessionService) applyCreateMetadata(ctx context.Context, session *domain.MeetingSession, input MeetingSessionCreateInput, userProvidedTitle string, normalizedJoinURL string, updatedAt time.Time, reused bool) (*domain.MeetingSession, error) {
	if session == nil {
		return nil, nil
	}
	update := domain.MeetingSessionMetadataUpdate{
		SessionID:         session.ID,
		Purpose:           strings.TrimSpace(input.Purpose),
		Context:           strings.TrimSpace(input.Context),
		Agenda:            strings.TrimSpace(input.Agenda),
		DecisionPoints:    strings.TrimSpace(input.DecisionPoints),
		Concerns:          strings.TrimSpace(input.Concerns),
		ExpectedOutput:    strings.TrimSpace(input.ExpectedOutput),
		CustomInstruction: strings.TrimSpace(input.CustomInstruction),
		UpdatedAt:         updatedAt,
	}
	if reused && shouldApplyCreateTitleToReusedSession(*session, userProvidedTitle) {
		update.Title = userProvidedTitle
		update.TitleSource = "user_input"
		update.UserProvidedTitle = userProvidedTitle
		update.JoinWebURL = normalizedJoinURL
		update.CanonicalJoinWebURL = normalizedJoinURL
	}
	if !hasMeetingSessionMetadataUpdate(update) {
		return nil, nil
	}
	return s.repository.UpdateMeetingSessionMetadata(ctx, update)
}

func hasMeetingSessionCreateContext(input MeetingSessionCreateInput) bool {
	return strings.TrimSpace(input.Purpose) != "" ||
		strings.TrimSpace(input.Context) != "" ||
		strings.TrimSpace(input.Agenda) != "" ||
		strings.TrimSpace(input.DecisionPoints) != "" ||
		strings.TrimSpace(input.Concerns) != "" ||
		strings.TrimSpace(input.ExpectedOutput) != "" ||
		strings.TrimSpace(input.CustomInstruction) != ""
}

func hasMeetingSessionMetadataUpdate(update domain.MeetingSessionMetadataUpdate) bool {
	return strings.TrimSpace(update.Title) != "" ||
		strings.TrimSpace(update.UserProvidedTitle) != "" ||
		strings.TrimSpace(update.GraphTitle) != "" ||
		strings.TrimSpace(update.Provider) != "" ||
		strings.TrimSpace(update.ExternalMeetingID) != "" ||
		strings.TrimSpace(update.JoinMeetingID) != "" ||
		strings.TrimSpace(update.JoinWebURL) != "" ||
		strings.TrimSpace(update.CanonicalJoinWebURL) != "" ||
		strings.TrimSpace(update.ThreadID) != "" ||
		strings.TrimSpace(update.OrganizerID) != "" ||
		strings.TrimSpace(update.OrganizerName) != "" ||
		strings.TrimSpace(update.OrganizerEmail) != "" ||
		update.ScheduledStartAt != nil ||
		update.ScheduledEndAt != nil ||
		strings.TrimSpace(update.TitleResolutionErrorCode) != "" ||
		strings.TrimSpace(update.TitleResolutionErrorMessage) != "" ||
		update.TitleResolvedAt != nil ||
		strings.TrimSpace(update.Purpose) != "" ||
		strings.TrimSpace(update.Context) != "" ||
		strings.TrimSpace(update.Agenda) != "" ||
		strings.TrimSpace(update.DecisionPoints) != "" ||
		strings.TrimSpace(update.Concerns) != "" ||
		strings.TrimSpace(update.ExpectedOutput) != "" ||
		strings.TrimSpace(update.CustomInstruction) != ""
}

func meetingTitleLookupCandidateUserIDs(input MeetingSessionCreateInput) []string {
	values := make([]string, 0, len(input.CandidateUserIDs)+2)
	values = appendAadObjectID(values, input.OrganizerUserID)
	values = appendAadObjectID(values, input.CreatedByMicrosoftUserID)
	for _, value := range input.CandidateUserIDs {
		values = appendAadObjectID(values, value)
	}
	return uniqueTrimmedStrings(values)
}

func meetingTitleLookupCandidateUserPrincipalNames(input MeetingSessionCreateInput) []string {
	values := make([]string, 0, len(input.CandidateUserPrincipalNames)+len(input.CandidateUserIDs)+1)
	values = append(values, input.CreatedByEmail)
	values = append(values, input.CandidateUserPrincipalNames...)
	for _, value := range input.CandidateUserIDs {
		if !isAadObjectID(value) {
			values = append(values, value)
		}
	}
	return uniqueTrimmedStrings(values)
}

func appendAadObjectID(values []string, value string) []string {
	if isAadObjectID(value) {
		return append(values, value)
	}
	return values
}

func isAadObjectID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
				return false
			}
		}
	}
	return true
}

func uniqueTrimmedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, trimmed)
	}
	return unique
}

func extractTeamsJoinMeetingID(joinURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(joinURL))
	if err != nil {
		return ""
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	for index, segment := range segments {
		if strings.EqualFold(segment, "meet") && index+1 < len(segments) {
			if meetingID, err := url.PathUnescape(segments[index+1]); err == nil {
				return strings.TrimSpace(meetingID)
			}
			return strings.TrimSpace(segments[index+1])
		}
	}
	return ""
}

func hashesForLog(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	hashes := make([]string, 0, len(values))
	for _, value := range values {
		hashes = append(hashes, hashForLog(value))
	}
	return "[" + strings.Join(hashes, ",") + "]"
}

func hashForLog(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	bytes := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%X", bytes[:8])
}

func meetingSessionCreateTitle(title string) string {
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		return trimmed
	}
	return defaultMeetingSessionTitle
}

func meetingSessionCreateTitleSource(title string) string {
	if strings.TrimSpace(title) != "" {
		return "user_input"
	}
	return "fallback"
}

func shouldApplyCreateTitleToReusedSession(session domain.MeetingSession, title string) bool {
	if strings.TrimSpace(title) == "" {
		return false
	}
	titleSource := strings.TrimSpace(session.TitleSource)
	return titleSource == "" || titleSource == "fallback"
}

type meetingSessionTitleUpdateDecision struct {
	ApplyTitle bool
	Decision   string
}

func decideMeetingSessionTitleUpdate(previous *domain.MeetingSession, incomingTitle string, incomingSource string) meetingSessionTitleUpdateDecision {
	incomingTitle = strings.TrimSpace(incomingTitle)
	incomingSource = strings.TrimSpace(incomingSource)
	if incomingTitle == "" {
		return meetingSessionTitleUpdateDecision{Decision: "ignore_null"}
	}

	oldSource := ""
	if previous != nil {
		oldSource = strings.TrimSpace(previous.TitleSource)
	}
	oldRank := meetingSessionTitleSourceRank(oldSource)
	incomingRank := meetingSessionTitleSourceRank(incomingSource)
	if incomingRank == 0 {
		incomingRank = 1
	}
	if incomingRank >= oldRank {
		switch incomingRank {
		case 3:
			return meetingSessionTitleUpdateDecision{ApplyTitle: true, Decision: "overwrite_with_graph"}
		case 2:
			return meetingSessionTitleUpdateDecision{ApplyTitle: true, Decision: "overwrite_with_user_input"}
		default:
			if oldRank <= 1 {
				return meetingSessionTitleUpdateDecision{ApplyTitle: true, Decision: "overwrite_with_fallback"}
			}
		}
	}
	return meetingSessionTitleUpdateDecision{Decision: "keep_existing"}
}

func meetingSessionTitleSourceRank(source string) int {
	source = strings.ToLower(strings.TrimSpace(source))
	switch {
	case source == "graph_online_meeting" ||
		source == "graph_calendar_event" ||
		source == "teams_metadata" ||
		strings.HasPrefix(source, "graph_"):
		return 3
	case source == "user_input":
		return 2
	case source == "fallback":
		return 1
	default:
		if source == "" {
			return 0
		}
		return 1
	}
}

func metadataUserProvidedTitle(input MeetingSessionMetadataUpdateInput, titleSource string, title string) string {
	if value := strings.TrimSpace(input.UserProvidedTitle); value != "" {
		return value
	}
	if strings.TrimSpace(titleSource) == "user_input" {
		return strings.TrimSpace(title)
	}
	return ""
}

func metadataGraphTitle(input MeetingSessionMetadataUpdateInput, titleSource string, title string) string {
	if value := strings.TrimSpace(input.GraphTitle); value != "" {
		return value
	}
	if meetingSessionTitleSourceRank(titleSource) >= 3 {
		return strings.TrimSpace(title)
	}
	return ""
}

func normalizeMeetingSessionTitleSource(source string, title string) string {
	source = strings.TrimSpace(source)
	if source != "" {
		if len(source) > 80 {
			return source[:80]
		}
		return source
	}
	if strings.TrimSpace(title) != "" {
		return "bot_metadata"
	}
	return ""
}

func parseMetadataTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, value)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: metadata time is invalid", domain.ErrInvalidArgument)
	}
	return parsed.UTC(), nil
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
