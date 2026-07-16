package botcontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
	commandCandidateUserIDs, commandCandidateUserPrincipalNames := splitCandidateUserIdentifiers(command.CandidateUserIDs)
	mergedCandidateUserIDs := mergeCandidateUserIDs(commandCandidateUserIDs)
	mergedCandidateUserPrincipalNames := mergeCandidateUserIDs(
		command.CandidateUserPrincipalNames,
		commandCandidateUserPrincipalNames,
	)
	body, err := json.Marshal(joinRequest{
		SessionID:                   command.SessionID,
		JoinURL:                     command.JoinURL,
		CanonicalJoinWebURL:         command.CanonicalJoinWebURL,
		JoinMeetingID:               command.JoinMeetingID,
		CandidateUserIDs:            mergedCandidateUserIDs,
		CandidateUserPrincipalNames: mergedCandidateUserPrincipalNames,
		CreatedByMicrosoftUserID:    command.CreatedByMicrosoftUserID,
		CreatedByEmail:              command.CreatedByEmail,
	})
	if err != nil {
		return fmt.Errorf("%w: encode request", application.ErrBotControlCommandFailed)
	}
	log.Printf(
		"Bot join command payload prepared. sessionId=%s joinUrlHash=%s candidateUserIdsCount=%d candidateUserIdsHash=%s candidateUserPrincipalNamesCount=%d candidateUserPrincipalNamesHash=%s joinMeetingId=%s",
		command.SessionID,
		hashForLog(command.JoinURL),
		len(mergedCandidateUserIDs),
		hashesForLog(mergedCandidateUserIDs),
		len(mergedCandidateUserPrincipalNames),
		hashesForLog(mergedCandidateUserPrincipalNames),
		command.JoinMeetingID,
	)

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

func (c *Client) EndMeetingSession(ctx context.Context, command application.BotEndCommand) error {
	if strings.TrimSpace(c.config.URL) == "" || strings.TrimSpace(c.config.Token) == "" {
		return application.ErrBotControlNotConfigured
	}
	body, err := json.Marshal(endRequest{
		SessionID: command.SessionID,
		BotCallID: command.BotCallID,
		Reason:    command.Reason,
	})
	if err != nil {
		return fmt.Errorf("%w: encode request", application.ErrBotControlCommandFailed)
	}
	endURL, err := c.endMeetingSessionURL(command.SessionID)
	if err != nil {
		return err
	}
	log.Printf(
		"Bot end command payload prepared. sessionId=%s botCallId=%s reason=%s",
		command.SessionID,
		command.BotCallID,
		strings.TrimSpace(command.Reason),
	)

	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endURL, bytes.NewReader(body))
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
	SessionID                   string   `json:"sessionId"`
	JoinURL                     string   `json:"joinUrl"`
	CanonicalJoinWebURL         string   `json:"canonicalJoinWebUrl,omitempty"`
	JoinMeetingID               string   `json:"joinMeetingId,omitempty"`
	CandidateUserIDs            []string `json:"candidateUserIds,omitempty"`
	CandidateUserPrincipalNames []string `json:"candidateUserPrincipalNames,omitempty"`
	CreatedByMicrosoftUserID    string   `json:"createdByMicrosoftUserId,omitempty"`
	CreatedByEmail              string   `json:"createdByEmail,omitempty"`
}

type endRequest struct {
	SessionID string `json:"sessionId"`
	BotCallID string `json:"botCallId,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func (c *Client) endMeetingSessionURL(sessionID string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(c.config.URL))
	if err != nil {
		return "", fmt.Errorf("%w: parse end url", application.ErrBotControlCommandFailed)
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(strings.ToLower(path), "/join") {
		path = path[:len(path)-len("/join")]
	}
	parsed.Path = strings.TrimRight(path, "/") + "/meeting-sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/end"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func mergeCandidateUserIDs(values ...[]string) []string {
	var merged []string
	for _, group := range values {
		merged = append(merged, group...)
	}
	seen := make(map[string]struct{}, len(merged))
	unique := make([]string, 0, len(merged))
	for _, value := range merged {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, trimmed)
	}
	return unique
}

func splitCandidateUserIdentifiers(values []string) ([]string, []string) {
	objectIDs := make([]string, 0, len(values))
	principalNames := make([]string, 0, len(values))
	for _, value := range values {
		if isAadObjectID(value) {
			objectIDs = append(objectIDs, value)
			continue
		}
		principalNames = append(principalNames, value)
	}
	return objectIDs, principalNames
}

func isAadObjectID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
				return false
			}
		}
	}
	return true
}

func hashesForLog(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	hashes := make([]string, 0, len(values))
	for _, value := range values {
		hashes = append(hashes, hashForLog(value))
	}
	return "[" + strings.Join(hashes, ",") + "]"
}

func hashForLog(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	bytes := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%X", bytes[:8])
}
