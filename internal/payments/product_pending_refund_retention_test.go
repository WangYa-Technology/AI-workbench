package payments

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgxpool"
)

func applyFundsMediaRetentionMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	body, err := os.ReadFile("../platform/database/migrations/0126_product_funds_media_retention." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
}

func TestProductFundsMediaRetentionMigrationRoundTrip(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	applyFundsMediaRetentionMigration(t, pool, "down")
	applyFundsMediaRetentionMigration(t, pool, "up")
}

func assertUnsettledProductDeliveryRetained(t *testing.T, pool *pgxpool.Pool, service *Service, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	snapshot, err := productdelivery.Load(ctx, pool, orderID)
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err = pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,jsonb_build_object('orderId',$2::text)) RETURNING id,kind,payload`, productdelivery.CleanupJobKind, orderID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err = productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, job); err != nil {
		t.Fatal(err)
	}
	store, err := service.config.MediaStores.Get(snapshot.Backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stat(ctx, snapshot.Key); err != nil {
		t.Fatal("ordinary cleanup ignored unresolved financial evidence", err)
	}
}

func TestPendingHistoricalRefundRetainsOriginalAfterBothAccountsDeleted(t *testing.T) {
	for _, kind := range []string{"contract", "external"} {
		t.Run(kind, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := t.Context()
			// Static pre-snapshot evidence, including a historical purchase with
			// only an asset-origin link and no frozen contract. Do not invent one.
			f := legacyAccessFixture(t, pool, kind)
			var seller uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, f.source).Scan(&seller); err != nil {
				t.Fatal(err)
			}
			quarantineExec(t, pool, `UPDATE payment_intents SET status='refund_pending' WHERE order_id=$1`, f.order)
			quarantineExec(t, pool, `UPDATE orders SET status='refund_requested' WHERE id=$1`, f.order)
			deleteMarketplaceAccount(t, pool, f.root, seller)
			deleteMarketplaceAccount(t, pool, f.root, f.buyer)
			if _, err := media.NewLocalStore(f.root).Stat(ctx, f.source.String()+".jpg"); err != nil {
				t.Fatal("historical original lost while refund was unresolved", err)
			}
			if _, err := assets.NewService(pool, f.root).Content(ctx, f.buyer, f.purchased); err == nil {
				t.Fatal("financial evidence retention restored deleted buyer access")
			}
		})
	}
}

func TestPendingRefundRetainsDeliveryAcrossBuyerCleanup(t *testing.T) {
	for _, path := range []string{"account_deletion", "ordinary_cleanup", "account_deletion_failed", "ordinary_cleanup_failed"} {
		t.Run(path, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := t.Context()
			service, checkout, buyer, product, now := fulfilledRefundFixture(t, pool, &productCheckoutRuntime{})
			snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
			if err != nil {
				t.Fatal(err)
			}
			store, err := service.config.MediaStores.Get(snapshot.Backend)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.BeginProductRefund(ctx, buyer, checkout.OrderID, "pending-retention-refund", "test", "The purchased resource does not match my requirements."); err != nil {
				t.Fatal(err)
			}
			if err = service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err != nil {
				t.Fatal(err)
			}
			var cleanupJob jobs.Job
			if err = pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,jsonb_build_object('orderId',$2::text)) RETURNING id,kind,payload`, productdelivery.CleanupJobKind, checkout.OrderID).Scan(&cleanupJob.ID, &cleanupJob.Kind, &cleanupJob.Payload); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(path, "account_deletion") {
				deleteMarketplaceAccount(t, pool, paymentTestRoot(t, pool), buyer)
			} else {
				// Model an already deleted buyer while a previously queued ordinary
				// cleanup resumes. This is not a resolved-order reconciliation job.
				quarantineExec(t, pool, `UPDATE users SET status='deleted' WHERE id=$1`, buyer)
				if err = productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, cleanupJob); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = store.Stat(ctx, snapshot.Key); err != nil {
				t.Fatalf("cleanup erased delivery while the original refund remained pending: %v", err)
			}
			down, err := os.ReadFile("../platform/database/migrations/0126_product_funds_media_retention.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot remove unresolved product funds media protection") {
				t.Fatal("rollback discarded unsettled funds retention", err)
			}
			// Retention is not permanent: a verified original refund result lets
			// the same physical cleanup path remove the unneeded copy.
			outcome := "succeeded"
			if strings.HasSuffix(path, "_failed") {
				outcome = "failed"
			}
			receipt := receivePaymentWorkflowEvent(t, service, productRefundEvent("evt_pending_retention_done", "re_workflow123", outcome, checkout.PaymentID, product, now.Unix(), checkout.AmountCents), now)
			if err = service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID))}); err != nil {
				t.Fatal(err)
			}
			if err = productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, cleanupJob); err != nil {
				t.Fatal(err)
			}
			if _, err = store.Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
				t.Fatal("confirmed refund did not release delivery cleanup", err)
			}
			applyFundsMediaRetentionMigration(t, pool, "down")
			applyFundsMediaRetentionMigration(t, pool, "up")
		})
	}
}
