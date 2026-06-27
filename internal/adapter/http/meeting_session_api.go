package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

const meetingSessionBodyLimitBytes int64 = 64 * 1024

type MeetingSessionUseCases interface {
	CreateMeetingSession(ctx context.Context, joinURL string) (*domain.MeetingSession, error)
	GetMeetingSession(ctx context.Context, sessionID string) (*domain.MeetingSession, error)
	UpdateMeetingSessionStatus(ctx context.Context, input application.MeetingSessionStatusUpdateInput) (*domain.MeetingSession, error)
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

	session, err := api.service.CreateMeetingSession(r.Context(), request.JoinURL)
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
	log.Printf("Meeting session created. sessionId=%s joinUrlHash=%s status=%s", session.ID, session.JoinURLHash, session.Status)
	writeJSON(w, http.StatusCreated, meetingSessionCreateResponse{
		SessionID: session.ID,
		Status:    string(session.Status),
	})
}

func (api *MeetingSessionAPI) Get(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	session, err := api.service.GetMeetingSession(r.Context(), sessionID)
	if err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
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
	log.Printf("Meeting session status updated by bot. sessionId=%s status=%s botCallId=%s", session.ID, session.Status, session.BotCallID)
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
}

type meetingSessionCreateRequest struct {
	JoinURL string `json:"joinUrl"`
}

type meetingSessionCreateResponse struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
}

type meetingSessionStatusUpdateRequest struct {
	Status    string `json:"status"`
	BotCallID string `json:"botCallId"`
	Message   string `json:"message"`
}

type meetingSessionResponse struct {
	SessionID     string  `json:"sessionId"`
	Status        string  `json:"status"`
	BotCallID     string  `json:"botCallId,omitempty"`
	CreatedAt     string  `json:"createdAt"`
	RequestedAt   string  `json:"requestedAt"`
	CommandSentAt *string `json:"commandSentAt"`
	JoinedAt      *string `json:"joinedAt"`
	EndedAt       *string `json:"endedAt"`
	LastError     *string `json:"lastError"`
}

func meetingSessionResponseFromDomain(session domain.MeetingSession) meetingSessionResponse {
	lastError := optionalString(session.LastError)
	return meetingSessionResponse{
		SessionID:     session.ID,
		Status:        string(session.Status),
		BotCallID:     session.BotCallID,
		CreatedAt:     session.CreatedAt.UTC().Format(time.RFC3339Nano),
		RequestedAt:   session.RequestedAt.UTC().Format(time.RFC3339Nano),
		CommandSentAt: optionalTime(session.CommandSentAt),
		JoinedAt:      optionalTime(session.JoinedAt),
		EndedAt:       optionalTime(session.EndedAt),
		LastError:     lastError,
	}
}

func optionalTime(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
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
