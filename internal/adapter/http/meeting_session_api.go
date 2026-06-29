package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

const meetingSessionBodyLimitBytes int64 = 64 * 1024

type MeetingSessionUseCases interface {
	CreateMeetingSession(ctx context.Context, input application.MeetingSessionCreateInput) (*application.MeetingSessionCreateResult, error)
	GetMeetingSession(ctx context.Context, sessionID string) (*domain.MeetingSession, error)
	UpdateMeetingSessionStatus(ctx context.Context, input application.MeetingSessionStatusUpdateInput) (*domain.MeetingSession, error)
	UpdateMeetingSessionMetadata(ctx context.Context, input application.MeetingSessionMetadataUpdateInput) (*domain.MeetingSession, error)
	CleanupStaleMeetingSessions(ctx context.Context) ([]domain.MeetingSession, error)
	ListMeetingSessionDebug(ctx context.Context, limit int) ([]domain.MeetingSessionDebug, error)
}

type MeetingSessionAPI struct {
	service MeetingSessionUseCases
	apiKey  string
}

func NewMeetingSessionAPI(service MeetingSessionUseCases, apiKey string) *MeetingSessionAPI {
	return &MeetingSessionAPI{service: service, apiKey: apiKey}
}

func (api *MeetingSessionAPI) Create(w http.ResponseWriter, r *http.Request) {
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}
	var request meetingSessionCreateRequest
	if !decodeLimitedJSON(w, r, meetingSessionBodyLimitBytes, &request) {
		return
	}

	result, err := api.service.CreateMeetingSession(r.Context(), application.MeetingSessionCreateInput{
		JoinURL:                     request.JoinURL,
		Title:                       request.title(),
		UserProvidedTitle:           request.userProvidedTitle(),
		CandidateUserIDs:            request.candidateUserIDs(),
		CandidateUserPrincipalNames: request.candidateUserPrincipalNames(),
		CreatedByMicrosoftUserID:    request.createdByMicrosoftUserID(),
		CreatedByEmail:              request.createdByEmail(),
		OrganizerUserID:             request.organizerUserID(),
	})
	var session *domain.MeetingSession
	if result != nil {
		session = result.Session
	}
	if err != nil {
		if errors.Is(err, domain.ErrInvalidArgument) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if session != nil {
			log.Printf("Meeting session join command failed. sessionId=%s joinUrlHash=%s status=%s error=%v", session.ID, session.JoinURLHash, session.Status, err)
		}
		if errors.Is(err, application.ErrBotControlNotConfigured) {
			writeError(w, http.StatusServiceUnavailable, "bot_control_not_configured", "bot control URL or token is not configured")
			return
		}
		if errors.Is(err, application.ErrBotControlCommandFailed) {
			writeError(w, http.StatusBadGateway, "bot_control_command_failed", "failed to send join command to bot control API")
			return
		}
		log.Printf("Create meeting session failed. error=%v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if session == nil {
		log.Printf("Create meeting session returned nil session")
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if result.Reused {
		log.Printf("Meeting session reused. sessionId=%s joinUrlHash=%s status=%s", session.ID, session.JoinURLHash, session.Status)
	} else {
		log.Printf("Meeting session created. sessionId=%s joinUrlHash=%s status=%s", session.ID, session.JoinURLHash, session.Status)
	}
	status := http.StatusCreated
	if result.Reused {
		status = http.StatusOK
	}
	writeJSON(w, status, meetingSessionCreateResponse{
		SessionID:           session.ID,
		Title:               session.Title,
		DisplayTitle:        session.Title,
		TitleSource:         session.TitleSource,
		TitleUpdatedAt:      optionalTimeValue(session.TitleUpdatedAt),
		UserProvidedTitle:   session.UserProvidedTitle,
		GraphTitle:          session.GraphTitle,
		Provider:            session.Provider,
		ExternalMeetingID:   session.ExternalMeetingID,
		JoinMeetingID:       session.JoinMeetingID,
		JoinWebURL:          session.JoinWebURL,
		CanonicalJoinWebURL: session.CanonicalJoinWebURL,
		ThreadID:            session.ThreadID,
		OrganizerID:         session.OrganizerID,
		OrganizerName:       session.OrganizerName,
		OrganizerEmail:      session.OrganizerEmail,
		ScheduledStartAt:    optionalTimeValue(session.ScheduledStartAt),
		ScheduledEndAt:      optionalTimeValue(session.ScheduledEndAt),
		Status:              string(session.Status),
		MeetingURLHash:      session.JoinURLHash,
		Reused:              result.Reused,
		CreatedAt:           session.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:           session.UpdatedAt.UTC().Format(time.RFC3339Nano),
		BotCallID:           session.BotCallID,
	})
	log.Printf("Meeting session create response sent. sessionId=%s joinUrlHash=%s status=%s title=%q titleSource=%s reused=%t httpStatus=%d", session.ID, session.JoinURLHash, session.Status, session.Title, session.TitleSource, result.Reused, status)
}

func (api *MeetingSessionAPI) Get(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	session, err := api.service.GetMeetingSession(r.Context(), sessionID)
	if err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
	log.Printf("Meeting session get response sent. sessionId=%s joinUrlHash=%s status=%s title=%q titleSource=%s botCallId=%s updatedAt=%s", session.ID, session.JoinURLHash, session.Status, session.Title, session.TitleSource, session.BotCallID, session.UpdatedAt.UTC().Format(time.RFC3339Nano))
}

func (api *MeetingSessionAPI) CleanupStale(w http.ResponseWriter, r *http.Request) {
	if !authorizedSecret(r.Header.Get("X-DeciScope-Api-Key"), api.apiKey) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	sessions, err := api.service.CleanupStaleMeetingSessions(r.Context())
	if err != nil {
		log.Printf("Meeting session stale cleanup failed. error=%v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	log.Printf("Meeting session stale cleanup requested. count=%d", len(sessions))
	writeJSON(w, http.StatusOK, meetingSessionCleanupResponse{
		Count: len(sessions),
		Items: meetingSessionResponsesFromDomain(sessions),
	})
}

func (api *MeetingSessionAPI) DebugList(w http.ResponseWriter, r *http.Request) {
	if !authorizedSecret(r.Header.Get("X-DeciScope-Api-Key"), api.apiKey) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	limit, err := parseMeetingSessionDebugLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	items, err := api.service.ListMeetingSessionDebug(r.Context(), limit)
	if err != nil {
		log.Printf("Meeting session debug list failed. limit=%d error=%v", limit, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, meetingSessionDebugListResponse{
		Items: meetingSessionDebugResponsesFromDomain(items),
	})
}

func (api *MeetingSessionAPI) UpdateBotStatus(w http.ResponseWriter, r *http.Request) {
	if !authorizedSecret(r.Header.Get("X-DeciScope-Api-Key"), api.apiKey) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}
	var request meetingSessionStatusUpdateRequest
	if !decodeLimitedJSONAllowUnknown(w, r, meetingSessionBodyLimitBytes, &request) {
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	previous, previousErr := api.service.GetMeetingSession(r.Context(), sessionID)
	oldStatus := "unknown"
	if previousErr == nil && previous != nil {
		oldStatus = string(previous.Status)
	}
	log.Printf("Meeting session status PATCH received from bot. sessionId=%s oldStatus=%s requestedStatus=%s requestedBotCallId=%s reason=%s errorCode=%s source=%s previousReadError=%v",
		sessionID, oldStatus, strings.TrimSpace(request.Status), request.botCallID(), request.reason(), request.errorCode(), request.source(), previousErr)
	session, err := api.service.UpdateMeetingSessionStatus(r.Context(), application.MeetingSessionStatusUpdateInput{
		SessionID:   sessionID,
		Status:      domain.MeetingSessionStatus(request.Status),
		BotCallID:   request.botCallID(),
		Message:     request.Message,
		Reason:      request.reason(),
		ErrorCode:   request.errorCode(),
		Source:      request.source(),
		Title:       request.title(),
		TitleSource: request.titleSource(),
	})
	if err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	log.Printf("Meeting session status PATCH persisted. sessionId=%s joinUrlHash=%s oldStatus=%s newStatus=%s botCallId=%s reason=%s errorCode=%s source=%s updatedAt=%s",
		session.ID, session.JoinURLHash, oldStatus, session.Status, session.BotCallID, request.reason(), request.errorCode(), request.source(), session.UpdatedAt.UTC().Format(time.RFC3339Nano))
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
}

func (api *MeetingSessionAPI) UpdateBotMetadata(w http.ResponseWriter, r *http.Request) {
	if !authorizedSecret(r.Header.Get("X-DeciScope-Api-Key"), api.apiKey) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}
	var request meetingSessionMetadataUpdateRequest
	if !decodeLimitedJSONAllowUnknown(w, r, meetingSessionBodyLimitBytes, &request) {
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	previous, previousErr := api.service.GetMeetingSession(r.Context(), sessionID)
	oldTitle := ""
	oldTitleSource := ""
	if previousErr == nil && previous != nil {
		oldTitle = previous.Title
		oldTitleSource = previous.TitleSource
	}
	log.Printf("Meeting session metadata PATCH received. sessionId=%s oldTitle=%q oldTitleSource=%s title=%q titleSource=%s provider=%s externalMeetingId=%s joinMeetingId=%s threadId=%s organizerId=%s previousReadError=%v",
		sessionID, oldTitle, oldTitleSource, request.title(), request.titleSource(), request.provider(), request.externalMeetingID(), request.joinMeetingID(), request.threadID(), request.organizerID(), previousErr)
	session, err := api.service.UpdateMeetingSessionMetadata(r.Context(), application.MeetingSessionMetadataUpdateInput{
		SessionID:                   sessionID,
		Title:                       request.title(),
		TitleSource:                 request.titleSource(),
		UserProvidedTitle:           request.userProvidedTitle(),
		GraphTitle:                  request.graphTitle(),
		Provider:                    request.provider(),
		ExternalMeetingID:           request.externalMeetingID(),
		JoinMeetingID:               request.joinMeetingID(),
		JoinWebURL:                  request.joinWebURL(),
		CanonicalJoinWebURL:         request.canonicalJoinWebURL(),
		ThreadID:                    request.threadID(),
		OrganizerID:                 request.organizerID(),
		OrganizerName:               request.organizerName(),
		OrganizerEmail:              request.organizerEmail(),
		ScheduledStartAt:            request.scheduledStartAt(),
		ScheduledEndAt:              request.scheduledEndAt(),
		TitleResolutionErrorCode:    request.titleResolutionErrorCode(),
		TitleResolutionErrorMessage: request.titleResolutionErrorMessage(),
		TitleResolvedAt:             request.titleResolvedAt(),
	})
	if err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	log.Printf("Meeting session title changed. sessionId=%s joinUrlHash=%s oldTitle=%q newTitle=%q oldTitleSource=%s newTitleSource=%s provider=%s externalMeetingId=%s threadId=%s updatedAt=%s",
		session.ID, session.JoinURLHash, oldTitle, session.Title, oldTitleSource, session.TitleSource, session.Provider, session.ExternalMeetingID, session.ThreadID, session.UpdatedAt.UTC().Format(time.RFC3339Nano))
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
}

type meetingSessionCreateRequest struct {
	JoinURL                          string   `json:"joinUrl"`
	Title                            string   `json:"title"`
	TitleSnake                       string   `json:"meeting_title"`
	UserProvidedTitle                string   `json:"userProvidedTitle"`
	UserProvidedTitleSnake           string   `json:"user_provided_title"`
	CandidateUserIDs                 []string `json:"candidateUserIds"`
	CandidateUserIDsSnake            []string `json:"candidate_user_ids"`
	CandidateUserPrincipalNames      []string `json:"candidateUserPrincipalNames"`
	CandidateUserPrincipalNamesSnake []string `json:"candidate_user_principal_names"`
	CreatedByMicrosoftUserID         string   `json:"createdByMicrosoftUserId"`
	CreatedByMicrosoftUserIDSnake    string   `json:"created_by_microsoft_user_id"`
	CreatedByEmail                   string   `json:"createdByEmail"`
	CreatedByEmailSnake              string   `json:"created_by_email"`
	OrganizerUserID                  string   `json:"organizerUserId"`
	OrganizerUserIDSnake             string   `json:"organizer_user_id"`
}

type meetingSessionCreateResponse struct {
	SessionID           string  `json:"sessionId"`
	Title               string  `json:"title,omitempty"`
	DisplayTitle        string  `json:"displayTitle,omitempty"`
	TitleSource         string  `json:"titleSource,omitempty"`
	TitleUpdatedAt      *string `json:"titleUpdatedAt,omitempty"`
	UserProvidedTitle   string  `json:"userProvidedTitle,omitempty"`
	GraphTitle          string  `json:"graphTitle,omitempty"`
	Provider            string  `json:"provider,omitempty"`
	ExternalMeetingID   string  `json:"externalMeetingId,omitempty"`
	JoinMeetingID       string  `json:"joinMeetingId,omitempty"`
	JoinWebURL          string  `json:"joinWebUrl,omitempty"`
	CanonicalJoinWebURL string  `json:"canonicalJoinWebUrl,omitempty"`
	ThreadID            string  `json:"threadId,omitempty"`
	OrganizerID         string  `json:"organizerId,omitempty"`
	OrganizerName       string  `json:"organizerName,omitempty"`
	OrganizerEmail      string  `json:"organizerEmail,omitempty"`
	ScheduledStartAt    *string `json:"scheduledStartAt,omitempty"`
	ScheduledEndAt      *string `json:"scheduledEndAt,omitempty"`
	Status              string  `json:"status"`
	MeetingURLHash      string  `json:"meetingUrlHash,omitempty"`
	Reused              bool    `json:"reused"`
	CreatedAt           string  `json:"createdAt,omitempty"`
	UpdatedAt           string  `json:"updatedAt,omitempty"`
	BotCallID           string  `json:"botCallId,omitempty"`
}

type meetingSessionStatusUpdateRequest struct {
	Status            string `json:"status"`
	BotCallID         string `json:"botCallId"`
	BotCallIDSnake    string `json:"bot_call_id"`
	Message           string `json:"message"`
	FailedReason      string `json:"failedReason"`
	FailedReasonSnake string `json:"failed_reason"`
	EndReason         string `json:"endReason"`
	EndReasonSnake    string `json:"end_reason"`
	ErrorCode         string `json:"errorCode"`
	ErrorCodeSnake    string `json:"error_code"`
	Source            string `json:"source"`
	Title             string `json:"title"`
	TitleSnake        string `json:"meeting_title"`
	TitleSource       string `json:"titleSource"`
	TitleSourceSnake  string `json:"title_source"`
}

type meetingSessionMetadataUpdateRequest struct {
	Title                            string `json:"title"`
	TitleSnake                       string `json:"meeting_title"`
	TitleSource                      string `json:"titleSource"`
	TitleSourceSnake                 string `json:"title_source"`
	UserProvidedTitle                string `json:"userProvidedTitle"`
	UserProvidedTitleSnake           string `json:"user_provided_title"`
	GraphTitle                       string `json:"graphTitle"`
	GraphTitleSnake                  string `json:"graph_title"`
	Provider                         string `json:"provider"`
	ExternalMeetingID                string `json:"externalMeetingId"`
	ExternalMeetingIDSnake           string `json:"external_meeting_id"`
	JoinMeetingID                    string `json:"joinMeetingId"`
	JoinMeetingIDSnake               string `json:"join_meeting_id"`
	JoinWebURL                       string `json:"joinWebUrl"`
	JoinWebURLSnake                  string `json:"join_web_url"`
	CanonicalJoinWebURL              string `json:"canonicalJoinWebUrl"`
	CanonicalJoinWebURLSnake         string `json:"canonical_join_web_url"`
	ThreadID                         string `json:"threadId"`
	ThreadIDSnake                    string `json:"thread_id"`
	OrganizerID                      string `json:"organizerId"`
	OrganizerIDSnake                 string `json:"organizer_id"`
	OrganizerName                    string `json:"organizerName"`
	OrganizerNameSnake               string `json:"organizer_name"`
	OrganizerEmail                   string `json:"organizerEmail"`
	OrganizerEmailSnake              string `json:"organizer_email"`
	ScheduledStartAt                 string `json:"scheduledStartAt"`
	ScheduledStartAtSnake            string `json:"scheduled_start_at"`
	ScheduledEndAt                   string `json:"scheduledEndAt"`
	ScheduledEndAtSnake              string `json:"scheduled_end_at"`
	TitleResolutionErrorCode         string `json:"titleResolutionErrorCode"`
	TitleResolutionErrorCodeSnake    string `json:"title_resolution_error_code"`
	TitleResolutionErrorMessage      string `json:"titleResolutionErrorMessage"`
	TitleResolutionErrorMessageSnake string `json:"title_resolution_error_message"`
	TitleResolvedAt                  string `json:"titleResolvedAt"`
	TitleResolvedAtSnake             string `json:"title_resolved_at"`
}

type meetingSessionResponse struct {
	SessionID                   string  `json:"sessionId"`
	Title                       string  `json:"title,omitempty"`
	DisplayTitle                string  `json:"displayTitle,omitempty"`
	TitleSource                 string  `json:"titleSource,omitempty"`
	TitleUpdatedAt              *string `json:"titleUpdatedAt,omitempty"`
	UserProvidedTitle           string  `json:"userProvidedTitle,omitempty"`
	GraphTitle                  string  `json:"graphTitle,omitempty"`
	Provider                    string  `json:"provider,omitempty"`
	ExternalMeetingID           string  `json:"externalMeetingId,omitempty"`
	JoinMeetingID               string  `json:"joinMeetingId,omitempty"`
	JoinWebURL                  string  `json:"joinWebUrl,omitempty"`
	CanonicalJoinWebURL         string  `json:"canonicalJoinWebUrl,omitempty"`
	ThreadID                    string  `json:"threadId,omitempty"`
	OrganizerID                 string  `json:"organizerId,omitempty"`
	OrganizerName               string  `json:"organizerName,omitempty"`
	OrganizerEmail              string  `json:"organizerEmail,omitempty"`
	ScheduledStartAt            *string `json:"scheduledStartAt,omitempty"`
	ScheduledEndAt              *string `json:"scheduledEndAt,omitempty"`
	TitleResolutionErrorCode    string  `json:"titleResolutionErrorCode,omitempty"`
	TitleResolutionErrorMessage string  `json:"titleResolutionErrorMessage,omitempty"`
	TitleResolvedAt             *string `json:"titleResolvedAt,omitempty"`
	Status                      string  `json:"status"`
	MeetingURLHash              string  `json:"meetingUrlHash,omitempty"`
	BotCallID                   string  `json:"botCallId,omitempty"`
	CreatedAt                   string  `json:"createdAt"`
	UpdatedAt                   string  `json:"updatedAt"`
	RequestedAt                 string  `json:"requestedAt"`
	CommandSentAt               *string `json:"commandSentAt"`
	JoinedAt                    *string `json:"joinedAt"`
	EndedAt                     *string `json:"endedAt"`
	EndReason                   *string `json:"endReason"`
	LastBotStatusAt             *string `json:"lastBotStatusAt"`
	LastError                   *string `json:"lastError"`
}

type meetingSessionCleanupResponse struct {
	Count int                      `json:"count"`
	Items []meetingSessionResponse `json:"items"`
}

type meetingSessionDebugListResponse struct {
	Items []meetingSessionDebugResponse `json:"items"`
}

type meetingSessionDebugResponse struct {
	SessionID         string  `json:"sessionId"`
	Title             string  `json:"title,omitempty"`
	TitleSource       string  `json:"titleSource,omitempty"`
	UserProvidedTitle string  `json:"userProvidedTitle,omitempty"`
	GraphTitle        string  `json:"graphTitle,omitempty"`
	Provider          string  `json:"provider,omitempty"`
	ExternalMeetingID string  `json:"externalMeetingId,omitempty"`
	JoinMeetingID     string  `json:"joinMeetingId,omitempty"`
	ThreadID          string  `json:"threadId,omitempty"`
	OrganizerID       string  `json:"organizerId,omitempty"`
	MeetingURLHash    string  `json:"meetingUrlHash"`
	Status            string  `json:"status"`
	CreatedAt         string  `json:"createdAt"`
	UpdatedAt         string  `json:"updatedAt"`
	LastTranscriptAt  *string `json:"lastTranscriptAt"`
	BotCallID         string  `json:"botCallId,omitempty"`
}

func meetingSessionResponseFromDomain(session domain.MeetingSession) meetingSessionResponse {
	lastError := optionalString(session.LastError)
	return meetingSessionResponse{
		SessionID:                   session.ID,
		Title:                       session.Title,
		DisplayTitle:                session.Title,
		TitleSource:                 session.TitleSource,
		TitleUpdatedAt:              optionalTime(session.TitleUpdatedAt),
		UserProvidedTitle:           session.UserProvidedTitle,
		GraphTitle:                  session.GraphTitle,
		Provider:                    session.Provider,
		ExternalMeetingID:           session.ExternalMeetingID,
		JoinMeetingID:               session.JoinMeetingID,
		JoinWebURL:                  session.JoinWebURL,
		CanonicalJoinWebURL:         session.CanonicalJoinWebURL,
		ThreadID:                    session.ThreadID,
		OrganizerID:                 session.OrganizerID,
		OrganizerName:               session.OrganizerName,
		OrganizerEmail:              session.OrganizerEmail,
		ScheduledStartAt:            optionalTime(session.ScheduledStartAt),
		ScheduledEndAt:              optionalTime(session.ScheduledEndAt),
		TitleResolutionErrorCode:    session.TitleResolutionErrorCode,
		TitleResolutionErrorMessage: session.TitleResolutionErrorMessage,
		TitleResolvedAt:             optionalTime(session.TitleResolvedAt),
		Status:                      string(session.Status),
		MeetingURLHash:              session.JoinURLHash,
		BotCallID:                   session.BotCallID,
		CreatedAt:                   session.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:                   session.UpdatedAt.UTC().Format(time.RFC3339Nano),
		RequestedAt:                 session.RequestedAt.UTC().Format(time.RFC3339Nano),
		CommandSentAt:               optionalTime(session.CommandSentAt),
		JoinedAt:                    optionalTime(session.JoinedAt),
		EndedAt:                     optionalTime(session.EndedAt),
		EndReason:                   optionalString(session.EndReason),
		LastBotStatusAt:             optionalTime(session.LastBotStatusAt),
		LastError:                   lastError,
	}
}

func meetingSessionResponsesFromDomain(sessions []domain.MeetingSession) []meetingSessionResponse {
	items := make([]meetingSessionResponse, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, meetingSessionResponseFromDomain(session))
	}
	return items
}

func meetingSessionDebugResponsesFromDomain(sessions []domain.MeetingSessionDebug) []meetingSessionDebugResponse {
	items := make([]meetingSessionDebugResponse, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, meetingSessionDebugResponse{
			SessionID:         session.ID,
			Title:             session.Title,
			TitleSource:       session.TitleSource,
			UserProvidedTitle: session.UserProvidedTitle,
			GraphTitle:        session.GraphTitle,
			Provider:          session.Provider,
			ExternalMeetingID: session.ExternalMeetingID,
			JoinMeetingID:     session.JoinMeetingID,
			ThreadID:          session.ThreadID,
			OrganizerID:       session.OrganizerID,
			MeetingURLHash:    session.JoinURLHash,
			Status:            string(session.Status),
			CreatedAt:         session.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt:         session.UpdatedAt.UTC().Format(time.RFC3339Nano),
			LastTranscriptAt:  optionalTime(session.LastTranscriptAt),
			BotCallID:         session.BotCallID,
		})
	}
	return items
}

func optionalTime(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func optionalTimeValue(value time.Time) *string {
	return optionalTime(value)
}

func parseMeetingSessionDebugLimit(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 {
		return 0, errors.New("limit must be 1 or greater")
	}
	if limit > 500 {
		return 500, nil
	}
	return limit, nil
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func (request meetingSessionStatusUpdateRequest) reason() string {
	for _, value := range []string{request.EndReason, request.EndReasonSnake, request.FailedReason, request.FailedReasonSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionStatusUpdateRequest) errorCode() string {
	for _, value := range []string{request.ErrorCode, request.ErrorCodeSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionStatusUpdateRequest) source() string {
	return strings.TrimSpace(request.Source)
}

func (request meetingSessionStatusUpdateRequest) title() string {
	for _, value := range []string{request.Title, request.TitleSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionStatusUpdateRequest) titleSource() string {
	for _, value := range []string{request.TitleSource, request.TitleSourceSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionStatusUpdateRequest) botCallID() string {
	for _, value := range []string{request.BotCallID, request.BotCallIDSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionCreateRequest) title() string {
	for _, value := range []string{request.Title, request.TitleSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionCreateRequest) userProvidedTitle() string {
	for _, value := range []string{request.UserProvidedTitle, request.UserProvidedTitleSnake, request.Title, request.TitleSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionCreateRequest) candidateUserIDs() []string {
	values := append([]string{}, request.CandidateUserIDs...)
	values = append(values, request.CandidateUserIDsSnake...)
	return uniqueRequestStrings(values)
}

func (request meetingSessionCreateRequest) candidateUserPrincipalNames() []string {
	values := append([]string{}, request.CandidateUserPrincipalNames...)
	values = append(values, request.CandidateUserPrincipalNamesSnake...)
	return uniqueRequestStrings(values)
}

func (request meetingSessionCreateRequest) createdByMicrosoftUserID() string {
	for _, value := range []string{request.CreatedByMicrosoftUserID, request.CreatedByMicrosoftUserIDSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionCreateRequest) createdByEmail() string {
	for _, value := range []string{request.CreatedByEmail, request.CreatedByEmailSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionCreateRequest) organizerUserID() string {
	for _, value := range []string{request.OrganizerUserID, request.OrganizerUserIDSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func uniqueRequestStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
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
		result = append(result, trimmed)
	}
	return result
}

func (request meetingSessionMetadataUpdateRequest) title() string {
	for _, value := range []string{request.Title, request.TitleSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) titleSource() string {
	for _, value := range []string{request.TitleSource, request.TitleSourceSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) userProvidedTitle() string {
	for _, value := range []string{request.UserProvidedTitle, request.UserProvidedTitleSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) graphTitle() string {
	for _, value := range []string{request.GraphTitle, request.GraphTitleSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) provider() string {
	return strings.TrimSpace(request.Provider)
}

func (request meetingSessionMetadataUpdateRequest) externalMeetingID() string {
	for _, value := range []string{request.ExternalMeetingID, request.ExternalMeetingIDSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) joinMeetingID() string {
	for _, value := range []string{request.JoinMeetingID, request.JoinMeetingIDSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) joinWebURL() string {
	for _, value := range []string{request.JoinWebURL, request.JoinWebURLSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) canonicalJoinWebURL() string {
	for _, value := range []string{request.CanonicalJoinWebURL, request.CanonicalJoinWebURLSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) threadID() string {
	for _, value := range []string{request.ThreadID, request.ThreadIDSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) organizerID() string {
	for _, value := range []string{request.OrganizerID, request.OrganizerIDSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) organizerName() string {
	for _, value := range []string{request.OrganizerName, request.OrganizerNameSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) organizerEmail() string {
	for _, value := range []string{request.OrganizerEmail, request.OrganizerEmailSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) scheduledStartAt() string {
	for _, value := range []string{request.ScheduledStartAt, request.ScheduledStartAtSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) scheduledEndAt() string {
	for _, value := range []string{request.ScheduledEndAt, request.ScheduledEndAtSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) titleResolutionErrorCode() string {
	for _, value := range []string{request.TitleResolutionErrorCode, request.TitleResolutionErrorCodeSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) titleResolutionErrorMessage() string {
	for _, value := range []string{request.TitleResolutionErrorMessage, request.TitleResolutionErrorMessageSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (request meetingSessionMetadataUpdateRequest) titleResolvedAt() string {
	for _, value := range []string{request.TitleResolvedAt, request.TitleResolvedAtSnake} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func writeMeetingSessionError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrInvalidArgument) {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "meeting session not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func decodeLimitedJSON(w http.ResponseWriter, r *http.Request, limit int64, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		if isBodyTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if isBodyTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON request body")
		return false
	}
	return true
}

func decodeLimitedJSONAllowUnknown(w http.ResponseWriter, r *http.Request, limit int64, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(destination); err != nil {
		if isBodyTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if isBodyTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON request body")
		return false
	}
	return true
}
