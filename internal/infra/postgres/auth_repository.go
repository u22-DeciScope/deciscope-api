package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"deciscope-core-api/internal/contract"
	"deciscope-core-api/internal/domain"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type AuthRepository struct {
	db *sql.DB
}

func NewAuthRepository(databaseURL string) (*AuthRepository, error) {
	dsn := strings.TrimSpace(databaseURL)
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &AuthRepository{db: db}, nil
}

func (r *AuthRepository) FindUserByIdentity(ctx context.Context, identity domain.IdentityInput) (domain.User, bool, error) {
	const query = `
		SELECT u.id, u.status, u.created_at, u.updated_at, u.deleted_at
		FROM user_identities ui
		INNER JOIN users u ON u.id = ui.user_id
		WHERE ui.provider = $1 AND ui.provider_subject = $2
	`

	row := r.db.QueryRowContext(ctx, query, identity.Provider, identity.Subject)
	user, found, err := scanUser(row)
	if err != nil {
		return domain.User{}, false, err
	}

	return user, found, nil
}

func (r *AuthRepository) FindUserByEmail(ctx context.Context, email string) (domain.User, bool, error) {
	const query = `
		SELECT u.id, u.status, u.created_at, u.updated_at, u.deleted_at
		FROM user_emails ue
		INNER JOIN users u ON u.id = ue.user_id
		WHERE LOWER(ue.email) = LOWER($1)
		ORDER BY ue.is_primary DESC, ue.created_at ASC
		LIMIT 1
	`

	row := r.db.QueryRowContext(ctx, query, strings.TrimSpace(email))
	user, found, err := scanUser(row)
	if err != nil {
		return domain.User{}, false, err
	}

	return user, found, nil
}

func (r *AuthRepository) CreateUserWithIdentity(ctx context.Context, identity domain.IdentityInput, seed domain.UserSeed) (domain.User, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.User{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	now := time.Now().UTC()

	user := domain.User{
		ID:        domain.NewID(),
		Status:    domain.UserStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}

	const insertUser = `
		INSERT INTO users (id, status, created_at, updated_at, deleted_at)
		VALUES ($1, $2, $3, $4, NULL)
	`
	if _, err := tx.ExecContext(ctx, insertUser, user.ID, user.Status, user.CreatedAt, user.UpdatedAt); err != nil {
		return domain.User{}, err
	}

	const insertIdentity = `
		INSERT INTO user_identities (id, provider, provider_subject, user_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	if _, err := tx.ExecContext(ctx, insertIdentity, domain.NewID(), identity.Provider, identity.Subject, user.ID, now); err != nil {
		if isUniqueConstraint(err) {
			return domain.User{}, domain.ErrIdentityConflict
		}
		return domain.User{}, err
	}

	if err := insertPrimaryEmail(ctx, tx, user.ID, seed.Email, now); err != nil {
		return domain.User{}, err
	}

	if err := insertProfile(ctx, tx, user.ID, seed.DisplayName, seed.AvatarURL, now); err != nil {
		return domain.User{}, err
	}

	if err := tx.Commit(); err != nil {
		return domain.User{}, err
	}

	return user, nil
}

func (r *AuthRepository) FindUserByID(ctx context.Context, userID string) (domain.User, bool, error) {
	const query = `
		SELECT id, status, created_at, updated_at, deleted_at
		FROM users
		WHERE id = $1
	`

	row := r.db.QueryRowContext(ctx, query, userID)
	user, found, err := scanUser(row)
	if err != nil {
		return domain.User{}, false, err
	}

	return user, found, nil
}

func (r *AuthRepository) AttachIdentityToUser(ctx context.Context, userID string, identity domain.IdentityInput) error {
	const query = `
		INSERT INTO user_identities (id, provider, provider_subject, user_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := r.db.ExecContext(ctx, query, domain.NewID(), identity.Provider, identity.Subject, userID, time.Now().UTC())
	if err != nil {
		if isUniqueConstraint(err) {
			return domain.ErrIdentityConflict
		}
		return err
	}

	return nil
}

func (r *AuthRepository) CreateSession(ctx context.Context, seed domain.SessionSeed) (domain.Session, error) {
	session := domain.Session{
		ID:          domain.NewID(),
		UserID:      seed.UserID,
		DeviceType:  seed.DeviceType,
		DeviceName:  seed.DeviceName,
		LoginMethod: seed.LoginMethod,
		UserAgent:   seed.UserAgent,
		CreatedAt:   seed.CreatedAt,
		LastSeenAt:  seed.CreatedAt,
	}

	const query = `
		INSERT INTO user_sessions (id, user_id, device_type, device_name, user_agent, login_method, created_at, last_seen_at, revoked_at, revoke_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, '')
	`
	_, err := r.db.ExecContext(
		ctx,
		query,
		session.ID,
		session.UserID,
		session.DeviceType,
		session.DeviceName,
		session.UserAgent,
		session.LoginMethod,
		session.CreatedAt,
		session.LastSeenAt,
	)
	if err != nil {
		return domain.Session{}, err
	}

	return session, nil
}

func (r *AuthRepository) FindSessionByID(ctx context.Context, sessionID string) (domain.Session, bool, error) {
	const query = `
		SELECT id, user_id, device_type, device_name, user_agent, login_method, created_at, last_seen_at, revoked_at, revoke_reason
		FROM user_sessions
		WHERE id = $1
	`

	row := r.db.QueryRowContext(ctx, query, sessionID)
	session, found, err := scanSession(row)
	if err != nil {
		return domain.Session{}, false, err
	}

	return session, found, nil
}

func (r *AuthRepository) RevokeSession(ctx context.Context, sessionID string, revokedAt time.Time, reason string) error {
	const query = `
		UPDATE user_sessions
		SET revoked_at = $1, revoke_reason = $2
		WHERE id = $3 AND revoked_at IS NULL
	`
	_, err := r.db.ExecContext(ctx, query, revokedAt, reason, sessionID)
	return err
}

func (r *AuthRepository) RevokeAllSessionsByUserID(ctx context.Context, userID string, revokedAt time.Time, reason string) error {
	const query = `
		UPDATE user_sessions
		SET revoked_at = $1, revoke_reason = $2
		WHERE user_id = $3 AND revoked_at IS NULL
	`
	_, err := r.db.ExecContext(ctx, query, revokedAt, reason, userID)
	return err
}

func (r *AuthRepository) ListActiveSessionsByUserID(ctx context.Context, userID string) ([]domain.Session, error) {
	const query = `
		SELECT id, user_id, device_type, device_name, user_agent, login_method, created_at, last_seen_at, revoked_at, revoke_reason
		FROM user_sessions
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]domain.Session, 0)
	for rows.Next() {
		session, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}

	return result, rows.Err()
}

func (r *AuthRepository) TouchSession(ctx context.Context, sessionID string, seenAt time.Time) error {
	const query = `
		UPDATE user_sessions
		SET last_seen_at = $1
		WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, seenAt, sessionID)
	return err
}

func scanUser(row interface{ Scan(dest ...any) error }) (domain.User, bool, error) {
	var user domain.User
	var deletedAt sql.NullTime

	err := row.Scan(&user.ID, &user.Status, &user.CreatedAt, &user.UpdatedAt, &deletedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.User{}, false, nil
		}
		return domain.User{}, false, err
	}

	if deletedAt.Valid {
		value := deletedAt.Time
		user.DeletedAt = &value
	}

	return user, true, nil
}

func scanSession(row interface{ Scan(dest ...any) error }) (domain.Session, bool, error) {
	session, err := scanSessionRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, false, nil
		}
		return domain.Session{}, false, err
	}

	return session, true, nil
}

func scanSessionRow(row interface{ Scan(dest ...any) error }) (domain.Session, error) {
	var session domain.Session
	var revokedAt sql.NullTime

	err := row.Scan(
		&session.ID,
		&session.UserID,
		&session.DeviceType,
		&session.DeviceName,
		&session.UserAgent,
		&session.LoginMethod,
		&session.CreatedAt,
		&session.LastSeenAt,
		&revokedAt,
		&session.RevokeReason,
	)
	if err != nil {
		return domain.Session{}, err
	}

	if revokedAt.Valid {
		value := revokedAt.Time
		session.RevokedAt = &value
	}

	return session, nil
}

func insertPrimaryEmail(ctx context.Context, tx *sql.Tx, userID string, email string, createdAt time.Time) error {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" {
		return nil
	}

	const query = `
		INSERT INTO user_emails (id, user_id, email, is_primary, is_verified, created_at)
		VALUES ($1, $2, $3, TRUE, FALSE, $4)
	`
	_, err := tx.ExecContext(ctx, query, domain.NewID(), userID, trimmed, createdAt)
	return err
}

func insertProfile(ctx context.Context, tx *sql.Tx, userID string, displayName string, avatarURL string, now time.Time) error {
	trimmedDisplayName := strings.TrimSpace(displayName)
	trimmedAvatarURL := strings.TrimSpace(avatarURL)
	if trimmedDisplayName == "" && trimmedAvatarURL == "" {
		return nil
	}

	const query = `
		INSERT INTO user_profiles (id, user_id, display_name, avatar_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := tx.ExecContext(
		ctx,
		query,
		domain.NewID(),
		userID,
		nullableString(trimmedDisplayName),
		nullableString(trimmedAvatarURL),
		now,
		now,
	)
	return err
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") || strings.Contains(message, "unique constraint")
}

var _ contract.AuthRepository = (*AuthRepository)(nil)
