package payments

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func occupyMediaStages(t *testing.T) func() {
	t.Helper()
	var held []*media.StagedObject
	release := func() {
		for _, s := range held {
			_ = s.Close()
		}
		held = nil
	}
	t.Cleanup(release)
	for range 16 {
		s, err := media.Stage(t.Context(), bytes.NewBufferString("x"), 1)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, s)
	}
	return release
}

func TestProductCheckoutStageBudgetPreventsProviderDispatch(t *testing.T) {
	for _, capacity := range []bool{true, false} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			buyer, _, _, product := newProductCheckoutFixture(t, pool)
			runtime := &productCheckoutRuntime{}
			cfg := config.Config{MediaRoot: paymentTestRoot(t, pool)}
			release := func() {}
			want := error(media.ErrStageStorage)
			if capacity {
				release = occupyMediaStages(t)
				want = media.ErrStageBusy
			} else {
				cfg.MediaStage.TempDir = t.TempDir() + "/missing"
			}
			service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, MediaStores: media.NewCatalogFromConfig(cfg)}, NewRuntimeCatalog(runtime))
			version := productOfferVersion(t, pool, product)
			_, _, err := service.BeginProductCheckout(t.Context(), buyer, product, "stage-capacity-command", "test", "https://example.test/success", "https://example.test/cancel", true, version)
			if !errors.Is(err, want) || runtime.calls != 0 {
				t.Fatal("provider dispatched despite unavailable staging", err, runtime.calls)
			}
			var orders, payments int
			if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM orders WHERE buyer_id=$1),(SELECT count(*) FROM payment_intents WHERE payer_id=$1)`, buyer).Scan(&orders, &payments); err != nil || orders != 0 || payments != 0 {
				t.Fatal("failed reservation committed transaction", orders, payments, err)
			}
			release()
			cfg.MediaStage.TempDir = t.TempDir()
			service.config.MediaStores = media.NewCatalogFromConfig(cfg)
			if _, _, err := service.BeginProductCheckout(t.Context(), buyer, product, "stage-capacity-command", "retry", "https://example.test/success", "https://example.test/cancel", true, version); err != nil || runtime.calls != 1 {
				t.Fatal("capacity recovery failed", err, runtime.calls)
			}
		})
	}
}

func TestPaidProductStageBudgetRetriesWithoutRefundOrPrematureGrant(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	runtime := &productCheckoutRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	checkout, _, err := service.BeginProductCheckout(t.Context(), buyer, product, "paid-stage-command", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents)
	header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
	if _, err := service.ReceiveStripeWebhook(t.Context(), body, header); err != nil {
		t.Fatal(err)
	}
	repository := jobs.NewRepository(pool)
	job, err := repository.Claim(t.Context(), "stage-worker", time.Minute)
	if err != nil || job.Kind != PaymentEventJobKind {
		t.Fatal(job, err)
	}
	release := occupyMediaStages(t)
	err = service.HandlePaymentEventJob(t.Context(), job)
	if !errors.Is(err, media.ErrStageBusy) || !jobs.ShouldRetry(err) {
		t.Fatal("transient capacity did not remain retryable", err)
	}
	var rights, refunds int
	var reason, status string
	if err := pool.QueryRow(t.Context(), `SELECT pi.status,COALESCE(pi.compensation_reason,''),
 (SELECT count(*) FROM entitlements WHERE order_id=pi.order_id),
 (SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'paymentId'=pi.id::text)
 FROM payment_intents pi WHERE pi.id=$1`, checkout.PaymentID, ProductRefundJobKind).Scan(&status, &reason, &rights, &refunds); err != nil || rights != 0 || refunds != 0 || reason != "" || status == "refunded" || status == "refund_pending" {
		t.Fatal("capacity failure changed financial obligations", status, reason, rights, refunds, err)
	}
	if err := repository.Fail(t.Context(), job, "stage-worker", err); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET available_at=now() WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := repository.Claim(t.Context(), "stage-retry", time.Minute)
	if err != nil || retry.ID != job.ID {
		t.Fatal(retry, err)
	}
	if err := service.HandlePaymentEventJob(t.Context(), retry); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(t.Context(), retry, "stage-retry"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT status,(SELECT count(*) FROM entitlements WHERE order_id=$1 AND status='active') FROM orders WHERE id=$1`, checkout.OrderID).Scan(&status, &rights); err != nil || status != "fulfilled" || rights != 1 {
		t.Fatal(status, rights, err)
	}
}
