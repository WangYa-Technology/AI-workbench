package marketplace_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/jackc/pgx/v5/pgxpool"
)

func bundleListingFixture(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID, marketplace.ProductDraft) {
	t.Helper()
	seller, reviewer, d := listingFixture(t, pool)
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type,scan_status,storage_backend,storage_key,license_code) VALUES($1,$2,'document','Second source',$3,'text/plain','upload','clean','local_file',$1::uuid::text||'.txt','hcai-commercial-standard-v1')`, id, seller, "/api/v1/assets/"+id.String()+"/content")
	if err != nil {
		t.Fatal(err)
	}
	d.Files = []marketplace.ProductFile{{AssetID: d.AssetID, Name: "workflow.txt"}, {AssetID: id, Name: "使用说明.txt"}}
	d.IncludedFiles = []string{d.Files[0].Name, d.Files[1].Name}
	return seller, reviewer, d
}

func TestProductFileDraftLifecycleAndVersionedReview(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, reviewer, d := bundleListingFixture(t, pool)
	svc := marketplace.NewService(pool)
	item, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", "bundle-create-001", "test", marketplace.ListingMutation{Draft: &d})
	if err != nil || len(item.Files) != 2 || item.Files[1] != d.Files[1] {
		t.Fatal(item, err)
	}
	b, err := json.Marshal(item)
	if err != nil || strings.Contains(string(b), "storageKey") || strings.Contains(string(b), "storageBackend") {
		t.Fatal("private locators exposed", err)
	}
	replay, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", "bundle-create-001", "test", marketplace.ListingMutation{Draft: &d})
	if err != nil || replay.ID != item.ID || replay.Version != item.Version {
		t.Fatal("idempotent manifest", replay, err)
	}
	if _, err = svc.GetListing(ctx, reviewer, item.ID, false); !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatal("foreign seller read", err)
	}
	if _, err = svc.GetProduct(ctx, uuid.Nil, item.ID); !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatal("bundle draft public", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE products SET status='active' WHERE id=$1`, item.ID); err == nil {
		t.Fatal("old writer activated incomplete bundle")
	}
	if _, _, err = svc.ReviewListingFile(ctx, reviewer, item.ID, "source", item.Version, "test"); !errors.Is(err, marketplace.ErrInvalidListing) {
		t.Fatal("implicit first-file review", err)
	}
	index := 1
	owner, file, err := svc.ReviewListingFileAt(ctx, reviewer, item.ID, "source", item.Version, "test", &index)
	if err != nil || owner != seller || file != d.Files[1].AssetID {
		t.Fatal("member review", owner, file, err)
	}
	if _, _, err = svc.ReviewListingFileAt(ctx, seller, item.ID, "source", item.Version, "test", &index); !errors.Is(err, marketplace.ErrListingForbidden) {
		t.Fatal("seller review privilege", err)
	}
	if _, _, err = svc.ReviewListingFileAt(ctx, reviewer, item.ID, "preview", item.Version, "test", &index); !errors.Is(err, marketplace.ErrInvalidListing) {
		t.Fatal("preview indexed", err)
	}
	index = 2
	if _, _, err = svc.ReviewListingFileAt(ctx, reviewer, item.ID, "source", item.Version, "test", &index); !errors.Is(err, marketplace.ErrInvalidListing) {
		t.Fatal("outside manifest", err)
	}
	// A change to the second file must invalidate the entire observed listing,
	// including attempts to review the first file or edit using an old version.
	if _, err = pool.Exec(ctx, `UPDATE assets SET storage_key=storage_key||'.changed' WHERE id=$1`, d.Files[1].AssetID); err != nil {
		t.Fatal(err)
	}
	changed, err := svc.GetListing(ctx, seller, item.ID, false)
	if err != nil || changed.Version == item.Version || changed.ContentVersion == item.ContentVersion {
		t.Fatal("second source not versioned", changed, err)
	}
	index = 0
	if _, _, err = svc.ReviewListingFileAt(ctx, reviewer, item.ID, "source", item.Version, "test", &index); !errors.Is(err, marketplace.ErrListingConflict) {
		t.Fatal("stale review", err)
	}
	if _, err = svc.MutateListing(ctx, seller, item.ID, "edit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, Draft: &d}); !errors.Is(err, marketplace.ErrListingConflict) {
		t.Fatal("stale edit", err)
	}
	// An older client omitting the manifest cannot silently truncate it.
	single := d
	single.Files = nil
	single.IncludedFiles = []string{"workflow.txt"}
	if _, err = svc.MutateListing(ctx, seller, item.ID, "edit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: changed.Version, Draft: &single}); !errors.Is(err, marketplace.ErrInvalidListing) {
		t.Fatal("old client dropped members", err)
	}
	d.Files[0], d.Files[1] = d.Files[1], d.Files[0]
	d.AssetID = d.Files[0].AssetID
	d.IncludedFiles = []string{d.Files[0].Name, d.Files[1].Name}
	edited, err := svc.MutateListing(ctx, seller, item.ID, "edit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: changed.Version, Draft: &d})
	if err != nil || edited.Files[0] != d.Files[0] || edited.Version == changed.Version {
		t.Fatal("ordered edit", edited, err)
	}
	single.Files = []marketplace.ProductFile{}
	restored, err := svc.MutateListing(ctx, seller, item.ID, "edit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: edited.Version, Draft: &single})
	if err != nil || len(restored.Files) != 0 {
		t.Fatal("explicit single file", restored, err)
	}
	if _, err = svc.MutateListing(ctx, seller, item.ID, "submit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: restored.Version, RightsConfirmed: true}); err != nil {
		t.Fatal("single-file submission", err)
	}
	// Even when the current bundle was removed, the immutable command history
	// is evidence: a down migration must not discard its source protections.
	down, err := os.ReadFile("../platform/database/migrations/0112_product_listing_files.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback discarded file evidence")
	}
}

func TestProductFileSourcesValidationAndPublicProtection(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, reviewer, base := bundleListingFixture(t, pool)
	svc := marketplace.NewService(pool)
	for _, name := range []string{"workflow.txt", "WORKFLOW.TXT", "ｗｏｒｋｆｌｏｗ.txt", "../secret", "HCAI-MANIFEST.json", "CON.txt", "e\u0301.txt", "line\n.txt"} {
		d := base
		d.Files = append([]marketplace.ProductFile(nil), base.Files...)
		d.IncludedFiles = append([]string(nil), base.IncludedFiles...)
		d.Files[1].Name = name
		d.IncludedFiles[1] = name
		if _, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &d}); !errors.Is(err, marketplace.ErrInvalidListing) {
			t.Fatal("unsafe bundle name", name, err)
		}
	}
	for _, change := range []string{
		`UPDATE assets SET owner_id='` + reviewer.String() + `' WHERE id=$1`,
		`UPDATE assets SET scan_status='pending' WHERE id=$1`,
		`UPDATE assets SET license_code='task-contract' WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, change, base.Files[1].AssetID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &base}); !errors.Is(err, marketplace.ErrListingSource) {
			t.Fatal("ineligible non-first source", change, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE assets SET owner_id=$2,scan_status='clean',license_code='hcai-commercial-standard-v1',storage_key=$1::uuid::text||'.txt' WHERE id=$1`, base.Files[1].AssetID, seller); err != nil {
			t.Fatal(err)
		}
	}
	item, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &base})
	if err != nil {
		t.Fatal(err)
	}
	second := base.Files[1].AssetID
	var candidate bool
	for _, view := range []string{"product_preview_candidates", "product_repair_backup_candidates"} {
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+view+` WHERE asset_id=$1)`, second).Scan(&candidate); err != nil || candidate {
			t.Fatal("member leaked into candidate list", view, candidate, err)
		}
	}
	assetService := assets.NewService(pool, t.TempDir())
	if _, err = assetService.Content(ctx, uuid.Nil, second); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("anonymous member read", err)
	}
	if _, err = community.NewRepository(pool).Publish(ctx, seller, community.PublishInput{AssetID: second, Title: "Expose bundle member", AIDisclosure: "Original source", PromptVisibility: "private"}); !errors.Is(err, community.ErrForbidden) {
		t.Fatal("member published", err)
	}
	// A later alias must also be protected, even if it bypassed normal source
	// candidate admission. Public projection must not expose matching bytes.
	// Production's uniqueness index prevents creating it; remove it only in
	// this isolated schema to exercise historical/corrupted storage aliases.
	if _, err = pool.Exec(ctx, `DROP INDEX assets_storage_object_unique`); err != nil {
		t.Fatal(err)
	}
	alias := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type,scan_status,storage_backend,storage_key) SELECT $1,owner_id,kind,'Alias',media_url,mime_type,source_type,scan_status,storage_backend,storage_key FROM assets WHERE id=$2`, alias, second); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_delivery_roots WHERE asset_id=$1)`, alias).Scan(&candidate); err != nil || !candidate {
		t.Fatal("alias unprotected", candidate, err)
	}
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_preview_candidates WHERE asset_id=$1)`, alias).Scan(&candidate); err != nil || candidate {
		t.Fatal("alias became preview", candidate, err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM product_listing_files WHERE product_id=$1 AND position=1`, item.ID); err == nil {
		t.Fatal("partial manifest committed")
	}
	if _, err = pool.Exec(ctx, `UPDATE product_listing_files SET position=5 WHERE product_id=$1 AND position=1`, item.ID); err == nil {
		t.Fatal("non-contiguous manifest committed")
	}
	if _, err = pool.Exec(ctx, `UPDATE products SET preview_asset_id=$2 WHERE id=$1`, item.ID, second); err == nil {
		t.Fatal("member became own sample")
	}
}

func TestProductFileMigrationRoundTripPreservesSingleFileVersions(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	listingFixture(t, pool)
	var id uuid.UUID
	var version, content, offer string
	if err := pool.QueryRow(ctx, `SELECT v.product_id,v.version,v.content_version,o.offer_version FROM product_listing_versions v JOIN product_offers o ON o.product_id=v.product_id LIMIT 1`).Scan(&id, &version, &content, &offer); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"0114_product_bundle_lifecycle.down", "0113_product_bundle_snapshots.down", "0112_product_listing_files.down", "0112_product_listing_files.up", "0113_product_bundle_snapshots.up", "0114_product_bundle_lifecycle.up"} {
		body, err := os.ReadFile("../platform/database/migrations/" + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(body)); err != nil {
			t.Fatal(direction, err)
		}
		var gotVersion, gotContent, gotOffer string
		if err = pool.QueryRow(ctx, `SELECT v.version,v.content_version,o.offer_version FROM product_listing_versions v JOIN product_offers o ON o.product_id=v.product_id WHERE v.product_id=$1`, id).Scan(&gotVersion, &gotContent, &gotOffer); err != nil || gotVersion != version || gotContent != content || gotOffer != offer {
			t.Fatal("single-file version changed", direction, err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_listing_files`).Scan(&n); err != nil || n != 0 {
		t.Fatal("fabricated historical manifest", n, err)
	}
}

func TestProductFileSecondarySourceRechecksAfterLockWait(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	seller, _, draft := bundleListingFixture(t, pool)
	second := draft.Files[1].AssetID
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var pid int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM assets WHERE id=$1 FOR UPDATE`, second).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, e := marketplace.NewService(pool).MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "wait-for-second-source", marketplace.ListingMutation{Draft: &draft})
		finished <- e
	}()
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err = <-finished:
			t.Fatal("did not lock second source", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Only a new public reference is committed. The asset itself need not
	// change for the previously observed qualification to become invalid.
	if _, err = tx.Exec(ctx, `INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at) VALUES($1,$2,$3,'Published second member','Private before the lock wait','Imported','published','Original text',now())`, uuid.New(), seller, second); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if !errors.Is(err, marketplace.ErrListingSource) {
			t.Fatal("stale source qualification", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_listing_commands WHERE actor_id=$1`, seller).Scan(&n); err != nil || n != 0 {
		t.Fatal("rejected bundle left evidence", n, err)
	}
}

func TestProductFileManifestBoundsAndConflictingIdentities(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, _, base := bundleListingFixture(t, pool)
	svc := marketplace.NewService(pool)
	for _, c := range []struct {
		name   string
		change func(*marketplace.ProductDraft)
	}{
		{"duplicate source", func(d *marketplace.ProductDraft) { d.Files[1].AssetID = d.AssetID }},
		{"wrong primary", func(d *marketplace.ProductDraft) { d.AssetID = d.Files[1].AssetID }},
		{"nil member", func(d *marketplace.ProductDraft) { d.Files[1].AssetID = uuid.Nil }},
		{"conflicting label", func(d *marketplace.ProductDraft) { d.IncludedFiles[1] = "unrelated.txt" }},
		{"member as sample", func(d *marketplace.ProductDraft) { d.PreviewAssetID = &d.Files[1].AssetID }},
		{"only one member", func(d *marketplace.ProductDraft) { d.Files = d.Files[:1]; d.IncludedFiles = d.IncludedFiles[:1] }},
		{"console input device", func(d *marketplace.ProductDraft) { d.Files[0].Name = "CONIN$"; d.IncludedFiles[0] = "CONIN$" }},
		{"normalized console output device", func(d *marketplace.ProductDraft) {
			d.Files[0].Name = "ＣＯＮＯＵＴ＄.txt"
			d.IncludedFiles[0] = "ＣＯＮＯＵＴ＄.txt"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := base
			d.Files = append([]marketplace.ProductFile(nil), base.Files...)
			d.IncludedFiles = append([]string(nil), base.IncludedFiles...)
			c.change(&d)
			if _, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "invalid-file-list", marketplace.ListingMutation{Draft: &d}); !errors.Is(err, marketplace.ErrInvalidListing) {
				t.Fatal(err)
			}
		})
	}
	for i := 2; i < 20; i++ {
		id := uuid.New()
		name := fmt.Sprintf("file-%02d.txt", i)
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type,scan_status,storage_backend,storage_key,license_code) VALUES($1,$2,'document','Additional member',$3,'text/plain','upload','clean','local_file',$1::uuid::text||'.txt','hcai-commercial-standard-v1')`, id, seller, "/api/v1/assets/"+id.String()+"/content"); err != nil {
			t.Fatal(err)
		}
		base.Files = append(base.Files, marketplace.ProductFile{AssetID: id, Name: name})
		base.IncludedFiles = append(base.IncludedFiles, name)
	}
	full, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "twenty-files", marketplace.ListingMutation{Draft: &base})
	if err != nil || len(full.Files) != 20 || full.Files[19] != base.Files[19] {
		t.Fatal("valid twenty-member list", full, err)
	}
	base.Files = append(base.Files, marketplace.ProductFile{AssetID: uuid.New(), Name: "overflow.txt"})
	base.IncludedFiles = append(base.IncludedFiles, "overflow.txt")
	if _, err = svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "twenty-one-files", marketplace.ListingMutation{Draft: &base}); !errors.Is(err, marketplace.ErrInvalidListing) {
		t.Fatal("oversize list", err)
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_listing_commands WHERE actor_id=$1`, seller).Scan(&n); err != nil || n != 1 {
		t.Fatal("invalid input produced command evidence", n, err)
	}
}
