package payments

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type bundleSnapshotFixture struct {
	pool                                  *pgxpool.Pool
	buyer, seller, first, second, product uuid.UUID
	store                                 *media.LocalStore
	stores                                *media.Catalog
	root                                  string
}

func newBundleSnapshotFixture(t *testing.T) bundleSnapshotFixture {
	t.Helper()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	buyer, seller, first, _ := newProductCheckoutFixture(t, pool)
	second := uuid.New()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','Bundle notes',$3,'text/plain','clean','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.txt')`, second, seller, "/api/v1/assets/"+second.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	root := paymentTestRoot(t, pool)
	store := media.NewLocalStore(root)
	if err := store.Put(ctx, second.String()+".txt", []byte("Private bundle notes"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	draft := marketplace.ProductDraft{Title: "Bundle snapshot", Description: "Two independent originals", ProductType: "asset", Category: "market_asset", AssetID: first, PriceCents: 1900, Currency: "USD", LicenseCode: "hcai-commercial-standard-v1", AIDisclosure: "Author-owned originals", IncludedFiles: []string{"image.jpg", "说明.txt"}, Files: []marketplace.ProductFile{{AssetID: first, Name: "image.jpg"}, {AssetID: second, Name: "说明.txt"}}}
	listing, err := marketplace.NewService(pool).MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &draft})
	if err != nil {
		t.Fatal(err)
	}
	return bundleSnapshotFixture{pool: pool, buyer: buyer, seller: seller, first: first, second: second, product: listing.ID, store: store, stores: media.NewCatalog(store), root: root}
}

// Internal evidence fixture: independent of publication. This never creates an
// external payment or claims to exercise a buyer-facing bundle checkout.
func (f bundleSnapshotFixture) contractTx(t *testing.T, contractProjection ...string) (pgx.Tx, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	order := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot,delivery_snapshot_required)
 SELECT $1,$2,p.id,p.price_cents,p.currency,'payment_pending',now(),$1::uuid::text,p.title,l.name,l.version,l.terms,l.refund_window_days,true FROM products p JOIN licenses l ON l.code=p.license_code WHERE p.id=$3`, order, f.buyer, f.product); err != nil {
		t.Fatal(err)
	}
	projection := "contract"
	if len(contractProjection) > 0 {
		projection = contractProjection[0]
	}
	// Only test-authored SQL projections reach this helper.
	if _, err = tx.Exec(ctx, `INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract) SELECT $1,source_asset_id,root_asset_id,encode(public.digest((`+projection+`)::text,'sha256'),'hex'),`+projection+` FROM product_offers WHERE product_id=$2`, order, f.product); err != nil {
		t.Fatal(err)
	}
	return tx, order
}

func (f bundleSnapshotFixture) reserve(t *testing.T) productdelivery.Snapshot {
	t.Helper()
	ctx := context.Background()
	tx, order := f.contractTx(t)
	if err := productdelivery.ReserveTx(ctx, tx, f.stores, order); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := productdelivery.Load(ctx, f.pool, order)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestProductBundleSnapshotFrozenSourcesAndIndependentReads(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	before := productOfferVersion(t, f.pool, f.product)
	s := f.reserve(t)
	if s.Format != productdelivery.FormatZIPV1 || s.SourceBackend != "" || s.SourceKey != "" || s.Manifest == nil || len(s.Manifest.Files) != 2 || s.MIMEType != "application/zip" {
		t.Fatalf("invalid snapshot: %+v", s)
	}
	if _, err := f.store.Stat(ctx, s.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("reserved evidence wrote durable copy: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE assets SET storage_key='new-file-location.txt' WHERE id=$1`, f.second); err != nil {
		t.Fatal(err)
	}
	if productOfferVersion(t, f.pool, f.product) == before {
		t.Fatal("second member omitted from offer version")
	}
	// Today's locator and listing no longer describe the accepted files.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM product_listing_files WHERE product_id=$1`, f.product); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE products SET included_files='["single.jpg"]' WHERE id=$1`, f.product); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var protected, backup bool
	if err = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_delivery_roots WHERE asset_id=$1),EXISTS(SELECT 1 FROM product_repair_backup_candidates WHERE asset_id=$1)`, f.second).Scan(&protected, &backup); err != nil || !protected || backup {
		t.Fatal("frozen secondary source lost protection", protected, backup, err)
	}
	alias := uuid.New()
	if _, err = f.pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','Old location alias',$3,'text/plain','clean','upload','creator-owned','local_file',$4)`, alias, f.seller, "/api/v1/assets/"+alias.String()+"/content", f.second.String()+".txt"); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_delivery_roots WHERE asset_id=$1),EXISTS(SELECT 1 FROM product_repair_backup_candidates WHERE asset_id=$1)`, alias).Scan(&protected, &backup); err != nil || !protected || backup {
		t.Fatal("historical source alias lost protection", protected, backup, err)
	}
	if err = productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); err != nil {
		t.Fatal(err)
	}
	ready, err := productdelivery.Load(ctx, f.pool, s.OrderID)
	if err != nil || ready.State != "ready" || ready.Key != s.Key || ready.SHA256 != s.SHA256 {
		t.Fatal("changed frozen evidence", err)
	}
	for _, key := range []string{f.first.String() + ".jpg", f.second.String() + ".txt"} {
		if err = f.store.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	object, err := ready.Open(ctx, f.stores, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(object.Body)
	_ = object.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil || len(archive.File) != 3 {
		t.Fatalf("not a complete ZIP: %v", err)
	}
	for i, want := range []string{"The accepted purchased reference", "Private bundle notes"} {
		member, err := ready.OpenFile(ctx, f.stores, i, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(member.Body)
		_ = member.Body.Close()
		if err != nil || string(got) != want {
			t.Fatalf("member %d: %q %v", i, got, err)
		}
	}
	part, err := ready.OpenFile(ctx, f.stores, 1, &media.ByteRange{Start: 1, End: 6})
	if err != nil {
		t.Fatal(err)
	}
	partBytes, err := io.ReadAll(part.Body)
	_ = part.Body.Close()
	if err != nil || string(partBytes) != "rivate" {
		t.Fatal("member range", string(partBytes), err)
	}
	for _, index := range []int{-1, 2} {
		if _, err = ready.OpenFile(ctx, f.stores, index, nil); err == nil {
			t.Fatal("invalid file index accepted", index)
		}
	}
	// Corruption outside the requested member must still reject the read.
	body[len(body)-1] ^= 1
	if err = os.WriteFile(filepath.Join(f.root, s.Key), body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ready.OpenFile(ctx, f.stores, 0, nil); !errors.Is(err, media.ErrIntegrity) {
		t.Fatal("corruption leaked partial member", err)
	}
}

func TestProductBundleSnapshotInterruptedPreparation(t *testing.T) {
	for _, scenario := range []string{"source_changed", "write_response_lost", "ready_commit_lost", "ready_missing"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBundleSnapshotFixture(t)
			ctx := context.Background()
			s := f.reserve(t)
			switch scenario {
			case "source_changed":
				if err := os.WriteFile(filepath.Join(f.root, f.second.String()+".txt"), []byte("Changed bundle notes"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); !errors.Is(err, media.ErrIntegrity) {
					t.Fatal("accepted changed second member", err)
				}
				if _, err := f.store.Stat(ctx, s.Key); !errors.Is(err, media.ErrNotFound) {
					t.Fatal("wrote unaccepted bundle", err)
				}
				if err := os.WriteFile(filepath.Join(f.root, f.second.String()+".txt"), []byte("Private bundle notes"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ready_commit_lost":
				if _, err := f.pool.Exec(ctx, `CREATE FUNCTION bundle_lost_commit() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'lost commit'; END $$ LANGUAGE plpgsql; CREATE TRIGGER bundle_lost_commit BEFORE UPDATE ON product_delivery_snapshots FOR EACH ROW EXECUTE FUNCTION bundle_lost_commit()`); err != nil {
					t.Fatal(err)
				}
				if err := productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); err == nil {
					t.Fatal("missing interruption")
				}
				if _, err := f.pool.Exec(ctx, `DROP TRIGGER bundle_lost_commit ON product_delivery_snapshots`); err != nil {
					t.Fatal(err)
				}
				if err := f.store.Delete(ctx, f.second.String()+".txt"); err != nil {
					t.Fatal(err)
				}
			case "write_response_lost":
				f.stores = media.NewCatalog(&bundleUncertainStore{f.store})
			}
			if err := productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); err != nil {
				t.Fatal(err)
			}
			ready, err := productdelivery.Load(ctx, f.pool, s.OrderID)
			if err != nil || ready.State != "ready" || ready.Key != s.Key || ready.SHA256 != s.SHA256 {
				t.Fatal("recovery changed evidence", err)
			}
			if scenario == "ready_missing" {
				if err = f.store.Delete(ctx, s.Key); err != nil {
					t.Fatal(err)
				}
				if err = productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); !errors.Is(err, media.ErrNotFound) {
					t.Fatal("ready copy silently regenerated", err)
				}
			}
		})
	}
}

type bundleUncertainStore struct{ *media.LocalStore }

func (s *bundleUncertainStore) PutStream(ctx context.Context, key string, body io.ReadSeeker, size int64, digest, mime string) error {
	if err := s.LocalStore.PutStream(ctx, key, body, size, digest, mime); err != nil {
		return err
	}
	return errors.New("simulated response lost after durable write")
}

func TestProductBundleSnapshotEvidenceGuards(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	s := f.reserve(t)
	for _, scenario := range []string{"missing_snapshot", "old_single_writer", "incomplete_manifest", "renamed_member", "downgraded_contract"} {
		t.Run(scenario, func(t *testing.T) {
			projection := "contract"
			if scenario == "downgraded_contract" {
				projection = "contract-'delivery'"
			}
			tx, order := f.contractTx(t, projection)
			var err error
			switch scenario {
			case "missing_snapshot":
			case "downgraded_contract":
				err = productdelivery.ReserveTx(ctx, tx, f.stores, order)
			case "old_single_writer":
				_, err = tx.Exec(ctx, `INSERT INTO product_delivery_snapshots(order_id,source_backend,source_key,storage_backend,storage_key,sha256,size_bytes,mime_type)
 SELECT $1,'local_file',$2,'local_file',$1::uuid::text,repeat('0',64),10,'image/jpeg'`, order, f.first.String()+".jpg")
			default:
				manifest := `jsonb_set(file_manifest,'{files}',(file_manifest->'files')-1)`
				if scenario == "renamed_member" {
					manifest = `jsonb_set(file_manifest,'{files,1,name}','"different.txt"')`
				}
				_, err = tx.Exec(ctx, `INSERT INTO product_delivery_snapshots(order_id,source_backend,source_key,storage_backend,storage_key,sha256,size_bytes,mime_type,format,file_manifest)
 SELECT $1,NULL,NULL,storage_backend,$1::uuid::text,sha256,size_bytes,mime_type,format,`+manifest+` FROM product_delivery_snapshots WHERE order_id=$2`, order, s.OrderID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err == nil {
				t.Fatal("committed incomplete bundle evidence")
			}
		})
	}
	for _, query := range []string{`UPDATE product_delivery_snapshots SET file_manifest='{}' WHERE order_id=$1`, `UPDATE product_delivery_snapshots SET format='single' WHERE order_id=$1`, `DELETE FROM product_delivery_snapshots WHERE order_id=$1`, `UPDATE product_order_contracts SET contract=contract-'delivery' WHERE order_id=$1`, `UPDATE orders SET delivery_snapshot_required=false WHERE id=$1`} {
		if _, err := f.pool.Exec(ctx, query, s.OrderID); err == nil {
			t.Fatal("mutable evidence", query)
		}
	}
	var legacyBackend string
	if err := f.pool.QueryRow(ctx, `SELECT source_backend FROM product_delivery_snapshots WHERE order_id=$1`, s.OrderID).Scan(&legacyBackend); err == nil {
		t.Fatal("legacy reader accepted ZIP as single file")
	}
	down, err := os.ReadFile("../platform/database/migrations/0113_product_bundle_snapshots.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback discarded bundle evidence")
	}
}

func TestProductBundleSnapshotExportPrivacy(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	s := f.reserve(t)
	pkg, body := runProductExport(t, f.pool, f.buyer)
	contracts := pkg.Data.Marketplace.Data["contracts"]
	snapshots := pkg.Data.Marketplace.Data["deliverySnapshots"]
	if len(contracts) != 1 || len(snapshots) != 1 {
		t.Fatal("missing buyer evidence", len(contracts), len(snapshots))
	}
	delivery := contracts[0]["delivery"].(map[string]any)
	files := delivery["files"].([]any)
	if delivery["format"] != "zip-v1" || len(files) != 2 || len(files[1].(map[string]any)) != 4 || files[1].(map[string]any)["assetId"] != f.second.String() {
		t.Fatal("incomplete exported contract", delivery)
	}
	manifest := snapshots[0]["fileManifest"].(map[string]any)
	member := manifest["files"].([]any)[1].(map[string]any)
	if snapshots[0]["format"] != "zip-v1" || len(member) != 4 || member["sha256"] != s.Manifest.Files[1].SHA256 {
		t.Fatal("incomplete exported checksums", snapshots)
	}
	for _, private := range []string{"storageBackend", "storageKey", s.Key, f.second.String() + ".txt", "Private bundle notes"} {
		if strings.Contains(string(body), private) {
			t.Fatal("private locator or body exported", private)
		}
	}
	foreign, _ := runProductExport(t, f.pool, f.seller)
	if len(foreign.Data.Marketplace.Data["contracts"]) != 0 || len(foreign.Data.Marketplace.Data["deliverySnapshots"]) != 0 {
		t.Fatal("seller received buyer delivery export")
	}
}
