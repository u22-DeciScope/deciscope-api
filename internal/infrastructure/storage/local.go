package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Local struct {
	dir string
}

func NewLocal(dir string) *Local {
	if dir == "" {
		dir = "./uploads"
	}
	return &Local{dir: dir}
}

func (s *Local) Save(_ context.Context, key string, src io.Reader) (string, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", fmt.Errorf("create upload directory: %w", err)
	}
	path := filepath.Join(s.dir, key)
	dst, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create upload file: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return "", fmt.Errorf("write upload file: %w", err)
	}
	if err := dst.Close(); err != nil {
		return "", fmt.Errorf("close upload file: %w", err)
	}
	return path, nil
}
