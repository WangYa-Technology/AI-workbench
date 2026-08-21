package admin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
)

func TestAdminContentAndMediaInventoriesTraverseBeyondLegacyWindow(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	administratorID, creatorID, sourceAssetID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status,created_at) VALUES
		($1,$2,$3,'Backlog Administrator','admin','active',now() - interval '4 hours'),
		($4,$5,$6,'Backlog Creator','creator','active',now() - interval '4 hours')`,
		administratorID, administratorID.String()+"@test.local", "backlog_admin_"+administratorID.String()[:8],
		creatorID, creatorID.String()+"@test.local", "backlog_creator_"+creatorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,created_at)
		VALUES($1,$2,'image','Backlog source','/media/backlog-source.jpg','image/jpeg','clean','demo','demo',now() - interval '4 hours')`, sourceAssetID, creatorID); err != nil {
		t.Fatal(err)
	}

	oldWorkID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at,created_at,updated_at)
		VALUES($1,$2,$3,'Content backlog target','Older moderation evidence','Local Test','published','Content backlog disclosure',now() - interval '3 hours',now() - interval '3 hours',now() - interval '3 hours')`, oldWorkID, creatorID, sourceAssetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at,created_at,updated_at)
		SELECT $1,$2,'Content backlog pressure '||value,'Newer moderation evidence','Local Test','published','Content backlog disclosure',now() - interval '1 hour',now() - interval '1 hour',now() - interval '1 hour'
		FROM generate_series(1,205) value`, creatorID, sourceAssetID); err != nil {
		t.Fatal(err)
	}

	service := admin.NewService(pool, true)
	contentInput := admin.ContentListInput{Query: "content backlog", ResourceType: "work", Status: "published", Limit: 50}
	seenContent := map[uuid.UUID]bool{}
	foundWork := false
	for {
		page, err := service.ListContent(ctx, contentInput)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seenContent[item.ID] {
				t.Fatalf("content repeated across cursor pages: %s", item.ID)
			}
			seenContent[item.ID] = true
			foundWork = foundWork || item.ID == oldWorkID
		}
		if page.NextCursor == nil {
			break
		}
		contentInput.Cursor = *page.NextCursor
	}
	if len(seenContent) != 206 || !foundWork {
		t.Fatalf("incomplete content traversal: count=%d target=%t", len(seenContent), foundWork)
	}
	if _, err := service.ListContent(ctx, admin.ContentListInput{Cursor: "modified", Limit: 20}); !errors.Is(err, admin.ErrInvalidContentFilter) {
		t.Fatalf("modified content cursor was accepted: %v", err)
	}
	moderated, err := service.UpdateContent(ctx, administratorID, oldWorkID, admin.ContentUpdate{
		Status: "hidden"}, "content-backlog-review")
	if err != nil || moderated.ID != oldWorkID || moderated.Status != "hidden" {
		t.Fatalf("exact older content moderation response failed: %#v %v", moderated, err)
	}

	oldMediaID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,uploaded_filename,size_bytes,storage_backend,storage_key,created_at)
		VALUES($1,$2,'image','Media backlog target','/media/backlog-target.jpg','image/jpeg','review','upload','personal','media-backlog-target.jpg',128,'local_file',$3,now() - interval '3 hours')`, oldMediaID, creatorID, oldMediaID.String()+".jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,uploaded_filename,size_bytes,storage_backend,storage_key,created_at)
		SELECT $1,'image','Media backlog pressure '||value,'/media/backlog-pressure-'||value||'.jpg','image/jpeg','review','upload','personal','media-backlog-pressure-'||value||'.jpg',128,'local_file','media-backlog-pressure-'||value||'.jpg',now() - interval '1 hour'
		FROM generate_series(1,205) value`, creatorID); err != nil {
		t.Fatal(err)
	}

	mediaInput := admin.MediaListInput{Query: "media backlog", Kind: "image", Status: "review", Limit: 50}
	seenMedia := map[uuid.UUID]bool{}
	foundMedia := false
	for {
		page, err := service.ListMedia(ctx, mediaInput)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seenMedia[item.ID] {
				t.Fatalf("media repeated across cursor pages: %s", item.ID)
			}
			seenMedia[item.ID] = true
			foundMedia = foundMedia || item.ID == oldMediaID
		}
		if page.NextCursor == nil {
			break
		}
		mediaInput.Cursor = *page.NextCursor
	}
	if len(seenMedia) != 206 || !foundMedia {
		t.Fatalf("incomplete media traversal: count=%d target=%t", len(seenMedia), foundMedia)
	}
	if _, err := service.ListMedia(ctx, admin.MediaListInput{Cursor: "modified", Limit: 20}); !errors.Is(err, admin.ErrInvalidMediaFilter) {
		t.Fatalf("modified media cursor was accepted: %v", err)
	}
	reviewed, err := service.ReviewMedia(ctx, administratorID, oldMediaID, admin.MediaReview{
		Status: "rejected"}, "media-backlog-review")
	if err != nil || reviewed.ID != oldMediaID || reviewed.ScanStatus != "rejected" || reviewed.ScannedAt == nil {
		t.Fatalf("exact older media review response failed: %#v %v", reviewed, err)
	}

	var contentUpdatedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM works WHERE id=$1`, oldWorkID).Scan(&contentUpdatedAt); err != nil || !contentUpdatedAt.After(time.Now().Add(-time.Minute)) {
		t.Fatalf("older content operation was not persisted: %v %v", contentUpdatedAt, err)
	}
}
