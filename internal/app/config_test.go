package app

import "testing"

func TestConfigFromEnvPrefersDatabaseSettings(t *testing.T) {
	t.Setenv("DATABASE_DRIVER", "sqlite")
	t.Setenv("DATABASE_URL", "database.sqlite")
	t.Setenv("SQLITE_PATH", "legacy.sqlite")
	config := ConfigFromEnv()
	if config.Database.Driver != "sqlite" || config.Database.URL != "database.sqlite" {
		t.Fatalf("ConfigFromEnv() = %+v", config)
	}
}

func TestConfigFromEnvSupportsLegacySQLitePath(t *testing.T) {
	t.Setenv("DATABASE_DRIVER", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SQLITE_PATH", "legacy.sqlite")
	config := ConfigFromEnv()
	if config.Database.Driver != "sqlite" || config.Database.URL != "legacy.sqlite" {
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
