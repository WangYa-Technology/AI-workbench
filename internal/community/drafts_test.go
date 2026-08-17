package community_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/community"
)

func TestPersistedContentDraftLifecycle(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, otherID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Draft Owner','creator','active'),($4,$5,$6,'Draft Other','creator','active')`,
		ownerID, ownerID.String()+"@test.local", "draft_"+ownerID.String()[:8],
		otherID, otherID.String()+"@test.local", "other_"+otherID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	cleanAssetID, pendingAssetID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code) VALUES
		($1,$2,'image','Draft source','/media/draft-source.jpg','image/jpeg','clean','upload','personal'),
		($3,$2,'image','Pending source','/media/pending-source.jpg','image/jpeg','pending','upload','personal')`,
		cleanAssetID, ownerID, pendingAssetID); err != nil {
		t.Fatal(err)
	}
	repository := community.NewRepository(pool)
	draft, err := repository.SaveDraft(ctx, ownerID, nil, community.DraftInput{PublishInput: community.PublishInput{
		AssetID: cleanAssetID, PromptVisibility: "private",
	}}, "draft-create")
	if err != nil || draft.Version != 1 || draft.AssetID != cleanAssetID || draft.Title != "" {
		t.Fatalf("create incomplete private draft: %#v %v", draft, err)
	}
	if _, err := repository.SaveDraft(ctx, ownerID, nil, community.DraftInput{PublishInput: community.PublishInput{AssetID: cleanAssetID, PromptVisibility: "public"}}, "draft-duplicate"); !errors.Is(err, community.ErrConflict) {
		t.Fatalf("duplicate active Asset draft was accepted: %v", err)
	}
	if _, err := repository.GetDraft(ctx, otherID, draft.ID); !errors.Is(err, community.ErrNotFound) {
		t.Fatalf("another account read the draft: %v", err)
	}
	updated, err := repository.SaveDraft(ctx, ownerID, &draft.ID, community.DraftInput{
		PublishInput: community.PublishInput{
			AssetID: cleanAssetID, Title: "Persisted studio draft", Summary: "A private draft saved before publication.",
			Prompt: "A precise editorial image prompt", PromptVisibility: "partial",
			AIDisclosure: "Created with the deterministic Local Test image Provider.", Body: "Prepared privately, then published with durable evidence.",
		}, ExpectedVersion: draft.Version,
	}, "draft-update")
	if err != nil || updated.Version != 2 || updated.Title != "Persisted studio draft" {
		t.Fatalf("update draft: %#v %v", updated, err)
	}
	if _, err := repository.SaveDraft(ctx, ownerID, &draft.ID, community.DraftInput{PublishInput: updatedToInput(updated), ExpectedVersion: 1}, "draft-stale"); !errors.Is(err, community.ErrConflict) {
		t.Fatalf("stale draft update was accepted: %v", err)
	}
	publication, err := repository.PublishDraft(ctx, ownerID, draft.ID, updated.Version, "draft-publish")
	if err != nil || publication.WorkID != draft.ID || publication.PostID != draft.PostID {
		t.Fatalf("publish persisted draft: %#v %v", publication, err)
	}
	if _, err := repository.GetDraft(ctx, ownerID, draft.ID); !errors.Is(err, community.ErrNotFound) {
		t.Fatalf("published draft remained editable: %v", err)
	}
	page, err := repository.ListDrafts(ctx, ownerID, community.DraftListInput{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("published draft remained in private inventory: %#v %v", page, err)
	}

	pending, err := repository.SaveDraft(ctx, ownerID, nil, community.DraftInput{PublishInput: community.PublishInput{
		AssetID: pendingAssetID, Title: "Pending media draft", AIDisclosure: "Created with a local Provider pending media review.", PromptVisibility: "public",
	}}, "pending-create")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PublishDraft(ctx, ownerID, pending.ID, pending.Version, "pending-publish"); !errors.Is(err, community.ErrForbidden) {
		t.Fatalf("draft with pending media was published: %v", err)
	}
	if err := repository.DiscardDraft(ctx, ownerID, pending.ID, pending.Version, "pending-discard"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetDraft(ctx, ownerID, pending.ID); !errors.Is(err, community.ErrNotFound) {
		t.Fatalf("discarded draft remained editable: %v", err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_id IN ($1,$2) AND action LIKE 'content.draft_%'`, draft.ID, pending.ID).Scan(&auditCount); err != nil || auditCount != 5 {
		t.Fatalf("content draft audit evidence mismatch: count=%d err=%v", auditCount, err)
	}
}

func updatedToInput(item community.Draft) community.PublishInput {
	return community.PublishInput{
		AssetID: item.AssetID, Title: item.Title, Summary: item.Summary, Prompt: item.Prompt,
		PromptVisibility: item.PromptVisibility, AIDisclosure: item.AIDisclosure, Body: item.Body,
	}
}
