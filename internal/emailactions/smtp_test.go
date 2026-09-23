package emailactions

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/identity"
)

type captureSender struct {
	calls int
	fail  bool
	body  []byte
}

func (s *captureSender) Send(_ context.Context, _, _ string, body []byte) error {
	s.calls++
	if s.fail {
		return errors.New("upstream secret must not be persisted")
	}
	s.body = body
	return nil
}

func TestSMTPPasswordResetRetriesAndRevokesOldSession(t *testing.T) {
	pool, cleanup := emailActionTestPool(t)
	defer cleanup()
	ctx := context.Background()
	repository := identity.NewRepository(pool)
	user, oldToken, err := repository.Register(ctx, identity.RegisterInput{Email: "smtp-reset@example.com", Password: "initial-password-2026", Handle: "smtp_reset", DisplayName: "SMTP Reset", Locale: "en-US", Timezone: "UTC"}, identity.ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	sender := &captureSender{fail: true}
	service := NewService(pool, bytes.Repeat([]byte{0x53}, 32), "smtp", root, "https://example.com", sender)
	if err := service.RequestPasswordReset(ctx, user.Email, "smtp-reset"); err != nil {
		t.Fatal(err)
	}
	job := claimKind(t, ctx, pool, DeliveryJobKind)
	if err := service.HandleDeliveryJob(ctx, job); err == nil {
		t.Fatal("failed delivery did not request retry")
	}
	sender.fail = false
	job.Attempts++
	if err := service.HandleDeliveryJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleDeliveryJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 2 {
		t.Fatalf("duplicate delivery: %d", sender.calls)
	}
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_email_delivery_attempts WHERE adapter='smtp' AND (status='delivered' OR error_code='smtp_delivery_failed')`).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("missing SMTP evidence: %d %v", attempts, err)
	}
	if _, err := os.Stat(filepath.Join(root, "mailbox")); !os.IsNotExist(err) {
		t.Fatal("SMTP created local mailbox")
	}
	token := regexp.MustCompile(`emailact_[A-Za-z0-9_-]+`).FindString(string(sender.body))
	if _, err := service.ConfirmPasswordReset(ctx, token, "replacement-password-2026", "smtp-reset-confirm"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Authenticate(ctx, oldToken); err == nil {
		t.Fatal("old session survived reset")
	}
	if _, _, err := repository.Login(ctx, identity.LoginInput{Email: user.Email, Password: "replacement-password-2026"}, identity.ClientInfo{}); err != nil {
		t.Fatal(err)
	}
}
