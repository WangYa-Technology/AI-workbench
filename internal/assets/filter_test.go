package assets_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
)

func TestAssetFiltersBeforePaginationAndCursorBinding(t *testing.T) {
	p, close := assetTestPool(t)
	defer close()
	ctx := t.Context()
	owner, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{owner, other} {
		if _, err := p.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Filters','creator')`, id, id.String()+"@test.local", "f_"+id.String()[:8]); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 65; i++ {
		id := uuid.New()
		title := fmt.Sprintf("Garden study %02d", i)
		kind, status := "image", "clean"
		if i%2 == 0 {
			kind = "video"
		}
		if i%3 == 0 {
			status = "review"
		}
		if _, err := p.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,family_id,version_number) VALUES($1,$2,$3,$4,'/media/fixture','image/png',$5,'delivery','creator-owned',$1,1)`, id, owner, kind, title, status); err != nil {
			t.Fatal(err)
		}
	}
	service := assets.NewService(p, t.TempDir())
	filter := assets.ListInput{Query: "gArDeN", Kind: "video", ScanStatus: "clean", Source: "delivery", Limit: 5}
	first, err := service.List(ctx, owner, filter)
	if err != nil || first.Total != 22 || len(first.Items) != 5 || first.NextCursor == nil {
		t.Fatal(first, err)
	}
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for {
		filter.Cursor = cursor
		page, e := service.List(ctx, owner, filter)
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range page.Items {
			if seen[a.ID] || a.Kind != "video" || a.ScanStatus != "clean" {
				t.Fatal(a)
			}
			seen[a.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 22 {
		t.Fatal(len(seen))
	}
	for _, mutate := range []func(*assets.ListInput){func(f *assets.ListInput) { f.Query = "other" }, func(f *assets.ListInput) { f.Kind = "image" }, func(f *assets.ListInput) { f.ScanStatus = "review" }, func(f *assets.ListInput) { f.Source = "generation" }} {
		candidate := filter
		candidate.Cursor = *first.NextCursor
		mutate(&candidate)
		if _, e := service.List(ctx, owner, candidate); !errors.Is(e, assets.ErrInvalidList) {
			t.Fatal("cross-filter cursor accepted", e)
		}
	}
	for _, q := range []string{"%", "_", "' OR 1=1 --"} {
		page, e := service.List(ctx, owner, assets.ListInput{Query: q})
		if e != nil || page.Total != 0 {
			t.Fatal("query was interpreted rather than literal", q, e, page.Total)
		}
	}
	if page, e := service.List(ctx, other, assets.ListInput{Query: "Garden"}); e != nil || page.Total != 0 {
		t.Fatal("owner filter lost", e)
	}
	for _, invalid := range []assets.ListInput{{Query: strings.Repeat("图", 121)}, {Query: "bad\xff"}, {Kind: "bogus"}, {ScanStatus: "safe"}, {Source: "bogus"}} {
		if _, e := service.List(ctx, owner, invalid); !errors.Is(e, assets.ErrInvalidList) {
			t.Fatal("invalid filter accepted", e)
		}
	}
}
