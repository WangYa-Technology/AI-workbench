package payments

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProductBundleRejectsOldWorkersAndPartialPurchaseAssets(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	snapshot := f.reserve(t)
	if err := productdelivery.Ensure(ctx, f.pool, f.stores, snapshot.OrderID); err != nil {
		t.Fatal(err)
	}
	var jobID uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,available_at) VALUES('test.bundle_protocol','{}',now()-interval '1 year') RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	config := f.pool.Config()
	delete(config.ConnConfig.RuntimeParams, "app.product_bundle_protocol")
	old, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err = old.Exec(ctx, `UPDATE product_publications SET review_status='pending',submitted_version=(SELECT content_version FROM product_listing_versions WHERE product_id=$1) WHERE product_id=$1`, f.product); err == nil || !strings.Contains(err.Error(), "bundle-aware application required") {
		t.Fatal("old writer submitted bundle", err)
	}
	if _, err = jobs.NewRepository(old).Claim(ctx, "old-worker", time.Minute); err == nil || !strings.Contains(err.Error(), "bundle-aware application required") {
		t.Fatal("old worker can claim", err)
	}
	var status string
	if err = f.pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, jobID).Scan(&status); err != nil || status != "queued" {
		t.Fatal("old claim changed job", status, err)
	}
	current, err := jobs.NewRepository(f.pool).Claim(ctx, "bundle-aware-worker", time.Minute)
	if err != nil || current.ID != jobID {
		t.Fatal("current worker cannot claim", current, err)
	}
	// Even a migration invoked by a new binary cannot enable publication while
	// old in-flight work might still hold external file/payment handles.
	up, err := os.ReadFile("../platform/database/migrations/0115_product_bundle_publication.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, string(up))
	_ = tx.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "drain running jobs") {
		t.Fatal("migration admitted running jobs", err)
	}
	if err = jobs.NewRepository(f.pool).Complete(ctx, current, "bundle-aware-worker"); err != nil {
		t.Fatal(err)
	}
	const insert = `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,source_id,license_code,size_bytes,origin_asset_id)
 VALUES($1,$2,$3,'Purchased package',$1::uuid::text,$4,'clean','purchase',$5,'hcai-commercial-standard-v1',$6,$7)`
	// A current binary cannot bypass the structural invariant either.
	for _, shape := range []struct {
		kind, mime string
		size       int64
		origin     *uuid.UUID
	}{
		{"image", "image/jpeg", snapshot.Size, &f.first},
		{"document", "application/zip", snapshot.Size - 1, nil},
		{"document", "application/zip", snapshot.Size, &f.first},
	} {
		if _, err = f.pool.Exec(ctx, insert, uuid.New(), f.buyer, shape.kind, shape.mime, snapshot.OrderID, shape.size, shape.origin); err == nil {
			t.Fatal("partial package asset accepted", shape)
		}
	}
	assetID := uuid.New()
	if _, err = old.Exec(ctx, insert, assetID, f.buyer, "document", "application/zip", snapshot.OrderID, snapshot.Size, nil); err == nil || !strings.Contains(err.Error(), "bundle-aware application required") {
		t.Fatal("old writer accepted bundle", err)
	}
	if _, err = f.pool.Exec(ctx, insert, assetID, f.buyer, "document", "application/zip", snapshot.OrderID, snapshot.Size, nil); err != nil {
		t.Fatal("complete package rejected", err)
	}
	for _, sql := range []string{
		`UPDATE assets SET source_type='upload',source_id=NULL WHERE id=$1`,
		`UPDATE assets SET source_id=gen_random_uuid() WHERE id=$1`,
		`UPDATE assets SET kind='image',mime_type='image/jpeg' WHERE id=$1`,
	} {
		if _, err = f.pool.Exec(ctx, sql, assetID); err == nil {
			t.Fatal("bundle downgraded", sql)
		}
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO entitlements(user_id,product_id,order_id,asset_id,license_code,status) VALUES($1,$2,$3,$4,'hcai-commercial-standard-v1','active')`, f.buyer, f.product, snapshot.OrderID, f.first); err == nil {
		t.Fatal("original source granted as package")
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO entitlements(user_id,product_id,order_id,asset_id,license_code,status) VALUES($1,$2,$3,$4,'hcai-commercial-standard-v1','active')`, f.buyer, f.product, snapshot.OrderID, assetID); err != nil {
		t.Fatal("complete package entitlement", err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE entitlements SET asset_id=$2 WHERE order_id=$1`, snapshot.OrderID, f.first); err == nil {
		t.Fatal("accepted entitlement rebound to first source")
	}
	if _, err = f.pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, snapshot.OrderID); err != nil {
		t.Fatal("protocol blocked revocation", err)
	}

}

func TestProductBundlePublicationMigrationRoundtrip(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, direction := range []string{"down", "up"} {
		body, err := os.ReadFile("../platform/database/migrations/0115_product_bundle_publication." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, string(body)); err != nil {
			tx.Rollback(ctx)
			t.Fatal(direction, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(direction, err)
		}
	}
}
