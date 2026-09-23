package authchallenges

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
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

func TestSMTPRegistrationRetriesAndCreatesVerifiedAccount(t *testing.T) {
	pool, cleanup := challengeTestPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	sender := &captureSender{fail: true}
	service := NewService(pool, []byte("01234567890123456789012345678901"), "smtp", root, sender)
	challenge, err := service.Start(ctx, "smtp-user@example.com", RegistrationCode, "en-US", "smtp-register")
	if err != nil {
		t.Fatal(err)
	}
	job := jobs.Job{Payload: []byte(`{"challengeId":"` + challenge.ID.String() + `"}`), Attempts: 1, MaxAttempts: 5}
	if err := service.HandleDeliveryJob(ctx, job); err == nil {
		t.Fatal("failed SMTP delivery did not request retry")
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
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_auth_challenge_delivery_attempts WHERE challenge_id=$1 AND adapter='smtp' AND (status='delivered' OR error_code='smtp_delivery_failed')`, challenge.ID).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("missing SMTP evidence: %d %v", attempts, err)
	}
	if _, err := os.Stat(filepath.Join(root, "mailbox")); !os.IsNotExist(err) {
		t.Fatal("SMTP created a local mailbox")
	}
	code := regexp.MustCompile(`\b[0-9]{6}\b`).FindString(string(sender.body))
	user, token, err := service.CompleteRegistration(ctx, challenge.ID, identity.RegisterInput{Email: "smtp-user@example.com", Password: "smtp-password-2026", Handle: "smtp_user", DisplayName: "SMTP User", Locale: "en-US", Timezone: "UTC"}, code, identity.ClientInfo{})
	if err != nil || !user.EmailVerified || token == "" {
		t.Fatalf("SMTP registration failed: %v", err)
	}
}
