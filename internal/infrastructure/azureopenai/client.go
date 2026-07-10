// Package azureopenai implements application.AIChatCompleter against the
// Azure OpenAI chat completions REST API. It is the only place in the
// codebase that knows the Azure OpenAI wire format.
package azureopenai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"deciscope-core-api/internal/application"
)

const (
	defaultAPIVersion    = "2024-10-21"
	maxResponseBodyBytes = 1 << 20 // 1 MiB
	errorSnippetMaxChars = 200
	legacyTemperature    = 0.2
)

// Config holds the Azure OpenAI deployment connection details. It is read
// from the environment only in internal/app and passed in here.
type Config struct {
	Endpoint   string
	APIKey     string
	Deployment string
	APIVersion string
	Timeout    time.Duration
}

// Enabled reports whether every field required to call Azure OpenAI is set.
func (c Config) Enabled() bool {
	return strings.TrimSpace(c.Endpoint) != "" &&
		strings.TrimSpace(c.APIKey) != "" &&
		strings.TrimSpace(c.Deployment) != ""
}

// HTTPDoer is satisfied by *http.Client and lets tests substitute a fake
// transport without starting a real server.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Parameter modes for different model generations. Reasoning models (gpt-5
// family, o-series) reject max_tokens and non-default temperature but need
// reasoning_effort "minimal" so the hidden reasoning does not consume the
// whole completion budget; older chat models reject reasoning_effort and the
// oldest ones also reject max_completion_tokens. The client starts with the
// reasoning-model parameters and falls back one step at a time when the
// deployment rejects a parameter, remembering the working mode.
const (
	paramModeReasoning = int32(iota) // max_completion_tokens + reasoning_effort
	paramModeStandard                // max_completion_tokens + temperature
	paramModeLegacy                  // max_tokens + temperature
)

type Client struct {
	config Config
	http   HTTPDoer
	// paramMode is the parameter set accepted by the configured deployment,
	// discovered at runtime via 400 "unsupported parameter" fallbacks.
	paramMode atomic.Int32
}

func NewClient(config Config, httpClient ...HTTPDoer) *Client {
	doer := HTTPDoer(http.DefaultClient)
	if len(httpClient) > 0 && httpClient[0] != nil {
		doer = httpClient[0]
	}
	return &Client{config: config, http: doer}
}

// Complete calls the Azure OpenAI chat completions endpoint and returns the
// assistant message content plus token usage. The request/response bodies
// and the API key are never logged by this client.
func (c *Client) Complete(ctx context.Context, request application.AIChatRequest) (application.AIChatResult, error) {
	if !c.config.Enabled() {
		return application.AIChatResult{}, fmt.Errorf("azure openai client is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.config.Timeout)
		defer cancel()
	}

	mode := c.paramMode.Load()
	for {
		result, err := c.completeOnce(ctx, request, mode)
		nextMode, retry := fallbackParamMode(mode, err)
		if !retry {
			return result, err
		}
		mode = nextMode
		c.paramMode.Store(mode)
	}
}

// fallbackParamMode maps a 400 "unsupported parameter" error to the next
// parameter mode to try, if any.
func fallbackParamMode(mode int32, err error) (int32, bool) {
	if err == nil {
		return mode, false
	}
	if mode == paramModeReasoning && isUnsupportedParam(err, "reasoning_effort") {
		return paramModeStandard, true
	}
	if mode < paramModeLegacy && isUnsupportedParam(err, "max_completion_tokens") {
		return paramModeLegacy, true
	}
	return mode, false
}

func (c *Client) completeOnce(ctx context.Context, request application.AIChatRequest, mode int32) (application.AIChatResult, error) {
	body := chatCompletionRequest{
		Messages: []chatMessage{
			{Role: "system", Content: request.System},
			{Role: "user", Content: request.User},
		},
		ResponseFormat: &chatResponseFormat{Type: "json_object"},
	}
	switch mode {
	case paramModeReasoning:
		body.MaxCompletionTokens = request.MaxTokens
		body.ReasoningEffort = "minimal"
	case paramModeStandard:
		body.MaxCompletionTokens = request.MaxTokens
		temperature := legacyTemperature
		body.Temperature = &temperature
	default:
		body.MaxTokens = request.MaxTokens
		temperature := legacyTemperature
		body.Temperature = &temperature
	}

	requestBody, err := json.Marshal(body)
	if err != nil {
		return application.AIChatResult{}, fmt.Errorf("marshal azure openai request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, completionsURL(c.config), bytes.NewReader(requestBody))
	if err != nil {
		return application.AIChatResult{}, fmt.Errorf("build azure openai request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("api-key", c.config.APIKey)

	response, err := c.http.Do(httpRequest)
	if err != nil {
		return application.AIChatResult{}, fmt.Errorf("call azure openai: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes))
	if err != nil {
		return application.AIChatResult{}, fmt.Errorf("read azure openai response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return application.AIChatResult{}, fmt.Errorf("azure openai request failed: status=%d body=%s", response.StatusCode, errorSnippet(responseBody))
	}

	var parsed chatCompletionResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return application.AIChatResult{}, fmt.Errorf("parse azure openai response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return application.AIChatResult{}, fmt.Errorf("azure openai response has no choices")
	}
	choice := parsed.Choices[0]
	if strings.TrimSpace(choice.Message.Content) == "" {
		// Reasoning models can exhaust the completion budget before emitting
		// any visible output; surface the finish reason so operators can tell
		// a token-limit problem apart from an empty model answer.
		return application.AIChatResult{}, fmt.Errorf("azure openai response content is empty: finishReason=%s completionTokens=%d", choice.FinishReason, parsed.Usage.CompletionTokens)
	}
	return application.AIChatResult{
		Content:          choice.Message.Content,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
	}, nil
}

// isUnsupportedParam detects the 400 error a model returns when it rejects a
// request parameter it does not know or support.
func isUnsupportedParam(err error, param string) bool {
	message := err.Error()
	if !strings.Contains(message, "status=400") {
		return false
	}
	if !strings.Contains(message, param) {
		return false
	}
	lower := strings.ToLower(message)
	return strings.Contains(lower, "unsupported") ||
		strings.Contains(lower, "unrecognized") ||
		strings.Contains(lower, "unknown") ||
		strings.Contains(lower, "not supported") ||
		strings.Contains(lower, "invalid")
}

func completionsURL(config Config) string {
	endpoint := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	version := strings.TrimSpace(config.APIVersion)
	if version == "" {
		version = defaultAPIVersion
	}
	return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s", endpoint, config.Deployment, version)
}

func errorSnippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	runes := []rune(text)
	if len(runes) > errorSnippetMaxChars {
		return string(runes[:errorSnippetMaxChars])
	}
	return text
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponseFormat struct {
	Type string `json:"type"`
}

type chatCompletionRequest struct {
	Messages       []chatMessage       `json:"messages"`
	ResponseFormat *chatResponseFormat `json:"response_format,omitempty"`
	// Temperature is only sent on the standard/legacy parameter paths;
	// reasoning models reject any non-default temperature.
	Temperature         *float64 `json:"temperature,omitempty"`
	MaxTokens           int      `json:"max_tokens,omitempty"`
	MaxCompletionTokens int      `json:"max_completion_tokens,omitempty"`
	// ReasoningEffort is only sent on the reasoning-model path. "minimal"
	// keeps hidden reasoning tokens from consuming the completion budget on
	// this schema-extraction workload.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

var _ application.AIChatCompleter = (*Client)(nil)
