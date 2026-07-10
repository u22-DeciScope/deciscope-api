package azureopenai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
)

func TestConfigEnabledRequiresAllFields(t *testing.T) {
	if (Config{}).Enabled() {
		t.Fatal("empty config should not be enabled")
	}
	if (Config{Endpoint: "https://x", APIKey: "k"}).Enabled() {
		t.Fatal("config missing deployment should not be enabled")
	}
	if !(Config{Endpoint: "https://x", APIKey: "k", Deployment: "d"}).Enabled() {
		t.Fatal("fully populated config should be enabled")
	}
}

func TestClientCompleteBuildsURLAndSendsAPIKeyHeader(t *testing.T) {
	var gotPath, gotQuery, gotAPIKey string
	var gotBody chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAPIKey = r.Header.Get("api-key")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"summary\":\"ok\"}"}}],"usage":{"prompt_tokens":12,"completion_tokens":34}}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{
		Endpoint:   server.URL + "/", // trailing slash must be tolerated
		APIKey:     "secret-key",
		Deployment: "gpt-4o-mini",
		APIVersion: "2024-10-21",
		Timeout:    5 * time.Second,
	})

	result, err := client.Complete(context.Background(), application.AIChatRequest{
		System:    "system prompt",
		User:      "user prompt",
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.Content != `{"summary":"ok"}` || result.PromptTokens != 12 || result.CompletionTokens != 34 {
		t.Fatalf("result = %+v", result)
	}
	if gotPath != "/openai/deployments/gpt-4o-mini/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotQuery != "api-version=2024-10-21" {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotAPIKey != "secret-key" {
		t.Fatalf("api-key header = %q", gotAPIKey)
	}
	if len(gotBody.Messages) != 2 || gotBody.Messages[0].Content != "system prompt" || gotBody.Messages[1].Content != "user prompt" {
		t.Fatalf("request body = %+v", gotBody)
	}
	if gotBody.ResponseFormat == nil || gotBody.ResponseFormat.Type != "json_object" {
		t.Fatalf("response format = %+v", gotBody.ResponseFormat)
	}
	if gotBody.MaxCompletionTokens != 100 || gotBody.MaxTokens != 0 {
		t.Fatalf("token params = maxCompletionTokens:%d maxTokens:%d, want modern parameter only", gotBody.MaxCompletionTokens, gotBody.MaxTokens)
	}
	if gotBody.Temperature != nil {
		t.Fatalf("temperature = %v, want omitted on reasoning parameter path", *gotBody.Temperature)
	}
	if gotBody.ReasoningEffort != "minimal" {
		t.Fatalf("reasoning effort = %q, want minimal on default path", gotBody.ReasoningEffort)
	}
}

func TestClientCompleteFallsBackToStandardModeWhenReasoningEffortUnsupported(t *testing.T) {
	var bodies []chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		bodies = append(bodies, body)
		if body.ReasoningEffort != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: reasoning_effort","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\"}"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{Endpoint: server.URL, APIKey: "k", Deployment: "gpt-4o", Timeout: time.Second})
	result, err := client.Complete(context.Background(), application.AIChatRequest{System: "s", User: "u", MaxTokens: 70})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.Content != `{"summary":"ok"}` {
		t.Fatalf("result content = %q", result.Content)
	}
	if len(bodies) != 2 {
		t.Fatalf("request count = %d, want retry after reasoning_effort rejection", len(bodies))
	}
	if bodies[1].ReasoningEffort != "" || bodies[1].MaxCompletionTokens != 70 {
		t.Fatalf("standard retry body = %+v", bodies[1])
	}
	if bodies[1].Temperature == nil || *bodies[1].Temperature != legacyTemperature {
		t.Fatalf("standard retry temperature = %v", bodies[1].Temperature)
	}
}

func TestClientCompleteFallsBackToLegacyMaxTokens(t *testing.T) {
	var bodies []chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		bodies = append(bodies, body)
		if body.MaxCompletionTokens > 0 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: max_completion_tokens","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\"}"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{Endpoint: server.URL, APIKey: "k", Deployment: "legacy-model", Timeout: time.Second})
	result, err := client.Complete(context.Background(), application.AIChatRequest{System: "s", User: "u", MaxTokens: 50})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.Content != `{"summary":"ok"}` {
		t.Fatalf("result content = %q", result.Content)
	}
	if len(bodies) != 2 {
		t.Fatalf("request count = %d, want retry after max_completion_tokens rejection", len(bodies))
	}
	if bodies[1].MaxTokens != 50 || bodies[1].MaxCompletionTokens != 0 {
		t.Fatalf("legacy retry body = %+v", bodies[1])
	}
	if bodies[1].Temperature == nil || *bodies[1].Temperature != legacyTemperature {
		t.Fatalf("legacy retry temperature = %v", bodies[1].Temperature)
	}

	// Subsequent calls should use the legacy parameter immediately.
	if _, err := client.Complete(context.Background(), application.AIChatRequest{System: "s", User: "u", MaxTokens: 50}); err != nil {
		t.Fatalf("Complete() second call error = %v", err)
	}
	if len(bodies) != 3 || bodies[2].MaxTokens != 50 {
		t.Fatalf("second call bodies = %d, want single legacy request", len(bodies))
	}
}

func TestClientCompleteReportsEmptyContentWithFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":100}}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{Endpoint: server.URL, APIKey: "k", Deployment: "d", Timeout: time.Second})
	_, err := client.Complete(context.Background(), application.AIChatRequest{System: "s", User: "u", MaxTokens: 100})
	if err == nil || !strings.Contains(err.Error(), "finishReason=length") {
		t.Fatalf("Complete() error = %v, want empty-content error with finish reason", err)
	}
}

func TestClientCompleteUsesDefaultAPIVersionWhenUnset(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\"}"}}]}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{Endpoint: server.URL, APIKey: "k", Deployment: "d", Timeout: time.Second})
	if _, err := client.Complete(context.Background(), application.AIChatRequest{System: "s", User: "u"}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if gotQuery != "api-version="+defaultAPIVersion {
		t.Fatalf("query = %q, want default api version", gotQuery)
	}
}

func TestClientCompleteReturnsErrorWithoutLeakingAPIKeyOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited","code":"429"}}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{Endpoint: server.URL, APIKey: "top-secret-key", Deployment: "d", Timeout: time.Second})
	_, err := client.Complete(context.Background(), application.AIChatRequest{System: "s", User: "u"})
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v, want status/body snippet", err)
	}
	if strings.Contains(err.Error(), "top-secret-key") {
		t.Fatalf("error leaked api key: %v", err)
	}
}

func TestClientCompleteReturnsErrorOn500(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{Endpoint: server.URL, APIKey: "k", Deployment: "d", Timeout: time.Second})
	_, err := client.Complete(context.Background(), application.AIChatRequest{System: "s", User: "u"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("Complete() error = %v, want status 500", err)
	}
}

func TestClientCompleteTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{Endpoint: server.URL, APIKey: "k", Deployment: "d", Timeout: 10 * time.Millisecond})
	_, err := client.Complete(context.Background(), application.AIChatRequest{System: "s", User: "u"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClientCompleteFailsWhenNotConfigured(t *testing.T) {
	client := NewClient(Config{})
	if _, err := client.Complete(context.Background(), application.AIChatRequest{System: "s", User: "u"}); err == nil {
		t.Fatal("expected error when client is not configured")
	}
}

func TestClientCompleteRespectsExistingContextDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bounded sleep (rather than blocking on r.Context().Done()) so
		// httptest.Server.Close() in t.Cleanup cannot hang if connection
		// close does not promptly cancel the request context.
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	t.Cleanup(server.Close)

	// Client Timeout is long; the caller-provided context deadline should
	// still cut the request short.
	client := NewClient(Config{Endpoint: server.URL, APIKey: "k", Deployment: "d", Timeout: time.Minute})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := client.Complete(ctx, application.AIChatRequest{System: "s", User: "u"})
	if err == nil {
		t.Fatal("expected error from caller-provided context deadline")
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("Complete() took %s, want it to fail close to the 20ms context deadline instead of waiting for config.Timeout", elapsed)
	}
}
