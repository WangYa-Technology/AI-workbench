package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProductQuarantineAdmissionDoesNotWaitForConcurrentReview(t *testing.T) {
	for _, first := range []string{"review", "ingress"} {
		t.Run(first, func(t *testing.T) {
			s, pool, checkout, actor, id := recoverableQuarantineAuthorityFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			t.Cleanup(cancel)
			query := "SELECT event FROM product_webhook_quarantines WHERE id=$1"
			if first == "ingress" {
				query = "UPDATE product_webhook_quarantines q SET state='admitted'"
			}
			traced, entered, release := testutil.GateQuery(t, pool, query)
			blocked := NewServiceWithRuntimes(traced, s.config, s.runtimes)
			var product uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT resource_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&product); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			// Use the original signed receipt, including its original timestamp.
			var raw []byte
			if err := pool.QueryRow(ctx, `SELECT event FROM product_webhook_quarantines WHERE id=$1`, id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var event minimizedProviderEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			body := productPaidEvent(checkout.PaymentID, product, event.OccurredAt.Unix(), checkout.AmountCents)
			signature := fmt.Sprintf("t=%d,v1=%s", now.Unix(), stripeSignature(testStripeWebhookSecret, now.Unix(), body))
			input := RecheckWebhookInput{ExpectedVersion: 1, Reason: "Original checkout identity was verified."}
			var workers sync.WaitGroup
			workers.Add(1)
			t.Cleanup(func() { release(); cancel(); workers.Wait() })
			done := make(chan error, 1)
			go func() {
				defer workers.Done()
				var err error
				if first == "review" {
					_, err = blocked.RecheckWebhookQuarantine(ctx, actor, id, input)
				} else {
					_, err = blocked.ReceiveStripeWebhook(ctx, body, signature)
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("first admission did not reach its gate", ctx.Err())
			}
			attempt, stop := context.WithTimeout(ctx, 2*time.Second)
			defer stop()
			if first == "review" {
				if _, err := s.ReceiveStripeWebhook(attempt, body, signature); !errors.Is(err, ErrWebhookAdmissionBusy) {
					t.Fatalf("ingress waited for review or accepted partial evidence: %v", err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events`, 0)
			} else {
				if _, err := s.RecheckWebhookQuarantine(attempt, actor, id, input); !errors.Is(err, ErrQuarantineConflict) {
					t.Fatalf("review waited for ingress or admitted twice: %v", err)
				}
			}
			release()
			if err := <-done; err != nil {
				t.Fatal("original admission failed", err)
			}
			receipt, err := s.ReceiveStripeWebhook(ctx, body, signature)
			if err != nil || !receipt.Duplicate {
				t.Fatal("original signed delivery was not safely retryable", receipt, err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events`, 1)
			quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1`, 1, PaymentEventJobKind)
			quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines WHERE state='admitted'`, 1)
			processStripeReceipt(t, s, pool, receipt)
			assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
		})
	}
}

func recoverableQuarantineAuthorityFixture(t *testing.T) (*Service, *pgxpool.Pool, Checkout, uuid.UUID, uuid.UUID) {
	t.Helper()
	s, pool, checkout, _, product := pendingStripeWebhookFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents)
	quarantineExec(t, pool, `UPDATE payment_intents SET provider_checkout_id='cs_identity_unavailable' WHERE id=$1`, checkout.PaymentID)
	if _, err := signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE payment_intents SET provider_checkout_id='cs_workflow123' WHERE id=$1`, checkout.PaymentID)
	actor := uuid.New()
	quarantineExec(t, pool, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Finance operator','admin')`, actor, actor.String()+"@test.local", "finance_"+actor.String()[:8])
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM product_webhook_quarantines`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return s, pool, checkout, actor, id
}

func TestProductQuarantineRecheckRejectsRevokedFinanceAuthority(t *testing.T) {
	for _, phase := range []string{"payment_lock", "locked_receipt"} {
		for _, change := range []string{"role", "suspended", "permission"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				s, pool, checkout, actor, id := recoverableQuarantineAuthorityFixture(t)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				t.Cleanup(cancel)
				var wait, release func()
				if phase == "locked_receipt" {
					traced, entered, resume := testutil.GateQuery(t, pool, "SELECT event FROM product_webhook_quarantines WHERE id=$1")
					s = NewServiceWithRuntimes(traced, s.config, s.runtimes)
					wait = func() {
						select {
						case <-entered:
						case <-ctx.Done():
							t.Fatal("recheck did not reach locked receipt", ctx.Err())
						}
					}
					release = resume
				} else {
					gate, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = gate.Rollback(context.Background()) })
					if _, err := gate.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, checkout.PaymentID); err != nil {
						t.Fatal(err)
					}
					wait = func() { waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID())) }
					release = func() {
						if err := gate.Commit(ctx); err != nil {
							t.Fatal(err)
						}
					}
				}
				input := RecheckWebhookInput{ExpectedVersion: 1, Reason: "Original checkout identity was verified."}
				done := make(chan error, 1)
				go func() {
					_, err := s.RecheckWebhookQuarantine(ctx, actor, id, input)
					done <- err
				}()
				wait()
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
				if err := <-done; !errors.Is(err, ErrQuarantineConflict) && !errors.Is(err, ErrQuarantineForbidden) {
					t.Fatalf("revoked finance operator rechecked evidence: %v", err)
				}
				if _, err := s.RecheckWebhookQuarantine(ctx, actor, id, input); !errors.Is(err, ErrQuarantineForbidden) {
					t.Fatalf("revoked operator retried in a fresh transaction: %v", err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines WHERE id=$1 AND state='pending' AND version=1 AND checked_at IS NULL`, 1, id)
				quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantine_checks`, 0)
				quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.webhook_rechecked'`, 0)
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events`, 0)
				quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1`, 0, PaymentEventJobKind)
				quarantineCount(t, pool, `SELECT count(*) FROM entitlements`, 0)
			})
		}
	}
}

func TestProductQuarantineAuthorityRemainsLockedThroughAdmission(t *testing.T) {
	for _, change := range []string{"role", "permission"} {
		t.Run(change, func(t *testing.T) {
			s, pool, _, actor, id := recoverableQuarantineAuthorityFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			traced, entered, release := testutil.GateQuery(t, pool, "INSERT INTO product_webhook_quarantine_checks")
			s = NewServiceWithRuntimes(traced, s.config, s.runtimes)
			revocation, err := pool.Begin(ctx)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			pid := int32(revocation.Conn().PgConn().PID())
			var wg sync.WaitGroup
			t.Cleanup(func() {
				release()
				cancel()
				wg.Wait()
				_ = revocation.Rollback(context.Background())
			})
			input := RecheckWebhookInput{ExpectedVersion: 1, Reason: "The original paid event is consistent again."}
			rechecked := make(chan error, 1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := s.RecheckWebhookQuarantine(ctx, actor, id, input)
				rechecked <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("recheck did not reach its audit record", ctx.Err())
			}
			revoked := make(chan error, 1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				var err error
				if change == "role" {
					_, err = revocation.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
				} else {
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
					t.Fatalf("authority changed before admission committed: %v", err)
				case <-ctx.Done():
					t.Fatal("revocation did not wait for admission", ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			release()
			if err := <-rechecked; err != nil {
				t.Fatal("authorized admission failed", err)
			}
			if err := <-revoked; err != nil {
				t.Fatal("revocation failed after admission", err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines WHERE id=$1 AND state='admitted'`, 1, id)
			quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantine_checks`, 1)
			quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events`, 1)
			quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1`, 1, PaymentEventJobKind)
			if _, err := s.RecheckWebhookQuarantine(ctx, actor, id, input); !errors.Is(err, ErrQuarantineForbidden) {
				t.Fatal("revoked operator remained authorized", err)
			}
		})
	}
}
