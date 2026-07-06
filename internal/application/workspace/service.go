package workspace

import (
	"context"
	"fmt"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

type Repository interface {
	ListWorkspaces(ctx context.Context, userID string) ([]domain.Workspace, error)
	GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error)
	CreateWorkspace(ctx context.Context, userID, name, description string) (*domain.Workspace, error)
	UpdateWorkspace(ctx context.Context, userID, workspaceID string, name, description *string) (*domain.Workspace, error)
	ListMembers(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceMember, error)
	MemberEmailExists(ctx context.Context, workspaceID, normalizedEmail string) (bool, error)
	CreateInvitation(ctx context.Context, userID, workspaceID, email, role, tokenHash, expiresAt string) (*domain.WorkspaceInvitation, error)
	DeleteInvitation(ctx context.Context, invitationID string) error
	InvitationByTokenHash(ctx context.Context, tokenHash string) (*domain.WorkspaceInvitation, error)
	AcceptInvitation(ctx context.Context, invitationID, userID string) error
	WorkspaceNameByID(ctx context.Context, workspaceID string) (string, error)
	ListInvitations(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceInvitation, error)
	RevokeInvitation(ctx context.Context, userID, workspaceID, invitationID string) error
	RemoveMember(ctx context.Context, userID, workspaceID, memberID string) error
	UpdateMemberRole(ctx context.Context, userID, workspaceID, memberID, role string) (*domain.WorkspaceMember, error)
}

type Service struct {
	repository      Repository
	mailer          InvitationMailer
	frontendBaseURL string
}

func NewService(repository Repository, mailer InvitationMailer, frontendBaseURL string) *Service {
	return &Service{repository: repository, mailer: mailer, frontendBaseURL: frontendBaseURL}
}

func (s *Service) ListWorkspaces(ctx context.Context, userID string) ([]domain.Workspace, error) {
	return s.repository.ListWorkspaces(ctx, userID)
}

func (s *Service) GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error) {
	return s.repository.GetWorkspace(ctx, userID, workspaceID)
}

func (s *Service) CreateWorkspace(ctx context.Context, userID, name, description string) (*domain.Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: workspace name is required", domain.ErrInvalidArgument)
	}
	return s.repository.CreateWorkspace(ctx, userID, name, strings.TrimSpace(description))
}

// UpdateWorkspace は nil のフィールドを変更せず、指定されたフィールドだけ更新する。
func (s *Service) UpdateWorkspace(ctx context.Context, userID, workspaceID string, name, description *string) (*domain.Workspace, error) {
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: workspace name is required", domain.ErrInvalidArgument)
		}
		name = &trimmed
	}
	if description != nil {
		trimmed := strings.TrimSpace(*description)
		description = &trimmed
	}
	if name == nil && description == nil {
		return nil, fmt.Errorf("%w: nothing to update", domain.ErrInvalidArgument)
	}
	return s.repository.UpdateWorkspace(ctx, userID, workspaceID, name, description)
}

func (s *Service) ListMembers(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceMember, error) {
	return s.repository.ListMembers(ctx, userID, workspaceID)
}

func (s *Service) ListInvitations(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceInvitation, error) {
	invitations, err := s.repository.ListInvitations(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for i := range invitations {
		invitations[i].Status = effectiveInvitationStatus(invitations[i], now)
	}
	return invitations, nil
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
