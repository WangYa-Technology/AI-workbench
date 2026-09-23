package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

func TestProductRefundPartialReadPreservesEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	runtime := &durableProductRefundRuntime{}
	service, checkout, buyer, product, _ := fulfilledRefundFixture(t, pool, runtime)
	var providerPayment string
	if err := pool.QueryRow(ctx, `SELECT provider_payment_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&providerPayment); err != nil {
		t.Fatal(err)
	}
	var empty atomic.Bool
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer local-refund-fixture" {
			t.Error("unexpected financial dispatch or authentication")
			http.Error(w, "rejected", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/account":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "acct_workflow", "object": "account"})
		case "/balance":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "balance", "livemode": false})
		case "/payment_intents/" + providerPayment:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": providerPayment, "amount": 1900, "amount_received": 1900, "currency": "usd", "livemode": false, "status": "succeeded", "metadata": map[string]string{"hcai_payment_id": checkout.PaymentID.String(), "hcai_resource_id": product.String(), "hcai_purpose": "product"}})
		case "/refunds":
			if r.URL.Query().Get("payment_intent") != providerPayment {
				t.Error("unscoped refund query")
			}
			if empty.Load() {
				_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "has_more": false, "data": []any{}})
			} else if r.URL.Query().Get("starting_after") == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "has_more": true, "data": []any{map[string]any{"id": "re_partial_external", "payment_intent": providerPayment, "amount": 100, "currency": "usd", "status": "succeeded"}}})
			} else {
				http.Error(w, "private provider diagnostic", http.StatusServiceUnavailable)
			}
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	service.runtimes = NewRuntimeCatalog(originalMerchantHTTPRuntime(t, upstream, "local-refund-fixture"))
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	checkID := history.LatestCheck.ID
	repo := jobs.NewRepository(pool)
	job, err := repo.Claim(ctx, "partial-refund-test", time.Minute)
	if err != nil || job.Kind != ProductRefundCheckJobKind {
		t.Fatal(job, err)
	}
	failure := service.HandleProductRefundCheckJob(ctx, job)
	if failure == nil {
		t.Fatal("incomplete remote history reported success")
	}
	detail, err := service.GetRefundCheck(ctx, checkout.PaymentID, checkID)
	if err != nil || detail.Status != "failed" || len(detail.Observations) != 1 || detail.Observations[0].ProviderID != "re_partial_external" || detail.ObservedAt == nil || detail.ErrorCode == nil || *detail.ErrorCode != "payment_provider_unavailable" || detail.UnresolvedCount != 1 {
		t.Fatalf("lost verified first page on later HTTP failure: %#v %v", detail, err)
	}
	if jobs.ShouldRetry(failure) {
		t.Fatal("immutable partial evidence may be overwritten by retry")
	}
	if err = repo.Fail(ctx, job, "partial-refund-test", failure); err != nil {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE id=$1 AND status='failed'`, 1, job.ID)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE refund_check_id=$1`, 0, checkID)
	assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
	// A later complete empty result must not erase the previously seen refund.
	empty.Store(true)
	history, err = service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil || !history.CanCheck {
		t.Fatal(history, err)
	}
	if _, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion); err != nil {
		t.Fatal(err)
	}
	runAutomaticRefundCheck(t, service)
	assertOperationalMetric(t, pool, "problem", "refund_observation_unresolved", "test", 1, 0, 0)
	order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
	if err != nil || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" {
		t.Fatal("partial evidence lost refund gate", order, err)
	}
	if _, err = service.BeginProductRefund(ctx, buyer, checkout.OrderID, "partial-new-refund", "test", "The delivered content differs from the agreed description."); !errors.Is(err, ErrRefundConflict) {
		t.Fatal("partial evidence allowed a new refund", err)
	}
	detail, err = service.GetRefundCheck(ctx, checkout.PaymentID, checkID)
	if err != nil || detail.Status != "failed" || len(detail.Observations) != 1 || len(detail.UnresolvedProviderRefundIDs) != 1 {
		t.Fatal("history overwritten", detail, err)
	}
	// Without ordinary buyer retention, the same evidence must still protect bytes.
	quarantineExec(t, pool, `UPDATE users SET status='deleted' WHERE id=$1`, buyer)
	snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	var cleanupJob jobs.Job
	if err = pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,jsonb_build_object('orderId',$2::text)) RETURNING id,kind,payload`, productdelivery.CleanupJobKind, checkout.OrderID).Scan(&cleanupJob.ID, &cleanupJob.Kind, &cleanupJob.Payload); err != nil {
		t.Fatal(err)
	}
	if err = productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, cleanupJob); err != nil {
		t.Fatal(err)
	}
	store, err := service.config.MediaStores.Get(snapshot.Backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stat(ctx, snapshot.Key); err != nil {
		t.Fatal("partial refund evidence deleted", err)
	}
	if calls.Load() != 9 || len(runtime.operations) != 0 {
		t.Fatal("unexpected query or financial replay", calls.Load(), runtime.operations)
	}
}

func TestProductRefundPartialReadRequiresCompleteRecovery(t *testing.T) {
	pool, service, runtime, checkout, _ := automaticRefundFixture(t, true)
	ctx := t.Context()
	runtime.readError = newProviderFailure("payment_timeout", 0)
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err = pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN product_refund_checks c ON c.job_id=j.id WHERE c.id=$1`, history.LatestCheck.ID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	failure := service.HandleProductRefundCheckJob(ctx, job)
	if failure == nil || jobs.ShouldRetry(failure) {
		t.Fatal("partial read not retained as failed evidence", failure)
	}
	assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE refund_check_id=$1`, 0, history.LatestCheck.ID)
	original, err := service.GetRefundCheck(ctx, checkout.PaymentID, history.LatestCheck.ID)
	if err != nil || len(original.Observations) != 1 || original.Status != "failed" {
		t.Fatal(original, err)
	}
	runtime.readError = nil
	// The original failed check cannot reinterpret partial observations as a complete read.
	if err = service.HandleProductRefundCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
	history, err = service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion); err != nil {
		t.Fatal(err)
	}
	// Run the new check directly; the fixture deliberately retains older queued jobs.
	var recovery jobs.Job
	if err = pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN product_refund_checks c ON c.job_id=j.id WHERE c.payment_id=$1 AND c.status='requested'`, checkout.PaymentID).Scan(&recovery.ID, &recovery.Kind, &recovery.Payload); err != nil {
		t.Fatal(err)
	}
	if err = service.HandleProductRefundCheckJob(ctx, recovery); err != nil {
		t.Fatal(err)
	}
	assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_review WHERE payment_id=$1`, 0, checkout.PaymentID)
	original, err = service.GetRefundCheck(ctx, checkout.PaymentID, original.ID)
	if err != nil || original.Status != "failed" || len(original.Observations) != 1 || len(original.UnresolvedProviderRefundIDs) != 0 {
		t.Fatal("recovery rewrote original evidence", original, err)
	}
	if runtime.reads != 2 || len(runtime.operations) != 1 {
		t.Fatal("recovery replayed reads or money movement", runtime.reads, runtime.operations)
	}
}

type cancelledRefundReadRuntime struct {
	durableProductRefundRuntime
	cancel context.CancelFunc
}

func (r *cancelledRefundReadRuntime) ReadProductRefunds(_ context.Context, input RefundReadRequest) ([]RefundObservation, error) {
	r.cancel()
	return []RefundObservation{{ProviderID: "re_cancelled_prefix", ProviderPaymentID: input.ProviderPaymentID, AmountCents: 100, Currency: "USD", Status: "succeeded"}}, context.Canceled
}

func TestProductRefundPartialReadSurvivesCallerCancellation(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runtime := &cancelledRefundReadRuntime{cancel: cancel}
	service, checkout, _, _, _ := fulfilledRefundFixture(t, pool, runtime)
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err = pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN product_refund_checks c ON c.job_id=j.id WHERE c.id=$1`, history.LatestCheck.ID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	failure := service.HandleProductRefundCheckJob(ctx, job)
	if failure == nil || jobs.ShouldRetry(failure) || ctx.Err() == nil {
		t.Fatal("cancelled read lost terminal partial state", failure, ctx.Err())
	}
	detail, err := service.GetRefundCheck(t.Context(), checkout.PaymentID, history.LatestCheck.ID)
	if err != nil || detail.Status != "failed" || len(detail.Observations) != 1 || detail.ObservedAt == nil {
		t.Fatal("caller cancellation discarded verified evidence", detail, err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='payment.refund_check_incomplete'`, 1, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_review WHERE payment_id=$1`, 1, checkout.PaymentID)
}

func TestProductRefundPartialReadRejectsInvalidEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	runtime := &refundReadRuntime{readError: newProviderFailure("payment_timeout", 0)}
	service, checkout, _, _, _ := fulfilledRefundFixture(t, pool, runtime)
	runtime.observations = []RefundObservation{{ProviderID: "re_foreign_prefix", ProviderPaymentID: "pi_unrelated", AmountCents: 100, Currency: "USD", Status: "succeeded"}}
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err = pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN product_refund_checks c ON c.job_id=j.id WHERE c.id=$1`, history.LatestCheck.ID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	failure := service.HandleProductRefundCheckJob(ctx, job)
	if failure == nil || failure.Error() != "payment_response_invalid" {
		t.Fatal("invalid partial evidence accepted", failure)
	}
	detail, err := service.GetRefundCheck(ctx, checkout.PaymentID, history.LatestCheck.ID)
	if err != nil || len(detail.Observations) != 0 || detail.ObservedAt != nil {
		t.Fatal("unverified partial data persisted", detail, err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='payment.refund_check_incomplete'`, 0, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE refund_check_id=$1`, 0, history.LatestCheck.ID)
	assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
}
