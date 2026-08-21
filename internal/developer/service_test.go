package developer_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/developer"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCredentialIPBoundaryExpiryAndAccountCascade(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, adminID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Developer Owner','member','active'),($4,$5,$6,'Developer Admin','admin','active')`, ownerID, ownerID.String()+"@test.local", "developer_"+ownerID.String()[:8], adminID, adminID.String()+"@test.local", "admin_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	service := developer.NewService(pool)
	control, err := service.GetControl(ctx)
	if err != nil || control.Enabled {
		t.Fatalf("unexpected initial control: %#v %v", control, err)
	}
	control, err = service.UpdateControl(ctx, adminID, developer.ControlUpdate{Enabled: true, MaxServiceAccounts: 2, MaxActiveKeys: 2, DefaultTTLDays: 30, ExpectedVersion: 1}, "developer-control")
	if err != nil || !control.Enabled || control.Version != 2 {
		t.Fatalf("enable control failed: %#v %v", control, err)
	}
	account, err := service.CreateAccount(ctx, ownerID, developer.AccountCreate{Name: "Render pipeline"}, "developer-account")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := service.IssueKey(ctx, ownerID, account.ID, developer.KeyCreate{Scopes: []string{developer.IdentityReadScope()}, IPAllowlist: []string{"203.0.113.0/24"}, TTLDays: 5}, "developer-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, credential.PlaintextKey, "203.0.113.19:443", developer.IdentityReadScope()); err != nil {
		t.Fatalf("allowlisted source rejected: %v", err)
	}
	if _, err := service.Authenticate(ctx, credential.PlaintextKey, "198.51.100.8:443", developer.IdentityReadScope()); !errors.Is(err, developer.ErrIP) {
		t.Fatalf("non-allowlisted source did not fail closed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE developer_api_keys SET expires_at=now()-interval '1 second' WHERE id=$1`, credential.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, credential.PlaintextKey, "203.0.113.19:443", developer.IdentityReadScope()); !errors.Is(err, developer.ErrUnauthenticated) {
		t.Fatalf("expired key authenticated: %v", err)
	}
	second, err := service.IssueKey(ctx, ownerID, account.ID, developer.KeyCreate{Scopes: []string{developer.IdentityReadScope()}, TTLDays: 5}, "developer-key-2")
	if err != nil {
		t.Fatal(err)
	}
	account, err = service.RevokeAccount(ctx, ownerID, account.ID, developer.Transition{ExpectedVersion: account.Version, Reason: "Retire the bounded render automation identity", Confirmed: true}, "developer-account-revoke")
	if err != nil || account.Status != "revoked" {
		t.Fatalf("account revocation failed: %#v %v", account, err)
	}
	if _, err := service.Authenticate(ctx, second.PlaintextKey, "127.0.0.1:443", developer.IdentityReadScope()); !errors.Is(err, developer.ErrUnauthenticated) {
		t.Fatalf("Service Account cascade did not invalidate key: %v", err)
	}
	var plaintextMatches, activeKeys int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE secret_hash=$1 OR secret_hash=$2),count(*) FILTER (WHERE status='active') FROM developer_api_keys WHERE service_account_id=$3`, credential.PlaintextKey, second.PlaintextKey, account.ID).Scan(&plaintextMatches, &activeKeys); err != nil || plaintextMatches != 0 || activeKeys != 0 {
		t.Fatalf("credential storage/cascade mismatch: plaintext=%d active=%d err=%v", plaintextMatches, activeKeys, err)
	}
}

func testPool(t *testing.T) (*pgxpool.Pool, func()) {
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
	schema := "test_developer_" + uuid.NewString()[:8]
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
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
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = root.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	}
}
