package payments

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func scheduleFundingRecovery(t *testing.T, service *Service) jobs.Job {
	t.Helper()
	var due time.Time
	if err := service.pool.QueryRow(t.Context(), `SELECT due_at FROM seller_payout_funding_check_candidates`).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileSellerPayoutFunding(t.Context(), 100, due.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("scheduled %d: %v", n, err)
	}
	var job jobs.Job
	if err := service.pool.QueryRow(t.Context(), `SELECT j.id,j.kind,j.payload FROM seller_payout_funding_checks c JOIN jobs j ON j.id=c.job_id
 ORDER BY c.created_at DESC,c.job_id DESC LIMIT 1`).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestSellerFundingRecoveryStoppedJobsReadOnly(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			pool, service, runtime, _, request, original := sellerFundingFixture(t)
			ctx := t.Context()
			runtime.lost = true
			if err := service.HandleSellerPayoutFundingJob(ctx, original); err == nil {
				t.Fatal("expected lost response")
			}
			if _, err := pool.Exec(ctx, `UPDATE jobs SET status=$2,attempts=max_attempts,last_error_code='worker_lease_expired',updated_at=clock_timestamp() WHERE id=$1`, original.ID, status); err != nil {
				t.Fatal(err)
			}
			if n, err := service.ReconcileSellerPayoutFunding(ctx, 100); err != nil || n != 0 {
				t.Fatalf("ignored cooldown: %d %v", n, err)
			}
			service.config.Enabled = false
			recovery := scheduleFundingRecovery(t, service)
			if n, err := service.reconcileSellerPayoutFunding(ctx, 100, time.Now().Add(time.Hour)); err != nil || n != 0 {
				t.Fatalf("duplicated active check: %d %v", n, err)
			}
			if err := service.HandleSellerPayoutFundingJob(ctx, recovery); err == nil {
				t.Fatal("read job accepted by write entry")
			}
			if err := service.HandleSellerPayoutFundingCheckJob(ctx, original); err == nil {
				t.Fatal("original job accepted by recovery entry")
			}
			if err := service.HandleSellerPayoutFundingCheckJob(ctx, recovery); err != nil {
				t.Fatal(err)
			}
			assertSellerFunding(t, pool, request, "succeeded", "reconciliation_required")
			if runtime.transfers.Load() != 1 || runtime.reads.Load() != 1 {
				t.Fatalf("calls: writes=%d reads=%d", runtime.transfers.Load(), runtime.reads.Load())
			}
			var retained bool
			if err := pool.QueryRow(ctx, `SELECT status=$2 AND attempts=max_attempts AND last_error_code='worker_lease_expired' FROM jobs WHERE id=$1`, original.ID, status).Scan(&retained); err != nil || !retained {
				t.Fatalf("original history rewritten: %t %v", retained, err)
			}
			balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
			if err != nil || balance.ReservedCents != request.AmountCents || balance.WithdrawableCents != 0 {
				t.Fatalf("reservation lost: %+v %v", balance, err)
			}
			if n, err := service.reconcileSellerPayoutFunding(ctx, 100, time.Now().Add(time.Hour)); err != nil || n != 0 {
				t.Fatalf("terminal transfer rescheduled: %d %v", n, err)
			}
		})
	}
}

func TestSellerFundingRecoveryAdmissionAndEvidence(t *testing.T) {
	pool, service, runtime, _, _, original := sellerFundingFixture(t)
	ctx := t.Context()
	var transferID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT transfer_id FROM seller_payout_funding_dispatches WHERE job_id=$1`, original.ID).Scan(&transferID); err != nil {
		t.Fatal(err)
	}
	var forged uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,$2) RETURNING id`, SellerPayoutFundingCheckJobKind, original.Payload).Scan(&forged); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO seller_payout_funding_checks(job_id,transfer_id) VALUES($1,$2)`, forged, transferID)
	requirePayoutConstraint(t, err)
	if err := service.HandleSellerPayoutFundingCheckJob(ctx, jobs.Job{ID: forged, Payload: original.Payload}); err == nil {
		t.Fatal("unbound recovery job accepted")
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileSellerPayoutFunding(ctx, 100, time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("undispatched source scheduled: %d %v", n, err)
	}
	if runtime.transfers.Load() != 0 || runtime.reads.Load() != 0 {
		t.Fatal("undispatched recovery made network call")
	}
	// Use a fresh source to model committed start followed by process loss
	// before the POST. A stopped source must not acquire a first-send marker.
	pool, service, runtime, _, _, original = sellerFundingFixture(t)
	if err := pool.QueryRow(ctx, `SELECT transfer_id FROM seller_payout_funding_dispatches WHERE job_id=$1`, original.ID).Scan(&transferID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_funding_dispatches SET started_at=clock_timestamp() WHERE transfer_id=$1`, transferID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_transfers SET status='processing' WHERE id=$1`, transferID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	recovery := scheduleFundingRecovery(t, service)
	runtime.lookup = func(context.Context, TransferLookupRequest) (TransferLookupResult, error) {
		return TransferLookupResult{Outcome: "not_found", Pages: 1, Observations: []TransferObservation{}}, nil
	}
	if err := service.HandleSellerPayoutFundingCheckJob(ctx, recovery); err == nil {
		t.Fatal("empty lookup reported success")
	}
	if runtime.transfers.Load() != 0 || runtime.reads.Load() != 1 {
		t.Fatal("crashed dispatch resent funds")
	}
	for _, query := range []string{
		`DELETE FROM seller_payout_funding_checks`,
		`UPDATE seller_payout_funding_checks SET created_at=clock_timestamp()`,
	} {
		_, err := pool.Exec(ctx, query)
		requirePayoutConstraint(t, err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0157_seller_funding_recovery.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, string(down))
	requirePayoutConstraint(t, err)
}

func TestSellerFundingRecoveryRetriesRetainOriginalAttempts(t *testing.T) {
	pool, service, runtime, _, _, original := sellerFundingFixture(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now()+interval '1 day' WHERE id<>$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET max_attempts=1 WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	repository := jobs.NewRepository(pool)
	claimed, err := repository.Claim(ctx, "funding", time.Minute)
	if err != nil || claimed.ID != original.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	runtime.lost = true
	err = service.HandleSellerPayoutFundingJob(ctx, claimed)
	if err == nil {
		t.Fatal("expected lost response")
	}
	if err := repository.Fail(ctx, claimed, "funding", err); err != nil {
		t.Fatal(err)
	}
	recovery := scheduleFundingRecovery(t, service)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET max_attempts=1 WHERE id=$1`, recovery.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = repository.Claim(ctx, "recovery", time.Minute)
	if err != nil || claimed.ID != recovery.ID {
		t.Fatalf("recovery claim: %+v %v", claimed, err)
	}
	runtime.lookup = func(context.Context, TransferLookupRequest) (TransferLookupResult, error) {
		return TransferLookupResult{}, errors.New("temporary read failure")
	}
	err = service.HandleSellerPayoutFundingCheckJob(ctx, claimed)
	if err == nil {
		t.Fatal("read failure reported success")
	}
	if err := repository.Fail(ctx, claimed, "recovery", err); err != nil {
		t.Fatal(err)
	}
	next := scheduleFundingRecovery(t, service)
	if next.ID == recovery.ID {
		t.Fatal("failed recovery job rewritten")
	}
	claimed, err = repository.Claim(ctx, "recovery", time.Minute)
	if err != nil || claimed.ID != next.ID {
		t.Fatalf("second recovery claim: %+v %v", claimed, err)
	}
	runtime.lookup = nil
	if err := service.HandleSellerPayoutFundingCheckJob(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, claimed, "recovery"); err != nil {
		t.Fatal(err)
	}
	var failed, succeeded, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='failed'),count(*) FILTER(WHERE status='succeeded'),
 (SELECT count(*) FROM audit_events WHERE action='marketplace.seller_funding_check_scheduled')
 FROM job_attempts WHERE job_id=ANY($1::uuid[])`, []uuid.UUID{original.ID, recovery.ID, next.ID}).Scan(&failed, &succeeded, &audits); err != nil || failed != 2 || succeeded != 1 || audits != 2 {
		t.Fatalf("attempts=%d/%d audit=%d err=%v", failed, succeeded, audits, err)
	}
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 2 {
		t.Fatal("recovery resent source funds")
	}
}

func TestSellerFundingRecoveryConcurrentScansAndBusyPayment(t *testing.T) {
	pool, service, runtime, checkout, _, original := sellerFundingFixture(t)
	ctx := t.Context()
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(ctx, original); err == nil {
		t.Fatal("expected lost response")
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled' WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileSellerPayoutFunding(ctx, 100, time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("busy payment not skipped: %d %v", n, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	type result struct {
		count int
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		// Separate service instances exercise database serialization.
		other := &Service{pool: pool}
		go func() {
			n, err := other.reconcileSellerPayoutFunding(ctx, 100, time.Now().Add(time.Hour))
			results <- result{n, err}
		}()
	}
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.count+b.count != 1 {
		t.Fatalf("concurrent scans: %+v %+v", a, b)
	}
}
