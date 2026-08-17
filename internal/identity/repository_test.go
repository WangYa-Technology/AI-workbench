package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmailIdentityProfileAndSessionLifecycle(t *testing.T) {
	pool, cleanup := identityTestPool(t)
	defer cleanup()
	ctx := context.Background()
	repository := identity.NewRepository(pool)
	register := identity.RegisterInput{
		Email: "Creator.Example@example.com", Password: "correct horse battery staple", Handle: "creator_example",
		DisplayName: "Creator Example", Locale: "en-US", Timezone: "America/New_York",
	}
	client := identity.ClientInfo{Label: "Chrome on macOS", NetworkHash: identity.HashNetwork("203.0.113.9"), RequestID: "register-test"}
	user, firstToken, err := repository.Register(ctx, register, client)
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "creator.example@example.com" || user.Role != "member" || !slices.Contains(user.Permissions, "account:self") {
		t.Fatalf("unexpected account: %#v", user)
	}
	var passwordHash, tokenHash, networkHash string
	if err := pool.QueryRow(ctx, `SELECT u.password_hash,s.token_hash,COALESCE(s.network_hash,'') FROM users u JOIN sessions s ON s.user_id=u.id WHERE u.id=$1`, user.ID).Scan(&passwordHash, &tokenHash, &networkHash); err != nil {
		t.Fatal(err)
	}
	if passwordHash == register.Password || tokenHash == firstToken || tokenHash != identity.HashToken(firstToken) || networkHash == "203.0.113.9" {
		t.Fatalf("credential or network evidence was not safely stored")
	}
	if _, _, err := repository.Login(ctx, identity.LoginInput{Email: register.Email, Password: "wrong password"}, client); !errors.Is(err, identity.ErrInvalidLogin) {
		t.Fatalf("expected generic invalid login, got %v", err)
	}
	loggedIn, secondToken, err := repository.Login(ctx, identity.LoginInput{Email: register.Email, Password: register.Password}, identity.ClientInfo{Label: "Safari on iOS", RequestID: "login-test"})
	if err != nil || loggedIn.ID != user.ID || secondToken == firstToken {
		t.Fatalf("login did not create a distinct session: user=%#v err=%v", loggedIn, err)
	}
	sessions, err := repository.ListSessions(ctx, user.ID, secondToken, identity.SessionListInput{})
	if err != nil || len(sessions.Items) != 2 || !sessions.Items[0].Current {
		t.Fatalf("unexpected session list: %#v err=%v", sessions, err)
	}
	updated, err := repository.UpdateProfile(ctx, user.ID, identity.ProfileInput{DisplayName: "Creator Studio", Locale: "zh-CN", Timezone: "Asia/Shanghai"}, "profile-test")
	if err != nil || updated.DisplayName != "Creator Studio" || updated.Locale != "zh-CN" {
		t.Fatalf("profile update failed: %#v err=%v", updated, err)
	}
	revoked, err := repository.RevokeOtherSessions(ctx, user.ID, secondToken, "revoke-others-test")
	if err != nil || revoked != 1 {
		t.Fatalf("expected one other session revoked, got %d err=%v", revoked, err)
	}
	if _, err := repository.Authenticate(ctx, firstToken); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("revoked token authenticated: %v", err)
	}
	if _, err := repository.Authenticate(ctx, secondToken); err != nil {
		t.Fatalf("current token should remain active: %v", err)
	}
	providers, err := repository.ListOAuthProviders(ctx)
	if err != nil || len(providers) != 2 || providers[0].Available || providers[1].Available {
		t.Fatalf("OAuth providers did not fail closed: %#v err=%v", providers, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_id=$1 AND action LIKE 'identity.%'`, user.ID).Scan(&auditCount); err != nil || auditCount < 4 {
		t.Fatalf("identity audit evidence missing: count=%d err=%v", auditCount, err)
	}
}

func TestRegistrationValidationAndConflicts(t *testing.T) {
	pool, cleanup := identityTestPool(t)
	defer cleanup()
	repository := identity.NewRepository(pool)
	ctx := context.Background()
	valid := identity.RegisterInput{Email: "member@example.com", Password: "a secure password", Handle: "member_one", DisplayName: "Member One", Locale: "en-US", Timezone: "UTC"}
	if _, _, err := repository.Register(ctx, valid, identity.ClientInfo{RequestID: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Register(ctx, valid, identity.ClientInfo{RequestID: "duplicate"}); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("expected duplicate conflict, got %v", err)
	}
	invalid := valid
	invalid.Handle = "Not Allowed"
	if _, _, err := repository.Register(ctx, invalid, identity.ClientInfo{RequestID: "invalid"}); !errors.Is(err, identity.ErrInvalid) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestRegistrationAccountLinkRiskIsThresholdedAndPrivacyMinimized(t *testing.T) {
	pool, cleanup := identityTestPool(t)
	defer cleanup()
	ctx := context.Background()
	repository := identity.NewRepository(pool)
	sharedNetwork := identity.HashNetwork("198.51.100.18")

	users := make([]identity.User, 0, 3)
	for index := 1; index <= 3; index++ {
		handle := "linked_" + uuid.NewString()[:8]
		user, _, err := repository.Register(ctx, identity.RegisterInput{
			Email: handle + "@test.local", Password: "local-test-password", Handle: handle,
			DisplayName: "Linked Test Account", Locale: "en-US", Timezone: "UTC",
		}, identity.ClientInfo{Label: "risk registration", NetworkHash: sharedNetwork, RequestID: "linked-register"})
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, user)
		var signalCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM risk_signals WHERE signal_type='account_link'`).Scan(&signalCount); err != nil {
			t.Fatal(err)
		}
		expected := 0
		if index == 3 {
			expected = 1
		}
		if signalCount != expected {
			t.Fatalf("registration %d produced %d account-link signals, expected %d", index, signalCount, expected)
		}
	}

	var score, version int
	var severity string
	var evidence map[string]any
	if err := pool.QueryRow(ctx, `
		SELECT score,severity,(evidence->>'riskRuleVersion')::integer,evidence
		FROM risk_signals WHERE signal_type='account_link' AND subject_user_id=$1`, users[2].ID).
		Scan(&score, &severity, &version, &evidence); err != nil {
		t.Fatal(err)
	}
	if score != 65 || severity != "medium" || version != 1 || evidence["linkedAccountCount"] != float64(3) ||
		evidence["minimumAccounts"] != float64(3) || evidence["windowHours"] != float64(24) || evidence["networkDataStored"] != false {
		t.Fatalf("unexpected privacy-minimized account-link evidence: score=%d severity=%s version=%d evidence=%#v", score, severity, version, evidence)
	}
	evidenceBody, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{sharedNetwork, "198.51.100.18", "networkHash", "ipAddress", "deviceFingerprint", "linkedAccounts"} {
		if strings.Contains(string(evidenceBody), forbidden) {
			t.Fatalf("risk evidence exposed forbidden identity-link detail %q: %s", forbidden, evidenceBody)
		}
	}

	if _, _, err := repository.Login(ctx, identity.LoginInput{Email: users[2].Email, Password: "local-test-password"}, identity.ClientInfo{Label: "ordinary login", NetworkHash: sharedNetwork, RequestID: "linked-login"}); err != nil {
		t.Fatal(err)
	}
	differentHandle := "separate_" + uuid.NewString()[:8]
	if _, _, err := repository.Register(ctx, identity.RegisterInput{
		Email: differentHandle + "@test.local", Password: "local-test-password", Handle: differentHandle,
		DisplayName: "Separate Test Account", Locale: "en-US", Timezone: "UTC",
	}, identity.ClientInfo{Label: "separate registration", NetworkHash: identity.HashNetwork("203.0.113.71"), RequestID: "separate-register"}); err != nil {
		t.Fatal(err)
	}
	var finalCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM risk_signals WHERE signal_type='account_link'`).Scan(&finalCount); err != nil || finalCount != 1 {
		t.Fatalf("login or unrelated network duplicated account-link evidence: count=%d err=%v", finalCount, err)
	}
}

func identityTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	schema := "test_identity_" + uuid.NewString()[:8]
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
