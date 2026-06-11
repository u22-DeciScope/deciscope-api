package database

import "testing"

func TestConfigFromEnvPrefersDatabaseSettings(t *testing.T) {
	t.Setenv("DATABASE_DRIVER", "sqlite")
	t.Setenv("DATABASE_URL", "database.sqlite")
	t.Setenv("SQLITE_PATH", "legacy.sqlite")

	config := ConfigFromEnv()
	if config.Driver != "sqlite" || config.URL != "database.sqlite" {
		t.Fatalf("ConfigFromEnv() = %+v", config)
	}
}

func TestConfigFromEnvSupportsLegacySQLitePath(t *testing.T) {
	t.Setenv("DATABASE_DRIVER", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SQLITE_PATH", "legacy.sqlite")

	config := ConfigFromEnv()
	if config.Driver != "sqlite" || config.URL != "legacy.sqlite" {
		t.Fatalf("ConfigFromEnv() = %+v", config)
	}
}
