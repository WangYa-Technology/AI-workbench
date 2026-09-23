package admin_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func financeSettingsFixture(t *testing.T) (*pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool, cleanup := testPool(t)
	t.Cleanup(cleanup)
	actor, user := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, user} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Finance settings','admin')`, id, id.String()+"@test.local", "settings_"+id.String()[:8]); err != nil {
			t.Fatal(err)
		}
	}
	return pool, actor, user
}

func financeSettingsSnapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var value string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'adjustments',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.operation_id) FROM admin_finance_adjustments c),
 'configs',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.provider) FROM payment_provider_configs c),
 'destinations',(SELECT jsonb_agg(to_jsonb(d) ORDER BY d.id) FROM payment_destinations d),
 'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.user_id) FROM billing_accounts a),
 'entries',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM billing_entries e),
 'audit',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM audit_events e))::text`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestFinanceSettingsRejectRevokedAuthority(t *testing.T) {
	for _, action := range []string{"adjust", "destination_create", "destination_update", "provider"} {
		for _, change := range []string{"role", "suspended", "permission"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				pool, actor, user := financeSettingsFixture(t)
				version := 0
				query := "SELECT balance_cents,reserved_cents FROM billing_accounts"
				if action == "destination_create" || action == "destination_update" {
					query = "SELECT id,version,destination_id,original_merchant_id FROM payment_destinations"
				}
				if action == "destination_update" {
					version = 1
					if _, err := pool.Exec(t.Context(), `INSERT INTO payment_destinations(provider,user_id,destination_id,account_type,status,charges_enabled,payouts_enabled,details_submitted,requirements_due,admin_disabled,verified_at) VALUES('stripe',$1,'acct_existing','manual','verified',true,true,true,false,false,now())`, user); err != nil {
						t.Fatal(err)
					}
				}
				if action == "provider" {
					query = "SELECT id,enabled,environment,merchant_id,store_id"
				}
				before := financeSettingsSnapshot(t, pool)
				traced, entered, release := testutil.GateQuery(t, pool, query)
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				var workers sync.WaitGroup
				t.Cleanup(func() { release(); cancel(); workers.Wait() })
				run := func(p *pgxpool.Pool) error {
					s := admin.NewService(p, true)
					switch action {
					case "adjust":
						_, err := s.AdjustFinance(ctx, actor, user, admin.FinanceAdjustment{DeltaCents: 500, Currency: "USD"}, "settings-adjust-key", "settings-adjust")
						return err
					case "provider":
						_, err := s.UpdatePaymentProviderConfig(ctx, actor, "stripe", admin.PaymentProviderConfigUpdate{MerchantID: stringPtr("acct_changed")}, "settings-provider")
						return err
					default:
						_, err := s.UpdatePaymentDestination(ctx, actor, user, admin.PaymentDestinationUpdate{DestinationID: "acct_changed", Enabled: true, ExpectedVersion: version}, "settings-destination")
						return err
					}
				}
				done := make(chan error, 1)
				workers.Add(1)
				go func() { defer workers.Done(); done <- run(traced) }()
				select {
				case <-entered:
				case err := <-done:
					t.Fatal("command did not reach resource", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				var err error
				switch change {
				case "role":
					_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
				case "suspended":
					_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor)
				case "permission":
					_, err = pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:finance'`)
				}
				if err != nil {
					t.Fatal(err)
				}
				release()
				if err := <-done; !errors.Is(err, admin.ErrForbidden) && !errors.Is(err, admin.ErrConflict) {
					t.Errorf("revoked command accepted: %v", err)
				}
				if after := financeSettingsSnapshot(t, pool); before != after {
					t.Error("revoked command changed finance state")
				}
				if err := run(pool); !errors.Is(err, admin.ErrForbidden) {
					t.Errorf("fresh revoked command not forbidden: %v", err)
				}
			})
		}
	}
}

func TestPaymentProviderConcurrentUpdatesPreserveFieldsAndOneActive(t *testing.T) {
	for _, scenario := range []string{"existing", "new", "switch"} {
		t.Run(scenario, func(t *testing.T) {
			pool, actor, _ := financeSettingsFixture(t)
			if scenario != "new" {
				if _, err := pool.Exec(t.Context(), `INSERT INTO payment_provider_configs(provider,environment,merchant_id,store_id) VALUES('stripe','test','acct_original','store_original'),('waffo_pancake','test','merchant_original','store_original')`); err != nil {
					t.Fatal(err)
				}
			}
			traced, entered, release := testutil.GateQuery(t, pool, "INSERT INTO payment_provider_configs(id,provider,enabled")
			config := pool.Config()
			app := "finance-config-" + uuid.NewString()
			config.ConnConfig.RuntimeParams["application_name"] = app
			// Updates must still read the latest row after waiting when the pool
			// default is stronger than Read Committed.
			config.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
			secondPool, err := pgxpool.NewWithConfig(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(secondPool.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			var workers sync.WaitGroup
			t.Cleanup(func() { release(); cancel(); workers.Wait() })
			first, second := make(chan error, 1), make(chan error, 1)
			firstInput := admin.PaymentProviderConfigUpdate{MerchantID: stringPtr("acct_changed")}
			secondInput := admin.PaymentProviderConfigUpdate{StoreID: stringPtr("store_changed")}
			secondProvider := "stripe"
			if scenario == "switch" {
				firstInput.Enabled = boolPtr(true)
				secondInput.Enabled = boolPtr(true)
				secondProvider = "waffo_pancake"
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				_, err := admin.NewService(traced, true).UpdatePaymentProviderConfig(ctx, actor, "stripe", firstInput, "config-first")
				first <- err
			}()
			select {
			case <-entered:
			case err := <-first:
				t.Fatal("first update did not reach write", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				_, err := admin.NewService(secondPool, true).UpdatePaymentProviderConfig(ctx, actor, secondProvider, secondInput, "config-second")
				second <- err
			}()
			var secondFinished bool
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND cardinality(pg_blocking_pids(pid))>0)`, app).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-second:
					if err != nil {
						t.Fatal(err)
					}
					secondFinished = true
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if secondFinished {
					break
				}
			}
			release()
			if err := <-first; err != nil {
				t.Fatal(err)
			}
			if !secondFinished {
				if err := <-second; err != nil {
					t.Fatal(err)
				}
			}
			var merchant, store string
			var active, badAudit int
			if err := pool.QueryRow(ctx, `SELECT merchant_id,store_id,(SELECT count(*) FROM payment_provider_configs WHERE enabled),(SELECT count(*) FROM audit_events a WHERE action='admin.payment_provider_config_updated' AND NOT EXISTS(SELECT 1 FROM payment_provider_configs c WHERE c.id=a.resource_id)) FROM payment_provider_configs WHERE provider='stripe'`).Scan(&merchant, &store, &active, &badAudit); err != nil {
				t.Fatal(err)
			}
			if scenario == "switch" {
				if active != 1 {
					t.Errorf("multiple providers enabled: %d", active)
				}
			} else if merchant != "acct_changed" || store != "store_changed" {
				t.Errorf("concurrent partial fields lost: %s %s", merchant, store)
			}
			if badAudit != 0 {
				t.Errorf("audit points to nonexistent config: %d", badAudit)
			}
		})
	}
}

// Return the command and audit action for each independently mutating entry.
func financeSettingsCommand(t *testing.T, pool *pgxpool.Pool, actor, user uuid.UUID, action string) (func(context.Context, *pgxpool.Pool) error, string) {
	t.Helper()
	version := 0
	audit := "admin.payment_destination_updated"
	if action == "destination_update" {
		version = 1
		if _, err := pool.Exec(t.Context(), `INSERT INTO payment_destinations(provider,user_id,destination_id,account_type,status,charges_enabled,payouts_enabled,details_submitted,requirements_due,admin_disabled,verified_at) VALUES('stripe',$1,'acct_existing','manual','verified',true,true,true,false,false,now())`, user); err != nil {
			t.Fatal(err)
		}
	}
	if action == "adjust" {
		audit = "admin.finance_adjusted"
	} else if action == "provider" {
		audit = "admin.payment_provider_config_updated"
		// Include the implicit disabling of the previously active provider in
		// both rollback and successful-switch assertions.
		if _, err := pool.Exec(t.Context(), `INSERT INTO payment_provider_configs(provider,enabled,environment) VALUES('waffo_pancake',true,'test')`); err != nil {
			t.Fatal(err)
		}
	}
	return func(ctx context.Context, p *pgxpool.Pool) error {
		s := admin.NewService(p, true)
		switch action {
		case "adjust":
			_, err := s.AdjustFinance(ctx, actor, user, admin.FinanceAdjustment{DeltaCents: 500, Currency: "USD"}, "finance-settings-key", "finance-settings-audit")
			return err
		case "provider":
			_, err := s.UpdatePaymentProviderConfig(ctx, actor, "stripe", admin.PaymentProviderConfigUpdate{Enabled: boolPtr(true), MerchantID: stringPtr("acct_changed")}, "finance-settings-audit")
			return err
		default:
			_, err := s.UpdatePaymentDestination(ctx, actor, user, admin.PaymentDestinationUpdate{DestinationID: "acct_changed", Enabled: true, ExpectedVersion: version}, "finance-settings-audit")
			return err
		}
	}, audit
}

func TestFinanceSettingsAuditAtomicity(t *testing.T) {
	for _, action := range []string{"adjust", "destination_create", "destination_update", "provider"} {
		t.Run(action, func(t *testing.T) {
			pool, actor, user := financeSettingsFixture(t)
			run, audit := financeSettingsCommand(t, pool, actor, user, action)
			before := financeSettingsSnapshot(t, pool)
			if _, err := pool.Exec(t.Context(), `ALTER TABLE audit_events ADD CONSTRAINT reject_finance_settings_audit CHECK (action NOT IN ('admin.finance_adjusted','admin.payment_destination_updated','admin.payment_provider_config_updated')) NOT VALID`); err != nil {
				t.Fatal(err)
			}
			if err := run(t.Context(), pool); err == nil {
				t.Fatal("finance mutation accepted without audit")
			}
			if financeSettingsSnapshot(t, pool) != before {
				t.Fatal("audit failure did not roll back complete finance state")
			}
			if _, err := pool.Exec(t.Context(), `ALTER TABLE audit_events DROP CONSTRAINT reject_finance_settings_audit`); err != nil {
				t.Fatal(err)
			}
			if err := run(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			var valid int
			query := `SELECT count(*) FROM audit_events a WHERE a.actor_id=$1 AND a.action=$2 AND a.request_id='finance-settings-audit' AND `
			switch action {
			case "adjust":
				query += `a.resource_type='user' AND a.resource_id=$3 AND a.metadata->>'deltaCents'='500' AND a.metadata->>'currency'='USD'
 AND EXISTS(SELECT 1 FROM billing_entries e JOIN billing_accounts b ON b.user_id=e.user_id AND b.currency=e.currency
 WHERE e.user_id=$3 AND e.operation_id::text=a.metadata->>'operationId' AND e.amount_cents=500 AND e.direction='credit'
 AND e.balance_after_cents=b.balance_cents AND e.metadata->>'source'='admin_adjustment' AND e.metadata->>'actorId'=$1::text
 AND e.metadata->>'requestId'=a.request_id AND NOT e.metadata ? 'paymentMode')`
			case "provider":
				query += `a.resource_type='payment_provider_config' AND a.metadata->>'merchantId'='acct_changed'
 AND EXISTS(SELECT 1 FROM payment_provider_configs c WHERE c.id=a.resource_id AND c.provider='stripe' AND c.enabled)
 AND NOT EXISTS(SELECT 1 FROM payment_provider_configs c WHERE c.provider<>'stripe' AND c.enabled) AND $3::uuid IS NOT NULL`
			default:
				previous := ""
				if action == "destination_update" {
					previous = "acct_existing"
				}
				query += `a.resource_type='payment_destination' AND a.metadata->>'userId'=$3::text AND a.metadata->>'destinationId'='acct_changed'
 AND a.metadata->>'enabled'='true' AND a.metadata->>'previousDestinationId'='` + previous + `'
 AND EXISTS(SELECT 1 FROM payment_destinations d WHERE d.id=a.resource_id AND d.user_id=$3::uuid AND d.destination_id='acct_changed')`
			}
			if err := pool.QueryRow(t.Context(), query, actor, audit, user).Scan(&valid); err != nil || valid != 1 {
				t.Fatalf("missing atomic attributable finance audit: %d %v", valid, err)
			}
		})
	}
}

func TestFinanceSettingsPinsAuthorityThroughCommit(t *testing.T) {
	for _, action := range []string{"adjust", "destination_create", "destination_update", "provider"} {
		for _, change := range []string{"role", "permission"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				pool, actor, user := financeSettingsFixture(t)
				run, audit := financeSettingsCommand(t, pool, actor, user, action)
				traced, entered, release := testutil.GateQuery(t, pool, "INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)")
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				var workers sync.WaitGroup
				t.Cleanup(func() { release(); cancel(); workers.Wait() })
				done := make(chan error, 1)
				workers.Add(1)
				go func() { defer workers.Done(); done <- run(ctx, traced) }()
				select {
				case <-entered:
				case err := <-done:
					t.Fatal("command did not reach audit", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				conn, err := pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				pid := int32(conn.Conn().PgConn().PID())
				revoked := make(chan error, 1)
				workers.Add(1)
				go func() {
					defer workers.Done()
					defer conn.Release()
					var err error
					if change == "role" {
						_, err = conn.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
					} else {
						_, err = conn.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:finance'`)
					}
					revoked <- err
				}()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var waiting bool
					if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting {
						break
					}
					select {
					case err := <-revoked:
						t.Fatal("authority changed before finance commit", err)
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("revocation did not wait", ctx.Err())
					}
				}
				release()
				if err := <-done; err != nil {
					t.Fatal("authorized command did not commit", err)
				}
				if err := <-revoked; err != nil {
					t.Fatal(err)
				}
				var count int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_id=$1 AND action=$2 AND request_id='finance-settings-audit'`, actor, audit).Scan(&count); err != nil || count != 1 {
					t.Fatalf("authorized command lost audit: %d %v", count, err)
				}
				if err := run(ctx, pool); !errors.Is(err, admin.ErrForbidden) {
					t.Fatalf("fresh revoked command accepted: %v", err)
				}
			})
		}
	}
}
