package app

import "testing"

func TestConfigFromEnvReadsDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://deciscope:secret@localhost:5432/deciscope")
	config := ConfigFromEnv()
	if config.Database.URL != "postgres://deciscope:secret@localhost:5432/deciscope" {
		t.Fatalf("ConfigFromEnv() = %+v", config)
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
	t.Setenv("PORT", "")
	if got := ListenAddressFromEnv(); got != ":9090" {
		t.Fatalf("default address = %q, want :9090", got)
	}
	t.Setenv("PORT", "18080")
	if got := ListenAddressFromEnv(); got != ":18080" {
		t.Fatalf("configured address = %q, want :18080", got)
	}
}
