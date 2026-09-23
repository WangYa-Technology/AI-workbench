package payments

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgxpool"
)

func applyRefundObservationMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	if direction == "down" {
		applyRefundReadReceiptMigration(t, pool, "down")
		applyFundsMediaRetentionMigration(t, pool, "down")
	}
	body, err := os.ReadFile("../platform/database/migrations/0125_product_refund_observation_review." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
	if direction == "up" {
		applyFundsMediaRetentionMigration(t, pool, "up")
		applyRefundReadReceiptMigration(t, pool, "up")
	}
}

func TestProductRefundUnknownObservationSurvivesLaterChecks(t *testing.T) {
	for _, scenario := range []string{"failed_read", "empty_read"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := t.Context()
			runtime := &refundReadRuntime{}
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
			var providerPayment string
			if err := pool.QueryRow(ctx, `SELECT provider_payment_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&providerPayment); err != nil {
				t.Fatal(err)
			}
			runtime.observations = []RefundObservation{{ProviderID: "re_unbound_external", ProviderPaymentID: providerPayment, AmountCents: 100, Currency: "USD", Status: "succeeded"}}
			request := func() {
				t.Helper()
				history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
				if err != nil || !history.CanCheck {
					t.Fatal("funds query unavailable", history, err)
				}
				if _, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion); err != nil {
					t.Fatal(err)
				}
			}
			assertHeld := func() {
				t.Helper()
				assertOperationalMetric(t, pool, "problem", "refund_observation_unresolved", "test", 1, 0, 0)
				assertOperationalMetric(t, pool, "problem", "refund_observation_unresolved", "live", 0, 0, 0)
				order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
				if err != nil || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" {
					t.Fatalf("unresolved remote refund lost its shared gate: canRefund=%v reason=%s err=%v", order.CanRequestRefund, order.RefundUnavailableReason, err)
				}
				if _, err = service.BeginProductRefund(ctx, buyer, checkout.OrderID, "unknown-evidence-refund", "test", "The delivered content does not match the description."); !errors.Is(err, ErrRefundConflict) {
					t.Fatalf("unresolved remote refund permitted a new command: %v", err)
				}
			}
			request()
			runAutomaticRefundCheck(t, service)
			assertHeld()
			down, err := os.ReadFile("../platform/database/migrations/0125_product_refund_observation_review.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot remove unresolved refund observation protection") {
				t.Fatal("rollback removed unresolved financial protection", err)
			}
			request()
			if scenario == "failed_read" {
				runtime.readError = newProviderFailure("payment_authentication", 0)
				repo := jobs.NewRepository(pool)
				job, err := repo.Claim(ctx, "unknown-refund-check", time.Minute)
				if err != nil || job.Kind != ProductRefundCheckJobKind {
					t.Fatal(job, err)
				}
				err = service.HandleProductRefundCheckJob(ctx, job)
				if err == nil {
					t.Fatal("expected read failure")
				}
				if err = repo.Fail(ctx, job, "unknown-refund-check", err); err != nil {
					t.Fatal(err)
				}
			} else {
				runtime.observations = nil
				runAutomaticRefundCheck(t, service)
			}
			assertHeld()
			if scenario == "empty_read" {
				history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
				if err != nil || history.LatestCheck.UnresolvedCount != 1 {
					t.Fatal("new history hid prior unmatched observation", history, err)
				}
				// Once the buyer is deleted, ordinary entitlement retention no
				// longer applies. The financial evidence must protect the copy.
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
				var deletionJob jobs.Job
				if err = pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,jsonb_build_object('userId',$2::text)) RETURNING id,kind,payload`, datarights.MediaCleanupJobKind, buyer).Scan(&deletionJob.ID, &deletionJob.Kind, &deletionJob.Payload); err != nil {
					t.Fatal(err)
				}
				if err = datarights.NewService(pool, paymentTestRoot(t, pool)).HandleMediaCleanupJob(ctx, deletionJob); err != nil {
					t.Fatal(err)
				}
				store, err := service.config.MediaStores.Get(snapshot.Backend)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.Stat(ctx, snapshot.Key); err != nil {
					t.Fatal("cleanup erased unresolved refund evidence", err)
				}
			}
			if len(runtime.operations) != 0 {
				t.Fatal("read-only review dispatched a refund")
			}
		})
	}
}

func TestProductRefundObservationMigrationRoundTrip(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	applyRefundObservationMigration(t, pool, "down")
	applyRefundObservationMigration(t, pool, "up")
	assertOperationalMetric(t, pool, "problem", "refund_observation_unresolved", "live", 0, 0, 0)
}

func TestProductRefundObservationResolvedByBoundFundsEvidence(t *testing.T) {
	pool, service, runtime, checkout, _ := automaticRefundFixture(t, true)
	ctx := t.Context()
	operation := runtime.observations[0].OperationID
	// The provider omits metadata after the original dispatch reply was lost.
	// The same external refund is not yet bound to its original operation.
	runtime.observations[0].OperationID = nil
	request := func() {
		t.Helper()
		history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion); err != nil {
			t.Fatal(err)
		}
		runAutomaticRefundCheck(t, service)
	}
	request()
	assertOperationalMetric(t, pool, "problem", "refund_observation_unresolved", "test", 1, 0, 0)
	page, err := service.ListRefundChecks(ctx, checkout.PaymentID, "unresolved", "", 20)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("real unbound query absent from history", page, err)
	}
	originalCheck := page.Items[0].ID
	// Keep a real unresolved cursor across a later binding resolution. The
	// watermark must remain usable even after it leaves the filtered directory.
	request()
	page, err = service.ListRefundChecks(ctx, checkout.PaymentID, "unresolved", "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal("missing unresolved history cursor", page, err)
	}
	unresolvedCursor := *page.NextCursor
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err == nil || jobs.ShouldRetry(err) {
		t.Fatal("unresolved observation allowed refund redispatch", err)
	}
	// A later authenticated read supplies the real operation metadata. The
	// existing event handler binds it and applies success before releasing review.
	runtime.observations[0].OperationID = operation
	request()
	assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
	assertOperationalMetric(t, pool, "problem", "refund_observation_unresolved", "test", 0, 0, 0)
	page, err = service.ListRefundChecks(ctx, checkout.PaymentID, "unresolved", "", 20)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("resolved observations still marked pending", page, err)
	}
	page, err = service.ListRefundChecks(ctx, checkout.PaymentID, "unresolved", unresolvedCursor, 1)
	if err != nil || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatal("resolved watermark invalidated continued history lookup", page, err)
	}
	detail, err := service.GetRefundCheck(ctx, checkout.PaymentID, originalCheck)
	if err != nil || len(detail.Observations) != 1 || len(detail.UnresolvedProviderRefundIDs) != 0 || detail.UnresolvedCount == 0 {
		t.Fatal("current resolution rewrote original query evidence", detail, err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_review WHERE payment_id=$1`, 0, checkout.PaymentID)
	if runtime.reads != 3 || len(runtime.operations) != 1 {
		t.Fatal("reconciliation repeated money movement", runtime.reads, len(runtime.operations))
	}
}
