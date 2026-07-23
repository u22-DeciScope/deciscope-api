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
	ListMeetingSessions(ctx context.Context, workspaceID string, limit int) ([]domain.MeetingSession, error)
	EndMeetingSession(ctx context.Context, input application.MeetingSessionEndInput) (*domain.MeetingSession, error)
	UpdateMeetingSessionStatus(ctx context.Context, input application.MeetingSessionStatusUpdateInput) (*domain.MeetingSession, error)
	UpdateMeetingSessionMetadata(ctx context.Context, input application.MeetingSessionMetadataUpdateInput) (*domain.MeetingSession, error)
	CleanupStaleMeetingSessions(ctx context.Context) ([]domain.MeetingSession, error)
	ListMeetingSessionDebug(ctx context.Context, limit int) ([]domain.MeetingSessionDebug, error)
	RecordMeetingSessionHeartbeat(ctx context.Context, sessionID string) (*domain.MeetingSession, error)
	DeleteMeetingSession(ctx context.Context, sessionID string) error
}

type TranscriptListUseCases interface {
	ListTranscriptSegments(ctx context.Context, callID, sessionID string, limit int) ([]domain.TranscriptSegment, error)
}

type MeetingAIAnalysisUseCases interface {
	GetMeetingAIAnalyses(ctx context.Context, sessionID string) (*application.MeetingAIAnalysesSnapshot, error)
	ListFinalSummaryPreviews(ctx context.Context, sessionIDs []string) ([]application.MeetingFinalSummaryPreview, error)
	UpdateAgendaProgressOverride(ctx context.Context, sessionID string, input application.AgendaProgressOverrideInput) (json.RawMessage, error)
}

type MeetingSessionAPI struct {
	service            MeetingSessionUseCases
	transcript         TranscriptListUseCases
	transcriptRealtime http.HandlerFunc
	aiAnalysis         MeetingAIAnalysisUseCases
	metricsStore       *application.BotMediaMetricsStore
	apiKey             string
}

type MeetingSessionAPIOption func(*MeetingSessionAPI)

func WithMeetingSessionTranscriptService(service TranscriptListUseCases) MeetingSessionAPIOption {
	return func(api *MeetingSessionAPI) {
		api.transcript = service
	}
}

func WithMeetingSessionTranscriptRealtime(handler http.HandlerFunc) MeetingSessionAPIOption {
	return func(api *MeetingSessionAPI) {
		api.transcriptRealtime = handler
	}
}

func WithMeetingSessionAIAnalysisService(service MeetingAIAnalysisUseCases) MeetingSessionAPIOption {
	return func(api *MeetingSessionAPI) {
		api.aiAnalysis = service
	}
}

// WithMeetingSessionBotMetricsStore injects the store used to record the
// audio/transcript liveness metrics the bot reports on RecordBotHeartbeat.
// It is optional: when not set, heartbeat bodies are decoded (to validate
// them) and discarded, exactly as before this option existed.
func WithMeetingSessionBotMetricsStore(store *application.BotMediaMetricsStore) MeetingSessionAPIOption {
	return func(api *MeetingSessionAPI) {
		api.metricsStore = store
	}
}

func NewMeetingSessionAPI(service MeetingSessionUseCases, apiKey string, options ...MeetingSessionAPIOption) *MeetingSessionAPI {
	api := &MeetingSessionAPI{service: service, apiKey: apiKey}
	for _, option := range options {
		option(api)
	}
	return api
}

// Create はワークスペースを介さないレガシー作成エンドポイント。
// ブラウザからは呼ばれない前提のため、Bot連携用のAPIキーを必須にする
// (認可なしで誰でもBot参加を起動できてしまうのを防ぐ)。
func (api *MeetingSessionAPI) Create(w http.ResponseWriter, r *http.Request) {
	if !authorizedSecret(r.Header.Get("X-DeciScope-Api-Key"), api.apiKey) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	api.create(w, r, application.MeetingSessionCreateInput{})
}

func (api *MeetingSessionAPI) CreateForWorkspace(w http.ResponseWriter, r *http.Request) {
	api.create(w, r, application.MeetingSessionCreateInput{
		WorkspaceID:     strings.TrimSpace(chi.URLParam(r, "workspace_code")),
		CreatedByUserID: currentUserID(r),
		CreatedByEmail:  currentUserEmail(r),
	})
}

func (api *MeetingSessionAPI) create(w http.ResponseWriter, r *http.Request, defaults application.MeetingSessionCreateInput) {
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}
	var request meetingSessionCreateRequest
	if !decodeLimitedJSON(w, r, meetingSessionBodyLimitBytes, &request) {
		return
	}

	input := defaults
	input.JoinURL = request.JoinURL
	input.Title = request.title()
	input.UserProvidedTitle = request.userProvidedTitle()
	input.CandidateUserIDs = request.candidateUserIDs()
	input.CandidateUserPrincipalNames = request.candidateUserPrincipalNames()
	input.CreatedByMicrosoftUserID = request.createdByMicrosoftUserID()
	if createdByEmail := request.createdByEmail(); createdByEmail != "" || input.CreatedByEmail == "" {
		input.CreatedByEmail = createdByEmail
	}
	input.OrganizerUserID = request.organizerUserID()
	input.Purpose = request.purpose()
	input.Context = request.context()
	input.Agenda = request.agenda()
	input.DecisionPoints = request.decisionPoints()
	input.Concerns = request.concerns()
	input.ExpectedOutput = request.expectedOutput()
	input.CustomInstruction = request.customInstruction()
	result, err := api.service.CreateMeetingSession(r.Context(), input)
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
		WorkspaceID:         session.WorkspaceID,
		CreatedByUserID:     session.CreatedByUserID,
		MeetingID:           session.MeetingID,
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
		Purpose:             session.Purpose,
		Context:             session.Context,
		Agenda:              session.Agenda,
		DecisionPoints:      session.DecisionPoints,
		Concerns:            session.Concerns,
		ExpectedOutput:      session.ExpectedOutput,
		CustomInstruction:   session.CustomInstruction,
		Status:              string(session.Status),
		MeetingURLHash:      session.JoinURLHash,
		Reused:              result.Reused,
		CreatedAt:           session.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:           session.UpdatedAt.UTC().Format(time.RFC3339Nano),
		BotCallID:           session.BotCallID,
	})
	log.Printf("Meeting session create response sent. sessionId=%s joinUrlHash=%s status=%s title=%q titleSource=%s reused=%t httpStatus=%d", session.ID, session.JoinURLHash, session.Status, session.Title, session.TitleSource, result.Reused, status)
}

// Get はワークスペースを介さないレガシー取得エンドポイント。APIキー必須。
func (api *MeetingSessionAPI) Get(w http.ResponseWriter, r *http.Request) {
	if !authorizedSecret(r.Header.Get("X-DeciScope-Api-Key"), api.apiKey) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	session, err := api.service.GetMeetingSession(r.Context(), sessionID)
	if err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
	log.Printf("Meeting session get response sent. sessionId=%s joinUrlHash=%s status=%s title=%q titleSource=%s botCallId=%s updatedAt=%s", session.ID, session.JoinURLHash, session.Status, session.Title, session.TitleSource, session.BotCallID, session.UpdatedAt.UTC().Format(time.RFC3339Nano))
}

func (api *MeetingSessionAPI) ListForWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspace_code"))
	limit, err := parseMeetingSessionDebugLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	sessions, err := api.service.ListMeetingSessions(r.Context(), workspaceID, limit)
	if err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meetingSessionListResponse{Items: meetingSessionResponsesFromDomain(sessions)})
}

// GetWorkspaceFinalSummaryPreviews bulk-fetches a short preview of each
// finished meeting's AI final summary for the workspace's meeting list, so
// the dashboard/history cards don't need one request per session. Sessions
// without a completed final summary yet are simply absent from the result.
func (api *MeetingSessionAPI) GetWorkspaceFinalSummaryPreviews(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspace_code"))
	if api.aiAnalysis == nil {
		writeJSON(w, http.StatusOK, meetingFinalSummaryPreviewListResponse{Items: []meetingFinalSummaryPreviewResponse{}})
		return
	}
	sessions, err := api.service.ListMeetingSessions(r.Context(), workspaceID, 500)
	if err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	sessionIDs := make([]string, 0, len(sessions))
	for _, session := range sessions {
		sessionIDs = append(sessionIDs, session.ID)
	}
	previews, err := api.aiAnalysis.ListFinalSummaryPreviews(r.Context(), sessionIDs)
	if err != nil {
		log.Printf("Workspace final summary previews fetch failed. workspaceId=%s error=%v", workspaceID, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, meetingFinalSummaryPreviewListResponse{
		Items: meetingFinalSummaryPreviewResponsesFromDomain(previews),
	})
}

func (api *MeetingSessionAPI) GetForWorkspace(w http.ResponseWriter, r *http.Request) {
	session, ok := api.workspaceMeetingSession(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
}

// End はワークスペースを介さないレガシー終了エンドポイント。APIキー必須。
func (api *MeetingSessionAPI) End(w http.ResponseWriter, r *http.Request) {
	if !authorizedSecret(r.Header.Get("X-DeciScope-Api-Key"), api.apiKey) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	api.end(w, r)
}

func (api *MeetingSessionAPI) end(w http.ResponseWriter, r *http.Request) {
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}
	var request meetingSessionEndRequest
	if !decodeLimitedJSONAllowUnknown(w, r, meetingSessionBodyLimitBytes, &request) {
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	log.Printf("Meeting session end requested. sessionId=%s reason=%s", sessionID, request.reason())
	session, err := api.service.EndMeetingSession(r.Context(), application.MeetingSessionEndInput{
		SessionID: sessionID,
		Reason:    request.reason(),
	})
	if err != nil {
		writeMeetingSessionEndError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
	log.Printf("Meeting session end response sent. sessionId=%s joinUrlHash=%s status=%s botCallId=%s updatedAt=%s", session.ID, session.JoinURLHash, session.Status, session.BotCallID, session.UpdatedAt.UTC().Format(time.RFC3339Nano))
}

func (api *MeetingSessionAPI) EndForWorkspace(w http.ResponseWriter, r *http.Request) {
	if _, ok := api.workspaceMeetingSession(w, r); !ok {
		return
	}
	api.end(w, r)
}

// DeleteForWorkspace permanently deletes a finished meeting session from the
// workspace's history. Only terminal sessions can be deleted; the service
// rejects anything still active with ErrInvalidArgument.
func (api *MeetingSessionAPI) DeleteForWorkspace(w http.ResponseWriter, r *http.Request) {
	session, ok := api.workspaceMeetingSession(w, r)
	if !ok {
		return
	}
	if err := api.service.DeleteMeetingSession(r.Context(), session.ID); err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	log.Printf("Meeting session deleted via workspace API. sessionId=%s workspaceId=%s", session.ID, session.WorkspaceID)
	w.WriteHeader(http.StatusNoContent)
}

func (api *MeetingSessionAPI) ListWorkspaceTranscriptSegments(w http.ResponseWriter, r *http.Request) {
	session, ok := api.workspaceMeetingSession(w, r)
	if !ok {
		return
	}
	if api.transcript == nil {
		writeError(w, http.StatusServiceUnavailable, "transcript_unavailable", "transcript service is unavailable")
		return
	}
	limit, err := parseTranscriptLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	segments, err := api.transcript.ListTranscriptSegments(r.Context(), "", session.ID, limit)
	if err != nil {
		log.Printf("Workspace transcript list failed. workspaceId=%s sessionId=%s limit=%d error=%v", session.WorkspaceID, session.ID, limit, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, transcriptSegmentListResponse{Items: transcriptSegmentItems(segments)})
}

func (api *MeetingSessionAPI) StreamWorkspaceTranscriptSegments(w http.ResponseWriter, r *http.Request) {
	session, ok := api.workspaceMeetingSession(w, r)
	if !ok {
		return
	}
	if api.transcriptRealtime == nil {
		writeError(w, http.StatusServiceUnavailable, "transcript_stream_unavailable", "transcript stream is unavailable")
		return
	}
	log.Printf("Workspace transcript websocket request received. path=%s workspaceId=%s sessionId=%s origin=%s remoteAddr=%s", r.URL.Path, session.WorkspaceID, session.ID, r.Header.Get("Origin"), r.RemoteAddr)
	cloned := r.Clone(r.Context())
	query := cloned.URL.Query()
	query.Set("sessionId", session.ID)
	query.Del("callId")
	query.Del("token")
	cloned.URL.RawQuery = query.Encode()
	log.Printf("Workspace transcript websocket request forwarding. path=%s workspaceId=%s sessionId=%s origin=%s", cloned.URL.Path, session.WorkspaceID, session.ID, cloned.Header.Get("Origin"))
	api.transcriptRealtime(w, cloned)
}

func (api *MeetingSessionAPI) GetWorkspaceAIAnalyses(w http.ResponseWriter, r *http.Request) {
	session, ok := api.workspaceMeetingSession(w, r)
	if !ok {
		return
	}
	if api.aiAnalysis == nil {
		writeError(w, http.StatusServiceUnavailable, "ai_analysis_unavailable", "AI analysis service is unavailable")
		return
	}
	snapshot, err := api.aiAnalysis.GetMeetingAIAnalyses(r.Context(), session.ID)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidArgument) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		log.Printf("Workspace AI analyses fetch failed. workspaceId=%s sessionId=%s error=%v", session.WorkspaceID, session.ID, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, meetingAIAnalysesResponseFromSnapshot(session.ID, snapshot))
}

// UpdateAgendaProgressForWorkspace applies exactly one manual agenda-progress
// override (a per-entry status override, or a current-topic override) and
// returns the freshly stamped agendaProgress projection. Role enforcement
// (admin/owner only) is the router's requireWorkspaceAdminOrOwner middleware.
func (api *MeetingSessionAPI) UpdateAgendaProgressForWorkspace(w http.ResponseWriter, r *http.Request) {
	session, ok := api.workspaceMeetingSession(w, r)
	if !ok {
		return
	}
	if api.aiAnalysis == nil {
		writeError(w, http.StatusServiceUnavailable, "ai_analysis_unavailable", "AI analysis service is unavailable")
		return
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}
	var request agendaProgressOverrideRequest
	if !decodeLimitedJSON(w, r, meetingSessionBodyLimitBytes, &request) {
		return
	}
	input, err := request.toOverrideInput()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	stamped, err := api.aiAnalysis.UpdateAgendaProgressOverride(r.Context(), session.ID, input)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidArgument) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		log.Printf("Agenda progress override update failed. workspaceId=%s sessionId=%s error=%v", session.WorkspaceID, session.ID, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, agendaProgressOverrideResponse{AgendaProgress: stamped})
}

type agendaProgressOverrideResponse struct {
	AgendaProgress json.RawMessage `json:"agendaProgress"`
}

// agendaProgressOverrideRequest accepts exactly one operation (§1.3):
//   - {"entryId": "...", "manualStatus": "discussed"|null}
//   - {"manualCurrentTopicId": "..."|null}
//
// ManualStatus/ManualCurrentTopicID are kept as json.RawMessage so an
// explicit JSON null (clear the override) can be distinguished from the
// field being entirely absent (not this operation).
type agendaProgressOverrideRequest struct {
	EntryID              string          `json:"entryId,omitempty"`
	ManualStatus         json.RawMessage `json:"manualStatus,omitempty"`
	ManualCurrentTopicID json.RawMessage `json:"manualCurrentTopicId,omitempty"`
}

func (request agendaProgressOverrideRequest) toOverrideInput() (application.AgendaProgressOverrideInput, error) {
	entryID := strings.TrimSpace(request.EntryID)
	hasStatusOp := len(request.ManualStatus) > 0
	hasCurrentOp := len(request.ManualCurrentTopicID) > 0
	if hasStatusOp == hasCurrentOp {
		return application.AgendaProgressOverrideInput{}, errors.New("exactly one of manualStatus or manualCurrentTopicId is required")
	}
	if hasStatusOp {
		if entryID == "" {
			return application.AgendaProgressOverrideInput{}, errors.New("entryId is required with manualStatus")
		}
		if string(request.ManualStatus) == "null" {
			cleared := ""
			return application.AgendaProgressOverrideInput{EntryID: entryID, ManualStatus: &cleared}, nil
		}
		var status string
		if err := json.Unmarshal(request.ManualStatus, &status); err != nil || strings.TrimSpace(status) == "" {
			return application.AgendaProgressOverrideInput{}, errors.New("manualStatus must be a status string or null")
		}
		return application.AgendaProgressOverrideInput{EntryID: entryID, ManualStatus: &status}, nil
	}
	if entryID != "" {
		return application.AgendaProgressOverrideInput{}, errors.New("entryId cannot be combined with manualCurrentTopicId")
	}
	if string(request.ManualCurrentTopicID) == "null" {
		return application.AgendaProgressOverrideInput{ManualCurrentSet: true}, nil
	}
	var currentID string
	if err := json.Unmarshal(request.ManualCurrentTopicID, &currentID); err != nil || strings.TrimSpace(currentID) == "" {
		return application.AgendaProgressOverrideInput{}, errors.New("manualCurrentTopicId must be an id string or null")
	}
	return application.AgendaProgressOverrideInput{ManualCurrentSet: true, ManualCurrentID: currentID}, nil
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
	if request.LastFinalSequenceNo < 0 || request.LastFinalSequenceNoSnake < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "lastFinalSequenceNo must be 0 or greater")
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
		SessionID:                     sessionID,
		Status:                        domain.MeetingSessionStatus(request.Status),
		BotCallID:                     request.botCallID(),
		Message:                       request.Message,
		Reason:                        request.reason(),
		ErrorCode:                     request.errorCode(),
		Source:                        request.source(),
		Title:                         request.title(),
		TitleSource:                   request.titleSource(),
		BotLastForwardedFinalSequence: request.lastFinalSequenceNo(),
		TranscriptQueueDrained:        request.TranscriptQueueDrained || request.TranscriptQueueDrainedSnake,
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

// RecordBotHeartbeat receives a periodic liveness ping from the bot. It never
// changes status and never publishes a WebSocket event (heartbeats arrive too
// often, e.g. every 20s, for that to be anything but spam); the watchdog is
// what turns silence into a bot_health_changed event or an ended session.
//
// The body is optional (see docs/api.md): when the request has no body at
// all (Content-Length == 0), the Content-Type check and JSON decoding are
// skipped entirely so a bodyless POST succeeds. This mirrors chi's
// AllowContentType middleware, which likewise only enforces Content-Type
// when a body is present. When a body is sent, it must still be valid JSON
// with the expected content type. Besides botCallId (read and discarded, as
// before), the body may optionally carry audio/transcript liveness metrics;
// when metricsStore is configured and the body actually contains at least
// one such metric, they are recorded for the watchdog's transcript health
// classification (see BotMediaMetricsStore). A bodyless heartbeat, or one
// with only botCallId, does not touch previously recorded metrics — they
// simply age out of freshness on their own.
func (api *MeetingSessionAPI) RecordBotHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !authorizedSecret(r.Header.Get("X-DeciScope-Api-Key"), api.apiKey) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	var request meetingSessionHeartbeatRequest
	if r.ContentLength != 0 {
		if !isJSONContentType(r.Header.Get("Content-Type")) {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		if !decodeLimitedJSONAllowUnknown(w, r, meetingSessionBodyLimitBytes, &request) {
			return
		}
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	if api.metricsStore != nil {
		if metrics, ok := request.botMediaMetrics(); ok {
			api.metricsStore.Record(sessionID, metrics)
		}
	}
	session, err := api.service.RecordMeetingSessionHeartbeat(r.Context(), sessionID)
	if err != nil {
		writeMeetingSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meetingSessionResponseFromDomain(*session))
}

type meetingSessionHeartbeatRequest struct {
	BotCallID                        string  `json:"botCallId"`
	LastAudioFrameAtUTC              string  `json:"lastAudioFrameAtUtc"`
	LastNonZeroAudioAtUTC            string  `json:"lastNonZeroAudioAtUtc"`
	LastNonEmptyTranscriptAtUTC      string  `json:"lastNonEmptyTranscriptAtUtc"`
	LastFinalTranscriptAtUTC         string  `json:"lastFinalTranscriptAtUtc"`
	LastPeakAmplitude                int     `json:"lastPeakAmplitude"`
	LastRmsAmplitude                 float64 `json:"lastRmsAmplitude"`
	AudioFrameCount                  int64   `json:"audioFrameCount"`
	FramesSinceLastNonZeroAudio      int64   `json:"framesSinceLastNonZeroAudio"`
	SecondsSinceLastNonZeroAudio     int     `json:"secondsSinceLastNonZeroAudio"`
	ActiveSpeakerRecognizerCount     int     `json:"activeSpeakerRecognizerCount"`
	MixedFallbackActive              bool    `json:"mixedFallbackActive"`
	UnmixedAudioSeen                 bool    `json:"unmixedAudioSeen"`
	LastAudioSocketReceiveStallAtUTC string  `json:"lastAudioSocketReceiveStallAtUtc"`
	AudioSocketReceiveStallCount     int64   `json:"audioSocketReceiveStallCount"`
	AudioStalled                     bool    `json:"audioStalled"`
}

// botMediaMetrics builds an application.BotMediaMetrics from the decoded
// heartbeat request. ok reports whether the request actually carried at
// least one audio/transcript metric field; a bare {"botCallId": "..."}
// heartbeat (or no body at all, which decodes to the zero value) must not be
// recorded, so it does not overwrite/refresh previously stored metrics with
// an all-zero value.
func (request meetingSessionHeartbeatRequest) botMediaMetrics() (application.BotMediaMetrics, bool) {
	m := application.BotMediaMetrics{
		LastAudioFrameAt:              parseOptionalRFC3339(request.LastAudioFrameAtUTC),
		LastNonZeroAudioAt:            parseOptionalRFC3339(request.LastNonZeroAudioAtUTC),
		LastNonEmptyTranscriptAt:      parseOptionalRFC3339(request.LastNonEmptyTranscriptAtUTC),
		LastFinalTranscriptAt:         parseOptionalRFC3339(request.LastFinalTranscriptAtUTC),
		LastPeakAmplitude:             request.LastPeakAmplitude,
		LastRmsAmplitude:              request.LastRmsAmplitude,
		AudioFrameCount:               request.AudioFrameCount,
		FramesSinceLastNonZeroAudio:   request.FramesSinceLastNonZeroAudio,
		SecondsSinceLastNonZeroAudio:  request.SecondsSinceLastNonZeroAudio,
		ActiveSpeakerRecognizerCount:  request.ActiveSpeakerRecognizerCount,
		MixedFallbackActive:           request.MixedFallbackActive,
		UnmixedAudioSeen:              request.UnmixedAudioSeen,
		LastAudioSocketReceiveStallAt: parseOptionalRFC3339(request.LastAudioSocketReceiveStallAtUTC),
		AudioSocketReceiveStallCount:  request.AudioSocketReceiveStallCount,
		AudioStalled:                  request.AudioStalled,
	}
	m.HasMetrics = !m.LastAudioFrameAt.IsZero() ||
		!m.LastNonZeroAudioAt.IsZero() ||
		!m.LastNonEmptyTranscriptAt.IsZero() ||
		!m.LastFinalTranscriptAt.IsZero() ||
		m.LastPeakAmplitude != 0 ||
		m.LastRmsAmplitude != 0 ||
		m.AudioFrameCount != 0 ||
		m.FramesSinceLastNonZeroAudio != 0 ||
		m.SecondsSinceLastNonZeroAudio != 0 ||
		m.ActiveSpeakerRecognizerCount != 0 ||
		m.MixedFallbackActive ||
		m.UnmixedAudioSeen ||
		!m.LastAudioSocketReceiveStallAt.IsZero() ||
		m.AudioSocketReceiveStallCount != 0 ||
		m.AudioStalled
	return m, m.HasMetrics
}

// parseOptionalRFC3339 parses an optional RFC3339 timestamp string, treating
// a blank or unparseable value as "not provided" (zero time) rather than an
// error; the heartbeat endpoint must stay lenient about malformed optional
// metrics fields instead of rejecting the whole heartbeat.
func parseOptionalRFC3339(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
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
	Purpose                          string   `json:"purpose"`
	PurposeSnake                     string   `json:"-"`
	Context                          string   `json:"context"`
	ContextSnake                     string   `json:"-"`
	Agenda                           string   `json:"agenda"`
	AgendaSnake                      string   `json:"-"`
	DecisionPoints                   string   `json:"decisionPoints"`
	DecisionPointsSnake              string   `json:"decision_points"`
	Concerns                         string   `json:"concerns"`
	ConcernsSnake                    string   `json:"-"`
	ExpectedOutput                   string   `json:"expectedOutput"`
	ExpectedOutputSnake              string   `json:"expected_output"`
	CustomInstruction                string   `json:"customInstruction"`
	CustomInstructionSnake           string   `json:"custom_instruction"`
}

type meetingSessionCreateResponse struct {
	SessionID           string  `json:"sessionId"`
	WorkspaceID         string  `json:"workspaceId,omitempty"`
	CreatedByUserID     string  `json:"createdByUserId,omitempty"`
	MeetingID           string  `json:"meetingId,omitempty"`
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
	Purpose             string  `json:"purpose,omitempty"`
	Context             string  `json:"context,omitempty"`
	Agenda              string  `json:"agenda,omitempty"`
	DecisionPoints      string  `json:"decisionPoints,omitempty"`
	Concerns            string  `json:"concerns,omitempty"`
	ExpectedOutput      string  `json:"expectedOutput,omitempty"`
	CustomInstruction   string  `json:"customInstruction,omitempty"`
	Status              string  `json:"status"`
	MeetingURLHash      string  `json:"meetingUrlHash,omitempty"`
	Reused              bool    `json:"reused"`
	CreatedAt           string  `json:"createdAt,omitempty"`
	UpdatedAt           string  `json:"updatedAt,omitempty"`
	BotCallID           string  `json:"botCallId,omitempty"`
}

type meetingSessionStatusUpdateRequest struct {
	Status                      string `json:"status"`
	BotCallID                   string `json:"botCallId"`
	BotCallIDSnake              string `json:"bot_call_id"`
	Message                     string `json:"message"`
	FailedReason                string `json:"failedReason"`
	FailedReasonSnake           string `json:"failed_reason"`
	EndReason                   string `json:"endReason"`
	EndReasonSnake              string `json:"end_reason"`
	ErrorCode                   string `json:"errorCode"`
	ErrorCodeSnake              string `json:"error_code"`
	Source                      string `json:"source"`
	Title                       string `json:"title"`
	TitleSnake                  string `json:"meeting_title"`
	TitleSource                 string `json:"titleSource"`
	TitleSourceSnake            string `json:"title_source"`
	LastFinalSequenceNo         int64  `json:"lastFinalSequenceNo"`
	LastFinalSequenceNoSnake    int64  `json:"last_final_sequence_no"`
	TranscriptQueueDrained      bool   `json:"transcriptQueueDrained"`
	TranscriptQueueDrainedSnake bool   `json:"transcript_queue_drained"`
}

type meetingSessionEndRequest struct {
	Reason          string `json:"reason"`
	EndReason       string `json:"endReason"`
	EndReasonSnake  string `json:"end_reason"`
	FailedReason    string `json:"failedReason"`
	FailedReasonRaw string `json:"failed_reason"`
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
	WorkspaceID                 string  `json:"workspaceId,omitempty"`
	CreatedByUserID             string  `json:"createdByUserId,omitempty"`
	MeetingID                   string  `json:"meetingId,omitempty"`
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
	Purpose                     string  `json:"purpose,omitempty"`
	Context                     string  `json:"context,omitempty"`
	Agenda                      string  `json:"agenda,omitempty"`
	DecisionPoints              string  `json:"decisionPoints,omitempty"`
	Concerns                    string  `json:"concerns,omitempty"`
	ExpectedOutput              string  `json:"expectedOutput,omitempty"`
	CustomInstruction           string  `json:"customInstruction,omitempty"`
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

type meetingSessionListResponse struct {
	Items []meetingSessionResponse `json:"items"`
}

type meetingFinalSummaryPreviewResponse struct {
	SessionID string `json:"sessionId"`
	Overview  string `json:"overview"`
}

type meetingFinalSummaryPreviewListResponse struct {
	Items []meetingFinalSummaryPreviewResponse `json:"items"`
}

func meetingFinalSummaryPreviewResponsesFromDomain(previews []application.MeetingFinalSummaryPreview) []meetingFinalSummaryPreviewResponse {
	items := make([]meetingFinalSummaryPreviewResponse, 0, len(previews))
	for _, preview := range previews {
		items = append(items, meetingFinalSummaryPreviewResponse{
			SessionID: preview.SessionID,
			Overview:  preview.Overview,
		})
	}
	return items
}

type meetingSessionDebugListResponse struct {
	Items []meetingSessionDebugResponse `json:"items"`
}

type meetingSessionDebugResponse struct {
	SessionID         string  `json:"sessionId"`
	WorkspaceID       string  `json:"workspaceId,omitempty"`
	CreatedByUserID   string  `json:"createdByUserId,omitempty"`
	MeetingID         string  `json:"meetingId,omitempty"`
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
		WorkspaceID:                 session.WorkspaceID,
		CreatedByUserID:             session.CreatedByUserID,
		MeetingID:                   session.MeetingID,
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
		Purpose:                     session.Purpose,
		Context:                     session.Context,
		Agenda:                      session.Agenda,
		DecisionPoints:              session.DecisionPoints,
		Concerns:                    session.Concerns,
		ExpectedOutput:              session.ExpectedOutput,
		CustomInstruction:           session.CustomInstruction,
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
			WorkspaceID:       session.WorkspaceID,
			CreatedByUserID:   session.CreatedByUserID,
			MeetingID:         session.MeetingID,
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

type meetingAIAnalysesResponse struct {
	SessionID string                     `json:"sessionId"`
	Live      *meetingAIAnalysisResponse `json:"live"`
	Final     *meetingAIAnalysisResponse `json:"final"`
	// Tree is the durable discussion tree snapshot persisted at meeting end.
	// It is null while the meeting is still running.
	Tree         *meetingAIAnalysisResponse `json:"tree"`
	Finalization *meetingAIAnalysisResponse `json:"finalization"`
	// LiveHistory is the durable history of completed live analysis versions
	// (oldest to newest). Always an array, empty when no history exists yet.
	LiveHistory         []meetingAIAnalysisResponse `json:"liveHistory"`
	LiveIntervalSeconds int                         `json:"liveIntervalSeconds"`
}

type meetingAIAnalysisResponse struct {
	AnalysisType string          `json:"analysisType"`
	Status       string          `json:"status"`
	Version      int64           `json:"version"`
	Payload      json.RawMessage `json:"payload"`
	Model        string          `json:"model,omitempty"`
	UpdatedAtUTC string          `json:"updatedAtUtc"`
	Error        string          `json:"error,omitempty"`
}

func meetingAIAnalysesResponseFromSnapshot(sessionID string, snapshot *application.MeetingAIAnalysesSnapshot) meetingAIAnalysesResponse {
	response := meetingAIAnalysesResponse{SessionID: sessionID, LiveHistory: []meetingAIAnalysisResponse{}}
	if snapshot != nil {
		response.Live = meetingAIAnalysisResponseFromDomain(snapshot.Live)
		response.Final = meetingAIAnalysisResponseFromDomain(snapshot.Final)
		response.Tree = meetingAIAnalysisResponseFromDomain(snapshot.Tree)
		response.Finalization = meetingAIAnalysisResponseFromDomain(snapshot.Finalization)
		response.LiveIntervalSeconds = snapshot.LiveIntervalSeconds
		if len(snapshot.LiveHistory) > 0 {
			response.LiveHistory = make([]meetingAIAnalysisResponse, 0, len(snapshot.LiveHistory))
			for i := range snapshot.LiveHistory {
				if converted := meetingAIAnalysisResponseFromDomain(&snapshot.LiveHistory[i]); converted != nil {
					response.LiveHistory = append(response.LiveHistory, *converted)
				}
			}
		}
	}
	return response
}

func meetingAIAnalysisResponseFromDomain(analysis *domain.MeetingAIAnalysis) *meetingAIAnalysisResponse {
	if analysis == nil {
		return nil
	}
	return &meetingAIAnalysisResponse{
		AnalysisType: string(analysis.Type),
		Status:       string(analysis.Status),
		Version:      analysis.Version,
		Payload:      analysis.Payload,
		Model:        analysis.Model,
		UpdatedAtUTC: analysis.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Error:        analysis.LastError,
	}
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

func (request meetingSessionStatusUpdateRequest) lastFinalSequenceNo() int64 {
	if request.LastFinalSequenceNo > 0 {
		return request.LastFinalSequenceNo
	}
	return request.LastFinalSequenceNoSnake
}

func (request meetingSessionEndRequest) reason() string {
	for _, value := range []string{request.Reason, request.EndReason, request.EndReasonSnake, request.FailedReason, request.FailedReasonRaw} {
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

func (request meetingSessionCreateRequest) purpose() string {
	return firstRequestString(request.Purpose, request.PurposeSnake)
}

func (request meetingSessionCreateRequest) context() string {
	return firstRequestString(request.Context, request.ContextSnake)
}

func (request meetingSessionCreateRequest) agenda() string {
	return firstRequestString(request.Agenda, request.AgendaSnake)
}

func (request meetingSessionCreateRequest) decisionPoints() string {
	return firstRequestString(request.DecisionPoints, request.DecisionPointsSnake)
}

func (request meetingSessionCreateRequest) concerns() string {
	return firstRequestString(request.Concerns, request.ConcernsSnake)
}

func (request meetingSessionCreateRequest) expectedOutput() string {
	return firstRequestString(request.ExpectedOutput, request.ExpectedOutputSnake)
}

func (request meetingSessionCreateRequest) customInstruction() string {
	return firstRequestString(request.CustomInstruction, request.CustomInstructionSnake)
}

func firstRequestString(values ...string) string {
	for _, value := range values {
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

func (api *MeetingSessionAPI) workspaceMeetingSession(w http.ResponseWriter, r *http.Request) (*domain.MeetingSession, bool) {
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspace_code"))
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	if workspaceID == "" || sessionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "workspaceId and sessionId are required")
		return nil, false
	}
	session, err := api.service.GetMeetingSession(r.Context(), sessionID)
	if err != nil {
		writeMeetingSessionError(w, err)
		return nil, false
	}
	if session.WorkspaceID != workspaceID {
		log.Printf("Workspace/session mismatch rejected. requestedWorkspaceId=%s sessionWorkspaceId=%s sessionId=%s", workspaceID, session.WorkspaceID, session.ID)
		writeError(w, http.StatusNotFound, "not_found", "meeting session not found")
		return nil, false
	}
	return session, true
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
	if errors.Is(err, domain.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "meeting session not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeMeetingSessionEndError(w http.ResponseWriter, err error) {
	if errors.Is(err, application.ErrBotControlNotConfigured) {
		writeError(w, http.StatusServiceUnavailable, "bot_control_not_configured", "bot control URL or token is not configured")
		return
	}
	if errors.Is(err, application.ErrBotControlCommandFailed) {
		writeError(w, http.StatusBadGateway, "bot_control_command_failed", "failed to send end command to bot control API")
		return
	}
	writeMeetingSessionError(w, err)
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
