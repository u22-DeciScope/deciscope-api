// Package email は招待リンク通知の infrastructure 実装を提供する。
//
//   - development: 招待URLをログに出す LogMailer (dev fallback)
//   - production:  通知を失敗させる DisabledMailer (招待が成功扱いにならないようにする)
package email

import (
	"context"
	"errors"
	"log"
	"time"

	appworkspace "deciscope-core-api/internal/application/workspace"
)

var ErrNotConfigured = errors.New("invitation delivery is not configured")

// LogMailer は開発環境向けの fallback。招待URL (生tokenを含む) をログに出すため、
// development 環境以外で使用してはならない。
type LogMailer struct{}

func (LogMailer) SendInvitation(_ context.Context, invitation appworkspace.InvitationEmail) error {
	log.Printf("[dev] invitation email (not sent): to=%q workspace=%q role=%q expires_at=%q", invitation.To, invitation.WorkspaceName, invitation.Role, invitation.ExpiresAt.Format(time.RFC3339))
	log.Printf("[dev] invitation accept url: %s", invitation.AcceptURL)
	return nil
}

// DisabledMailer は production で通知基盤がない場合に使い、招待作成を失敗させる。
type DisabledMailer struct{}

func (DisabledMailer) SendInvitation(context.Context, appworkspace.InvitationEmail) error {
	return ErrNotConfigured
}
