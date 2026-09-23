package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIdentityAndNotificationHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173",
		LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	client := testHTTPClient(t)
	email := "http-identity-" + uuid.NewString()[:8] + "@test.local"
	handle := "http_" + uuid.NewString()[:8]
	var registered struct {
		User identity.User `json:"user"`
	}
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/register", map[string]any{
		"email": email, "password": "correct-horse-2026", "handle": handle, "displayName": "HTTP Identity",
		"locale": "en-US", "timezone": "Europe/London",
	}, &registered)
	if response.StatusCode != http.StatusCreated || registered.User.Email != email {
		t.Fatalf("unexpected registration response: status=%d user=%#v", response.StatusCode, registered.User)
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name != identity.SessionCookie || !cookies[0].HttpOnly || cookies[0].Value == "" {
		t.Fatalf("registration did not establish a protected session cookie: %#v", cookies)
	}

	var session struct {
		User identity.User `json:"user"`
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/auth/session", nil, &session)
	if response.StatusCode != http.StatusOK || session.User.ID != registered.User.ID {
		t.Fatalf("session lookup failed: status=%d user=%#v", response.StatusCode, session.User)
	}
	response = requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/account/profile", map[string]any{
		"displayName": "HTTP Identity Updated", "locale": "en-US", "timezone": "America/New_York",
	}, &session)
	if response.StatusCode != http.StatusOK || session.User.DisplayName != "HTTP Identity Updated" || session.User.Timezone != "America/New_York" {
		t.Fatalf("profile update failed: status=%d user=%#v", response.StatusCode, session.User)
	}

	var sessions struct {
		Items []identity.Session `json:"items"`
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/account/sessions", nil, &sessions)
	if response.StatusCode != http.StatusOK || len(sessions.Items) != 1 || !sessions.Items[0].Current || sessions.Items[0].ClientLabel == "" {
		t.Fatalf("active session evidence missing: status=%d items=%#v", response.StatusCode, sessions.Items)
	}

	var providers struct {
		Items []identity.OAuthProvider `json:"items"`
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/auth/oauth/providers", nil, &providers)
	if response.StatusCode != http.StatusOK || len(providers.Items) != 2 || providers.Items[0].Available || providers.Items[1].Available {
		t.Fatalf("OAuth providers must fail closed without verified credentials: status=%d items=%#v", response.StatusCode, providers.Items)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/oauth/google/start", map[string]any{}, nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured OAuth start did not fail closed: %d", response.StatusCode)
	}

	resourceID := uuid.New()
	sourceKey := "http-generation:" + resourceID.String()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = notifications.CreateTx(context.Background(), tx, notifications.CreateInput{
		UserID: registered.User.ID, Kind: "generation.completed", Title: "Generation ready", Body: "Your generated Asset is ready.",
		TargetPath: "/workspace/assets/" + resourceID.String(), ResourceType: "generation", ResourceID: &resourceID, SourceKey: sourceKey,
	})
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	notificationRepository := notifications.NewRepository(pool)
	jobRepository := jobs.NewRepository(pool)
	job, err := jobRepository.Claim(context.Background(), "http-notification-worker", time.Minute)
	if err != nil || job.Kind != notifications.JobKind {
		t.Fatalf("claim HTTP notification delivery: job=%#v err=%v", job, err)
	}
	if err := notificationRepository.HandleDeliveryJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(context.Background(), job, "http-notification-worker"); err != nil {
		t.Fatal(err)
	}
	var notificationID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM notifications WHERE user_id=$1 AND source_key=$2`, registered.User.ID, sourceKey).Scan(&notificationID); err != nil {
		t.Fatal(err)
	}

	var inbox notifications.Page
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/notifications?readState=unread&kind=generation.completed", nil, &inbox)
	if response.StatusCode != http.StatusOK || inbox.UnreadCount != 1 || len(inbox.Items) != 1 || inbox.Items[0].TargetPath != "/workspace/assets/"+resourceID.String() {
		t.Fatalf("notification inbox contract failed: status=%d page=%#v", response.StatusCode, inbox)
	}
	var deliveries struct {
		Items []notifications.DeliveryEvidence `json:"items"`
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/notification-deliveries", nil, &deliveries)
	if response.StatusCode != http.StatusOK || len(deliveries.Items) != 1 || deliveries.Items[0].ID != notificationID || deliveries.Items[0].Status != "delivered" || deliveries.Items[0].Attempts != 1 || deliveries.Items[0].CompletedAt == nil {
		t.Fatalf("notification delivery evidence contract failed: status=%d items=%#v", response.StatusCode, deliveries.Items)
	}
	response = requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/notification-deliveries", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("notification delivery evidence allowed anonymous access: %d", response.StatusCode)
	}

	outsider := testHTTPClient(t)
	response = requestJSON(t, outsider, http.MethodPost, server.URL+"/api/v1/auth/register", map[string]any{
		"email": "http-outsider-" + uuid.NewString()[:8] + "@test.local", "password": "correct-horse-2026",
		"handle": "outsider_" + uuid.NewString()[:8], "displayName": "HTTP Outsider", "locale": "en-US", "timezone": "UTC",
	}, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("outsider registration failed: %d", response.StatusCode)
	}
	deliveries.Items = nil
	response = requestJSON(t, outsider, http.MethodGet, server.URL+"/api/v1/notification-deliveries", nil, &deliveries)
	if response.StatusCode != http.StatusOK || len(deliveries.Items) != 0 {
		t.Fatalf("notification delivery evidence crossed account boundary: status=%d items=%#v", response.StatusCode, deliveries.Items)
	}
	response = requestJSON(t, outsider, http.MethodPost, server.URL+"/api/v1/notifications/"+notificationID.String()+"/read", map[string]any{}, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("notification ownership leaked through HTTP status: %d", response.StatusCode)
	}

	var read notifications.Notification
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/notifications/"+notificationID.String()+"/read", map[string]any{}, &read)
	if response.StatusCode != http.StatusOK || read.ReadAt == nil {
		t.Fatalf("mark-read contract failed: status=%d item=%#v", response.StatusCode, read)
	}

	var preference notifications.Preference
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/notification-preferences/generation.completed", map[string]any{
		"inAppEnabled": false, "expectedVersion": 1,
	}, &preference)
	if response.StatusCode != http.StatusOK || preference.InAppEnabled || preference.Version != 2 {
		t.Fatalf("preference update contract failed: status=%d preference=%#v", response.StatusCode, preference)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/notification-preferences/generation.completed", map[string]any{
		"inAppEnabled": true, "expectedVersion": 1,
	}, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale preference version did not return conflict: %d", response.StatusCode)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/logout", map[string]any{}, nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout failed: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/auth/session", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked session remained authorized: %d", response.StatusCode)
	}
}

func requestJSON(t *testing.T, client *http.Client, method, target string, input, output any) *http.Response {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("User-Agent", "HCAI HTTP Contract Test")
	if method == http.MethodPost && (strings.Contains(target, "/community/posts") || strings.Contains(target, "/admin/finance/accounts/") && strings.HasSuffix(target, "/adjust")) {
		request.Header.Set("Idempotency-Key", uuid.NewString())
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatalf("decode %s %s: %v", method, target, err)
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	return response
}

func testHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func claimHTTPJobKind(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner, kind string) jobs.Job {
	t.Helper()
	jobRepository := jobs.NewRepository(pool)
	notificationRepository := notifications.NewRepository(pool)
	for attempt := 0; attempt < 20; attempt++ {
		job, err := jobRepository.Claim(ctx, owner, time.Minute)
		if err != nil {
			t.Fatalf("claim %s job: %v", kind, err)
		}
		if job.Kind == kind {
			return job
		}
		if job.Kind != notifications.JobKind {
			t.Fatalf("unexpected job %q while waiting for %q", job.Kind, kind)
		}
		if err := notificationRepository.HandleDeliveryJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if err := jobRepository.Complete(ctx, job, owner); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatalf("job %q unavailable after draining notifications", kind)
	return jobs.Job{}
}

func httpTestPool(t *testing.T) (*pgxpool.Pool, func()) {
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
	schema := "test_http_identity_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
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
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
