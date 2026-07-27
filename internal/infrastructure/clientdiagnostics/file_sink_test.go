package clientdiagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

func newEvent(sessionID, name string) domain.ClientDiagnosticEvent {
	nodeCount := int64(3)
	return domain.ClientDiagnosticEvent{
		Timestamp:   time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC),
		ReceivedAt:  time.Date(2026, 7, 25, 10, 0, 1, 0, time.UTC),
		Event:       name,
		SessionID:   sessionID,
		WorkspaceID: "w_test",
		TabID:       "tab_1",
		NodeCount:   &nodeCount,
	}
}

func TestFileSinkWritesOneJSONLinePerSession(t *testing.T) {
	directory := t.TempDir()
	sink, err := NewFileSink(FileSinkConfig{Directory: directory})
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	for _, name := range []string{"tree_state_changed", "tree_became_empty"} {
		if err := sink.WriteClientDiagnosticEvent(newEvent("session_abc", name)); err != nil {
			t.Fatalf("WriteClientDiagnosticEvent: %v", err)
		}
	}
	if err := sink.WriteClientDiagnosticEvent(newEvent("session_def", "ws_connected")); err != nil {
		t.Fatalf("WriteClientDiagnosticEvent: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(directory, "session_abc.jsonl"))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &decoded); err != nil {
		t.Fatalf("decode line: %v", err)
	}
	if decoded["event"] != "tree_became_empty" || decoded["sessionId"] != "session_abc" {
		t.Fatalf("decoded = %v", decoded)
	}
	if _, err := os.Stat(filepath.Join(directory, "session_def.jsonl")); err != nil {
		t.Fatalf("second session file missing: %v", err)
	}
}

func TestFileSinkRotatesOnSizeLimit(t *testing.T) {
	directory := t.TempDir()
	sink, err := NewFileSink(FileSinkConfig{Directory: directory, MaxFileBytes: 400})
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	for index := 0; index < 8; index++ {
		if err := sink.WriteClientDiagnosticEvent(newEvent("session_abc", "tree_state_changed")); err != nil {
			t.Fatalf("WriteClientDiagnosticEvent: %v", err)
		}
	}

	active := filepath.Join(directory, "session_abc.jsonl")
	rotated := active + rotatedSuffix
	activeInfo, err := os.Stat(active)
	if err != nil {
		t.Fatalf("stat active file: %v", err)
	}
	if activeInfo.Size() > 400 {
		t.Errorf("active file size = %d, want <= 400", activeInfo.Size())
	}
	if _, err := os.Stat(rotated); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
}

func TestFileSinkPurgesFilesBeyondRetention(t *testing.T) {
	directory := t.TempDir()
	stale := filepath.Join(directory, "session_old.jsonl")
	if err := os.WriteFile(stale, []byte("{}\n"), 0o640); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}
	staleTime := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(stale, staleTime, staleTime); err != nil {
		t.Fatalf("age stale file: %v", err)
	}

	sink, err := NewFileSink(FileSinkConfig{Directory: directory, Retention: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := sink.WriteClientDiagnosticEvent(newEvent("session_new", "ws_connected")); err != nil {
		t.Fatalf("WriteClientDiagnosticEvent: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file still present (err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "session_new.jsonl")); err != nil {
		t.Fatalf("fresh file missing: %v", err)
	}
}

func TestNewFileSinkFailsWhenDirectoryIsNotWritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	parent := t.TempDir()
	directory := filepath.Join(parent, "readonly")
	if err := os.Mkdir(directory, 0o500); err != nil {
		t.Fatalf("create read-only directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })

	if _, err := NewFileSink(FileSinkConfig{Directory: directory}); err == nil {
		t.Skip("filesystem does not enforce directory write permissions")
	}
}

func TestFileSinkRejectsUnsafeSessionID(t *testing.T) {
	directory := t.TempDir()
	sink, err := NewFileSink(FileSinkConfig{Directory: directory})
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	for _, sessionID := range []string{"../escape", "a/b", "", strings.Repeat("x", 129)} {
		if err := sink.WriteClientDiagnosticEvent(newEvent(sessionID, "ws_connected")); err == nil {
			t.Errorf("WriteClientDiagnosticEvent(%q) error = nil, want rejection", sessionID)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory entries = %d, want none created", len(entries))
	}
}
