package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/creation"
)

func TestModelRouteRevisionsControlSubsequentGenerations(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	actorID, ownerID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Model Admin','admin','active'),($4,$5,$6,'Route Creator','creator','active')`, actorID, actorID.String()+"@test.local", "model_admin_"+actorID.String()[:8], ownerID, ownerID.String()+"@test.local", "route_creator_"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	initial, err := service.GetModelRoutePolicy(ctx)
	if err != nil || len(initial.Routes) != 4 || initial.Routes["image"].Version != 1 || initial.Routes["image"].ProviderProfileID != "local-image-v1" {
		t.Fatalf("initial routes mismatch: %#v %v", initial, err)
	}
	input := admin.ModelRouteUpdate{ProviderProfileID: "local-image-v1", Name: "Verified image route", TimeoutSeconds: 90, MaxAttempts: 2, ExpectedVersion: 1}
	stale := input
	stale.ExpectedVersion = 2
	if _, err := service.UpdateModelRoute(ctx, actorID, "image", stale, "model-stale"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("stale route did not conflict: %v", err)
	}
	crossMode := input
	crossMode.ProviderProfileID = "local-chat-v1"
	if _, err := service.UpdateModelRoute(ctx, actorID, "image", crossMode, "model-mode"); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("cross-mode route accepted: %v", err)
	}
	external := input
	external.ProviderProfileID = "openai-image"
	if _, err := service.UpdateModelRoute(ctx, actorID, "image", external, "model-external"); !errors.Is(err, admin.ErrProviderConfig) {
		t.Fatalf("external route did not fail closed: %v", err)
	}
	updated, err := service.UpdateModelRoute(ctx, actorID, "image", input, "model-update")
	if err != nil || updated.Routes["image"].Version != 2 || len(updated.History["image"]) != 2 {
		t.Fatalf("route update mismatch: %#v %v", updated, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE model_route_revisions SET max_attempts=5 WHERE id=$1`, updated.Routes["image"].ID); err == nil {
		t.Fatal("model route revision was mutable")
	}

	generation, err := creation.NewService(pool, t.TempDir(), "", true).SubmitCommand(ctx, ownerID, creation.SubmitInput{Mode: "image", Prompt: "Versioned model route evidence"}, "model-route-generation", "model-route-request")
	if err != nil {
		t.Fatal(err)
	}
	var routeID uuid.UUID
	var routeVersion int
	if err := pool.QueryRow(ctx, `SELECT model_route_revision_id,model_route_version FROM generations WHERE id=$1`, generation.ID).Scan(&routeID, &routeVersion); err != nil {
		t.Fatal(err)
	}
	if routeID != updated.Routes["image"].ID || routeVersion != 2 {
		t.Fatalf("generation route evidence mismatch: %s v%d", routeID, routeVersion)
	}
}
