package payments

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func applyFundingReadDeadlineMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	body, err := os.ReadFile("../platform/database/migrations/0158_seller_funding_read_deadlines." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
}

func TestSellerFundingReadDeadlineGuards(t *testing.T) {
	f := newFundingMetricsFixture(t, true)
	ctx := t.Context()
	for _, value := range []string{"NULL", "'infinity'", "'-infinity'", "clock_timestamp()-interval '1 second'", "clock_timestamp()+interval '1 hour'"} {
		_, err := f.pool.Exec(ctx, `INSERT INTO seller_payout_funding_reads(transfer_id,read_deadline) VALUES($1,`+value+`)`, f.transfer)
		requirePayoutConstraint(t, err)
	}
	var read uuid.UUID
	var started, deadline time.Time
	if err := f.pool.QueryRow(ctx, `INSERT INTO seller_payout_funding_reads(transfer_id) VALUES($1) RETURNING id,started_at,read_deadline`, f.transfer).Scan(&read, &started, &deadline); err != nil {
		t.Fatal(err)
	}
	if d := deadline.Sub(started); d < 20*time.Second || d > 21*time.Second {
		t.Fatalf("unexpected durable budget: %v", d)
	}
	for _, query := range []string{
		`UPDATE seller_payout_funding_reads SET read_deadline=NULL WHERE id=$1`,
		`UPDATE seller_payout_funding_reads SET read_deadline=read_deadline+interval '1 second' WHERE id=$1`,
		`DELETE FROM seller_payout_funding_reads WHERE id=$1`,
	} {
		_, err := f.pool.Exec(ctx, query, read)
		requirePayoutConstraint(t, err)
	}
	body, err := os.ReadFile("../platform/database/migrations/0158_seller_funding_read_deadlines.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(ctx, string(body))
	requirePayoutConstraint(t, err)
}

func TestSellerFundingReadDeadlineLegacyEvidence(t *testing.T) {
	f := newFundingMetricsFixture(t, true)
	applyFundingReadDeadlineMigration(t, f.pool, "down")
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO seller_payout_funding_reads(transfer_id) VALUES($1),($1)`, f.transfer); err != nil {
		t.Fatal(err)
	}
	applyFundingReadDeadlineMigration(t, f.pool, "up")
	var unknown int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_payout_funding_reads WHERE read_deadline IS NULL AND finished_at IS NULL`).Scan(&unknown); err != nil || unknown != 2 {
		t.Fatalf("legacy deadlines were fabricated: %d %v", unknown, err)
	}
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_read_unrecorded", "test", 1, 0, 0)
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_read_unrecorded", "live", 0, 0, 0)
	// A mutable parent cannot hide the original mode or missing evidence.
	if _, err := f.pool.Exec(t.Context(), `UPDATE payment_intents SET live_mode=true,status='refunded' WHERE id=$1`, f.checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, f.pool, "problem", "seller_funding_read_unrecorded", "test", 1, 0, 0)
	applyFundingReadDeadlineMigration(t, f.pool, "down")
	applyFundingReadDeadlineMigration(t, f.pool, "up")
}

func TestSellerFundingReadDeadlineMetricGrace(t *testing.T) {
	f := newFundingMetricsFixture(t, true)
	ctx := t.Context()
	for _, age := range []time.Duration{-20 * time.Second, time.Second, 10 * time.Second} {
		deadline := time.Now().Add(-age)
		var read uuid.UUID
		if err := f.pool.QueryRow(ctx, `INSERT INTO seller_payout_funding_reads(transfer_id,started_at,read_deadline) VALUES($1,$2,$3) RETURNING id`, f.transfer, deadline.Add(-20*time.Second), deadline).Scan(&read); err != nil {
			t.Fatal(err)
		}
		want := int64(0)
		if age > 5*time.Second {
			want = 1
		}
		assertOperationalMetric(t, f.pool, "problem", "seller_funding_read_unrecorded", "test", want, 0, 0)
		if _, err := f.pool.Exec(ctx, `UPDATE seller_payout_funding_reads SET finished_at=clock_timestamp(),outcome='error',error_code='payment_request_failed',evidence='{"observations":[]}' WHERE id=$1`, read); err != nil {
			t.Fatal(err)
		}
		assertOperationalMetric(t, f.pool, "problem", "seller_funding_read_unrecorded", "test", 0, 0, 0)
	}
}

func TestSellerFundingReadDeadlineIncludesRegistration(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err == nil {
		t.Fatal("expected unknown first dispatch")
	}
	// Consume the entire read budget before the registration transaction commits.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE seller_payout_funding_reads ALTER COLUMN read_deadline SET DEFAULT (clock_timestamp()+interval '50 milliseconds');
 CREATE FUNCTION delay_funding_registration() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN PERFORM pg_sleep(0.1); RETURN NEW; END; $$;
 CREATE TRIGGER delay_funding_registration AFTER INSERT ON seller_payout_funding_reads FOR EACH ROW EXECUTE FUNCTION delay_funding_registration()`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired read restarted its deadline: %v", err)
	}
	var unfinished int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_payout_funding_reads WHERE finished_at IS NULL AND read_deadline<clock_timestamp()`).Scan(&unfinished); err != nil || unfinished != 1 {
		t.Fatalf("lost registration: %d %v", unfinished, err)
	}
	if runtime.reads.Load() != 0 || runtime.transfers.Load() != 1 {
		t.Fatal("expired invocation reached provider")
	}
	assertSellerFunding(t, pool, request, "reconciliation_required", "reconciliation_required")
}

func TestSellerFundingReadDeadlineIncludesLockWait(t *testing.T) {
	pool, service, runtime, checkout, request, job := sellerFundingFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("expected unknown first dispatch")
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE seller_payout_funding_reads ALTER COLUMN read_deadline SET DEFAULT (clock_timestamp()+interval '2 seconds');
 CREATE FUNCTION gate_funding_registration() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN PERFORM pg_advisory_xact_lock(158,1); RETURN NEW; END; $$;
 CREATE TRIGGER gate_funding_registration AFTER INSERT ON seller_payout_funding_reads FOR EACH ROW EXECUTE FUNCTION gate_funding_registration()`); err != nil {
		t.Fatal(err)
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock(158,1)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- service.HandleSellerPayoutFundingJob(ctx, job) }()
	gatePID := int32(gate.Conn().PgConn().PID())
	workerPID := waitForProductBlockingTx(t, ctx, pool, gatePID)
	locker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback(context.Background())
	locked := make(chan error, 1)
	go func() {
		_, err := locker.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, checkout.PaymentID)
		locked <- err
	}()
	waitForProductBlockingTx(t, ctx, pool, workerPID)
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-locked; err != nil {
		t.Fatal(err)
	}
	waitForProductBlockingTx(t, ctx, pool, int32(locker.Conn().PgConn().PID()))
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock wait did not respect durable deadline: %v", err)
	}
	if runtime.reads.Load() != 0 || runtime.transfers.Load() != 1 {
		t.Fatal("timed-out waiter reached provider")
	}
	if err := locker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertSellerFunding(t, pool, request, "succeeded", "reconciliation_required")
	if runtime.reads.Load() != 1 || runtime.transfers.Load() != 1 {
		t.Fatal("recovery resent source funds")
	}
	var skipped int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_payout_funding_reads WHERE outcome='skipped'`).Scan(&skipped); err != nil || skipped != 1 {
		t.Fatalf("abandoned read not retained as skipped: %d %v", skipped, err)
	}
}

func TestSellerFundingReadDeadlineRejectsLateSuccess(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err == nil {
		t.Fatal("expected unknown first dispatch")
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE seller_payout_funding_reads ALTER COLUMN read_deadline SET DEFAULT (clock_timestamp()+interval '100 milliseconds')`); err != nil {
		t.Fatal(err)
	}
	runtime.lookup = func(ctx context.Context, input TransferLookupRequest) (TransferLookupResult, error) {
		deadline, ok := ctx.Deadline()
		var stored time.Time
		if err := pool.QueryRow(t.Context(), `SELECT read_deadline FROM seller_payout_funding_reads WHERE finished_at IS NULL`).Scan(&stored); err != nil || !ok || !deadline.Equal(stored) {
			t.Errorf("provider deadline differs from durable deadline: %v %v %v", deadline, stored, err)
		}
		<-ctx.Done()
		return observedSettlementTransfer(input), nil
	}
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err == nil {
		t.Fatal("late success advanced funding")
	}
	assertSellerFunding(t, pool, request, "reconciliation_required", "reconciliation_required")
	var outcome string
	var observations int
	if err := pool.QueryRow(t.Context(), `SELECT outcome,jsonb_array_length(evidence->'observations') FROM seller_payout_funding_reads WHERE finished_at IS NOT NULL`).Scan(&outcome, &observations); err != nil || outcome != "error" || observations != 1 {
		t.Fatalf("late evidence lost or accepted: %q %d %v", outcome, observations, err)
	}
	runtime.lookup = nil
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	assertSellerFunding(t, pool, request, "succeeded", "reconciliation_required")
}

func TestSellerFundingReadDeadlineRejectsOldWriter(t *testing.T) {
	f := newFundingMetricsFixture(t, true)
	for _, protocol := range []string{"", "deadline-v0"} {
		tx, err := f.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `SELECT set_config('app.seller_funding_read_protocol',$1,true)`, protocol); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), `INSERT INTO seller_payout_funding_reads(transfer_id) VALUES($1)`, f.transfer)
		_ = tx.Rollback(t.Context())
		requirePayoutConstraint(t, err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO seller_payout_funding_reads(transfer_id) VALUES($1)`, f.transfer); err != nil {
		t.Fatalf("current binary cannot register: %v", err)
	}
}

func TestSellerFundingReadDeadlineBoundsPersistence(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err == nil {
		t.Fatal("expected unknown first dispatch")
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE seller_payout_funding_reads ALTER COLUMN read_deadline SET DEFAULT (clock_timestamp()+interval '100 milliseconds')`); err != nil {
		t.Fatal(err)
	}
	runtime.lookup = func(ctx context.Context, input TransferLookupRequest) (TransferLookupResult, error) {
		deadline, _ := ctx.Deadline()
		timer := time.NewTimer(time.Until(deadline.Add(5100 * time.Millisecond)))
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-t.Context().Done():
		}
		return observedSettlementTransfer(input), nil
	}
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late persistence was accepted: %v", err)
	}
	assertSellerFunding(t, pool, request, "reconciliation_required", "reconciliation_required")
	assertOperationalMetric(t, pool, "problem", "seller_funding_read_unrecorded", "test", 1, 0, 0)
	runtime.lookup = nil
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "problem", "seller_funding_read_unrecorded", "test", 0, 0, 0)
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 2 {
		t.Fatal("late result recovery resent source funds")
	}
	balance, err := singleSellerFunds(t, service, t.Context(), request.SellerID)
	if err != nil || balance.ReservedCents != request.AmountCents || balance.WithdrawableCents != 0 {
		t.Fatalf("source recovery changed bank reservation: %+v %v", balance, err)
	}
}
