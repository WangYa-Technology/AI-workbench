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

type financeAuthorityFixture struct {
	pool                  *pgxpool.Pool
	actor, payment, event uuid.UUID
	action                string
}

func newFinanceAuthorityFixture(t *testing.T, action string) financeAuthorityFixture {
	t.Helper()
	pool, cleanup := testPool(t)
	t.Cleanup(cleanup)
	f := financeAuthorityFixture{pool: pool, actor: uuid.New(), payment: uuid.New(), event: uuid.New(), action: action}
	buyer, seller, resource, source, order, operation := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{f.actor, buyer, seller} {
		exec(`INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Recovery authority','member')`, id, id.String()+"@test.local", "authority_"+id.String()[:8])
	}
	exec(`UPDATE users SET role='admin' WHERE id=$1`, f.actor)
	purpose, status := "product", "refund_failed"
	if action == "retry_transfer" || action == "task_refund" {
		purpose = "task"
		if action == "retry_transfer" {
			status = "transfer_pending"
		}
		exec(`INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary)
 VALUES($1,$2,'Authority task','Finance recovery fixture.','image',1900,'USD',now()+interval '7 days','cancelled',$3,'Recovery fixture.')`, resource, buyer, seller)
		exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id)
 VALUES($1,'stripe','task',$2,$3,$4,1900,'USD',$5,false,'authority-payment','pi_authority')`, f.payment, buyer, seller, resource, status)
	} else {
		orderStatus := "refund_requested"
		if action == "check_checkout" || action == "locate_checkout" || action == "replay" {
			orderStatus, status = "payment_pending", "checkout_pending"
		}
		exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'image','Authority source','/media/authority.jpg','image/jpeg','clean','upload','hcai-commercial-standard-v1','local_file','authority.jpg')`, source, seller)
		exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Authority product','Recovery fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, resource, seller, source)
		exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot,refund_operation_id,refund_reason)
 SELECT $1,$2,$3,1900,'USD',$4,'authority-order','Authority product',name,version,terms,refund_window_days,$5,'Automatic refund: source_unavailable'
 FROM licenses WHERE code='hcai-commercial-standard-v1'`, order, buyer, resource, orderStatus, operation)
		exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id,provider_refund_id,compensation_reason)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD',$6,false,'authority-payment',
 CASE WHEN $6='refund_failed' THEN 'pi_authority' END,CASE WHEN $6='refund_failed' THEN 're_authority_failed' END,
 CASE WHEN $6='refund_failed' THEN 'source_unavailable' END)`, f.payment, buyer, seller, resource, order, status)
		if action != "verify_identity" {
			exec(`INSERT INTO product_checkout_requests(payment_id,identity,request)
 SELECT pi.id,jsonb_build_object('provider',pi.provider,'merchantId','acct_fixture','liveMode',pi.live_mode,'endpoint','https://api.stripe.com/v1','apiVersion','2026-02-25.clover','requestVersion','stripe-product-checkout-v1'),
 jsonb_build_object('PaymentID',pi.id,'Purpose','product','ResourceID',pi.resource_id,'OrderExternalID',pi.order_id,'BuyerIdentity',pi.payer_id,'AmountCents',pi.amount_cents,'Currency',pi.currency)
 FROM payment_intents pi WHERE pi.id=$1`, f.payment)
		}
		if action == "check_checkout" {
			exec(`UPDATE payment_intents SET status='checkout_open',provider_checkout_id='cs_authority',checkout_url='https://checkout.stripe.com/c/pay/cs_authority',checkout_expires_at=now()-interval '1 hour' WHERE id=$1`, f.payment)
		}
	}
	if action == "replay" {
		exec(`INSERT INTO payment_provider_events(id,provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,payment_id,purpose)
 VALUES($1,'stripe','evt_authority','payment_intent.succeeded','2026-02-25.clover',false,now(),repeat('a',64),'pi_authority','payment_intent',$2,$3)`, f.event, f.payment, purpose)
		exec(`INSERT INTO payment_provider_event_processing(event_id,status,error_code,processed_at) VALUES($1,'failed','provider_unavailable',now())`, f.event)
	}
	return f
}

func (f financeAuthorityFixture) run(ctx context.Context, pool *pgxpool.Pool) error {
	s := admin.NewService(pool, true)
	if f.action == "replay" {
		_, err := s.ReplayPaymentEvent(ctx, f.actor, f.event, admin.PaymentEventReplay{ExpectedVersion: 1}, "authority-replay")
		return err
	}
	action := f.action
	if action == "task_refund" {
		action = "retry_refund"
	}
	_, err := s.RecoverPayment(ctx, f.actor, f.payment, admin.PaymentRecovery{Action: action, ExpectedVersion: 1}, "authority-recovery")
	return err
}

// Compare all mutable financial evidence, not just the returned error. Refund
// recovery has already changed uncommitted order state when the gate is reached.
func (f financeAuthorityFixture) snapshot(t *testing.T) string {
	t.Helper()
	var snapshot string
	err := f.pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'payments',(SELECT jsonb_agg(to_jsonb(p)) FROM payment_intents p),
 'orders',(SELECT jsonb_agg(to_jsonb(o)) FROM orders o),
 'attempts',(SELECT jsonb_agg(to_jsonb(a)) FROM product_refund_attempts a),
 'processing',(SELECT jsonb_agg(to_jsonb(p)) FROM payment_provider_event_processing p),
 'jobs',(SELECT jsonb_agg(to_jsonb(j)) FROM jobs j),
 'events',(SELECT jsonb_agg(to_jsonb(e)) FROM payment_intent_events e),
 'orderEvents',(SELECT jsonb_agg(to_jsonb(e)) FROM order_events e),
 'audit',(SELECT jsonb_agg(to_jsonb(e)) FROM audit_events e))::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestAdminPaymentRecoveryRejectsRevokedAuthority(t *testing.T) {
	for _, action := range []string{"retry_refund", "task_refund", "retry_transfer", "check_checkout", "verify_identity", "locate_checkout", "replay"} {
		for _, change := range []string{"role", "suspended", "permission"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				f := newFinanceAuthorityFixture(t, action)
				before := f.snapshot(t)
				traced, entered, release := testutil.GateQuery(t, f.pool, "SELECT EXISTS(SELECT 1 FROM jobs WHERE kind=$1")
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				var workers sync.WaitGroup
				workers.Add(1)
				t.Cleanup(func() { release(); cancel(); workers.Wait() })
				done := make(chan error, 1)
				go func() { defer workers.Done(); done <- f.run(ctx, traced) }()
				select {
				case <-entered:
				case err := <-done:
					t.Fatal("fixture was not eligible for recovery", err)
				case <-ctx.Done():
					t.Fatal("recovery did not reach enqueue checks", ctx.Err())
				}
				var err error
				switch change {
				case "role":
					_, err = f.pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, f.actor)
				case "suspended":
					_, err = f.pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, f.actor)
				case "permission":
					_, err = f.pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:finance'`)
				}
				if err != nil {
					t.Fatal(err)
				}
				release()
				if err := <-done; !errors.Is(err, admin.ErrConflict) && !errors.Is(err, admin.ErrForbidden) {
					t.Errorf("revoked finance operator did not receive an authority rejection: %v", err)
				}
				if after := f.snapshot(t); after != before {
					t.Error("rejected recovery changed financial evidence or queued work")
				}
				if err := f.run(ctx, f.pool); !errors.Is(err, admin.ErrForbidden) {
					t.Errorf("fresh revoked operator did not receive forbidden: %v", err)
				}
			})
		}
	}
}

func TestAdminPaymentRecoveryPinsAuthorityThroughEnqueue(t *testing.T) {
	for _, action := range []string{"retry_refund", "replay"} {
		for _, change := range []string{"role", "permission"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				f := newFinanceAuthorityFixture(t, action)
				traced, entered, release := testutil.GateQuery(t, f.pool, "INSERT INTO jobs(kind,payload,max_attempts)")
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				var workers sync.WaitGroup
				t.Cleanup(func() { release(); cancel(); workers.Wait() })
				done := make(chan error, 1)
				workers.Add(1)
				go func() { defer workers.Done(); done <- f.run(ctx, traced) }()
				select {
				case <-entered:
				case err := <-done:
					t.Fatal("valid recovery did not reach enqueue", err)
				case <-ctx.Done():
					t.Fatal("valid recovery did not reach enqueue", ctx.Err())
				}
				conn, err := f.pool.Acquire(ctx)
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
						_, err = conn.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, f.actor)
					} else {
						_, err = conn.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:finance'`)
					}
					revoked <- err
				}()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var waiting bool
					if err := f.pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting {
						break
					}
					select {
					case err := <-revoked:
						t.Fatal("authority changed before enqueue committed", err)
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("revocation did not wait", ctx.Err())
					}
				}
				release()
				if err := <-done; err != nil {
					t.Fatal("authorized recovery did not commit", err)
				}
				if err := <-revoked; err != nil {
					t.Fatal("later revocation did not complete", err)
				}
				var jobs int
				if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 1 {
					t.Fatalf("recovery enqueue: %d %v", jobs, err)
				}
				if action == "replay" {
					var audited int
					if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_id=$1 AND resource_id=$2 AND action='payment.event_replay_requested' AND request_id='authority-replay' AND metadata->>'expectedVersion'='1'`, f.actor, f.event).Scan(&audited); err != nil || audited != 1 {
						t.Fatalf("replay lost actor attribution: %d %v", audited, err)
					}
				}
			})
		}
	}
}
