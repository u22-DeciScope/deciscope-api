// Package clientdiagnostics はブラウザから届いたクライアント診断イベントの
// 出力先(sessionId単位のJSONLファイルと構造化標準ログ)を提供する。
package clientdiagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"deciscope-core-api/internal/domain"
)

const (
	DefaultMaxFileBytes    int64 = 10 * 1024 * 1024
	DefaultRetention             = 7 * 24 * time.Hour
	DefaultCleanupInterval       = time.Hour
	rotatedSuffix                = ".1"
	fileExtension                = ".jsonl"
)

// sessionIdはDB上の実在セッションに限定済みだが、ファイル名に使う以上
// パス区切り・相対参照を含まないことをここでも検査する。
var safeSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_\-.]{1,128}$`)

var ErrUnsafeSessionID = errors.New("client diagnostics: unsafe session id for file name")

// FileSinkConfig はJSONLファイル出力の設定。
type FileSinkConfig struct {
	// Directory は {sessionId}.jsonl を置くディレクトリ。
	Directory string
	// MaxFileBytes を超える場合、既存ファイルを .1 へ退避して書き直す。
	MaxFileBytes int64
	// Retention より古いファイルは削除する。
	Retention time.Duration
	// CleanupInterval は保持期間チェックを走らせる最短間隔。
	CleanupInterval time.Duration
}

func (c FileSinkConfig) withDefaults() FileSinkConfig {
	if c.MaxFileBytes <= 0 {
		c.MaxFileBytes = DefaultMaxFileBytes
	}
	if c.Retention <= 0 {
		c.Retention = DefaultRetention
	}
	if c.CleanupInterval <= 0 {
		c.CleanupInterval = DefaultCleanupInterval
	}
	return c
}

// FileSink は sessionId 単位のJSONLファイルへ追記する。
type FileSink struct {
	config FileSinkConfig
	now    func() time.Time

	mu          sync.Mutex
	lastCleanup time.Time
}

type FileSinkOption func(*FileSink)

func WithFileSinkClock(now func() time.Time) FileSinkOption {
	return func(s *FileSink) {
		if now != nil {
			s.now = now
		}
	}
}

// NewFileSink は出力ディレクトリを作成して sink を返す。
func NewFileSink(config FileSinkConfig, options ...FileSinkOption) (*FileSink, error) {
	resolved := config.withDefaults()
	if resolved.Directory == "" {
		return nil, errors.New("client diagnostics: file sink directory is required")
	}
	if err := os.MkdirAll(resolved.Directory, 0o750); err != nil {
		return nil, fmt.Errorf("create client diagnostics directory: %w", err)
	}
	// 書き込み可否をここで一度だけ確かめる。Dockerのbind mountはホスト側の
	// 所有者次第で非rootのコンテナから書けないことがあり、その場合は
	// イベントごとに失敗ログを出し続けるより、起動時に一度落として
	// 標準ログのみへ縮退したほうが運用しやすい。
	if err := probeWritable(resolved.Directory); err != nil {
		return nil, err
	}
	sink := &FileSink{config: resolved, now: time.Now}
	for _, option := range options {
		option(sink)
	}
	sink.PurgeExpired()
	return sink, nil
}

func (s *FileSink) Directory() string {
	return s.config.Directory
}

// WriteClientDiagnosticEvent は1件をJSONL 1行として追記する。
func (s *FileSink) WriteClientDiagnosticEvent(event domain.ClientDiagnosticEvent) error {
	name, err := sessionFileName(event.SessionID)
	if err != nil {
		return err
	}
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode client diagnostic event: %w", err)
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	s.maybeCleanupLocked()

	path := filepath.Join(s.config.Directory, name)
	if err := s.rotateIfNeededLocked(path, int64(len(line))); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open client diagnostics file: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("write client diagnostics file: %w", err)
	}
	return nil
}

func (s *FileSink) rotateIfNeededLocked(path string, incomingBytes int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat client diagnostics file: %w", err)
	}
	if info.Size()+incomingBytes <= s.config.MaxFileBytes {
		return nil
	}
	rotated := path + rotatedSuffix
	if err := os.Remove(rotated); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove rotated client diagnostics file: %w", err)
	}
	if err := os.Rename(path, rotated); err != nil {
		return fmt.Errorf("rotate client diagnostics file: %w", err)
	}
	return nil
}

func (s *FileSink) maybeCleanupLocked() {
	now := s.now()
	if !s.lastCleanup.IsZero() && now.Sub(s.lastCleanup) < s.config.CleanupInterval {
		return
	}
	s.lastCleanup = now
	s.purgeExpiredLocked(now)
}

// PurgeExpired は保持期間を過ぎたファイルを削除する。
func (s *FileSink) PurgeExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.lastCleanup = now
	s.purgeExpiredLocked(now)
}

func (s *FileSink) purgeExpiredLocked(now time.Time) {
	entries, err := os.ReadDir(s.config.Directory)
	if err != nil {
		return
	}
	deadline := now.Add(-s.config.Retention)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(deadline) {
			_ = os.Remove(filepath.Join(s.config.Directory, entry.Name()))
		}
	}
}

func probeWritable(directory string) error {
	path := filepath.Join(directory, ".write-probe")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("client diagnostics directory is not writable: %w", err)
	}
	closeErr := file.Close()
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("client diagnostics directory is not writable: %w", removeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("client diagnostics directory is not writable: %w", closeErr)
	}
	return nil
}

func sessionFileName(sessionID string) (string, error) {
	if !safeSessionIDPattern.MatchString(sessionID) || sessionID == "." || sessionID == ".." {
		return "", ErrUnsafeSessionID
	}
	return sessionID + fileExtension, nil
}
