package community_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/tasktypes"
	"testing"
)

func TestScopedCategoriesLifecycle(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	directory := tasktypes.NewService(pool)
	repo := community.NewRepository(pool)
	user := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,'category@test.local','category_test','Category','creator','active')`, user); err != nil {
		t.Fatal(err)
	}
	category, err := directory.Create(ctx, tasktypes.Type{Code: "custom_community", NameZh: "自定义", NameEn: "Custom", Icon: "mixed", Scope: "community", SortOrder: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = directory.Create(ctx, category); err == nil {
		t.Fatal("duplicate accepted")
	}
	category.NameZh = "新名称"
	category.SortOrder = 0
	if _, err = directory.Update(ctx, category.Code, category); err != nil {
		t.Fatal(err)
	}
	items, err := directory.List(ctx, "community")
	if err != nil || items[0].NameZh != "新名称" {
		t.Fatalf("order/name: %v %v", items, err)
	}
	tasks, err := directory.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range tasks {
		if item.Scope != "task" {
			t.Fatal("scope leaked")
		}
	}
	var ids []uuid.UUID
	for i := 0; i < 23; i++ {
		post, err := repo.CreatePost(ctx, user, community.PostCreateInput{Title: "Category test", Body: "Unchanged body", Category: category.Code})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, post.ID)
	}
	if _, err = repo.CreatePost(ctx, user, community.PostCreateInput{Title: "Cross scope", Body: "Reject", Category: "market_asset"}); err == nil {
		t.Fatal("cross-scope post accepted")
	}
	page, err := repo.ListPageForViewer(ctx, user, community.PostListInput{Category: category.Code, Limit: 20})
	if err != nil || len(page.Items) != 20 || page.NextCursor == nil {
		t.Fatalf("first page: %v %v", page, err)
	}
	next, err := repo.ListPageForViewer(ctx, user, community.PostListInput{Category: category.Code, Limit: 20, Cursor: *page.NextCursor})
	if err != nil || len(next.Items) != 3 {
		t.Fatalf("second page: %v %v", next, err)
	}
	if err = directory.Delete(ctx, category.Code, "", "community"); err == nil {
		t.Fatal("referenced category deleted without replacement")
	}
	if err = directory.Delete(ctx, category.Code, "market_asset", "community"); err == nil {
		t.Fatal("cross-scope replacement accepted")
	}
	if err = directory.Assign(ctx, "community", ids[0].String(), "market_asset"); err == nil {
		t.Fatal("cross-scope assignment accepted")
	}
	if err = directory.Delete(ctx, category.Code, "community_general", "community"); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		post, err := repo.GetPostForViewer(ctx, user, id)
		if err != nil || post.Category != "community_general" || post.Body != "Unchanged body" {
			t.Fatalf("transfer changed content: %v %v", post, err)
		}
	}
	if err = directory.Delete(ctx, "community_general", "community_tutorial", "community"); err != nil {
		t.Fatal(err)
	}
	post, err := repo.CreatePost(ctx, user, community.PostCreateInput{Title: "After default deletion", Body: "Still publishes"})
	if err != nil || post.Category == "" {
		t.Fatalf("default deletion broke publishing: %v %v", post, err)
	}
}
