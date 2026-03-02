package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFileSetsMissingValues(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, ".env")

	if err := os.WriteFile(path, []byte("DATABASE_URL=postgres://example\nALLOWED_ORIGINS=http://localhost:5173\n"), 0o600); err != nil {
		t.Fatalf("expected no write error, got %v", err)
	}

	if err := loadEnvFile(path); err != nil {
		t.Fatalf("expected no load error, got %v", err)
	}

	if got := os.Getenv("DATABASE_URL"); got != "postgres://example" {
		t.Fatalf("expected DATABASE_URL to be set, got %q", got)
	}
	if got := os.Getenv("ALLOWED_ORIGINS"); got != "http://localhost:5173" {
		t.Fatalf("expected ALLOWED_ORIGINS to be set, got %q", got)
	}
}

func TestLoadEnvFileDoesNotOverrideExistingValues(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, ".env")

	if err := os.WriteFile(path, []byte("DATABASE_URL=postgres://from-file\n"), 0o600); err != nil {
		t.Fatalf("expected no write error, got %v", err)
	}

	t.Setenv("DATABASE_URL", "postgres://from-process")

	if err := loadEnvFile(path); err != nil {
		t.Fatalf("expected no load error, got %v", err)
	}

	if got := os.Getenv("DATABASE_URL"); got != "postgres://from-process" {
		t.Fatalf("expected existing DATABASE_URL to remain, got %q", got)
	}
}
