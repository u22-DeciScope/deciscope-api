package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"deciscope-core-api/internal/infrastructure/botcontrol"
	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/firebase"

	"github.com/joho/godotenv"
)

const ingestAPIKeyPlaceholder = "REPLACE_WITH_A_LONG_RANDOM_SECRET"
const minIngestAPIKeyLength = 32

const (
	TranscriptStorePostgres = "postgres"
)

type Config struct {
	Database            database.Config
	TranscriptIngest    TranscriptIngestConfig
	TranscriptWebSocket TranscriptWebSocketConfig
	TranscriptOnly      bool
	BotControl          botcontrol.Config
	Firebase            firebase.Config
	UploadDir           string
	FrontendURL         string
	AllowedOrigins      string
	SessionCookieSecure bool
	SeedDemoData        bool
}

type TranscriptIngestConfig struct {
	Store  string
	APIKey string
}

type TranscriptWebSocketConfig struct {
	ClientToken    string
	AllowedOrigins string
}

func ConfigFromEnv() Config {
	transcriptOnly := strings.EqualFold(os.Getenv("DECISCOPE_TRANSCRIPT_ONLY"), "true")
	transcriptStore := strings.ToLower(strings.TrimSpace(os.Getenv("DECISCOPE_TRANSCRIPT_STORE")))
	if transcriptStore == "" {
		transcriptStore = TranscriptStorePostgres
	}
	return Config{
		Database: database.Config{URL: os.Getenv("DATABASE_URL")},
		TranscriptIngest: TranscriptIngestConfig{
			Store:  transcriptStore,
			APIKey: strings.TrimSpace(os.Getenv("DECISCOPE_INGEST_API_KEY")),
		},
		TranscriptWebSocket: TranscriptWebSocketConfig{
			ClientToken:    strings.TrimSpace(os.Getenv("DECISCOPE_WS_CLIENT_TOKEN")),
			AllowedOrigins: os.Getenv("DECISCOPE_WS_ALLOWED_ORIGINS"),
		},
		TranscriptOnly: transcriptOnly,
		BotControl: botcontrol.Config{
			URL:              strings.TrimSpace(os.Getenv("DECISCOPE_BOT_CONTROL_URL")),
			Token:            strings.TrimSpace(os.Getenv("DECISCOPE_BOT_CONTROL_TOKEN")),
			Timeout:          botControlTimeoutFromEnv(os.Getenv("DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS")),
			CandidateUserIDs: meetingTitleLookupUserIDsFromEnv(),
		},
		Firebase: firebase.Config{
			CredentialsFile: firstNonEmpty(os.Getenv("FIREBASE_SERVICE_ACCOUNT_JSON"), os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")),
			CredentialsJSON: os.Getenv("FIREBASE_CREDENTIALS_JSON"),
			ProjectID:       os.Getenv("FIREBASE_PROJECT_ID"),
			Enabled:         os.Getenv("AUTH_PROVIDER") == "firebase",
		},
		UploadDir:           os.Getenv("UPLOAD_DIR"),
		FrontendURL:         os.Getenv("FRONTEND_URL"),
		AllowedOrigins:      os.Getenv("ALLOWED_ORIGINS"),
		SessionCookieSecure: strings.EqualFold(os.Getenv("SESSION_COOKIE_SECURE"), "true"),
		SeedDemoData:        strings.EqualFold(strings.TrimSpace(os.Getenv("DECISCOPE_SEED_DEMO_DATA")), "true"),
	}
}

func botControlTimeoutFromEnv(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		return 10 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

func meetingTitleLookupUserIDsFromEnv() []string {
	return splitEnvList(firstNonEmpty(
		os.Getenv("MEETING_TITLE_LOOKUP_USER_IDS"),
		os.Getenv("DECISCOPE_MEETING_TITLE_LOOKUP_USER_IDS"),
	))
}

func splitEnvList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, trimmed)
	}
	return values
}

func LoadEnvironmentFiles() {
	_ = godotenv.Load(".env")
	_ = godotenv.Overload(".env.local")
}

func ListenAddressFromEnv() string {
	if address := strings.TrimSpace(os.Getenv("DECISCOPE_BACKEND_ADDR")); address != "" {
		return address
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "9090"
	}
	return ":" + port
}

func ValidateRuntimeConfig(config Config) error {
	if err := validateIngestAPIKey(config.TranscriptIngest.APIKey); err != nil {
		return err
	}
	if !config.TranscriptOnly && strings.TrimSpace(config.Database.URL) == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	switch config.TranscriptIngest.Store {
	case "", TranscriptStorePostgres:
		if strings.TrimSpace(config.Database.URL) == "" {
			return fmt.Errorf("DATABASE_URL is required")
		}
	default:
		return fmt.Errorf("DECISCOPE_TRANSCRIPT_STORE must be postgres")
	}
	return nil
}

func validateIngestAPIKey(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("DECISCOPE_INGEST_API_KEY is required")
	}
	if value == ingestAPIKeyPlaceholder {
		return fmt.Errorf("DECISCOPE_INGEST_API_KEY must be replaced")
	}
	if len(value) < minIngestAPIKeyLength {
		return fmt.Errorf("DECISCOPE_INGEST_API_KEY must be at least %d characters", minIngestAPIKeyLength)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
