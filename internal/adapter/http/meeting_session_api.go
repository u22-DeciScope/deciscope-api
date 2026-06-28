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
	CreateMeetingSession(ctx context.Context, joinURL string) (*application.MeetingSessionCreateResult, error)
	GetMeetingSession(ctx context.Context, sessionID string) (*domain.MeetingSession, error)
	UpdateMeetingSessionStatus(ctx context.Context, input application.MeetingSessionStatusUpdateInput) (*domain.MeetingSession, error)
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

	result, err := api.service.CreateMeetingSession(r.Context(), request.JoinURL)
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
		SessionID:      session.ID,
		Status:         string(session.Status),
		MeetingURLHash: session.JoinURLHash,
		Reused:         result.Reused,
		CreatedAt:      session.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:      session.UpdatedAt.UTC().Format(time.RFC3339Nano),
		BotCallID:      session.BotCallID,
	})
	log.Printf("Meeting session create response sent. sessionId=%s joinUrlHash=%s status=%s reused=%t httpStatus=%d", session.ID, session.JoinURLHash, session.Status, result.Reused, status)
}

func (api *MeetingSessionAPI) Get(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	session, err := api.service.GetMeetingSession(r.Context(), sessionID)
	if err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
	log.Printf("Meeting session get response sent. sessionId=%s joinUrlHash=%s status=%s botCallId=%s updatedAt=%s", session.ID, session.JoinURLHash, session.Status, session.BotCallID, session.UpdatedAt.UTC().Format(time.RFC3339Nano))
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
	if !decodeLimitedJSON(w, r, meetingSessionBodyLimitBytes, &request) {
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	previous, previousErr := api.service.GetMeetingSession(r.Context(), sessionID)
	oldStatus := "unknown"
	if previousErr == nil && previous != nil {
		oldStatus = string(previous.Status)
	}
	log.Printf("Meeting session status PATCH received from bot. sessionId=%s oldStatus=%s requestedStatus=%s requestedBotCallId=%s previousReadError=%v", sessionID, oldStatus, strings.TrimSpace(request.Status), strings.TrimSpace(request.BotCallID), previousErr)
	session, err := api.service.UpdateMeetingSessionStatus(r.Context(), application.MeetingSessionStatusUpdateInput{
		SessionID: sessionID,
		Status:    domain.MeetingSessionStatus(request.Status),
		BotCallID: request.BotCallID,
		Message:   request.Message,
	})
	if err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	log.Printf("Meeting session status PATCH persisted. sessionId=%s joinUrlHash=%s oldStatus=%s newStatus=%s botCallId=%s updatedAt=%s", session.ID, session.JoinURLHash, oldStatus, session.Status, session.BotCallID, session.UpdatedAt.UTC().Format(time.RFC3339Nano))
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
}

type meetingSessionCreateRequest struct {
	JoinURL string `json:"joinUrl"`
}

type meetingSessionCreateResponse struct {
	SessionID      string `json:"sessionId"`
	Status         string `json:"status"`
	MeetingURLHash string `json:"meetingUrlHash,omitempty"`
	Reused         bool   `json:"reused"`
	CreatedAt      string `json:"createdAt,omitempty"`
	UpdatedAt      string `json:"updatedAt,omitempty"`
	BotCallID      string `json:"botCallId,omitempty"`
}

type meetingSessionStatusUpdateRequest struct {
	Status    string `json:"status"`
	BotCallID string `json:"botCallId"`
	Message   string `json:"message"`
}

type meetingSessionResponse struct {
	SessionID      string  `json:"sessionId"`
	Status         string  `json:"status"`
	MeetingURLHash string  `json:"meetingUrlHash,omitempty"`
	BotCallID      string  `json:"botCallId,omitempty"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
	RequestedAt    string  `json:"requestedAt"`
	CommandSentAt  *string `json:"commandSentAt"`
	JoinedAt       *string `json:"joinedAt"`
	EndedAt        *string `json:"endedAt"`
	LastError      *string `json:"lastError"`
}

type meetingSessionCleanupResponse struct {
	Count int                      `json:"count"`
	Items []meetingSessionResponse `json:"items"`
}

type meetingSessionDebugListResponse struct {
	Items []meetingSessionDebugResponse `json:"items"`
}

type meetingSessionDebugResponse struct {
	SessionID        string  `json:"sessionId"`
	MeetingURLHash   string  `json:"meetingUrlHash"`
	Status           string  `json:"status"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
	LastTranscriptAt *string `json:"lastTranscriptAt"`
	BotCallID        string  `json:"botCallId,omitempty"`
}

func meetingSessionResponseFromDomain(session domain.MeetingSession) meetingSessionResponse {
	lastError := optionalString(session.LastError)
	return meetingSessionResponse{
		SessionID:      session.ID,
		Status:         string(session.Status),
		MeetingURLHash: session.JoinURLHash,
		BotCallID:      session.BotCallID,
		CreatedAt:      session.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:      session.UpdatedAt.UTC().Format(time.RFC3339Nano),
		RequestedAt:    session.RequestedAt.UTC().Format(time.RFC3339Nano),
		CommandSentAt:  optionalTime(session.CommandSentAt),
		JoinedAt:       optionalTime(session.JoinedAt),
		EndedAt:        optionalTime(session.EndedAt),
		LastError:      lastError,
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
			SessionID:        session.ID,
			MeetingURLHash:   session.JoinURLHash,
			Status:           string(session.Status),
			CreatedAt:        session.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt:        session.UpdatedAt.UTC().Format(time.RFC3339Nano),
			LastTranscriptAt: optionalTime(session.LastTranscriptAt),
			BotCallID:        session.BotCallID,
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
