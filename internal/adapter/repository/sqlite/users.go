package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/domain"
)

type UserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) FindOrCreateFirebaseUser(ctx context.Context, email, name string) (*domain.User, error) {
	user, err := r.findByEmail(ctx, email)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO t_Users (email, name, password)
		VALUES (?, ?, ?)
	`, email, name, "firebase_auth")
	if err != nil && !isUniqueConstraint(err) {
		return nil, fmt.Errorf("insert firebase user: %w", err)
	}

	user, err = r.findByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("load firebase user: %w", err)
	}
	return user, nil
}

func (r *UserRepository) findByEmail(ctx context.Context, email string) (*domain.User, error) {
	var user domain.User
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, email
		FROM t_Users
		WHERE email = ?
	`, email).Scan(&user.ID, &user.Name, &user.Email)
	return &user, err
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

var _ appauth.UserRepository = (*UserRepository)(nil)
