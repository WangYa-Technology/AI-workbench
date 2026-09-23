package payments

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const reserveSellerTransferSQL = `INSERT INTO seller_payout_transfers
 (payout_request_id,settlement_id,provider,live_mode,destination_id,amount_cents,currency,provider_identity,dispatch_key)
 SELECT $1::uuid,ps.id,ps.provider,ps.live_mode,'acct_settlement',ps.net_amount_cents,ps.currency,r.identity,'seller-payout:'||$1::text
 FROM product_settlements ps JOIN product_checkout_requests r ON r.payment_id=ps.payment_id WHERE ps.id=$2::uuid`

func sellerTransferFixture(t *testing.T, pool *pgxpool.Pool) (*Service, Checkout, uuid.UUID, SellerPayoutRequest) {
	t.Helper()
	ctx := context.Background()
	service, checkout, job, settlement := productSettlementFixture(t, pool, &productSettlementRuntime{})
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
	request, err := service.CreateSellerPayoutRequestForSettlement(ctx, seller, settlement, amount, "transfer-evidence-test")
	if err != nil {
		t.Fatal(err)
	}
	return service, checkout, settlement, request
}

func requirePayoutConstraint(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || (pgErr.Code != "23514" && pgErr.Code != "55000") {
		t.Fatalf("expected financial constraint rejection, got %v", err)
	}
}

func TestSellerPayoutTransferReservationBindings(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	_, _, settlement, request := sellerTransferFixture(t, pool)
	for _, test := range []struct{ name, from, to string }{
		{"destination", "'acct_settlement'", "'acct_other'"},
		{"provider", "ps.provider,ps.live_mode", "'waffo_pancake',ps.live_mode"},
		{"mode", "ps.provider,ps.live_mode", "ps.provider,NOT ps.live_mode"},
		{"amount", "ps.net_amount_cents,ps.currency", "ps.net_amount_cents+1,ps.currency"},
		{"merchant", "r.identity,", `jsonb_set(r.identity,'{merchantId}','"acct_other"'::jsonb),`},
		{"missing_identity", "r.identity,", "'{}'::jsonb,"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, strings.Replace(reserveSellerTransferSQL, test.from, test.to, 1), request.ID, settlement)
			requirePayoutConstraint(t, err)
		})
	}
	if _, err := pool.Exec(ctx, reserveSellerTransferSQL, request.ID, settlement); err != nil {
		t.Fatal(err)
	}
}

func TestSellerPayoutTransferEvidenceAndUnknownResult(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, _, settlement, request := sellerTransferFixture(t, pool)
	if _, err := pool.Exec(ctx, reserveSellerTransferSQL, request.ID, settlement); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`DELETE FROM seller_payout_transfers WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET provider_identity=jsonb_set(provider_identity,'{merchantId}','"acct_other"') WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET dispatch_key='another-command' WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET reserved_at=reserved_at+interval '1 second' WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET status='succeeded' WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET status='failed' WHERE payout_request_id=$1`,
		`UPDATE seller_payout_requests SET status='failed' WHERE id=$1`,
		`UPDATE seller_payout_requests SET status='succeeded' WHERE id=$1`,
	} {
		_, err := pool.Exec(ctx, sql, request.ID)
		requirePayoutConstraint(t, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_transfers SET status='processing',evidence='{"attempt":"original"}' WHERE payout_request_id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_transfers SET status='reconciliation_required' WHERE payout_request_id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_requests SET status='reconciliation_required' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE seller_payout_transfers SET status='requested' WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET status='processing' WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET evidence='{}' WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET evidence='{"attempt":"replacement"}' WHERE payout_request_id=$1`,
		`UPDATE seller_payout_requests SET status='under_review' WHERE id=$1`,
		`UPDATE seller_payout_requests SET status='processing' WHERE id=$1`,
		`UPDATE seller_payout_requests SET status='failed' WHERE id=$1`,
	} {
		_, err := pool.Exec(ctx, sql, request.ID)
		requirePayoutConstraint(t, err)
	}
	if _, err := service.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID); !errors.Is(err, ErrSellerPayoutNotCancellable) {
		t.Fatalf("unknown transfer cancelled: %v", err)
	}
	balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.ReservedCents != request.AmountCents || balance.WithdrawableCents != 0 {
		t.Fatalf("unknown funds released: %+v, %v", balance, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_transfers SET status='succeeded',provider_transfer_id='tr_evidence',
 evidence=evidence||'{"result":"confirmed"}'::jsonb WHERE payout_request_id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE seller_payout_transfers SET provider_transfer_id='tr_replacement' WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET provider_transfer_id=NULL WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET status='reconciliation_required' WHERE payout_request_id=$1`,
		`UPDATE seller_payout_transfers SET evidence=evidence||'{"extra":true}'::jsonb WHERE payout_request_id=$1`,
	} {
		_, err := pool.Exec(ctx, sql, request.ID)
		requirePayoutConstraint(t, err)
	}
}

func TestFailedSellerPayoutDoesNotCreateRecoveryDebt(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	_, checkout, settlement, request := sellerTransferFixture(t, pool)
	if _, err := pool.Exec(ctx, reserveSellerTransferSQL, request.ID, settlement); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_transfers SET status='failed',error_code='account_restricted',evidence='{"provider":"declined"}' WHERE payout_request_id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := lockProductSettlementPaymentTx(ctx, tx, settlement); err != nil {
		t.Fatal(err)
	}
	if err := markProductSettlementRefundTx(ctx, tx, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	var recovery int
	if err := pool.QueryRow(ctx, `SELECT status,recovery_amount_cents FROM product_settlements WHERE id=$1`, settlement).Scan(&status, &recovery); err != nil {
		t.Fatal(err)
	}
	if status != "refund_hold" || recovery != 0 {
		t.Fatalf("failed payout was treated as transferred: status=%s recovery=%d", status, recovery)
	}
}

func TestSellerPayoutTransferCancellationSerialization(t *testing.T) {
	for _, cancelFirst := range []bool{true, false} {
		name := "reservation_first"
		if cancelFirst {
			name = "cancellation_first"
		}
		t.Run(name, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			service, _, settlement, request := sellerTransferFixture(t, pool)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if err := lockSellerFundsTx(ctx, tx, request.SellerID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			if cancelFirst {
				if _, err := tx.Exec(ctx, `UPDATE seller_payout_requests SET status='cancelled' WHERE id=$1`, request.ID); err != nil {
					t.Fatal(err)
				}
				go func() {
					_, err := pool.Exec(ctx, reserveSellerTransferSQL, request.ID, settlement)
					done <- err
				}()
			} else {
				if _, err := tx.Exec(ctx, reserveSellerTransferSQL, request.ID, settlement); err != nil {
					t.Fatal(err)
				}
				go func() {
					_, err := service.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID)
					done <- err
				}()
			}
			waitForProductBlockingTx(t, ctx, pool, int32(tx.Conn().PgConn().PID()))
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if cancelFirst {
				requirePayoutConstraint(t, err)
			} else if !errors.Is(err, ErrSellerPayoutNotCancellable) {
				t.Fatalf("reserved transfer cancellation: %v", err)
			}
		})
	}
}

func TestSellerPayoutTransferRefundSerialization(t *testing.T) {
	for _, refundFirst := range []bool{true, false} {
		name := "reservation_first"
		if refundFirst {
			name = "refund_first"
		}
		t.Run(name, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			service, checkout, settlement, request := sellerTransferFixture(t, pool)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			done := make(chan error, 1)
			if refundFirst {
				if err := lockProductSettlementPaymentTx(ctx, tx, settlement); err != nil {
					t.Fatal(err)
				}
				if err := markProductSettlementRefundTx(ctx, tx, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				go func() {
					_, err := pool.Exec(ctx, reserveSellerTransferSQL, request.ID, settlement)
					done <- err
				}()
			} else {
				if _, err := tx.Exec(ctx, reserveSellerTransferSQL, request.ID, settlement); err != nil {
					t.Fatal(err)
				}
				go func() {
					refund, err := pool.Begin(ctx)
					if err != nil {
						done <- err
						return
					}
					defer refund.Rollback(context.Background())
					if err = lockProductSettlementPaymentTx(ctx, refund, settlement); err == nil {
						err = markProductSettlementRefundTx(ctx, refund, checkout.PaymentID)
					}
					if err == nil {
						err = refund.Commit(ctx)
					}
					done <- err
				}()
			}
			waitForProductBlockingTx(t, ctx, pool, int32(tx.Conn().PgConn().PID()))
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if refundFirst {
				requirePayoutConstraint(t, err)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
				if err != nil || balance.RecoveryDueCents != 0 || balance.WithdrawableCents != 0 {
					t.Fatalf("unstarted transfer refund created debt or released funds: %+v, %v", balance, err)
				}
				var status string
				if err := pool.QueryRow(ctx, `SELECT status FROM product_settlements WHERE id=$1`, settlement).Scan(&status); err != nil {
					t.Fatal(err)
				}
				if status != "refund_hold" {
					t.Fatalf("unstarted transfer refund used recovery state: %s", status)
				}
			}
		})
	}
}

func TestSellerPayoutTransferMigrationProtectsExistingEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	_, _, settlement, request := sellerTransferFixture(t, pool)
	if _, err := pool.Exec(ctx, reserveSellerTransferSQL, request.ID, settlement); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"down", "up"} {
		body, err := os.ReadFile("../platform/database/migrations/0155_seller_payout_transfer_evidence." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, execErr := tx.Exec(ctx, string(body))
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		requirePayoutConstraint(t, execErr)
	}
}
