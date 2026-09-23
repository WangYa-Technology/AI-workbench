package payments

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A separate operator preserves the buyer's real permissions and export scope.
func refundCheckOperator(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	quarantineExec(t, pool, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Refund check operator','admin')`, id, id.String()+"@test.local", "refund_op_"+id.String()[:8])
	return id
}

func refundCheckAuthoritySnapshot(t *testing.T, pool *pgxpool.Pool, payment uuid.UUID) string {
	t.Helper()
	var snapshot string
	err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'payment',to_jsonb(pi),
 'order',(SELECT to_jsonb(o) FROM orders o WHERE o.id=pi.order_id),
 'checks',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM product_refund_checks c),
 'jobs',(SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id) FROM jobs j),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM audit_events a),
 'rights',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM entitlements e))::text
 FROM payment_intents pi WHERE pi.id=$1`, payment).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func revokeRefundCheckOperator(t *testing.T, pool *pgxpool.Pool, actor uuid.UUID, change string) {
	t.Helper()
	switch change {
	case "role":
		quarantineExec(t, pool, `UPDATE users SET role='member' WHERE id=$1`, actor)
	case "suspended":
		quarantineExec(t, pool, `UPDATE users SET status='suspended' WHERE id=$1`, actor)
	case "permission":
		quarantineExec(t, pool, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:finance'`)
	}
}

func TestProductRefundCheckRequiresFinanceAuthority(t *testing.T) {
	for _, change := range []string{"buyer", "role", "suspended", "permission"} {
		t.Run(change, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			t.Cleanup(cleanup)
			s, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, &refundReadRuntime{})
			actor := refundCheckOperator(t, pool)
			if change == "buyer" {
				actor = buyer
			} else {
				revokeRefundCheckOperator(t, pool, actor, change)
			}
			h, err := s.RefundHistory(t.Context(), checkout.PaymentID, "", 20)
			if err != nil {
				t.Fatal(err)
			}
			before := refundCheckAuthoritySnapshot(t, pool, checkout.PaymentID)
			if _, err := s.RequestRefundCheck(t.Context(), actor, checkout.PaymentID, h.PaymentVersion); !errors.Is(err, ErrFinanceForbidden) {
				t.Error("caller without finance authority was not rejected", err)
			}
			if after := refundCheckAuthoritySnapshot(t, pool, checkout.PaymentID); after != before {
				t.Error("unauthorized command changed payment, order, checks, jobs, audit or rights")
			}
		})
	}
}

func TestProductRefundCheckRejectsRevocationBeforeMutation(t *testing.T) {
	for _, phase := range []string{"payment_lock", "locked_payment"} {
		for _, change := range []string{"role", "suspended", "permission"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				pool, cleanup := paymentTestPool(t)
				t.Cleanup(cleanup)
				s, checkout, _, _, _ := fulfilledRefundFixture(t, pool, &refundReadRuntime{})
				actor := refundCheckOperator(t, pool)
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				t.Cleanup(cancel)
				h, err := s.RefundHistory(ctx, checkout.PaymentID, "", 20)
				if err != nil {
					t.Fatal(err)
				}
				h, err = s.RequestRefundCheck(ctx, actor, checkout.PaymentID, h.PaymentVersion)
				if err != nil {
					t.Fatal(err)
				}
				// A failed job whose check has not yet been marked failed must remain
				// byte-for-byte unchanged if authority is revoked during the command.
				quarantineExec(t, pool, `UPDATE jobs SET status='failed',attempts=max_attempts,last_error_code='payment_timeout' WHERE id=(SELECT job_id FROM product_refund_checks WHERE id=$1)`, h.LatestCheck.ID)
				before := refundCheckAuthoritySnapshot(t, pool, checkout.PaymentID)
				var wait, release func()
				if phase == "payment_lock" {
					gate, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = gate.Rollback(context.Background()) })
					if _, err := gate.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, checkout.PaymentID); err != nil {
						t.Fatal(err)
					}
					wait = func() { waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID())) }
					var once sync.Once
					release = func() { once.Do(func() { _ = gate.Rollback(context.Background()) }) }
				} else {
					traced, entered, resume := testutil.GateQuery(t, pool, "SELECT EXISTS(SELECT 1 FROM product_payment_identity_gaps WHERE payment_id=$1)")
					s = NewServiceWithRuntimes(traced, s.config, s.runtimes)
					release = resume
					wait = func() {
						select {
						case <-entered:
						case <-ctx.Done():
							t.Fatal("refund check did not reach locked payment", ctx.Err())
						}
					}
				}
				var workers sync.WaitGroup
				t.Cleanup(func() { release(); cancel(); workers.Wait() })
				done := make(chan error, 1)
				workers.Add(1)
				go func() {
					defer workers.Done()
					_, err := s.RequestRefundCheck(ctx, actor, checkout.PaymentID, h.PaymentVersion)
					done <- err
				}()
				wait()
				revokeRefundCheckOperator(t, pool, actor, change)
				release()
				if err := <-done; !errors.Is(err, ErrRefundConflict) {
					t.Error("stale authority did not return a transaction conflict", err)
				}
				if _, err := s.RequestRefundCheck(ctx, actor, checkout.PaymentID, h.PaymentVersion); !errors.Is(err, ErrFinanceForbidden) {
					t.Error("fresh command from revoked actor was not forbidden", err)
				}
				if after := refundCheckAuthoritySnapshot(t, pool, checkout.PaymentID); after != before {
					t.Error("revoked command changed payment, order, checks, jobs, audit or rights")
				}
			})
		}
	}
}

func TestProductRefundCheckAuthorityPinnedThroughCommit(t *testing.T) {
	for _, change := range []string{"role", "suspended", "permission"} {
		t.Run(change, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			t.Cleanup(cleanup)
			runtime := &refundReadRuntime{}
			s, checkout, _, _, _ := fulfilledRefundFixture(t, pool, runtime)
			actor := refundCheckOperator(t, pool)
			h, err := s.RefundHistory(t.Context(), checkout.PaymentID, "", 20)
			if err != nil {
				t.Fatal(err)
			}
			traced, entered, release := testutil.GateQuery(t, pool, "INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)")
			s = NewServiceWithRuntimes(traced, s.config, s.runtimes)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			t.Cleanup(cancel)
			revocation, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pid := int32(revocation.Conn().PgConn().PID())
			var workers sync.WaitGroup
			t.Cleanup(func() { release(); cancel(); workers.Wait(); _ = revocation.Rollback(context.Background()) })
			done := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				_, err := s.RequestRefundCheck(ctx, actor, checkout.PaymentID, h.PaymentVersion)
				done <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("command did not reach audit", ctx.Err())
			}
			revoked := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				var err error
				switch change {
				case "role":
					_, err = revocation.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
				case "suspended":
					_, err = revocation.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor)
				case "permission":
					_, err = revocation.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:finance'`)
				}
				if err == nil {
					err = revocation.Commit(ctx)
				}
				revoked <- err
			}()
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
					t.Fatalf("authority changed before authorized commit: %v", err)
				case <-ctx.Done():
					t.Fatal("revocation did not wait for the command", ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			release()
			if err := <-done; err != nil {
				t.Fatal("authorized command failed", err)
			}
			if err := <-revoked; err != nil {
				t.Fatal("later revocation failed", err)
			}
			if _, err := s.RequestRefundCheck(ctx, actor, checkout.PaymentID, h.PaymentVersion+1); !errors.Is(err, ErrFinanceForbidden) {
				t.Fatal("revoked actor remained authorized", err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1 AND requested_by=$2 AND origin='operator'`, 1, checkout.PaymentID, actor)
			quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1`, 1, ProductRefundCheckJobKind)
			quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.refund_check_requested' AND actor_id=$1 AND resource_id=$2`, 1, actor, checkout.PaymentID)
			if runtime.reads != 0 || len(runtime.operations) != 0 {
				t.Fatal("enqueue performed remote I/O")
			}
		})
	}
}

func TestProductRefundCheckAuditFailureRollsBackCommand(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	s, checkout, _, _, _ := fulfilledRefundFixture(t, pool, &refundReadRuntime{})
	actor := refundCheckOperator(t, pool)
	h, err := s.RefundHistory(t.Context(), checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	h, err = s.RequestRefundCheck(t.Context(), actor, checkout.PaymentID, h.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE jobs SET status='failed',last_error_code='payment_timeout' WHERE id=(SELECT job_id FROM product_refund_checks WHERE id=$1)`, h.LatestCheck.ID)
	quarantineExec(t, pool, `CREATE FUNCTION reject_refund_check_audit() RETURNS trigger AS $$ BEGIN
 IF NEW.action='payment.refund_check_requested' THEN RAISE EXCEPTION 'injected audit failure'; END IF;
 RETURN NEW; END; $$ LANGUAGE plpgsql;
 CREATE TRIGGER reject_refund_check_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_refund_check_audit()`)
	before := refundCheckAuthoritySnapshot(t, pool, checkout.PaymentID)
	if _, err := s.RequestRefundCheck(t.Context(), actor, checkout.PaymentID, h.PaymentVersion); err == nil {
		t.Fatal("audit failure accepted")
	}
	if refundCheckAuthoritySnapshot(t, pool, checkout.PaymentID) != before {
		t.Fatal("audit failure did not roll back entire command")
	}
}
