// Package email は招待メール送信の infrastructure 実装を提供する。
//
// 送信基盤が未設定の場合の挙動は環境で分ける:
//   - development: 招待URLをログに出す LogMailer (dev fallback)
//   - production:  送信を失敗させる DisabledMailer (招待が成功扱いにならないようにする)
package email

import (
	"context"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/smtp"
	"strings"
	"time"

	appworkspace "deciscope-core-api/internal/application/workspace"
)

var ErrNotConfigured = errors.New("invitation email is not configured")

type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

func (c SMTPConfig) Configured() bool {
	return strings.TrimSpace(c.Host) != "" && strings.TrimSpace(c.From) != ""
}

// SMTPMailer は net/smtp で招待メールを送信する。plain text のみ。
type SMTPMailer struct {
	config SMTPConfig
}

func NewSMTPMailer(config SMTPConfig) *SMTPMailer {
	return &SMTPMailer{config: config}
}

func (m *SMTPMailer) SendInvitation(_ context.Context, invitation appworkspace.InvitationEmail) error {
	port := strings.TrimSpace(m.config.Port)
	if port == "" {
		port = "587"
	}
	addr := strings.TrimSpace(m.config.Host) + ":" + port
	var auth smtp.Auth
	if strings.TrimSpace(m.config.Username) != "" {
		auth = smtp.PlainAuth("", m.config.Username, m.config.Password, strings.TrimSpace(m.config.Host))
	}
	message := buildInvitationMessage(m.config.From, invitation)
	if err := smtp.SendMail(addr, auth, m.config.From, []string{invitation.To}, message); err != nil {
		return fmt.Errorf("send invitation email: %w", err)
	}
	log.Printf("invitation email sent: workspace=%q role=%q", invitation.WorkspaceName, invitation.Role)
	return nil
}

// LogMailer は開発環境向けの fallback。招待URL (生tokenを含む) をログに出すため、
// development 環境以外で使用してはならない。
type LogMailer struct{}

func (LogMailer) SendInvitation(_ context.Context, invitation appworkspace.InvitationEmail) error {
	log.Printf("[dev] invitation email (not sent): to=%q workspace=%q role=%q expires_at=%q", invitation.To, invitation.WorkspaceName, invitation.Role, invitation.ExpiresAt.Format(time.RFC3339))
	log.Printf("[dev] invitation accept url: %s", invitation.AcceptURL)
	return nil
}

// DisabledMailer は production で送信設定がない場合に使い、招待作成を失敗させる。
type DisabledMailer struct{}

func (DisabledMailer) SendInvitation(context.Context, appworkspace.InvitationEmail) error {
	return ErrNotConfigured
}

// buildInvitationMessage は招待メール本文を組み立てる。
// 機密情報 (Teams会議URL・文字起こし・session_id・token_hash 等) を含めてはならない。
func buildInvitationMessage(from string, invitation appworkspace.InvitationEmail) []byte {
	inviter := strings.TrimSpace(invitation.InviterName)
	if inviter == "" {
		inviter = "DeciScopeのメンバー"
	}
	subject := fmt.Sprintf("DeciScope: ワークスペース「%s」への招待", invitation.WorkspaceName)
	body := fmt.Sprintf(
		"%s さんから、DeciScope のワークスペース「%s」に招待されました。\r\n"+
			"\r\n"+
			"付与予定のロール: %s\r\n"+
			"\r\n"+
			"以下のリンクから参加できます。\r\n"+
			"\r\n"+
			"%s\r\n"+
			"\r\n"+
			"この招待リンクの有効期限は72時間 (%s まで) です。\r\n"+
			"\r\n"+
			"心当たりがない場合は、このメールを無視してください。\r\n",
		inviter, invitation.WorkspaceName, invitation.Role, invitation.AcceptURL,
		invitation.ExpiresAt.In(time.FixedZone("JST", 9*60*60)).Format("2006/01/02 15:04 MST"),
	)
	var message strings.Builder
	message.WriteString("From: " + from + "\r\n")
	message.WriteString("To: " + invitation.To + "\r\n")
	message.WriteString("Subject: " + encodeSubject(subject) + "\r\n")
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	message.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	message.WriteString("\r\n")
	message.WriteString(body)
	return []byte(message.String())
}

func encodeSubject(subject string) string {
	// RFC 2047 B-encoding (UTF-8) で日本語件名を安全に送る。
	return mime.BEncoding.Encode("UTF-8", subject)
}
