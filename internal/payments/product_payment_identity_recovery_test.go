package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func identityRecoveryJob(t *testing.T, pool *pgxpool.Pool, paymentID, actorID uuid.UUID) jobs.Job {
	t.Helper()
	var job jobs.Job
	if err := pool.QueryRow(context.Background(), `INSERT INTO jobs(kind,payload,max_attempts)
 VALUES($1,jsonb_build_object('paymentId',$2::text,'actorId',$3::text),20) RETURNING id,kind,payload`, ProductIdentityRecoveryJobKind, paymentID, actorID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestLegacyProductIdentityRecovery(t *testing.T) {
	for _, session := range []bool{false, true} {
		name := "payment_intent"
		if session {
			name = "session"
		}
		t.Run(name, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, &durableProductRefundRuntime{})
			if !session {
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_checkout_id=NULL WHERE id=$1`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
			}
			binding, err := readProductPaymentBinding(ctx, pool, checkout.PaymentID, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `TRUNCATE product_checkout_dispatches, product_checkout_requests`); err != nil {
				t.Fatal(err)
			}
			var seller uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT payee_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&seller); err != nil {
				t.Fatal(err)
			}
			funds, err := service.GetSellerFunds(ctx, seller)
			if err != nil || funds.UnresolvedRecords == 0 || len(funds.Accounts) != 0 {
				t.Fatalf("missing identity was classified: %+v %v", funds, err)
			}
			market := marketplace.NewService(pool)
			order, err := market.GetOrder(ctx, buyer, checkout.OrderID)
			if err != nil || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" {
				t.Fatalf("legacy refund capability: %#v %v", order, err)
			}
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "before-recovery", "test", "The delivered content is not usable."); !errors.Is(err, ErrRefundConflict) {
				t.Fatalf("accepted unbound refund %v", err)
			}
			history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
			if err != nil || history.CanCheck {
				t.Fatalf("unbound query advertised: %#v %v", history, err)
			}
			job := identityRecoveryJob(t, pool, checkout.PaymentID, buyer)
			var reads, mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				if r.Method != http.MethodGet {
					mutations.Add(1)
					http.Error(w, "no money operations", http.StatusBadRequest)
					return
				}
				if r.Header.Get("Authorization") != "Bearer identity-recovery-test-key" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
					http.Error(w, "bad auth", 401)
					return
				}
				metadata := map[string]string{"hcai_payment_id": binding.PaymentID.String(), "hcai_resource_id": binding.ResourceID.String(), "hcai_purpose": "product"}
				switch {
				case r.URL.Path == "/account":
					json.NewEncoder(w).Encode(map[string]any{"id": "acct_workflow", "object": "account"})
				case r.URL.Path == "/balance":
					json.NewEncoder(w).Encode(map[string]any{"object": "balance", "livemode": false})
				case r.URL.Path == "/checkout/sessions/"+binding.ProviderCheckoutID && session:
					json.NewEncoder(w).Encode(map[string]any{"id": binding.ProviderCheckoutID, "object": "checkout.session", "mode": "payment", "status": "complete", "payment_status": "paid", "payment_intent": binding.ProviderPaymentID, "amount_total": binding.AmountCents, "currency": "usd", "livemode": false, "expires_at": time.Now().Unix(), "client_reference_id": binding.PaymentID.String(), "metadata": metadata})
				case r.URL.Path == "/payment_intents/"+binding.ProviderPaymentID:
					json.NewEncoder(w).Encode(map[string]any{"id": binding.ProviderPaymentID, "object": "payment_intent", "status": "succeeded", "amount": binding.AmountCents, "amount_received": binding.AmountCents, "currency": "usd", "livemode": false, "latest_charge": "ch_recovered123", "metadata": metadata})
				case r.URL.Path == "/refunds":
					json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}, "has_more": false})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			runtime := originalMerchantHTTPRuntime(t, server, "identity-recovery-test-key")
			service.runtimes = NewRuntimeCatalog(runtime)
			if err := service.HandleProductIdentityRecoveryJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			funds, err = service.GetSellerFunds(ctx, seller)
			if err != nil || funds.UnresolvedRecords != 0 || len(funds.Accounts) != 1 || funds.Accounts[0].Provider != "stripe" || funds.Accounts[0].Environment != "test" {
				t.Fatalf("authenticated recovered identity not classified: %+v %v", funds, err)
			}
			after := reads.Load()
			if err := service.HandleProductIdentityRecoveryJob(ctx, job); err != nil || reads.Load() != after {
				t.Fatalf("replay performed another query %v", err)
			}
			assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			order, err = market.GetOrder(ctx, buyer, checkout.OrderID)
			if err != nil || order.CanRequestRefund {
				t.Fatalf("identity proof bypassed funds query: %v", err)
			}
			var body string
			if err := pool.QueryRow(ctx, `SELECT row_to_json(r)::text FROM product_payment_identity_recoveries r WHERE payment_id=$1`, checkout.PaymentID).Scan(&body); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(body, "identity-recovery-test-key") || strings.Contains(body, "BuyerEmail") {
				t.Fatal("recovery persisted secret or fabricated customer")
			}
			for _, query := range []string{`UPDATE product_payment_identity_recoveries SET identity='{}' WHERE payment_id=$1`, `DELETE FROM product_payment_identity_recoveries WHERE payment_id=$1`} {
				if _, err := pool.Exec(ctx, query, checkout.PaymentID); err == nil {
					t.Fatal("mutable recovery evidence")
				}
			}
			down, err := os.ReadFile("../platform/database/migrations/0086_product_payment_identity_recovery.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard product merchant recovery") {
				t.Fatalf("rollback lost evidence or failed for wrong reason: %v", err)
			}
			var checkJob jobs.Job
			if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.payment_id=$1`, checkout.PaymentID).Scan(&checkJob.ID, &checkJob.Kind, &checkJob.Payload); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleProductRefundCheckJob(ctx, checkJob); err != nil {
				t.Fatal(err)
			}
			order, err = market.GetOrder(ctx, buyer, checkout.OrderID)
			if err != nil || !order.CanRequestRefund {
				t.Fatalf("verified funds did not release capability %#v %v", order, err)
			}
			if _, _, err := verifyProductPaymentIdentity(ctx, pool, runtime, checkout.PaymentID); err != nil {
				t.Fatalf("recovered identity unusable %v", err)
			}
			// Recovery must not manufacture a replayable checkout request.
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			identity, _ := runtime.ProductCheckoutIdentity(ctx)
			_, err = loadProductCheckoutRequestTx(ctx, tx, checkout, identity)
			_ = tx.Rollback(ctx)
			if !errors.Is(err, ErrCheckoutReconciliation) {
				t.Fatalf("identity allowed new checkout replay: %v", err)
			}
			if mutations.Load() != 0 {
				t.Fatal("identity/funds recovery dispatched a money operation")
			}
			// A subsequent legitimate refund uses the recovered merchant and preserves
			// its operation key. A different current merchant is still rejected.
			base := &durableProductRefundRuntime{}
			bound := &merchantBoundRuntime{ProviderRuntime: base, identity: identity}
			service.runtimes = NewRuntimeCatalog(bound)
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "after-recovery", "test", "The delivered content is not usable."); err != nil {
				t.Fatal(err)
			}
			bound.identity.MerchantID = "acct_other"
			refundJob := currentProductRefundJob(t, pool, checkout.PaymentID)
			if err := service.HandleProductRefundJob(ctx, refundJob); !errors.Is(err, ErrCheckoutReconciliation) || bound.refundCalls != 0 {
				t.Fatalf("wrong recovered merchant dispatched %v", err)
			}
			bound.identity = identity
			if err := service.HandleProductRefundJob(ctx, refundJob); err != nil || bound.refundCalls != 1 {
				t.Fatalf("recovered refund dispatch %v", err)
			}
			if bound.lastRefund.BuyerIdentity != buyer.String() || bound.lastRefund.BuyerEmail != "" {
				t.Fatal("legacy refund invented historical customer")
			}
			exported, packageBody := runProductExport(t, pool, buyer)
			recoveries := exported.Data.Marketplace.Data["identityRecoveries"]
			if len(recoveries) != 1 || recoveries[0]["paymentId"] != checkout.PaymentID.String() || recoveries[0]["merchantId"] != "acct_workflow" {
				t.Fatal("export omitted verified historical identity")
			}
			if len(exported.Data.Marketplace.Data["checkoutRequests"]) != 0 || strings.Contains(string(packageBody), "identity-recovery-test-key") || strings.Contains(string(packageBody), identity.Endpoint) {
				t.Fatal("export invented an original request or exposed provider internals")
			}
		})
	}
}

type recoveryReadRuntime struct {
	refundReadRuntime
	read func(context.Context, ProductPaymentBinding) (ProductPaymentIdentityObservation, error)
}

func (r *recoveryReadRuntime) ReadProductPaymentIdentity(ctx context.Context, b ProductPaymentBinding) (ProductPaymentIdentityObservation, error) {
	return r.read(ctx, b)
}
func recoveredPaymentObservation(b ProductPaymentBinding) ProductPaymentIdentityObservation {
	if b.ProviderChargeID == "" {
		b.ProviderChargeID = "ch_recovered123"
	}
	return ProductPaymentIdentityObservation{Payment: &StripeProductPaymentObservation{ProviderPaymentID: b.ProviderPaymentID, ProviderChargeID: b.ProviderChargeID, AmountCents: b.AmountCents, Currency: b.Currency, LiveMode: b.LiveMode, Status: "succeeded"}}
}

func TestLegacyIdentityRecoveryRejectsUncertainEvidence(t *testing.T) {
	for _, scenario := range []string{"remote_failure", "invalid_observation", "changed_during_query", "forged_job", "unknown_refund", "funds_query_failed"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			runtime := &recoveryReadRuntime{}
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
			if _, err := pool.Exec(ctx, `TRUNCATE product_checkout_dispatches, product_checkout_requests; UPDATE payment_intents SET provider_checkout_id=NULL WHERE purpose='product'`); err != nil {
				t.Fatal(err)
			}
			job := identityRecoveryJob(t, pool, checkout.PaymentID, buyer)
			runtime.read = func(_ context.Context, b ProductPaymentBinding) (ProductPaymentIdentityObservation, error) {
				o := recoveredPaymentObservation(b)
				switch scenario {
				case "remote_failure":
					return ProductPaymentIdentityObservation{}, newProviderFailure("payment_authentication", 0)
				case "invalid_observation":
					o.Payment.ProviderPaymentID = "pi_other"
				case "changed_during_query":
					if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_payment_id='pi_changed_during_query' WHERE id=$1`, b.PaymentID); err != nil {
						t.Fatal(err)
					}
				case "forged_job":
					t.Error("forged job reached provider")
				case "unknown_refund":
					runtime.observations = []RefundObservation{{ProviderID: "re_unknown", ProviderPaymentID: b.ProviderPaymentID, AmountCents: b.AmountCents, Currency: b.Currency, Status: "succeeded"}}
				case "funds_query_failed":
					runtime.readError = newProviderFailure("payment_authentication", 0)
				}
				return o, nil
			}
			if scenario == "forged_job" {
				job.ID = uuid.New()
			}
			err := service.HandleProductIdentityRecoveryJob(ctx, job)
			fundsStage := scenario == "unknown_refund" || scenario == "funds_query_failed"
			if !fundsStage && err == nil {
				t.Fatal("uncertain identity accepted")
			}
			if fundsStage && err != nil {
				t.Fatal(err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_payment_identity_recoveries WHERE payment_id=$1`, checkout.PaymentID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (count == 1) != fundsStage {
				t.Fatal("wrong recovery evidence count", count)
			}
			if fundsStage {
				var checkJob jobs.Job
				if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.payment_id=$1`, checkout.PaymentID).Scan(&checkJob.ID, &checkJob.Kind, &checkJob.Payload); err != nil {
					t.Fatal(err)
				}
				err := service.HandleProductRefundCheckJob(ctx, checkJob)
				if scenario == "funds_query_failed" && err == nil {
					t.Fatal("funds query error lost")
				}
				if scenario == "unknown_refund" && err != nil {
					t.Fatal(err)
				}
			}
			order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
			if err != nil || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" {
				t.Fatalf("unsafe recovery advertised refund %#v %v", order, err)
			}
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "unsafe-recovery", "test", "The delivered content is not usable."); !errors.Is(err, ErrRefundConflict) {
				t.Fatal("unsafe recovery accepted refund", err)
			}
			assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
		})
	}
}

func TestLegacyIdentityRecoveryResumesExpiredCheckout(t *testing.T) {
	pool, service, runtime, checkout, buyer, checkJob := checkoutCheckFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE product_checkout_dispatches, product_checkout_requests`); err != nil {
		t.Fatal(err)
	}
	wrapped := &recoveryCheckoutRuntime{checkoutReadRuntime: runtime}
	service.runtimes = NewRuntimeCatalog(wrapped)
	recoveryJob := identityRecoveryJob(t, pool, checkout.PaymentID, buyer)
	if err := service.HandleProductIdentityRecoveryJob(ctx, recoveryJob); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
	if err := service.HandleProductCheckoutCheckJob(ctx, checkJob); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "cancelled", "cancelled", 0)
	// The original query remains unique when recovery races its scheduled job.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, ProductCheckoutCheckJobKind, checkout.PaymentID.String()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate checkout queries %d %v", count, err)
	}
}

type recoveryCheckoutRuntime struct{ *checkoutReadRuntime }

func (r *recoveryCheckoutRuntime) ReadProductPaymentIdentity(ctx context.Context, b ProductPaymentBinding) (ProductPaymentIdentityObservation, error) {
	result, err := r.ReadProductCheckout(ctx, CheckoutReadRequest{PaymentID: b.PaymentID, ResourceID: b.ResourceID, ProviderCheckoutID: b.ProviderCheckoutID, AmountCents: b.AmountCents, Currency: b.Currency, LiveMode: b.LiveMode})
	return ProductPaymentIdentityObservation{Checkout: &result}, err
}

func TestRecoveredCheckoutLaterPaidSchedulesFundsCheck(t *testing.T) {
	pool, service, runtime, checkout, buyer, _ := checkoutCheckFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE product_checkout_dispatches, product_checkout_requests`); err != nil {
		t.Fatal(err)
	}
	service.runtimes = NewRuntimeCatalog(&recoveryCheckoutRuntime{checkoutReadRuntime: runtime})
	if err := service.HandleProductIdentityRecoveryJob(ctx, identityRecoveryJob(t, pool, checkout.PaymentID, buyer)); err != nil {
		t.Fatal(err)
	}
	processCheckoutSignedPayment(t, service, checkout, "evt_recoveredlaterpaid")
	processCheckoutSignedPayment(t, service, checkout, "evt_recoveredlaterpaidrepeat")
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, checkout.PaymentID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("post-payment check missing/duplicated %d %v", count, err)
	}
	var checkJob jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.payment_id=$1`, checkout.PaymentID).Scan(&checkJob.ID, &checkJob.Kind, &checkJob.Payload); err != nil {
		t.Fatal(err)
	}
	service.runtimes = NewRuntimeCatalog(&refundReadRuntime{})
	if err := service.HandleProductRefundCheckJob(ctx, checkJob); err != nil {
		t.Fatal(err)
	}
	order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
	if err != nil || !order.CanRequestRefund {
		t.Fatalf("later paid order stuck after funds check: %#v %v", order, err)
	}
}
