package emailactions

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIdentityEmailVerificationAndPasswordResetLifecycle(t *testing.T) {
	pool, cleanup := emailActionTestPool(t)
	defer cleanup()
	ctx := context.Background()
	repository := identity.NewRepository(pool)
	user, oldToken, err := repository.Register(ctx, identity.RegisterInput{
		Email: "mail-owner@example.test", Password: "correct-horse-123", Handle: "mail_owner", DisplayName: "Mail Owner", Locale: "en-US", Timezone: "UTC",
	}, identity.ClientInfo{Label: "Integration browser", RequestID: "register-email-owner"})
	if err != nil {
		t.Fatal(err)
	}
	mediaRoot := t.TempDir()
	key := bytes.Repeat([]byte{0x53}, 32)
	service := NewService(pool, key, "local_file", mediaRoot, "http://127.0.0.1:5173")

	verification, err := service.RequestVerification(ctx, user.ID, "request-verification")
	if err != nil {
		t.Fatal(err)
	}
	var nonce, ciphertext []byte
	var storedHash string
	if err := pool.QueryRow(ctx, `SELECT token_hash,token_nonce,token_ciphertext FROM identity_email_actions WHERE id=$1`, verification.ID).Scan(&storedHash, &nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if len(storedHash) != 64 || len(nonce) != 12 || bytes.Contains(ciphertext, []byte("emailact_")) {
		t.Fatalf("token storage boundary mismatch: hash=%d nonce=%d ciphertext=%x", len(storedHash), len(nonce), ciphertext)
	}
	deliveryJob := claimKind(t, ctx, pool, DeliveryJobKind)
	if err := service.HandleDeliveryJob(ctx, deliveryJob); err != nil {
		t.Fatal(err)
	}
	mailPath := filepath.Join(mediaRoot, "mailbox", user.ID.String(), verification.ID.String()+".eml")
	mailBody, err := os.ReadFile(mailPath)
	if err != nil {
		t.Fatal(err)
	}
	verificationToken := regexp.MustCompile(`emailact_[A-Za-z0-9_-]+`).FindString(string(mailBody))
	if verificationToken == "" || bytes.Contains(mailBody, ciphertext) {
		t.Fatalf("local mailbox contract mismatch: %s", mailBody)
	}
	if err := service.ConfirmVerification(ctx, verificationToken, "consume-verification"); err != nil {
		t.Fatal(err)
	}
	var verified bool
	var tokenErased bool
	if err := pool.QueryRow(ctx, `SELECT email_verified_at IS NOT NULL FROM users WHERE id=$1`, user.ID).Scan(&verified); err != nil || !verified {
		t.Fatalf("email was not verified: verified=%v err=%v", verified, err)
	}
	if err := pool.QueryRow(ctx, `SELECT token_hash IS NULL AND token_nonce IS NULL AND token_ciphertext IS NULL FROM identity_email_actions WHERE id=$1`, verification.ID).Scan(&tokenErased); err != nil || !tokenErased {
		t.Fatalf("consumed token material was not erased: erased=%v err=%v", tokenErased, err)
	}
	if _, err := os.Stat(mailPath); !os.IsNotExist(err) {
		t.Fatalf("consumed local email still exists: %v", err)
	}
	if err := service.ConfirmVerification(ctx, verificationToken, "reuse-verification"); err != ErrNotFound {
		t.Fatalf("one-time token reuse did not fail closed: %v", err)
	}

	if err := service.RequestPasswordReset(ctx, "MAIL-OWNER@example.test", "request-reset"); err != nil {
		t.Fatal(err)
	}
	resetJob := claimKind(t, ctx, pool, DeliveryJobKind)
	if err := service.HandleDeliveryJob(ctx, resetJob); err != nil {
		t.Fatal(err)
	}
	var resetID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM identity_email_actions WHERE user_id=$1 AND kind='password_reset' ORDER BY created_at DESC LIMIT 1`, user.ID).Scan(&resetID); err != nil {
		t.Fatal(err)
	}
	resetMail, err := os.ReadFile(filepath.Join(mediaRoot, "mailbox", user.ID.String(), resetID.String()+".eml"))
	if err != nil {
		t.Fatal(err)
	}
	resetToken := regexp.MustCompile(`emailact_[A-Za-z0-9_-]+`).FindString(string(resetMail))
	revoked, err := service.ConfirmPasswordReset(ctx, resetToken, "new-correct-horse-456", "consume-reset")
	if err != nil || revoked != 1 {
		t.Fatalf("password reset session revocation mismatch: revoked=%d err=%v", revoked, err)
	}
	if _, err := repository.Authenticate(ctx, oldToken); err != identity.ErrUnauthenticated {
		t.Fatalf("old session remained usable after reset: %v", err)
	}
	if _, _, err := repository.Login(ctx, identity.LoginInput{Email: user.Email, Password: "correct-horse-123"}, identity.ClientInfo{RequestID: "old-password"}); err != identity.ErrInvalidLogin {
		t.Fatalf("old password remained usable: %v", err)
	}
	if _, _, err := repository.Login(ctx, identity.LoginInput{Email: user.Email, Password: "new-correct-horse-456"}, identity.ClientInfo{RequestID: "new-password"}); err != nil {
		t.Fatalf("new password did not work: %v", err)
	}

	before := countActions(t, ctx, pool)
	if err := service.RequestPasswordReset(ctx, "unknown@example.test", "non-enumerating-reset"); err != nil {
		t.Fatal(err)
	}
	if after := countActions(t, ctx, pool); after != before {
		t.Fatalf("unknown reset request created evidence: before=%d after=%d", before, after)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity_email_delivery_attempts SET status='failed' WHERE action_id=$1`, resetID); err == nil {
		t.Fatal("delivery attempt evidence accepted mutation")
	}
}

func TestEmailDeadLetterDirectoryPaginationAndExactCancellation(t *testing.T) {
	pool, cleanup := emailActionTestPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID := uuid.New()
	handle := "email_scale_" + adminID.String()[:8]
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Email Scale Admin','admin','active')`, adminID, adminID.String()+"@test.local", handle); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity_email_actions(user_id,kind,status,email_snapshot,locale,version,attempt_count,expires_at,created_at,updated_at,dead_lettered_at)
		SELECT $1,CASE WHEN value%2=0 THEN 'verify_email' ELSE 'password_reset' END,'dead_letter',$2,'en-US',1,5,
		       now()+interval '1 hour',now()-make_interval(secs=>value),now()-make_interval(secs=>value),now()-make_interval(secs=>value)
		FROM generate_series(1,106) value`, adminID, handle+"@test.local"); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, bytes.Repeat([]byte{0x53}, 32), "disabled", t.TempDir(), "http://127.0.0.1:5173")
	ownerSeen := make(map[uuid.UUID]struct{})
	ownerCursor := ""
	for {
		page, err := service.ListForUser(ctx, adminID, OwnerListInput{Cursor: ownerCursor, Limit: 25})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if _, duplicate := ownerSeen[item.ID]; duplicate {
				t.Fatalf("duplicate owner email action %s", item.ID)
			}
			ownerSeen[item.ID] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		ownerCursor = *page.NextCursor
	}
	if len(ownerSeen) != 106 {
		t.Fatalf("expected 106 owner email actions, got %d", len(ownerSeen))
	}
	if _, err := service.ListForUser(ctx, adminID, OwnerListInput{Cursor: ownerCursor + "modified", Limit: 25}); !errors.Is(err, ErrInvalidOwnerFilter) {
		t.Fatalf("modified owner email cursor accepted: %v", err)
	}
	seen := make(map[uuid.UUID]struct{})
	var oldest Action
	cursor := ""
	for {
		page, err := service.ListDeadLetters(ctx, DeadLetterListInput{Query: handle, Cursor: cursor, Limit: 25})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("duplicate email dead letter %s", item.ID)
			}
			seen[item.ID] = struct{}{}
			oldest = item
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 {
		t.Fatalf("expected 106 email dead letters, got %d", len(seen))
	}
	if _, err := service.ListDeadLetters(ctx, DeadLetterListInput{Cursor: cursor + "modified", Limit: 25}); !errors.Is(err, ErrInvalidDeadLetterFilter) {
		t.Fatalf("modified email cursor accepted: %v", err)
	}
	cancelled, err := service.Cancel(ctx, adminID, oldest.ID, Transition{ExpectedVersion: oldest.Version, Reason: "Scale test confirms exact cancellation outside the former recovery window", Confirmed: true}, "email-scale-cancel")
	if err != nil || cancelled.ID != oldest.ID || cancelled.Status != "cancelled" {
		t.Fatalf("oldest email cancellation failed: %#v %v", cancelled, err)
	}
}

func TestIdentityEmailDeadLetterRetryAndCancel(t *testing.T) {
	pool, cleanup := emailActionTestPool(t)
	defer cleanup()
	ctx := context.Background()
	userID := uuid.New()
	adminID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,locale,role) VALUES($1,'disabled@example.test','disabled_mail','Disabled Mail','en-US','member'),($2,'operator@example.test','email_operator','Email Operator','en-US','admin')`, userID, adminID); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, bytes.Repeat([]byte{0x64}, 32), "disabled", t.TempDir(), "http://127.0.0.1:5173")
	action, err := service.RequestVerification(ctx, userID, "disabled-verification")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		err := service.HandleDeliveryJob(ctx, jobs.Job{Kind: DeliveryJobKind, Payload: mustPayload(action.ID), Attempts: attempt, MaxAttempts: 5})
		if attempt < 5 && err == nil {
			t.Fatalf("attempt %d did not request a retry", attempt)
		}
		if attempt == 5 && err != nil {
			t.Fatalf("terminal attempt returned worker error: %v", err)
		}
	}
	deadLetters, err := service.ListDeadLetters(ctx, DeadLetterListInput{})
	if err != nil || len(deadLetters.Items) != 1 || deadLetters.Items[0].AttemptCount != 5 {
		t.Fatalf("dead-letter evidence mismatch: items=%#v err=%v", deadLetters, err)
	}
	retried, err := service.Retry(ctx, adminID, action.ID, Transition{ExpectedVersion: deadLetters.Items[0].Version, Reason: "Verified delivery configuration recovery", Confirmed: true}, "retry-email-action")
	if err != nil || retried.Status != "queued" {
		t.Fatalf("admin retry mismatch: item=%#v err=%v", retried, err)
	}
	cancelled, err := service.Cancel(ctx, adminID, action.ID, Transition{ExpectedVersion: retried.Version, Reason: "Cancel after recipient support confirmation", Confirmed: true}, "cancel-email-action")
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("admin cancellation mismatch: item=%#v err=%v", cancelled, err)
	}
	var auditCount, notificationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_id=$1 AND action IN ('admin.identity_email_retried','admin.identity_email_cancelled')`, adminID).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("admin audit evidence mismatch: count=%d err=%v", auditCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND kind='security.email_delivery_retried'`, userID).Scan(&notificationCount); err != nil || notificationCount != 1 {
		t.Fatalf("recovery notification mismatch: count=%d err=%v", notificationCount, err)
	}
}

func claimKind(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind string) jobs.Job {
	t.Helper()
	job, err := jobs.NewRepository(pool).Claim(ctx, "identity-email-test-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != kind {
		t.Fatalf("expected job kind %q, got %q", kind, job.Kind)
	}
	return job
}

func mustPayload(actionID uuid.UUID) []byte {
	return []byte(`{"actionId":"` + actionID.String() + `"}`)
}

func countActions(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_email_actions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func emailActionTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	root, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	if err := root.Ping(ctx); err != nil {
		root.Close()
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	schema := "test_email_actions_" + uuid.NewString()[:8]
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		root.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	}
}
