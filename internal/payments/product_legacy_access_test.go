package payments

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Historical fixtures have no new checkout and do not relax any evidence
// triggers. Contracted cases represent an accepted pre-snapshot external order.
func legacyAccessFixture(t *testing.T, pool *pgxpool.Pool, kind string) purchasedReferenceFixture {
	t.Helper()
	if kind == "snapshot" {
		return newPurchasedReferenceFixture(t, pool, "video")
	}
	buyer, seller, source, product := newProductCheckoutFixture(t, pool)
	f := testutil.SeedLegacyProductOrder(t, pool, buyer, product)
	ctx := context.Background()
	if kind != "internal" {
		if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key)
   VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','paid',false,$1::uuid::text)`, uuid.New(), buyer, seller, product, f.OrderID); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "contract" || kind == "incomplete_contract" || kind == "unbound_snapshot" {
		projection := "contract"
		if kind == "incomplete_contract" {
			projection = "contract #- '{asset,storageKey}'"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract)
   SELECT $1,source_asset_id,root_asset_id,encode(public.digest((`+projection+`)::text,'sha256'),'hex'),`+projection+` FROM product_offers WHERE product_id=$2`, f.OrderID, product); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "unbound_snapshot" {
		if _, err := pool.Exec(ctx, `INSERT INTO product_delivery_snapshots(order_id,storage_backend,storage_key,sha256,size_bytes,mime_type,source_backend,source_key,state,ready_at)
   VALUES($1,'local_file',$1::uuid::text||'.jpg',repeat('0',64),32,'image/jpeg','local_file',$2::uuid::text||'.jpg','ready',now())`, f.OrderID, source); err != nil {
			t.Fatal(err)
		}
	}
	return purchasedReferenceFixture{buyer: buyer, source: source, purchased: f.AssetID, order: f.OrderID, root: paymentTestRoot(t, pool), content: []byte("The accepted purchased reference")}
}

func assertPurchaseAccessCapabilities(t *testing.T, pool *pgxpool.Pool, f purchasedReferenceFixture, allowed bool) {
	t.Helper()
	item, err := assets.NewService(pool, f.root).GetOwned(context.Background(), f.buyer, f.purchased)
	if err != nil || item.Provenance == nil || item.Provenance.Purchase == nil {
		t.Fatalf("lost metadata: %+v %v", item, err)
	}
	p := item.Provenance.Purchase
	if p.CanDownload != allowed || p.CanReuse != allowed {
		t.Fatalf("capabilities download=%v reuse=%v; want %v", p.CanDownload, p.CanReuse, allowed)
	}
}

func TestPurchasedAccessRechecksLegacyAndSnapshotHandles(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, kind := range []string{"internal", "contract", "snapshot"} {
		for _, change := range []string{"entitlement_revoked", "order_refunded", "buyer_suspended", "buyer_deleted", "wrong_buyer", "wrong_order", "wrong_entitlement_owner", "source_rejected", "asset_rejected"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				pool := pool
				if kind == "snapshot" {
					var release func()
					pool, release = paymentTestPool(t)
					defer release()
				}
				f := legacyAccessFixture(t, pool, kind)
				service := assets.NewService(pool, f.root)
				assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
				assertPurchaseAccessCapabilities(t, pool, f, true)
				content, err := service.Content(ctx, f.buyer, f.purchased)
				if err != nil {
					t.Fatal(err)
				}
				runtime := &referenceWorkerRuntime{}
				creator := creation.NewServiceWithRuntimes(pool, f.root, creation.NewRuntimeCatalog(runtime))
				input := creation.SubmitInput{Mode: "video", Prompt: "Use the accepted resource", SourceAssetIDs: []uuid.UUID{f.purchased}}
				generation, err := creator.SubmitCommand(ctx, f.buyer, input, "before-permission-change", "test")
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "entitlement_revoked":
					_, err = pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, f.order)
				case "order_refunded":
					_, err = pool.Exec(ctx, `UPDATE orders SET status='refunded' WHERE id=$1`, f.order)
				case "buyer_suspended":
					_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, f.buyer)
				case "buyer_deleted":
					_, err = pool.Exec(ctx, `UPDATE users SET status='deleted' WHERE id=$1`, f.buyer)
				case "wrong_buyer":
					_, err = pool.Exec(ctx, `UPDATE orders SET buyer_id=(SELECT owner_id FROM assets WHERE id=$2) WHERE id=$1`, f.order, f.source)
				case "wrong_order":
					_, err = pool.Exec(ctx, `UPDATE assets SET source_id=$2 WHERE id=$1`, f.purchased, uuid.New())
				case "wrong_entitlement_owner":
					_, err = pool.Exec(ctx, `UPDATE entitlements SET user_id=(SELECT owner_id FROM assets WHERE id=$2) WHERE order_id=$1`, f.order, f.source)
				case "source_rejected":
					_, err = pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, f.source)
				case "asset_rejected":
					_, err = pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, f.purchased)
				}
				if err != nil {
					t.Fatal(err)
				}
				assertPurchaseAccessCapabilities(t, pool, f, false)
				if _, err = content.Stat(ctx); !errors.Is(err, assets.ErrForbidden) {
					t.Fatalf("stale stat authorized: %v", err)
				}
				for _, requested := range []*media.ByteRange{nil, {Start: 0, End: 3}} {
					object, err := content.Open(ctx, requested)
					if object.Body != nil {
						object.Body.Close()
						t.Fatal("revoked handle returned a body")
					}
					if !errors.Is(err, assets.ErrForbidden) {
						t.Fatalf("stale read authorized: %v", err)
					}
				}
				if _, err = service.Content(ctx, f.buyer, f.purchased); !errors.Is(err, assets.ErrForbidden) && !errors.Is(err, assets.ErrNotFound) {
					t.Fatalf("fresh read authorized: %v", err)
				}
				if _, err = creator.SubmitCommand(ctx, f.buyer, input, "after-permission-change", "test"); err == nil {
					t.Fatal("new reference submitted after access lost")
				}
				if change == "buyer_suspended" || change == "buyer_deleted" {
					assertReferenceWorkerRejected(t, pool, creator, runtime, f, generation, 0, creation.ErrAccountUnavailable)
				} else {
					assertReferenceWorkerRejected(t, pool, creator, runtime, f, generation, 0)
				}
			})
		}
	}
}

type accessTrackingBody struct {
	io.ReadCloser
	reads, closes int
}

func (b *accessTrackingBody) Read(p []byte) (int, error) { b.reads++; return b.ReadCloser.Read(p) }
func (b *accessTrackingBody) Close() error               { b.closes++; return b.ReadCloser.Close() }

type changingPurchaseStore struct {
	media.Store
	change func() error
	opened int
	body   *accessTrackingBody
}

func (s *changingPurchaseStore) Open(ctx context.Context, key string, requested *media.ByteRange) (media.Object, error) {
	object, err := s.Store.Open(ctx, key, requested)
	if err != nil {
		return object, err
	}
	s.opened++
	s.body = &accessTrackingBody{ReadCloser: object.Body}
	object.Body = s.body
	if err = s.change(); err != nil {
		object.Body.Close()
		return media.Object{}, err
	}
	return object, nil
}

func TestLegacyPurchaseReadClosesStreamWhenRefundCommitsDuringOpen(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, kind := range []string{"internal", "contract"} {
		for _, useRange := range []bool{false, true} {
			name := kind + "/full"
			if useRange {
				name = kind + "/range"
			}
			t.Run(name, func(t *testing.T) {
				f := legacyAccessFixture(t, pool, kind)
				store := &changingPurchaseStore{Store: media.NewLocalStore(f.root), change: func() error {
					if kind == "internal" {
						_, err := marketplace.NewService(pool).RefundLegacyOrder(ctx, f.buyer, f.order, "refund-during-file-open", "test", "This historical resource is no longer required.")
						return err
					}
					_, err := pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, f.order)
					return err
				}}
				content, err := assets.NewServiceWithMedia(pool, media.NewCatalog(store), nil).Content(ctx, f.buyer, f.purchased)
				if err != nil {
					t.Fatal(err)
				}
				var requested *media.ByteRange
				if useRange {
					requested = &media.ByteRange{Start: 0, End: 3}
				}
				object, err := content.Open(ctx, requested)
				if object.Body != nil {
					object.Body.Close()
					t.Fatal("returned revoked body")
				}
				if !errors.Is(err, assets.ErrForbidden) || store.opened != 1 || store.body.reads != 0 || store.body.closes != 1 {
					t.Fatalf("read not safely closed: err=%v store=%+v body=%+v", err, store, store.body)
				}
				if _, err = content.Open(ctx, requested); !errors.Is(err, assets.ErrForbidden) || store.opened != 1 {
					t.Fatal("revoked handle reopened storage", err)
				}
				assertPurchaseAccessCapabilities(t, pool, f, false)
			})
		}
	}
}

func TestHistoricalExternalPurchaseRequiresAcceptedDeliveryEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, kind := range []string{"missing_contract", "incomplete_contract", "unbound_snapshot"} {
		t.Run(kind, func(t *testing.T) {
			f := legacyAccessFixture(t, pool, kind)
			assertPurchaseAccessCapabilities(t, pool, f, false)
			service := assets.NewService(pool, f.root)
			if _, err := service.Content(ctx, f.buyer, f.purchased); !errors.Is(err, assets.ErrForbidden) {
				t.Fatal("fell back to mutable source", err)
			}
			creator := creation.NewService(pool, f.root, "", true)
			if _, err := creator.Submit(ctx, f.buyer, creation.SubmitInput{Mode: "image", Prompt: "Reject unproven delivery", SourceAssetIDs: []uuid.UUID{f.purchased}}); !errors.Is(err, creation.ErrInvalid) {
				t.Fatal("unproven reference allowed", err)
			}
		})
	}
}

func TestHistoricalContractRetainsAcceptedLocatorAndRefundWindowAccess(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := legacyAccessFixture(t, pool, "contract")
	if _, err := pool.Exec(ctx, `UPDATE assets SET storage_key='replacement.jpg' WHERE id=$1`, f.source); err != nil {
		t.Fatal(err)
	}
	if err := media.NewLocalStore(f.root).Put(ctx, "replacement.jpg", []byte("A different current file"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET status='refund_requested' WHERE id=$1`, f.order); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET status='paused',title='New title' WHERE id=(SELECT product_id FROM orders WHERE id=$1)`, f.order); err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
	assertPurchaseAccessCapabilities(t, pool, f, true)
	runtime := &referenceWorkerRuntime{}
	creator := creation.NewServiceWithRuntimes(pool, f.root, creation.NewRuntimeCatalog(runtime))
	generation, err := creator.SubmitCommand(ctx, f.buyer, creation.SubmitInput{Mode: "video", Prompt: "Keep original accepted reference", SourceAssetIDs: []uuid.UUID{f.purchased}}, "old-contract-reference", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = creator.HandleJob(ctx, generationWorkerJob(t, pool, generation.ID)); err != nil || runtime.calls != 1 {
		t.Fatal("historical contract dispatch", err)
	}
	if len(runtime.request.ReferenceAssets) != 1 || string(runtime.request.ReferenceAssets[0].Content) != string(f.content) {
		t.Fatal("mutable source sent to provider")
	}
}
