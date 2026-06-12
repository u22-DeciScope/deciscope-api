package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/domain"
)

type fakeVerifier struct {
	identity *appauth.Identity
	err      error
}

func (v fakeVerifier) VerifyIDToken(context.Context, string) (*appauth.Identity, error) {
	return v.identity, v.err
}

func TestLoginCreatesUserWorkspaceAndSession(t *testing.T) {
	repository := &fakeRepository{}
	service := appauth.NewService(
		repository,
		fakeVerifier{identity: &appauth.Identity{UID: "firebase-uid", Email: "user@example.com", Name: "User", EmailVerified: true}},
		time.Hour,
	)

	result, err := service.Login(context.Background(), "token")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.User.Email != "user@example.com" || len(result.Workspaces) != 1 || result.Token == "" {
		t.Fatalf("Login() result = %+v", result)
	}
	if _, err := service.Authenticate(context.Background(), result.Token); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
}

type fakeRepository struct {
	appauth.Repository
	user      domain.User
	workspace domain.Workspace
	session   domain.Session
}

func (r *fakeRepository) FindOrCreateUser(_ context.Context, identity appauth.Identity) (*domain.User, error) {
	r.user = domain.User{ID: "u_test", DisplayName: identity.Name, Email: identity.Email}
	return &r.user, nil
}
func (*fakeRepository) AcceptInvitations(context.Context, string, string) error { return nil }
func (r *fakeRepository) EnsureInitialWorkspace(context.Context, string, string, string) (*domain.Workspace, error) {
	r.workspace = domain.Workspace{ID: "w_test", Name: "User's Workspace", Role: "owner"}
	return &r.workspace, nil
}
func (r *fakeRepository) ListWorkspaces(context.Context, string) ([]domain.Workspace, error) {
	return []domain.Workspace{r.workspace}, nil
}
func (r *fakeRepository) CreateSession(_ context.Context, session domain.Session) error {
	r.session = session
	return nil
}
func (r *fakeRepository) SessionByTokenHash(context.Context, string) (*domain.Session, *domain.User, error) {
	return &r.session, &r.user, nil
}

func TestLoginWithoutVerifierIsUnavailable(t *testing.T) {
	service := appauth.NewService(nil, nil, time.Hour)
	_, err := service.Login(context.Background(), "token")
	if !errors.Is(err, appauth.ErrUnavailable) {
		t.Fatalf("Login() error = %v, want ErrUnavailable", err)
	}
}
