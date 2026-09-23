package payments

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func operationalPaymentFixture(t *testing.T, pool *pgxpool.Pool, live bool, since time.Time) uuid.UUID {
	t.Helper()
	buyer, seller, _, product := newProductCheckoutFixture(t, pool)
	order, payment := uuid.New(), uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot)
 SELECT $1,$2,$3,1900,'USD','payment_pending',$4,'Metrics product',name,version,terms,refund_window_days FROM licenses WHERE code='hcai-commercial-standard-v1'`, order, buyer, product, "metric-order-"+order.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id,created_at)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','checkout_pending',$6,$7,$8,$9)`, payment, buyer, seller, product, order, live, "metric-payment-"+payment.String(), "pi_"+payment.String()[:8], since.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO product_checkout_requests(payment_id,identity,request,created_at)
 VALUES($1::uuid,'{"provider":"stripe","merchantId":"acct_metrics","requestVersion":"stripe-product-checkout-v1"}',jsonb_build_object('PaymentID',$1::uuid::text,'Purpose','product'),$2)`, payment, since); err != nil {
		t.Fatal(err)
	}
	return payment
}

func assertOperationalMetric(t *testing.T, pool *pgxpool.Pool, category, kind, mode string, count int64, minAge, maxAge float64) {
	t.Helper()
	result, err := ProductOperationalMetrics(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	// Assert the complete public label set, including zero-valued series. A
	// count alone could miss an omitted label replaced by a duplicate or an
	// unexpected (potentially provider-controlled) label.
	for _, set := range []struct {
		category string
		items    []OperationalMetric
		kinds    []string
	}{
		{"backlog", result.Backlogs, []string{
			"checkout_pending", "checkout_expired", "refund_unresolved", "refund_check_due",
			"settlement_due", "settlement_unresolved", "seller_funding_unresolved", "seller_funding_check_due", "seller_bank_unresolved", "seller_bank_check_due", "seller_reversal_unresolved", "seller_reversal_closure_due",
		}},
		{"problem", result.Problems, []string{
			"event_failed", "checkout_check_missing", "checkout_check_stopped", "refund_check_stopped",
			"refund_observation_unresolved", "refund_read_unrecorded", "refund_evidence_missing",
			"checkout_evidence_missing", "checkout_evidence_conflict", "closed_checkout_paid",
			"settlement_missing", "settlement_check_stopped", "seller_funding_dispatch_missing",
			"seller_funding_admission_missing", "seller_funding_stopped", "seller_funding_review",
			"seller_funding_read_unrecorded", "seller_bank_failed", "seller_bank_review", "seller_bank_read_unrecorded", "seller_bank_dispatch_stopped", "seller_reversal_review", "seller_reversal_read_unrecorded", "seller_reversal_stopped", "invalid_timestamp",
		}},
	} {
		want := make(map[[2]string]bool, len(set.kinds)*2)
		for _, name := range set.kinds {
			for _, environment := range []string{"live", "test"} {
				want[[2]string{name, environment}] = true
			}
		}
		for _, item := range set.items {
			label := [2]string{item.Kind, item.Mode}
			if !want[label] {
				t.Fatalf("unexpected or duplicate %s label: %v", set.category, label)
			}
			delete(want, label)
		}
		if len(want) != 0 {
			t.Fatalf("missing %s labels: %v", set.category, want)
		}
	}
	items := result.Backlogs
	if category == "problem" {
		items = result.Problems
	}
	for _, item := range items {
		if item.Kind == kind && item.Mode == mode {
			if item.Count != count || item.OldestAgeSeconds < minAge || item.OldestAgeSeconds > maxAge {
				t.Fatalf("%s %+v want count=%d age=[%f,%f]", category, item, count, minAge, maxAge)
			}
			return
		}
	}
	t.Fatalf("missing %s/%s/%s", category, kind, mode)
}

func TestProductOperationalMetricsCheckoutClocksAndModes(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	assertOperationalMetric(t, pool, "backlog", "checkout_pending", "live", 0, 0, 0)
	live := operationalPaymentFixture(t, pool, true, time.Now().Add(-2*time.Hour))
	_ = operationalPaymentFixture(t, pool, false, time.Now().Add(-3*time.Hour))
	// Preparation may precede the frozen provider request; count that time too.
	assertOperationalMetric(t, pool, "backlog", "checkout_pending", "live", 1, 10800, 10850)
	assertOperationalMetric(t, pool, "backlog", "checkout_pending", "test", 1, 14400, 14450)
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET updated_at=now()+interval '1 hour',version=version+1 WHERE id=$1`, live); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "checkout_pending", "live", 1, 10800, 10850)
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='checkout_open',provider_checkout_id='cs_metricsexpired',checkout_url='https://checkout.stripe.com/metrics',checkout_expires_at=now()-interval '1 hour' WHERE id=$1`, live); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "checkout_pending", "live", 0, 0, 0)
	assertOperationalMetric(t, pool, "backlog", "checkout_expired", "live", 1, 3600, 3650)
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET checkout_expires_at=now()+interval '1 hour' WHERE id=$1`, live); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "checkout_expired", "live", 0, 0, 0)
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET checkout_expires_at='infinity' WHERE id=$1`, live); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "checkout_evidence_missing", "live", 1, 0, 0)
}

func TestProductOperationalMetricsRefundHistoryAndDueChecks(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	payment := operationalPaymentFixture(t, pool, true, time.Now().Add(-72*time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='refund_pending',updated_at=now() WHERE id=$1`, payment); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "refund_evidence_missing", "live", 1, 0, 0)
	operation := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,status,requested_at)
 SELECT $1,id,provider,provider_payment_id,amount_cents,currency,true,'pending',now()-interval '2 days' FROM payment_intents WHERE id=$2`, operation, payment); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "refund_evidence_missing", "live", 0, 0, 0)
	assertOperationalMetric(t, pool, "backlog", "refund_unresolved", "live", 1, 172800, 172850)
	assertOperationalMetric(t, pool, "backlog", "refund_check_due", "live", 1, 171900, 171950)
	if _, err := pool.Exec(ctx, `UPDATE product_refund_attempts SET updated_at=now() WHERE operation_id=$1`, operation); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "refund_unresolved", "live", 1, 172800, 172850)
	// Settled parent status cannot hide another unresolved operation.
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='refunded' WHERE id=$1`, payment); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "refund_unresolved", "live", 1, 172800, 172850)
	if _, err := pool.Exec(ctx, `UPDATE product_refund_attempts SET status='failed',reconciliation_required=true WHERE operation_id=$1`, operation); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "refund_unresolved", "live", 1, 172800, 172850)
	if _, err := pool.Exec(ctx, `UPDATE product_refund_attempts SET reconciliation_required=false WHERE operation_id=$1`, operation); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "refund_unresolved", "live", 0, 0, 0)
	assertOperationalMetric(t, pool, "backlog", "refund_check_due", "live", 0, 0, 0)
	// Future or infinite evidence must not silently turn into a healthy age zero.
	if _, err := pool.Exec(ctx, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,status,requested_at)
 SELECT gen_random_uuid(),id,provider,provider_payment_id,amount_cents,currency,true,'requested',stamp
 FROM payment_intents CROSS JOIN (VALUES(now()+interval '1 hour'),('infinity'::timestamptz)) times(stamp) WHERE id=$1`, payment); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "invalid_timestamp", "live", 2, 0, 0)
}

func TestProductOperationalMetricsMissingCheckoutEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	payment := operationalPaymentFixture(t, pool, true, time.Now())
	// Stripe's database constraint permits a NULL URL for recovery. Without
	// an authenticated terminal lookup that allowance is not healthy evidence.
	for _, tc := range []struct {
		name, session string
		url           any
		want          int64
	}{
		{"missing_url", "cs_without_proof", nil, 1},
		{"usable_session", "cs_with_url", "https://checkout.stripe.com/c/pay/example", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(t.Context(), `UPDATE payment_intents SET status='checkout_open',provider_checkout_id=$2,checkout_url=$3,checkout_expires_at=now()+interval '1 hour' WHERE id=$1`, payment, tc.session, tc.url); err != nil {
				t.Fatal(err)
			}
			assertOperationalMetric(t, pool, "problem", "checkout_evidence_missing", "live", tc.want, 0, 0)
			assertOperationalMetric(t, pool, "problem", "checkout_evidence_missing", "test", 0, 0, 0)
		})
	}
}

func TestProductOperationalMetricsStoppedCheckoutCheck(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	payment := operationalPaymentFixture(t, pool, true, time.Now())
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='checkout_open',provider_checkout_id='cs_stopped',checkout_url='https://checkout.stripe.com/c/pay/stopped',checkout_expires_at=now()+interval '1 hour' WHERE id=$1`, payment); err != nil {
		t.Fatal(err)
	}
	var job uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status) VALUES($1,jsonb_build_object('paymentId',$2::text),'cancelled') RETURNING id`, ProductCheckoutCheckJobKind, payment).Scan(&job); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "checkout_expired", "live", 0, 0, 0)
	assertOperationalMetric(t, pool, "problem", "checkout_check_stopped", "live", 1, 0, 0)
	assertOperationalMetric(t, pool, "problem", "checkout_check_stopped", "test", 0, 0, 0)
	// A queued recovery supersedes a stopped attempt without deleting evidence.
	var recovery uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,created_at) VALUES($1,jsonb_build_object('paymentId',$2::text),now()+interval '1 second') RETURNING id`, ProductCheckoutCheckJobKind, payment).Scan(&recovery); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "checkout_check_stopped", "live", 0, 0, 0)
	// Even a newer stopped record must not mask another active execution.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET created_at=now()+interval '2 seconds' WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "checkout_check_stopped", "live", 0, 0, 0)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, recovery); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "checkout_check_stopped", "live", 1, 0, 0)
	// A credible result can settle the payment before an old check is retired.
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='paid' WHERE id=$1`, payment); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "checkout_check_stopped", "live", 0, 0, 0)
}

func TestProductOperationalMetricsRealRefundCheckCompletion(t *testing.T) {
	pool, service, _, checkout, _ := automaticRefundFixture(t, true)
	assertOperationalMetric(t, pool, "backlog", "refund_unresolved", "test", 1, 0, 60)
	if n, err := service.reconcileProductRefunds(t.Context(), 100, time.Now().Add(16*time.Minute)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	job := runAutomaticRefundCheck(t, service)
	assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
	assertOperationalMetric(t, pool, "backlog", "refund_unresolved", "test", 0, 0, 0)
	assertOperationalMetric(t, pool, "problem", "refund_check_stopped", "test", 0, 0, 0)
	// A stopped query remains visible even if an earlier funds result settled.
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='failed' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "refund_check_stopped", "test", 1, 0, 0)
	var next uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO jobs(kind) VALUES($1) RETURNING id`, ProductRefundCheckJobKind).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO product_refund_checks(id,payment_id,job_id,status,origin) VALUES($1,$2,$3,'requested','automatic')`, uuid.New(), checkout.PaymentID, next); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "refund_check_stopped", "test", 0, 0, 0)
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='cancelled' WHERE id=$1`, next); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "refund_check_stopped", "test", 1, 0, 0)
}
