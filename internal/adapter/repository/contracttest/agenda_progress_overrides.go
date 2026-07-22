package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

// AgendaProgressOverridesFactory builds a fresh
// application.MeetingAgendaProgressOverridesRepository for each subtest.
type AgendaProgressOverridesFactory func(t *testing.T) application.MeetingAgendaProgressOverridesRepository

// RunAgendaProgressOverrides exercises the shared
// MeetingAgendaProgressOverridesRepository contract (missing row, upsert +
// get roundtrip, upsert overwrites) against any implementation.
func RunAgendaProgressOverrides(t *testing.T, factory AgendaProgressOverridesFactory) {
	t.Helper()

	t.Run("get missing returns not found", func(t *testing.T) {
		repo := factory(t)
		ctx := context.Background()
		if _, err := repo.GetAgendaProgressOverrides(ctx, "session_missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("GetAgendaProgressOverrides(missing) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("upsert then get roundtrips payload", func(t *testing.T) {
		repo := factory(t)
		ctx := context.Background()
		payload := json.RawMessage(`{"statusOverrides":{"agenda-1":"discussed"},"currentTopicId":"agenda-2"}`)
		updatedAt := time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)
		if err := repo.UpsertAgendaProgressOverrides(ctx, "session_test", payload, updatedAt); err != nil {
			t.Fatalf("UpsertAgendaProgressOverrides() error = %v", err)
		}
		got, err := repo.GetAgendaProgressOverrides(ctx, "session_test")
		if err != nil {
			t.Fatalf("GetAgendaProgressOverrides() error = %v", err)
		}
		var decoded application.AgendaProgressOverrides
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatalf("unmarshal stored payload: %v", err)
		}
		if decoded.StatusOverrides["agenda-1"] != "discussed" || decoded.CurrentTopicID != "agenda-2" {
			t.Fatalf("decoded overrides = %+v", decoded)
		}
	})

	t.Run("upsert overwrites in place", func(t *testing.T) {
		repo := factory(t)
		ctx := context.Background()
		first := json.RawMessage(`{"statusOverrides":{"agenda-1":"discussed"}}`)
		if err := repo.UpsertAgendaProgressOverrides(ctx, "session_overwrite", first, time.Now().UTC()); err != nil {
			t.Fatalf("UpsertAgendaProgressOverrides(first) error = %v", err)
		}
		second := json.RawMessage(`{"currentTopicId":"agenda-3"}`)
		if err := repo.UpsertAgendaProgressOverrides(ctx, "session_overwrite", second, time.Now().UTC()); err != nil {
			t.Fatalf("UpsertAgendaProgressOverrides(second) error = %v", err)
		}
		got, err := repo.GetAgendaProgressOverrides(ctx, "session_overwrite")
		if err != nil {
			t.Fatalf("GetAgendaProgressOverrides() error = %v", err)
		}
		var decoded application.AgendaProgressOverrides
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatalf("unmarshal stored payload: %v", err)
		}
		if len(decoded.StatusOverrides) != 0 || decoded.CurrentTopicID != "agenda-3" {
			t.Fatalf("decoded overrides after overwrite = %+v, want only currentTopicId=agenda-3", decoded)
		}
	})
}
