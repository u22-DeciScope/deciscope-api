package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/domain"
)

type AuthWorkspaceRepository struct {
	db *sql.DB
}

func NewAuthWorkspaceRepository(db *sql.DB) *AuthWorkspaceRepository {
	return &AuthWorkspaceRepository{db: db}
}

func (r *AuthWorkspaceRepository) FindOrCreateUser(ctx context.Context, identity appauth.Identity) (*domain.User, error) {
	var user domain.User
	err := r.db.QueryRowContext(ctx, `
		SELECT u.id, u.display_name, e.email
		FROM users u
		JOIN user_identities i ON i.user_id = u.id
		JOIN user_emails e ON e.user_id = u.id AND e.is_primary = 1
		WHERE i.provider = 'firebase' AND i.provider_subject = ?
	`, identity.UID).Scan(&user.ID, &user.DisplayName, &user.Email)
	if err == nil {
		return &user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	user = domain.User{ID: domain.NewUUID(), DisplayName: strings.TrimSpace(identity.Name), Email: strings.TrimSpace(identity.Email)}
	if user.DisplayName == "" {
		user.DisplayName = defaultWorkspaceBase(user.Email)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, display_name, status, created_at, updated_at) VALUES (?, ?, 'active', ?, ?)`,
		user.ID, user.DisplayName, now, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_identities (id, user_id, provider, provider_subject, created_at) VALUES (?, ?, 'firebase', ?, ?)`,
		domain.NewUUID(), user.ID, identity.UID, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_emails (id, user_id, email, normalized_email, verified, is_primary, created_at) VALUES (?, ?, ?, ?, 1, 1, ?)`,
		domain.NewUUID(), user.ID, user.Email, normalizeEmail(user.Email), now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *AuthWorkspaceRepository) AcceptInvitations(ctx context.Context, userID, normalizedEmail string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, workspace_id, role FROM workspace_invitations WHERE normalized_email = ? AND status = 'pending'`, normalizedEmail)
	if err != nil {
		return err
	}
	type invitation struct{ id, workspaceID, role string }
	var invitations []invitation
	for rows.Next() {
		var value invitation
		if err := rows.Scan(&value.id, &value.workspaceID, &value.role); err != nil {
			rows.Close()
			return err
		}
		invitations = append(invitations, value)
	}
	rows.Close()
	for _, invitation := range invitations {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO workspace_members (workspace_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)`,
			invitation.workspaceID, userID, invitation.role, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workspace_invitations SET status = 'accepted', accepted_by = ?, accepted_at = ? WHERE id = ?`,
			userID, now, invitation.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *AuthWorkspaceRepository) EnsureInitialWorkspace(ctx context.Context, userID, displayName, email string) (*domain.Workspace, error) {
	workspaces, err := r.ListWorkspaces(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(workspaces) > 0 {
		return &workspaces[0], nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = defaultWorkspaceBase(email)
	}
	name += "のワークスペース"
	workspace := domain.Workspace{ID: domain.NewUUID(), Name: name, Role: "owner", CreatedAt: now, UpdatedAt: now}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, name, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		workspace.ID, workspace.Name, userID, now, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, joined_at) VALUES (?, ?, 'owner', ?)`,
		workspace.ID, userID, now); err != nil {
		return nil, err
	}
	return &workspace, tx.Commit()
}

func (r *AuthWorkspaceRepository) CreateSession(ctx context.Context, session domain.Session) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_sessions (id, user_id, token_hash, current_workspace_id, created_at, expires_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, session.ID, session.UserID, session.TokenHash, nullable(session.CurrentWorkspaceID), session.CreatedAt, session.ExpiresAt, session.CreatedAt)
	return err
}

func (r *AuthWorkspaceRepository) SessionByTokenHash(ctx context.Context, tokenHash string) (*domain.Session, *domain.User, error) {
	var session domain.Session
	var user domain.User
	err := r.db.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.token_hash, COALESCE(s.current_workspace_id, ''), s.expires_at, s.created_at,
		       u.display_name, e.email
		FROM user_sessions s
		JOIN users u ON u.id = s.user_id AND u.status = 'active'
		JOIN user_emails e ON e.user_id = u.id AND e.is_primary = 1
		WHERE s.token_hash = ? AND s.revoked_at IS NULL
	`, tokenHash).Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CurrentWorkspaceID, &session.ExpiresAt, &session.CreatedAt, &user.DisplayName, &user.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, domain.ErrNotFound
	}
	user.ID = session.UserID
	if err == nil {
		_, _ = r.db.ExecContext(ctx, `UPDATE user_sessions SET last_seen_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), session.ID)
	}
	return &session, &user, err
}

func (r *AuthWorkspaceRepository) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE user_sessions SET revoked_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), sessionID)
	return err
}

func (r *AuthWorkspaceRepository) SetCurrentWorkspace(ctx context.Context, sessionID, workspaceID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE user_sessions SET current_workspace_id = ? WHERE id = ?`, workspaceID, sessionID)
	return err
}

func (r *AuthWorkspaceRepository) ListWorkspaces(ctx context.Context, userID string) ([]domain.Workspace, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT w.id, w.name, m.role, w.created_at, w.updated_at
		FROM workspaces w JOIN workspace_members m ON m.workspace_id = w.id
		WHERE m.user_id = ? ORDER BY w.created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Workspace
	for rows.Next() {
		var workspace domain.Workspace
		if err := rows.Scan(&workspace.ID, &workspace.Name, &workspace.Role, &workspace.CreatedAt, &workspace.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, workspace)
	}
	return result, rows.Err()
}

func (r *AuthWorkspaceRepository) GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error) {
	var workspace domain.Workspace
	err := r.db.QueryRowContext(ctx, `
		SELECT w.id, w.name, m.role, w.created_at, w.updated_at
		FROM workspaces w JOIN workspace_members m ON m.workspace_id = w.id
		WHERE m.user_id = ? AND w.id = ?
	`, userID, workspaceID).Scan(&workspace.ID, &workspace.Name, &workspace.Role, &workspace.CreatedAt, &workspace.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &workspace, err
}

func (r *AuthWorkspaceRepository) UpdateWorkspaceName(ctx context.Context, userID, workspaceID, name string) (*domain.Workspace, error) {
	if err := r.requireOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	_, err := r.db.ExecContext(ctx, `UPDATE workspaces SET name = ?, updated_at = ? WHERE id = ?`, name, time.Now().UTC().Format(time.RFC3339), workspaceID)
	if err != nil {
		return nil, err
	}
	return r.GetWorkspace(ctx, userID, workspaceID)
}

func (r *AuthWorkspaceRepository) ListMembers(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceMember, error) {
	if _, err := r.GetWorkspace(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT m.workspace_id, m.user_id, u.display_name, e.email, m.role, m.joined_at
		FROM workspace_members m JOIN users u ON u.id = m.user_id
		JOIN user_emails e ON e.user_id = u.id AND e.is_primary = 1
		WHERE m.workspace_id = ? ORDER BY m.joined_at
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.WorkspaceMember
	for rows.Next() {
		var member domain.WorkspaceMember
		if err := rows.Scan(&member.WorkspaceID, &member.UserID, &member.DisplayName, &member.Email, &member.Role, &member.JoinedAt); err != nil {
			return nil, err
		}
		result = append(result, member)
	}
	return result, rows.Err()
}

func (r *AuthWorkspaceRepository) CreateInvitation(ctx context.Context, userID, workspaceID, email string) (*domain.WorkspaceInvitation, error) {
	if err := r.requireOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	invitation := domain.WorkspaceInvitation{
		ID: domain.NewUUID(), WorkspaceID: workspaceID, Email: strings.TrimSpace(email),
		NormalizedEmail: normalizeEmail(email), Role: "member", Status: "pending", InvitedBy: userID, CreatedAt: now,
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workspace_invitations (id, workspace_id, email, normalized_email, role, status, invited_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, invitation.ID, invitation.WorkspaceID, invitation.Email, invitation.NormalizedEmail, invitation.Role, invitation.Status, invitation.InvitedBy, invitation.CreatedAt)
	return &invitation, uniqueError(err)
}

func (r *AuthWorkspaceRepository) ListInvitations(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceInvitation, error) {
	if err := r.requireOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, workspace_id, email, normalized_email, role, status, invited_by, created_at
		FROM workspace_invitations WHERE workspace_id = ? AND status = 'pending' ORDER BY created_at
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.WorkspaceInvitation
	for rows.Next() {
		var invitation domain.WorkspaceInvitation
		if err := rows.Scan(&invitation.ID, &invitation.WorkspaceID, &invitation.Email, &invitation.NormalizedEmail, &invitation.Role, &invitation.Status, &invitation.InvitedBy, &invitation.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, invitation)
	}
	return result, rows.Err()
}

func (r *AuthWorkspaceRepository) RevokeInvitation(ctx context.Context, userID, workspaceID, invitationID string) error {
	if err := r.requireOwner(ctx, userID, workspaceID); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE workspace_invitations SET status = 'revoked'
		WHERE id = ? AND workspace_id = ? AND status = 'pending'
	`, invitationID, workspaceID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *AuthWorkspaceRepository) RemoveMember(ctx context.Context, userID, workspaceID, memberID string) error {
	if err := r.requireOwner(ctx, userID, workspaceID); err != nil {
		return err
	}
	var role string
	if err := r.db.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, memberID).Scan(&role); errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	} else if err != nil {
		return err
	}
	if role == "owner" {
		return domain.ErrForbidden
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, memberID)
	return err
}

func (r *AuthWorkspaceRepository) CanAccessMeeting(ctx context.Context, userID, meetingID string) error {
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM meetings mt JOIN workspace_members wm ON wm.workspace_id = mt.workspace_id
			WHERE mt.id = ? AND wm.user_id = ?
		)
	`, meetingID, userID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return domain.ErrNotFound
	}
	return nil
}

func (r *AuthWorkspaceRepository) CanAccessJob(ctx context.Context, userID, jobID string) error {
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM jobs j JOIN workspace_members wm ON wm.workspace_id = j.workspace_id
			WHERE j.id = ? AND wm.user_id = ?
		)
	`, jobID, userID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return domain.ErrNotFound
	}
	return nil
}

func (r *AuthWorkspaceRepository) requireOwner(ctx context.Context, userID, workspaceID string) error {
	var role string
	err := r.db.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if role != "owner" {
		return domain.ErrForbidden
	}
	return nil
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func defaultWorkspaceBase(email string) string {
	local, _, _ := strings.Cut(strings.TrimSpace(email), "@")
	if local == "" {
		return "DeciScope"
	}
	return local
}

var _ appauth.Repository = (*AuthWorkspaceRepository)(nil)

func uniqueError(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("%w: already exists", domain.ErrConflict)
	}
	return err
}
