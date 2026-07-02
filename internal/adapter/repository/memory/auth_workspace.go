package memory

import (
	"context"
	"strings"
	"sync"
	"time"

	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/domain"
)

type AuthWorkspaceRepository struct {
	mu          sync.Mutex
	users       map[string]domain.User
	identities  map[string]string
	sessions    map[string]domain.Session
	workspaces  map[string]domain.Workspace
	members     map[string]map[string]string
	invitations map[string]domain.WorkspaceInvitation
	meetings    *MemoryStore
}

func NewAuthWorkspaceRepository(meetings *MemoryStore) *AuthWorkspaceRepository {
	return &AuthWorkspaceRepository{
		users: make(map[string]domain.User), identities: make(map[string]string),
		sessions: make(map[string]domain.Session), workspaces: make(map[string]domain.Workspace),
		members: make(map[string]map[string]string), invitations: make(map[string]domain.WorkspaceInvitation),
		meetings: meetings,
	}
}

func (r *AuthWorkspaceRepository) FindOrCreateUser(_ context.Context, identity appauth.Identity) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id := r.identities[identity.UID]; id != "" {
		user := r.users[id]
		return &user, nil
	}
	name := strings.TrimSpace(identity.Name)
	if name == "" {
		name, _, _ = strings.Cut(identity.Email, "@")
	}
	user := domain.User{ID: domain.NewUUID(), DisplayName: name, Email: strings.TrimSpace(identity.Email)}
	r.users[user.ID], r.identities[identity.UID] = user, user.ID
	return &user, nil
}

func (r *AuthWorkspaceRepository) AcceptInvitations(_ context.Context, userID, normalizedEmail string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, invitation := range r.invitations {
		if invitation.Status == "pending" && invitation.NormalizedEmail == normalizedEmail {
			if r.members[invitation.WorkspaceID] == nil {
				r.members[invitation.WorkspaceID] = make(map[string]string)
			}
			role := domain.NormalizeWorkspaceRole(invitation.Role)
			if role == "" {
				role = domain.WorkspaceRoleViewer
			}
			r.members[invitation.WorkspaceID][userID] = role
			invitation.Status = "accepted"
			r.invitations[id] = invitation
		}
	}
	return nil
}

func (r *AuthWorkspaceRepository) EnsureInitialWorkspace(_ context.Context, userID, displayName, email string) (*domain.Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for workspaceID, members := range r.members {
		if members[userID] != "" {
			workspace := r.workspaces[workspaceID]
			workspace.Role = domain.NormalizeWorkspaceRole(members[userID])
			return &workspace, nil
		}
	}
	if displayName == "" {
		displayName, _, _ = strings.Cut(email, "@")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	workspace := domain.Workspace{ID: domain.NewUUID(), Name: displayName + "のワークスペース", Role: domain.WorkspaceRoleOwner, CreatedAt: now, UpdatedAt: now}
	r.workspaces[workspace.ID] = workspace
	r.members[workspace.ID] = map[string]string{userID: domain.WorkspaceRoleOwner}
	return &workspace, nil
}

func (r *AuthWorkspaceRepository) CreateSession(_ context.Context, session domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[session.TokenHash] = session
	return nil
}

func (r *AuthWorkspaceRepository) SessionByTokenHash(_ context.Context, tokenHash string) (*domain.Session, *domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[tokenHash]
	if !ok {
		return nil, nil, domain.ErrNotFound
	}
	user := r.users[session.UserID]
	return &session, &user, nil
}

func (r *AuthWorkspaceRepository) RevokeSession(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for hash, session := range r.sessions {
		if session.ID == sessionID {
			delete(r.sessions, hash)
		}
	}
	return nil
}

func (r *AuthWorkspaceRepository) SetCurrentWorkspace(_ context.Context, sessionID, workspaceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for hash, session := range r.sessions {
		if session.ID == sessionID {
			session.CurrentWorkspaceID = workspaceID
			r.sessions[hash] = session
		}
	}
	return nil
}

func (r *AuthWorkspaceRepository) ListWorkspaces(_ context.Context, userID string) ([]domain.Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.Workspace
	for id, members := range r.members {
		if role := members[userID]; role != "" {
			workspace := r.workspaces[id]
			workspace.Role = domain.NormalizeWorkspaceRole(role)
			result = append(result, workspace)
		}
	}
	return result, nil
}

func (r *AuthWorkspaceRepository) GetWorkspace(_ context.Context, userID, workspaceID string) (*domain.Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	role := r.members[workspaceID][userID]
	if role == "" {
		return nil, domain.ErrNotFound
	}
	workspace := r.workspaces[workspaceID]
	workspace.Role = domain.NormalizeWorkspaceRole(role)
	return &workspace, nil
}

func (r *AuthWorkspaceRepository) UpdateWorkspaceName(_ context.Context, userID, workspaceID, name string) (*domain.Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !domain.CanManageWorkspace(r.members[workspaceID][userID]) {
		return nil, domain.ErrForbidden
	}
	workspace := r.workspaces[workspaceID]
	workspace.Name, workspace.UpdatedAt, workspace.Role = name, time.Now().UTC().Format(time.RFC3339), domain.WorkspaceRoleOwner
	r.workspaces[workspaceID] = workspace
	return &workspace, nil
}

func (r *AuthWorkspaceRepository) ListMembers(_ context.Context, userID, workspaceID string) ([]domain.WorkspaceMember, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.members[workspaceID][userID] == "" {
		return nil, domain.ErrNotFound
	}
	var result []domain.WorkspaceMember
	for memberID, role := range r.members[workspaceID] {
		user := r.users[memberID]
		result = append(result, domain.WorkspaceMember{WorkspaceID: workspaceID, UserID: memberID, DisplayName: user.DisplayName, Email: user.Email, Role: domain.NormalizeWorkspaceRole(role)})
	}
	return result, nil
}

func (r *AuthWorkspaceRepository) CreateInvitation(_ context.Context, userID, workspaceID, email, role string) (*domain.WorkspaceInvitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !domain.CanManageWorkspace(r.members[workspaceID][userID]) {
		return nil, domain.ErrForbidden
	}
	role = domain.NormalizeWorkspaceRole(role)
	if !domain.ValidWorkspaceInvitationRole(role) {
		return nil, domain.ErrInvalidArgument
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	for _, existing := range r.invitations {
		if existing.WorkspaceID == workspaceID && existing.NormalizedEmail == normalizedEmail && existing.Status == "pending" {
			return nil, domain.ErrConflict
		}
	}
	invitation := domain.WorkspaceInvitation{ID: domain.NewUUID(), WorkspaceID: workspaceID, Email: strings.TrimSpace(email), NormalizedEmail: normalizedEmail, Role: role, Status: "pending", InvitedBy: userID, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	r.invitations[invitation.ID] = invitation
	return &invitation, nil
}

func (r *AuthWorkspaceRepository) RevokeInvitation(_ context.Context, userID, workspaceID, invitationID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !domain.CanManageWorkspace(r.members[workspaceID][userID]) {
		return domain.ErrForbidden
	}
	invitation, ok := r.invitations[invitationID]
	if !ok || invitation.WorkspaceID != workspaceID || invitation.Status != "pending" {
		return domain.ErrNotFound
	}
	invitation.Status = "revoked"
	r.invitations[invitationID] = invitation
	return nil
}

func (r *AuthWorkspaceRepository) ListInvitations(_ context.Context, userID, workspaceID string) ([]domain.WorkspaceInvitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !domain.CanManageWorkspace(r.members[workspaceID][userID]) {
		return nil, domain.ErrForbidden
	}
	var result []domain.WorkspaceInvitation
	for _, invitation := range r.invitations {
		if invitation.WorkspaceID == workspaceID && invitation.Status == "pending" {
			result = append(result, invitation)
		}
	}
	return result, nil
}

func (r *AuthWorkspaceRepository) RemoveMember(_ context.Context, userID, workspaceID, memberID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !domain.CanManageWorkspace(r.members[workspaceID][userID]) {
		return domain.ErrForbidden
	}
	if r.members[workspaceID][memberID] == "" {
		return domain.ErrNotFound
	}
	if domain.IsWorkspaceOwner(r.members[workspaceID][memberID]) {
		return domain.ErrForbidden
	}
	delete(r.members[workspaceID], memberID)
	return nil
}

func (r *AuthWorkspaceRepository) UpdateMemberRole(_ context.Context, userID, workspaceID, memberID, role string) (*domain.WorkspaceMember, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !domain.CanManageWorkspace(r.members[workspaceID][userID]) {
		return nil, domain.ErrForbidden
	}
	role = domain.NormalizeWorkspaceRole(role)
	if !domain.ValidWorkspaceInvitationRole(role) {
		return nil, domain.ErrInvalidArgument
	}
	if r.members[workspaceID][memberID] == "" {
		return nil, domain.ErrNotFound
	}
	if domain.IsWorkspaceOwner(r.members[workspaceID][memberID]) {
		return nil, domain.ErrForbidden
	}
	r.members[workspaceID][memberID] = role
	user := r.users[memberID]
	return &domain.WorkspaceMember{WorkspaceID: workspaceID, UserID: memberID, DisplayName: user.DisplayName, Email: user.Email, Role: role}, nil
}

func (r *AuthWorkspaceRepository) CanAccessMeeting(_ context.Context, userID, meetingID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.meetings.mu.Lock()
	defer r.meetings.mu.Unlock()
	meeting, ok := r.meetings.meetings[meetingID]
	if !ok || r.members[meeting.WorkspaceID][userID] == "" {
		return domain.ErrNotFound
	}
	return nil
}

func (r *AuthWorkspaceRepository) CanAccessJob(_ context.Context, userID, jobID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.meetings.mu.Lock()
	defer r.meetings.mu.Unlock()
	job, ok := r.meetings.jobs[jobID]
	if !ok || r.members[job.WorkspaceID][userID] == "" {
		return domain.ErrNotFound
	}
	return nil
}

var _ appauth.Repository = (*AuthWorkspaceRepository)(nil)
