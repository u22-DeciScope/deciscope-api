package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDependencyDirection(t *testing.T) {
	root := filepath.Clean("..")
	assertImports(t, filepath.Join(root, "domain"), func(path string) bool {
		return !strings.HasPrefix(path, "deciscope-core-api/")
	})
	assertImports(t, filepath.Join(root, "application"), func(path string) bool {
		return !strings.HasPrefix(path, "deciscope-core-api/") ||
			path == "deciscope-core-api/internal/domain"
	})
}

func TestLegacyPackagesAreRemoved(t *testing.T) {
	for _, name := range []string{"core", "handlers", "repository", "database", "firebase", "fixture", "realtime", "users"} {
		if _, err := os.Stat(filepath.Join(rootDir(t), "internal", name)); err == nil {
			t.Errorf("legacy package still exists: internal/%s", name)
		}
	}
}

func assertImports(t *testing.T, dir string, allowed func(string) bool) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if !allowed(importPath) {
				t.Errorf("%s imports forbidden package %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect %s: %v", dir, err)
	}
}

func rootDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
