package community_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/jackc/pgx/v5"
)

func TestCommentHistoryStablePaginationAndVisibility(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	authorID, commenterID, outsiderID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Comment Author','creator','active'),
		($4,$5,$6,'Commenter','member','active'),
		($7,$8,$9,'Outsider','member','active')`,
		authorID, authorID.String()+"@test.local", "comment_author_"+authorID.String()[:8],
		commenterID, commenterID.String()+"@test.local", "commenter_"+commenterID.String()[:8],
		outsiderID, outsiderID.String()+"@test.local", "comment_outsider_"+outsiderID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	assetID, workID, postID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type) VALUES($1,$2,'image','Comment source','/media/comment.jpg','image/jpeg','clean','demo')`, assetID, authorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO works(id,author_id,asset_id,title,model_name,status,ai_disclosure,published_at) VALUES($1,$2,$3,'Comment work','Imported asset','published','Owner supplied source.',now())`, workID, authorID, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO posts(id,author_id,work_id,body,status,published_at) VALUES($1,$2,$3,'Comment history','published',now())`, postID, authorID, workID); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-2 * time.Hour)
	batch := &pgx.Batch{}
	for index := 0; index < 106; index++ {
		batch.Queue(`INSERT INTO comments(id,post_id,author_id,body,status,created_at) VALUES($1,$2,$3,$4,'published',$5)`, uuid.New(), postID, commenterID, "Stable paginated comment", base.Add(time.Duration(index)*time.Second))
	}
	batch.Queue(`INSERT INTO comments(post_id,author_id,body,status,created_at) VALUES($1,$2,'Hidden comment','hidden',$3)`, postID, outsiderID, base.Add(3*time.Hour))
	results := pool.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}

	repository := community.NewRepository(pool)
	seen := make(map[uuid.UUID]struct{})
	cursor := ""
	var previous time.Time
	for {
		page, err := repository.ListComments(ctx, postID, community.CommentListInput{Cursor: cursor, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.PostID != postID || item.Status != "published" {
				t.Fatalf("invisible comment leaked: %#v", item)
			}
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("duplicate comment %s", item.ID)
			}
			if !previous.IsZero() && item.CreatedAt.Before(previous) {
				t.Fatalf("comment order regressed: %s before %s", item.CreatedAt, previous)
			}
			seen[item.ID], previous = struct{}{}, item.CreatedAt
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 {
		t.Fatalf("expected 106 visible comments, got %d", len(seen))
	}
	if _, err := repository.ListComments(ctx, postID, community.CommentListInput{Cursor: cursor + "modified", Limit: 50}); !errors.Is(err, community.ErrInvalidCommentFilter) {
		t.Fatalf("modified comment cursor was accepted: %v", err)
	}
	if _, err := repository.ListComments(ctx, postID, community.CommentListInput{Limit: 51}); !errors.Is(err, community.ErrInvalidCommentFilter) {
		t.Fatalf("oversized comment page was accepted: %v", err)
	}
}

func TestContentDraftHistoryStablePaginationAndOwnership(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, outsiderID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Draft History Owner','creator','active'),
		($4,$5,$6,'Draft History Outsider','creator','active')`,
		ownerID, ownerID.String()+"@test.local", "draft_history_"+ownerID.String()[:8],
		outsiderID, outsiderID.String()+"@test.local", "draft_outsider_"+outsiderID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	batch := &pgx.Batch{}
	for index := 0; index < 106; index++ {
		assetID, workID, postID := uuid.New(), uuid.New(), uuid.New()
		updatedAt := base.Add(-time.Duration(index) * time.Second)
		batch.Queue(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type) VALUES($1,$2,'image',$3,'/media/draft-history.jpg','image/jpeg','clean','demo')`, assetID, ownerID, "Draft source")
		batch.Queue(`INSERT INTO works(id,author_id,asset_id,title,model_name,status,ai_disclosure,created_at,updated_at) VALUES($1,$2,$3,$4,'Imported asset','draft','Private draft evidence.',$5,$5)`, workID, ownerID, assetID, "Draft history", updatedAt)
		batch.Queue(`INSERT INTO posts(id,author_id,work_id,body,status,created_at,updated_at) VALUES($1,$2,$3,'Private body','draft',$4,$4)`, postID, ownerID, workID, updatedAt)
	}
	outsiderAssetID, outsiderWorkID := uuid.New(), uuid.New()
	batch.Queue(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type) VALUES($1,$2,'image','Outsider source','/media/outsider.jpg','image/jpeg','clean','demo')`, outsiderAssetID, outsiderID)
	batch.Queue(`INSERT INTO works(id,author_id,asset_id,title,model_name,status,ai_disclosure) VALUES($1,$2,$3,'Invisible draft','Imported asset','draft','Private draft evidence.')`, outsiderWorkID, outsiderID, outsiderAssetID)
	batch.Queue(`INSERT INTO posts(author_id,work_id,body,status) VALUES($1,$2,'Invisible','draft')`, outsiderID, outsiderWorkID)
	results := pool.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}

	repository := community.NewRepository(pool)
	seen := make(map[uuid.UUID]struct{})
	cursor := ""
	for {
		page, err := repository.ListDrafts(ctx, ownerID, community.DraftListInput{Cursor: cursor, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("duplicate draft %s", item.ID)
			}
			seen[item.ID] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 {
		t.Fatalf("expected 106 owned drafts, got %d", len(seen))
	}
	if _, err := repository.ListDrafts(ctx, ownerID, community.DraftListInput{Cursor: cursor + "modified", Limit: 50}); !errors.Is(err, community.ErrInvalidDraftFilter) {
		t.Fatalf("modified draft cursor was accepted: %v", err)
	}
	if _, err := repository.ListDrafts(ctx, ownerID, community.DraftListInput{Limit: 51}); !errors.Is(err, community.ErrInvalidDraftFilter) {
		t.Fatalf("oversized draft page was accepted: %v", err)
	}
}

func TestPostFeedStablePaginationAndVisibility(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	authorID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Feed Author','creator','active')`, authorID, authorID.String()+"@test.local", "feed_author_"+authorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	batch := &pgx.Batch{}
	for index := 0; index < 106; index++ {
		assetID, workID, postID := uuid.New(), uuid.New(), uuid.New()
		batch.Queue(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type) VALUES($1,$2,'image',$3,'/media/feed.jpg','image/jpeg','clean','demo')`, assetID, authorID, "Feed source")
		batch.Queue(`INSERT INTO works(id,author_id,asset_id,title,model_name,status,ai_disclosure,published_at) VALUES($1,$2,$3,$4,'Imported asset','published','Owner supplied source.',$5)`, workID, authorID, assetID, "Feed work", base)
		batch.Queue(`INSERT INTO posts(id,author_id,work_id,body,status,published_at) VALUES($1,$2,$3,'Visible feed post','published',$4)`, postID, authorID, workID, base)
	}
	for _, hidden := range []struct {
		assetStatus string
		workStatus  string
		postStatus  string
	}{
		{assetStatus: "review", workStatus: "published", postStatus: "published"},
		{assetStatus: "clean", workStatus: "hidden", postStatus: "published"},
		{assetStatus: "clean", workStatus: "published", postStatus: "hidden"},
	} {
		assetID, workID := uuid.New(), uuid.New()
		batch.Queue(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type) VALUES($1,$2,'image','Hidden source','/media/hidden.jpg','image/jpeg',$3,'demo')`, assetID, authorID, hidden.assetStatus)
		batch.Queue(`INSERT INTO works(id,author_id,asset_id,title,model_name,status,ai_disclosure,published_at) VALUES($1,$2,$3,'Hidden work','Imported asset',$4,'Owner supplied source.',$5)`, workID, authorID, assetID, hidden.workStatus, base.Add(time.Hour))
		batch.Queue(`INSERT INTO posts(author_id,work_id,body,status,published_at) VALUES($1,$2,'Hidden feed post',$3,$4)`, authorID, workID, hidden.postStatus, base.Add(time.Hour))
	}
	results := pool.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}

	repository := community.NewRepository(pool)
	seen := make(map[uuid.UUID]struct{})
	cursor := ""
	for {
		page, err := repository.ListPageForViewer(ctx, uuid.Nil, community.PostListInput{Cursor: cursor, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if !item.PublishedAt.Equal(base) || item.Body != "Visible feed post" {
				t.Fatalf("invisible feed record leaked: %#v", item)
			}
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("duplicate feed post %s", item.ID)
			}
			seen[item.ID] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 {
		t.Fatalf("expected 106 visible feed posts, got %d", len(seen))
	}
	if _, err := repository.ListPageForViewer(ctx, uuid.Nil, community.PostListInput{Cursor: cursor + "modified", Limit: 50}); !errors.Is(err, community.ErrInvalidPostFilter) {
		t.Fatalf("modified post cursor was accepted: %v", err)
	}
	if _, err := repository.ListPageForViewer(ctx, uuid.Nil, community.PostListInput{Limit: 51}); !errors.Is(err, community.ErrInvalidPostFilter) {
		t.Fatalf("oversized post page was accepted: %v", err)
	}
}
