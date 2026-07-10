package botcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
)

func TestClientSendsJoinCommand(t *testing.T) {
	var gotToken string
	var gotBody joinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-DeciScope-Bot-Control-Token")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{URL: server.URL, Token: "control-token", Timeout: time.Second})
	err := client.SendJoinCommand(context.Background(), application.BotJoinCommand{
		SessionID:                   "session_1",
		JoinURL:                     "https://teams.microsoft.com/l/meetup-join/abc",
		CanonicalJoinWebURL:         "https://teams.microsoft.com/l/meetup-join/abc",
		JoinMeetingID:               "123456789",
		CandidateUserIDs:            []string{"11111111-1111-1111-1111-111111111111"},
		CandidateUserPrincipalNames: []string{"user@example.com"},
		CreatedByMicrosoftUserID:    "11111111-1111-1111-1111-111111111111",
		CreatedByEmail:              "user@example.com",
	})

	if err != nil {
		t.Fatalf("SendJoinCommand() error = %v", err)
	}
	if gotToken != "control-token" || gotBody.SessionID != "session_1" || gotBody.JoinURL == "" {
		t.Fatalf("request token=%q body=%+v", gotToken, gotBody)
	}
	if gotBody.JoinMeetingID != "123456789" ||
		gotBody.CanonicalJoinWebURL == "" ||
		len(gotBody.CandidateUserIDs) != 1 ||
		gotBody.CandidateUserIDs[0] != "11111111-1111-1111-1111-111111111111" ||
		len(gotBody.CandidateUserPrincipalNames) != 1 ||
		gotBody.CandidateUserPrincipalNames[0] != "user@example.com" {
		t.Fatalf("request body missing title lookup metadata: %+v", gotBody)
	}
}

func TestClientSplitsConfiguredCandidateUserIdentifiers(t *testing.T) {
	var gotBody joinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{
		URL:              server.URL,
		Token:            "control-token",
		Timeout:          time.Second,
		CandidateUserIDs: []string{"22222222-2222-2222-2222-222222222222", "configured@example.com"},
	})
	err := client.SendJoinCommand(context.Background(), application.BotJoinCommand{
		SessionID:                   "session_1",
		JoinURL:                     "https://teams.microsoft.com/l/meetup-join/abc",
		CandidateUserIDs:            []string{"11111111-1111-1111-1111-111111111111", "legacy@example.com"},
		CandidateUserPrincipalNames: []string{"request@example.com"},
	})

	if err != nil {
		t.Fatalf("SendJoinCommand() error = %v", err)
	}
	if len(gotBody.CandidateUserIDs) != 2 ||
		gotBody.CandidateUserIDs[0] != "11111111-1111-1111-1111-111111111111" ||
		gotBody.CandidateUserIDs[1] != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("candidateUserIds = %#v", gotBody.CandidateUserIDs)
	}
	if len(gotBody.CandidateUserPrincipalNames) != 3 ||
		gotBody.CandidateUserPrincipalNames[0] != "request@example.com" ||
		gotBody.CandidateUserPrincipalNames[1] != "legacy@example.com" ||
		gotBody.CandidateUserPrincipalNames[2] != "configured@example.com" {
		t.Fatalf("candidateUserPrincipalNames = %#v", gotBody.CandidateUserPrincipalNames)
	}
}

func TestClientSendsEndCommand(t *testing.T) {
	var gotPath string
	var gotToken string
	var gotBody endRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-DeciScope-Bot-Control-Token")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{URL: server.URL + "/internal/bot/join", Token: "control-token", Timeout: time.Second})
	err := client.EndMeetingSession(context.Background(), application.BotEndCommand{
		SessionID: "session_1",
		BotCallID: "call-1",
		Reason:    "manual_end_requested",
	})

	if err != nil {
		t.Fatalf("EndMeetingSession() error = %v", err)
	}
	if gotPath != "/internal/bot/meeting-sessions/session_1/end" || gotToken != "control-token" {
		t.Fatalf("request path=%q token=%q", gotPath, gotToken)
	}
	if gotBody.SessionID != "session_1" || gotBody.BotCallID != "call-1" || gotBody.Reason != "manual_end_requested" {
		t.Fatalf("request body = %+v", gotBody)
	}
}

func TestClientRejectsMissingConfig(t *testing.T) {
	client := NewClient(Config{})
	err := client.SendJoinCommand(context.Background(), application.BotJoinCommand{})
	if !errors.Is(err, application.ErrBotControlNotConfigured) {
		t.Fatalf("SendJoinCommand() error = %v, want not configured", err)
	}
}

func TestClientMapsNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{URL: server.URL, Token: "control-token", Timeout: time.Second})
	err := client.SendJoinCommand(context.Background(), application.BotJoinCommand{SessionID: "session_1", JoinURL: "https://teams.microsoft.com/l/meetup-join/abc"})
	if !errors.Is(err, application.ErrBotControlCommandFailed) {
		t.Fatalf("SendJoinCommand() error = %v, want command failed", err)
	}
}
