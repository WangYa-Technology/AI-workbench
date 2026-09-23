package admin_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
)

func TestFinanceAdjustmentIdempotencyAndConflict(t *testing.T) {
	pool, actor, user := financeSettingsFixture(t)
	s := admin.NewService(pool, true)
	input := admin.FinanceAdjustment{DeltaCents: 500, Currency: "USD"}
	var balance int64
	if err := pool.QueryRow(t.Context(), `SELECT balance_cents FROM billing_accounts WHERE user_id=$1 AND currency='USD'`, user).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	first, err := s.AdjustFinance(t.Context(), actor, user, input, "adjust-original", "first-request")
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.OperationID == uuid.Nil || first.BalanceCents != balance+500 {
		t.Fatalf("first: %+v", first)
	}
	input.Currency = " usd "
	replay, err := s.AdjustFinance(t.Context(), actor, user, input, "adjust-original", "different-request")
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.OperationID != first.OperationID || replay.BalanceCents != first.BalanceCents {
		t.Fatalf("replay: %+v", replay)
	}
	for _, conflict := range []struct {
		user  uuid.UUID
		delta int
	}{{actor, 500}, {user, 501}, {user, -500}} {
		if _, err := s.AdjustFinance(t.Context(), actor, conflict.user, admin.FinanceAdjustment{DeltaCents: conflict.delta, Currency: "USD"}, "adjust-original", "conflict-request"); !errors.Is(err, admin.ErrConflict) {
			t.Fatalf("key rebind accepted: %v", err)
		}
	}
	second, err := s.AdjustFinance(t.Context(), user, user, input, "adjust-original", "second-actor")
	if err != nil {
		t.Fatal(err)
	}
	if second.Replayed || second.OperationID == first.OperationID || second.BalanceCents != balance+1000 {
		t.Fatalf("actor scopes merged: %+v", second)
	}
	latest, err := s.AdjustFinance(t.Context(), actor, user, input, "adjust-original", "third-request")
	if err != nil {
		t.Fatal(err)
	}
	if latest.OperationID != first.OperationID || !latest.Replayed || latest.BalanceCents != second.BalanceCents {
		t.Fatalf("replay must return current account, original operation: %+v", latest)
	}
	var commands, entries, audits int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM admin_finance_adjustments),(SELECT count(*) FROM billing_entries WHERE entry_type='admin_adjustment'),(SELECT count(*) FROM audit_events WHERE action='admin.finance_adjusted')`).Scan(&commands, &entries, &audits); err != nil || commands != 2 || entries != 2 || audits != 2 {
		t.Fatalf("duplicate evidence: %d %d %d %v", commands, entries, audits, err)
	}
	var request string
	if err := pool.QueryRow(t.Context(), `SELECT request_id FROM admin_finance_adjustments WHERE operation_id=$1`, first.OperationID).Scan(&request); err != nil || request != "first-request" {
		t.Fatalf("replay overwrote attribution: %q %v", request, err)
	}
	for _, key := range []string{"", "short", "key has spaces", string(make([]byte, 129))} {
		if _, err := s.AdjustFinance(t.Context(), actor, user, input, key, "invalid-key"); !errors.Is(err, admin.ErrInvalid) {
			t.Fatal("invalid key accepted", err)
		}
	}
	if _, err := s.AdjustFinance(t.Context(), actor, uuid.New(), input, "missing-account", "missing"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatal("missing account", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdjustFinance(t.Context(), actor, user, input, "adjust-original", "revoked-replay"); !errors.Is(err, admin.ErrForbidden) {
		t.Fatal("replay bypassed revoked authority", err)
	}
}

func TestFinanceAdjustmentConcurrentRetries(t *testing.T) {
	pool, actor, user := financeSettingsFixture(t)
	s := admin.NewService(pool, true)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	const n = 12
	results := make(chan admin.FinanceAdjustmentResult, n)
	errs := make(chan error, n)
	var workers sync.WaitGroup
	for range n {
		workers.Add(1)
		go func() {
			defer workers.Done()
			r, err := s.AdjustFinance(ctx, actor, user, admin.FinanceAdjustment{DeltaCents: 500, Currency: "USD"}, "parallel-adjust", "parallel-request")
			results <- r
			errs <- err
		}()
	}
	workers.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var operation uuid.UUID
	newCount := 0
	for r := range results {
		if operation == uuid.Nil {
			operation = r.OperationID
		}
		if operation != r.OperationID {
			t.Fatal("same key created multiple operations")
		}
		if !r.Replayed {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatal("new operations", newCount)
	}
	var entries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_entries WHERE operation_id=$1`, operation).Scan(&entries); err != nil || entries != 1 {
		t.Fatal("duplicate entries", entries, err)
	}
	// Different keys may compete for the same balance but cannot spend its
	// reserved portion. The losing transaction must not retain a command.
	if _, err := pool.Exec(ctx, `UPDATE billing_accounts SET balance_cents=100,reserved_cents=50 WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	errA := make(chan error, 2)
	for _, key := range []string{"debit-one", "debit-two"} {
		workers.Add(1)
		go func(key string) {
			defer workers.Done()
			_, err := s.AdjustFinance(ctx, actor, user, admin.FinanceAdjustment{DeltaCents: -40, Currency: "USD"}, key, "debit-request")
			errA <- err
		}(key)
	}
	workers.Wait()
	close(errA)
	success, insufficient := 0, 0
	for err := range errA {
		if err == nil {
			success++
		} else if errors.Is(err, billing.ErrInsufficientFunds) {
			insufficient++
		} else {
			t.Fatal(err)
		}
	}
	var count int
	var balance, reserved int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents,reserved_cents,(SELECT count(*) FROM admin_finance_adjustments WHERE idempotency_key LIKE 'debit-%') FROM billing_accounts WHERE user_id=$1 AND currency='USD'`, user).Scan(&balance, &reserved, &count); err != nil || success != 1 || insufficient != 1 || balance != 60 || reserved != 50 || count != 1 {
		t.Fatalf("debit invariant: %d %d %d %d %d %v", success, insufficient, balance, reserved, count, err)
	}
}

func TestFinanceAdjustmentReplayRevocation(t *testing.T) {
	pool, actor, user := financeSettingsFixture(t)
	input := admin.FinanceAdjustment{DeltaCents: 500, Currency: "USD"}
	if _, err := admin.NewService(pool, true).AdjustFinance(t.Context(), actor, user, input, "replay-revoke", "original"); err != nil {
		t.Fatal(err)
	}
	before := financeSettingsSnapshot(t, pool)
	traced, entered, release := testutil.GateQuery(t, pool, "SELECT operation_id,user_id,delta_cents,currency FROM admin_finance_adjustments")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	var workers sync.WaitGroup
	t.Cleanup(func() { release(); cancel(); workers.Wait() })
	done := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, err := admin.NewService(traced, true).AdjustFinance(ctx, actor, user, input, "replay-revoke", "replay")
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal("missing replay gate", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:finance'`); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; !errors.Is(err, admin.ErrForbidden) {
		t.Fatal("in-flight replay bypassed revocation", err)
	}
	if before != financeSettingsSnapshot(t, pool) {
		t.Fatal("replay changed evidence")
	}
}

func TestFinanceAdjustmentDatabaseGuards(t *testing.T) {
	pool, actor, user := financeSettingsFixture(t)
	ctx := t.Context()
	// Empty rollback is allowed. Simulate a real pre-protocol entry and ensure
	// upgrading preserves it without inventing a replay key or receipt.
	for _, direction := range []string{"down", "up"} {
		migration, err := os.ReadFile("../platform/database/migrations/0135_admin_finance_adjustments." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, string(migration)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if direction == "down" {
			tx, err = pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = billing.AdjustTx(ctx, tx, user, uuid.New(), 100, "USD", "Historical adjustment", map[string]any{"paymentMode": "local_test"}); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	var historical, fabricated int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_entries WHERE description='Historical adjustment'),(SELECT count(*) FROM admin_finance_adjustments)`).Scan(&historical, &fabricated); err != nil || historical != 1 || fabricated != 0 {
		t.Fatalf("migration changed historical evidence: %d %d %v", historical, fabricated, err)
	}
	before := financeSettingsSnapshot(t, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = billing.AdjustTx(ctx, tx, user, uuid.New(), 500, "USD", "Legacy adjustment writer", map[string]any{"actorId": actor.String(), "requestId": "legacy", "source": "admin_adjustment"})
	if err == nil {
		t.Error("old writer bypassed command guard")
	}
	_ = tx.Rollback(ctx)
	if before != financeSettingsSnapshot(t, pool) {
		t.Fatal("old writer changed balance")
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_finance_adjustments(operation_id,actor_id,idempotency_key,user_id,delta_cents,currency,request_id) VALUES($1,$2,'incomplete-command',$3,500,'USD','incomplete')`, uuid.New(), actor, user)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil {
		t.Fatal("receipt committed without ledger and audit")
	}
	if before != financeSettingsSnapshot(t, pool) {
		t.Fatal("incomplete receipt persisted")
	}
	result, err := admin.NewService(pool, true).AdjustFinance(ctx, actor, user, admin.FinanceAdjustment{DeltaCents: 500, Currency: "USD"}, "immutable-command", "immutable")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM admin_finance_adjustments WHERE operation_id=$1`, `UPDATE admin_finance_adjustments SET delta_cents=501 WHERE operation_id=$1`, `DELETE FROM billing_entries WHERE operation_id=$1`, `UPDATE billing_entries SET amount_cents=501 WHERE operation_id=$1`} {
		if _, err := pool.Exec(ctx, query, result.OperationID); err == nil {
			t.Fatal("immutable financial evidence mutated", query)
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0135_admin_finance_adjustments.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(down)); err == nil {
		t.Error("downgrade discarded replay protection")
	}
	_ = tx.Rollback(ctx)
}
