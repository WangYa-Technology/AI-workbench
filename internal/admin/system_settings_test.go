package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
)

func TestSystemSettingRevisionsGateBusinessTransactions(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	actorID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Settings Admin','admin','active')`, actorID, actorID.String()+"@test.local", "settings_"+actorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	initial, err := service.GetSystemSettingPolicy(ctx)
	if err != nil || initial.Current.Version != 1 || !initial.Current.RegistrationsEnabled || !initial.Current.GenerationsEnabled || !initial.Current.PublishingEnabled || !initial.Current.MarketplaceCheckoutEnabled || !initial.Current.TaskCreationEnabled {
		t.Fatalf("initial settings mismatch: %#v %v", initial, err)
	}
	input := admin.SystemSettingUpdate{Name: "Bounded maintenance controls", RegistrationsEnabled: false, GenerationsEnabled: false, PublishingEnabled: false, MarketplaceCheckoutEnabled: false, TaskCreationEnabled: false, PublicNotice: "Selected write operations are paused in this Local Test environment.", Reason: "Verify every high-impact write boundary fails closed during bounded maintenance.", ExpectedVersion: 1, Confirmed: true}
	stale := input
	stale.ExpectedVersion = 2
	if _, err := service.UpdateSystemSettingPolicy(ctx, actorID, stale, "settings-stale"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("stale settings did not conflict: %v", err)
	}
	updated, err := service.UpdateSystemSettingPolicy(ctx, actorID, input, "settings-update")
	if err != nil || updated.Current.Version != 2 || len(updated.History) != 2 {
		t.Fatalf("settings update mismatch: %#v %v", updated, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE system_setting_revisions SET registrations_enabled=true WHERE id=$1`, updated.Current.ID); err == nil {
		t.Fatal("system setting revision was mutable")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, capability := range []string{systemsettings.Registrations, systemsettings.Generations, systemsettings.Publishing, systemsettings.Checkout, systemsettings.TaskCreation} {
		if err := systemsettings.RequireTx(ctx, tx, capability); !errors.Is(err, systemsettings.ErrDisabled) {
			t.Fatalf("capability %s remained enabled: %v", capability, err)
		}
	}
}
