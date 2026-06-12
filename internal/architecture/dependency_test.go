package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "deciscope-core-api/"

func TestDependencyDirection(t *testing.T) {
	internal := filepath.Join(rootDir(t), "internal")

	t.Run("domain has no project dependencies", func(t *testing.T) {
		assertImports(t, filepath.Join(internal, "domain"), false, func(path string) bool {
			return !strings.HasPrefix(path, modulePath)
		})
	})
	t.Run("application production imports only domain", func(t *testing.T) {
		assertImports(t, filepath.Join(internal, "application"), true, func(path string) bool {
			return !strings.HasPrefix(path, modulePath) || path == modulePath+"internal/domain"
		})
	})
	t.Run("application tests do not use concrete adapters", func(t *testing.T) {
		assertImports(t, filepath.Join(internal, "application"), false, func(path string) bool {
			return !strings.HasPrefix(path, modulePath+"internal/adapter/") &&
				!strings.HasPrefix(path, modulePath+"internal/infrastructure/") &&
				path != modulePath+"internal/app"
		})
	})
	t.Run("application avoids external IO packages", func(t *testing.T) {
		forbidden := map[string]bool{
			"database/sql":              true,
			"mime":                      true,
			"net/http":                  true,
			"os":                        true,
			"path/filepath":             true,
			"firebase.google.com/go/v4": true,
		}
		assertImports(t, filepath.Join(internal, "application"), false, func(path string) bool {
			return !forbidden[path] && !strings.HasPrefix(path, "firebase.google.com/")
		})
	})
}

func TestAdaptersDoNotDependOnOtherAdapterFamilies(t *testing.T) {
	adapterRoot := filepath.Join(rootDir(t), "internal", "adapter")
	inspectFiles(t, adapterRoot, false, func(path string, file *ast.File) {
		relative, err := filepath.Rel(adapterRoot, path)
		if err != nil {
			t.Fatal(err)
		}
		sourceFamily := strings.Split(filepath.ToSlash(relative), "/")[0]
		for _, spec := range file.Imports {
			importPath := unquoteImport(t, spec)
			const prefix = modulePath + "internal/adapter/"
			if !strings.HasPrefix(importPath, prefix) {
				continue
			}
			targetFamily := strings.Split(strings.TrimPrefix(importPath, prefix), "/")[0]
			if sourceFamily != targetFamily {
				t.Errorf("%s imports adapter family %s", path, targetFamily)
			}
		}
	})
}

func TestEnvironmentReadsStayInCompositionRoot(t *testing.T) {
	root := rootDir(t)
	compositionRoot := filepath.Join(root, "internal", "app")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(content)
		if (strings.Contains(text, "os.Getenv(") || strings.Contains(text, "os.LookupEnv(")) &&
			!pathWithin(path, compositionRoot) {
			t.Errorf("%s reads environment outside composition root", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect environment reads: %v", err)
	}
}

func TestLegacyPackagesAreRemoved(t *testing.T) {
	for _, name := range []string{"core", "handlers", "repository", "database", "firebase", "fixture", "realtime", "users"} {
		if _, err := os.Stat(filepath.Join(rootDir(t), "internal", name)); err == nil {
			t.Errorf("legacy package still exists: internal/%s", name)
		}
	}
}

func assertImports(t *testing.T, dir string, productionOnly bool, allowed func(string) bool) {
	t.Helper()
	inspectFiles(t, dir, productionOnly, func(path string, file *ast.File) {
		for _, spec := range file.Imports {
			importPath := unquoteImport(t, spec)
			if !allowed(importPath) {
				t.Errorf("%s imports forbidden package %s", path, importPath)
			}
		}
	})
}

func inspectFiles(t *testing.T, dir string, productionOnly bool, inspect func(string, *ast.File)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") ||
			(productionOnly && strings.HasSuffix(path, "_test.go")) {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		inspect(path, file)
		return nil
	})
	if err != nil {
		t.Fatalf("inspect %s: %v", dir, err)
	}
}

func unquoteImport(t *testing.T, spec *ast.ImportSpec) string {
	t.Helper()
	importPath, err := strconv.Unquote(spec.Path.Value)
	if err != nil {
		t.Fatal(err)
	}
	return importPath
}

func pathWithin(path, dir string) bool {
	relative, err := filepath.Rel(dir, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func rootDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate dependency test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
