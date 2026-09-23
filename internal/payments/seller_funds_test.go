package payments

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSellerFundsPayoutRequestCannotCompeteWithAutomaticSettlement(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, _, _, settlement := productSettlementFixture(t, pool, &productSettlementRuntime{})
	var seller uuid.UUID
	var amount int
	if err := pool.QueryRow(ctx, `UPDATE product_settlements SET status='available' WHERE id=$1 RETURNING seller_id,net_amount_cents`, settlement).Scan(&seller, &amount); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, amount, "automatic-mode-payout"); !errors.Is(err, ErrSellerPayoutModeDisabled) {
		t.Fatalf("automatic settlement accepted competing payout: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_payout_requests WHERE seller_id=$1`, seller).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("automatic mode created %d payout requests", count)
	}
}

func TestSellerFundsPayoutReservationIsIdempotentAndCancellable(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, _, _, settlement := productSettlementFixture(t, pool, &productSettlementRuntime{})
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='seller_payout' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	var seller uuid.UUID
	var amount int
	if err := pool.QueryRow(ctx, `UPDATE product_settlements SET status='available' WHERE id=$1 RETURNING seller_id,net_amount_cents`, settlement).Scan(&seller, &amount); err != nil {
		t.Fatal(err)
	}
	first, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, amount, "payout-idempotent-1")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, amount, "payout-idempotent-1")
	if err != nil || replay.ID != first.ID || replay.Status != "under_review" {
		t.Fatalf("idempotent replay=%+v err=%v", replay, err)
	}
	if _, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, 1, "payout-idempotent-1"); !errors.Is(err, ErrSellerPayoutConflict) {
		t.Fatalf("amount conflict err=%v", err)
	}
	cancelled, err := service.CancelSellerPayoutRequest(ctx, seller, first.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancelled=%+v err=%v", cancelled, err)
	}
	var reservations, releases, events int
	if err := pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type='payout_reservation'),
	 (SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type='payout_release'),
	 (SELECT count(*) FROM seller_payout_request_events WHERE payout_request_id=$1)`, first.ID).Scan(&reservations, &releases, &events); err != nil {
		t.Fatal(err)
	}
	if reservations != 1 || releases != 1 || events != 2 {
		t.Fatalf("ledger reservation=%d release=%d events=%d", reservations, releases, events)
	}
	second, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, amount, "payout-after-cancellation")
	if err != nil || second.ID == first.ID {
		t.Fatalf("cancelled allocation cannot be reserved again: request=%+v err=%v", second, err)
	}
}

func TestSellerFundsUnresolvedRecoveryFreezesWithdrawals(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, _, _, settlement := productSettlementFixture(t, pool, &productSettlementRuntime{})
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='seller_payout' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	var seller uuid.UUID
	var amount int
	if err := pool.QueryRow(ctx, `UPDATE product_settlements SET status='available' WHERE id=$1 RETURNING seller_id,net_amount_cents`, settlement).Scan(&seller, &amount); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status)
	 VALUES($1,$2,1,1,'USD','reconciliation_required')`, seller, settlement); err != nil {
		t.Fatal(err)
	}
	balance, err := singleSellerFunds(t, service, ctx, seller)
	if err != nil || balance.RecoveryDueCents != 1 || balance.WithdrawableCents != 0 {
		t.Errorf("unresolved recovery became withdrawable: balance=%+v err=%v", balance, err)
	}
	if _, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, amount, "unresolved-recovery"); !errors.Is(err, ErrSellerFundsRecoveryDue) {
		t.Fatalf("unresolved recovery allowed request: %v", err)
	}
}

func TestSellerFundsAllocationEvidenceAndUnknownReservation(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, _, _, settlement := productSettlementFixture(t, pool, &productSettlementRuntime{})
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='seller_payout' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	var seller uuid.UUID
	var amount int
	if err := pool.QueryRow(ctx, `UPDATE product_settlements SET status='available' WHERE id=$1 RETURNING seller_id,net_amount_cents`, settlement).Scan(&seller, &amount); err != nil {
		t.Fatal(err)
	}
	request, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, amount, "immutable-allocation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM seller_payout_requests WHERE id=$1`, request.ID); err == nil {
		t.Fatal("database allowed deletion of seller payout evidence")
	}
	for _, sql := range []string{
		`DELETE FROM seller_payout_request_allocations WHERE payout_request_id=$1`,
		`UPDATE seller_payout_request_allocations SET amount_cents=amount_cents+1 WHERE payout_request_id=$1`,
		`UPDATE seller_payout_request_allocations SET released_at=clock_timestamp() WHERE payout_request_id=$1`,
	} {
		if _, err := pool.Exec(ctx, sql, request.ID); err == nil {
			t.Fatalf("allocation mutation succeeded: %s", sql)
		}
	}
	if _, err := service.CancelSellerPayoutRequest(ctx, uuid.New(), request.ID); !errors.Is(err, ErrSellerPayoutNotFound) {
		t.Fatalf("other seller could access cancellation: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_requests SET status='reconciliation_required' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	balance, err := singleSellerFunds(t, service, ctx, seller)
	if err != nil || balance.ReservedCents != amount || balance.WithdrawableCents != 0 {
		t.Fatalf("unknown request released funds: balance=%+v err=%v", balance, err)
	}
	if _, err := service.CancelSellerPayoutRequest(ctx, seller, request.ID); !errors.Is(err, ErrSellerPayoutNotCancellable) {
		t.Fatalf("unknown request cancelled: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_requests SET status='cancelled' WHERE id=$1`, request.ID); err == nil {
		t.Fatal("database allowed cancellation of unknown request")
	}
	for _, migration := range []string{"0154_seller_payout_request_delete_guard", "0153_seller_payout_allocation_release", "0152_seller_payout_mode", "0151_seller_funds_ledger"} {
		t.Run(migration, func(t *testing.T) {
			body, err := os.ReadFile("../platform/database/migrations/" + migration + ".down.sql")
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, string(body))
			if err == nil || !strings.Contains(err.Error(), "evidence prevents") {
				t.Fatalf("downgrade did not protect evidence: %v", err)
			}
		})
	}
}

func TestSellerFundsRefundSerializesWithRequests(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	service, checkout, _, settlement := productSettlementFixture(t, pool, &productSettlementRuntime{})
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='seller_payout' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	var seller uuid.UUID
	var amount int
	if err := pool.QueryRow(ctx, `UPDATE product_settlements SET status='available' WHERE id=$1 RETURNING seller_id,net_amount_cents`, settlement).Scan(&seller, &amount); err != nil {
		t.Fatal(err)
	}
	refund, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer refund.Rollback(context.Background())
	if err := lockProductSettlementPaymentTx(ctx, refund, settlement); err != nil {
		t.Fatal(err)
	}
	if err := markProductSettlementRefundTx(ctx, refund, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, amount, "refund-concurrent-request")
		done <- err
	}()
	waitForProductBlockingTx(t, ctx, pool, int32(refund.Conn().PgConn().PID()))
	if err := refund.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrSellerFundsInsufficient) {
		t.Fatalf("request did not re-read refunded funds after waiting: %v", err)
	}
}

func TestSellerFundsEmptyMigrationsRoundTrip(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, migration := range []string{
		"0172_seller_source_reversal_closure.down", "0171_seller_source_reversal_execution.down", "0170_seller_source_reversal_commands.down", "0169_payment_execution_lease_locks.down", "0168_transfer_execution_jobs.down", "0167_seller_bank_payout_resumes.down", "0166_seller_bank_payout_execution.down",
		"0165_seller_bank_payout_commands.down",
		"0164_seller_funds_scopes.down",
		"0163_seller_payout_bank_summary.down",
		"0162_seller_funding_admissions.down",
		"0161_seller_payout_messages.down",
		"0160_seller_payout_reviews.down",
		"0159_seller_payout_bank_targets.down",
		"0158_seller_funding_read_deadlines.down",
		"0157_seller_funding_recovery.down",
		"0156_seller_payout_funding.down",
		"0155_seller_payout_transfer_evidence.down",
		"0154_seller_payout_request_delete_guard.down", "0153_seller_payout_allocation_release.down", "0152_seller_payout_mode.down", "0151_seller_funds_ledger.down",
		"0151_seller_funds_ledger.up", "0152_seller_payout_mode.up", "0153_seller_payout_allocation_release.up", "0154_seller_payout_request_delete_guard.up",
		"0155_seller_payout_transfer_evidence.up",
		"0156_seller_payout_funding.up",
		"0157_seller_funding_recovery.up",
		"0158_seller_funding_read_deadlines.up",
		"0159_seller_payout_bank_targets.up",
		"0160_seller_payout_reviews.up",
		"0161_seller_payout_messages.up",
		"0162_seller_funding_admissions.up",
		"0163_seller_payout_bank_summary.up",
		"0164_seller_funds_scopes.up",
		"0165_seller_bank_payout_commands.up",
		"0166_seller_bank_payout_execution.up", "0167_seller_bank_payout_resumes.up", "0168_transfer_execution_jobs.up", "0169_payment_execution_lease_locks.up", "0170_seller_source_reversal_commands.up", "0171_seller_source_reversal_execution.up", "0172_seller_source_reversal_closure.up",
	} {
		body, err := os.ReadFile("../platform/database/migrations/" + migration + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("%s: %v", migration, err)
		}
	}
}

func TestSellerFundsModeChangeCannotTransferReservedSettlement(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &productSettlementRuntime{}
	service, _, job, settlement := productSettlementFixture(t, pool, runtime)
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='seller_payout' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductSettlementJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var seller uuid.UUID
	var amount int
	if err := pool.QueryRow(ctx, `SELECT seller_id,net_amount_cents FROM product_settlements WHERE id=$1`, settlement).Scan(&seller, &amount); err != nil {
		t.Fatal(err)
	}
	request, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, amount, "reserve-before-mode-change")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='automatic' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductSettlementJob(ctx, job); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Errorf("reserved settlement dispatch error=%v", err)
	}
	if runtime.transfers.Load() != 0 {
		t.Fatalf("reserved funds transferred %d times", runtime.transfers.Load())
	}
	if _, err := service.CancelSellerPayoutRequest(ctx, seller, request.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductSettlementJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if runtime.transfers.Load() != 1 {
		t.Fatalf("released funds not transferred exactly once: %d", runtime.transfers.Load())
	}
}

func TestSellerFundsManualAvailabilityValidatesPaymentAndReplaysOnce(t *testing.T) {
	for _, changedIdentity := range []bool{true, false} {
		name := "replay"
		if changedIdentity {
			name = "changed_identity"
		}
		t.Run(name, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			runtime := &productSettlementRuntime{}
			service, _, job, settlement := productSettlementFixture(t, pool, runtime)
			if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='seller_payout' WHERE singleton=true`); err != nil {
				t.Fatal(err)
			}
			if changedIdentity {
				runtime.merchant = "different-merchant"
			}
			for range 2 {
				err := service.HandleProductSettlementJob(ctx, job)
				if changedIdentity && !errors.Is(err, ErrCheckoutReconciliation) {
					t.Errorf("wrong merchant accepted: %v", err)
				} else if !changedIdentity && err != nil {
					t.Fatal(err)
				}
			}
			var status string
			var events int
			if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM product_settlement_events WHERE settlement_id=$1 AND event_type='settlement.available') FROM product_settlements WHERE id=$1`, settlement).Scan(&status, &events); err != nil {
				t.Fatal(err)
			}
			if changedIdentity {
				if status != "pending_hold" || events != 0 {
					t.Fatalf("invalid funds became available: status=%s events=%d", status, events)
				}
			} else if status != "available" || events != 1 {
				t.Fatalf("replay changed availability evidence: status=%s events=%d", status, events)
			}
			if runtime.transfers.Load() != 0 {
				t.Fatal("manual mode dispatched automatic transfer")
			}
		})
	}
}

func TestSellerFundsConcurrentRequestsCannotDoubleReserve(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, _, _, settlement := productSettlementFixture(t, pool, &productSettlementRuntime{})
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='seller_payout' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	var seller uuid.UUID
	var amount int
	if err := pool.QueryRow(ctx, `UPDATE product_settlements SET status='available' WHERE id=$1 RETURNING seller_id,net_amount_cents`, settlement).Scan(&seller, &amount); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, amount, "concurrent-payout-"+string(rune('a'+i)))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	var success, insufficient int
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrSellerFundsInsufficient) {
			insufficient++
		} else {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if success != 1 || insufficient != 1 {
		t.Fatalf("success=%d insufficient=%d", success, insufficient)
	}
}
