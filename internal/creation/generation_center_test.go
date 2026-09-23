package creation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/creation"
)

func TestGenerationCenterFiltersStableCursorAndActions(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, outsiderID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Generation Center Owner','creator','active'),
		($4,$5,$6,'Generation Center Outsider','creator','active')`,
		ownerID, ownerID.String()+"@test.local", "center_"+ownerID.String()[:8],
		outsiderID, outsiderID.String()+"@test.local", "outside_"+outsiderID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	assetID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,family_id,version_number)
		VALUES($1,$2,'image','Reusable Generation output',$3,'image/jpeg','clean','delivery','creator-owned',$1,1)`,
		assetID, ownerID, "/api/v1/assets/"+assetID.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC)
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-4000-8000-000000000106"),
		uuid.MustParse("00000000-0000-4000-8000-000000000105"),
		uuid.MustParse("00000000-0000-4000-8000-000000000104"),
		uuid.MustParse("00000000-0000-4000-8000-000000000103"),
		uuid.MustParse("00000000-0000-4000-8000-000000000102"),
		uuid.MustParse("00000000-0000-4000-8000-000000000101"),
	}
	rows := []struct {
		mode, status, prompt string
		progress             int
		createdAt            time.Time
		outputAssetID        *uuid.UUID
	}{
		{"image", "succeeded", "Newest reusable image", 100, base.Add(5 * time.Minute), &assetID},
		{"image", "failed", "Failed image attempt", 0, base.Add(5 * time.Minute), nil},
		{"video", "succeeded", "Completed video", 100, base.Add(4 * time.Minute), nil},
		{"chat", "cancelled", "Cancelled chat", 0, base.Add(3 * time.Minute), nil},
		{"music", "running", "Running music", 45, base.Add(2 * time.Minute), nil},
		{"image", "queued", "Queued image", 0, base.Add(time.Minute), nil},
	}
	for index, row := range rows {
		if _, err := pool.Exec(ctx, `
			INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,charged_cost_cents,output_asset_id,created_at,updated_at)
			VALUES($1,$2,$3,'local_test','Generation Center Local',$4,$5,$6,5,CASE WHEN $5='succeeded' THEN 5 ELSE 0 END,$7,$8,$8)`,
			ids[index], ownerID, row.mode, row.prompt, row.status, row.progress, row.outputAssetID, row.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,created_at)
		VALUES($1,$2,'image','local_test','Generation Center Local','Foreign generation','queued',0,5,$3)`, uuid.New(), outsiderID, base.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	service := creation.NewService(pool, t.TempDir(), "", true)
	first, err := service.List(ctx, ownerID, creation.GenerationListInput{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first stable page: %#v %v", first, err)
	}
	if first.Items[0].ID != ids[0] || first.Items[1].ID != ids[1] {
		t.Fatalf("same-time UUID ordering is unstable: %#v", first.Items)
	}
	second, err := service.List(ctx, ownerID, creation.GenerationListInput{Limit: 2, Cursor: *first.NextCursor})
	if err != nil || len(second.Items) != 2 || second.Items[0].ID == first.Items[0].ID || second.Items[0].ID == first.Items[1].ID {
		t.Fatalf("second stable page: %#v %v", second, err)
	}

	filtered, err := service.List(ctx, ownerID, creation.GenerationListInput{
		Mode: "image", Status: "succeeded", DateFrom: timePointer(base.Add(4 * time.Minute)), DateTo: timePointer(base.Add(6 * time.Minute)),
	})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != ids[0] {
		t.Fatalf("combined generation filters: %#v %v", filtered, err)
	}
	completed := filtered.Items[0]
	if !completed.Actions.CanView || completed.Actions.CanCancel || completed.Actions.CanRetry || !completed.Actions.CanDownload || !completed.Actions.CanReuse || completed.Actions.DownloadPath == nil || completed.Actions.ReusePath == nil {
		t.Fatalf("completed action eligibility: %#v", completed.Actions)
	}
	failed, err := service.List(ctx, ownerID, creation.GenerationListInput{Status: "failed"})
	if err != nil || len(failed.Items) != 1 || !failed.Items[0].Actions.CanRetry || failed.Items[0].Actions.CanCancel || failed.Items[0].Actions.CanDownload {
		t.Fatalf("failed action eligibility: %#v %v", failed, err)
	}
	running, err := service.List(ctx, ownerID, creation.GenerationListInput{Status: "running"})
	if err != nil || len(running.Items) != 1 || !running.Items[0].Actions.CanCancel || running.Items[0].Actions.CanRetry {
		t.Fatalf("running action eligibility: %#v %v", running, err)
	}
	all, err := service.List(ctx, ownerID, creation.GenerationListInput{Limit: 50})
	if err != nil || len(all.Items) != len(rows) {
		t.Fatalf("owner isolation failed: count=%d err=%v", len(all.Items), err)
	}

	invalid := []creation.GenerationListInput{
		{Mode: "unknown"}, {Status: "unknown"}, {Limit: 51}, {Cursor: "modified"},
		{DateFrom: timePointer(base.Add(time.Hour)), DateTo: timePointer(base)},
	}
	for _, input := range invalid {
		if _, err := service.List(ctx, ownerID, input); !errors.Is(err, creation.ErrInvalid) {
			t.Fatalf("invalid Generation Center input accepted: %#v err=%v", input, err)
		}
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}
