package payments

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var productPayoutBatchConflicts = []struct{ name, sql string }{
	{"missing_item", `DELETE FROM product_payout_batch_items WHERE settlement_id=$1`},
	{"wrong_batch_mode", `UPDATE product_payout_batches SET live_mode=true WHERE id=(SELECT payout_batch_id FROM product_settlements WHERE id=$1)`},
	{"wrong_provider", `UPDATE product_payout_batches SET provider='waffo_pancake' WHERE id=(SELECT payout_batch_id FROM product_settlements WHERE id=$1)`},
	{"conflicting_transfer", `UPDATE product_payout_batch_items SET provider_transfer_id='tr_conflicting' WHERE settlement_id=$1`},
	{"cancelled_item", `UPDATE product_payout_batch_items SET status='cancelled' WHERE settlement_id=$1`},
	{"unrequested_item", `UPDATE product_payout_batch_items SET status='queued' WHERE settlement_id=$1`},
	{"success_without_evidence", `UPDATE product_payout_batch_items SET status='succeeded' WHERE settlement_id=$1`},
	{"premature_batch_completion", `UPDATE product_payout_batches SET status='completed',completed_at=now() WHERE id=(SELECT payout_batch_id FROM product_settlements WHERE id=$1)`},
}

func TestProductSettlementCompletionRequiresOriginalBatch(t *testing.T) {
	for _, tc := range productPayoutBatchConflicts {
		t.Run(tc.name, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			runtime := &productSettlementRuntime{lost: true}
			service, checkout, job, settlement := productSettlementFixture(t, pool, runtime)
			if err := service.HandleProductSettlementJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE product_settlements SET status='transfer_pending' WHERE id=$1`, settlement); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, tc.sql, settlement); err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if err := lockProductSettlementPaymentTx(ctx, tx, settlement); err != nil {
				t.Fatal(err)
			}
			var batch, seller uuid.UUID
			var destination string
			if err := tx.QueryRow(ctx, `SELECT payout_batch_id,seller_id,destination_id FROM product_settlements WHERE id=$1`, settlement).Scan(&batch, &seller, &destination); err != nil {
				t.Fatal(err)
			}
			err = completeProductSettlementTx(ctx, tx, settlement, batch, seller, destination, "tr_"+strings.ReplaceAll(checkout.PaymentID.String(), "-", ""))
			if err == nil {
				t.Fatal("normal completion accepted inconsistent batch")
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var unchanged bool
			if err := pool.QueryRow(ctx, `SELECT ps.status='transfer_pending' AND ps.provider_transfer_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM product_settlement_events WHERE settlement_id=ps.id AND event_type='transfer.completed')
 AND NOT EXISTS(SELECT 1 FROM notifications WHERE resource_id=ps.id)
 FROM product_settlements ps WHERE ps.id=$1`, settlement).Scan(&unchanged); err != nil || !unchanged {
				t.Fatalf("partial completion: %t %v", unchanged, err)
			}
			if err := service.HandleProductSettlementJob(ctx, job); !errors.Is(err, ErrCheckoutReconciliation) || runtime.transfers.Load() != 1 {
				t.Fatalf("reserved transfer replay: %v calls=%d", err, runtime.transfers.Load())
			}
		})
	}
}

func TestProductSettlementDispatchChecksAndLocksBatch(t *testing.T) {
	for _, scenario := range []string{"invalid_before_send", "concurrent_change"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			runtime := &productSettlementRuntime{}
			service, _, job, settlement := productSettlementFixture(t, pool, runtime)
			if scenario == "invalid_before_send" {
				if _, err := pool.Exec(ctx, `CREATE FUNCTION change_reserved_batch() RETURNS trigger AS $$ BEGIN
 UPDATE product_payout_batches SET live_mode=NOT live_mode WHERE id=(SELECT payout_batch_id FROM product_settlements WHERE id=NEW.settlement_id);
 RETURN NEW; END $$ LANGUAGE plpgsql;
 CREATE TRIGGER change_reserved_batch AFTER UPDATE ON product_settlement_dispatches FOR EACH ROW EXECUTE FUNCTION change_reserved_batch()`); err != nil {
					t.Fatal(err)
				}
			} else {
				runtime.onTransfer = func(ctx context.Context, _ TransferRequest) {
					tx, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(context.Background())
					if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout='100ms'`); err != nil {
						t.Fatal(err)
					}
					_, err = tx.Exec(ctx, `UPDATE product_payout_batches SET live_mode=NOT live_mode WHERE id=(SELECT payout_batch_id FROM product_settlements WHERE id=$1)`, settlement)
					var pgErr *pgconn.PgError
					if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
						t.Fatalf("batch was not locked during transfer: %v", err)
					}
				}
			}
			err := service.HandleProductSettlementJob(ctx, job)
			if scenario == "invalid_before_send" {
				if !errors.Is(err, ErrCheckoutReconciliation) || runtime.transfers.Load() != 0 {
					t.Fatalf("invalid batch sent: %v calls=%d", err, runtime.transfers.Load())
				}
			} else if err != nil || runtime.transfers.Load() != 1 {
				t.Fatalf("valid transfer failed: %v calls=%d", err, runtime.transfers.Load())
			}
		})
	}
}
