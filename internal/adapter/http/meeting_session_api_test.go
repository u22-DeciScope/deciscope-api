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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/meeting-sessions", strings.NewReader(`{"joinUrl":"https://teams.microsoft.com/l/meetup-join/abc"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	api.Create(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if service.joinURL != "https://teams.microsoft.com/l/meetup-join/abc" {
		t.Fatalf("joinURL = %q", service.joinURL)
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
	resp := httptest.NewRecorder()
	api.Create(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
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

type fakeMeetingSessionUseCases struct {
	session domain.MeetingSession
	err     error
	joinURL string
	update  application.MeetingSessionStatusUpdateInput
	reused  bool
}

func (f *fakeMeetingSessionUseCases) CreateMeetingSession(_ context.Context, joinURL string) (*application.MeetingSessionCreateResult, error) {
	f.joinURL = joinURL
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

func (f *fakeMeetingSessionUseCases) UpdateMeetingSessionStatus(_ context.Context, input application.MeetingSessionStatusUpdateInput) (*domain.MeetingSession, error) {
	f.update = input
	if f.err != nil {
		return nil, f.err
	}
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

func requestWithSessionParam(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("session_id", "session_1")
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}
