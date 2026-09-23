package payments

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

type lateRefundReadFailure struct {
	*refundReadRuntime
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *lateRefundReadFailure) ReadProductRefunds(ctx context.Context, _ RefundReadRequest) ([]RefundObservation, error) {
	r.once.Do(func() { close(r.entered) })
	select {
	case <-r.release:
		return nil, newProviderFailure("payment_authentication", 0)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestProductRefundCheckLateFailureCannotFailReplacement(t *testing.T) {
	for _, saved := range []bool{false, true} {
		name := "replacement_reading"
		if saved {
			name = "replacement_observed"
		}
		t.Run(name, func(t *testing.T) {
			pool, service, runtime, checkout, _ := automaticRefundFixture(t, true)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			blocked := &lateRefundReadFailure{refundReadRuntime: runtime, entered: make(chan struct{}), release: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(blocked.release) }) }
			defer release()
			service.runtimes = NewRuntimeCatalog(blocked)
			history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
			if err != nil {
				t.Fatal(err)
			}
			history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
			if err != nil {
				t.Fatal(err)
			}
			repo := jobs.NewRepository(pool)
			previous, err := repo.Claim(ctx, "old-refund-reader", time.Minute)
			if err != nil || previous.Kind != ProductRefundCheckJobKind {
				t.Fatal(previous, err)
			}
			done := make(chan error, 1)
			go func() { done <- service.HandleProductRefundCheckJob(ctx, previous) }()
			select {
			case <-blocked.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// Simulate a paused process losing its lease. All mutation is confined to
			// this test schema; reclaim and the replacement claim use real job logic.
			quarantineExec(t, pool, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, previous.ID)
			if err = repo.RecoverExpired(ctx); err != nil {
				t.Fatal(err)
			}
			replacement, err := repo.Claim(ctx, "new-refund-reader", time.Minute)
			if err != nil || replacement.ID != previous.ID || replacement.LeaseToken == previous.LeaseToken || replacement.Attempts != previous.Attempts+1 {
				t.Fatal(replacement, err)
			}
			expected := "requested"
			if saved {
				var request RefundReadRequest
				if err = pool.QueryRow(ctx, `SELECT id,resource_id,provider_payment_id,amount_cents,currency,live_mode FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&request.PaymentID, &request.ResourceID, &request.ProviderPaymentID, &request.AmountCents, &request.Currency, &request.LiveMode); err != nil {
					t.Fatal(err)
				}
				if err = service.saveRefundObservations(ctx, history.LatestCheck.ID, request, runtime.observations); err != nil {
					t.Fatal(err)
				}
				expected = "observed"
			}
			release()
			var oldError error
			select {
			case oldError = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if oldError == nil {
				t.Fatal("old provider failure disappeared")
			}
			if err = repo.Fail(ctx, previous, "old-refund-reader", oldError); !errors.Is(err, jobs.ErrLeaseLost) {
				t.Fatal("old job changed replacement execution", err)
			}
			detail, err := service.GetRefundCheck(ctx, checkout.PaymentID, history.LatestCheck.ID)
			if err != nil || detail.Status != expected || detail.ErrorCode == nil || *detail.ErrorCode != "worker_lease_expired" {
				t.Fatalf("late failure overwrote replacement result: status=%s expected=%s error=%v readErr=%v", detail.Status, expected, detail.ErrorCode, err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE id=$1 AND error_code IS NULL`, 1, history.LatestCheck.ID)
			if err = service.HandleProductRefundCheckJob(ctx, previous); !errors.Is(err, jobs.ErrLeaseLost) {
				t.Fatal("stale execution was admitted again", err)
			}
			// A genuine replacement can finish the stored result or perform its read.
			service.runtimes = NewRuntimeCatalog(runtime)
			if err = service.HandleProductRefundCheckJob(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			if err = repo.Complete(ctx, replacement, "new-refund-reader"); err != nil {
				t.Fatal(err)
			}
			assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
			detail, err = service.GetRefundCheck(ctx, checkout.PaymentID, history.LatestCheck.ID)
			if err != nil || detail.Status != "completed" || detail.ErrorCode != nil {
				t.Fatal("replacement could not recover", detail, err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='refund.confirmed'`, 1, checkout.PaymentID)
			if len(runtime.operations) != 1 {
				t.Fatal("query recovery repeated refund", runtime.operations)
			}
		})
	}
}

func TestProductRefundCheckFailureAfterLockWaitNeedsLiveLease(t *testing.T) {
	pool, service, _, checkout, _ := automaticRefundFixture(t, true)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	repo := jobs.NewRepository(pool)
	job, err := repo.Claim(ctx, "expiring-refund-failure", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var expires time.Time
	if err = pool.QueryRow(ctx, `SELECT lease_expires_at FROM jobs WHERE id=$1`, job.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var blocker int32
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM product_refund_checks WHERE id=$1 FOR UPDATE`, history.LatestCheck.ID).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	type result struct {
		changed bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		changed, err := service.failRefundCheckExecution(ctx, refundCheckExecution{jobID: job.ID, status: "running", attempts: job.Attempts, leaseToken: &job.LeaseToken}, history.LatestCheck.ID, "requested", "payment_authentication")
		done <- result{changed, err}
	}()
	waitForProductBlockingTx(t, ctx, pool, blocker)
	var live bool
	if err = pool.QueryRow(ctx, `SELECT clock_timestamp()<$1`, expires).Scan(&live); err != nil || !live {
		t.Fatal("fixture lease expired before the lock wait was observed", err)
	}
	timer := time.NewTimer(max(time.Until(expires)+25*time.Millisecond, 0))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.changed {
			t.Fatal("expired failure changed the check after waiting for its row", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE id=$1 AND status='requested' AND error_code IS NULL`, 1, history.LatestCheck.ID)
}

func TestProductRefundCheckFailureWriteCanRetry(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	runtime := &refundReadRuntime{readError: newProviderFailure("payment_authentication", 0)}
	service, checkout, _, _, _ := fulfilledRefundFixture(t, pool, runtime)
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	repo := jobs.NewRepository(pool)
	job, err := repo.Claim(ctx, "refund-failure-write", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `CREATE FUNCTION reject_refund_failure_fixture() RETURNS trigger AS $$
 BEGIN RAISE EXCEPTION 'isolated failure-write fixture'; END; $$ LANGUAGE plpgsql;
 CREATE TRIGGER reject_refund_failure_fixture BEFORE UPDATE ON product_refund_checks
 FOR EACH ROW WHEN (NEW.status='failed') EXECUTE FUNCTION reject_refund_failure_fixture()`)
	failure := service.HandleProductRefundCheckJob(ctx, job)
	if failure == nil || !jobs.ShouldRetry(failure) {
		t.Fatal("failed state persistence was silently treated as terminal", failure)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE id=$1 AND status='requested' AND error_code IS NULL`, 1, history.LatestCheck.ID)
	if err = repo.Fail(ctx, job, "refund-failure-write", failure); err != nil {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `DROP TRIGGER reject_refund_failure_fixture ON product_refund_checks; DROP FUNCTION reject_refund_failure_fixture()`)
	quarantineExec(t, pool, `UPDATE jobs SET available_at=now() WHERE id=$1`, job.ID)
	retry, err := repo.Claim(ctx, "refund-failure-retry", time.Minute)
	if err != nil || retry.ID != job.ID {
		t.Fatal(retry, err)
	}
	failure = service.HandleProductRefundCheckJob(ctx, retry)
	if failure == nil || jobs.ShouldRetry(failure) || failure.Error() != "payment_authentication" {
		t.Fatal("original failure did not complete after database recovery", failure)
	}
	if err = repo.Fail(ctx, retry, "refund-failure-retry", failure); err != nil {
		t.Fatal(err)
	}
	detail, err := service.GetRefundCheck(ctx, checkout.PaymentID, history.LatestCheck.ID)
	if err != nil || detail.Status != "failed" || detail.ErrorCode == nil || *detail.ErrorCode != "payment_authentication" {
		t.Fatal(detail, err)
	}
	if runtime.reads != 2 || len(runtime.operations) != 0 {
		t.Fatal("unexpected financial operation", runtime.reads, runtime.operations)
	}
}
