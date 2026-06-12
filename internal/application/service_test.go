package application_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deciscope-core-api/internal/adapter/repository/memory"
	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/infrastructure/storage"
)

func TestServiceCoreUseCases(t *testing.T) {
	ctx := context.Background()
	uploadDir := t.TempDir()
	service := application.NewService(memory.Repositories(memory.NewMemoryStore()), nil, storage.NewLocal(uploadDir))

	meeting, err := service.CreateMeeting(ctx, "Service use cases", "fixture_replay")
	if err != nil {
		t.Fatalf("CreateMeeting() error = %v", err)
	}

	token, err := service.CreateJoinToken(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("CreateJoinToken() error = %v", err)
	}
	if !strings.HasPrefix(token.Token, "local."+meeting.ID+".") {
		t.Fatalf("token = %q, want meeting-scoped local token", token.Token)
	}

	report, err := service.GetOrCreateReport(ctx, meeting.ID)
	if err != nil {
		t.Fatalf("GetOrCreateReport() error = %v", err)
	}
	if report.MeetingID != meeting.ID {
		t.Fatalf("report meeting = %q, want %q", report.MeetingID, meeting.ID)
	}

	result, err := service.UploadFile(ctx, "notes.txt", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}
	if result.Job.Status != "completed" {
		t.Fatalf("job status = %q, want completed", result.Job.Status)
	}
	got, err := os.ReadFile(filepath.Join(uploadDir, result.Job.ID+"_notes.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("uploaded content = %q, want hello", got)
	}
}
