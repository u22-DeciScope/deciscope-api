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
		SessionID: "session_1",
		JoinURL:   "https://teams.microsoft.com/l/meetup-join/abc",
	})

	if err != nil {
		t.Fatalf("SendJoinCommand() error = %v", err)
	}
	if gotToken != "control-token" || gotBody.SessionID != "session_1" || gotBody.JoinURL == "" {
		t.Fatalf("request token=%q body=%+v", gotToken, gotBody)
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
