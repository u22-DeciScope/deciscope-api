package app

import "testing"

func TestConfigFromEnvReadsDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://deciscope:secret@localhost:5432/deciscope")
	t.Setenv("DECISCOPE_GO_SQLITE_PATH", `C:\tmp\deciscope-go.db`)
	t.Setenv("DECISCOPE_INGEST_API_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("DECISCOPE_TRANSCRIPT_ONLY", "true")
	config := ConfigFromEnv()
	if config.Database.URL != "postgres://deciscope:secret@localhost:5432/deciscope" {
		t.Fatalf("ConfigFromEnv() = %+v", config)
	}
	if config.TranscriptIngest.SQLite.Path != `C:\tmp\deciscope-go.db` {
		t.Fatalf("ConfigFromEnv() = %+v", config)
	}
	if !config.TranscriptOnly {
		t.Fatalf("ConfigFromEnv() = %+v, want transcript-only enabled", config)
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
		TranscriptIngest: TranscriptIngestConfig{
			APIKey: "0123456789abcdef0123456789abcdef",
		},
	}
	if err := ValidateRuntimeConfig(config); err == nil {
		t.Fatal("ValidateRuntimeConfig() error = nil, want missing sqlite path")
	}
	config.TranscriptIngest.SQLite.Path = `C:\tmp\deciscope-go.db`
	config.TranscriptIngest.APIKey = ingestAPIKeyPlaceholder
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
