package payments

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fundingMetricsFixture struct {
	pool     *pgxpool.Pool
	service  *Service
	checkout Checkout
	request  SellerPayoutRequest
	transfer uuid.UUID
	job      jobs.Job
}

func newFundingMetricsFixture(t *testing.T, dispatch bool) fundingMetricsFixture {
	t.Helper()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	service, checkout, settlement, request := sellerTransferFixture(t, pool)
	actor, approval := approveSellerFundingFixture(t, pool, service, request)
	f := fundingMetricsFixture{pool: pool, service: service, checkout: checkout, request: request}
	// Supply original clocks on insert; never disable immutable evidence guards.
	query := strings.Replace(reserveSellerTransferSQL, "dispatch_key)", "dispatch_key,created_at,reserved_at)", 1)
	query = strings.Replace(query, "'seller-payout:'||$1::text", "'transfer-'||ps.payment_id::text,$3,$3", 1)
	if err := pool.QueryRow(t.Context(), query+" RETURNING id", request.ID, settlement, time.Now().Add(-2*time.Hour)).Scan(&f.transfer); err != nil {
		t.Fatal(err)
	}
	if dispatch {
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(t.Context())
		insertFundingAdmissionFixture(t, tx, request.ID, f.transfer, actor, approval.ReviewID)
		f.job.Kind = SellerPayoutFundingJobKind
		if err := tx.QueryRow(t.Context(), `INSERT INTO jobs(kind,payload,created_at,updated_at)
 VALUES($1,jsonb_build_object('transferId',$2::uuid),now()-interval '1 hour',now()-interval '1 hour')
 RETURNING id,payload`, f.job.Kind, f.transfer).Scan(&f.job.ID, &f.job.Payload); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `INSERT INTO seller_payout_funding_dispatches(transfer_id,job_id,payment_id,provider_charge_id,created_at)
 SELECT $1,$2,id,provider_charge_id,now()-interval '1 hour' FROM payment_intents WHERE id=$3`, f.transfer, f.job.ID, checkout.PaymentID); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestProductOperationalMetricsFundingMissingDispatch(t *testing.T) {
	f := newFundingMetricsFixture(t, false)
	assertOperationalMetric(t, f.pool, "backlog", "seller_funding_unresolved", "test", 1, 7200, 7260)
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_dispatch_missing", "test", 1, 0, 0)
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_stopped", "test", 0, 0, 0)
	// A job with the right-looking payload is not an immutable dispatch binding.
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO jobs(kind,payload)
 VALUES($1,jsonb_build_object('transferId',$2::uuid))`, SellerPayoutFundingJobKind, f.transfer); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_dispatch_missing", "test", 1, 0, 0)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	// A missing dispatch cannot now be repaired by silently consuming an
	// unbound review. Preserve the gap until an explicit recovery is implemented.
	_, err = enqueueSellerPayoutFundingTx(t.Context(), tx, f.transfer)
	requirePayoutConstraint(t, err)
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_dispatch_missing", "test", 1, 0, 0)
	assertOperationalMetric(t, f.pool, "backlog", "seller_funding_unresolved", "test", 1, 7200, 7260)
}

func TestProductOperationalMetricsFundingStoppedAndClocks(t *testing.T) {
	f := newFundingMetricsFixture(t, true)
	ctx := t.Context()
	for _, status := range []string{"failed", "cancelled", "succeeded", "queued", "running"} {
		if _, err := f.pool.Exec(ctx, `UPDATE jobs SET status=$2,updated_at=now() WHERE id=$1`, f.job.ID, status); err != nil {
			t.Fatal(err)
		}
		want := int64(1)
		if status == "queued" || status == "running" {
			want = 0
		}
		assertOperationalMetric(t, f.pool, "problem", "seller_funding_stopped", "test", want, 0, 0)
		assertOperationalMetric(t, f.pool, "backlog", "seller_funding_unresolved", "test", 1, 7200, 7260)
		// The scanner cannot recover a source that never registered its first start.
		assertOperationalMetric(t, f.pool, "backlog", "seller_funding_check_due", "test", 0, 0, 0)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE seller_payout_funding_dispatches SET started_at=created_at;
 UPDATE seller_payout_transfers SET status='processing';
 UPDATE seller_payout_requests SET status='processing'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE jobs SET status='failed',updated_at=now()-interval '45 minutes' WHERE id=$1`, f.job.ID); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, f.pool, "backlog", "seller_funding_check_due", "test", 1, 2400, 2460)
	recovery := scheduleFundingRecovery(t, f.service)
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_stopped", "test", 0, 0, 0)
	assertOperationalMetric(t, f.pool, "backlog", "seller_funding_check_due", "test", 0, 0, 0)
	assertOperationalMetric(t, f.pool, "backlog", "seller_funding_unresolved", "test", 1, 7200, 7260)
	if _, err := f.pool.Exec(ctx, `UPDATE jobs SET status='failed',updated_at=now() WHERE id=$1`, recovery.ID); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_stopped", "test", 1, 0, 0)
	assertOperationalMetric(t, f.pool, "backlog", "seller_funding_check_due", "test", 0, 0, 0)
	assertOperationalMetric(t, f.pool, "problem", "invalid_timestamp", "test", 0, 0, 0)
	// Mutable parent states/mode must neither hide nor relabel original source funds.
	if _, err := f.pool.Exec(ctx, `UPDATE payment_intents SET status='refunded',live_mode=true,updated_at=now() WHERE id=$1`, f.checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE orders SET status='refunded' WHERE id=(SELECT order_id FROM payment_intents WHERE id=$1)`, f.checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, f.pool, "backlog", "seller_funding_unresolved", "test", 1, 7200, 7260)
	assertOperationalMetric(t, f.pool, "backlog", "seller_funding_unresolved", "live", 0, 0, 0)
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_stopped", "test", 1, 0, 0)
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_stopped", "live", 0, 0, 0)
}

func TestProductOperationalMetricsFundingInvalidActivityClocks(t *testing.T) {
	f := newFundingMetricsFixture(t, true)
	for _, clock := range []string{"infinity", "-infinity", time.Now().Add(time.Hour).Format(time.RFC3339Nano)} {
		if _, err := f.pool.Exec(t.Context(), `UPDATE jobs SET updated_at=$2::text::timestamptz WHERE id=$1`, f.job.ID, clock); err != nil {
			t.Fatal(err)
		}
		assertOperationalMetric(t, f.pool, "problem", "invalid_timestamp", "test", 1, 0, 0)
		assertOperationalMetric(t, f.pool, "backlog", "seller_funding_unresolved", "test", 1, 7200, 7260)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE jobs SET updated_at=now() WHERE id=$1`, f.job.ID); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, f.pool, "problem", "invalid_timestamp", "test", 0, 0, 0)
	// Preserve and expose historical invalid clocks without weakening new inserts.
	applyFundingReadDeadlineMigration(t, f.pool, "down")
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO seller_payout_funding_reads(transfer_id,started_at) VALUES($1,'infinity')`, f.transfer); err != nil {
		t.Fatal(err)
	}
	applyFundingReadDeadlineMigration(t, f.pool, "up")
	assertOperationalMetric(t, f.pool, "problem", "invalid_timestamp", "test", 1, 0, 0)
}

func TestProductOperationalMetricsFundingReadOnlyRecovery(t *testing.T) {
	pool, service, runtime, _, request, original := sellerFundingFixture(t)
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(t.Context(), original); err == nil {
		t.Fatal("lost response reported success")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='failed' WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "seller_funding_stopped", "test", 1, 0, 0)
	recovery := scheduleFundingRecovery(t, service)
	assertOperationalMetric(t, pool, "problem", "seller_funding_stopped", "test", 0, 0, 0)
	assertOperationalMetric(t, pool, "backlog", "seller_funding_unresolved", "test", 1, 0, 60)
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 0 {
		t.Fatal("metrics issued provider calls")
	}
	if err := service.HandleSellerPayoutFundingCheckJob(t.Context(), recovery); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "seller_funding_unresolved", "test", 0, 0, 0)
	assertOperationalMetric(t, pool, "problem", "seller_funding_stopped", "test", 0, 0, 0)
	assertOperationalMetric(t, pool, "problem", "seller_funding_review", "test", 0, 0, 0)
	assertSellerFunding(t, pool, request, "succeeded", "reconciliation_required")
	balance, err := singleSellerFunds(t, service, t.Context(), request.SellerID)
	if err != nil || balance.ReservedCents != request.AmountCents || balance.WithdrawableCents != 0 {
		t.Fatalf("source metrics cleared bank reservation: %+v %v", balance, err)
	}
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 1 {
		t.Fatal("metrics retried source funding")
	}
}

func TestProductOperationalMetricsFundingReviewRetained(t *testing.T) {
	pool, service, runtime, _, _, original := sellerFundingFixture(t)
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(t.Context(), original); err == nil {
		t.Fatal("lost response reported success")
	}
	runtime.lookup = func(context.Context, TransferLookupRequest) (TransferLookupResult, error) {
		return TransferLookupResult{Outcome: "found", Pages: 1}, nil // No supporting observation.
	}
	if err := service.HandleSellerPayoutFundingJob(t.Context(), original); err == nil {
		t.Fatal("invalid observation accepted")
	}
	assertOperationalMetric(t, pool, "problem", "seller_funding_review", "test", 1, 0, 0)
	runtime.lookup = nil
	for range 2 {
		if err := service.HandleSellerPayoutFundingJob(t.Context(), original); err == nil {
			t.Fatal("later result erased review evidence")
		}
		assertOperationalMetric(t, pool, "problem", "seller_funding_review", "test", 1, 0, 0)
		assertOperationalMetric(t, pool, "backlog", "seller_funding_unresolved", "test", 1, 0, 60)
	}
	assertOperationalMetric(t, pool, "problem", "seller_funding_review", "live", 0, 0, 0)
}
