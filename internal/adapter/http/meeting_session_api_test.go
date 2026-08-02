package httpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestMeetingSessionAPIUpdateBotStatusAcceptsTranscriptDrainProof(t *testing.T) {
	service := &fakeMeetingSessionUseCases{session: domain.MeetingSession{
		ID: "session_1", Status: domain.MeetingSessionEnding,
		RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"), CreatedAt: mustTime(t, "2026-06-27T00:00:00Z"), UpdatedAt: mustTime(t, "2026-06-27T00:00:01Z"),
	}}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithSessionParam(http.MethodPatch, "/api/v1/bot/meeting-sessions/session_1/status", `{
		"status":"ended","lastFinalSequenceNo":27,"transcriptQueueDrained":true
	}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.UpdateBotStatus(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.update.BotLastForwardedFinalSequence != 27 || !service.update.TranscriptQueueDrained {
		t.Fatalf("update drain proof = %+v", service.update)
	}
}

func TestMeetingSessionAPIUpdateBotStatusRejectsNegativeFinalSequence(t *testing.T) {
	service := &fakeMeetingSessionUseCases{}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithSessionParam(http.MethodPatch, "/api/v1/bot/meeting-sessions/session_1/status", `{"status":"ended","lastFinalSequenceNo":-1}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.UpdateBotStatus(resp, req)

	if resp.Code != http.StatusBadRequest || service.update.SessionID != "" {
		t.Fatalf("response = %d %s, update=%+v", resp.Code, resp.Body.String(), service.update)
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

func TestMeetingSessionAPIDeleteForWorkspaceDeletesSession(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			WorkspaceID: "workspace_1",
			Status:      domain.MeetingSessionEnded,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:02Z"),
		},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithWorkspaceSessionParams(http.MethodDelete, "/v1/workspaces/workspace_1/meeting-sessions/session_1", "")
	resp := httptest.NewRecorder()

	api.DeleteForWorkspace(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.deletedSessionID != "session_1" {
		t.Fatalf("deletedSessionID = %q, want session_1", service.deletedSessionID)
	}
}

func TestMeetingSessionAPIDeleteForWorkspaceRejectsWorkspaceMismatch(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			WorkspaceID: "workspace_other",
			Status:      domain.MeetingSessionEnded,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:02Z"),
		},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithWorkspaceSessionParams(http.MethodDelete, "/v1/workspaces/workspace_1/meeting-sessions/session_1", "")
	resp := httptest.NewRecorder()

	api.DeleteForWorkspace(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.deletedSessionID != "" {
		t.Fatalf("service should not be called on workspace mismatch, deletedSessionID=%q", service.deletedSessionID)
	}
}

func TestMeetingSessionAPIDeleteForWorkspaceMapsServiceRejection(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			WorkspaceID: "workspace_1",
			Status:      domain.MeetingSessionActive,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:02Z"),
		},
		deleteErr: fmt.Errorf("%w: meeting session is not finished yet", domain.ErrInvalidArgument),
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithWorkspaceSessionParams(http.MethodDelete, "/v1/workspaces/workspace_1/meeting-sessions/session_1", "")
	resp := httptest.NewRecorder()

	api.DeleteForWorkspace(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
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

func TestMeetingSessionAPIRecordBotHeartbeatRecordsBotMediaMetrics(t *testing.T) {
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
	metricsStore := application.NewBotMediaMetricsStore()
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionBotMetricsStore(metricsStore))
	body := `{
		"botCallId": "call-1",
		"lastAudioFrameAtUtc": "2026-06-27T00:04:58Z",
		"lastNonZeroAudioAtUtc": "2026-06-27T00:04:50Z",
		"lastNonEmptyTranscriptAtUtc": "2026-06-27T00:04:30Z",
		"audioStalled": true,
		"audioSocketReceiveStallCount": 3,
		"speechPipelineReady": true,
		"speechStarted": true,
		"speechAcceptingFrames": true,
		"recognizerCreated": true,
		"pushStreamOpen": true,
		"pipelineGeneration": 4,
		"recognizerInstanceIdHash": "abc123",
		"lastRecognizerStartedAtUtc": "2026-06-27T00:04:20Z",
		"lastSpeechPartialAtUtc": "2026-06-27T00:04:29Z",
		"lastSpeechFinalAtUtc": "2026-06-27T00:04:30Z"
	}`
	req := requestWithSessionParam(http.MethodPost, "/api/v1/bot/meeting-sessions/session_1/heartbeat", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.RecordBotHeartbeat(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	metrics, ok := metricsStore.Get("session_1")
	if !ok {
		t.Fatalf("metrics store should have recorded session_1's bot media metrics")
	}
	if !metrics.HasMetrics || !metrics.AudioStalled || metrics.AudioSocketReceiveStallCount != 3 {
		t.Fatalf("recorded metrics = %+v, want HasMetrics=true AudioStalled=true AudioSocketReceiveStallCount=3", metrics)
	}
	if metrics.LastAudioFrameAt.IsZero() || metrics.LastNonZeroAudioAt.IsZero() || metrics.LastNonEmptyTranscriptAt.IsZero() {
		t.Fatalf("recorded metrics timestamps not parsed: %+v", metrics)
	}
	if !metrics.HasSpeechPipelineMetrics || !metrics.SpeechPipelineReady || !metrics.SpeechStarted ||
		!metrics.SpeechAcceptingFrames || !metrics.RecognizerCreated || !metrics.PushStreamOpen ||
		metrics.PipelineGeneration != 4 || metrics.RecognizerInstanceIDHash != "abc123" {
		t.Fatalf("recorded pipeline metrics = %+v", metrics)
	}
	if metrics.LastRecognizerStartedAt.IsZero() || metrics.LastSpeechPartialAt.IsZero() || metrics.LastSpeechFinalAt.IsZero() {
		t.Fatalf("recorded pipeline timestamps not parsed: %+v", metrics)
	}
	if metrics.ReceivedAt.IsZero() {
		t.Fatalf("recorded metrics ReceivedAt should be stamped by the store, got zero value")
	}
}

func TestMeetingSessionAPIRecordBotHeartbeatRecordsExplicitFalsePipelineFlags(t *testing.T) {
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
	metricsStore := application.NewBotMediaMetricsStore()
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionBotMetricsStore(metricsStore))
	req := requestWithSessionParam(
		http.MethodPost,
		"/api/v1/bot/meeting-sessions/session_1/heartbeat",
		`{"botCallId":"call-1","speechPipelineReady":false,"speechStarted":false,"recognizerCreated":false,"pushStreamOpen":false}`,
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.RecordBotHeartbeat(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	metrics, ok := metricsStore.Get("session_1")
	if !ok || !metrics.HasMetrics || !metrics.HasSpeechPipelineMetrics {
		t.Fatalf("explicit false pipeline metrics were not preserved: ok=%t metrics=%+v", ok, metrics)
	}
}

func TestMeetingSessionAPIRecordBotHeartbeatBareBodyDoesNotRecordMetrics(t *testing.T) {
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
	metricsStore := application.NewBotMediaMetricsStore()
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionBotMetricsStore(metricsStore))
	req := requestWithSessionParam(http.MethodPost, "/api/v1/bot/meeting-sessions/session_1/heartbeat", `{"botCallId":"call-1"}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.RecordBotHeartbeat(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if _, ok := metricsStore.Get("session_1"); ok {
		t.Fatalf("a heartbeat with only botCallId (no audio/transcript metrics) must not be recorded")
	}
}

func TestMeetingSessionAPIRecordsAndReloadsTransientMediaHealth(t *testing.T) {
	service := &fakeMeetingSessionUseCases{session: domain.MeetingSession{
		ID: "session_1", WorkspaceID: "workspace_1", Status: domain.MeetingSessionRecording,
		BotCallID: "call-1",
	}}
	mediaHealth := application.NewBotMediaHealthService(nil)
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionBotMediaHealth(mediaHealth))
	requestBody := `{
		"eventId":"audio-stall:call-1:1:started",
		"botCallId":"call-1",
		"state":"audio_receive_stalled",
		"event":"started",
		"occurredAtUtc":"2026-08-01T00:50:25Z",
		"startedAtUtc":"2026-08-01T00:50:20Z",
		"lastAudioFrameAtUtc":"2026-08-01T00:50:20Z",
		"durationMs":5000,
		"source":"audio_frame_watchdog"
	}`
	req := requestWithSessionParam(http.MethodPost, "/api/v1/bot/meeting-sessions/session_1/media-health", requestBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.RecordBotMediaHealth(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("record response = %d %s", resp.Code, resp.Body.String())
	}

	getReq := requestWithWorkspaceSessionParams(http.MethodGet, "/v1/workspaces/workspace_1/meeting-sessions/session_1/media-health", "")
	getResp := httptest.NewRecorder()
	api.GetWorkspaceBotMediaHealth(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get response = %d %s", getResp.Code, getResp.Body.String())
	}
	var got application.BotMediaHealthState
	if err := json.Unmarshal(getResp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode media health: %v", err)
	}
	if got.SessionID != "session_1" || got.State != application.BotMediaHealthAudioReceiveStalled ||
		got.Event != application.BotMediaHealthEventStarted || got.DurationMilliseconds != 5000 {
		t.Fatalf("media health = %+v", got)
	}
	if service.update.SessionID != "" || service.endInput.SessionID != "" {
		t.Fatalf("media health must not change meeting lifecycle: update=%+v end=%+v", service.update, service.endInput)
	}
}

func TestMeetingSessionAPIRejectsMediaHealthFromDifferentBotCall(t *testing.T) {
	service := &fakeMeetingSessionUseCases{session: domain.MeetingSession{
		ID: "session_1", Status: domain.MeetingSessionRecording, BotCallID: "call-current",
	}}
	api := NewMeetingSessionAPI(
		service,
		testTranscriptAPIKey,
		WithMeetingSessionBotMediaHealth(application.NewBotMediaHealthService(nil)),
	)
	req := requestWithSessionParam(http.MethodPost, "/api/v1/bot/meeting-sessions/session_1/media-health", `{
		"botCallId":"call-stale","state":"audio_receive_stalled","event":"started"
	}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Api-Key", testTranscriptAPIKey)
	resp := httptest.NewRecorder()

	api.RecordBotMediaHealth(resp, req)

	if resp.Code != http.StatusConflict {
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
	var gotToken string
	api := NewMeetingSessionAPI(
		service,
		testTranscriptAPIKey,
		WithMeetingSessionTranscriptRealtime(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotSessionID = r.URL.Query().Get("sessionId")
			gotCallID = r.URL.Query().Get("callId")
			gotToken = r.URL.Query().Get("token")
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	req := requestWithWorkspaceSessionParams(http.MethodGet, "/v1/workspaces/workspace_1/meeting-sessions/session_1/transcript-stream?callId=call-ignored&token=must-not-forward", "")
	resp := httptest.NewRecorder()

	api.StreamWorkspaceTranscriptSegments(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if gotPath != "/v1/workspaces/workspace_1/meeting-sessions/session_1/transcript-stream" || gotSessionID != "session_1" || gotCallID != "" || gotToken != "" {
		t.Fatalf("forwarded path=%q sessionId=%q callId=%q", gotPath, gotSessionID, gotCallID)
	}
}

func TestMeetingSessionAPIGetWorkspaceAIAnalysesReturnsSnapshot(t *testing.T) {
	relationSentinelPayload := json.RawMessage(`{"summary":"進行中です","items":[{"id":"item-source","kind":"issue","title":"原因仮説","labelResolution":{"status":"fallback_applied","reason":"context_dependent","sourceEvidenceSequenceNos":[16,17]}}],"tree":{"nodes":[{"id":"item-source","kind":"issue","label":"原因仮説","labelResolution":{"status":"fallback_applied","reason":"context_dependent","sourceEvidenceSequenceNos":[16,17]}}],"edges":[],"relations":[{"id":"relation-sentinel-rest-v1","source":"item-source","target":"item-target","kind":"refines","confidence":0.73125,"evidenceSequenceNos":[17,29],"origin":"transport_sentinel","status":"active","createdAtVersion":41,"updatedAtVersion":43}]}}`)
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
				Payload:   relationSentinelPayload,
				Model:     "gpt-4o-mini",
				UpdatedAt: mustTime(t, "2026-06-27T00:00:02Z"),
			},
			Finalization: &domain.MeetingAIAnalysis{
				SessionID: "session_1", Type: domain.MeetingAIAnalysisFinalization,
				Status: domain.MeetingAIAnalysisRunning, Version: 1,
				Payload:   json.RawMessage(`{"stage":"final_analysis_running","finalizationTargetSequence":27}`),
				UpdatedAt: mustTime(t, "2026-06-27T00:00:03Z"),
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
	if body.Finalization == nil || body.Finalization.Status != "running" {
		t.Fatalf("body.Finalization = %+v, want running", body.Finalization)
	}
	if !strings.Contains(string(body.Live.Payload), "進行中です") {
		t.Fatalf("body.Live.Payload = %s", string(body.Live.Payload))
	}
	var wantPayload, gotPayload any
	if err := json.Unmarshal(relationSentinelPayload, &wantPayload); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body.Live.Payload, &gotPayload); err != nil || !reflect.DeepEqual(gotPayload, wantPayload) {
		t.Fatalf("REST relation sentinel changed: got=%s want=%s err=%v", body.Live.Payload, relationSentinelPayload, err)
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

func meetingSessionForAgendaProgress(t *testing.T) *fakeMeetingSessionUseCases {
	t.Helper()
	return &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			WorkspaceID: "workspace_1",
			Status:      domain.MeetingSessionRecording,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:01Z"),
		},
	}
}

func TestMeetingSessionAPIUpdateAgendaProgressSetsManualStatus(t *testing.T) {
	service := meetingSessionForAgendaProgress(t)
	analysis := &fakeMeetingAIAnalysisUseCases{overrideResult: json.RawMessage(`{"computedCurrentTopicId":"agenda-1","entries":[]}`)}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodPatch, "/v1/workspaces/workspace_1/meeting-sessions/session_1/agenda-progress", `{"entryId":"agenda-1","manualStatus":"discussed"}`)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	api.UpdateAgendaProgressForWorkspace(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if analysis.gotOverrideSessionID != "session_1" {
		t.Fatalf("gotOverrideSessionID = %q", analysis.gotOverrideSessionID)
	}
	if analysis.gotOverrideInput.EntryID != "agenda-1" || analysis.gotOverrideInput.ManualStatus == nil || *analysis.gotOverrideInput.ManualStatus != "discussed" || analysis.gotOverrideInput.ManualCurrentSet {
		t.Fatalf("gotOverrideInput = %+v", analysis.gotOverrideInput)
	}
	var body agendaProgressOverrideResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(string(body.AgendaProgress), "agenda-1") {
		t.Fatalf("body.AgendaProgress = %s", string(body.AgendaProgress))
	}
}

func TestMeetingSessionAPIUpdateAgendaProgressClearsManualStatus(t *testing.T) {
	service := meetingSessionForAgendaProgress(t)
	analysis := &fakeMeetingAIAnalysisUseCases{overrideResult: json.RawMessage(`{"entries":[]}`)}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodPatch, "/v1/workspaces/workspace_1/meeting-sessions/session_1/agenda-progress", `{"entryId":"agenda-1","manualStatus":null}`)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	api.UpdateAgendaProgressForWorkspace(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if analysis.gotOverrideInput.ManualStatus == nil || *analysis.gotOverrideInput.ManualStatus != "" {
		t.Fatalf("gotOverrideInput.ManualStatus = %v, want pointer to empty string", analysis.gotOverrideInput.ManualStatus)
	}
}

func TestMeetingSessionAPIUpdateAgendaProgressSetsManualCurrentTopic(t *testing.T) {
	service := meetingSessionForAgendaProgress(t)
	analysis := &fakeMeetingAIAnalysisUseCases{overrideResult: json.RawMessage(`{"entries":[]}`)}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodPatch, "/v1/workspaces/workspace_1/meeting-sessions/session_1/agenda-progress", `{"manualCurrentTopicId":"agenda-2"}`)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	api.UpdateAgendaProgressForWorkspace(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if !analysis.gotOverrideInput.ManualCurrentSet || analysis.gotOverrideInput.ManualCurrentID != "agenda-2" || analysis.gotOverrideInput.EntryID != "" {
		t.Fatalf("gotOverrideInput = %+v", analysis.gotOverrideInput)
	}
}

func TestMeetingSessionAPIUpdateAgendaProgressClearsManualCurrentTopic(t *testing.T) {
	service := meetingSessionForAgendaProgress(t)
	analysis := &fakeMeetingAIAnalysisUseCases{overrideResult: json.RawMessage(`{"entries":[]}`)}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodPatch, "/v1/workspaces/workspace_1/meeting-sessions/session_1/agenda-progress", `{"manualCurrentTopicId":null}`)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	api.UpdateAgendaProgressForWorkspace(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if !analysis.gotOverrideInput.ManualCurrentSet || analysis.gotOverrideInput.ManualCurrentID != "" {
		t.Fatalf("gotOverrideInput = %+v", analysis.gotOverrideInput)
	}
}

func TestMeetingSessionAPIUpdateAgendaProgressRejectsCombinedFields(t *testing.T) {
	service := meetingSessionForAgendaProgress(t)
	analysis := &fakeMeetingAIAnalysisUseCases{overrideResult: json.RawMessage(`{"entries":[]}`)}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodPatch, "/v1/workspaces/workspace_1/meeting-sessions/session_1/agenda-progress", `{"entryId":"agenda-1","manualStatus":"discussed","manualCurrentTopicId":"agenda-2"}`)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	api.UpdateAgendaProgressForWorkspace(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if analysis.gotOverrideSessionID != "" {
		t.Fatalf("service should not be called: gotOverrideSessionID = %q", analysis.gotOverrideSessionID)
	}
}

func TestMeetingSessionAPIUpdateAgendaProgressRejectsEmptyBody(t *testing.T) {
	service := meetingSessionForAgendaProgress(t)
	analysis := &fakeMeetingAIAnalysisUseCases{overrideResult: json.RawMessage(`{"entries":[]}`)}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodPatch, "/v1/workspaces/workspace_1/meeting-sessions/session_1/agenda-progress", `{}`)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	api.UpdateAgendaProgressForWorkspace(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestMeetingSessionAPIUpdateAgendaProgressRejectsWrongContentType(t *testing.T) {
	service := meetingSessionForAgendaProgress(t)
	analysis := &fakeMeetingAIAnalysisUseCases{overrideResult: json.RawMessage(`{"entries":[]}`)}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodPatch, "/v1/workspaces/workspace_1/meeting-sessions/session_1/agenda-progress", `{"entryId":"agenda-1","manualStatus":"discussed"}`)
	req.Header.Set("Content-Type", "text/plain")
	resp := httptest.NewRecorder()

	api.UpdateAgendaProgressForWorkspace(resp, req)

	if resp.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestMeetingSessionAPIUpdateAgendaProgressMapsInvalidArgumentToBadRequest(t *testing.T) {
	service := meetingSessionForAgendaProgress(t)
	analysis := &fakeMeetingAIAnalysisUseCases{overrideErr: fmt.Errorf("%w: unknown agenda progress entry id", domain.ErrInvalidArgument)}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodPatch, "/v1/workspaces/workspace_1/meeting-sessions/session_1/agenda-progress", `{"entryId":"agenda-unknown","manualStatus":"discussed"}`)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	api.UpdateAgendaProgressForWorkspace(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestMeetingSessionAPIUpdateAgendaProgressReturnsServiceUnavailableWhenNotWired(t *testing.T) {
	service := meetingSessionForAgendaProgress(t)
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithWorkspaceSessionParams(http.MethodPatch, "/v1/workspaces/workspace_1/meeting-sessions/session_1/agenda-progress", `{"entryId":"agenda-1","manualStatus":"discussed"}`)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	api.UpdateAgendaProgressForWorkspace(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
}

func TestMeetingSessionAPIGetWorkspaceFinalSummaryPreviews(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{
			ID:          "session_1",
			WorkspaceID: "workspace_1",
			Status:      domain.MeetingSessionEnded,
			RequestedAt: mustTime(t, "2026-06-27T00:00:00Z"),
			CreatedAt:   mustTime(t, "2026-06-27T00:00:00Z"),
			UpdatedAt:   mustTime(t, "2026-06-27T00:00:01Z"),
		},
	}
	analysis := &fakeMeetingAIAnalysisUseCases{
		previews: []application.MeetingFinalSummaryPreview{{SessionID: "session_1", Overview: "概要です"}},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey, WithMeetingSessionAIAnalysisService(analysis))
	req := requestWithWorkspaceSessionParams(http.MethodGet, "/v1/workspaces/workspace_1/meeting-sessions/final-summaries", "")
	resp := httptest.NewRecorder()

	api.GetWorkspaceFinalSummaryPreviews(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if len(analysis.gotPreviewIDs) != 1 || analysis.gotPreviewIDs[0] != "session_1" {
		t.Fatalf("gotPreviewIDs = %#v", analysis.gotPreviewIDs)
	}
	var body meetingFinalSummaryPreviewListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].SessionID != "session_1" || body.Items[0].Overview != "概要です" {
		t.Fatalf("body = %+v", body)
	}
}

func TestMeetingSessionAPIGetWorkspaceFinalSummaryPreviewsWithoutAIAnalysisServiceReturnsEmpty(t *testing.T) {
	service := &fakeMeetingSessionUseCases{
		session: domain.MeetingSession{ID: "session_1", WorkspaceID: "workspace_1"},
	}
	api := NewMeetingSessionAPI(service, testTranscriptAPIKey)
	req := requestWithWorkspaceSessionParams(http.MethodGet, "/v1/workspaces/workspace_1/meeting-sessions/final-summaries", "")
	resp := httptest.NewRecorder()

	api.GetWorkspaceFinalSummaryPreviews(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	var body meetingFinalSummaryPreviewListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 0 {
		t.Fatalf("body.Items = %+v, want empty", body.Items)
	}
}

type fakeMeetingAIAnalysisUseCases struct {
	snapshot      *application.MeetingAIAnalysesSnapshot
	err           error
	gotSessionID  string
	previews      []application.MeetingFinalSummaryPreview
	gotPreviewIDs []string
	previewErr    error

	overrideResult       json.RawMessage
	overrideErr          error
	gotOverrideSessionID string
	gotOverrideInput     application.AgendaProgressOverrideInput
}

func (f *fakeMeetingAIAnalysisUseCases) UpdateAgendaProgressOverride(_ context.Context, sessionID string, input application.AgendaProgressOverrideInput) (json.RawMessage, error) {
	f.gotOverrideSessionID = sessionID
	f.gotOverrideInput = input
	if f.overrideErr != nil {
		return nil, f.overrideErr
	}
	return f.overrideResult, nil
}

func (f *fakeMeetingAIAnalysisUseCases) ListFinalSummaryPreviews(_ context.Context, sessionIDs []string) ([]application.MeetingFinalSummaryPreview, error) {
	f.gotPreviewIDs = sessionIDs
	if f.previewErr != nil {
		return nil, f.previewErr
	}
	return f.previews, nil
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
	deletedSessionID   string
	deleteErr          error
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

func (f *fakeMeetingSessionUseCases) DeleteMeetingSession(_ context.Context, sessionID string) error {
	f.deletedSessionID = sessionID
	return f.deleteErr
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
