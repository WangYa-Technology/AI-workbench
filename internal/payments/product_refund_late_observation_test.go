package payments

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgconn"
)

type lateRefundReadObservation struct {
	*refundReadRuntime
	entered chan struct{}
	release chan struct{}
	partial bool
}

func (r *lateRefundReadObservation) ReadProductRefunds(ctx context.Context, input RefundReadRequest) ([]RefundObservation, error) {
	close(r.entered)
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	result := []RefundObservation{{ProviderID: "re_late_unbound", ProviderPaymentID: input.ProviderPaymentID, AmountCents: 100, Currency: "USD", Status: "succeeded"}}
	if r.partial {
		return result, newProviderFailure("payment_timeout", 0)
	}
	return result, nil
}

func TestProductRefundLateObservationSurvivesReplacement(t *testing.T) {
	for _, scenario := range []struct{ partial, conflict bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		partial := scenario.partial
		name := "complete_read"
		if partial {
			name = "partial_read"
		}
		if scenario.conflict {
			name += "_write_conflict"
		}
		t.Run(name, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			runtime := &refundReadRuntime{}
			blocked := &lateRefundReadObservation{refundReadRuntime: runtime, entered: make(chan struct{}), release: make(chan struct{}), partial: partial}
			var once sync.Once
			release := func() { once.Do(func() { close(blocked.release) }) }
			defer release()
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, blocked)
			history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
			if err != nil {
				t.Fatal(err)
			}
			history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
			if err != nil {
				t.Fatal(err)
			}
			originalID := history.LatestCheck.ID
			repo := jobs.NewRepository(pool)
			oldJob, err := repo.Claim(ctx, "old-refund-observer", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- service.HandleProductRefundCheckJob(ctx, oldJob) }()
			select {
			case <-blocked.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			quarantineExec(t, pool, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, oldJob.ID)
			if err = repo.RecoverExpired(ctx); err != nil {
				t.Fatal(err)
			}
			replacement, err := repo.Claim(ctx, "new-refund-observer", time.Minute)
			if err != nil || replacement.ID != oldJob.ID || replacement.LeaseToken == oldJob.LeaseToken {
				t.Fatal(replacement, err)
			}
			current := newPaymentTestService(t, pool, service.config, NewRuntimeCatalog(runtime))
			if err = current.HandleProductRefundCheckJob(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			if err = repo.Complete(ctx, replacement, "new-refund-observer"); err != nil {
				t.Fatal(err)
			}
			if scenario.conflict {
				// This committed payment update races with the old reader's
				// SERIALIZABLE snapshot. The first evidence write must abort.
				gate, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer gate.Rollback(ctx)
				if _, err = gate.Exec(ctx, `UPDATE payment_intents SET version=version+1 WHERE id=$1`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				release()
				waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
				if err = gate.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				release()
			}
			select {
			case lateErr := <-done:
				var conflict *pgconn.PgError
				if errors.As(lateErr, &conflict) && conflict.Code == "40001" {
					t.Fatalf("verified late observation lost after transaction conflict: %s", conflict.Code)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// The replacement's empty result stays immutable; the independently
			// authenticated observation must nevertheless protect the transaction.
			original, err := current.GetRefundCheck(ctx, checkout.PaymentID, originalID)
			if err != nil || original.Status != "completed" || len(original.Observations) != 0 {
				t.Fatal("replacement evidence changed", original, err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_observation_review WHERE payment_id=$1`, 1, checkout.PaymentID)
			order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
			if err != nil || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" {
				t.Fatal("late refund lost its financial gate", order, err)
			}
			if original.LateReceiptCount != 1 {
				t.Fatal("receipt hidden from history", original)
			}
			receipts, err := current.ListRefundReadReceipts(ctx, checkout.PaymentID, originalID, "")
			if err != nil || len(receipts.Items) != 1 || receipts.NextCursor != nil || receipts.Items[0].Complete == partial || len(receipts.Items[0].Observations) != 1 || len(receipts.Items[0].UnresolvedProviderRefundIDs) != 1 {
				t.Fatal("late receipt unavailable", receipts, err)
			}
			receiptID := receipts.Items[0].ID
			// A real lease from another attempt cannot be relabelled as the
			// source of these observations, even when it belongs to this job.
			if _, err = pool.Exec(ctx, `INSERT INTO product_refund_read_receipts(id,check_id,attempt_number,lease_token,complete,error_code,observations,evidence_sha256)
 SELECT gen_random_uuid(),check_id,attempt_number+1,lease_token,complete,error_code,observations,evidence_sha256
 FROM product_refund_read_receipts WHERE id=$1`, receiptID); err == nil {
				t.Fatal("receipt accepted a mismatched execution")
			}
			for _, query := range []string{`UPDATE product_refund_read_receipts SET complete=NOT complete WHERE id=$1`, `DELETE FROM product_refund_read_receipts WHERE id=$1`} {
				if _, err = pool.Exec(ctx, query, receiptID); err == nil {
					t.Fatal("late evidence was mutable")
				}
			}
			var request RefundReadRequest
			if err = pool.QueryRow(ctx, `SELECT id,resource_id,provider_payment_id,amount_cents,currency,live_mode FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&request.PaymentID, &request.ResourceID, &request.ProviderPaymentID, &request.AmountCents, &request.Currency, &request.LiveMode); err != nil {
				t.Fatal(err)
			}
			var readErr error
			if partial {
				readErr = newProviderFailure("payment_timeout", 0)
			}
			if _, err = current.saveRefundReadResult(ctx, originalID, request, receipts.Items[0].Observations, readErr, &refundCheckExecution{jobID: oldJob.ID, status: "running", attempts: oldJob.Attempts, leaseToken: &oldJob.LeaseToken}); err != nil {
				t.Fatal(err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_read_receipts WHERE check_id=$1`, 1, originalID)
			quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.refund_read_receipt_saved' AND resource_id=$1`, 1, checkout.PaymentID)
			down, err := os.ReadFile("../platform/database/migrations/0127_product_refund_read_receipts.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard late refund read receipts") {
				t.Fatal("rollback discarded late evidence", err)
			}
			// A new complete empty query still cannot erase the late refund.
			history, err = current.RefundHistory(ctx, checkout.PaymentID, "", 20)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = current.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion); err != nil {
				t.Fatal(err)
			}
			runAutomaticRefundCheck(t, current)
			if _, err = pool.Exec(ctx, `INSERT INTO product_refund_read_receipts(id,check_id,attempt_number,lease_token,complete,error_code,observations,evidence_sha256)
 SELECT gen_random_uuid(),c.id,r.attempt_number,r.lease_token,r.complete,r.error_code,r.observations,r.evidence_sha256
 FROM product_refund_read_receipts r JOIN product_refund_checks c ON c.payment_id=$2 AND c.id<>r.check_id
 WHERE r.id=$1`, receiptID, checkout.PaymentID); err == nil {
				t.Fatal("receipt accepted a lease from a different check")
			}
			if _, err = current.BeginProductRefund(ctx, buyer, checkout.OrderID, "late-evidence-refund", "test", "The delivered content differs from the description."); !errors.Is(err, ErrRefundConflict) {
				t.Fatal("late evidence allowed a refund", err)
			}
			pkg, body := runProductExport(t, pool, buyer)
			exported := pkg.Data.Marketplace.Data["refundReadReceipts"]
			if len(exported) != 1 || exported[0]["checkId"] != originalID.String() || !strings.Contains(string(body), "re_late_unbound") || strings.Contains(string(body), oldJob.LeaseToken.String()) {
				t.Fatal("receipt export missing or leaked private lease", exported)
			}
			var seller uuid.UUID
			if err = pool.QueryRow(ctx, `SELECT p.seller_id FROM products p JOIN orders o ON o.product_id=p.id WHERE o.id=$1`, checkout.OrderID).Scan(&seller); err != nil {
				t.Fatal(err)
			}
			sellerPackage, sellerBody := runProductExport(t, pool, seller)
			if len(sellerPackage.Data.Marketplace.Data["refundReadReceipts"]) != 0 || strings.Contains(string(sellerBody), "re_late_unbound") {
				t.Fatal("seller export exposed buyer refund observations")
			}
			quarantineExec(t, pool, `UPDATE users SET status='deleted' WHERE id=$1`, buyer)
			snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
			if err != nil {
				t.Fatal(err)
			}
			var cleanupJob jobs.Job
			if err = pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,jsonb_build_object('orderId',$2::text)) RETURNING id,kind,payload`, productdelivery.CleanupJobKind, checkout.OrderID).Scan(&cleanupJob.ID, &cleanupJob.Kind, &cleanupJob.Payload); err != nil {
				t.Fatal(err)
			}
			if err = productdelivery.CleanupHandler(pool, current.config.MediaStores)(ctx, cleanupJob); err != nil {
				t.Fatal(err)
			}
			store, err := current.config.MediaStores.Get(snapshot.Backend)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Stat(ctx, snapshot.Key); err != nil {
				t.Fatal("late receipt failed to retain bytes", err)
			}
			if len(runtime.operations) != 0 {
				t.Fatal("observation recovery sent a refund", runtime.operations)
			}
		})
	}
}
