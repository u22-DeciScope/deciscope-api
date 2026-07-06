package workspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

// 招待リンクの有効期限 (MVP要件: 72時間)。
const invitationTTL = 72 * time.Hour

var (
	ErrInvitationExpired         = errors.New("invitation expired")
	ErrInvitationRevoked         = errors.New("invitation revoked")
	ErrInvitationAlreadyAccepted = errors.New("invitation already accepted")
	ErrInvitationEmailMismatch   = errors.New("invitation email mismatch")
	ErrInvitationEmailDelivery   = errors.New("invitation email delivery failed")
)

// InvitationEmail は招待メールの内容。機密情報 (会議URL・session_id・token_hash など) を
// 含めてはならない。生tokenはAcceptURLの一部としてのみ扱う。
type InvitationEmail struct {
	To            string
	WorkspaceName string
	InviterName   string
	Role          string
	AcceptURL     string
	ExpiresAt     time.Time
}

// InvitationMailer は招待メール送信の outbound port。
type InvitationMailer interface {
	SendInvitation(ctx context.Context, email InvitationEmail) error
}

// InvitationPreview は承認前に表示してよい情報のみを持つ。
type InvitationPreview struct {
	WorkspaceName string `json:"workspace_name"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	ExpiresAt     string `json:"expires_at"`
}

// CreateInvitation は pending 招待を作成し、招待メールを送信する。
// メール送信に失敗した場合は作成した招待を削除して状態不整合を避ける。
func (s *Service) CreateInvitation(ctx context.Context, userID, userDisplayName, workspaceID, email, role string) (*domain.WorkspaceInvitation, error) {
	email = strings.TrimSpace(email)
	normalizedEmail := normalizeEmail(email)
	if normalizedEmail == "" {
		return nil, fmt.Errorf("%w: email is required", domain.ErrInvalidArgument)
	}
	if _, err := mail.ParseAddress(normalizedEmail); err != nil {
		return nil, fmt.Errorf("%w: invalid email address", domain.ErrInvalidArgument)
	}
	// owner は招待で付与できない。viewer / admin のみ。
	if domain.NormalizeWorkspaceRole(role) == domain.WorkspaceRoleOwner {
		return nil, fmt.Errorf("%w: owner role cannot be granted via invitation", domain.ErrInvalidArgument)
	}
	role = domain.NormalizeWorkspaceRole(role)
	if role == "" {
		role = domain.WorkspaceRoleViewer
	}
	if !domain.ValidWorkspaceInvitationRole(role) {
		return nil, fmt.Errorf("%w: invitation role must be admin or viewer", domain.ErrInvalidArgument)
	}

	workspace, err := s.repository.GetWorkspace(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	alreadyMember, err := s.repository.MemberEmailExists(ctx, workspaceID, normalizedEmail)
	if err != nil {
		return nil, err
	}
	if alreadyMember {
		return nil, fmt.Errorf("%w: user is already a member", domain.ErrConflict)
	}
	invitations, err := s.repository.ListInvitations(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, invitation := range invitations {
		if invitation.NormalizedEmail != normalizedEmail {
			continue
		}
		if effectiveInvitationStatus(invitation, now) == domain.WorkspaceInvitationStatusPending {
			return nil, fmt.Errorf("%w: invitation already exists", domain.ErrConflict)
		}
		// 期限切れの pending 招待は削除して再招待できるようにする。
		if err := s.repository.DeleteInvitation(ctx, invitation.ID); err != nil {
			return nil, err
		}
	}

	rawToken, tokenHash, err := newInvitationToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().UTC().Add(invitationTTL)
	invitation, err := s.repository.CreateInvitation(ctx, userID, workspaceID, email, role, tokenHash, expiresAt.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	if err := s.mailer.SendInvitation(ctx, InvitationEmail{
		To:            invitation.Email,
		WorkspaceName: workspace.Name,
		InviterName:   strings.TrimSpace(userDisplayName),
		Role:          invitation.Role,
		AcceptURL:     s.invitationAcceptURL(rawToken),
		ExpiresAt:     expiresAt,
	}); err != nil {
		if deleteErr := s.repository.DeleteInvitation(ctx, invitation.ID); deleteErr != nil {
			log.Printf("workspace invitation rollback failed: workspace_id=%q invitation_id=%q error=%v", workspaceID, invitation.ID, deleteErr)
		}
		return nil, fmt.Errorf("%w: %v", ErrInvitationEmailDelivery, err)
	}
	log.Printf("workspace invitation created: workspace_id=%q invitation_id=%q role=%q invited_by_user_id=%q expires_at=%q", workspaceID, invitation.ID, invitation.Role, userID, invitation.ExpiresAt)
	return invitation, nil
}

// PreviewInvitation は招待リンクを開いた際に承認前確認用の情報を返す。認証不要。
func (s *Service) PreviewInvitation(ctx context.Context, rawToken string) (*InvitationPreview, error) {
	invitation, err := s.invitationByRawToken(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	workspaceName, err := s.repository.WorkspaceNameByID(ctx, invitation.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return &InvitationPreview{
		WorkspaceName: workspaceName,
		Email:         invitation.Email,
		Role:          invitation.Role,
		Status:        effectiveInvitationStatus(*invitation, time.Now().UTC()),
		ExpiresAt:     invitation.ExpiresAt,
	}, nil
}

// AcceptInvitation は token を検証し、ログイン中ユーザーのメールアドレスが招待先と
// 一致する場合のみ workspace_members に追加する。
func (s *Service) AcceptInvitation(ctx context.Context, userID, userEmail, rawToken string) (*domain.Workspace, error) {
	invitation, err := s.invitationByRawToken(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	switch effectiveInvitationStatus(*invitation, time.Now().UTC()) {
	case domain.WorkspaceInvitationStatusPending:
	case domain.WorkspaceInvitationStatusAccepted:
		return nil, ErrInvitationAlreadyAccepted
	case domain.WorkspaceInvitationStatusRevoked:
		return nil, ErrInvitationRevoked
	default:
		return nil, ErrInvitationExpired
	}
	if normalizeEmail(userEmail) == "" || normalizeEmail(userEmail) != invitation.NormalizedEmail {
		log.Printf("workspace invitation accept rejected (email mismatch): workspace_id=%q invitation_id=%q accepted_by_user_id=%q", invitation.WorkspaceID, invitation.ID, userID)
		return nil, ErrInvitationEmailMismatch
	}
	if err := s.repository.AcceptInvitation(ctx, invitation.ID, userID); err != nil {
		return nil, err
	}
	// 参加ログ (owner/admin が後から確認できるよう backend log に残す)。
	log.Printf(
		"workspace invitation accepted: workspace_id=%q invitation_id=%q accepted_by_user_id=%q accepted_email=%q role=%q invited_by_user_id=%q accepted_at=%q",
		invitation.WorkspaceID, invitation.ID, userID, invitation.NormalizedEmail, invitation.Role, invitation.InvitedBy, time.Now().UTC().Format(time.RFC3339),
	)
	return s.repository.GetWorkspace(ctx, userID, invitation.WorkspaceID)
}

func (s *Service) invitationByRawToken(ctx context.Context, rawToken string) (*domain.WorkspaceInvitation, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil, domain.ErrNotFound
	}
	return s.repository.InvitationByTokenHash(ctx, hashInvitationToken(rawToken))
}

func (s *Service) invitationAcceptURL(rawToken string) string {
	base := strings.TrimRight(s.frontendBaseURL, "/")
	if base == "" {
		base = "http://localhost:5193"
	}
	return base + "/invitations/accept?token=" + url.QueryEscape(rawToken)
}

// effectiveInvitationStatus は pending でも期限切れなら expired として扱う。
// DB上の status 更新は行わない (MVP方針)。
func effectiveInvitationStatus(invitation domain.WorkspaceInvitation, now time.Time) string {
	if invitation.Status != domain.WorkspaceInvitationStatusPending {
		return invitation.Status
	}
	if invitation.ExpiresAt == "" {
		// token 導入前の旧 pending 招待は受理できないため expired 扱いにする。
		return domain.WorkspaceInvitationStatusExpired
	}
	expiresAt, err := time.Parse(time.RFC3339, invitation.ExpiresAt)
	if err != nil || !expiresAt.After(now) {
		return domain.WorkspaceInvitationStatusExpired
	}
	return domain.WorkspaceInvitationStatusPending
}

func newInvitationToken() (string, string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", "", err
	}
	raw := hex.EncodeToString(token[:])
	return raw, hashInvitationToken(raw), nil
}

func hashInvitationToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
