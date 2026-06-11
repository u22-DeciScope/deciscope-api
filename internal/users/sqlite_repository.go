package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

func (r *SQLiteRepository) FindOrCreateFirebaseUser(ctx context.Context, email, name string) (*User, error) {
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

func (r *SQLiteRepository) findByEmail(ctx context.Context, email string) (*User, error) {
	var user User
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

var _ Repository = (*SQLiteRepository)(nil)
