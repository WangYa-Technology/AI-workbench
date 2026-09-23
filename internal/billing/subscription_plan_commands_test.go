package billing_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func planCommandFixture(t *testing.T) (*pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool, cleanup := testPool(t)
	t.Cleanup(cleanup)
	actor := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Plan operator','admin')`, actor, actor.String()+"@test.local", "plan_"+actor.String()[:8]); err != nil {
		t.Fatal(err)
	}
	var plan uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM subscription_plans WHERE tier_code='creator'`).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	return pool, actor, plan
}

func planUpdate(t *testing.T, body string) billing.SubscriptionPlanUpdate {
	t.Helper()
	var input billing.SubscriptionPlanUpdate
	if err := json.Unmarshal([]byte(body), &input); err != nil {
		t.Fatal(err)
	}
	return input
}

func TestSubscriptionPlanConcurrentStaleEditor(t *testing.T) {
	pool, actor, planID := planCommandFixture(t)
	traced, entered, release := testutil.GateQuery(t, pool, "UPDATE subscription_plans SET")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	config := pool.Config()
	app := "plan-edit-" + uuid.NewString()
	config.ConnConfig.RuntimeParams["application_name"] = app
	config.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	secondPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secondPool.Close)
	var wg sync.WaitGroup
	t.Cleanup(func() { release(); cancel(); wg.Wait() })
	first, second := make(chan error, 1), make(chan error, 1)
	firstInput := planUpdate(t, `{"expectedVersion":1,"name":"First operator name"}`)
	secondInput := planUpdate(t, `{"expectedVersion":1,"priceCents":7890}`)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := billing.NewService(traced).UpdateSubscriptionPlan(ctx, actor, planID, firstInput, "plan-first")
		first <- err
	}()
	select {
	case <-entered:
	case err := <-first:
		t.Fatal("first did not reach write", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := billing.NewService(secondPool).UpdateSubscriptionPlan(ctx, actor, planID, secondInput, "plan-second")
		second <- err
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var secondErr error
	finished := false
wait:
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND cardinality(pg_blocking_pids(pid))>0)`, app).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case secondErr = <-second:
			finished = true
			break wait
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	release()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if !finished {
		secondErr = <-second
	}
	if !errors.Is(secondErr, billing.ErrPlanConflict) {
		t.Fatalf("stale concurrent editor must return a conflict: %v", secondErr)
	}
	var name string
	var price int
	if err := pool.QueryRow(ctx, `SELECT name,price_cents FROM subscription_plans WHERE id=$1`, planID).Scan(&name, &price); err != nil {
		t.Fatal(err)
	}
	if name != "First operator name" || price == 7890 {
		t.Fatalf("conflicting editor mutated plan: %q %d", name, price)
	}
}

func TestSubscriptionPlanRejectsNonFinanceWriter(t *testing.T) {
	pool, actor, planID := planCommandFixture(t)
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	_, err := billing.NewService(pool).UpdateSubscriptionPlan(t.Context(), actor, planID, planUpdate(t, `{"expectedVersion":1,"name":"Unauthorized rewrite"}`), "unauthorized")
	if !errors.Is(err, billing.ErrPlanForbidden) {
		t.Fatalf("member write must be forbidden: %v", err)
	}
	_, err = billing.NewService(pool).CreateSubscriptionPlan(t.Context(), actor, planCreateInput(), "unauthorized-create")
	if !errors.Is(err, billing.ErrPlanForbidden) {
		t.Fatalf("member create must be forbidden: %v", err)
	}
}

func planCreateInput() billing.SubscriptionPlanInput {
	return billing.SubscriptionPlanInput{TierCode: "command_plan", Name: "Command plan", Description: "Plan command test", PriceCents: 1900, Currency: "USD", IncludedPoints: 1000, BillingPeriodDays: 30, Active: true, ModelIDs: []uuid.UUID{}}
}

func readCommandPlan(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) billing.SubscriptionPlan {
	t.Helper()
	items, err := billing.NewService(pool).ListSubscriptionPlans(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatal("plan missing", id)
	return billing.SubscriptionPlan{}
}

func TestSubscriptionPlanRequiredCommandContext(t *testing.T) {
	pool, actor, id := planCommandFixture(t)
	s := billing.NewService(pool)
	before := readCommandPlan(t, pool, id)
	for _, requestID := range []string{"", " \t\n"} {
		if _, err := s.CreateSubscriptionPlan(t.Context(), actor, planCreateInput(), requestID); !errors.Is(err, billing.ErrInvalidPlan) {
			t.Fatalf("blank create request ID: %v", err)
		}
		if _, err := s.UpdateSubscriptionPlan(t.Context(), actor, id, planUpdate(t, `{"expectedVersion":1,"name":"Invalid edit"}`), requestID); !errors.Is(err, billing.ErrInvalidPlan) {
			t.Fatalf("blank update request ID: %v", err)
		}
	}
	for _, version := range []int64{0, -1} {
		if _, err := s.UpdateSubscriptionPlan(t.Context(), actor, id, billing.SubscriptionPlanUpdate{ExpectedVersion: version}, "missing-version"); !errors.Is(err, billing.ErrInvalidPlan) {
			t.Fatalf("invalid version %d: %v", version, err)
		}
	}
	if after := readCommandPlan(t, pool, id); !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid command changed plan: before=%+v after=%+v", before, after)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM subscription_plans WHERE tier_code='command_plan') + (SELECT count(*) FROM audit_events WHERE action IN ('billing.plan_created','billing.plan_updated'))`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid command left records: count=%d err=%v", count, err)
	}
}

func TestSubscriptionPlanAuditFailureRollsBack(t *testing.T) {
	pool, actor, id := planCommandFixture(t)
	ctx := t.Context()
	provider, first, second := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO provider_configs(id,name,protocol,endpoint,runtime_provider,created_by,updated_by)
	 VALUES($1,'Plan models','openai_responses','https://example.invalid/v1','openai',$2,$2)`, provider, actor); err != nil {
		t.Fatal(err)
	}
	for i, model := range []uuid.UUID{first, second} {
		if _, err := pool.Exec(ctx, `INSERT INTO provider_config_models(id,provider_id,mode,model_name,display_name,description,estimated_cost_cents,created_by,updated_by)
		 VALUES($1,$2,'chat',$3,'Plan model','Test model',1,$4,$4)`, model, provider, []string{"first", "second"}[i], actor); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO subscription_plan_models(plan_id,provider_model_id) VALUES($1,$2)`, id, first); err != nil {
		t.Fatal(err)
	}
	before := readCommandPlan(t, pool, id)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_plan_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
	 IF NEW.action IN ('billing.plan_created','billing.plan_updated') THEN RAISE EXCEPTION 'fixture plan audit failure'; END IF; RETURN NEW; END $$;
	 CREATE TRIGGER reject_plan_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_plan_audit()`); err != nil {
		t.Fatal(err)
	}
	models := []uuid.UUID{second}
	name := "Rollback this edit"
	if _, err := billing.NewService(pool).UpdateSubscriptionPlan(ctx, actor, id, billing.SubscriptionPlanUpdate{ExpectedVersion: before.Version, Name: &name, ModelIDs: &models}, "audit-update"); err == nil {
		t.Fatal("accepted update without audit")
	}
	if after := readCommandPlan(t, pool, id); !reflect.DeepEqual(before, after) {
		t.Fatalf("audit failure changed plan/models/version: before=%+v after=%+v", before, after)
	}
	input := planCreateInput()
	input.ModelIDs = models
	if _, err := billing.NewService(pool).CreateSubscriptionPlan(ctx, actor, input, "audit-create"); err == nil {
		t.Fatal("accepted create without audit")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM subscription_plans WHERE tier_code=$1`, input.TierCode).Scan(&count); err != nil || count != 0 {
		t.Fatalf("audit failure left created plan: count=%d err=%v", count, err)
	}
}

func TestSubscriptionPlanRevocationBeforeWrite(t *testing.T) {
	for _, revocation := range []struct{ name, sql string }{
		{"role", `UPDATE users SET role='member' WHERE id=$1`},
		{"suspension", `UPDATE users SET status='suspended' WHERE id=$1`},
		{"permission", `DELETE FROM role_permissions WHERE role=(SELECT role FROM users WHERE id=$1) AND permission_id='admin:finance'`},
	} {
		t.Run(revocation.name, func(t *testing.T) {
			pool, actor, id := planCommandFixture(t)
			before := readCommandPlan(t, pool, id)
			traced, entered, release := testutil.GateQuery(t, pool, "SELECT version FROM subscription_plans")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			var wg sync.WaitGroup
			t.Cleanup(func() { release(); cancel(); wg.Wait() })
			done := make(chan error, 1)
			wg.Go(func() {
				_, err := billing.NewService(traced).UpdateSubscriptionPlan(ctx, actor, id, planUpdate(t, `{"expectedVersion":1,"name":"Revoked edit"}`), "revoked-edit")
				done <- err
			})
			select {
			case <-entered:
			case err := <-done:
				t.Fatal("write did not reach plan lock", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if _, err := pool.Exec(ctx, revocation.sql, actor); err != nil {
				t.Fatal(err)
			}
			release()
			if err := <-done; !errors.Is(err, billing.ErrPlanForbidden) {
				t.Fatalf("revoked writer was not forbidden: %v", err)
			}
			if after := readCommandPlan(t, pool, id); !reflect.DeepEqual(before, after) {
				t.Fatal("revoked writer changed plan")
			}
		})
	}
}

func TestSubscriptionPlanAuthorityHeldThroughAudit(t *testing.T) {
	for _, kind := range []string{"create", "update"} {
		for _, revocation := range []struct{ name, sql string }{
			{"role", `UPDATE users SET role='member' WHERE id=$1`},
			{"suspension", `UPDATE users SET status='suspended' WHERE id=$1`},
			{"permission", `DELETE FROM role_permissions WHERE role=(SELECT role FROM users WHERE id=$1) AND permission_id='admin:finance'`},
		} {
			t.Run(kind+"/"+revocation.name, func(t *testing.T) {
				pool, actor, id := planCommandFixture(t)
				traced, entered, release := testutil.GateQuery(t, pool, "INSERT INTO audit_events")
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				var wg sync.WaitGroup
				t.Cleanup(func() { release(); cancel(); wg.Wait() })
				done, revoked := make(chan error, 1), make(chan error, 1)
				input := planUpdate(t, `{"expectedVersion":1,"name":"Authorized edit"}`)
				wg.Go(func() {
					var err error
					if kind == "create" {
						_, err = billing.NewService(traced).CreateSubscriptionPlan(ctx, actor, planCreateInput(), "locked-create")
					} else {
						_, err = billing.NewService(traced).UpdateSubscriptionPlan(ctx, actor, id, input, "locked-update")
					}
					done <- err
				})
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
				pid := conn.Conn().PgConn().PID()
				wg.Go(func() {
					defer conn.Release()
					_, err := conn.Exec(ctx, revocation.sql, actor)
					revoked <- err
				})
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var blocked bool
					if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&blocked); err != nil {
						t.Fatal(err)
					}
					if blocked {
						break
					}
					select {
					case err := <-revoked:
						t.Fatalf("revocation crossed an accepted command before its audit committed: %v", err)
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
				release()
				if err := <-done; err != nil {
					t.Fatal("accepted command failed", err)
				}
				if err := <-revoked; err != nil {
					t.Fatal("revocation failed after commit", err)
				}
				var count int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_id=$1 AND request_id=$2 AND metadata->'after'->>'version'=$3 AND (metadata->'before'->>'version') IS NOT DISTINCT FROM $4::text`, actor, "locked-"+kind,
					map[string]string{"create": "1", "update": "2"}[kind], map[string]any{"create": nil, "update": "1"}[kind]).Scan(&count); err != nil || count != 1 {
					t.Fatalf("accepted command audit mismatch: %d err=%v", count, err)
				}
			})
		}
	}
}

func TestSubscriptionPlanMigrationRollbackGuard(t *testing.T) {
	down, err := os.ReadFile("../platform/database/migrations/0140_subscription_plan_versions.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0140_subscription_plan_versions.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"empty", "legacy_update", "created_audit"} {
		t.Run(kind, func(t *testing.T) {
			pool, actor, id := planCommandFixture(t)
			ctx := t.Context()
			if kind == "legacy_update" {
				if _, err := pool.Exec(ctx, `UPDATE subscription_plans SET name='Legacy writer' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
				if got := readCommandPlan(t, pool, id).Version; got != 2 {
					t.Fatalf("legacy write did not advance version: %d", got)
				}
			}
			if kind == "created_audit" {
				if _, err := billing.NewService(pool).CreateSubscriptionPlan(ctx, actor, planCreateInput(), "migration-create"); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			_, err = tx.Exec(ctx, string(down))
			if kind != "empty" {
				if err == nil {
					t.Fatal("downgrade discarded command history")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, string(up)); err != nil {
				t.Fatal("reapply after empty downgrade", err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if got := readCommandPlan(t, pool, id).Version; got != 1 {
				t.Fatalf("unexpected reapplied version: %d", got)
			}
		})
	}
}
