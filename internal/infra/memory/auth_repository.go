package memory

import (
	"context"
	"strings"
	"sync"
	"time"

	"deciscope-core-api/internal/contract"
	"deciscope-core-api/internal/domain"
)

type storedIdentity struct {
	provider string
	subject  string
	userID   string
}

type AuthRepository struct {
	mu         sync.RWMutex
	users      map[string]domain.User
	emails     map[string]string
	identities map[string]storedIdentity
	sessions   map[string]domain.Session
}

func NewAuthRepository() *AuthRepository {
	return &AuthRepository{
		users:      make(map[string]domain.User),
		emails:     make(map[string]string),
		identities: make(map[string]storedIdentity),
		sessions:   make(map[string]domain.Session),
	}
}

func (r *AuthRepository) FindUserByIdentity(_ context.Context, identity domain.IdentityInput) (domain.User, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stored, ok := r.identities[identityKey(identity)]
	if !ok {
		return domain.User{}, false, nil
	}

	user, ok := r.users[stored.userID]
	if !ok {
		return domain.User{}, false, nil
	}

	return user, true, nil
}

func (r *AuthRepository) CreateUserWithIdentity(_ context.Context, identity domain.IdentityInput, seed domain.UserSeed) (domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_ = seed

	key := identityKey(identity)
	if _, exists := r.identities[key]; exists {
		return domain.User{}, domain.ErrIdentityConflict
	}

	now := time.Now().UTC()

	user := domain.User{
		ID:        domain.NewID(),
		Status:    domain.UserStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}

	r.users[user.ID] = user
	if seed.Email != "" {
		r.emails[strings.ToLower(seed.Email)] = user.ID
	}
	r.identities[key] = storedIdentity{
		provider: identity.Provider,
		subject:  identity.Subject,
		userID:   user.ID,
	}

	return user, nil
}

func (r *AuthRepository) FindUserByEmail(_ context.Context, email string) (domain.User, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	userID, ok := r.emails[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return domain.User{}, false, nil
	}

	user, ok := r.users[userID]
	if !ok {
		return domain.User{}, false, nil
	}

	return user, true, nil
}

func (r *AuthRepository) AttachIdentityToUser(_ context.Context, userID string, identity domain.IdentityInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.users[userID]; !ok {
		return nil
	}

	key := identityKey(identity)
	if existing, exists := r.identities[key]; exists {
		if existing.userID == userID {
			return nil
		}
		return domain.ErrIdentityConflict
	}

	r.identities[key] = storedIdentity{
		provider: identity.Provider,
		subject:  identity.Subject,
		userID:   userID,
	}

	return nil
}

func (r *AuthRepository) FindUserByID(_ context.Context, userID string) (domain.User, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	user, ok := r.users[userID]
	return user, ok, nil
}

func (r *AuthRepository) MarkUserDeleted(_ context.Context, userID string, deletedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	user, ok := r.users[userID]
	if !ok {
		return nil
	}

	user.Status = domain.UserStatusDeleted
	user.UpdatedAt = deletedAt
	user.DeletedAt = &deletedAt
	r.users[userID] = user
	return nil
}

func (r *AuthRepository) CreateSession(_ context.Context, seed domain.SessionSeed) (domain.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

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

	r.sessions[session.ID] = session
	return session, nil
}

func (r *AuthRepository) FindSessionByID(_ context.Context, sessionID string) (domain.Session, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	session, ok := r.sessions[sessionID]
	return session, ok, nil
}

func (r *AuthRepository) RevokeSession(_ context.Context, sessionID string, revokedAt time.Time, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	session, ok := r.sessions[sessionID]
	if !ok || session.RevokedAt != nil {
		return nil
	}

	session.RevokedAt = &revokedAt
	session.RevokeReason = reason
	r.sessions[sessionID] = session
	return nil
}

func (r *AuthRepository) RevokeAllSessionsByUserID(_ context.Context, userID string, revokedAt time.Time, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for sessionID, session := range r.sessions {
		if session.UserID != userID || session.RevokedAt != nil {
			continue
		}

		session.RevokedAt = &revokedAt
		session.RevokeReason = reason
		r.sessions[sessionID] = session
	}

	return nil
}

func (r *AuthRepository) ListActiveSessionsByUserID(_ context.Context, userID string) ([]domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]domain.Session, 0)
	for _, session := range r.sessions {
		if session.UserID != userID || session.RevokedAt != nil {
			continue
		}

		result = append(result, session)
	}

	return result, nil
}

func (r *AuthRepository) TouchSession(_ context.Context, sessionID string, seenAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	session, ok := r.sessions[sessionID]
	if !ok {
		return nil
	}

	session.LastSeenAt = seenAt
	r.sessions[sessionID] = session
	return nil
}

func identityKey(identity domain.IdentityInput) string {
	return identity.Provider + "|" + identity.Subject
}

var _ contract.AuthRepository = (*AuthRepository)(nil)
