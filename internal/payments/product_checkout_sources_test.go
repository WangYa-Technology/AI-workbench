package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/jackc/pgx/v5"
)

func TestProductCheckoutRechecksPublicEligibilityAfterLockWait(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	buyer, seller, source, product := newProductCheckoutFixture(t, pool)
	// Model an approved listing, so public qualification requires a private
	// independent source as well as a clean scan. No publication gate is lifted.
	if _, err := pool.Exec(ctx, `INSERT INTO product_publications(product_id,review_status,approved_version)
 SELECT $1,'approved',content_version FROM product_listing_versions WHERE product_id=$1`, product); err != nil {
		t.Fatal(err)
	}
	version := productOfferVersion(t, pool, product)
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err = gate.Exec(ctx, `SELECT id FROM assets WHERE id=$1 FOR UPDATE`, source); err != nil {
		t.Fatal(err)
	}
	runtime := &guardedProductRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	done := make(chan error, 1)
	go func() {
		_, _, err := service.BeginProductCheckout(ctx, buyer, product, "public-source-race", "test", "https://example.test/success", "https://example.test/cancel", true, version)
		done <- err
	}()
	waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
	// An earlier publication wins while checkout waits. The asset's scan and
	// offer hash are unchanged; reusing the old joined view snapshot is unsafe.
	if _, err = gate.Exec(ctx, `INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
 VALUES($1,$2,$3,'Previously published source','Source visibility race','Imported','published','Original',now())`, uuid.New(), seller, source); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrInvalidCheckout) {
		t.Fatal("accepted stale public qualification", err)
	}
	var orders, payments int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders WHERE product_id=$1),(SELECT count(*) FROM payment_intents WHERE resource_id=$1)`, product).Scan(&orders, &payments); err != nil || orders != 0 || payments != 0 || runtime.calls.Load() != 0 {
		t.Fatal("ineligible checkout left a payment obligation", orders, payments, runtime.calls.Load(), err)
	}
}

func TestProductBundleCheckoutSourcesRecheckEveryMember(t *testing.T) {
	for _, change := range []string{"scan", "owner", "public", "storage", "task_license"} {
		t.Run(change, func(t *testing.T) {
			f := newBundleSnapshotFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			gate, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err = gate.Exec(ctx, `SELECT id FROM assets WHERE id=$1 FOR UPDATE`, f.second); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				tx, err := f.pool.Begin(ctx)
				if err == nil {
					defer tx.Rollback(context.Background())
					err = lockProductCheckoutSources(ctx, tx, f.buyer, f.product)
				}
				done <- err
			}()
			waitForProductBlockingTx(t, ctx, f.pool, int32(gate.Conn().PgConn().PID()))
			switch change {
			case "scan":
				_, err = gate.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, f.second)
			case "owner":
				_, err = gate.Exec(ctx, `UPDATE assets SET owner_id=$2 WHERE id=$1`, f.second, f.buyer)
			case "public":
				_, err = gate.Exec(ctx, `INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
 VALUES($1,$2,$3,'Earlier publication','Private source race','Imported','published','Original',now())`, uuid.New(), f.seller, f.second)
			case "storage":
				_, err = gate.Exec(ctx, `UPDATE assets SET source_type='purchase',storage_backend=NULL,storage_key=NULL WHERE id=$1`, f.second)
			case "task_license":
				_, err = gate.Exec(ctx, `UPDATE assets SET license_code='task-contract' WHERE id=$1`, f.second)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, ErrInvalidCheckout) {
				t.Fatal("accepted an ineligible secondary source after waiting", err)
			}
		})
	}
}

func TestProductBundleCheckoutSourcesLockOrderAndStableContract(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	listing, err := marketplace.NewService(f.pool).GetListing(ctx, f.seller, f.product, false)
	if err != nil {
		t.Fatal(err)
	}
	draft := listing.ProductDraft
	draft.Files[0], draft.Files[1] = draft.Files[1], draft.Files[0]
	draft.IncludedFiles[0], draft.IncludedFiles[1] = draft.IncludedFiles[1], draft.IncludedFiles[0]
	draft.AssetID = f.second
	reversed, err := marketplace.NewService(f.pool).MutateListing(ctx, f.seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &draft})
	if err != nil {
		t.Fatal(err)
	}
	// These two manifests share sources in opposite presentation order. Both
	// must lock by UUID, not by the seller's file ordering or the first member.
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err = gate.Exec(ctx, `SELECT id FROM assets WHERE id=ANY($1::uuid[]) ORDER BY id LIMIT 1 FOR UPDATE`, []uuid.UUID{f.first, f.second}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for _, id := range []uuid.UUID{f.product, reversed.ID} {
		go func(id uuid.UUID) {
			tx, err := f.pool.Begin(ctx)
			if err == nil {
				defer tx.Rollback(context.Background())
				err = lockProductCheckoutSources(ctx, tx, f.buyer, id)
				if err == nil {
					err = tx.Commit(ctx)
				}
			}
			done <- err
		}(id)
	}
	waitForProductBlockingTx(t, ctx, f.pool, int32(gate.Conn().PgConn().PID()))
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = <-done; err != nil {
			t.Fatal("opposite manifest order failed", err)
		}
	}
	// A successful lock covers every member until the contract is committed.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = lockProductCheckoutSources(ctx, tx, f.buyer, f.product); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, err := f.pool.Exec(ctx, `UPDATE assets SET storage_key='later-location.txt' WHERE id=$1`, f.second)
		done <- err
	}()
	waitForProductBlockingTx(t, ctx, f.pool, int32(tx.Conn().PgConn().PID()))
	var key string
	if err = tx.QueryRow(ctx, `SELECT contract->'delivery'->'files'->1->'source'->>'storageKey' FROM product_offers WHERE product_id=$1`, f.product).Scan(&key); err != nil || key != f.second.String()+".txt" {
		t.Fatal("accepted sources changed inside lock", key, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestProductCheckoutRejectsRootChangedDuringLockWait(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	buyer, seller, source, product := newProductCheckoutFixture(t, pool)
	root := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'image','Another root','/root.jpg','image/jpeg','clean','upload','creator-owned','local_file',$1::uuid::text||'.jpg')`, root, seller); err != nil {
		t.Fatal(err)
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err = gate.Exec(ctx, `SELECT id FROM assets WHERE id=$1 FOR UPDATE`, source); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err == nil {
			defer tx.Rollback(context.Background())
			err = lockProductCheckoutSources(ctx, tx, buyer, product)
		}
		done <- err
	}()
	waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
	if _, err = gate.Exec(ctx, `UPDATE assets SET origin_asset_id=$2 WHERE id=$1`, source, root); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrOfferChanged) {
		t.Fatal("froze an unlocked root", err)
	}
}
