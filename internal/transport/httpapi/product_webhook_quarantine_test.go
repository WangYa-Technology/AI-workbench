package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/observability"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestProductWebhookQuarantineHTTPRevocationDuringRecheck(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	t.Cleanup(cleanup)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	traced, entered, release := testutil.GateQuery(t, pool, "SELECT event FROM product_webhook_quarantines WHERE id=$1")
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true, StripeEnabled: true, StripeSecretKey: "test-no-network", StripeBaseURL: "http://127.0.0.1:1", StripeAPIVersion: "2026-02-25.clover"}, traced, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	t.Cleanup(func() { release(); cancel() })
	client := testHTTPClient(t)
	actor := registerGovernanceUser(t, client, server.URL, "quarantine_revoked")
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	id, payment := uuid.New(), uuid.New()
	hash := strings.Repeat("a", 64)
	event, err := json.Marshal(map[string]any{"ProviderEventID": "evt_authority_http", "PayloadSHA256": hash, "LiveMode": false, "PaymentID": payment, "Supported": true, "Purpose": "product", "EventType": "checkout.session.completed", "ObjectType": "checkout.session", "ObjectID": "cs_private_http", "ProviderPaymentID": "pi_private_http", "AmountCents": 1900, "Currency": "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_webhook_quarantines(id,provider,provider_event_id,payload_sha256,live_mode,claimed_payment_id,event,rejection_code)
 VALUES($1,'stripe','evt_authority_http',$2,false,$3,$4,'payment_unknown')`, id, hash, payment, event); err != nil {
		t.Fatal(err)
	}
	post := server.URL + "/api/v1/admin/payments/webhook-quarantines/" + id.String() + "/recheck"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, post, strings.NewReader(`{"expectedVersion":1,"reason":"Original payment evidence needs rechecking."}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	type result struct {
		status int
		body   string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		response, err := client.Do(request)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		done <- result{response.StatusCode, string(body), err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("HTTP recheck did not reach its locked receipt", ctx.Err())
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	release()
	r := <-done
	if r.err != nil || r.status != 409 || !strings.Contains(r.body, "admin_state_conflict") || strings.Contains(r.body, "private_http") {
		t.Fatalf("unsafe or incorrect recheck response: %d %s %v", r.status, r.body, r.err)
	}
	if response := requestJSON(t, client, http.MethodPost, post, map[string]any{"expectedVersion": 1, "reason": "Retry after the permission change."}, nil); response.StatusCode != 403 {
		t.Fatal("revoked operator retried via HTTP", response.StatusCode)
	}
	var pending, checks, jobs int
	if err := pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM product_webhook_quarantines WHERE id=$1 AND state='pending' AND version=1 AND checked_at IS NULL),
 (SELECT count(*) FROM product_webhook_quarantine_checks),
 (SELECT count(*) FROM jobs WHERE kind=$2)`, id, payments.PaymentEventJobKind).Scan(&pending, &checks, &jobs); err != nil || pending != 1 || checks != 0 || jobs != 0 {
		t.Fatalf("rejected HTTP mutation changed evidence: %d %d %d %v", pending, checks, jobs, err)
	}
}

func TestProductWebhookQuarantineHTTP(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := t.Context()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true, StripeEnabled: true, StripeSecretKey: "test-no-network", StripeBaseURL: "http://127.0.0.1:1", StripeAPIVersion: "2026-02-25.clover"}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	guest, operator, member, second := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	admin := registerGovernanceUser(t, operator, server.URL, "quarantine_admin")
	registerGovernanceUser(t, member, server.URL, "quarantine_member")
	other := registerGovernanceUser(t, second, server.URL, "quarantine_second")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE users SET role='admin' WHERE id=ANY($1)`, []uuid.UUID{admin.ID, other.ID})
	var firstID uuid.UUID
	for i := range 3 {
		id, payment := uuid.New(), uuid.New()
		if i == 2 {
			firstID = id
		}
		eventID, hash := fmt.Sprintf("evt_review_%d", i), strings.Repeat(fmt.Sprint(i+1), 64)
		event, _ := json.Marshal(map[string]any{"ProviderEventID": eventID, "PayloadSHA256": hash, "LiveMode": false, "PaymentID": payment, "Supported": true, "Purpose": "product", "EventType": "checkout.session.completed", "ObjectType": "checkout.session", "ObjectID": "cs_private_remote", "ProviderPaymentID": "pi_original_remote", "AmountCents": 1900, "Currency": "USD"})
		exec(`INSERT INTO product_webhook_quarantines(id,provider,provider_event_id,payload_sha256,live_mode,claimed_payment_id,event,rejection_code,last_error_code,received_at)
 VALUES($1,'stripe',$2,$3,false,$4,$5,'payment_unknown','payment_unknown','2026-01-01'::timestamptz + $6 * interval '1 second')`, id, eventID, hash, payment, event, i)
	}
	base := server.URL + "/api/v1/admin/payments/webhook-quarantines"
	post := base + "/" + firstID.String() + "/recheck"
	if r := requestJSON(t, guest, "GET", base, nil, nil); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	if r := requestJSON(t, guest, "POST", post, map[string]any{}, nil); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	if r := requestJSON(t, member, "GET", base, nil, nil); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	if r := requestJSON(t, member, "POST", post, map[string]any{}, nil); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	var first payments.WebhookQuarantinePage
	if r := requestJSON(t, operator, "GET", base+"?limit=2", nil, &first); r.StatusCode != 200 || r.Header.Get("Cache-Control") != "private, no-store" || len(first.Items) != 2 || first.NextCursor == nil || first.Items[0].ID != firstID {
		t.Fatal(r.StatusCode, first)
	}
	raw, _ := json.Marshal(first)
	for _, private := range []string{"payloadSHA256", strings.Repeat("1", 64), "cs_private_remote", "pi_original_remote", "Supported", "event\"", "reason"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("internal evidence leaked", private)
		}
	}
	var tail payments.WebhookQuarantinePage
	if r := requestJSON(t, operator, "GET", base+"?limit=2&cursor="+url.QueryEscape(*first.NextCursor), nil, &tail); r.StatusCode != 200 || len(tail.Items) != 1 || tail.NextCursor != nil || tail.Items[0].ID == first.Items[1].ID {
		t.Fatal(r.StatusCode, tail)
	}
	for _, query := range []string{"limit=0", "limit=", "limit=51", "limit=1&limit=2", "state=pending&state=all", "unknown=1", "state=invalid", "mode=prod", "provider=unknown", "cursor=garbage", "state=all&cursor=" + url.QueryEscape(*first.NextCursor)} {
		if r := requestJSON(t, operator, "GET", base+"?"+query, nil, nil); r.StatusCode != 422 {
			t.Fatal(query, r.StatusCode)
		}
	}
	if r := requestJSON(t, second, "GET", base+"?cursor="+url.QueryEscape(*first.NextCursor), nil, nil); r.StatusCode != 422 {
		t.Fatal("cross-actor cursor", r.StatusCode)
	}
	for _, input := range []map[string]any{{"expectedVersion": 1, "reason": "short"}, {"expectedVersion": 0, "reason": "Original evidence was reviewed."}, {"expectedVersion": 1, "reason": strings.Repeat("核", 1001)}} {
		if r := requestJSON(t, operator, "POST", post, input, nil); r.StatusCode != 422 {
			t.Fatal(r.StatusCode)
		}
	}
	input := map[string]any{"expectedVersion": 1, "reason": strings.Repeat("核", 10)}
	metrics := observability.NewMetrics(time.Now())
	readAge := func() float64 {
		t.Helper()
		body, err := metrics.Render(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body, `hcai_product_webhook_quarantines{mode="test"} 3`) || !strings.Contains(body, `hcai_product_webhook_quarantines{mode="live"} 0`) || strings.Contains(body, firstID.String()) {
			t.Fatal("unsafe or missing durable quarantine metrics")
		}
		for _, line := range strings.Split(body, "\n") {
			const prefix = `hcai_product_webhook_quarantine_oldest_age_seconds{mode="test"} `
			if strings.HasPrefix(line, prefix) {
				var age float64
				if _, err = fmt.Sscan(strings.TrimPrefix(line, prefix), &age); err != nil || age <= 0 {
					t.Fatal(age, err)
				}
				return age
			}
		}
		t.Fatal("missing receipt age")
		return 0
	}
	beforeAge := readAge()
	var checked payments.WebhookQuarantine
	if r := requestJSON(t, operator, "POST", post, input, &checked); r.StatusCode != 200 || checked.State != "pending" || checked.Version != 2 || checked.LastErrorCode == nil || *checked.LastErrorCode != "payment_unknown" || r.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal(r.StatusCode, checked)
	}
	if readAge() < beforeAge {
		t.Fatal("recheck reset the age of unresolved evidence")
	}
	if r := requestJSON(t, operator, "POST", post, input, nil); r.StatusCode != 409 {
		t.Fatal("stale version", r.StatusCode)
	}
	if r := requestJSON(t, operator, "POST", base+"/"+uuid.NewString()+"/recheck", input, nil); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	exec(`UPDATE users SET role='member' WHERE id=$1`, admin.ID)
	if r := requestJSON(t, operator, "GET", base, nil, nil); r.StatusCode != 403 {
		t.Fatal("revoked permission", r.StatusCode)
	}
	input["expectedVersion"] = 2
	if r := requestJSON(t, operator, "POST", post, input, nil); r.StatusCode != 403 {
		t.Fatal("revoked mutation permission", r.StatusCode)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_webhook_quarantine_checks`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
