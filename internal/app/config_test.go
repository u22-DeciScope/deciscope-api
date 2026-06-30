package app

import (
	"testing"
	"time"

	"deciscope-core-api/internal/infrastructure/database"
)

func TestConfigFromEnvReadsDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://deciscope:secret@localhost:5432/deciscope")
	t.Setenv("DECISCOPE_INGEST_API_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("DECISCOPE_WS_CLIENT_TOKEN", "dev-ws-token")
	t.Setenv("DECISCOPE_WS_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:5173")
	t.Setenv("DECISCOPE_TRANSCRIPT_ONLY", "true")
	t.Setenv("DECISCOPE_BOT_CONTROL_URL", "http://100.64.0.1:7071/internal/bot/join")
	t.Setenv("DECISCOPE_BOT_CONTROL_TOKEN", "bot-control-token")
	t.Setenv("DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS", "12")
	t.Setenv("MEETING_TITLE_LOOKUP_USER_IDS", "user-a,user-b user-a")
	config := ConfigFromEnv()
	if config.Database.URL != "postgres://deciscope:secret@localhost:5432/deciscope" {
		t.Fatalf("ConfigFromEnv() = %+v", config)
	}
	if config.TranscriptIngest.Store != TranscriptStorePostgres {
		t.Fatalf("ConfigFromEnv() = %+v, want postgres transcript store", config)
	}
	if config.TranscriptWebSocket.ClientToken != "dev-ws-token" ||
		config.TranscriptWebSocket.AllowedOrigins != "http://localhost:3000,http://localhost:5173" {
		t.Fatalf("ConfigFromEnv() = %+v, want websocket config", config)
	}
	if !config.TranscriptOnly {
		t.Fatalf("ConfigFromEnv() = %+v, want transcript-only enabled", config)
	}
	if config.BotControl.URL != "http://100.64.0.1:7071/internal/bot/join" ||
		config.BotControl.Token != "bot-control-token" ||
		config.BotControl.Timeout != 12*time.Second {
		t.Fatalf("ConfigFromEnv() = %+v, want bot control config", config)
	}
	if len(config.BotControl.CandidateUserIDs) != 2 ||
		config.BotControl.CandidateUserIDs[0] != "user-a" ||
		config.BotControl.CandidateUserIDs[1] != "user-b" {
		t.Fatalf("CandidateUserIDs = %#v", config.BotControl.CandidateUserIDs)
	}
}

func TestConfigFromEnvLeavesMissingDatabaseURLBlank(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	config := ConfigFromEnv()
	if config.Database.URL != "" {
		t.Fatalf("ConfigFromEnv() = %+v", config)
	}
}

func TestListenAddressFromEnv(t *testing.T) {
	t.Setenv("DECISCOPE_BACKEND_ADDR", "")
	t.Setenv("PORT", "")
	if got := ListenAddressFromEnv(); got != ":9090" {
		t.Fatalf("default address = %q, want :9090", got)
	}
	t.Setenv("PORT", "18080")
	if got := ListenAddressFromEnv(); got != ":18080" {
		t.Fatalf("configured address = %q, want :18080", got)
	}
	t.Setenv("DECISCOPE_BACKEND_ADDR", "127.0.0.1:18080")
	if got := ListenAddressFromEnv(); got != "127.0.0.1:18080" {
		t.Fatalf("configured backend address = %q, want 127.0.0.1:18080", got)
	}
}

func TestValidateRuntimeConfigRequiresTranscriptSettings(t *testing.T) {
	config := Config{
		Database: databaseConfigForTest(),
		TranscriptIngest: TranscriptIngestConfig{
			Store:  TranscriptStorePostgres,
			APIKey: ingestAPIKeyPlaceholder,
		},
	}
	if err := ValidateRuntimeConfig(config); err == nil {
		t.Fatal("ValidateRuntimeConfig() error = nil, want placeholder api key error")
	}
	config.TranscriptIngest.APIKey = "short"
	if err := ValidateRuntimeConfig(config); err == nil {
		t.Fatal("ValidateRuntimeConfig() error = nil, want short api key error")
	}
	config.TranscriptIngest.APIKey = "0123456789abcdef0123456789abcdef"
	if err := ValidateRuntimeConfig(config); err != nil {
		t.Fatalf("ValidateRuntimeConfig() error = %v", err)
	}
}

func TestValidateRuntimeConfigRejectsSQLiteTranscriptStore(t *testing.T) {
	config := Config{
		Database: databaseConfigForTest(),
		TranscriptIngest: TranscriptIngestConfig{
			Store:  "sqlite",
			APIKey: "0123456789abcdef0123456789abcdef",
		},
	}
	if err := ValidateRuntimeConfig(config); err == nil {
		t.Fatal("ValidateRuntimeConfig() error = nil, want unsupported sqlite store")
	}
}

func TestValidateRuntimeConfigRequiresDatabaseURLForPostgresTranscriptStore(t *testing.T) {
	config := Config{
		TranscriptOnly: true,
		TranscriptIngest: TranscriptIngestConfig{
			Store:  TranscriptStorePostgres,
			APIKey: "0123456789abcdef0123456789abcdef",
		},
	}
	if err := ValidateRuntimeConfig(config); err == nil {
		t.Fatal("ValidateRuntimeConfig() error = nil, want missing database URL")
	}
	config.Database = databaseConfigForTest()
	if err := ValidateRuntimeConfig(config); err != nil {
		t.Fatalf("ValidateRuntimeConfig() error = %v", err)
	}
}

func databaseConfigForTest() database.Config {
	return database.Config{URL: "postgres://deciscope:secret@localhost:5432/deciscope"}
}
