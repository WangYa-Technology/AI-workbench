package assets_test

import (
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"testing"
)

func TestSecondaryReferenceAndMaskUsage(t *testing.T) {
	p, close := assetTestPool(t)
	defer close()
	ctx := t.Context()
	owner, first, second, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := p.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,'audit@fixture.test','audit','Audit','creator','active')`, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{first, second} {
		_, err = p.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,family_id,version_number) VALUES($1,$2,'image','Reference','/media/ref.png','image/png','clean','delivery','personal',$1,1)`, id, owner)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = p.Exec(ctx, `INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,source_asset_id) VALUES($1,$2,'image','local_test','Local','Reference audit','succeeded',100,35,$3)`, generation, owner, first)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(ctx, `INSERT INTO generation_reference_assets(generation_id,asset_id,position) VALUES($1,$2,0),($1,$3,1)`, generation, first, second)
	if err != nil {
		t.Fatal(err)
	}
	s := assets.NewService(p, t.TempDir())
	a, err := s.GetOwned(ctx, owner, first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.GetOwned(ctx, owner, second)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Usages) != 1 || len(b.Usages) != 1 {
		t.Fatalf("missing reference graph: %d %d", len(a.Usages), len(b.Usages))
	}

	// Now remove references and rely only on the mask relationship.
	if _, err = p.Exec(ctx, `DELETE FROM generation_reference_assets WHERE generation_id=$1`, generation); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `UPDATE generations SET mask_asset_id=$2 WHERE id=$1`, generation, second); err != nil {
		t.Fatal(err)
	}
	b, err = s.GetOwned(ctx, owner, second)
	if err != nil || len(b.Usages) != 1 {
		t.Fatal("mask missing", err, b.Usages)
	}
}
