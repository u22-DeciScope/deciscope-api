package fixture

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

type Loader interface {
	Dir() string
	List() ([]application.FixtureInfo, error)
	Open(name string) (io.ReadCloser, error)
}

type LocalLoader struct {
	dir string
}

func NewLocalLoader(dir string) *LocalLoader {
	if dir == "" {
		dir = "./fixtures/meetings"
	}
	return &LocalLoader{dir: dir}
}

func (l *LocalLoader) Dir() string {
	return l.dir
}

func (l *LocalLoader) List() ([]application.FixtureInfo, error) {
	entries, err := os.ReadDir(l.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []application.FixtureInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	var fixtures []application.FixtureInfo
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			fixtures = append(fixtures, application.FixtureInfo{Name: entry.Name(), Path: filepath.Join(l.dir, entry.Name())})
		}
	}
	return fixtures, nil
}

func (l *LocalLoader) Open(name string) (io.ReadCloser, error) {
	path, err := l.safePath(domain.NormalizeFixtureName(name))
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (l *LocalLoader) safePath(name string) (string, error) {
	if strings.Contains(name, "..") {
		return "", errors.New("invalid fixture name")
	}
	base, err := filepath.Abs(l.dir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.Abs(filepath.Join(l.dir, name))
	if err != nil {
		return "", err
	}
	if resolved != base && !strings.HasPrefix(resolved, base+string(os.PathSeparator)) {
		return "", errors.New("fixture path escapes fixture directory")
	}
	return resolved, nil
}
