package tasks_test

import (
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
	"github.com/hcai-chat/hcai-chat/internal/tasktypes"
)

func TestTaskDeliveryKindContractSurvivesCategoryChanges(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := t.Context()
	client, creator, other, image := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, client, creator, other, image)
	categories := tasktypes.NewService(pool)
	category, err := categories.Create(ctx, tasktypes.Type{Scope: "task", Code: "brand_image", NameZh: "品牌图片", NameEn: "Brand image", Icon: "image", SortOrder: 100})
	if err != nil || category.DeliveryKind != "image" {
		t.Fatal(category, err)
	}
	input := validTaskInput()
	input.DeliverableType = category.Code
	input.AllowDirectAccept = true
	service := tasks.NewService(pool)
	brief, err := service.Create(ctx, client, input, "kind-create-original")
	if err != nil || brief.DeliveryKind != "image" {
		t.Fatal(brief.DeliveryKind, err)
	}
	if _, err = service.Claim(ctx, creator, brief.ID, "kind-claim-original"); err != nil {
		t.Fatal(err)
	}

	// A legacy caller changing only the icon must preserve the media contract.
	category.Icon = "audio"
	category.DeliveryKind = ""
	category, err = categories.Update(ctx, category.Code, category)
	if err != nil || category.DeliveryKind != "image" {
		t.Fatal(category, err)
	}
	category.DeliveryKind = "video"
	category, err = categories.Update(ctx, category.Code, category)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := service.Create(ctx, client, input, "kind-create-new-video")
	if err != nil || newer.DeliveryKind != "video" {
		t.Fatal(newer.DeliveryKind, err)
	}
	if _, err = service.Claim(ctx, creator, newer.ID, "kind-claim-new-video"); err != nil {
		t.Fatal(err)
	}
	delivery := tasks.DeliverInput{AssetID: image, Note: "Original image ready.", RightsEvidence: "All campaign source rights are held by the creator.", AIDisclosure: "Original image with documented sources.", RightsConfirmed: true}
	if _, err = service.Deliver(ctx, creator, newer.ID, delivery, "kind-reject-wrong-media"); !errors.Is(err, tasks.ErrInvalid) {
		t.Fatalf("wrong kind accepted: %v", err)
	}
	if err = categories.Delete(ctx, category.Code, "mixed", "task"); err != nil {
		t.Fatal(err)
	}
	delivered, err := service.Deliver(ctx, creator, brief.ID, delivery, "kind-deliver-old-image")
	if err != nil || delivered.Status != "submitted" || delivered.DeliveryKind != "image" || delivered.DeliverableType != "mixed" {
		t.Fatal(delivered, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE demands SET delivery_kind='video' WHERE id=$1`, brief.ID); err == nil {
		t.Fatal("published media contract mutated")
	}
	category.Code = "invalid_contract"
	category.DeliveryKind = "unsupported"
	if _, err = categories.Create(ctx, category); !errors.Is(err, tasktypes.ErrInvalid) {
		t.Fatalf("invalid contract accepted: %v", err)
	}
}

func TestTaskDeliveryKindMigrationBackfillAndRoundtrip(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := t.Context()
	client, creator, other, asset := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, client, creator, other, asset)
	cat, err := tasktypes.NewService(pool).Create(ctx, tasktypes.Type{Scope: "task", Code: "legacy_brand", NameZh: "旧分类", NameEn: "Legacy brand", Icon: "image"})
	if err != nil {
		t.Fatal(err)
	}
	input := validTaskInput()
	input.DeliverableType = cat.Code
	brief, err := tasks.NewService(pool).Create(ctx, client, input, "migration-custom-brief")
	if err != nil {
		t.Fatal(err)
	}
	downSQL, err := os.ReadFile("../platform/database/migrations/0180_task_delivery_kind.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE task_types SET delivery_kind='video' WHERE code=$1`, cat.Code); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(downSQL)); err == nil {
		t.Fatal("downgrade ignored a changed task delivery contract")
	}
	if _, err = pool.Exec(ctx, `UPDATE task_types SET delivery_kind='image' WHERE code=$1`, cat.Code); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"down", "up"} {
		sql := downSQL
		var e error
		if direction == "up" {
			sql, e = os.ReadFile("../platform/database/migrations/0180_task_delivery_kind.up.sql")
		}
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(sql)); e != nil {
			t.Fatalf("%s: %v", direction, e)
		}
	}
	restored, err := tasks.NewService(pool).Get(ctx, client, brief.ID)
	if err != nil || restored.DeliveryKind != "image" {
		t.Fatal(restored.DeliveryKind, err)
	}
	var invalid int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM task_types WHERE delivery_kind IS NULL OR delivery_kind=''`).Scan(&invalid); err != nil || invalid != 0 {
		t.Fatal(invalid, err)
	}
}
