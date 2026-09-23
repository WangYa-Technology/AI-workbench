package admin_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
)

func TestWalletTopupSettingsAuthorityAndRevision(t *testing.T) {
	pool, actor, _ := financeSettingsFixture(t)
	s := admin.NewService(pool, true)
	ctx := t.Context()
	input := admin.WalletTopupSettingsUpdate{ExpectedVersion: 1, MinimumAmountCents: 1000, PresetAmountsCents: []int{2000, 1000}}
	for name, invalid := range map[string]admin.WalletTopupSettingsUpdate{
		"minimum":       {ExpectedVersion: 1, MinimumAmountCents: 49, PresetAmountsCents: []int{}},
		"version":       {MinimumAmountCents: 50, PresetAmountsCents: []int{}},
		"duplicate":     {ExpectedVersion: 1, MinimumAmountCents: 50, PresetAmountsCents: []int{1000, 1000}},
		"below minimum": {ExpectedVersion: 1, MinimumAmountCents: 1000, PresetAmountsCents: []int{500}},
		"null":          {ExpectedVersion: 1, MinimumAmountCents: 50},
		"maximum":       {ExpectedVersion: 1, MinimumAmountCents: 100000000, PresetAmountsCents: []int{}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.UpdateWalletTopupSettings(ctx, actor, invalid, "invalid"); !errors.Is(err, admin.ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateWalletTopupSettings(ctx, actor, input, "denied"); !errors.Is(err, admin.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	saved, err := s.UpdateWalletTopupSettings(ctx, actor, input, "saved")
	if err != nil || saved.Version != 2 || saved.MinimumAmountCents != 1000 || !reflect.DeepEqual(saved.PresetAmountsCents, []int{1000, 2000}) || !reflect.DeepEqual(input.PresetAmountsCents, []int{2000, 1000}) {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, minimum := range []int{500, 1000} {
		wg.Add(1)
		go func(minimum int) {
			defer wg.Done()
			_, err := s.UpdateWalletTopupSettings(ctx, actor, admin.WalletTopupSettingsUpdate{ExpectedVersion: 2, MinimumAmountCents: minimum, PresetAmountsCents: []int{}}, "concurrent")
			results <- err
		}(minimum)
	}
	wg.Wait()
	close(results)
	passed, conflicts := 0, 0
	for err := range results {
		if err == nil {
			passed++
		} else if errors.Is(err, admin.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if passed != 1 || conflicts != 1 {
		t.Fatalf("passed=%d conflicts=%d", passed, conflicts)
	}
	current, err := billing.ReadWalletTopupSettings(ctx, pool, false)
	if err != nil || current.Version != 3 || len(current.PresetAmountsCents) != 0 {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='admin.wallet_topup_settings_updated' AND metadata->'after'->>'version' IN ('2','3')`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_topup_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='admin.wallet_topup_settings_updated' THEN RAISE EXCEPTION 'fixture audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_topup_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_topup_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateWalletTopupSettings(ctx, actor, admin.WalletTopupSettingsUpdate{ExpectedVersion: 3, MinimumAmountCents: 1500, PresetAmountsCents: []int{}}, "audit-failure"); err == nil {
		t.Fatal("audit failure accepted")
	}
	after, err := billing.ReadWalletTopupSettings(ctx, pool, false)
	if err != nil || !reflect.DeepEqual(current, after) {
		t.Fatalf("failed audit changed settings: %+v %v", after, err)
	}
}

func TestWalletTopupSettingsRevocationDuringSave(t *testing.T) {
	pool, actor, _ := financeSettingsFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	traced, entered, release := testutil.GateQuery(t, pool, "SELECT version FROM wallet_topup_settings")
	s := admin.NewService(traced, true)
	var wg sync.WaitGroup
	t.Cleanup(func() { release(); wg.Wait() })
	done := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := s.UpdateWalletTopupSettings(ctx, actor, admin.WalletTopupSettingsUpdate{ExpectedVersion: 1, MinimumAmountCents: 1000, PresetAmountsCents: []int{1000}}, "revoked")
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("did not reach settings lock: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; !errors.Is(err, admin.ErrForbidden) {
		t.Fatal(err)
	}
	current, err := billing.ReadWalletTopupSettings(t.Context(), pool, false)
	if err != nil || current.Version != 1 || current.MinimumAmountCents != 50 {
		t.Fatalf("revocation changed settings: %+v %v", current, err)
	}
	var audits int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='admin.wallet_topup_settings_updated'`).Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
}

func TestWalletTopupSettingsMigrationPreservesConfiguredRules(t *testing.T) {
	pool, actor, _ := financeSettingsFixture(t)
	up, err := os.ReadFile("../platform/database/migrations/0136_wallet_topup_settings.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0136_wallet_topup_settings.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), string(up)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := admin.NewService(pool, true)
	if _, err := s.UpdateWalletTopupSettings(t.Context(), actor, admin.WalletTopupSettingsUpdate{ExpectedVersion: 1, MinimumAmountCents: 1000, PresetAmountsCents: []int{1000}}, "migration"); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), string(down)); err == nil {
		t.Fatal("discarded configured settings")
	}
	_ = tx.Rollback(t.Context())
	if current, err := billing.ReadWalletTopupSettings(t.Context(), pool, false); err != nil || current.Version != 2 {
		t.Fatalf("downgrade changed settings: %+v %v", current, err)
	}
}
