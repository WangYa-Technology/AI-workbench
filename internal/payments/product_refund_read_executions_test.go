package payments

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

func TestProductRefundReadExecutionLostResponseRecovery(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(t.Context(), 55*time.Second)
	defer cancel()
	runtime := &refundReadRuntime{}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
	requestCheck := func() uuid.UUID {
		t.Helper()
		h, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
		if err != nil {
			t.Fatal(err)
		}
		h, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, h.PaymentVersion)
		if err != nil {
			t.Fatal(err)
		}
		return h.LatestCheck.ID
	}
	original := requestCheck()
	repo := jobs.NewRepository(pool)
	old, err := repo.Claim(ctx, "lost-refund-reader", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The provider returned, but the database cannot persist that response. The
	// process then disappears without failing/completing its job lease.
	quarantineExec(t, pool, `CREATE FUNCTION reject_read_result_test() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected response write failure'; END; $$ LANGUAGE plpgsql;
 CREATE TRIGGER reject_read_result_test BEFORE UPDATE OF observations ON product_refund_checks FOR EACH ROW EXECUTE FUNCTION reject_read_result_test()`)
	if err = service.HandleProductRefundCheckJob(ctx, old); err == nil {
		t.Fatal("failed response write was accepted")
	}
	var readID uuid.UUID
	var deadline time.Time
	if err = pool.QueryRow(ctx, `SELECT id,read_deadline FROM product_refund_read_executions WHERE check_id=$1 AND recorded_at IS NULL`, original).Scan(&readID, &deadline); err != nil {
		t.Fatal("remote read had no durable registration", err)
	}
	quarantineExec(t, pool, `DROP TRIGGER reject_read_result_test ON product_refund_checks`)
	quarantineExec(t, pool, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, old.ID)
	if err = repo.RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := repo.Claim(ctx, "replacement-refund-reader", time.Minute)
	if err != nil || current.ID != old.ID {
		t.Fatal(current, err)
	}
	if err = service.HandleProductRefundCheckJob(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(ctx, current, "replacement-refund-reader"); err != nil {
		t.Fatal(err)
	}
	detail, err := service.GetRefundCheck(ctx, checkout.PaymentID, original)
	if err != nil || detail.Status != "completed" || len(detail.Observations) != 0 || detail.UnrecordedReadCount != 1 {
		t.Fatal("replacement erased missing response", detail, err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_funds_review WHERE payment_id=$1`, 0, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_observation_review WHERE payment_id=$1`, 0, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_review WHERE payment_id=$1`, 1, checkout.PaymentID)
	order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
	if err != nil || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" {
		t.Fatal("unknown response allowed refund", order, err)
	}
	if _, err = service.BeginProductRefund(ctx, buyer, checkout.OrderID, "missing-read-refund", "test", "The delivered content differs from the description."); !errors.Is(err, ErrRefundConflict) {
		t.Fatal(err)
	}
	list, err := service.ListRefundChecks(ctx, checkout.PaymentID, "unresolved", "", 20)
	if err != nil || len(list.Items) != 1 || !list.Items[0].RequiresReview {
		t.Fatal("missing read absent from review", list, err)
	}
	pkg, body := runProductExport(t, pool, buyer)
	if len(pkg.Data.Marketplace.Data["refundChecks"]) != 1 || !strings.Contains(string(body), readID.String()) || strings.Contains(string(body), old.LeaseToken.String()) {
		t.Fatal("missing read export absent or leaked lease")
	}
	exportJob, err := repo.Claim(ctx, "read-evidence-export", time.Minute)
	if err != nil || exportJob.Kind != "data_rights.export" {
		t.Fatal(exportJob, err)
	}
	if err = repo.Complete(ctx, exportJob, "read-evidence-export"); err != nil {
		t.Fatal(err)
	}
	// Access is revoked independently; unresolved query evidence retains bytes.
	quarantineExec(t, pool, `UPDATE users SET status='deleted' WHERE id=$1`, buyer)
	snap, err := productdelivery.Load(ctx, pool, checkout.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	var cleanupJob jobs.Job
	if err = pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,jsonb_build_object('orderId',$2::text)) RETURNING id,kind,payload`, productdelivery.CleanupJobKind, checkout.OrderID).Scan(&cleanupJob.ID, &cleanupJob.Kind, &cleanupJob.Payload); err != nil {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE jobs SET available_at=now()-interval '1 day' WHERE id=$1`, cleanupJob.ID)
	claimedCleanup, err := repo.Claim(ctx, "retained-read-cleanup", time.Minute)
	if err != nil || claimedCleanup.ID != cleanupJob.ID {
		t.Fatal(claimedCleanup, err)
	}
	cleanupJob = claimedCleanup
	if err = productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, cleanupJob); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(ctx, cleanupJob, "retained-read-cleanup"); err != nil {
		t.Fatal(err)
	}
	store, err := service.config.MediaStores.Get(snap.Backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stat(ctx, snap.Key); err != nil {
		t.Fatal("missing response lost delivery", err)
	}
	// No fake clock: a newer complete remote read must start after both original
	// budgets. This also verifies the absolute dispatch deadline is persisted.
	timer := time.NewTimer(max(time.Until(deadline.Add(5*time.Second))+50*time.Millisecond, 0))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertOperationalMetric(t, pool, "problem", "refund_read_unrecorded", "test", 1, 0, 0)
	// The same existing maintenance scan now sees a missing read without any
	// local refund attempt. Run its due time forward; the new read uses real time.
	n, err := service.reconcileProductRefunds(ctx, 100, time.Now().Add(48*time.Hour))
	if err != nil || n != 1 {
		t.Fatal("missing response not scheduled", n, err)
	}
	quarantineExec(t, pool, `UPDATE jobs SET available_at=now()-interval '1 day' WHERE kind=$1 AND status='queued'`, ProductRefundCheckJobKind)
	runAutomaticRefundCheck(t, service)
	detail, err = service.GetRefundCheck(ctx, checkout.PaymentID, original)
	if err != nil || detail.UnrecordedReadCount != 0 || detail.RecoveredReadCount != 1 || len(detail.Observations) != 0 {
		t.Fatal("complete query did not reconcile missing response", detail, err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_review WHERE payment_id=$1`, 0, checkout.PaymentID)
	assertOperationalMetric(t, pool, "problem", "refund_read_unrecorded", "test", 0, 0, 0)
	quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.refund_read_recovered' AND resource_id=$1`, 1, checkout.PaymentID)
	var resumedCleanup jobs.Job
	if err = pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,available_at) VALUES($1,$2,now()-interval '1 day') RETURNING id,kind,payload`, productdelivery.CleanupJobKind, cleanupJob.Payload).Scan(&resumedCleanup.ID, &resumedCleanup.Kind, &resumedCleanup.Payload); err != nil {
		t.Fatal(err)
	}
	claimedCleanup, err = repo.Claim(ctx, "resolved-read-cleanup", time.Minute)
	if err != nil || claimedCleanup.ID != resumedCleanup.ID {
		t.Fatal(claimedCleanup, err)
	}
	if err = productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, claimedCleanup); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(ctx, claimedCleanup, "resolved-read-cleanup"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stat(ctx, snap.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("resolved read left unneeded copy", err)
	}
	for _, q := range []string{`UPDATE product_refund_read_executions SET started_at=started_at-interval '1 day' WHERE id=$1`, `DELETE FROM product_refund_read_executions WHERE id=$1`, `DELETE FROM product_refund_read_recoveries WHERE execution_id=$1`} {
		if _, err = pool.Exec(ctx, q, readID); err == nil {
			t.Fatal("read evidence mutable")
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0128_product_refund_read_executions.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard refund read execution evidence") {
		t.Fatal("downgrade erased execution evidence", err)
	}
	if len(runtime.operations) != 0 {
		t.Fatal("read recovery sent a refund", runtime.operations)
	}
}

func TestProductRefundReadExecutionRegistrationFailureStopsRemote(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	runtime := &refundReadRuntime{}
	service, checkout, _, _, _ := fulfilledRefundFixture(t, pool, runtime)
	h, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	h, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, h.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `CREATE FUNCTION reject_read_registration_test() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected registration failure'; END; $$ LANGUAGE plpgsql;
 CREATE TRIGGER reject_read_registration_test BEFORE INSERT ON product_refund_read_executions FOR EACH ROW EXECUTE FUNCTION reject_read_registration_test()`)
	repo := jobs.NewRepository(pool)
	job, err := repo.Claim(ctx, "registration-failure", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.HandleProductRefundCheckJob(ctx, job); err == nil || runtime.reads != 0 {
		t.Fatal("unregistered remote read", runtime.reads, err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_read_executions WHERE check_id=$1`, 0, h.LatestCheck.ID)
	quarantineExec(t, pool, `DROP TRIGGER reject_read_registration_test ON product_refund_read_executions`)
	if err = service.HandleProductRefundCheckJob(ctx, job); err != nil || runtime.reads != 1 {
		t.Fatal("registered retry failed", runtime.reads, err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_read_executions WHERE check_id=$1 AND recorded_at IS NOT NULL AND complete`, 1, h.LatestCheck.ID)
}

func TestProductRefundReadRecoveryProofAndLateEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	service, checkout, _, _, _ := fulfilledRefundFixture(t, pool, &refundReadRuntime{})
	h, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	h, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, h.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	checkID := h.LatestCheck.ID
	runAutomaticRefundCheck(t, service)
	var payment string
	if err = pool.QueryRow(ctx, `SELECT provider_payment_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&payment); err != nil {
		t.Fatal(err)
	}
	makeRead := func(age time.Duration, complete *bool) uuid.UUID {
		t.Helper()
		id := uuid.New()
		start := time.Now().UTC().Add(-age)
		quarantineExec(t, pool, `INSERT INTO product_refund_read_executions(id,check_id,attempt_number,started_at,read_deadline) VALUES($1,$2,0,$3,$4)`, id, checkID, start, start.Add(20*time.Second))
		if complete != nil {
			var code *string
			if !*complete {
				v := "payment_timeout"
				code = &v
			}
			quarantineExec(t, pool, `UPDATE product_refund_read_executions SET recorded_at=clock_timestamp(),complete=$2,error_code=$3 WHERE id=$1`, id, *complete, code)
		}
		return id
	}
	yes, no := true, false
	lost := makeRead(time.Minute, nil)
	for _, candidate := range []uuid.UUID{makeRead(50*time.Second, &yes), makeRead(0, &no), makeRead(0, nil)} {
		if _, err = pool.Exec(ctx, `INSERT INTO product_refund_read_recoveries(execution_id,recovery_execution_id) VALUES($1,$2)`, lost, candidate); err == nil {
			t.Fatal("incomplete or early query accepted as recovery")
		}
	}
	recent := makeRead(0, nil)
	complete := makeRead(0, &yes)
	if _, err = pool.Exec(ctx, `INSERT INTO product_refund_read_recoveries(execution_id,recovery_execution_id) VALUES($1,$2)`, recent, complete); err == nil {
		t.Fatal("active time window recovered")
	}
	liveHistory, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	liveHistory, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, liveHistory.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	liveJob, err := jobs.NewRepository(pool).Claim(ctx, "live-read-proof", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	liveRead := uuid.New()
	quarantineExec(t, pool, `INSERT INTO product_refund_read_executions(id,check_id,attempt_number,lease_token,started_at,read_deadline) VALUES($1,$2,$3,$4,clock_timestamp()-interval '1 minute',clock_timestamp()-interval '40 seconds')`, liveRead, liveHistory.LatestCheck.ID, liveJob.Attempts, liveJob.LeaseToken)
	if _, err = pool.Exec(ctx, `INSERT INTO product_refund_read_recoveries(execution_id,recovery_execution_id) VALUES($1,$2)`, liveRead, complete); err == nil {
		t.Fatal("live execution lease was ignored")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO product_refund_read_executions(id,check_id,attempt_number,lease_token) VALUES($1,$2,$3,$4)`, uuid.New(), checkID, liveJob.Attempts, liveJob.LeaseToken); err == nil {
		t.Fatal("foreign execution registered under original check")
	}
	// A fully captured response under another payment is never recovery proof.
	foreignPayment := operationalPaymentFixture(t, pool, false, time.Now())
	foreignCheck, foreignRead := uuid.New(), uuid.New()
	var foreignJob uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status) VALUES($1,'{}','succeeded') RETURNING id`, ProductRefundCheckJobKind).Scan(&foreignJob); err != nil {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `INSERT INTO product_refund_checks(id,payment_id,job_id,status,origin) VALUES($1,$2,$3,'completed','automatic')`, foreignCheck, foreignPayment, foreignJob)
	quarantineExec(t, pool, `INSERT INTO product_refund_read_executions(id,check_id,attempt_number) VALUES($1,$2,0)`, foreignRead, foreignCheck)
	quarantineExec(t, pool, `UPDATE product_refund_read_executions SET recorded_at=clock_timestamp(),complete=true WHERE id=$1`, foreignRead)
	if _, err = pool.Exec(ctx, `INSERT INTO product_refund_read_recoveries(execution_id,recovery_execution_id) VALUES($1,$2)`, lost, foreignRead); err == nil {
		t.Fatal("cross-payment recovery accepted")
	}
	quarantineExec(t, pool, `INSERT INTO product_refund_read_recoveries(execution_id,recovery_execution_id) VALUES($1,$2)`, lost, complete)
	if _, err = pool.Exec(ctx, `UPDATE product_refund_read_recoveries SET recovery_execution_id=$2 WHERE execution_id=$1`, lost, foreignRead); err == nil {
		t.Fatal("recovery proof changed")
	}
	// A recovered missing response is not overwritten. If its actual verified
	// positive observation arrives later it still enters the independent gate.
	observation := []RefundObservation{{ProviderID: "re_after_read_recovery", ProviderPaymentID: payment, AmountCents: 100, Currency: "USD", Status: "succeeded"}}
	request := RefundReadRequest{PaymentID: checkout.PaymentID, ProviderPaymentID: payment, AmountCents: 1900, Currency: "USD"}
	if _, err = service.saveRefundReadResult(ctx, checkID, request, observation, nil, &refundCheckExecution{readID: lost}); err != nil {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_observation_review WHERE payment_id=$1`, 1, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_read_recoveries WHERE execution_id=$1`, 1, lost)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_read_executions WHERE id=$1 AND recorded_at IS NOT NULL`, 1, lost)
}
