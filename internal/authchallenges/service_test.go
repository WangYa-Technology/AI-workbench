package authchallenges

import (
	"context"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRegistrationChallengeVerifiesBeforeCreatingAccount(t *testing.T) {
	pool, cleanup := challengeTestPool(t)
	defer cleanup()
	root := t.TempDir()
	key := []byte("01234567890123456789012345678901")
	service := NewService(pool, key, "local_file", root)
	ctx := context.Background()
	challenge, err := service.Start(ctx, "new-user@example.com", RegistrationCode, "en-US", "challenge-test")
	if err != nil {
		t.Fatal(err)
	}
	var userCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email)='new-user@example.com'`).Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 0 {
		t.Fatalf("registration challenge created an account before verification")
	}
	var nonce, ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT code_nonce,code_ciphertext FROM identity_auth_challenges WHERE id=$1`, challenge.ID).Scan(&nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	code, err := service.decrypt(challenge.ID, RegistrationCode, nonce, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	user, token, err := service.CompleteRegistration(ctx, challenge.ID, identity.RegisterInput{Email: "new-user@example.com", Password: "a secure password", Handle: "new_user", DisplayName: "New User", Locale: "en-US", Timezone: "UTC"}, code, identity.ClientInfo{RequestID: "challenge-register"})
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "new-user@example.com" || !user.EmailVerified || token == "" {
		t.Fatalf("unexpected verified registration: %#v token=%q", user, token)
	}
	if _, _, err := service.CompleteRegistration(ctx, challenge.ID, identity.RegisterInput{Email: user.Email, Password: "a secure password", Handle: "new_user_2", DisplayName: "New User", Locale: "en-US", Timezone: "UTC"}, code, identity.ClientInfo{RequestID: "replay"}); err != ErrConflict {
		t.Fatalf("expected consumed challenge conflict, got %v", err)
	}
}

func TestLoginChallengeDeliveryAndConfirmation(t *testing.T) {
	pool, cleanup := challengeTestPool(t)
	defer cleanup()
	root := t.TempDir()
	key := []byte("01234567890123456789012345678901")
	service := NewService(pool, key, "local_file", root)
	ctx := context.Background()
	repository := identity.NewRepository(pool)
	user, _, err := repository.Register(ctx, identity.RegisterInput{Email: "login-user@example.com", Password: "a secure password", Handle: "login_user", DisplayName: "Login User", Locale: "en-US", Timezone: "UTC"}, identity.ClientInfo{RequestID: "seed"})
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := service.Start(ctx, user.Email, LoginCode, "en-US", "login-challenge")
	if err != nil {
		t.Fatal(err)
	}
	var nonce, ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT code_nonce,code_ciphertext FROM identity_auth_challenges WHERE id=$1`, challenge.ID).Scan(&nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	code, err := service.decrypt(challenge.ID, LoginCode, nonce, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"challengeId":"` + challenge.ID.String() + `"}`)
	if err := service.HandleDeliveryJob(ctx, jobs.Job{Payload: payload, Attempts: 1, MaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "mailbox", "auth-challenges", challenge.ID.String()+".eml")); err != nil {
		t.Fatalf("code email was not written: %v", err)
	}
	loggedIn, token, err := service.ConfirmLogin(ctx, challenge.ID, user.Email, code, identity.ClientInfo{RequestID: "code-login"})
	if err != nil || loggedIn.ID != user.ID || token == "" {
		t.Fatalf("code login failed: user=%#v token=%q err=%v", loggedIn, token, err)
	}
}

func challengeTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		testutil.DatabaseUnavailable(t, err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		testutil.DatabaseUnavailable(t, err)
	}
	schema := "test_auth_challenge_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(baseURL)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
