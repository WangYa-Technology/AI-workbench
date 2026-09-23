package httpapi_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/emailactions"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestIdentityEmailHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	mediaRoot := t.TempDir()
	key := bytes.Repeat([]byte{0x73}, 32)
	cfg := config.Config{Environment: "test", MediaRoot: mediaRoot, WebOrigin: "http://127.0.0.1:5173", LocalProviderEnabled: true, EmailDeliveryMode: "local_file", EmailActionKey: key}
	server := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	workerService := emailactions.NewService(pool, key, "local_file", mediaRoot, cfg.WebOrigin)
	jobRepository := jobs.NewRepository(pool)
	client := testHTTPClient(t)
	email := "email-http-" + uuid.NewString()[:8] + "@test.local"
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/register", map[string]any{
		"email": email, "password": "initial-password-2026", "handle": "email_" + uuid.NewString()[:8], "displayName": "Email HTTP", "locale": "en-US", "timezone": "UTC",
	}, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("registration failed: %d", response.StatusCode)
	}

	job := claimHTTPJobKind(t, ctx, pool, "identity-email-http-worker", emailactions.DeliveryJobKind)
	if err := workerService.HandleDeliveryJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(ctx, job, "identity-email-http-worker"); err != nil {
		t.Fatal(err)
	}
	var actionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM identity_email_actions WHERE kind='verify_email' ORDER BY created_at DESC LIMIT 1`).Scan(&actionID); err != nil {
		t.Fatal(err)
	}
	verificationMail, err := os.ReadFile(filepath.Join(mediaRoot, "mailbox", userIDForEmail(t, ctx, pool, email).String(), actionID.String()+".eml"))
	if err != nil {
		t.Fatal(err)
	}
	verificationToken := regexp.MustCompile(`emailact_[A-Za-z0-9_-]+`).FindString(string(verificationMail))

	var actions struct {
		Items []emailactions.Action `json:"items"`
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/account/email-actions?limit=1", nil, &actions)
	if response.StatusCode != http.StatusOK || len(actions.Items) != 1 || actions.Items[0].EmailHint == email || actions.Items[0].Attempts[0].ReceiptSHA256 == nil {
		t.Fatalf("safe account action evidence mismatch: status=%d actions=%#v", response.StatusCode, actions.Items)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/account/email-actions?limit=51", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized account email-action page was accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/account/email-actions?cursor=modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified account email-action cursor was accepted: %d", response.StatusCode)
	}

	if _, err := pool.Exec(ctx, `UPDATE identity_email_actions SET status='dead_letter',dead_lettered_at=now(),version=version+1 WHERE id=$1`, actionID); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("fixture-password-2026"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status,locale,timezone,email_verified_at,password_hash) VALUES($1,'email-admin@test.local','email_admin','Email Admin','admin','active','en-US','UTC',now(),$2)`, uuid.MustParse("00000000-0000-4000-8000-000000000004"), string(hash)); err != nil {
		t.Fatal(err)
	}
	adminClient := testHTTPClient(t)
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{"email": "email-admin@test.local", "password": "fixture-password-2026"}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("admin password session failed: %d", response.StatusCode)
	}
	var deadLetters struct {
		Items      []emailactions.Action `json:"items"`
		NextCursor *string               `json:"nextCursor"`
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/email-actions/dead-letters?q=email-http-&kind=verify_email&limit=1", nil, &deadLetters)
	if response.StatusCode != http.StatusOK || len(deadLetters.Items) != 1 || deadLetters.Items[0].ID != actionID {
		t.Fatalf("admin dead-letter projection mismatch: status=%d items=%#v", response.StatusCode, deadLetters.Items)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/email-actions/dead-letters?kind=unsupported", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported Admin email kind was accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/email-actions/dead-letters?limit=51", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized Admin email page was accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/email-actions/dead-letters?cursor=modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified Admin email cursor was accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/email-actions/"+actionID.String()+"/retry", map[string]any{
		"expectedVersion": deadLetters.Items[0].Version}, nil)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("admin identity email retry failed: %d", response.StatusCode)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/email-verification/confirm", map[string]any{"token": verificationToken}, nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("verification confirmation failed: %d", response.StatusCode)
	}
	var current struct {
		User struct {
			EmailVerified bool `json:"emailVerified"`
		} `json:"user"`
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/auth/session", nil, &current)
	if response.StatusCode != http.StatusOK || !current.User.EmailVerified {
		t.Fatalf("verified session projection mismatch: status=%d user=%#v", response.StatusCode, current.User)
	}

	response = requestJSON(t, testHTTPClient(t), http.MethodPost, server.URL+"/api/v1/auth/password-reset-requests", map[string]any{"email": "unknown@example.test"}, nil)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("unknown email reset request leaked status: %d", response.StatusCode)
	}
	response = requestJSON(t, testHTTPClient(t), http.MethodPost, server.URL+"/api/v1/auth/password-reset-requests", map[string]any{"email": email}, nil)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("known email reset request status differed: %d", response.StatusCode)
	}
	job = claimHTTPJobKind(t, ctx, pool, "identity-email-http-worker", emailactions.DeliveryJobKind)
	if err := workerService.HandleDeliveryJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(ctx, job, "identity-email-http-worker"); err != nil {
		t.Fatal(err)
	}
	var resetID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM identity_email_actions WHERE kind='password_reset' ORDER BY created_at DESC LIMIT 1`).Scan(&resetID); err != nil {
		t.Fatal(err)
	}
	resetMail, err := os.ReadFile(filepath.Join(mediaRoot, "mailbox", userIDForEmail(t, ctx, pool, email).String(), resetID.String()+".eml"))
	if err != nil {
		t.Fatal(err)
	}
	resetToken := regexp.MustCompile(`emailact_[A-Za-z0-9_-]+`).FindString(string(resetMail))
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/password-reset-confirm", map[string]any{"token": resetToken, "password": "replacement-password-2026"}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("password reset confirmation failed: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/auth/session", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("password reset did not revoke current cookie session: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{"email": email, "password": "replacement-password-2026"}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("new password login failed: %d", response.StatusCode)
	}
}

func userIDForEmail(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}
