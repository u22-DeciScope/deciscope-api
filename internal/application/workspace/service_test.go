package workspace

import (
	"context"
	"errors"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestServiceValidatesWorkspaceName(t *testing.T) {
	service := NewService(fakeRepository{})

	_, err := service.UpdateWorkspaceName(context.Background(), "u_test", "w_test", " ")
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("UpdateWorkspaceName() error = %v, want ErrInvalidArgument", err)
	}
}

func TestServiceRejectsDuplicatePendingInvitation(t *testing.T) {
	service := NewService(fakeRepository{
		invitations: []domain.WorkspaceInvitation{{
			WorkspaceID: "w_test", NormalizedEmail: "user@example.com", Status: "pending",
		}},
	})

	_, err := service.CreateInvitation(context.Background(), "u_owner", "w_test", " User@example.com ", "viewer")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("CreateInvitation() error = %v, want ErrConflict", err)
	}
}

func TestServiceNormalizesRemoveMemberErrors(t *testing.T) {
	service := NewService(fakeRepository{
		members: []domain.WorkspaceMember{{UserID: "u_owner", Role: "owner"}},
	})

	if err := service.RemoveMember(context.Background(), "u_owner", "w_test", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RemoveMember(missing) error = %v, want ErrNotFound", err)
	}
	if err := service.RemoveMember(context.Background(), "u_owner", "w_test", "u_owner"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("RemoveMember(owner) error = %v, want ErrForbidden", err)
	}
}

type fakeRepository struct {
	invitations []domain.WorkspaceInvitation
	members     []domain.WorkspaceMember
}

func (fakeRepository) ListWorkspaces(context.Context, string) ([]domain.Workspace, error) {
	return nil, nil
}

func (fakeRepository) GetWorkspace(context.Context, string, string) (*domain.Workspace, error) {
	return &domain.Workspace{}, nil
}

func (fakeRepository) UpdateWorkspaceName(context.Context, string, string, string) (*domain.Workspace, error) {
	return &domain.Workspace{}, nil
}

func (r fakeRepository) ListMembers(context.Context, string, string) ([]domain.WorkspaceMember, error) {
	return r.members, nil
}

func (fakeRepository) CreateInvitation(context.Context, string, string, string, string) (*domain.WorkspaceInvitation, error) {
	return &domain.WorkspaceInvitation{}, nil
}

func (r fakeRepository) ListInvitations(context.Context, string, string) ([]domain.WorkspaceInvitation, error) {
	return r.invitations, nil
}

func (fakeRepository) RevokeInvitation(context.Context, string, string, string) error {
	return nil
}

func (fakeRepository) RemoveMember(context.Context, string, string, string) error {
	return nil
}

func (fakeRepository) UpdateMemberRole(context.Context, string, string, string, string) (*domain.WorkspaceMember, error) {
	return &domain.WorkspaceMember{}, nil
}
