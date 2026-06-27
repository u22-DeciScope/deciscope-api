package botcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"deciscope-core-api/internal/application"
)

const defaultTimeout = 10 * time.Second

type Config struct {
	URL     string
	Token   string
	Timeout time.Duration
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	config Config
	http   HTTPDoer
}

func NewClient(config Config, httpClient ...HTTPDoer) *Client {
	doer := HTTPDoer(http.DefaultClient)
	if len(httpClient) > 0 && httpClient[0] != nil {
		doer = httpClient[0]
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	return &Client{config: config, http: doer}
}

func (c *Client) SendJoinCommand(ctx context.Context, command application.BotJoinCommand) error {
	if strings.TrimSpace(c.config.URL) == "" || strings.TrimSpace(c.config.Token) == "" {
		return application.ErrBotControlNotConfigured
	}
	body, err := json.Marshal(joinRequest{
		SessionID: command.SessionID,
		JoinURL:   command.JoinURL,
	})
	if err != nil {
		return fmt.Errorf("%w: encode request", application.ErrBotControlCommandFailed)
	}

	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: build request", application.ErrBotControlCommandFailed)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DeciScope-Bot-Control-Token", c.config.Token)

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: timeout", application.ErrBotControlCommandFailed)
		}
		return fmt.Errorf("%w: request failed", application.ErrBotControlCommandFailed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: status %d", application.ErrBotControlCommandFailed, resp.StatusCode)
	}
	return nil
}

type joinRequest struct {
	SessionID string `json:"sessionId"`
	JoinURL   string `json:"joinUrl"`
}
