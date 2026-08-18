package assets_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
)

func TestOwnedAssetUsageGraphAcrossVersions(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, outsiderID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Usage Owner','creator','active'),
		($4,$5,$6,'Usage Outsider','publisher','active')`,
		ownerID, ownerID.String()+"@test.local", "usage_"+ownerID.String()[:8],
		outsiderID, outsiderID.String()+"@test.local", "outside_"+outsiderID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	rootID, versionID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,family_id,version_number,supersedes_asset_id)
		VALUES($1,$2,'image','Usage root','/media/usage-root.jpg','image/jpeg','clean','demo','hcai-personal-v1',$1,1,NULL),
		      ($3,$2,'image','Usage revision','/media/usage-v2.jpg','image/jpeg','clean','demo','hcai-personal-v1',$1,2,$1)`, rootID, ownerID, versionID); err != nil {
		t.Fatal(err)
	}
	generationID, workID, productID, demandID, deliveryID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,source_asset_id)
		VALUES($1,$2,'image','local_test','HCAI Image Local','Create a downstream study','succeeded',100,35,$3)`, generationID, ownerID, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
		VALUES($1,$2,$3,'Published from usage root','Usage graph work.','Imported asset','published','Owner supplied source.',now())`, workID, ownerID, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
		VALUES($1,$2,$3,'Usage workflow product','Versioned workflow listing.','workflow',1200,'USD','hcai-personal-v1','active')`, productID, ownerID, versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id)
		VALUES($1,$2,'Usage graph delivery','Deliver the reviewed Asset version.','image',5000,'USD',$3,'submitted',$4)`, demandID, outsiderID, time.Now().Add(24*time.Hour), ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO deliveries(id,demand_id,creator_id,asset_id,note,status,version)
		VALUES($1,$2,$3,$4,'Review-ready usage evidence.','submitted',1)`, deliveryID, demandID, ownerID, versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,source_asset_id)
		VALUES($1,$2,'image','local_test','HCAI Image Local','Foreign reference must stay private','queued',0,35,$3)`, uuid.New(), outsiderID, rootID); err != nil {
		t.Fatal(err)
	}

	service := assets.NewService(pool, t.TempDir())
	item, err := service.GetOwned(ctx, ownerID, versionID)
	if err != nil || len(item.Usages) != 4 {
		t.Fatalf("version-family usage graph: %#v %v", item.Usages, err)
	}
	want := map[string]struct {
		resourceID uuid.UUID
		assetID    uuid.UUID
		version    int
		pathPart   string
	}{
		"generation": {generationID, rootID, 1, generationID.String()},
		"work":       {workID, rootID, 1, "/works/" + workID.String()},
		"product":    {productID, versionID, 2, "/market/assets/" + productID.String()},
		"delivery":   {deliveryID, versionID, 2, "/market/demands/" + demandID.String()},
	}
	for _, usage := range item.Usages {
		expected, ok := want[usage.Kind]
		if !ok || usage.ResourceID != expected.resourceID || usage.AssetID != expected.assetID || usage.AssetVersion != expected.version || usage.TargetPath == nil || !strings.Contains(*usage.TargetPath, expected.pathPart) {
			t.Fatalf("unexpected %s usage: %#v", usage.Kind, usage)
		}
		delete(want, usage.Kind)
	}
	if len(want) != 0 {
		t.Fatalf("missing usage kinds: %#v", want)
	}
	root, err := service.GetOwned(ctx, ownerID, rootID)
	if err != nil || len(root.Usages) != 4 {
		t.Fatalf("root did not share family usage graph: %#v %v", root.Usages, err)
	}
	if _, err := service.GetOwned(ctx, outsiderID, rootID); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("outsider accessed usage graph: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE works SET status='hidden' WHERE id=$1`, workID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET status='paused' WHERE id=$1`, productID); err != nil {
		t.Fatal(err)
	}
	item, err = service.GetOwned(ctx, ownerID, versionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, usage := range item.Usages {
		if (usage.Kind == "work" || usage.Kind == "product") && usage.TargetPath != nil {
			t.Fatalf("unavailable historical usage retained a deep link: %#v", usage)
		}
	}
}
