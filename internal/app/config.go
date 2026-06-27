package app

import (
	"fmt"
	"os"
	"strings"

	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/firebase"
	sqliteinfra "deciscope-core-api/internal/infrastructure/sqlite"

	"github.com/joho/godotenv"
)

const ingestAPIKeyPlaceholder = "REPLACE_WITH_A_LONG_RANDOM_SECRET"
const minIngestAPIKeyLength = 32

const (
	TranscriptStorePostgres = "postgres"
	TranscriptStoreSQLite   = "sqlite"
)

type Config struct {
	Database            database.Config
	TranscriptIngest    TranscriptIngestConfig
	TranscriptWebSocket TranscriptWebSocketConfig
	TranscriptOnly      bool
	Firebase            firebase.Config
	UploadDir           string
	FixtureDir          string
	FrontendURL         string
	AllowedOrigins      string
	SessionCookieSecure bool
}

type TranscriptIngestConfig struct {
	Store  string
	SQLite sqliteinfra.Config
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
		if transcriptOnly {
			transcriptStore = TranscriptStoreSQLite
		}
	}
	return Config{
		Database: database.Config{URL: os.Getenv("DATABASE_URL")},
		TranscriptIngest: TranscriptIngestConfig{
			Store:  transcriptStore,
			SQLite: sqliteinfra.Config{Path: os.Getenv("DECISCOPE_GO_SQLITE_PATH")},
			APIKey: strings.TrimSpace(os.Getenv("DECISCOPE_INGEST_API_KEY")),
		},
		TranscriptWebSocket: TranscriptWebSocketConfig{
			ClientToken:    strings.TrimSpace(os.Getenv("DECISCOPE_WS_CLIENT_TOKEN")),
			AllowedOrigins: os.Getenv("DECISCOPE_WS_ALLOWED_ORIGINS"),
		},
		TranscriptOnly: transcriptOnly,
		Firebase: firebase.Config{
			CredentialsFile: firstNonEmpty(os.Getenv("FIREBASE_SERVICE_ACCOUNT_JSON"), os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")),
			CredentialsJSON: os.Getenv("FIREBASE_CREDENTIALS_JSON"),
			ProjectID:       os.Getenv("FIREBASE_PROJECT_ID"),
			Enabled:         os.Getenv("AUTH_PROVIDER") == "firebase",
		},
		UploadDir:           os.Getenv("UPLOAD_DIR"),
		FixtureDir:          os.Getenv("FIXTURE_DIR"),
		FrontendURL:         os.Getenv("FRONTEND_URL"),
		AllowedOrigins:      os.Getenv("ALLOWED_ORIGINS"),
		SessionCookieSecure: strings.EqualFold(os.Getenv("SESSION_COOKIE_SECURE"), "true"),
	}
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
	case TranscriptStoreSQLite:
		if strings.TrimSpace(config.TranscriptIngest.SQLite.Path) == "" {
			return fmt.Errorf("DECISCOPE_GO_SQLITE_PATH is required")
		}
	default:
		return fmt.Errorf("DECISCOPE_TRANSCRIPT_STORE must be postgres or sqlite")
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
