package workspace

import (
	"context"
	"fmt"
	"strings"

	"deciscope-core-api/internal/domain"
)

type Repository interface {
	ListWorkspaces(ctx context.Context, userID string) ([]domain.Workspace, error)
	GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error)
	UpdateWorkspaceName(ctx context.Context, userID, workspaceID, name string) (*domain.Workspace, error)
	ListMembers(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceMember, error)
	CreateInvitation(ctx context.Context, userID, workspaceID, email, role string) (*domain.WorkspaceInvitation, error)
	ListInvitations(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceInvitation, error)
	RevokeInvitation(ctx context.Context, userID, workspaceID, invitationID string) error
	RemoveMember(ctx context.Context, userID, workspaceID, memberID string) error
	UpdateMemberRole(ctx context.Context, userID, workspaceID, memberID, role string) (*domain.WorkspaceMember, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) ListWorkspaces(ctx context.Context, userID string) ([]domain.Workspace, error) {
	return s.repository.ListWorkspaces(ctx, userID)
}

func (s *Service) GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error) {
	return s.repository.GetWorkspace(ctx, userID, workspaceID)
}

func (s *Service) UpdateWorkspaceName(ctx context.Context, userID, workspaceID, name string) (*domain.Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: workspace name is required", domain.ErrInvalidArgument)
	}
	return s.repository.UpdateWorkspaceName(ctx, userID, workspaceID, name)
}

func (s *Service) ListMembers(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceMember, error) {
	return s.repository.ListMembers(ctx, userID, workspaceID)
}

func (s *Service) CreateInvitation(ctx context.Context, userID, workspaceID, email, role string) (*domain.WorkspaceInvitation, error) {
	email = strings.TrimSpace(email)
	normalizedEmail := normalizeEmail(email)
	if normalizedEmail == "" {
		return nil, fmt.Errorf("%w: email is required", domain.ErrInvalidArgument)
	}
	role = domain.NormalizeWorkspaceRole(role)
	if role == "" {
		role = domain.WorkspaceRoleViewer
	}
	if !domain.ValidWorkspaceInvitationRole(role) {
		return nil, fmt.Errorf("%w: invitation role must be admin or viewer", domain.ErrInvalidArgument)
	}
	invitations, err := s.repository.ListInvitations(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, invitation := range invitations {
		if invitation.NormalizedEmail == normalizedEmail {
			return nil, fmt.Errorf("%w: invitation already exists", domain.ErrConflict)
		}
	}
	return s.repository.CreateInvitation(ctx, userID, workspaceID, email, role)
}

func (s *Service) ListInvitations(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceInvitation, error) {
	return s.repository.ListInvitations(ctx, userID, workspaceID)
}

func (s *Service) RevokeInvitation(ctx context.Context, userID, workspaceID, invitationID string) error {
	return s.repository.RevokeInvitation(ctx, userID, workspaceID, invitationID)
}

func (s *Service) RemoveMember(ctx context.Context, userID, workspaceID, memberID string) error {
	members, err := s.repository.ListMembers(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	for _, member := range members {
		if member.UserID != memberID {
			continue
		}
		if domain.IsWorkspaceOwner(member.Role) {
			return domain.ErrForbidden
		}
		return s.repository.RemoveMember(ctx, userID, workspaceID, memberID)
	}
	return domain.ErrNotFound
}

func (s *Service) UpdateMemberRole(ctx context.Context, userID, workspaceID, memberID, role string) (*domain.WorkspaceMember, error) {
	role = domain.NormalizeWorkspaceRole(role)
	if !domain.ValidWorkspaceInvitationRole(role) {
		return nil, fmt.Errorf("%w: member role must be admin or viewer", domain.ErrInvalidArgument)
	}
	members, err := s.repository.ListMembers(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, member := range members {
		if member.UserID != memberID {
			continue
		}
		if domain.IsWorkspaceOwner(member.Role) {
			return nil, domain.ErrForbidden
		}
		return s.repository.UpdateMemberRole(ctx, userID, workspaceID, memberID, role)
	}
	return nil, domain.ErrNotFound
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
