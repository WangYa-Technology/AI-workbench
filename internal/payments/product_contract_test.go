package payments

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestProductOfferChangesRequireAcceptanceBeforeCheckout(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	changes := map[string]string{
		"price":       `UPDATE products SET price_cents=price_cents+100 WHERE id=$1`,
		"description": `UPDATE products SET description='Revised included content' WHERE id=$1`,
		"files":       `UPDATE products SET included_files='["different-file.png"]' WHERE id=$1`,
		"terms":       `UPDATE licenses SET terms=terms||' Revised terms.' WHERE code=(SELECT license_code FROM products WHERE id=$1)`,
		"rights":      `UPDATE licenses SET allows_derivatives=NOT allows_derivatives WHERE code=(SELECT license_code FROM products WHERE id=$1)`,
		"storage":     `UPDATE assets SET storage_key='replacement.png' WHERE id=(SELECT asset_id FROM products WHERE id=$1)`,
	}
	for name, statement := range changes {
		t.Run(name, func(t *testing.T) {
			buyer, _, _, product := newProductCheckoutFixture(t, pool)
			version := productOfferVersion(t, pool, product)
			if _, err := pool.Exec(ctx, statement, product); err != nil {
				t.Fatal(err)
			}
			runtime := &guardedProductRuntime{}
			service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
			if _, _, err := service.BeginProductCheckout(ctx, buyer, product, "stale-offer-command", "test", "https://example.test/success", "https://example.test/cancel", true, version); !errors.Is(err, ErrOfferChanged) {
				t.Fatalf("stale offer accepted: %v", err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE buyer_id=$1`, buyer).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 || runtime.calls.Load() != 0 {
				t.Fatal("stale acceptance created an order or provider checkout")
			}
			if name == "storage" {
				if err := media.NewLocalStore(paymentTestRoot(t, pool)).Put(ctx, "replacement.png", []byte("new offered bytes"), "image/jpeg"); err != nil {
					t.Fatal(err)
				}
			}
			current := productOfferVersion(t, pool, product)
			if _, _, err := service.BeginProductCheckout(ctx, buyer, product, "stale-offer-command", "test", "https://example.test/success", "https://example.test/cancel", true, current); err != nil {
				t.Fatalf("freshly accepted offer rejected: %v", err)
			}
		})
	}
}

func TestPurchasedContractControlsSourceMaskAndPrivateContent(t *testing.T) {
	for _, allowsDerivatives := range []bool{true, false} {
		t.Run(fmt.Sprintf("derivatives_%t", allowsDerivatives), func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			buyer, seller, source, product := newProductCheckoutFixture(t, pool)
			if _, err := pool.Exec(ctx, `UPDATE licenses SET allows_derivatives=$1 WHERE code='hcai-commercial-standard-v1'`, allowsDerivatives); err != nil {
				t.Fatal(err)
			}
			runtime := &productCheckoutRuntime{}
			service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
			checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "private-purchase-contract", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			service.verifier.now = func() time.Time { return now }
			receipt := receivePaymentWorkflowEvent(t, service, productPaidEvent(checkout.PaymentID, product, now.Unix(), 1900), now)
			if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
				t.Fatal(err)
			}
			var purchased uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1`, checkout.OrderID).Scan(&purchased); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
				VALUES($1,$2,$3,'Public source','Published source','Imported','published','AI-assisted',now())`, uuid.New(), seller, source); err != nil {
				t.Fatal(err)
			}
			mediaRoot := t.TempDir()
			assetService := assets.NewService(pool, mediaRoot)
			if _, err := assetService.Content(ctx, uuid.Nil, source); !errors.Is(err, assets.ErrForbidden) {
				t.Fatalf("legacy public work exposed the delivery original: %v", err)
			}
			if _, err := assetService.Content(ctx, uuid.Nil, purchased); !errors.Is(err, assets.ErrForbidden) {
				t.Fatalf("buyer copy inherited public visibility: %v", err)
			}
			if _, err := assetService.Content(ctx, buyer, purchased); err != nil {
				t.Fatalf("buyer lost active purchase access: %v", err)
			}
			projection, err := assetService.GetOwned(ctx, buyer, purchased)
			if err != nil || projection.Provenance == nil || projection.Provenance.Purchase == nil || !projection.Provenance.Purchase.CanDownload || projection.Provenance.Purchase.CanReuse != allowsDerivatives {
				t.Fatalf("purchase permissions do not match accepted contract: %+v %v", projection.Provenance, err)
			}
			if _, err := pool.Exec(ctx, `UPDATE licenses SET allows_derivatives=$1 WHERE code='hcai-commercial-standard-v1'`, !allowsDerivatives); err != nil {
				t.Fatal(err)
			}
			base := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
				VALUES($1,$2,'image','Owned base','/base','image/jpeg','clean','upload','creator-owned','local_file','base.jpg')`, base, buyer); err != nil {
				t.Fatal(err)
			}
			creator := creation.NewService(pool, mediaRoot, filepath.Join(t.TempDir(), "unused.jpg"), true)
			inputs := []creation.SubmitInput{
				{Mode: "image", Prompt: "Use the licensed reference", SourceAssetIDs: []uuid.UUID{purchased}},
				{Mode: "image", Prompt: "Use the licensed mask", SourceAssetIDs: []uuid.UUID{base}, MaskAssetID: &purchased},
			}
			var submitted []uuid.UUID
			for i, input := range inputs {
				generation, err := creator.SubmitCommand(ctx, buyer, input, fmt.Sprintf("contract-reference-%d", i), "test")
				if allowsDerivatives && err != nil {
					t.Fatalf("accepted right changed with current license: %v", err)
				}
				if !allowsDerivatives && !errors.Is(err, creation.ErrInvalid) {
					t.Fatalf("source/mask bypassed accepted restriction: %v", err)
				}
				if err == nil {
					submitted = append(submitted, generation.ID)
				}
			}
			if _, err := pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, checkout.OrderID); err != nil {
				t.Fatal(err)
			}
			if _, err := assetService.Content(ctx, buyer, purchased); !errors.Is(err, assets.ErrForbidden) {
				t.Fatalf("refunded content accessible: %v", err)
			}
			for i, input := range inputs {
				if _, err := creator.SubmitCommand(ctx, buyer, input, fmt.Sprintf("revoked-reference-%d", i), "test"); !errors.Is(err, creation.ErrInvalid) {
					t.Fatalf("source/mask bypassed revocation: %v", err)
				}
			}
			for i, id := range submitted {
				if _, err := pool.Exec(ctx, `UPDATE generations SET status='failed' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
				if _, err := creator.Retry(ctx, buyer, id, fmt.Sprintf("revoked-retry-%d", i), "test"); !errors.Is(err, creation.ErrInvalid) {
					t.Fatalf("retry bypassed revoked reference: %v", err)
				}
			}
		})
	}
}

func TestProductContractSurvivesOfferEditsAndModeratedReplay(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, seller, source, product := newProductCheckoutFixture(t, pool)
	version := productOfferVersion(t, pool, product)
	runtime := &productCheckoutRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "accepted-contract", "test", "https://example.test/success", "https://example.test/cancel", true, version)
	if err != nil {
		t.Fatal(err)
	}
	var contract string
	if err := pool.QueryRow(ctx, `SELECT contract::text FROM product_order_contracts WHERE order_id=$1`, checkout.OrderID).Scan(&contract); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE product_order_contracts SET contract=contract WHERE order_id=$1`,
		`DELETE FROM product_order_contracts WHERE order_id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, checkout.OrderID); err == nil {
			t.Fatal("accepted contract evidence was mutable")
		}
	}
	replacement := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
		VALUES($1,$2,'image','Replacement','/replacement','image/png','clean','upload','hcai-commercial-standard-v1','local_file','replacement.png')`, replacement, seller); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET asset_id=$2,price_cents=2900,title='Replaced offer' WHERE id=$1`, product, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE licenses SET allows_derivatives=false,terms='New terms',version='v2' WHERE code='hcai-commercial-standard-v1'`); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"accepted-contract", "different-command"} {
		if _, _, err := service.BeginProductCheckout(ctx, buyer, product, key, "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product)); !errors.Is(err, ErrOfferChanged) {
			t.Fatalf("existing checkout silently changed its contract: %v", err)
		}
	}
	if retry, _, err := service.BeginProductCheckout(ctx, buyer, product, "accepted-contract", "test", "https://example.test/success", "https://example.test/cancel", true, version); err != nil || retry.PaymentID != checkout.PaymentID || runtime.calls != 1 {
		t.Fatalf("original contract retry: %#v, %v", retry, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, service, productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents), now)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	var deliveredSource uuid.UUID
	var mediaURL, license, title, snapshot string
	var amount int
	if err := pool.QueryRow(ctx, `SELECT a.origin_asset_id,a.media_url,e.license_code,o.product_title_snapshot,o.amount_cents,c.contract::text
		FROM entitlements e JOIN assets a ON a.id=e.asset_id JOIN orders o ON o.id=e.order_id JOIN product_order_contracts c ON c.order_id=o.id
		WHERE o.id=$1`, checkout.OrderID).Scan(&deliveredSource, &mediaURL, &license, &title, &amount, &snapshot); err != nil {
		t.Fatal(err)
	}
	if deliveredSource != source || license != "hcai-commercial-standard-v1" || title != "Checkout product" || amount != 1900 || snapshot != contract {
		t.Fatalf("delivered mutated offer: source=%s license=%s title=%s amount=%d contractChanged=%t", deliveredSource, license, title, amount, snapshot != contract)
	}
	if mediaURL == "/api/v1/assets/"+source.String()+"/content" || mediaURL == "/replacement" {
		t.Fatal("delivery exposed a seller URL instead of buyer content")
	}
	// Provenance must retain the accepted seller even after catalog transfer.
	if _, err := pool.Exec(ctx, `UPDATE products SET seller_id=$2 WHERE id=$1`, product, buyer); err != nil {
		t.Fatal(err)
	}
	var purchased uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1`, checkout.OrderID).Scan(&purchased); err != nil {
		t.Fatal(err)
	}
	asset, err := assets.NewService(pool, paymentTestRoot(t, pool)).GetOwned(ctx, buyer, purchased)
	if err != nil || asset.Provenance == nil || asset.Provenance.Purchase == nil || asset.Provenance.Purchase.SellerID == nil || *asset.Provenance.Purchase.SellerID != seller {
		t.Fatalf("accepted seller changed: %+v %v", asset.Provenance, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	// Exercise the business replay rather than the event-receipt deduplication.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := service.fulfillProductPaymentTx(ctx, tx, "stripe", receipt.EventID, checkout.PaymentID, 1900, "USD", "pi_workflow123", nil); err != nil {
		t.Fatalf("completed payment replay depends on current moderation: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlements WHERE order_id=$1`, checkout.OrderID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replay duplicated entitlement: count=%d err=%v", count, err)
	}
}
