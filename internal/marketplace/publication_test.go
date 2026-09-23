package marketplace_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
	"github.com/jackc/pgx/v5/pgxpool"
)

func listingFixture(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID, marketplace.ProductDraft) {
	t.Helper()
	ctx := context.Background()
	seller, reviewer := uuid.New(), uuid.New()
	seedMarketplaceProduct(t, pool, seller, reviewer, uuid.New(), uuid.New())
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, reviewer); err != nil {
		t.Fatal(err)
	}
	source, sample := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{source, sample} {
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type,scan_status,storage_backend,storage_key,license_code) VALUES($1,$2,'document','Independent file',$3,'text/plain','upload','clean','local_file',$1::uuid::text||'.txt','hcai-commercial-standard-v1')`, id, seller, "/api/v1/assets/"+id.String()+"/content"); err != nil {
			t.Fatal(err)
		}
	}
	return seller, reviewer, marketplace.ProductDraft{Title: "Independent workflow", Description: "One complete file with a separate public sample.", ProductType: "workflow", Category: "market_workflow", AssetID: source, PreviewAssetID: &sample, PriceCents: 1900, Currency: "USD", LicenseCode: "hcai-commercial-standard-v1", AIDisclosure: "AI-assisted; seller-reviewed.", IncludedFiles: []string{"workflow.txt"}, Compatibility: "Text editor"}
}

func TestSellerPublicationLifecycleAndApprovalBinding(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, reviewer, draft := listingFixture(t, pool)
	svc := marketplace.NewService(pool)
	input := marketplace.ListingMutation{Draft: &draft}
	item, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", "create-product-001", "test", input)
	if err != nil || item.Status != "draft" || item.ReviewStatus != "draft" {
		t.Fatal(item, err)
	}
	replay, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", "create-product-001", "test", input)
	if err != nil || replay.ID != item.ID {
		t.Fatal("create replay", replay, err)
	}
	draft.Title = "Changed command"
	if _, err = svc.MutateListing(ctx, seller, uuid.Nil, "create", "create-product-001", "test", marketplace.ListingMutation{Draft: &draft}); !errors.Is(err, marketplace.ErrIdempotencyConflict) {
		t.Fatal("changed payload replay", err)
	}
	if _, err = svc.GetProduct(ctx, uuid.Nil, item.ID); !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatal("draft is public", err)
	}
	if _, err = svc.GetListing(ctx, reviewer, item.ID, false); !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatal("foreign private listing", err)
	}
	if _, err = svc.GetListing(ctx, seller, item.ID, true); !errors.Is(err, marketplace.ErrListingForbidden) {
		t.Fatal("seller entered review", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_listing_commands WHERE product_id=$1`, item.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("replay created evidence twice", count, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE product_listing_commands SET action='edit' WHERE product_id=$1`, item.ID); err == nil {
		t.Fatal("command evidence mutable")
	}
	submit := marketplace.ListingMutation{ExpectedVersion: item.Version, RightsConfirmed: true}
	pending, err := svc.MutateListing(ctx, seller, item.ID, "submit", "submit-product-001", "test", submit)
	if err != nil || pending.ReviewStatus != "pending" {
		t.Fatal("submit", pending, err)
	}
	if _, err = svc.MutateListing(ctx, seller, item.ID, "edit", "edit-pending-001", "test", marketplace.ListingMutation{ExpectedVersion: pending.Version, Draft: &draft}); !errors.Is(err, marketplace.ErrListingConflict) {
		t.Fatal("edited during review", err)
	}
	approve := marketplace.ListingMutation{ExpectedVersion: pending.Version, Confirmed: true, Reason: "Reviewed rights and the independent delivery."}
	active, err := svc.MutateListing(ctx, reviewer, item.ID, "approve", "approve-product-001", "test", approve)
	if err != nil || active.Status != "active" || active.ReviewStatus != "approved" {
		t.Fatal("approve", active, err)
	}
	if _, err = svc.GetProduct(ctx, uuid.Nil, item.ID); err != nil {
		t.Fatal("approved not public", err)
	}
	if _, err = svc.MutateListing(ctx, reviewer, item.ID, "approve", "approve-product-001", "test", approve); err != nil {
		t.Fatal("approval replay", err)
	}
	if _, err = svc.SetPreview(ctx, seller, item.ID, marketplace.PreviewUpdate{OfferVersion: active.ContentVersion}, "test"); !errors.Is(err, marketplace.ErrPreviewConflict) {
		t.Fatal("legacy preview bypass", err)
	}
	// Approval is tied to actual terms, not just a mutable status flag.
	if _, err = pool.Exec(ctx, `UPDATE licenses SET terms=terms||' Additional condition.' WHERE code=$1`, draft.LicenseCode); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.GetProduct(ctx, uuid.Nil, item.ID); !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatal("unreviewed terms visible", err)
	}
	stale := active.Version
	active, err = svc.GetListing(ctx, seller, item.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.MutateListing(ctx, seller, item.ID, "pause", "stale-pause-001", "test", marketplace.ListingMutation{ExpectedVersion: stale}); !errors.Is(err, marketplace.ErrListingConflict) {
		t.Fatal("stale pause accepted", err)
	}
	paused, err := svc.MutateListing(ctx, seller, item.ID, "pause", "pause-product-001", "test", marketplace.ListingMutation{ExpectedVersion: active.Version})
	if err != nil || paused.Status != "paused" {
		t.Fatal("pause", paused, err)
	}
	edited, err := svc.MutateListing(ctx, seller, item.ID, "edit", "edit-product-001", "test", marketplace.ListingMutation{ExpectedVersion: paused.Version, Draft: &draft})
	if err != nil || edited.ReviewStatus != "draft" || edited.Title != draft.Title {
		t.Fatal("edit", edited, err)
	}
	blocked, err := svc.MutateListing(ctx, reviewer, item.ID, "block", "block-product-001", "test", marketplace.ListingMutation{ExpectedVersion: edited.Version, Confirmed: true, Reason: "Rights evidence is disputed; publication blocked."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.MutateListing(ctx, seller, item.ID, "submit", "blocked-submit-001", "test", marketplace.ListingMutation{ExpectedVersion: blocked.Version, RightsConfirmed: true}); !errors.Is(err, marketplace.ErrListingConflict) {
		t.Fatal("seller lifted block", err)
	}
}

func TestSellerSourceBoundaryAndPagination(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, reviewer, draft := listingFixture(t, pool)
	svc := marketplace.NewService(pool)
	assetService := assets.NewService(pool, t.TempDir())
	candidates, err := assetService.List(ctx, seller, assets.ListInput{Purpose: "product_source", Limit: 1})
	if err != nil || candidates.Total != 2 || len(candidates.Items) != 1 || candidates.NextCursor == nil {
		t.Fatal("source page", candidates, err)
	}
	if _, err = assetService.List(ctx, seller, assets.ListInput{Purpose: "product_preview", Cursor: *candidates.NextCursor}); !errors.Is(err, assets.ErrInvalidList) {
		t.Fatal("cross purpose cursor", err)
	}
	for _, change := range []string{
		`UPDATE assets SET owner_id='` + reviewer.String() + `' WHERE id=$1`,
		`UPDATE assets SET scan_status='pending' WHERE id=$1`,
		`UPDATE assets SET source_type='purchase',storage_backend=NULL,storage_key=NULL WHERE id=$1`,
		`UPDATE assets SET license_code='task-contract' WHERE id=$1`,
	} {
		if _, err = pool.Exec(ctx, change, draft.AssetID); err != nil {
			t.Fatal(err)
		}
		if _, err = svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &draft}); !errors.Is(err, marketplace.ErrListingSource) {
			t.Fatal("ineligible source", change, err)
		}
		if _, err = pool.Exec(ctx, `UPDATE assets SET owner_id=$2,scan_status='clean',source_type='upload',license_code='hcai-commercial-standard-v1',storage_backend='local_file',storage_key=$1::uuid::text||'.txt' WHERE id=$1`, draft.AssetID, seller); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		d := draft
		d.Title = fmt.Sprintf("Draft %d", i)
		if _, err = svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &d}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := svc.ListListings(ctx, seller, false, marketplace.ListingFilter{Status: "draft", Limit: 2})
	if err != nil || page.Total != 3 || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	next, err := svc.ListListings(ctx, seller, false, marketplace.ListingFilter{Status: "draft", Limit: 2, Cursor: *page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Total != 3 {
		t.Fatal(next, err)
	}
	if _, err = svc.ListListings(ctx, reviewer, false, marketplace.ListingFilter{Status: "draft", Cursor: *page.NextCursor}); !errors.Is(err, marketplace.ErrInvalidListing) {
		t.Fatal("cross-owner cursor", err)
	}
	if _, err = svc.ListListings(ctx, seller, false, marketplace.ListingFilter{Status: "active", Cursor: *page.NextCursor}); !errors.Is(err, marketplace.ErrInvalidListing) {
		t.Fatal("cross-filter cursor", err)
	}
	// A file already used as any product's sample cannot silently disappear
	// from that listing when another listing claims it as a private original.
	d := draft
	d.AssetID = *draft.PreviewAssetID
	d.PreviewAssetID = nil
	if _, err = svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &d}); !errors.Is(err, marketplace.ErrListingSource) {
		t.Fatal("sample became source", err)
	}
}

func TestSellerConcurrentEditsAndSubmissions(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, _, draft := listingFixture(t, pool)
	svc := marketplace.NewService(pool)
	item, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &draft})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	out := make(chan error, 2)
	var wg sync.WaitGroup
	for _, action := range []string{"edit", "submit"} {
		wg.Add(1)
		go func(action string) {
			defer wg.Done()
			<-start
			in := marketplace.ListingMutation{ExpectedVersion: item.Version, RightsConfirmed: true}
			if action == "edit" {
				in.Draft = &draft
			}
			_, e := svc.MutateListing(ctx, seller, item.ID, action, uuid.NewString(), "race", in)
			out <- e
		}(action)
	}
	close(start)
	wg.Wait()
	close(out)
	success := 0
	for e := range out {
		if e == nil {
			success++
		} else if !errors.Is(e, marketplace.ErrListingConflict) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal("multiple writes accepted same version", success)
	}
}

func TestProductSourceConversionSerializesWithCommunityAndTaskDelivery(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, reviewer, base := listingFixture(t, pool)
	svc := marketplace.NewService(pool)
	for _, kind := range []string{"community", "task"} {
		for attempt := 0; attempt < 4; attempt++ {
			source := uuid.New()
			d := base
			d.AssetID = source
			if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type,scan_status,storage_backend,storage_key,license_code) VALUES($1,$2,'document','Contended original',$3,'text/plain','upload','clean','local_file',$1::uuid::text||'.txt','hcai-commercial-standard-v1')`, source, seller, "/api/v1/assets/"+source.String()+"/content"); err != nil {
				t.Fatal(err)
			}
			demand := uuid.New()
			if kind == "task" {
				if _, err := pool.Exec(ctx, `INSERT INTO demands(id,client_id,assignee_id,title,brief,deliverable_type,budget_cents,currency,deadline,status) VALUES($1,$2,$3,'Concurrent task','Delivery ownership test','mixed',1000,'USD',now()+interval '2 days','assigned')`, demand, reviewer, seller); err != nil {
					t.Fatal(err)
				}
			}
			start := make(chan struct{})
			out := make(chan error, 2)
			go func() {
				<-start
				_, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "source-race", marketplace.ListingMutation{Draft: &d})
				out <- err
			}()
			go func() {
				<-start
				if kind == "community" {
					_, err := community.NewRepository(pool).Publish(ctx, seller, community.PublishInput{AssetID: source, Title: "Concurrent public work", Summary: "Original text", AIDisclosure: "Seller-owned original", PromptVisibility: "private"})
					out <- err
				} else {
					_, err := tasks.NewService(pool).Deliver(ctx, seller, demand, tasks.DeliverInput{AssetID: source, Note: "The original task delivery.", RightsEvidence: "Independent source and task-specific license.", AIDisclosure: "Original text reviewed by its creator.", RightsConfirmed: true}, uuid.NewString())
					out <- err
				}
			}()
			close(start)
			succeeded := 0
			for range 2 {
				err := <-out
				if err == nil {
					succeeded++
				} else if !errors.Is(err, marketplace.ErrListingSource) && !errors.Is(err, community.ErrForbidden) && !errors.Is(err, tasks.ErrForbidden) {
					t.Fatal(kind, "unexpected race error", err)
				}
			}
			if succeeded != 1 {
				t.Fatal(kind, "original acquired for incompatible uses", succeeded)
			}
		}
	}
}

func TestPublicationMigrationRollbackPreservesEvidence(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, _, draft := listingFixture(t, pool)
	down, err := os.ReadFile("../platform/database/migrations/0093_product_publication.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0093_product_publication.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Existing legacy listings retain their original public visibility on an
	// unused migration's down/up cycle. No historical approvals are invented.
	if _, err = pool.Exec(ctx, string(down)); err != nil {
		t.Fatal("unused rollback", err)
	}
	if _, err = pool.Exec(ctx, string(up)); err != nil {
		t.Fatal("reapply", err)
	}
	svc := marketplace.NewService(pool)
	public, err := svc.ListProducts(ctx, uuid.Nil, marketplace.ListFilter{})
	if err != nil || public.Total != 1 {
		t.Fatal("legacy visibility changed", public, err)
	}
	item, err := svc.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &draft})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback discarded publication evidence")
	}
	current, err := svc.GetListing(ctx, seller, item.ID, false)
	if err != nil || current.Version != item.Version {
		t.Fatal("failed rollback changed evidence", current, err)
	}
	if _, err = svc.GetProduct(ctx, uuid.Nil, item.ID); !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatal("rollback exposed draft", err)
	}
}

func TestProductSourceRechecksReferencesAfterLockWait(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	seller, _, draft := listingFixture(t, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var pid int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM assets WHERE id=$1 FOR UPDATE`, draft.AssetID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := marketplace.NewService(pool).MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "locked-source", marketplace.ListingMutation{Draft: &draft})
		finished <- err
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
		case err := <-finished:
			t.Fatal("command did not wait for original", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Only the reference changes; no asset metadata changes. A stale transaction
	// snapshot would miss this work despite having acquired the asset row lock.
	if _, err = tx.Exec(ctx, `INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at) VALUES($1,$2,$3,'Work published while waiting','Public reference','Imported','published','Original text',now())`, uuid.New(), seller, draft.AssetID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if !errors.Is(err, marketplace.ErrListingSource) {
			t.Fatal("stale original qualification", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_listing_commands WHERE actor_id=$1`, seller).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed conversion left effects", count, err)
	}
}
