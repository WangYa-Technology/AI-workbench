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
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminRefundCheckHTTPRevocation(t *testing.T) {
	for _, phase := range []string{"before_transaction", "locked_payment"} {
		for _, change := range []string{"role", "suspended", "permission"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				pool, cleanup := httpTestPool(t)
				t.Cleanup(cleanup)
				query := "begin isolation level serializable"
				if phase == "locked_payment" {
					query = "SELECT EXISTS(SELECT 1 FROM product_payment_identity_gaps WHERE payment_id=$1)"
				}
				traced, entered, release := testutil.GateQuery(t, pool, query)
				cfg := config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
					StripeEnabled: true, StripeSecretKey: "test-no-network", StripeBaseURL: "http://127.0.0.1:1", StripeAPIVersion: "2026-02-25.clover"}
				// Registration uses a different handler so its own transactions cannot
				// trip the payment command gate. Session cookies are shared by host.
				login := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
				t.Cleanup(login.Close)
				client := testHTTPClient(t)
				actor := registerGovernanceUser(t, client, login.URL, "refund_authority")
				server := httptest.NewServer(httpapi.New(cfg, traced, slog.New(slog.NewTextHandler(io.Discard, nil))))
				t.Cleanup(server.Close)
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				var workers sync.WaitGroup
				t.Cleanup(func() { release(); cancel(); workers.Wait() })
				exec := func(query string, args ...any) {
					t.Helper()
					if _, err := pool.Exec(ctx, query, args...); err != nil {
						t.Fatal(err)
					}
				}
				exec(`UPDATE users SET role='admin' WHERE id=$1`, actor.ID)
				buyer, seller, asset, product, order, payment, job, check := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
				for _, id := range []uuid.UUID{buyer, seller} {
					exec(`INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Refund HTTP fixture')`, id, id.String()+"@test.local", "refund_"+id.String()[:8])
				}
				exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
 VALUES($1,$2,'image','Refund source','/media/refund.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1')`, asset, seller)
				exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Refund product','Refund authority fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller, asset)
				exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot)
 VALUES($1,$2,$3,1900,'USD','fulfilled',now(),'refund-authority-order','1.0','Original license','Refund product','Commercial Standard',14)`, order, buyer, product)
				exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','paid',false,'refund-authority-payment','pi_refund_authority')`, payment, buyer, seller, product, order)
				exec(`INSERT INTO product_checkout_requests(payment_id,identity,request)
 SELECT pi.id,jsonb_build_object('provider',pi.provider,'merchantId','acct_fixture','liveMode',pi.live_mode,
 'endpoint','https://api.stripe.com/v1','apiVersion','2026-02-25.clover','requestVersion','stripe-product-checkout-v1'),
 jsonb_build_object('PaymentID',pi.id,'Purpose','product','ResourceID',pi.resource_id,'OrderExternalID',pi.order_id,
 'BuyerIdentity',pi.payer_id,'BuyerEmail',u.email,'AmountCents',pi.amount_cents,'Currency',pi.currency)
 FROM payment_intents pi JOIN users u ON u.id=pi.payer_id WHERE pi.id=$1`, payment)
				exec(`INSERT INTO jobs(id,kind,status,payload) VALUES($1,$2,'failed',jsonb_build_object('checkId',$3::text))`, job, payments.ProductRefundCheckJobKind, check)
				exec(`INSERT INTO product_refund_checks(id,payment_id,requested_by,job_id,status,origin) VALUES($1,$2,$3,$4,'requested','operator')`, check, payment, actor.ID, job)
				snapshot := func() string {
					t.Helper()
					var value string
					if err := pool.QueryRow(ctx, `SELECT jsonb_build_object('payment',to_jsonb(pi),
 'checks',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM product_refund_checks c),
 'jobs',(SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id) FROM jobs j),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM audit_events a))::text FROM payment_intents pi WHERE pi.id=$1`, payment).Scan(&value); err != nil {
						t.Fatal(err)
					}
					return value
				}
				before := snapshot()
				path := server.URL + "/api/v1/admin/payments/" + payment.String() + "/refund-checks"
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(`{"expectedVersion":1}`))
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
				workers.Add(1)
				go func() {
					defer workers.Done()
					r, err := client.Do(request)
					if err != nil {
						done <- result{err: err}
						return
					}
					defer r.Body.Close()
					body, err := io.ReadAll(r.Body)
					done <- result{r.StatusCode, string(body), err}
				}()
				select {
				case <-entered:
				case r := <-done:
					t.Fatalf("request missed authorization gate: %#v", r)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				switch change {
				case "role":
					exec(`UPDATE users SET role='member' WHERE id=$1`, actor.ID)
				case "suspended":
					exec(`UPDATE users SET status='suspended' WHERE id=$1`, actor.ID)
				case "permission":
					exec(`DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:finance'`)
				}
				release()
				r := <-done
				want, code := 403, "forbidden"
				if phase == "locked_payment" {
					want, code = 409, "admin_state_conflict"
				}
				if r.err != nil || r.status != want || !strings.Contains(r.body, code) || strings.Contains(r.body, "pi_refund_authority") {
					t.Errorf("revoked command was not safely rejected: %#v, want %d/%s", r, want, code)
				}
				if snapshot() != before {
					t.Error("revoked request changed payment, prior check, jobs or audit")
				}
			})
		}
	}
}
