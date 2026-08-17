package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
)

func TestModelRouteHistoryStablePaginationAndExactCurrentRoute(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	var parentID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT active_revision_id FROM model_route_state WHERE mode='image'`).Scan(&parentID); err != nil {
		t.Fatal(err)
	}
	for version := 2; version <= 106; version++ {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO model_route_revisions(id,mode,version,parent_revision_id,provider_profile_id,name,timeout_seconds,max_attempts,reason) VALUES($1,'image',$2,$3,'local-image-v1',$4,120,3,'Immutable model route pagination evidence.')`, id, version, parentID, "Image route revision"); err != nil {
			t.Fatal(err)
		}
		parentID = id
	}
	if _, err := pool.Exec(ctx, `UPDATE model_route_state SET active_revision_id=$1,version=106 WHERE mode='image'`, parentID); err != nil {
		t.Fatal(err)
	}

	service := admin.NewService(pool, true)
	seen := make(map[uuid.UUID]struct{})
	cursor := ""
	for {
		page, err := service.GetModelRoutePolicy(ctx, admin.ModelRouteHistoryInput{Mode: "image", Cursor: cursor, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Routes) != 4 || page.Routes["image"].Version != 106 || page.Routes["image"].ID != parentID {
			t.Fatalf("exact current routes were lost on an old history page: %#v", page.Routes)
		}
		for _, item := range page.History["image"] {
			if item.Mode != "image" {
				t.Fatalf("cross-mode revision leaked: %#v", item)
			}
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("duplicate model route revision %s", item.ID)
			}
			seen[item.ID] = struct{}{}
		}
		cursor = page.NextCursors["image"]
		if cursor == "" {
			break
		}
	}
	if len(seen) != 106 {
		t.Fatalf("expected 106 image route revisions, got %d", len(seen))
	}
	for _, input := range []admin.ModelRouteHistoryInput{
		{Mode: "image", Cursor: cursor + "modified", Limit: 50},
		{Mode: "image", Limit: 51},
		{Cursor: "modified", Limit: 20},
		{Mode: "voice", Limit: 20},
	} {
		if _, err := service.GetModelRoutePolicy(ctx, input); !errors.Is(err, admin.ErrInvalidModelRouteHistory) {
			t.Fatalf("invalid model route history input accepted: %#v err=%v", input, err)
		}
	}
}
