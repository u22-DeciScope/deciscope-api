package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/domain"

	"github.com/jackc/pgx/v5/pgconn"
)

type AuthWorkspaceRepository struct {
	db       *sql.DB
	seedDemo bool
}

func NewAuthWorkspaceRepository(db *sql.DB) *AuthWorkspaceRepository {
	return &AuthWorkspaceRepository{db: db}
}

// WithDemoWorkspace は、ログイン時にユーザーをデモ用ワークスペース（domain.DemoWorkspaceID）へ
// 自動参加させるかどうかを設定する。DECISCOPE_SEED_DEMO_DATA が有効な開発環境でのみ true にする。
func (r *AuthWorkspaceRepository) WithDemoWorkspace(enabled bool) *AuthWorkspaceRepository {
	r.seedDemo = enabled
	return r
}

func (r *AuthWorkspaceRepository) FindOrCreateUser(ctx context.Context, identity appauth.Identity) (*domain.User, error) {
	var user domain.User
	err := r.db.QueryRowContext(ctx, `
		SELECT u.id, u.display_name, e.email
		FROM users u
		JOIN user_identities i ON i.user_id = u.id
		JOIN user_emails e ON e.user_id = u.id AND e.is_primary = TRUE
		WHERE i.provider = 'firebase' AND i.provider_subject = $1
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, display_name, status, created_at, updated_at) VALUES ($1, $2, 'active', $3, $4)`,
		user.ID, user.DisplayName, now, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_identities (id, user_id, provider, provider_subject, created_at) VALUES ($1, $2, 'firebase', $3, $4)`,
		domain.NewUUID(), user.ID, identity.UID, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_emails (id, user_id, email, normalized_email, verified, is_primary, created_at) VALUES ($1, $2, $3, $4, TRUE, TRUE, $5)`,
		domain.NewUUID(), user.ID, user.Email, normalizeEmail(user.Email), now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *AuthWorkspaceRepository) EnsureInitialWorkspace(ctx context.Context, userID, displayName, email string) (*domain.Workspace, error) {
	workspaces, err := r.ListWorkspaces(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(workspaces) > 0 {
		if err := r.ensureDemoMembership(ctx, userID); err != nil {
			return nil, err
		}
		return &workspaces[0], nil
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = defaultWorkspaceBase(email)
	}
	name += "のワークスペース"
	workspace, err := r.insertWorkspace(ctx, userID, name, "")
	if err != nil {
		return nil, err
	}
	if err := r.ensureDemoMembership(ctx, userID); err != nil {
		return nil, err
	}
	return workspace, nil
}

// CreateWorkspace はワークスペースを作成し、作成者を owner として workspace_members に登録する。
func (r *AuthWorkspaceRepository) CreateWorkspace(ctx context.Context, userID, name, description string) (*domain.Workspace, error) {
	return r.insertWorkspace(ctx, userID, name, description)
}

func (r *AuthWorkspaceRepository) insertWorkspace(ctx context.Context, userID, name, description string) (*domain.Workspace, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	workspace := domain.Workspace{ID: domain.NewUUID(), Name: name, Description: description, Role: domain.WorkspaceRoleOwner, CreatedAt: now, UpdatedAt: now}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, name, description, created_by, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		workspace.ID, workspace.Name, workspace.Description, userID, now, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, joined_at) VALUES ($1, $2, 'owner', $3)`,
		workspace.ID, userID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &workspace, nil
}

// ensureDemoMembership は、デモ用ワークスペースが存在する場合にユーザーを admin として参加させる。
// 開発環境（WithDemoWorkspace 有効）専用。デモワークスペースが未シードのときは何もしない。
func (r *AuthWorkspaceRepository) ensureDemoMembership(ctx context.Context, userID string) error {
	if !r.seedDemo {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role, joined_at)
		SELECT $1, $2, 'admin', $3
		WHERE EXISTS (SELECT 1 FROM workspaces WHERE id = $1)
		ON CONFLICT (workspace_id, user_id) DO NOTHING
	`, domain.DemoWorkspaceID, userID, now)
	if err != nil {
		return fmt.Errorf("ensure demo workspace membership: %w", err)
	}
	return nil
}

func (r *AuthWorkspaceRepository) CreateSession(ctx context.Context, session domain.Session) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_sessions (id, user_id, token_hash, current_workspace_id, created_at, expires_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
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
		JOIN user_emails e ON e.user_id = u.id AND e.is_primary = TRUE
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL
	`, tokenHash).Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CurrentWorkspaceID, &session.ExpiresAt, &session.CreatedAt, &user.DisplayName, &user.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, domain.ErrNotFound
	}
	user.ID = session.UserID
	if err == nil {
		_, _ = r.db.ExecContext(ctx, `UPDATE user_sessions SET last_seen_at = $1 WHERE id = $2`, time.Now().UTC().Format(time.RFC3339), session.ID)
	}
	return &session, &user, err
}

func (r *AuthWorkspaceRepository) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE user_sessions SET revoked_at = $1 WHERE id = $2`, time.Now().UTC().Format(time.RFC3339), sessionID)
	return err
}

func (r *AuthWorkspaceRepository) SetCurrentWorkspace(ctx context.Context, sessionID, workspaceID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE user_sessions SET current_workspace_id = $1 WHERE id = $2`, workspaceID, sessionID)
	return err
}

func (r *AuthWorkspaceRepository) ListWorkspaces(ctx context.Context, userID string) ([]domain.Workspace, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT w.id, w.name, w.description, m.role, w.created_at, w.updated_at
		FROM workspaces w JOIN workspace_members m ON m.workspace_id = w.id
		WHERE m.user_id = $1 ORDER BY w.created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Workspace
	for rows.Next() {
		var workspace domain.Workspace
		if err := rows.Scan(&workspace.ID, &workspace.Name, &workspace.Description, &workspace.Role, &workspace.CreatedAt, &workspace.UpdatedAt); err != nil {
			return nil, err
		}
		workspace.Role = domain.NormalizeWorkspaceRole(workspace.Role)
		result = append(result, workspace)
	}
	return result, rows.Err()
}

func (r *AuthWorkspaceRepository) GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error) {
	var workspace domain.Workspace
	err := r.db.QueryRowContext(ctx, `
		SELECT w.id, w.name, w.description, m.role, w.created_at, w.updated_at
		FROM workspaces w JOIN workspace_members m ON m.workspace_id = w.id
		WHERE m.user_id = $1 AND w.id = $2
	`, userID, workspaceID).Scan(&workspace.ID, &workspace.Name, &workspace.Description, &workspace.Role, &workspace.CreatedAt, &workspace.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	workspace.Role = domain.NormalizeWorkspaceRole(workspace.Role)
	return &workspace, err
}

func (r *AuthWorkspaceRepository) UpdateWorkspace(ctx context.Context, userID, workspaceID string, name, description *string) (*domain.Workspace, error) {
	if err := r.requireWorkspaceManager(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE workspaces
		SET name = COALESCE($1, name), description = COALESCE($2, description), updated_at = $3
		WHERE id = $4
	`, name, description, time.Now().UTC().Format(time.RFC3339), workspaceID)
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
		JOIN user_emails e ON e.user_id = u.id AND e.is_primary = TRUE
		WHERE m.workspace_id = $1 ORDER BY m.joined_at
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
		member.Role = domain.NormalizeWorkspaceRole(member.Role)
		result = append(result, member)
	}
	return result, rows.Err()
}

func (r *AuthWorkspaceRepository) CreateInvitation(ctx context.Context, userID, workspaceID, email, role, tokenHash, expiresAt string) (*domain.WorkspaceInvitation, error) {
	if err := r.requireWorkspaceManager(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	role = domain.NormalizeWorkspaceRole(role)
	if !domain.ValidWorkspaceInvitationRole(role) {
		return nil, domain.ErrInvalidArgument
	}
	now := time.Now().UTC().Format(time.RFC3339)
	invitation := domain.WorkspaceInvitation{
		ID: domain.NewUUID(), WorkspaceID: workspaceID, Email: strings.TrimSpace(email),
		NormalizedEmail: normalizeEmail(email), Role: role, Status: domain.WorkspaceInvitationStatusPending,
		InvitedBy: userID, ExpiresAt: expiresAt, CreatedAt: now,
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workspace_invitations (id, workspace_id, email, normalized_email, role, status, invited_by, token_hash, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, invitation.ID, invitation.WorkspaceID, invitation.Email, invitation.NormalizedEmail, invitation.Role, invitation.Status, invitation.InvitedBy, tokenHash, expiresAt, invitation.CreatedAt)
	return &invitation, uniqueError(err)
}

func (r *AuthWorkspaceRepository) DeleteInvitation(ctx context.Context, invitationID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM workspace_invitations WHERE id = $1`, invitationID)
	return err
}

func (r *AuthWorkspaceRepository) InvitationByTokenHash(ctx context.Context, tokenHash string) (*domain.WorkspaceInvitation, error) {
	if strings.TrimSpace(tokenHash) == "" {
		return nil, domain.ErrNotFound
	}
	var invitation domain.WorkspaceInvitation
	err := r.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, email, normalized_email, role, status, invited_by, COALESCE(expires_at, ''), created_at
		FROM workspace_invitations WHERE token_hash = $1
	`, tokenHash).Scan(&invitation.ID, &invitation.WorkspaceID, &invitation.Email, &invitation.NormalizedEmail, &invitation.Role, &invitation.Status, &invitation.InvitedBy, &invitation.ExpiresAt, &invitation.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	invitation.Role = domain.NormalizeWorkspaceRole(invitation.Role)
	return &invitation, nil
}

// AcceptInvitation はメンバー追加と招待の accepted 更新を1トランザクションで行う。
func (r *AuthWorkspaceRepository) AcceptInvitation(ctx context.Context, invitationID, userID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var workspaceID, role string
	err = tx.QueryRowContext(ctx, `
		SELECT workspace_id, role FROM workspace_invitations WHERE id = $1 AND status = 'pending'
	`, invitationID).Scan(&workspaceID, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	role = domain.NormalizeWorkspaceRole(role)
	if role == "" {
		role = domain.WorkspaceRoleViewer
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role, joined_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (workspace_id, user_id) DO NOTHING
	`, workspaceID, userID, role, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_invitations SET status = 'accepted', accepted_by = $1, accepted_at = $2 WHERE id = $3
	`, userID, now, invitationID); err != nil {
		return err
	}
	return tx.Commit()
}

// WorkspaceNameByID は招待preview用にワークスペース名のみを返す (membership不要)。
func (r *AuthWorkspaceRepository) WorkspaceNameByID(ctx context.Context, workspaceID string) (string, error) {
	var name string
	err := r.db.QueryRowContext(ctx, `SELECT name FROM workspaces WHERE id = $1`, workspaceID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return name, err
}

func (r *AuthWorkspaceRepository) MemberEmailExists(ctx context.Context, workspaceID, normalizedEmail string) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM workspace_members m
			JOIN user_emails e ON e.user_id = m.user_id AND e.is_primary = TRUE
			WHERE m.workspace_id = $1 AND e.normalized_email = $2
		)
	`, workspaceID, normalizedEmail).Scan(&exists)
	return exists, err
}

func (r *AuthWorkspaceRepository) ListInvitations(ctx context.Context, userID, workspaceID string) ([]domain.WorkspaceInvitation, error) {
	if err := r.requireWorkspaceManager(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT i.id, i.workspace_id, i.email, i.normalized_email, i.role, i.status, i.invited_by,
		       COALESCE(u.display_name, ''), COALESCE(i.expires_at, ''), i.created_at
		FROM workspace_invitations i
		LEFT JOIN users u ON u.id = i.invited_by
		WHERE i.workspace_id = $1 AND i.status = 'pending' ORDER BY i.created_at
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.WorkspaceInvitation
	for rows.Next() {
		var invitation domain.WorkspaceInvitation
		if err := rows.Scan(&invitation.ID, &invitation.WorkspaceID, &invitation.Email, &invitation.NormalizedEmail, &invitation.Role, &invitation.Status, &invitation.InvitedBy, &invitation.InvitedByName, &invitation.ExpiresAt, &invitation.CreatedAt); err != nil {
			return nil, err
		}
		invitation.Role = domain.NormalizeWorkspaceRole(invitation.Role)
		result = append(result, invitation)
	}
	return result, rows.Err()
}

func (r *AuthWorkspaceRepository) RevokeInvitation(ctx context.Context, userID, workspaceID, invitationID string) error {
	if err := r.requireWorkspaceManager(ctx, userID, workspaceID); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE workspace_invitations SET status = 'revoked', revoked_at = $3, revoked_by = $4
		WHERE id = $1 AND workspace_id = $2 AND status = 'pending'
	`, invitationID, workspaceID, time.Now().UTC().Format(time.RFC3339), userID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *AuthWorkspaceRepository) RemoveMember(ctx context.Context, userID, workspaceID, memberID string) error {
	if err := r.requireWorkspaceManager(ctx, userID, workspaceID); err != nil {
		return err
	}
	var role string
	if err := r.db.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, workspaceID, memberID).Scan(&role); errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	} else if err != nil {
		return err
	}
	if domain.IsWorkspaceOwner(role) {
		return domain.ErrForbidden
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, workspaceID, memberID)
	return err
}

func (r *AuthWorkspaceRepository) UpdateMemberRole(ctx context.Context, userID, workspaceID, memberID, role string) (*domain.WorkspaceMember, error) {
	// ロール変更は owner のみが実行できる。
	if err := r.requireWorkspaceOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	role = domain.NormalizeWorkspaceRole(role)
	if !domain.ValidWorkspaceInvitationRole(role) {
		return nil, domain.ErrInvalidArgument
	}
	var existingRole string
	if err := r.db.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, workspaceID, memberID).Scan(&existingRole); errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if domain.IsWorkspaceOwner(existingRole) {
		return nil, domain.ErrForbidden
	}
	_, err := r.db.ExecContext(ctx, `UPDATE workspace_members SET role = $1 WHERE workspace_id = $2 AND user_id = $3`, role, workspaceID, memberID)
	if err != nil {
		return nil, err
	}
	members, err := r.ListMembers(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, member := range members {
		if member.UserID == memberID {
			return &member, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *AuthWorkspaceRepository) CanAccessMeeting(ctx context.Context, userID, meetingID string) error {
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM meetings mt JOIN workspace_members wm ON wm.workspace_id = mt.workspace_id
			WHERE mt.id = $1 AND wm.user_id = $2
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
			WHERE j.id = $1 AND wm.user_id = $2
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

func (r *AuthWorkspaceRepository) requireWorkspaceManager(ctx context.Context, userID, workspaceID string) error {
	return r.requireWorkspaceRole(ctx, userID, workspaceID, domain.CanManageWorkspace)
}

func (r *AuthWorkspaceRepository) requireWorkspaceOwner(ctx context.Context, userID, workspaceID string) error {
	return r.requireWorkspaceRole(ctx, userID, workspaceID, domain.IsWorkspaceOwner)
}

func (r *AuthWorkspaceRepository) requireWorkspaceRole(ctx context.Context, userID, workspaceID string, allowed func(string) bool) error {
	var role string
	err := r.db.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !allowed(role) {
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
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: already exists", domain.ErrConflict)
	}
	return err
}
