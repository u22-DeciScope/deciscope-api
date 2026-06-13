package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

var (
	ErrUnavailable   = errors.New("authentication is unavailable")
	ErrInvalidToken  = errors.New("invalid token")
	ErrEmailRequired = errors.New("email is required")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrForbidden     = errors.New("forbidden")
)

type Identity struct {
	UID           string
	Email         string
	Name          string
	EmailVerified bool
	Provider      string
}

type TokenVerifier interface {
	VerifyIDToken(ctx context.Context, idToken string) (*Identity, error)
}

type Repository interface {
	FindOrCreateUser(ctx context.Context, identity Identity) (*domain.User, error)
	AcceptInvitations(ctx context.Context, userID, normalizedEmail string) error
	EnsureInitialWorkspace(ctx context.Context, userID, displayName, email string) (*domain.Workspace, error)
	CreateSession(ctx context.Context, session domain.Session) error
	SessionByTokenHash(ctx context.Context, tokenHash string) (*domain.Session, *domain.User, error)
	RevokeSession(ctx context.Context, sessionID string) error
	SetCurrentWorkspace(ctx context.Context, sessionID, workspaceID string) error
	ListWorkspaces(ctx context.Context, userID string) ([]domain.Workspace, error)
	GetWorkspace(ctx context.Context, userID, workspaceID string) (*domain.Workspace, error)
}

type LoginResult struct {
	User       *domain.User
	Workspaces []domain.Workspace
	Session    *domain.Session
	Token      string
}

type SessionResult struct {
	User       *domain.User
	Workspaces []domain.Workspace
	Session    *domain.Session
}

type Service struct {
	repository Repository
	verifier   TokenVerifier
	sessionTTL time.Duration
}

func NewService(repository Repository, verifier TokenVerifier, sessionTTL time.Duration) *Service {
	if sessionTTL <= 0 {
		sessionTTL = 7 * 24 * time.Hour
	}
	return &Service{repository: repository, verifier: verifier, sessionTTL: sessionTTL}
}

func (s *Service) Login(ctx context.Context, idToken string) (*LoginResult, error) {
	if s.verifier == nil || s.repository == nil {
		return nil, ErrUnavailable
	}
	identity, err := s.verifier.VerifyIDToken(ctx, idToken)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	identity.Email = strings.TrimSpace(identity.Email)
	log.Printf(
		"firebase login identity: uid_present=%t has_email=%t email_verified=%t provider=%q",
		strings.TrimSpace(identity.UID) != "",
		identity.Email != "",
		identity.EmailVerified,
		identity.Provider,
	)
	if identity.Email == "" {
		return nil, ErrEmailRequired
	}
	// Firebase Microsoft identities can omit email_verified after a successful OAuth sign-in.
	if !identity.EmailVerified && identity.Provider != "microsoft.com" {
		return nil, ErrInvalidToken
	}
	user, err := s.repository.FindOrCreateUser(ctx, *identity)
	if err != nil {
		return nil, fmt.Errorf("find or create user: %w", err)
	}
	if err := s.repository.AcceptInvitations(ctx, user.ID, normalizeEmail(identity.Email)); err != nil {
		return nil, fmt.Errorf("accept invitations: %w", err)
	}
	if _, err := s.repository.EnsureInitialWorkspace(ctx, user.ID, user.DisplayName, identity.Email); err != nil {
		return nil, fmt.Errorf("ensure initial workspace: %w", err)
	}
	workspaces, err := s.repository.ListWorkspaces(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	rawToken, tokenHash, err := newSessionToken()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	session := domain.Session{
		ID: domain.NewUUID(), UserID: user.ID, TokenHash: tokenHash,
		CreatedAt: now.Format(time.RFC3339), ExpiresAt: now.Add(s.sessionTTL).Format(time.RFC3339),
	}
	if len(workspaces) > 0 {
		session.CurrentWorkspaceID = workspaces[0].ID
	}
	if err := s.repository.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	log.Printf("backend login synchronized: user_id=%q workspace_count=%d", user.ID, len(workspaces))
	return &LoginResult{User: user, Workspaces: workspaces, Session: &session, Token: rawToken}, nil
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (*SessionResult, error) {
	if s.repository == nil || rawToken == "" {
		return nil, ErrUnauthorized
	}
	session, user, err := s.repository.SessionByTokenHash(ctx, hashToken(rawToken))
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	expiresAt, err := time.Parse(time.RFC3339, session.ExpiresAt)
	if err != nil || !expiresAt.After(time.Now().UTC()) {
		return nil, ErrUnauthorized
	}
	workspaces, err := s.repository.ListWorkspaces(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if !hasWorkspace(workspaces, session.CurrentWorkspaceID) {
		session.CurrentWorkspaceID = ""
		if len(workspaces) > 0 {
			session.CurrentWorkspaceID = workspaces[0].ID
		}
		if err := s.repository.SetCurrentWorkspace(ctx, session.ID, session.CurrentWorkspaceID); err != nil {
			return nil, err
		}
	}
	return &SessionResult{User: user, Workspaces: workspaces, Session: session}, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.repository.RevokeSession(ctx, sessionID)
}

func (s *Service) SetCurrentWorkspace(ctx context.Context, sessionID, userID, workspaceID string) error {
	if _, err := s.repository.GetWorkspace(ctx, userID, workspaceID); err != nil {
		return err
	}
	return s.repository.SetCurrentWorkspace(ctx, sessionID, workspaceID)
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func newSessionToken() (string, string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", "", err
	}
	raw := hex.EncodeToString(token[:])
	return raw, hashToken(raw), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func hasWorkspace(workspaces []domain.Workspace, workspaceID string) bool {
	for _, workspace := range workspaces {
		if workspace.ID == workspaceID {
			return true
		}
	}
	return false
}
