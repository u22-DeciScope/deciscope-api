package fixture

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalLoaderListsAndOpensFixturesWithoutEscapingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	loader := NewLocalLoader(dir)

	fixtures, err := loader.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(fixtures) != 1 || fixtures[0].Name != "demo.jsonl" {
		t.Fatalf("fixtures = %+v", fixtures)
	}

	file, err := loader.Open("demo.jsonl")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	_ = file.Close()

	if _, err := loader.Open("../secret.jsonl"); err == nil {
		t.Fatal("Open() allowed path traversal")
	}
}

func TestLocalLoaderMissingDirectoryIsEmpty(t *testing.T) {
	loader := NewLocalLoader(filepath.Join(t.TempDir(), "missing"))
	fixtures, err := loader.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if fixtures == nil || len(fixtures) != 0 {
		t.Fatalf("fixtures = %#v, want non-nil empty slice", fixtures)
	}

	if _, err := loader.Open("demo.jsonl"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open() error = %v, want os.ErrNotExist", err)
	}
}
