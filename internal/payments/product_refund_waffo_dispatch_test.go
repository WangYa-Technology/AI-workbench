package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type waffoRefundFixture struct {
	pool                      *pgxpool.Pool
	service                   *Service
	checkout                  Checkout
	buyer, product, operation uuid.UUID
	calls                     atomic.Int32
	checkoutCalls             atomic.Int32
	mode                      string
}

func newWaffoRefundFixture(t *testing.T, mode string) *waffoRefundFixture {
	t.Helper()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	f := &waffoRefundFixture{pool: pool, mode: mode}
	f.buyer, _, _, f.product = newProductCheckoutFixture(t, pool)
	connector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, "invalid", 400)
			return
		}
		switch r.URL.Path {
		case "/webhook/verify":
			if r.Header.Get("x-waffo-signature") != "fixture-signature" {
				http.Error(w, "bad signature", 401)
				return
			}
			hash := sha256.Sum256(raw)
			_ = json.NewEncoder(w).Encode(map[string]any{"verification": map[string]any{"contractVersion": waffoWebhookContractVersion, "environment": "test", "payloadSHA256": hex.EncodeToString(hash[:])}})
		case "/checkout/identity":
			_ = json.NewEncoder(w).Encode(ProductCheckoutIdentity{Provider: "waffo_pancake", MerchantID: "MER_dispatch", StoreID: "STO_dispatch", APIVersion: waffoProductCheckoutAPI, RequestVersion: waffoProductCheckoutVersion})
		case "/checkout":
			f.checkoutCalls.Add(1)
			var reserved bool
			if err := pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM product_checkout_dispatches WHERE payment_id=$1)`, body["paymentId"]).Scan(&reserved); err != nil || !reserved {
				t.Errorf("checkout dispatched without committed evidence: %t %v", reserved, err)
			}
			if mode == "checkout_lost" {
				http.Error(w, "response lost", 502)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"providerId": "CHK_dispatch", "checkoutUrl": "https://checkout.waffo.ai/session/CHK_dispatch", "status": "open", "paymentStatus": "pending", "expiresAt": time.Now().Add(time.Hour), "liveMode": false, "checkoutIdentity": body["checkoutIdentity"]})
		case "/refund":
			f.calls.Add(1)
			// A separate connection must observe the committed reservation BEFORE
			// anything at the remote financial boundary can happen.
			var reserved bool
			if err := pool.QueryRow(r.Context(), `SELECT reserved_at IS NOT NULL AND responded_at IS NULL FROM product_refund_dispatches WHERE operation_id=$1`, body["operationId"]).Scan(&reserved); err != nil || !reserved {
				t.Errorf("dispatch before durable reservation: %t %v", reserved, err)
			}
			if strings.HasPrefix(mode, "lost") {
				http.Error(w, "response lost", 502)
				return
			}
			if mode == "rate_limited" {
				http.Error(w, "waffo_request_failed", http.StatusTooManyRequests)
				return
			}
			result := map[string]any{"providerId": "TKT_dispatch", "providerPaymentId": "PAY_dispatch", "amountCents": 1900, "currency": "USD", "status": "pending", "operationId": body["operationId"], "refundContractVersion": body["refundContractVersion"], "paymentIdentity": body["paymentIdentity"]}
			if mode == "mismatch" {
				result["operationId"] = uuid.NewString()
			}
			if mode == "ticket_succeeded" {
				result["status"] = "succeeded"
			}
			_ = json.NewEncoder(w).Encode(result)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(connector.Close)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_configs(provider,enabled,environment,merchant_id,store_id,product_id_onetime) VALUES('waffo_pancake',true,'test','MER_dispatch','STO_dispatch','PROD_dispatch')`); err != nil {
		t.Fatal(err)
	}
	runtime := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: connector.URL, ConnectorToken: "dispatch-test-token", Environment: "test"})
	f.service = newPaymentTestService(t, pool, ServiceConfig{Enabled: true, Provider: "waffo_pancake", WaffoMerchantID: "MER_dispatch", WaffoEnvironment: "test", WaffoWebhookURL: connector.URL, WaffoConnectorToken: "dispatch-test-token"}, NewRuntimeCatalog(runtime))
	var err error
	f.checkout, _, err = f.service.BeginProductCheckout(ctx, f.buyer, f.product, "waffo-dispatch-checkout", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, f.product))
	if mode == "checkout_lost" {
		if err == nil {
			t.Fatal("expected lost checkout response")
		}
		f.checkout, err = scanProductCheckout(pool.QueryRow(ctx, productCheckoutSelect+` WHERE p.payer_id=$1`, f.buyer))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	if err != nil {
		t.Fatal(err)
	}
	if mode == "await_payment" {
		return f
	}
	f.event(t, "order.completed", uuid.Nil)
	if mode == "legacy" {
		f.operation = uuid.New()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err = tx.Exec(ctx, `UPDATE orders SET status='refund_requested',refund_operation_id=$2,refund_requested_at=now(),refund_correlation_enabled=true WHERE id=$1`, f.checkout.OrderID, f.operation); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE payment_intents SET status='refund_pending' WHERE id=$1`, f.checkout.PaymentID); err != nil {
			t.Fatal(err)
		}
		if err = RecordProductRefundAttemptTx(ctx, tx, f.checkout.PaymentID); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,jsonb_build_object('paymentId',$2::text,'operationId',$3::text))`, ProductRefundJobKind, f.checkout.PaymentID, f.operation); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err = f.service.BeginProductRefund(ctx, f.buyer, f.checkout.OrderID, "waffo-refund-command", "test", "The content does not match the description."); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT refund_operation_id FROM orders WHERE id=$1`, f.checkout.OrderID).Scan(&f.operation); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *waffoRefundFixture) event(t *testing.T, kind string, operation uuid.UUID) {
	t.Helper()
	purpose, currency, succeeded, remote := "product", "USD", "succeeded", "PAY_dispatch"
	amount := int64(1900)
	objectID, objectType, status := remote, "order", "succeeded"
	if strings.HasPrefix(kind, "refund.") {
		objectID = operation.String()
		objectType = "refund"
		if kind == "refund.failed" {
			status = "failed"
		}
	}
	e := minimizedProviderEvent{WaffoVerificationVersion: waffoWebhookContractVersion, WaffoStoreID: "STO_dispatch", WaffoOrderExternalID: f.checkout.OrderID.String(), WaffoBuyerIdentity: f.buyer.String(), ProviderEventID: "delivery_" + uuid.NewString(), EventType: kind, APIVersion: "waffo-pancake-v1", OccurredAt: time.Now().UTC(), PayloadSHA256: strings.Repeat("a", 64), ObjectID: objectID, ObjectType: objectType, PaymentID: &f.checkout.PaymentID, ResourceID: &f.product, Purpose: &purpose, AmountCents: &amount, Currency: &currency, PaymentStatus: &succeeded, ProviderPaymentID: &remote, Supported: true}
	if objectType == "refund" {
		e.PaymentStatus = &status
	}
	receipt, err := f.service.receiveProviderEvent(context.Background(), "waffo_pancake", e)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"eventId": receipt.EventID})
	if err = f.service.HandlePaymentEventJob(context.Background(), jobs.Job{Kind: PaymentEventJobKind, Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

func TestWaffoRefundDispatchUncertainty(t *testing.T) {
	for _, mode := range []string{"lost", "lost_failed", "mismatch", "legacy", "crash_before_send", "concurrent", "ticket_succeeded", "rate_limited"} {
		t.Run(mode, func(t *testing.T) {
			f := newWaffoRefundFixture(t, mode)
			ctx := context.Background()
			job := currentProductRefundJob(t, f.pool, f.checkout.PaymentID)
			if mode == "crash_before_send" {
				tx, err := f.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(ctx) }()
				if _, err = tx.Exec(ctx, `SELECT pi.id FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1 FOR UPDATE OF pi,o`, f.checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				if _, err = reserveWaffoRefundTx(ctx, tx, f.checkout.PaymentID, f.operation); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				// No call is issued: simulate losing the process after durable reservation.
			}
			var errs []error
			if mode == "concurrent" {
				var wg sync.WaitGroup
				out := make(chan error, 5)
				for i := 0; i < 5; i++ {
					wg.Add(1)
					go func() { defer wg.Done(); out <- f.service.HandleProductRefundJob(ctx, job) }()
				}
				wg.Wait()
				close(out)
				for err := range out {
					errs = append(errs, err)
				}
			} else {
				errs = append(errs, f.service.HandleProductRefundJob(ctx, job))
			}
			uncertain := mode != "concurrent" && mode != "ticket_succeeded"
			if uncertain && errs[0] == nil {
				t.Fatal("uncertain dispatch reported complete")
			}
			if mode == "legacy" {
				down, err := os.ReadFile("../platform/database/migrations/0107_product_refund_dispatch.down.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard product refund dispatch provenance") {
					t.Fatalf("rollback removed legacy uncertainty guard: %v", err)
				}
			}
			for _, err := range errs {
				if !uncertain && err != nil && !strings.Contains(err.Error(), "payment_reconciliation_required") {
					t.Fatal(err)
				}
			}
			// Repeated jobs use the same original operation and never call again.
			for i := 0; i < 3; i++ {
				err := f.service.HandleProductRefundJob(ctx, job)
				if uncertain && (err == nil || !strings.Contains(err.Error(), "payment_reconciliation_required")) {
					t.Fatalf("retry should require reconciliation: %v", err)
				}
			}
			wantCalls := int32(1)
			if mode == "legacy" || mode == "crash_before_send" {
				wantCalls = 0
			}
			if f.calls.Load() != wantCalls {
				t.Fatalf("remote ticket created %d times; want %d", f.calls.Load(), wantCalls)
			}
			var review bool
			if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=$1)`, f.checkout.PaymentID).Scan(&review); err != nil || review != uncertain {
				t.Fatalf("review projection %t %v", review, err)
			}
			history, err := f.service.RefundHistory(ctx, f.checkout.PaymentID, "", 20)
			if err != nil || len(history.Items) != 1 || history.Items[0].ReconciliationRequired != uncertain {
				t.Fatalf("history did not expose uncertainty: %#v %v", history, err)
			}
			var payment, order string
			var rights int
			if err = f.pool.QueryRow(ctx, `SELECT pi.status,o.status,(SELECT count(*) FROM entitlements WHERE order_id=o.id AND status='active') FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1`, f.checkout.PaymentID).Scan(&payment, &order, &rights); err != nil {
				t.Fatal(err)
			}
			if payment != "refund_pending" || order != "refund_requested" || rights != 1 {
				t.Fatalf("ticket response falsely settled money/rights: %s %s %d", payment, order, rights)
			}
			if mode == "lost" {
				pkg, body := runProductExport(t, f.pool, f.buyer)
				attempts := pkg.Data.Marketplace.Data["refundAttempts"]
				if len(attempts) != 1 || attempts[0]["reconciliationRequired"] != true {
					t.Fatalf("export omits uncertainty: %#v", attempts)
				}
				dispatch, ok := attempts[0]["dispatch"].(map[string]any)
				if !ok || dispatch["reservedAt"] == nil || dispatch["respondedAt"] != nil || dispatch["contractVersion"] != waffoRefundContractVersion {
					t.Fatalf("missing durable dispatch provenance: %#v", dispatch)
				}
				if strings.Contains(string(body), "dispatch-test-token") {
					t.Fatal("connector credential leaked")
				}
			}
			if mode == "lost_failed" {
				f.event(t, "refund.failed", f.operation)
				if _, err = f.service.BeginProductRefund(ctx, f.buyer, f.checkout.OrderID, "retry-after-authenticated-failure", "test", "The content still does not match its description."); err != nil {
					t.Fatal(err)
				}
				if err = f.service.HandleProductRefundJob(ctx, currentProductRefundJob(t, f.pool, f.checkout.PaymentID)); err == nil {
					t.Fatal("expected simulated response loss on fresh attempt")
				}
				if f.calls.Load() != 2 {
					t.Fatal("authenticated failure did not permit a distinct new operation")
				}
				return
			}
			// An authenticated late financial event is still allowed to settle the
			// original operation even when no ticket response was recorded locally.
			f.event(t, "refund.succeeded", f.operation)
			if err = f.service.HandleProductRefundJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if f.calls.Load() != wantCalls {
				t.Fatal("late success triggered another dispatch")
			}
			if err = f.pool.QueryRow(ctx, `SELECT pi.status,o.status,(SELECT count(*) FROM entitlements WHERE order_id=o.id AND status='active') FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1`, f.checkout.PaymentID).Scan(&payment, &order, &rights); err != nil {
				t.Fatal(err)
			}
			if payment != "refunded" || order != "refunded" || rights != 0 {
				t.Fatalf("late success not settled: %s %s %d", payment, order, rights)
			}
		})
	}
}

func TestWaffoRefundPermitEvidence(t *testing.T) {
	f := newWaffoRefundFixture(t, "pending")
	ctx := context.Background()
	// An existing operation cannot be upgraded or granted another permit.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = RecordNewProductRefundAttemptTx(ctx, tx, f.checkout.PaymentID, f.operation); err == nil {
		t.Fatal("existing operation upgraded")
	}
	_ = tx.Rollback(ctx)
	if err = f.service.HandleProductRefundJob(ctx, currentProductRefundJob(t, f.pool, f.checkout.PaymentID)); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE product_refund_dispatches SET reserved_at=NULL WHERE operation_id=$1`,
		`UPDATE product_refund_dispatches SET provider_refund_id='TKT_forged' WHERE operation_id=$1`,
		`UPDATE product_refund_dispatches SET responded_at=NULL,provider_refund_id=NULL WHERE operation_id=$1`,
		`DELETE FROM product_refund_dispatches WHERE operation_id=$1`,
	} {
		if _, err = f.pool.Exec(ctx, sql, f.operation); err == nil {
			t.Fatalf("rewrote dispatch evidence: %s", sql)
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0107_product_refund_dispatch.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard product refund dispatch provenance") {
		t.Fatalf("unsafe rollback: %v", err)
	}
	f.event(t, "refund.failed", f.operation)
	if _, err = f.service.BeginProductRefund(ctx, f.buyer, f.checkout.OrderID, "fresh-after-confirmed-failure", "test", "The content still does not match its description."); err != nil {
		t.Fatal(err)
	}
	var permits int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM product_refund_dispatches d JOIN product_refund_attempts a USING(operation_id) WHERE a.payment_id=$1`, f.checkout.PaymentID).Scan(&permits); err != nil || permits != 2 {
		t.Fatalf("new confirmed-failure retry lacks separate permit: %d %v", permits, err)
	}
}

func TestWaffoRefundReservationRechecksVersion(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			f := newWaffoRefundFixture(t, "pending")
			ctx := context.Background()
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err = tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, f.checkout.PaymentID); err != nil {
				t.Fatal(err)
			}
			version, err := reserveWaffoRefundTx(ctx, tx, f.checkout.PaymentID, f.operation)
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if changed {
				if _, err = f.pool.Exec(ctx, `UPDATE payment_intents SET version=version+1 WHERE id=$1`, f.checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
			}
			next, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = next.Rollback(ctx) }()
			err = lockWaffoRefundReservationTx(ctx, next, f.checkout.PaymentID, f.operation, version)
			if (err != nil) != changed {
				t.Fatalf("version guard: %v", err)
			}
		})
	}
}
