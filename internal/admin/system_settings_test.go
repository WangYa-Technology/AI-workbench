package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
)

func TestSystemSettingsUpdateCurrentGatesAndAudit(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	actorID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Settings Admin','admin','active')`, actorID, actorID.String()+"@test.local", "settings_"+actorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	initial, err := service.GetSystemSettings(ctx)
	if err != nil || !initial.RegistrationsEnabled || !initial.GenerationsEnabled || !initial.PublishingEnabled || !initial.MarketplaceCheckoutEnabled || !initial.TaskCreationEnabled {
		t.Fatalf("initial settings mismatch: %#v %v", initial, err)
	}
	input := admin.SystemSettingUpdate{RegistrationsEnabled: false, GenerationsEnabled: false, PublishingEnabled: false, MarketplaceCheckoutEnabled: false, TaskCreationEnabled: false, PublicNotice: "Selected write operations are paused in this Local Test environment."}
	updated, err := service.UpdateSystemSettings(ctx, actorID, input, "settings-update")
	if err != nil || updated.RegistrationsEnabled || updated.GenerationsEnabled || updated.PublishingEnabled || updated.MarketplaceCheckoutEnabled || updated.TaskCreationEnabled || updated.UpdatedBy == nil || *updated.UpdatedBy != actorID {
		t.Fatalf("settings update mismatch: %#v %v", updated, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='admin.system_settings_updated' AND actor_id=$1`, actorID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("settings audit mismatch: count=%d err=%v", auditCount, err)
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
