package assets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
)

func TestAssetListStablePaginationAppliesOwnershipVersionAndEntitlementFirst(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, outsiderID, sellerID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Paged Asset Owner','creator','active'),
		($4,$5,$6,'Paged Asset Outsider','member','active'),
		($7,$8,$9,'Paged Asset Seller','creator','active')`,
		ownerID, ownerID.String()+"@test.local", "asset_page_"+ownerID.String()[:8],
		outsiderID, outsiderID.String()+"@test.local", "asset_out_"+outsiderID.String()[:8],
		sellerID, sellerID.String()+"@test.local", "asset_sell_"+sellerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,created_at)
		SELECT $1,'image','Paged owned Asset '||value,'/media/paged-owned-'||value||'.jpg','image/jpeg','clean','upload','hcai-personal-v1',now()-value*interval '1 second'
		FROM generate_series(1,106) value`, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,created_at)
		SELECT $1,'image','Foreign Asset '||value,'/media/foreign-'||value||'.jpg','image/jpeg','clean','upload','hcai-personal-v1',now()+interval '1 hour'-value*interval '1 second'
		FROM generate_series(1,12) value`, outsiderID); err != nil {
		t.Fatal(err)
	}

	rootID, versionID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,created_at)
		VALUES($1,$2,'image','Excluded family root','/media/family-root.jpg','image/jpeg','clean','upload','hcai-personal-v1',now()+interval '3 hours')`, rootID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,family_id,version_number,supersedes_asset_id,created_at)
		VALUES($3,$2,'image','Visible family revision','/media/family-v2.jpg','image/jpeg','clean','upload','hcai-personal-v1',$1,2,$1,now()+interval '2 hours')`, rootID, ownerID, versionID); err != nil {
		t.Fatal(err)
	}

	sourceID, productID := uuid.New(), uuid.New()
	activeAssetID, revokedAssetID := uuid.New(), uuid.New()
	activeOrderID, revokedOrderID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Licensed source','/media/licensed-source.jpg','image/jpeg','clean','upload','hcai-personal-v1')`, sourceID, sellerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
		VALUES($1,$2,$3,'Paged license product','Pagination entitlement evidence.','asset',1200,'USD','hcai-personal-v1','active')`, productID, sellerID, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,origin_asset_id,license_code,created_at)
		VALUES($1,$3,'image','Active purchased Asset','/media/active-purchase.jpg','image/jpeg','clean','purchase',$4,'hcai-personal-v1',now()+interval '4 hours'),
		      ($2,$3,'image','Revoked purchased Asset','/media/revoked-purchase.jpg','image/jpeg','clean','purchase',$4,'hcai-personal-v1',now()+interval '5 hours')`, activeAssetID, revokedAssetID, ownerID, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot)
		VALUES($1,$3,$4,1200,'USD','fulfilled',now(),'active-page-order','1.0','Personal terms.','Paged license product','HCAI Personal License',7),
		      ($2,$3,$4,1200,'USD','test_refunded',now(),'revoked-page-order','1.0','Personal terms.','Paged license product','HCAI Personal License',7)`, activeOrderID, revokedOrderID, ownerID, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO entitlements(user_id,product_id,order_id,asset_id,license_code,status,granted_at,revoked_at)
		VALUES($1,$2,$3,$5,'hcai-personal-v1','active',now(),NULL),
		      ($1,$2,$4,$6,'hcai-personal-v1','refunded',now(),now())`,
		ownerID, productID, activeOrderID, revokedOrderID, activeAssetID, revokedAssetID); err != nil {
		t.Fatal(err)
	}

	service := assets.NewService(pool, t.TempDir())
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for {
		page, err := service.List(ctx, ownerID, assets.ListInput{Limit: 11, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 11 {
			t.Fatalf("page exceeded requested bound: %d", len(page.Items))
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate Asset across cursor pages: %s", item.ID)
			}
			seen[item.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 108 || !seen[versionID] || !seen[activeAssetID] || seen[rootID] || seen[revokedAssetID] {
		t.Fatalf("pagination boundaries changed: count=%d latest=%t active=%t root=%t revoked=%t", len(seen), seen[versionID], seen[activeAssetID], seen[rootID], seen[revokedAssetID])
	}
	if _, err := service.List(ctx, ownerID, assets.ListInput{Limit: 51}); !errors.Is(err, assets.ErrInvalidList) {
		t.Fatalf("oversized Asset page accepted: %v", err)
	}
	if _, err := service.List(ctx, ownerID, assets.ListInput{Cursor: "modified"}); !errors.Is(err, assets.ErrInvalidList) {
		t.Fatalf("modified Asset cursor accepted: %v", err)
	}
}

func TestSavedWorkStablePaginationAppliesVisibilityAndAccountFirst(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := context.Background()
	authorID, viewerID, outsiderID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Paged Save Author','creator','active'),
		($4,$5,$6,'Paged Save Viewer','member','active'),
		($7,$8,$9,'Paged Save Outsider','member','active')`,
		authorID, authorID.String()+"@test.local", "save_author_"+authorID.String()[:8],
		viewerID, viewerID.String()+"@test.local", "save_view_"+viewerID.String()[:8],
		outsiderID, outsiderID.String()+"@test.local", "save_out_"+outsiderID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		WITH seed AS (
			SELECT value,gen_random_uuid() asset_id,gen_random_uuid() work_id,gen_random_uuid() post_id
			FROM generate_series(1,106) value
		), inserted_assets AS (
			INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
			SELECT asset_id,$1,'image','Saved source '||value,'/media/saved-page-'||value||'.jpg','image/jpeg','clean','demo','hcai-personal-v1' FROM seed RETURNING id
		), inserted_works AS (
			INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
			SELECT work_id,$1,asset_id,'Visible saved Work '||value,'Stable saved reference.','Local Test','published','Local Test disclosure.',now() FROM seed JOIN inserted_assets ON id=asset_id RETURNING id,asset_id
		), inserted_posts AS (
			INSERT INTO posts(id,author_id,work_id,body,status,published_at)
			SELECT post_id,$1,work_id,'Visible saved post.','published',now() FROM seed JOIN inserted_works USING(asset_id) RETURNING id,work_id
		)
		INSERT INTO post_reactions(post_id,user_id,kind,created_at)
		SELECT post_id,$2,'bookmark',now()-value*interval '1 second' FROM seed JOIN inserted_posts ON id=post_id`, authorID, viewerID); err != nil {
		t.Fatal(err)
	}

	insertSavedBoundary := func(label, assetStatus, workStatus, postStatus string, reactionOwner uuid.UUID) uuid.UUID {
		t.Helper()
		assetID, workID, postID := uuid.New(), uuid.New(), uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
			VALUES($1,$2,'image',$3,'/media/saved-boundary.jpg','image/jpeg',$4,'demo','hcai-personal-v1')`, assetID, authorID, label, assetStatus); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
			VALUES($1,$2,$3,$4,'Boundary reference.','Local Test',$5,'Local Test disclosure.',now())`, workID, authorID, assetID, label, workStatus); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO posts(id,author_id,work_id,body,status,published_at)
			VALUES($1,$2,$3,'Boundary post.',$4,now())`, postID, authorID, workID, postStatus); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO post_reactions(post_id,user_id,kind,created_at) VALUES($1,$2,'bookmark',now()+interval '2 hours')`, postID, reactionOwner); err != nil {
			t.Fatal(err)
		}
		return postID
	}
	hiddenPostID := insertSavedBoundary("Hidden Work", "clean", "hidden", "published", viewerID)
	reviewPostID := insertSavedBoundary("Review Asset", "review", "published", "published", viewerID)
	outsiderPostID := insertSavedBoundary("Outsider-only Save", "clean", "published", "published", outsiderID)

	service := assets.NewService(pool, t.TempDir())
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for {
		page, err := service.ListSavedWorks(ctx, viewerID, assets.ListInput{Limit: 13, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.PostID] {
				t.Fatalf("duplicate saved Work across cursor pages: %s", item.PostID)
			}
			seen[item.PostID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 || seen[hiddenPostID] || seen[reviewPostID] || seen[outsiderPostID] {
		t.Fatalf("saved-work boundaries changed: count=%d hidden=%t review=%t outsider=%t", len(seen), seen[hiddenPostID], seen[reviewPostID], seen[outsiderPostID])
	}
	if _, err := service.ListSavedWorks(ctx, viewerID, assets.ListInput{Cursor: "modified"}); !errors.Is(err, assets.ErrInvalidList) {
		t.Fatalf("modified saved-work cursor accepted: %v", err)
	}
}

func TestAssetUsageStablePaginationSpansFamilyAndMixedKinds(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, outsiderID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Paged Usage Owner','creator','active'),
		($4,$5,$6,'Paged Usage Outsider','creator','active')`,
		ownerID, ownerID.String()+"@test.local", "usage_page_"+ownerID.String()[:8],
		outsiderID, outsiderID.String()+"@test.local", "usage_out_"+outsiderID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	rootID, versionID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Usage page root','/media/usage-page-root.jpg','image/jpeg','clean','upload','hcai-personal-v1')`, rootID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,family_id,version_number,supersedes_asset_id)
		VALUES($3,$2,'image','Usage page revision','/media/usage-page-v2.jpg','image/jpeg','clean','upload','hcai-personal-v1',$1,2,$1)`, rootID, ownerID, versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,source_asset_id,created_at)
		SELECT $1,'image','local_test','HCAI Image Local','Paged generation '||value,'succeeded',100,35,CASE WHEN value%2=0 THEN $2::uuid ELSE $3::uuid END,now()-value*interval '1 second'
		FROM generate_series(1,27) value`, ownerID, rootID, versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at,created_at)
		SELECT $1,CASE WHEN value%2=0 THEN $2::uuid ELSE $3::uuid END,'Paged Work '||value,'Usage history.','Imported Asset',CASE WHEN value=1 THEN 'hidden' ELSE 'published' END,'Owner supplied source.',now(),now()-value*interval '1 second'
		FROM generate_series(1,27) value`, ownerID, rootID, versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products(seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,created_at)
		SELECT $1,CASE WHEN value%2=0 THEN $2::uuid ELSE $3::uuid END,'Paged product '||value,'Usage history.','asset',1200,'USD','hcai-personal-v1',CASE WHEN value=1 THEN 'paused' ELSE 'active' END,now()-value*interval '1 second'
		FROM generate_series(1,26) value`, ownerID, rootID, versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		WITH inserted_demands AS (
			INSERT INTO demands(client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,created_at)
			SELECT $1,'Paged delivery demand '||value,'Usage pagination delivery.','image',5000,'USD',now()+interval '1 day','submitted',$2,now()-value*interval '1 second'
			FROM generate_series(1,26) value RETURNING id,created_at,title
		)
		INSERT INTO deliveries(demand_id,creator_id,asset_id,note,status,version,created_at)
		SELECT id,$2,CASE WHEN row_number() OVER (ORDER BY id)%2=0 THEN $3::uuid ELSE $4::uuid END,'Paged delivery evidence.','submitted',1,created_at
		FROM inserted_demands`, outsiderID, ownerID, rootID, versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,source_asset_id,created_at)
		VALUES($1,'image','local_test','HCAI Image Local','Foreign usage is private','queued',0,35,$2,now()+interval '1 hour')`, outsiderID, rootID); err != nil {
		t.Fatal(err)
	}

	service := assets.NewService(pool, t.TempDir())
	detail, err := service.GetOwned(ctx, ownerID, versionID)
	if err != nil || len(detail.Usages) != 20 || detail.UsageNextCursor == nil {
		t.Fatalf("bounded Asset detail usage projection: count=%d cursor=%v err=%v", len(detail.Usages), detail.UsageNextCursor, err)
	}
	all := append([]assets.AssetUsage(nil), detail.Usages...)
	cursor := *detail.UsageNextCursor
	for cursor != "" {
		page, err := service.ListUsages(ctx, ownerID, rootID, assets.ListInput{Limit: 17, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil {
			cursor = ""
		} else {
			cursor = *page.NextCursor
		}
	}
	seen := map[string]bool{}
	for index, item := range all {
		key := item.Kind + ":" + item.ResourceID.String()
		if seen[key] {
			t.Fatalf("duplicate usage across cursor pages: %s", key)
		}
		seen[key] = true
		if item.AssetID != rootID && item.AssetID != versionID {
			t.Fatalf("usage escaped the owned Asset family: %#v", item)
		}
		if (item.Kind == "work" && item.Status == "hidden") || (item.Kind == "product" && item.Status == "paused") {
			if item.TargetPath != nil {
				t.Fatalf("unavailable historical usage exposed a deep link: %#v", item)
			}
		}
		if index > 0 {
			previous := all[index-1]
			if item.CreatedAt.After(previous.CreatedAt) || (item.CreatedAt.Equal(previous.CreatedAt) && (item.Kind < previous.Kind || (item.Kind == previous.Kind && item.ResourceID.String() < previous.ResourceID.String()))) {
				t.Fatalf("mixed usage order is unstable at %d: previous=%#v current=%#v", index, previous, item)
			}
		}
	}
	if len(all) != 106 || len(seen) != 106 {
		t.Fatalf("usage history was truncated or duplicated: rows=%d unique=%d", len(all), len(seen))
	}
	if _, err := service.ListUsages(ctx, outsiderID, rootID, assets.ListInput{Cursor: "modified"}); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("ownership was not checked before usage cursor parsing: %v", err)
	}
	if _, err := service.ListUsages(ctx, ownerID, rootID, assets.ListInput{Cursor: "modified"}); !errors.Is(err, assets.ErrInvalidList) {
		t.Fatalf("modified usage cursor accepted: %v", err)
	}
	if _, err := service.ListUsages(ctx, ownerID, rootID, assets.ListInput{Limit: 51}); !errors.Is(err, assets.ErrInvalidList) {
		t.Fatalf("oversized usage page accepted: %v", err)
	}
}
