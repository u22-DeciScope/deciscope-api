package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

func TestServiceValidatesWorkspaceName(t *testing.T) {
	service := newTestService(newFakeRepository())

	name := " "
	_, err := service.UpdateWorkspace(context.Background(), "u_test", "w_test", &name, nil)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("UpdateWorkspace() error = %v, want ErrInvalidArgument", err)
	}
	if _, err := service.UpdateWorkspace(context.Background(), "u_test", "w_test", nil, nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("UpdateWorkspace(empty patch) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := service.CreateWorkspace(context.Background(), "u_test", "  ", ""); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("CreateWorkspace(blank name) error = %v, want ErrInvalidArgument", err)
	}
}

func TestServiceCreatesSampleMeetingOnlyForFirstWorkspace(t *testing.T) {
	ctx := context.Background()

	// 所属0件で最初のワークスペースを作成したときだけサンプル会議を投入する。
	repository := newFakeRepository()
	creator := &fakeSampleMeetingCreator{}
	service := newTestService(repository)
	service.SetSampleMeetingCreator(creator)

	first, err := service.CreateWorkspace(ctx, "u_new", "最初のWS", "")
	if err != nil {
		t.Fatalf("CreateWorkspace(first) error = %v", err)
	}
	if creator.calls != 1 || creator.lastWorkspaceID != first.ID || creator.lastUserID != "u_new" {
		t.Fatalf("sample creator = %+v, want called once with created workspace id", creator)
	}

	// 既に所属がある場合は投入しない。
	repository.workspacesByUser["u_member"] = []domain.Workspace{{ID: "w_existing"}}
	if _, err := service.CreateWorkspace(ctx, "u_member", "2つ目のWS", ""); err != nil {
		t.Fatalf("CreateWorkspace(second) error = %v", err)
	}
	if creator.calls != 1 {
		t.Fatalf("sample creator calls = %d, want 1 (no sample for non-first workspace)", creator.calls)
	}

	// creator 未設定なら何も起きない。
	plain := newTestService(newFakeRepository())
	if _, err := plain.CreateWorkspace(ctx, "u_other", "WS", ""); err != nil {
		t.Fatalf("CreateWorkspace(no creator) error = %v", err)
	}
}

func TestServiceCreateWorkspaceSucceedsEvenIfSampleMeetingFails(t *testing.T) {
	repository := newFakeRepository()
	creator := &fakeSampleMeetingCreator{err: errors.New("sample insert failed")}
	service := newTestService(repository)
	service.SetSampleMeetingCreator(creator)

	workspace, err := service.CreateWorkspace(context.Background(), "u_new", "最初のWS", "")
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v, want success even if sample fails", err)
	}
	if workspace == nil || workspace.ID == "" {
		t.Fatalf("workspace = %+v", workspace)
	}
	if creator.calls != 1 {
		t.Fatalf("sample creator calls = %d, want 1", creator.calls)
	}
}

type fakeSampleMeetingCreator struct {
	calls           int
	lastWorkspaceID string
	lastUserID      string
	err             error
}

func (c *fakeSampleMeetingCreator) CreateSampleMeeting(_ context.Context, workspaceID, userID string) error {
	c.calls++
	c.lastWorkspaceID = workspaceID
	c.lastUserID = userID
	return c.err
}

func TestServiceCreateInvitationValidatesInput(t *testing.T) {
	repository := newFakeRepository()
	service := newTestService(repository)
	ctx := context.Background()

	if _, err := service.CreateInvitation(ctx, "u_owner", "Owner", "w_test", "not-an-email", "viewer"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("CreateInvitation(invalid email) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := service.CreateInvitation(ctx, "u_owner", "Owner", "w_test", "user@example.com", "owner"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("CreateInvitation(owner role) error = %v, want ErrInvalidArgument", err)
	}
	repository.memberEmails["member@example.com"] = true
	if _, err := service.CreateInvitation(ctx, "u_owner", "Owner", "w_test", "member@example.com", "viewer"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("CreateInvitation(already member) error = %v, want ErrConflict", err)
	}
}

func TestServiceCreateInvitationStoresHashAndSendsMail(t *testing.T) {
	repository := newFakeRepository()
	mailer := &captureMailer{}
	service := NewService(repository, mailer, "https://app.example.com")
	ctx := context.Background()

	invitation, err := service.CreateInvitation(ctx, "u_owner", "Owner", "w_test", "User@Example.com", "")
	if err != nil {
		t.Fatalf("CreateInvitation() error = %v", err)
	}
	if invitation.Role != domain.WorkspaceRoleViewer {
		t.Fatalf("default role = %q, want viewer", invitation.Role)
	}
	if repository.lastTokenHash == "" || len(repository.lastTokenHash) != 64 {
		t.Fatalf("token hash = %q, want sha256 hex", repository.lastTokenHash)
	}
	if !strings.HasPrefix(mailer.last.AcceptURL, "https://app.example.com/invitations/accept?token=") {
		t.Fatalf("accept url = %q", mailer.last.AcceptURL)
	}
	rawToken := strings.TrimPrefix(mailer.last.AcceptURL, "https://app.example.com/invitations/accept?token=")
	if hashInvitationToken(rawToken) != repository.lastTokenHash {
		t.Fatalf("DB に保存された token_hash が生tokenのSHA-256と一致しません")
	}
	if strings.Contains(mailer.last.AcceptURL, repository.lastTokenHash) {
		t.Fatalf("accept url に token_hash が含まれています")
	}
	if mailer.last.WorkspaceName != "テストWS" || mailer.last.InviterName != "Owner" {
		t.Fatalf("mail content = %+v", mailer.last)
	}
}

func TestServiceCreateInvitationRollsBackWhenMailFails(t *testing.T) {
	repository := newFakeRepository()
	service := NewService(repository, failingMailer{}, "")
	_, err := service.CreateInvitation(context.Background(), "u_owner", "Owner", "w_test", "user@example.com", "viewer")
	if !errors.Is(err, ErrInvitationEmailDelivery) {
		t.Fatalf("CreateInvitation(mail failure) error = %v, want ErrInvitationEmailDelivery", err)
	}
	if len(repository.invitations) != 0 {
		t.Fatalf("invitation should be rolled back, got %d invitations", len(repository.invitations))
	}
}

func TestServiceAcceptInvitationFlow(t *testing.T) {
	repository := newFakeRepository()
	mailer := &captureMailer{}
	service := NewService(repository, mailer, "http://localhost:5193")
	ctx := context.Background()

	if _, err := service.CreateInvitation(ctx, "u_owner", "Owner", "w_test", "invitee@example.com", "viewer"); err != nil {
		t.Fatalf("CreateInvitation() error = %v", err)
	}
	rawToken := strings.TrimPrefix(mailer.last.AcceptURL, "http://localhost:5193/invitations/accept?token=")

	// メールアドレス不一致は 403 相当のエラー。
	if _, err := service.AcceptInvitation(ctx, "u_other", "other@example.com", rawToken); !errors.Is(err, ErrInvitationEmailMismatch) {
		t.Fatalf("AcceptInvitation(mismatch) error = %v, want ErrInvitationEmailMismatch", err)
	}
	// 一致すればメンバーに追加される (大文字小文字は正規化)。
	if _, err := service.AcceptInvitation(ctx, "u_invitee", "Invitee@Example.com", rawToken); err != nil {
		t.Fatalf("AcceptInvitation() error = %v", err)
	}
	if !repository.accepted["u_invitee"] {
		t.Fatalf("accepted member not recorded")
	}
	// accepted 済み token は再利用できない。
	if _, err := service.AcceptInvitation(ctx, "u_invitee", "invitee@example.com", rawToken); !errors.Is(err, ErrInvitationAlreadyAccepted) {
		t.Fatalf("AcceptInvitation(reuse) error = %v, want ErrInvitationAlreadyAccepted", err)
	}
	// 不正な token は 404。
	if _, err := service.AcceptInvitation(ctx, "u_invitee", "invitee@example.com", "bogus"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("AcceptInvitation(bogus token) error = %v, want ErrNotFound", err)
	}
}

func TestServiceAcceptInvitationRejectsExpiredAndRevoked(t *testing.T) {
	repository := newFakeRepository()
	mailer := &captureMailer{}
	service := NewService(repository, mailer, "http://localhost:5193")
	ctx := context.Background()

	if _, err := service.CreateInvitation(ctx, "u_owner", "Owner", "w_test", "late@example.com", "viewer"); err != nil {
		t.Fatalf("CreateInvitation() error = %v", err)
	}
	rawToken := strings.TrimPrefix(mailer.last.AcceptURL, "http://localhost:5193/invitations/accept?token=")

	// 期限切れにする。
	for id, invitation := range repository.invitations {
		invitation.ExpiresAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
		repository.invitations[id] = invitation
	}
	if _, err := service.AcceptInvitation(ctx, "u_late", "late@example.com", rawToken); !errors.Is(err, ErrInvitationExpired) {
		t.Fatalf("AcceptInvitation(expired) error = %v, want ErrInvitationExpired", err)
	}
	// preview でも expired と分かる。
	preview, err := service.PreviewInvitation(ctx, rawToken)
	if err != nil {
		t.Fatalf("PreviewInvitation() error = %v", err)
	}
	if preview.Status != domain.WorkspaceInvitationStatusExpired {
		t.Fatalf("preview status = %q, want expired", preview.Status)
	}

	// revoked も承諾できない。
	for id, invitation := range repository.invitations {
		invitation.Status = domain.WorkspaceInvitationStatusRevoked
		invitation.ExpiresAt = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
		repository.invitations[id] = invitation
	}
	if _, err := service.AcceptInvitation(ctx, "u_late", "late@example.com", rawToken); !errors.Is(err, ErrInvitationRevoked) {
		t.Fatalf("AcceptInvitation(revoked) error = %v, want ErrInvitationRevoked", err)
	}
}

func TestServiceRejectsDuplicatePendingInvitation(t *testing.T) {
	repository := newFakeRepository()
	repository.invitations["inv_existing"] = domain.WorkspaceInvitation{
		ID: "inv_existing", WorkspaceID: "w_test", NormalizedEmail: "user@example.com",
		Status:    domain.WorkspaceInvitationStatusPending,
		ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}
	service := newTestService(repository)

	_, err := service.CreateInvitation(context.Background(), "u_owner", "Owner", "w_test", " User@example.com ", "viewer")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("CreateInvitation() error = %v, want ErrConflict", err)
	}
}

func TestServiceReplacesExpiredPendingInvitation(t *testing.T) {
	repository := newFakeRepository()
	repository.invitations["inv_expired"] = domain.WorkspaceInvitation{
		ID: "inv_expired", WorkspaceID: "w_test", NormalizedEmail: "user@example.com",
		Status:    domain.WorkspaceInvitationStatusPending,
		ExpiresAt: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
	}
	service := newTestService(repository)

	invitation, err := service.CreateInvitation(context.Background(), "u_owner", "Owner", "w_test", "user@example.com", "viewer")
	if err != nil {
		t.Fatalf("CreateInvitation(after expiry) error = %v", err)
	}
	if _, exists := repository.invitations["inv_expired"]; exists {
		t.Fatalf("expired invitation should be deleted before re-invite")
	}
	if invitation.Status != domain.WorkspaceInvitationStatusPending {
		t.Fatalf("new invitation status = %q, want pending", invitation.Status)
	}
}

func TestServiceNormalizesRemoveMemberErrors(t *testing.T) {
	repository := newFakeRepository()
	repository.members = []domain.WorkspaceMember{{UserID: "u_owner", Role: "owner"}}
	service := newTestService(repository)

	if err := service.RemoveMember(context.Background(), "u_owner", "w_test", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RemoveMember(missing) error = %v, want ErrNotFound", err)
	}
	if err := service.RemoveMember(context.Background(), "u_owner", "w_test", "u_owner"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("RemoveMember(owner) error = %v, want ErrForbidden", err)
	}
}

func newTestService(repository *fakeRepository) *Service {
	return NewService(repository, &captureMailer{}, "http://localhost:5193")
}

type captureMailer struct {
	last InvitationEmail
}

func (m *captureMailer) SendInvitation(_ context.Context, email InvitationEmail) error {
	m.last = email
	return nil
}

type failingMailer struct{}

func (failingMailer) SendInvitation(context.Context, InvitationEmail) error {
	return errors.New("smtp unavailable")
}

type fakeRepository struct {
	invitations      map[string]domain.WorkspaceInvitation
	members          []domain.WorkspaceMember
	memberEmails     map[string]bool
	accepted         map[string]bool
	workspacesByUser map[string][]domain.Workspace
	lastTokenHash    string
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		invitations:      make(map[string]domain.WorkspaceInvitation),
		memberEmails:     make(map[string]bool),
		accepted:         make(map[string]bool),
		workspacesByUser: make(map[string][]domain.Workspace),
	}
}

func (r *fakeRepository) ListWorkspaces(_ context.Context, userID string) ([]domain.Workspace, error) {
	return r.workspacesByUser[userID], nil
}

func (r *fakeRepository) GetWorkspace(_ context.Context, _, workspaceID string) (*domain.Workspace, error) {
	return &domain.Workspace{ID: workspaceID, Name: "テストWS", Role: domain.WorkspaceRoleOwner}, nil
}

func (r *fakeRepository) CreateWorkspace(_ context.Context, userID, name, description string) (*domain.Workspace, error) {
	workspace := domain.Workspace{ID: domain.NewUUID(), Name: name, Description: description, Role: domain.WorkspaceRoleOwner}
	r.workspacesByUser[userID] = append(r.workspacesByUser[userID], workspace)
	return &workspace, nil
}

func (r *fakeRepository) UpdateWorkspace(context.Context, string, string, *string, *string) (*domain.Workspace, error) {
	return &domain.Workspace{}, nil
}

func (r *fakeRepository) ListMembers(context.Context, string, string) ([]domain.WorkspaceMember, error) {
	return r.members, nil
}

func (r *fakeRepository) MemberEmailExists(_ context.Context, _, normalizedEmail string) (bool, error) {
	return r.memberEmails[normalizedEmail], nil
}

func (r *fakeRepository) CreateInvitation(_ context.Context, userID, workspaceID, email, role, tokenHash, expiresAt string) (*domain.WorkspaceInvitation, error) {
	invitation := domain.WorkspaceInvitation{
		ID: domain.NewUUID(), WorkspaceID: workspaceID, Email: email,
		NormalizedEmail: strings.ToLower(strings.TrimSpace(email)), Role: role,
		Status: domain.WorkspaceInvitationStatusPending, InvitedBy: userID,
		TokenHash: tokenHash, ExpiresAt: expiresAt,
	}
	r.invitations[invitation.ID] = invitation
	r.lastTokenHash = tokenHash
	return &invitation, nil
}

func (r *fakeRepository) DeleteInvitation(_ context.Context, invitationID string) error {
	delete(r.invitations, invitationID)
	return nil
}

func (r *fakeRepository) InvitationByTokenHash(_ context.Context, tokenHash string) (*domain.WorkspaceInvitation, error) {
	for _, invitation := range r.invitations {
		if invitation.TokenHash == tokenHash {
			value := invitation
			return &value, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeRepository) AcceptInvitation(_ context.Context, invitationID, userID string) error {
	invitation, ok := r.invitations[invitationID]
	if !ok || invitation.Status != domain.WorkspaceInvitationStatusPending {
		return domain.ErrNotFound
	}
	invitation.Status = domain.WorkspaceInvitationStatusAccepted
	r.invitations[invitationID] = invitation
	r.accepted[userID] = true
	return nil
}

func (r *fakeRepository) WorkspaceNameByID(context.Context, string) (string, error) {
	return "テストWS", nil
}

func (r *fakeRepository) ListInvitations(context.Context, string, string) ([]domain.WorkspaceInvitation, error) {
	var result []domain.WorkspaceInvitation
	for _, invitation := range r.invitations {
		if invitation.Status == domain.WorkspaceInvitationStatusPending {
			result = append(result, invitation)
		}
	}
	return result, nil
}

func (r *fakeRepository) RevokeInvitation(context.Context, string, string, string) error {
	return nil
}

func (r *fakeRepository) RemoveMember(context.Context, string, string, string) error {
	return nil
}

func (r *fakeRepository) UpdateMemberRole(context.Context, string, string, string, string) (*domain.WorkspaceMember, error) {
	return &domain.WorkspaceMember{}, nil
}
