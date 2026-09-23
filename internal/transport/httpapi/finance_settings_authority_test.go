package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestFinanceSettingsHTTPRevocation(t *testing.T) {
	for _, action := range []string{"adjust", "destination_create", "destination_update", "provider"} {
		t.Run(action, func(t *testing.T) {
			pool, cleanup := httpTestPool(t)
			t.Cleanup(cleanup)
			gate := "SELECT balance_cents,reserved_cents FROM billing_accounts"
			if strings.HasPrefix(action, "destination_") {
				gate = "SELECT id,version,destination_id FROM payment_destinations"
			} else if action == "provider" {
				gate = "SELECT id,enabled,environment,merchant_id,store_id"
			}
			traced, entered, release := testutil.GateQuery(t, pool, gate)
			server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, traced, slog.New(slog.NewTextHandler(io.Discard, nil))))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			var workers sync.WaitGroup
			t.Cleanup(func() { release(); cancel(); workers.Wait() })
			client := testHTTPClient(t)
			actor := registerGovernanceUser(t, client, server.URL, "settings_http")
			user := uuid.New()
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`UPDATE users SET role='admin' WHERE id=$1`, actor.ID)
			exec(`INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Settings recipient')`, user, user.String()+"@test.local", "recipient_"+user.String()[:8])
			method, path := http.MethodPost, "/api/v1/admin/finance/accounts/"+user.String()+"/adjust"
			body := `{"deltaCents":500,"currency":"USD"}`
			status, code := http.StatusForbidden, "permission_required"
			if strings.HasPrefix(action, "destination_") {
				method, path = http.MethodPut, "/api/v1/admin/payment-destinations/"+user.String()
				body = `{"destinationId":"acct_changed","enabled":true,"expectedVersion":0}`
				status, code = http.StatusConflict, "admin_state_conflict"
				if action == "destination_update" {
					exec(`INSERT INTO payment_destinations(provider,user_id,destination_id,account_type,status,charges_enabled,payouts_enabled,details_submitted,requirements_due,admin_disabled,verified_at) VALUES('stripe',$1,'acct_existing','manual','verified',true,true,true,false,false,now())`, user)
					body = `{"destinationId":"acct_changed","enabled":true,"expectedVersion":1}`
				}
			} else if action == "provider" {
				method, path = http.MethodPut, "/api/v1/admin/payment-providers/stripe"
				body = `{"merchantId":"acct_changed"}`
			}
			snapshot := func() string {
				t.Helper()
				var value string
				if err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
 'configs',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.provider) FROM payment_provider_configs c),
 'destinations',(SELECT jsonb_agg(to_jsonb(d) ORDER BY d.id) FROM payment_destinations d),
 'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.user_id) FROM billing_accounts a),
 'entries',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM billing_entries e),
 'audit',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM audit_events e WHERE e.action IN ('admin.finance_adjusted','admin.payment_destination_updated','admin.payment_provider_config_updated')))::text`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			before := snapshot()
			type result struct {
				status int
				body   string
				err    error
			}
			request := func() result {
				req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
				if err != nil {
					return result{err: err}
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "settings-http-key")
				resp, err := client.Do(req)
				if err != nil {
					return result{err: err}
				}
				defer resp.Body.Close()
				raw, err := io.ReadAll(resp.Body)
				return result{resp.StatusCode, string(raw), err}
			}
			done := make(chan result, 1)
			workers.Add(1)
			go func() { defer workers.Done(); done <- request() }()
			select {
			case <-entered:
			case r := <-done:
				t.Fatalf("HTTP command did not reach gate: %#v", r)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			exec(`UPDATE users SET role='member' WHERE id=$1`, actor.ID)
			release()
			if r := <-done; r.err != nil || r.status != status || !strings.Contains(r.body, code) || strings.Contains(r.body, "acct_changed") {
				t.Fatalf("in-flight revoked command not safely rejected: %#v", r)
			}
			if r := request(); r.err != nil || r.status != http.StatusForbidden {
				t.Fatalf("fresh revoked command accepted: %#v", r)
			}
			if before != snapshot() {
				t.Fatal("revoked HTTP command changed finance state")
			}
		})
	}
}
