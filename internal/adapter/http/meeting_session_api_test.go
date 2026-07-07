package httpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

func TestMeetingSessionAPICreatesSession(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			JoinURLHash: "hash",
			Status:      domain.MeetingSessionCommandSent,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:01Z"),
		},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/meeting-sessions", strings.NewReader(`{"joinUrl":"https://teams.microsoft.com/l/meetup-join/abc","title":"週次定例","candidateUserPrincipalNames":["user@example.com"],"purpose":"意思決定","decision_points":"リリース可否"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()
	api.Create(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.createInput.JoinURL != "https://teams.microsoft.com/l/meetup-join/abc" || service.createInput.Title != "週次定例" || service.createInput.Purpose != "意思決定" || service.createInput.DecisionPoints != "リリース可否" {
		t.Fatalf("createInput = %+v", service.createInput)
	}
	if len(service.createInput.CandidateUserPrincipalNames) != 1 || service.createInput.CandidateUserPrincipalNames[0] != "user@example.com" {
		t.Fatalf("candidate principal names = %#v", service.createInput.CandidateUserPrincipalNames)
	}
	var body meetingSessionCreateResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.SessionID != "session_1" || body.Status != "command_sent" {
		t.Fatalf("body = %+v", body)
	}
}

func TestMeetingSessionAPICreateMapsBotConfigError(t *testing.T) {
	service := &fakeMeetingSessionUseCases{err: application.ErrBotControlNotConfigured}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/meeting-sessions", strings.NewReader(`{"joinUrl":"https://teams.microsoft.com/l/meetup-join/abc"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()
	api.Create(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

// レガシー (非workspace) の作成・取得・終了はAPIキーがないと 401 になる。
// ブラウザからの直接呼び出しでBot参加や会議終了ができてしまうのを防ぐ。
func TestMeetingSessionAPILegacyEndpointsRequireAPIKey(t *testing.T) {
	service := &fakeMeetingSessionUseCases{}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)

	create := httptest.NewRequest(http.MethodPost, "/api/v1/meeting-sessions", strings.NewReader(`{"joinUrl":"https://teams.microsoft.com/l/meetup-join/abc"}`))
	create.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	api.Create(createResp, create)
	if createResp.Code != http.StatusUnauthorized {
		t.Fatalf("Create without api key = %d, want 401", createResp.Code)
	}

	get := requestWithSessionParam(http.MethodGet, "/api/v1/meeting-sessions/session_1", "")
	getResp := httptest.NewRecorder()
	api.Get(getResp, get)
	if getResp.Code != http.StatusUnauthorized {
		t.Fatalf("Get without api key = %d, want 401", getResp.Code)
	}

	end := requestWithSessionParam(http.MethodPost, "/api/v1/meeting-sessions/session_1/end", `{"reason":"manual_end_requested"}`)
	end.Header.Set("Content-Type", "application/json")
	endResp := httptest.NewRecorder()
	api.End(endResp, end)
	if endResp.Code != http.StatusUnauthorized {
		t.Fatalf("End without api key = %d, want 401", endResp.Code)
	}
	if service.createInput.JoinURL != "" || service.endInput.SessionID != "" {
		t.Fatalf("service should not be called without api key: create=%+v end=%+v", service.createInput, service.endInput)
	}
}

func TestMeetingSessionAPIUpdateBotStatusRequiresAPIKey(t *testing.T) {
	api := NewMeetingSessionAPI(&fakeMeetingSessionUseCases{}, testTranscriptAPIKey)
	req := requestWithSessionParam(http.MethodPatch, "/api/v1/bot/meeting-sessions/session_1/status", `{"status":"joined"}`)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	api.UpdateBotStatus(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestMeetingSessionAPIUpdateBotStatus(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			Status:      domain.MeetingSessionJoined,
			BotCallID:   "call-1",
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:01Z"),
			JoinedAt:    mustTime(t, "2026-06-27T00:00:01Z"),
		},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithSessionParam(http.MethodPatch, "/api/v1/bot/meeting-sessions/session_1/status", `{"status":"joined","botCallId":"call-1","message":"joined successfully"}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.UpdateBotStatus(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.update.SessionID != "session_1" || service.update.Status != domain.MeetingSessionJoined || service.update.BotCallID != "call-1" {
		t.Fatalf("update = %+v", service.update)
	}
}

func TestMeetingSessionAPIUpdateBotStatusAcceptsFailureReasonFields(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			Status:      domain.MeetingSessionJoined,
			BotCallID:   "call-1",
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:01Z"),
			JoinedAt:    mustTime(t, "2026-06-27T00:00:01Z"),
		},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithSessionParam(http.MethodPatch, "/api/v1/bot/meeting-sessions/session_1/status", `{
		"status":"failed",
		"bot_call_id":"call-1",
		"failed_reason":"speech_pipeline_not_ready",
		"error_code":"SpeechPipelineNotReady",
		"source":"speech_pipeline",
		"message":"SpeechPipelineReady=False",
		"diagnostic":"ignored extra field"
	}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.UpdateBotStatus(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.update.Status != domain.MeetingSessionFailed ||
		service.update.BotCallID != "call-1" ||
		service.update.Reason != "speech_pipeline_not_ready" ||
		service.update.ErrorCode != "SpeechPipelineNotReady" ||
		service.update.Source != "speech_pipeline" {
		t.Fatalf("update = %+v", service.update)
	}
}

func TestMeetingSessionAPIEndsSession(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			JoinURLHash: "hash",
			Status:      domain.MeetingSessionEnded,
			BotCallID:   "call-1",
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:02Z"),
			EndedAt:     mustTime(t, "2026-06-27T00:00:02Z"),
			EndReason:   "manual_end_requested",
		},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithSessionParam(http.MethodPost, "/api/v1/meeting-sessions/session_1/end", `{"reason":"manual_end_requested"}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.End(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.endInput.SessionID != "session_1" || service.endInput.Reason != "manual_end_requested" {
		t.Fatalf("endInput = %+v", service.endInput)
	}
	var body meetingSessionResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.SessionID != "session_1" || body.Status != "ended" || body.EndedAt == nil {
		t.Fatalf("body = %+v", body)
	}
}

func TestMeetingSessionAPIRecordBotHeartbeatRequiresAPIKey(t *testing.T) {
	api := NewMeetingSessionAPI(&fakeMeetingSessionUseCases{}, testTranscriptAPIKey)
	req := requestWithSessionParam(http.MethodPost, "/api/v1/bot/meeting-sessions/session_1/heartbeat", `{"botCallId":"call-1"}`)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	api.RecordBotHeartbeat(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestMeetingSessionAPIRecordBotHeartbeat(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			Status:      domain.MeetingSessionRecording,
			BotCallID:   "call-1",
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:05:00Z"),
		},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithSessionParam(http.MethodPost, "/api/v1/bot/meeting-sessions/session_1/heartbeat", `{"botCallId":"call-1"}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.RecordBotHeartbeat(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.heartbeatSessionID != "session_1" {
		t.Fatalf("heartbeatSessionID = %q, want session_1", service.heartbeatSessionID)
	}
	var body meetingSessionResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.SessionID != "session_1" || body.Status != "recording" {
		t.Fatalf("body = %+v", body)
	}
}

func TestMeetingSessionAPIRecordBotHeartbeatWithoutBodySucceeds(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			Status:      domain.MeetingSessionRecording,
			BotCallID:   "call-1",
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:05:00Z"),
		},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	router := NewRouter(RouterDependencies{MeetingSessionAPI: api})

	// No body and no Content-Type header at all: chi's AllowContentType
	// middleware and the handler's own check must both let this through
	// since docs/api.md documents the heartbeat body as optional.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bot/meeting-sessions/session_1/heartbeat", nil)
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.heartbeatSessionID != "session_1" {
		t.Fatalf("heartbeatSessionID = %q, want session_1", service.heartbeatSessionID)
	}
}

func TestMeetingSessionAPIRecordBotHeartbeatReturnsNotFoundForUnknownSession(t *testing.T) {
	service := &fakeMeetingSessionUseCases{}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithSessionParam(http.MethodPost, "/api/v1/bot/meeting-sessions/session_1/heartbeat", `{}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.RecordBotHeartbeat(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestMeetingSessionAPIStreamWorkspaceTranscriptSegmentsForwardsSessionID(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			WorkspaceID: "workspace_1",
			Status:      domain.MeetingSessionJoined,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:01Z"),
		},
	}
	var gotPath string
	var gotSessionID string
	var gotCallID string
	api := NewMeetingSessionAPI(
		service,
		testTranscriptAPIKey,
		WithMeetingSessionTranscriptRealtime(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotSessionID = r.URL.Query().Get("sessionId")
			gotCallID = r.URL.Query().Get("callId")
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	req := requestWithWorkspaceSessionParams(http.MethodGet, "/v1/workspaces/workspace_1/meeting-sessions/session_1/transcript-stream?callId=call-ignored", "")
	resp := httptest.NewRecorder()

	api.StreamWorkspaceTranscriptSegments(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if gotPath != "/v1/workspaces/workspace_1/meeting-sessions/session_1/transcript-stream" || gotSessionID != "session_1" || gotCallID != "" {
		t.Fatalf("forwarded path=%q sessionId=%q callId=%q", gotPath, gotSessionID, gotCallID)
	}
}

func TestMeetingSessionAPIGetWorkspaceAIAnalysesReturnsSnapshot(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			WorkspaceID: "workspace_1",
			Status:      domain.MeetingSessionRecording,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:01Z"),
		},
	}
	analysis := &fakeMeetingAIAnalysisUseCases{
		snapshot: &application.MeetingAIAnalysesSnapshot{
			SessionID: "session_1",
			Live: &domain.MeetingAIAnalysis{
				SessionID: "session_1",
				Type:      domain.MeetingAIAnalysisLive,
				Status:    domain.MeetingAIAnalysisCompleted,
				Version:   4,
				Payload:   json.RawMessage(`{"summary":"進行中です"}`),
				Model:     "gpt-4o-mini",
				UpdatedAt: mustTime(t, "2026-06-27T00:00:02Z"),
			},
			LiveIntervalSeconds: 10,
		},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodGet, "/v1/workspaces/workspace_1/meeting-sessions/session_1/ai-analyses", "")
	resp := httptest.NewRecorder()

	api.GetWorkspaceAIAnalyses(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if analysis.gotSessionID != "session_1" {
		t.Fatalf("gotSessionID = %q", analysis.gotSessionID)
	}
	var body meetingAIAnalysesResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.SessionID != "session_1" || body.Live == nil || body.Live.Version != 4 || body.Live.Status != "completed" {
		t.Fatalf("body = %+v", body)
	}
	if body.Final != nil {
		t.Fatalf("body.Final = %+v, want nil", body.Final)
	}
	if !strings.Contains(string(body.Live.Payload), "進行中です") {
		t.Fatalf("body.Live.Payload = %s", string(body.Live.Payload))
	}
	if body.LiveIntervalSeconds != 10 {
		t.Fatalf("body.LiveIntervalSeconds = %d, want 10", body.LiveIntervalSeconds)
	}
}

func TestMeetingSessionAPIGetWorkspaceAIAnalysesReturnsNullsWhenNoAnalysisExists(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			WorkspaceID: "workspace_1",
			Status:      domain.MeetingSessionRecording,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:01Z"),
		},
	}
	analysis := &fakeMeetingAIAnalysisUseCases{snapshot: &application.MeetingAIAnalysesSnapshot{SessionID: "session_1"}}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodGet, "/v1/workspaces/workspace_1/meeting-sessions/session_1/ai-analyses", "")
	resp := httptest.NewRecorder()

	api.GetWorkspaceAIAnalyses(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	var body meetingAIAnalysesResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Live != nil || body.Final != nil {
		t.Fatalf("body = %+v, want nil live/final", body)
	}
}

func TestMeetingSessionAPIGetWorkspaceAIAnalysesReturnsServiceUnavailableWhenNotWired(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			WorkspaceID: "workspace_1",
			Status:      domain.MeetingSessionRecording,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:01Z"),
		},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithWorkspaceSessionParams(http.MethodGet, "/v1/workspaces/workspace_1/meeting-sessions/session_1/ai-analyses", "")
	resp := httptest.NewRecorder()

	api.GetWorkspaceAIAnalyses(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestMeetingSessionAPIGetWorkspaceAIAnalysesReturnsNotFoundForOtherWorkspace(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			WorkspaceID: "workspace_other",
			Status:      domain.MeetingSessionRecording,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:01Z"),
		},
	}
	analysis := &fakeMeetingAIAnalysisUseCases{snapshot: &application.MeetingAIAnalysesSnapshot{SessionID: "session_1"}}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodGet, "/v1/workspaces/workspace_1/meeting-sessions/session_1/ai-analyses", "")
	resp := httptest.NewRecorder()

	api.GetWorkspaceAIAnalyses(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

type fakeMeetingAIAnalysisUseCases struct {
	snapshot     *application.MeetingAIAnalysesSnapshot
	err          error
	gotSessionID string
}

func (f *fakeMeetingAIAnalysisUseCases) GetMeetingAIAnalyses(_ context.Context, sessionID string) (*application.MeetingAIAnalysesSnapshot, error) {
	f.gotSessionID = sessionID
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshot, nil
}

type fakeMeetingSessionUseCases struct {
	session            domain.MeetingSession
	err                error
	createInput        application.MeetingSessionCreateInput
	endInput           application.MeetingSessionEndInput
	update             application.MeetingSessionStatusUpdateInput
	reused             bool
	heartbeatSessionID string
}

func (f *fakeMeetingSessionUseCases) CreateMeetingSession(_ context.Context, input application.MeetingSessionCreateInput) (*application.MeetingSessionCreateResult, error) {
	f.createInput = input
	if f.err != nil {
		if f.session.ID == "" {
			return nil, f.err
		}
		return &application.MeetingSessionCreateResult{Session: &f.session, Reused: f.reused}, f.err
	}
	return &application.MeetingSessionCreateResult{Session: &f.session, Reused: f.reused}, nil
}

func (f *fakeMeetingSessionUseCases) GetMeetingSession(_ context.Context, sessionID string) (*domain.MeetingSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.session.ID == "" {
		return nil, fmt.Errorf("%w: meeting session not found", domain.ErrNotFound)
	}
	return &f.session, nil
}

func (f *fakeMeetingSessionUseCases) ListMeetingSessions(_ context.Context, _ string, _ int) ([]domain.MeetingSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []domain.MeetingSession{f.session}, nil
}

func (f *fakeMeetingSessionUseCases) EndMeetingSession(_ context.Context, input application.MeetingSessionEndInput) (*domain.MeetingSession, error) {
	f.endInput = input
	if f.err != nil {
		return nil, f.err
	}
	return &f.session, nil
}

func (f *fakeMeetingSessionUseCases) UpdateMeetingSessionStatus(_ context.Context, input application.MeetingSessionStatusUpdateInput) (*domain.MeetingSession, error) {
	f.update = input
	if f.err != nil {
		return nil, f.err
	}
	return &f.session, nil
}

func (f *fakeMeetingSessionUseCases) UpdateMeetingSessionMetadata(_ context.Context, input application.MeetingSessionMetadataUpdateInput) (*domain.MeetingSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	if input.Title != "" {
		f.session.Title = input.Title
	}
	if input.TitleSource != "" {
		f.session.TitleSource = input.TitleSource
	}
	f.session.Provider = input.Provider
	f.session.ExternalMeetingID = input.ExternalMeetingID
	f.session.ThreadID = input.ThreadID
	f.session.OrganizerName = input.OrganizerName
	f.session.OrganizerEmail = input.OrganizerEmail
	return &f.session, nil
}

func (f *fakeMeetingSessionUseCases) CleanupStaleMeetingSessions(_ context.Context) ([]domain.MeetingSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []domain.MeetingSession{f.session}, nil
}

func (f *fakeMeetingSessionUseCases) ListMeetingSessionDebug(_ context.Context, _ int) ([]domain.MeetingSessionDebug, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []domain.MeetingSessionDebug{{MeetingSession: f.session}}, nil
}

func (f *fakeMeetingSessionUseCases) RecordMeetingSessionHeartbeat(_ context.Context, sessionID string) (*domain.MeetingSession, error) {
	f.heartbeatSessionID = sessionID
	if f.err != nil {
		return nil, f.err
	}
	if f.session.ID == "" {
		return nil, fmt.Errorf("%w: meeting session not found", domain.ErrNotFound)
	}
	return &f.session, nil
}

func requestWithSessionParam(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("session_id", "session_1")
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}
func requestWithWorkspaceSessionParams(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("workspace_code", "workspace_1")
	routeCtx.URLParams.Add("session_id", "session_1")
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}
