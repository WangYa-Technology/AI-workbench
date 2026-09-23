package marketplace_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
)

func TestProductPreviewConcurrentSelectionsAndScanRevocation(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	seller, buyer, source, product := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedMarketplaceProduct(t, pool, seller, buyer, source, product)
	samples := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, id := range samples {
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type,scan_status,storage_backend,storage_key,license_code)
		 VALUES($1,$2,'document','Separate preview',$3,'text/plain','upload','clean','local_file',$1::uuid::text||'.txt','hcai-commercial-standard-v1')`, id, seller, "/api/v1/assets/"+id.String()+"/content"); err != nil {
			t.Fatal(err)
		}
	}
	service := marketplace.NewService(pool)
	offer, err := service.GetProduct(ctx, seller, product)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range samples[:2] {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			<-start
			_, err := service.SetPreview(ctx, seller, product, marketplace.PreviewUpdate{PreviewAssetID: &id, OfferVersion: offer.OfferVersion}, "concurrent-preview")
			results <- err
		}(id)
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, marketplace.ErrPreviewConflict) {
			t.Fatal("unexpected concurrent result", err)
		}
	}
	if succeeded != 1 {
		t.Fatal("same version allowed multiple selections", succeeded)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='marketplace.preview_updated' AND resource_id=$1`, product).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("selection audit not atomic", audits, err)
	}
	offer, err = service.GetProduct(ctx, seller, product)
	if err != nil {
		t.Fatal(err)
	}
	// Media review uses this asset lock. Commit a rejection while SetPreview is
	// genuinely waiting, so it must reject/retry rather than trust the old scan.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var pid int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM assets WHERE id=$1 FOR UPDATE`, samples[2]).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := service.SetPreview(ctx, seller, product, marketplace.PreviewUpdate{PreviewAssetID: &samples[2], OfferVersion: offer.OfferVersion}, "scan-race")
		finished <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-finished:
			t.Fatal("selection skipped asset lock", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, samples[2]); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, marketplace.ErrPreviewConflict) {
			t.Fatal("stale scan state accepted", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := service.SetPreview(ctx, seller, product, marketplace.PreviewUpdate{PreviewAssetID: &samples[2], OfferVersion: offer.OfferVersion}, "scan-retry"); !errors.Is(err, marketplace.ErrInvalidPreview) {
		t.Fatal("rejected scan selectable on retry", err)
	}
	var current uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT preview_asset_id FROM products WHERE id=$1`, product).Scan(&current); err != nil || (current != samples[0] && current != samples[1]) {
		t.Fatal("scan conflict changed prior selection", current, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='marketplace.preview_updated' AND resource_id=$1`, product).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("failed selection left an audit event", audits, err)
	}
}
