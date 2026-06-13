package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

type Repositories struct {
	Meetings application.MeetingRepository
	Events   application.EventRepository
	Reports  application.ReportRepository
	Jobs     application.JobRepository
	Uploads  application.UploadRepository
}

type Factory func(t *testing.T) Repositories

type Store interface {
	application.MeetingRepository
	application.EventRepository
	application.ReportRepository
	application.JobRepository
	application.UploadRepository
}

func FromStore(store Store) Repositories {
	return Repositories{
		Meetings: store, Events: store, Reports: store, Jobs: store, Uploads: store,
	}
}

func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("meetings", func(t *testing.T) {
		repos := factory(t)
		ctx := context.Background()

		meeting, err := repos.Meetings.CreateMeeting(ctx, "", "")
		if err != nil {
			t.Fatalf("CreateMeeting() error = %v", err)
		}
		if meeting.Title != "Untitled meeting" || meeting.Source != "fixture_replay" || meeting.Status != "created" {
			t.Fatalf("meeting defaults = %+v", meeting)
		}
		if meeting.CreatedAt == "" || meeting.UpdatedAt == "" {
			t.Fatalf("meeting timestamps are empty: %+v", meeting)
		}

		got, err := repos.Meetings.GetMeeting(ctx, meeting.ID)
		if err != nil {
			t.Fatalf("GetMeeting() error = %v", err)
		}
		if got.ID != meeting.ID {
			t.Fatalf("GetMeeting() id = %q, want %q", got.ID, meeting.ID)
		}

		meetings, err := repos.Meetings.ListMeetings(ctx)
		if err != nil {
			t.Fatalf("ListMeetings() error = %v", err)
		}
		if len(meetings) != 1 || meetings[0].ID != meeting.ID {
			t.Fatalf("ListMeetings() = %+v", meetings)
		}

		if _, err := repos.Meetings.GetMeeting(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("GetMeeting(missing) error = %v, want ErrNotFound", err)
		}
		if err := repos.Meetings.ResetMeeting(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("ResetMeeting(missing) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("events and reset", func(t *testing.T) {
		repos := factory(t)
		ctx := context.Background()
		meeting := createMeeting(t, ctx, repos)

		partial, err := repos.Events.AppendEvent(ctx, meeting.ID, domain.EventTranscriptPartial, map[string]any{"text": "draft"})
		if err != nil {
			t.Fatalf("AppendEvent(partial) error = %v", err)
		}
		if partial.Seq != 0 {
			t.Fatalf("partial seq = %d, want 0", partial.Seq)
		}

		final, err := repos.Events.AppendEvent(ctx, meeting.ID, domain.EventTranscriptFinal, map[string]any{
			"segment_id":    "seg_001",
			"speaker_label": "Speaker A",
			"text":          "final",
			"start_ms":      10,
			"end_ms":        20,
		})
		if err != nil {
			t.Fatalf("AppendEvent(final) error = %v", err)
		}
		state, err := repos.Events.AppendEvent(ctx, meeting.ID, domain.EventMeetingState, map[string]any{"status": "ended"})
		if err != nil {
			t.Fatalf("AppendEvent(state) error = %v", err)
		}
		if final.Seq != 1 || state.Seq != 2 {
			t.Fatalf("durable sequences = %d, %d, want 1, 2", final.Seq, state.Seq)
		}

		events, err := repos.Events.ListEvents(ctx, meeting.ID, 1)
		if err != nil {
			t.Fatalf("ListEvents() error = %v", err)
		}
		if len(events) != 1 || events[0].Seq != 2 {
			t.Fatalf("events after seq 1 = %+v", events)
		}
		segments, err := repos.Events.ListSegments(ctx, meeting.ID, 0)
		if err != nil {
			t.Fatalf("ListSegments() error = %v", err)
		}
		if len(segments) != 1 || segments[0].SegmentID != "seg_001" || segments[0].Seq != 1 {
			t.Fatalf("segments = %+v", segments)
		}
		ended, err := repos.Meetings.GetMeeting(ctx, meeting.ID)
		if err != nil {
			t.Fatalf("GetMeeting(ended) error = %v", err)
		}
		if ended.Status != "ended" || ended.EndedAt == "" {
			t.Fatalf("ended meeting = %+v", ended)
		}

		if err := repos.Meetings.ResetMeeting(ctx, meeting.ID); err != nil {
			t.Fatalf("ResetMeeting() error = %v", err)
		}
		reset, err := repos.Meetings.GetMeeting(ctx, meeting.ID)
		if err != nil {
			t.Fatalf("GetMeeting(reset) error = %v", err)
		}
		if reset.Status != "created" || reset.EndedAt != "" {
			t.Fatalf("reset meeting = %+v", reset)
		}
		events, err = repos.Events.ListEvents(ctx, meeting.ID, 0)
		if err != nil || len(events) != 0 {
			t.Fatalf("events after reset = %+v, error = %v", events, err)
		}
	})

	t.Run("reports", func(t *testing.T) {
		repos := factory(t)
		ctx := context.Background()
		meeting := createMeeting(t, ctx, repos)

		if _, err := repos.Reports.LatestReport(ctx, meeting.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("LatestReport(empty) error = %v, want ErrNotFound", err)
		}
		first, err := repos.Reports.SaveReport(ctx, meeting.ID, "first")
		if err != nil {
			t.Fatalf("SaveReport(first) error = %v", err)
		}
		second, err := repos.Reports.SaveReport(ctx, meeting.ID, "second")
		if err != nil {
			t.Fatalf("SaveReport(second) error = %v", err)
		}
		latest, err := repos.Reports.LatestReport(ctx, meeting.ID)
		if err != nil {
			t.Fatalf("LatestReport() error = %v", err)
		}
		if latest.ArtifactID != second.ArtifactID || latest.ArtifactID == first.ArtifactID || latest.Content != "second" {
			t.Fatalf("LatestReport() = %+v", latest)
		}
	})

	t.Run("jobs and uploads", func(t *testing.T) {
		repos := factory(t)
		ctx := context.Background()

		job, err := repos.Jobs.CreateJob(ctx, "file.extract_audio", "", "")
		if err != nil {
			t.Fatalf("CreateJob() error = %v", err)
		}
		if job.Status != "queued" {
			t.Fatalf("job status = %q, want queued", job.Status)
		}
		if err := repos.Jobs.CompleteJob(ctx, job.ID, map[string]any{"ok": true}); err != nil {
			t.Fatalf("CompleteJob() error = %v", err)
		}
		completed, err := repos.Jobs.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("GetJob(completed) error = %v", err)
		}
		var result map[string]bool
		if err := json.Unmarshal(completed.Result, &result); err != nil || !result["ok"] {
			t.Fatalf("completed result = %s, error = %v", completed.Result, err)
		}

		upload, err := repos.Uploads.SaveUpload(ctx, "notes.txt", "text/plain", "/tmp/notes.txt", job.ID)
		if err != nil {
			t.Fatalf("SaveUpload() error = %v", err)
		}
		if upload.JobID != job.ID || upload.Filename != "notes.txt" {
			t.Fatalf("upload = %+v", upload)
		}

		failed, err := repos.Jobs.CreateJob(ctx, "report.final", "", "running")
		if err != nil {
			t.Fatalf("CreateJob(failed) error = %v", err)
		}
		if err := repos.Jobs.FailJob(ctx, failed.ID, "boom"); err != nil {
			t.Fatalf("FailJob() error = %v", err)
		}
		failed, err = repos.Jobs.GetJob(ctx, failed.ID)
		if err != nil {
			t.Fatalf("GetJob(failed) error = %v", err)
		}
		if failed.Status != "failed" || failed.Error != "boom" {
			t.Fatalf("failed job = %+v", failed)
		}
		if _, err := repos.Jobs.GetJob(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("GetJob(missing) error = %v, want ErrNotFound", err)
		}
	})
}

func createMeeting(t *testing.T, ctx context.Context, repos Repositories) *domain.Meeting {
	t.Helper()
	meeting, err := repos.Meetings.CreateMeeting(ctx, "Contract test", "fixture_replay")
	if err != nil {
		t.Fatalf("CreateMeeting() error = %v", err)
	}
	return meeting
}
