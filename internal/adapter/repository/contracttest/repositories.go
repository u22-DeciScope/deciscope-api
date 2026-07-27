package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	appaccess "deciscope-core-api/internal/application/access"
	appauth "deciscope-core-api/internal/application/auth"
	appworkspace "deciscope-core-api/internal/application/workspace"
	"deciscope-core-api/internal/domain"
)

type Repositories struct {
	Meetings application.MeetingRepository
	Events   application.EventRepository
	Jobs     application.JobRepository
	Auth     AuthWorkspaceRepository
}

type Factory func(t *testing.T) Repositories

type AuthWorkspaceRepository interface {
	appauth.Repository
	appworkspace.Repository
	appaccess.Repository
}

type Store interface {
	application.MeetingRepository
	application.EventRepository
	application.JobRepository
}

func FromStore(store Store) Repositories {
	return Repositories{
		Meetings: store, Events: store, Jobs: store,
	}
}

func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("meetings", func(t *testing.T) {
		repos := factory(t)
		ctx := context.Background()

		meeting, err := repos.Meetings.CreateMeeting(ctx, "w_test", "", "")
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

		meetings, err := repos.Meetings.ListMeetings(ctx, "w_test")
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

	t.Run("jobs", func(t *testing.T) {
		repos := factory(t)
		ctx := context.Background()

		job, err := repos.Jobs.CreateJob(ctx, "w_test", "file.extract_audio", "", "")
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

		failed, err := repos.Jobs.CreateJob(ctx, "w_test", "report.final", "", "running")
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

	t.Run("auth workspace", func(t *testing.T) {
		repos := factory(t)
		if repos.Auth == nil {
			t.Skip("auth workspace repository not provided")
		}
		ctx := context.Background()

		owner, err := repos.Auth.FindOrCreateUser(ctx, appauth.Identity{UID: "owner", Email: "owner@example.com", Name: "Owner"})
		if err != nil {
			t.Fatalf("FindOrCreateUser(owner) error = %v", err)
		}
		member, err := repos.Auth.FindOrCreateUser(ctx, appauth.Identity{UID: "member", Email: "member@example.com", Name: "Member"})
		if err != nil {
			t.Fatalf("FindOrCreateUser(member) error = %v", err)
		}
		workspace, err := repos.Auth.CreateWorkspace(ctx, owner.ID, "最初のワークスペース", "")
		if err != nil {
			t.Fatalf("CreateWorkspace(initial) error = %v", err)
		}

		expiresAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
		invitation, err := repos.Auth.CreateInvitation(ctx, owner.ID, workspace.ID, member.Email, domain.WorkspaceRoleAdmin, "hash_member_admin", expiresAt)
		if err != nil {
			t.Fatalf("CreateInvitation() error = %v", err)
		}
		if invitation.NormalizedEmail != "member@example.com" {
			t.Fatalf("invitation normalized email = %q", invitation.NormalizedEmail)
		}
		if invitation.Status != domain.WorkspaceInvitationStatusPending {
			t.Fatalf("invitation status = %q, want pending", invitation.Status)
		}
		if _, err := repos.Auth.CreateInvitation(ctx, owner.ID, workspace.ID, " MEMBER@example.com ", domain.WorkspaceRoleAdmin, "hash_other", expiresAt); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("CreateInvitation(duplicate) error = %v, want ErrConflict", err)
		}
		found, err := repos.Auth.InvitationByTokenHash(ctx, "hash_member_admin")
		if err != nil || found.ID != invitation.ID {
			t.Fatalf("InvitationByTokenHash() = %+v, %v; want created invitation", found, err)
		}
		if _, err := repos.Auth.InvitationByTokenHash(ctx, "missing_hash"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("InvitationByTokenHash(missing) error = %v, want ErrNotFound", err)
		}
		if err := repos.Auth.AcceptInvitation(ctx, invitation.ID, member.ID); err != nil {
			t.Fatalf("AcceptInvitation() error = %v", err)
		}
		// accepted 済み招待は pending でなくなるため再承認できない。
		if err := repos.Auth.AcceptInvitation(ctx, invitation.ID, member.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("AcceptInvitation(reuse) error = %v, want ErrNotFound", err)
		}
		members, err := repos.Auth.ListMembers(ctx, owner.ID, workspace.ID)
		if err != nil {
			t.Fatalf("ListMembers() error = %v", err)
		}
		if !hasMember(members, member.ID, domain.WorkspaceRoleAdmin) {
			t.Fatalf("members = %+v, want accepted member", members)
		}
		exists, err := repos.Auth.MemberEmailExists(ctx, workspace.ID, "member@example.com")
		if err != nil || !exists {
			t.Fatalf("MemberEmailExists() = %t, %v; want true", exists, err)
		}
		if name, err := repos.Auth.WorkspaceNameByID(ctx, workspace.ID); err != nil || name != workspace.Name {
			t.Fatalf("WorkspaceNameByID() = %q, %v; want %q", name, err, workspace.Name)
		}

		if err := repos.Auth.RemoveMember(ctx, member.ID, workspace.ID, owner.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("RemoveMember(non-owner) error = %v, want ErrForbidden", err)
		}
		if err := repos.Auth.RemoveMember(ctx, owner.ID, workspace.ID, "missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("RemoveMember(missing) error = %v, want ErrNotFound", err)
		}
		if err := repos.Auth.RemoveMember(ctx, owner.ID, workspace.ID, owner.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("RemoveMember(owner) error = %v, want ErrForbidden", err)
		}
		if err := repos.Auth.RemoveMember(ctx, owner.ID, workspace.ID, member.ID); err != nil {
			t.Fatalf("RemoveMember(member) error = %v", err)
		}

		created, err := repos.Auth.CreateWorkspace(ctx, owner.ID, "契約テスト", "説明")
		if err != nil {
			t.Fatalf("CreateWorkspace() error = %v", err)
		}
		if created.Description != "説明" {
			t.Fatalf("CreateWorkspace() description = %q, want %q", created.Description, "説明")
		}
		createdMembers, err := repos.Auth.ListMembers(ctx, owner.ID, created.ID)
		if err != nil {
			t.Fatalf("ListMembers(created workspace) error = %v", err)
		}
		if !hasMember(createdMembers, owner.ID, domain.WorkspaceRoleOwner) {
			t.Fatalf("created workspace members = %+v, want creator as owner", createdMembers)
		}
		if _, err := repos.Auth.GetWorkspace(ctx, member.ID, created.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("GetWorkspace(non-member) error = %v, want ErrNotFound", err)
		}

		description := "更新後の説明"
		updated, err := repos.Auth.UpdateWorkspace(ctx, owner.ID, created.ID, nil, &description)
		if err != nil {
			t.Fatalf("UpdateWorkspace(description) error = %v", err)
		}
		if updated.Name != "契約テスト" || updated.Description != description {
			t.Fatalf("UpdateWorkspace() = %+v, want name kept and description updated", updated)
		}

		// 招待は必ず pending で作成され、承諾 (AcceptInvitation) で初めてメンバーになる。
		second, err := repos.Auth.CreateInvitation(ctx, owner.ID, created.ID, member.Email, domain.WorkspaceRoleViewer, "hash_second_viewer", expiresAt)
		if err != nil {
			t.Fatalf("CreateInvitation(second workspace) error = %v", err)
		}
		if second.Status != domain.WorkspaceInvitationStatusPending {
			t.Fatalf("CreateInvitation() status = %q, want pending", second.Status)
		}
		// 取り消した招待は pending でなくなるため承諾できない。
		if err := repos.Auth.RevokeInvitation(ctx, owner.ID, created.ID, second.ID); err != nil {
			t.Fatalf("RevokeInvitation() error = %v", err)
		}
		if err := repos.Auth.AcceptInvitation(ctx, second.ID, member.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("AcceptInvitation(revoked) error = %v, want ErrNotFound", err)
		}
		// DeleteInvitation はメール送信失敗時のロールバックに使う。
		third, err := repos.Auth.CreateInvitation(ctx, owner.ID, created.ID, member.Email, domain.WorkspaceRoleViewer, "hash_third_viewer", expiresAt)
		if err != nil {
			t.Fatalf("CreateInvitation(third) error = %v", err)
		}
		if err := repos.Auth.DeleteInvitation(ctx, third.ID); err != nil {
			t.Fatalf("DeleteInvitation() error = %v", err)
		}
		if _, err := repos.Auth.InvitationByTokenHash(ctx, "hash_third_viewer"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("InvitationByTokenHash(deleted) error = %v, want ErrNotFound", err)
		}
		// メンバー化は残りのテストで前提となるため、通常フローで参加させる。
		fourth, err := repos.Auth.CreateInvitation(ctx, owner.ID, created.ID, member.Email, domain.WorkspaceRoleViewer, "hash_fourth_viewer", expiresAt)
		if err != nil {
			t.Fatalf("CreateInvitation(fourth) error = %v", err)
		}
		if err := repos.Auth.AcceptInvitation(ctx, fourth.ID, member.ID); err != nil {
			t.Fatalf("AcceptInvitation(fourth) error = %v", err)
		}
		acceptedMembers, err := repos.Auth.ListMembers(ctx, owner.ID, created.ID)
		if err != nil {
			t.Fatalf("ListMembers(after accept) error = %v", err)
		}
		if !hasMember(acceptedMembers, member.ID, domain.WorkspaceRoleViewer) {
			t.Fatalf("members = %+v, want accepted viewer", acceptedMembers)
		}

		// ロール変更は owner のみが実行できる。admin/viewer は拒否される。
		if _, err := repos.Auth.UpdateMemberRole(ctx, member.ID, created.ID, member.ID, domain.WorkspaceRoleAdmin); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("UpdateMemberRole(by viewer) error = %v, want ErrForbidden", err)
		}
		changed, err := repos.Auth.UpdateMemberRole(ctx, owner.ID, created.ID, member.ID, domain.WorkspaceRoleAdmin)
		if err != nil {
			t.Fatalf("UpdateMemberRole(by owner) error = %v", err)
		}
		if changed.Role != domain.WorkspaceRoleAdmin {
			t.Fatalf("UpdateMemberRole() role = %q, want admin", changed.Role)
		}
		if _, err := repos.Auth.UpdateMemberRole(ctx, member.ID, created.ID, member.ID, domain.WorkspaceRoleViewer); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("UpdateMemberRole(by admin) error = %v, want ErrForbidden", err)
		}
		if _, err := repos.Auth.UpdateMemberRole(ctx, owner.ID, created.ID, owner.ID, domain.WorkspaceRoleViewer); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("UpdateMemberRole(demote owner) error = %v, want ErrForbidden", err)
		}
	})
}

func createMeeting(t *testing.T, ctx context.Context, repos Repositories) *domain.Meeting {
	t.Helper()
	meeting, err := repos.Meetings.CreateMeeting(ctx, "w_test", "Contract test", "fixture_replay")
	if err != nil {
		t.Fatalf("CreateMeeting() error = %v", err)
	}
	return meeting
}

func hasMember(members []domain.WorkspaceMember, userID, role string) bool {
	for _, member := range members {
		if member.UserID == userID && member.Role == role {
			return true
		}
	}
	return false
}
