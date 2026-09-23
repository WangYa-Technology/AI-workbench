package payments

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type productSettlementRuntime struct {
	productCheckoutRuntime
	transfers  atomic.Int32
	lost       bool
	merchant   string
	onTransfer func(context.Context, TransferRequest)
	transform  func(Transfer) Transfer
}

func (r *productSettlementRuntime) ProductCheckoutIdentity(ctx context.Context) (ProductCheckoutIdentity, error) {
	identity, err := r.productCheckoutRuntime.ProductCheckoutIdentity(ctx)
	if r.merchant != "" {
		identity.MerchantID = r.merchant
	}
	return identity, err
}

func (r *productSettlementRuntime) CreateTransfer(ctx context.Context, input TransferRequest) (Transfer, error) {
	r.transfers.Add(1)
	if r.onTransfer != nil {
		r.onTransfer(ctx, input)
	}
	if r.lost {
		return Transfer{}, errors.New("transfer response lost")
	}
	result := Transfer{ProviderID: "tr_" + strings.ReplaceAll(input.PaymentID.String(), "-", ""),
		DestinationID: input.DestinationID, AmountCents: input.AmountCents, Currency: input.Currency,
		TransferGroup: transferGroup(input.PaymentID)}
	if r.transform != nil {
		result = r.transform(result)
	}
	return result, nil
}

func productSettlementFixture(t *testing.T, pool *pgxpool.Pool, runtime *productSettlementRuntime) (*Service, Checkout, jobs.Job, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET hold_days=0;
		UPDATE licenses SET refund_window_days=0`); err != nil {
		t.Fatal(err)
	}
	service, checkout, _, _, _ := fulfilledRefundFixture(t, pool, runtime)
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_charge_id='ch_settlement' WHERE id=$1`, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	var seller, settlement uuid.UUID
	job := jobs.Job{Kind: ProductSettlementJobKind}
	if err := pool.QueryRow(ctx, `SELECT ps.id,ps.seller_id,j.id,j.payload FROM product_settlements ps
		JOIN product_settlement_dispatches d ON d.settlement_id=ps.id JOIN jobs j ON j.id=d.job_id
		WHERE ps.payment_id=$1`, checkout.PaymentID).Scan(&settlement, &seller, &job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	identity, err := runtime.ProductCheckoutIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_destinations(provider,user_id,destination_id,status,charges_enabled,payouts_enabled,details_submitted,
		original_merchant_id,original_store_id,original_live_mode,original_endpoint,original_api_version,original_request_version,verified_at)
		VALUES('stripe',$1,'acct_settlement','verified',true,true,true,$2,$3,$4,$5,$6,$7,now())`, seller, identity.MerchantID, identity.StoreID, identity.LiveMode, identity.Endpoint, identity.APIVersion, identity.RequestVersion); err != nil {
		t.Fatal(err)
	}
	return service, checkout, job, settlement
}

func TestProductSettlementTransferAndReplay(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &productSettlementRuntime{}
	service, _, job, settlement := productSettlementFixture(t, pool, runtime)
	for range 2 {
		if err := service.HandleProductSettlementJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	var count int
	if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM notifications WHERE resource_id=$1 AND kind='marketplace.product_settlement_transferred')
		FROM product_settlements WHERE id=$1`, settlement).Scan(&status, &count); err != nil {
		t.Fatal(err)
	}
	if status != "transferred" || count != 1 || runtime.transfers.Load() != 1 {
		t.Fatalf("status=%s notifications=%d transfers=%d", status, count, runtime.transfers.Load())
	}
}

func TestProductSettlementStoppedJobCannotStart(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	runtime := &productSettlementRuntime{}
	service, _, job, settlement := productSettlementFixture(t, pool, runtime)
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='cancelled' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductSettlementJob(t.Context(), job); err == nil {
		t.Fatal("stopped settlement job started a transfer")
	}
	if runtime.transfers.Load() != 0 {
		t.Fatal("stopped settlement job moved funds")
	}
	var reserved bool
	if err := pool.QueryRow(t.Context(), `SELECT reserved_at IS NOT NULL FROM product_settlement_dispatches WHERE settlement_id=$1`, settlement).Scan(&reserved); err != nil || reserved {
		t.Fatal("stopped settlement job consumed first dispatch", reserved, err)
	}
}

func TestProductSettlementRefundPreservesTransferEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, checkout, job, settlement := productSettlementFixture(t, pool, &productSettlementRuntime{})
	if err := service.HandleProductSettlementJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := markProductSettlementRefundTx(ctx, tx, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var status, transfer string
	var recovery, net int
	if err := pool.QueryRow(ctx, `SELECT status,provider_transfer_id,recovery_amount_cents,net_amount_cents FROM product_settlements WHERE id=$1`, settlement).Scan(&status, &transfer, &recovery, &net); err != nil {
		t.Fatal(err)
	}
	if status != "recovery_required" || transfer != "tr_"+strings.ReplaceAll(checkout.PaymentID.String(), "-", "") || recovery != net {
		t.Fatalf("refund lost transferred funds: status=%s transfer=%s recovery=%d net=%d", status, transfer, recovery, net)
	}
}

func TestProductSettlementUnknownTransferNeverResends(t *testing.T) {
	for _, lost := range []bool{true, false} {
		name := "invalid_group"
		if lost {
			name = "lost_response"
		}
		t.Run(name, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			runtime := &productSettlementRuntime{lost: lost}
			if !lost {
				runtime.transform = func(result Transfer) Transfer { result.TransferGroup = "wrong_group"; return result }
			}
			service, checkout, job, settlement := productSettlementFixture(t, pool, runtime)
			for range 2 {
				if err := service.HandleProductSettlementJob(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
			var status string
			var notifications, batches int
			if err := pool.QueryRow(ctx, `SELECT status,
			 (SELECT count(*) FROM notifications WHERE resource_id=$1),
			 (SELECT count(*) FROM product_payout_batch_items WHERE settlement_id=$1)
			 FROM product_settlements WHERE id=$1`, settlement).Scan(&status, &notifications, &batches); err != nil {
				t.Fatal(err)
			}
			if status != "recovery_required" || notifications != 0 || batches != 1 || runtime.transfers.Load() != 1 {
				t.Fatalf("unknown funds: status=%s notifications=%d batches=%d transfers=%d", status, notifications, batches, runtime.transfers.Load())
			}
			// A later confirmed buyer refund must record the possibly transferred
			// net amount as a recovery obligation, even with no transfer response.
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			for range 2 {
				if err := markProductSettlementRefundTx(ctx, tx, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			var recovery, net, events int
			if err := pool.QueryRow(ctx, `SELECT recovery_amount_cents,net_amount_cents,
			 (SELECT count(*) FROM product_settlement_events WHERE settlement_id=$1 AND event_type='refund.confirmed')
			 FROM product_settlements WHERE id=$1`, settlement).Scan(&recovery, &net, &events); err != nil {
				t.Fatal(err)
			}
			if recovery != net || events != 1 {
				t.Fatalf("recovery=%d net=%d events=%d", recovery, net, events)
			}
		})
	}
}

func TestProductSettlementCommittedReservationSurvivesLocalFailure(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &productSettlementRuntime{}
	service, _, job, settlement := productSettlementFixture(t, pool, runtime)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_settlement_notification() RETURNS trigger AS $$
	 BEGIN RAISE EXCEPTION 'injected notification failure'; END; $$ LANGUAGE plpgsql;
	 CREATE TRIGGER fail_settlement_notification BEFORE INSERT ON notifications
	 FOR EACH ROW EXECUTE FUNCTION fail_settlement_notification()`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductSettlementJob(ctx, job); err == nil {
		t.Fatal("expected local completion failure")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_settlement_notification ON notifications`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductSettlementJob(ctx, job); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Fatalf("retry=%v", err)
	}
	var status string
	var reserved bool
	if err := pool.QueryRow(ctx, `SELECT ps.status,d.reserved_at IS NOT NULL FROM product_settlements ps
	 JOIN product_settlement_dispatches d ON d.settlement_id=ps.id WHERE ps.id=$1`, settlement).Scan(&status, &reserved); err != nil {
		t.Fatal(err)
	}
	if status != "transfer_pending" || !reserved || runtime.transfers.Load() != 1 {
		t.Fatalf("status=%s reserved=%t transfers=%d", status, reserved, runtime.transfers.Load())
	}
}

func TestProductSettlementCancelledContextPreservesReceivedTransfer(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := &productSettlementRuntime{onTransfer: func(context.Context, TransferRequest) { cancel() }}
	service, _, job, settlement := productSettlementFixture(t, pool, runtime)
	if err := service.HandleProductSettlementJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM product_settlements WHERE id=$1`, settlement).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "transferred" {
		t.Fatalf("status=%s", status)
	}
}

func TestProductSettlementConcurrentDispatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}, 2), make(chan struct{})
	runtime := &productSettlementRuntime{onTransfer: func(ctx context.Context, _ TransferRequest) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
	}}
	service, _, job, _ := productSettlementFixture(t, pool, runtime)
	results := make(chan error, 2)
	go func() { results <- service.HandleProductSettlementJob(ctx, job) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first transfer did not start")
	}
	go func() { results <- service.HandleProductSettlementJob(ctx, job) }()
	close(release)
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("concurrent dispatch stuck")
		}
	}
	if runtime.transfers.Load() != 1 {
		t.Fatalf("transfers=%d", runtime.transfers.Load())
	}
}

func TestProductSettlementSnapshotAndEvidenceImmutable(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, checkout, job, settlement := productSettlementFixture(t, pool, &productSettlementRuntime{})
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET platform_fee_bps=3000,hold_days=30`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := service.ensureProductSettlementTx(ctx, tx, checkout.OrderID, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var fee int
	var due bool
	if err := pool.QueryRow(ctx, `SELECT fee_bps,available_at<=clock_timestamp() FROM product_settlements WHERE id=$1`, settlement).Scan(&fee, &due); err != nil {
		t.Fatal(err)
	}
	if fee != 0 || !due {
		t.Fatalf("replay changed economic snapshot: fee=%d due=%t", fee, due)
	}
	if err := service.HandleProductSettlementJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE product_settlements SET fee_bps=1 WHERE id=$1`,
		`UPDATE product_settlements SET available_at=now()+interval '1 day' WHERE id=$1`,
		`UPDATE product_settlements SET provider_transfer_id='tr_other' WHERE id=$1`,
		`UPDATE product_settlements SET status='pending_hold',provider_transfer_id=NULL,transferred_at=NULL WHERE id=$1`,
		`UPDATE product_settlements SET destination_id='acct_other' WHERE id=$1`,
		`DELETE FROM product_settlements WHERE id=$1`,
		`UPDATE product_settlement_dispatches SET reserved_at=NULL WHERE settlement_id=$1`,
		`DELETE FROM product_settlement_dispatches WHERE settlement_id=$1`,
		`UPDATE product_settlement_events SET evidence='{}' WHERE settlement_id=$1`,
		`DELETE FROM product_settlement_events WHERE settlement_id=$1`,
	} {
		if _, err := pool.Exec(ctx, sql, settlement); err == nil {
			t.Fatalf("allowed evidence mutation: %s", sql)
		}
	}
}

func TestProductSettlementZeroNetDoesNotTransfer(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET platform_fee_bps=10000`); err != nil {
		t.Fatal(err)
	}
	runtime := &productSettlementRuntime{}
	service, _, job, settlement := productSettlementFixture(t, pool, runtime)
	if err := service.HandleProductSettlementJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := pool.QueryRow(ctx, `SELECT status,hold_reason FROM product_settlements WHERE id=$1`, settlement).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" || reason != "no_seller_amount" || runtime.transfers.Load() != 0 {
		t.Fatalf("status=%s reason=%s transfers=%d", status, reason, runtime.transfers.Load())
	}
}

func TestProductSettlementSchedulesFrozenHold(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	_, checkout, _, _, _ := fulfilledRefundFixture(t, pool, &productSettlementRuntime{})
	var equal, future bool
	if err := pool.QueryRow(context.Background(), `SELECT ps.available_at=j.available_at,j.available_at>clock_timestamp()
	 FROM product_settlements ps JOIN product_settlement_dispatches d ON d.settlement_id=ps.id JOIN jobs j ON j.id=d.job_id
	 WHERE ps.payment_id=$1`, checkout.PaymentID).Scan(&equal, &future); err != nil {
		t.Fatal(err)
	}
	if !equal || !future {
		t.Fatalf("hold schedule: equal=%t future=%t", equal, future)
	}
}

func TestProductSettlementPendingRefundCanResumeAfterFailure(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &productSettlementRuntime{}
	service, checkout, job, settlement := productSettlementFixture(t, pool, runtime)
	if _, err := pool.Exec(ctx, `UPDATE orders SET status='refund_requested' WHERE id=$1`, checkout.OrderID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductSettlementJob(ctx, job); err == nil {
		t.Fatal("pending refund permanently completed settlement job")
	}
	if runtime.transfers.Load() != 0 {
		t.Fatal("transferred during pending refund")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM product_settlements WHERE id=$1`, settlement).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending_hold" {
		t.Fatalf("pending refund consumed payable: %s", status)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET status='fulfilled' WHERE id=$1`, checkout.OrderID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductSettlementJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if runtime.transfers.Load() != 1 {
		t.Fatal("confirmed refund failure prevented settlement")
	}
}

func TestProductSettlementIntegrityMigration(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	applyTransferExecutionMigration(t, pool, "down")
	checkDown, err := os.ReadFile("../platform/database/migrations/0145_product_settlement_checks.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(checkDown)); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0144_product_settlement_integrity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0144_product_settlement_integrity.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	runtime := &productSettlementRuntime{}
	service, _, job, settlement := productSettlementFixture(t, pool, runtime)
	var batch uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO product_payout_batches(provider,live_mode,status,started_at)
	 VALUES('stripe',false,'dispatching',now()) RETURNING id`).Scan(&batch); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE product_settlements SET status='transfer_pending',payout_batch_id=$2,destination_id='acct_settlement' WHERE id=$1`, settlement, batch); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_settlement_events(settlement_id,event_type,to_status)
	 VALUES($1,'transfer.requested','transfer_pending')`, settlement); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	applyTransferExecutionMigration(t, pool, "up")
	var reserved, keyed bool
	if err := pool.QueryRow(ctx, `SELECT reserved_at IS NOT NULL,
	 EXISTS(SELECT 1 FROM product_settlement_events WHERE settlement_id=$1 AND event_key LIKE 'legacy:%')
	 FROM product_settlement_dispatches WHERE settlement_id=$1`, settlement).Scan(&reserved, &keyed); err != nil {
		t.Fatal(err)
	}
	if !reserved || !keyed {
		t.Fatalf("legacy evidence: reserved=%t keyed=%t", reserved, keyed)
	}
	if err := service.HandleProductSettlementJob(ctx, job); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Fatalf("legacy dispatch retry: %v", err)
	}
	if runtime.transfers.Load() != 0 {
		t.Fatal("legacy dispatch sent again")
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatalf("downgrade=%v", err)
	}
}

func TestProductSettlementMerchantChangeDoesNotTransfer(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	runtime := &productSettlementRuntime{}
	service, _, job, _ := productSettlementFixture(t, pool, runtime)
	runtime.merchant = "acct_different"
	if err := service.HandleProductSettlementJob(context.Background(), job); err == nil {
		t.Fatal("changed merchant accepted")
	}
	if runtime.transfers.Load() != 0 {
		t.Fatal("transferred through another merchant")
	}
}

func TestProductSettlementFundsRequireCrossObjectBinding(t *testing.T) {
	for _, field := range []string{"payer_id", "resource_id"} {
		t.Run(field, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			_, checkout, _, settlement := productSettlementFixture(t, pool, &productSettlementRuntime{})
			if field == "payer_id" {
				other := uuid.New()
				handle := "settlement_other_" + strings.ReplaceAll(other.String(), "-", "")[:12]
				if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status)
 VALUES($1,$2,$3,'Other Buyer','member','active')`, other, handle+"@test.local", handle); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET payer_id=$2 WHERE id=$1`, checkout.PaymentID, other); err != nil {
					t.Fatal(err)
				}
			} else if _, err := pool.Exec(ctx, `UPDATE payment_intents SET resource_id=$2 WHERE id=$1`, checkout.PaymentID, uuid.New()); err != nil {
				t.Fatal(err)
			}
			var snapshot ProductSettlement
			if err := pool.QueryRow(ctx, `SELECT id,payment_id,provider,live_mode,gross_amount_cents,currency
 FROM product_settlements WHERE id=$1`, settlement).Scan(&snapshot.ID, &snapshot.PaymentID, &snapshot.Provider,
				&snapshot.LiveMode, &snapshot.GrossAmountCents, &snapshot.Currency); err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if err := validateProductSettlementFundsTx(ctx, tx, snapshot); !errors.Is(err, ErrCheckoutReconciliation) {
				t.Fatalf("accepted cross-object %s binding: %v", field, err)
			}
		})
	}
}

func TestProductSettlementRecoveryRequiresCrossObjectBinding(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &productSettlementRuntime{lost: true}
	service, checkout, job, settlement := productSettlementFixture(t, pool, runtime)
	if err := service.HandleProductSettlementJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	handle := "settlement_recovery_" + strings.ReplaceAll(other.String(), "-", "")[:12]
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status)
 VALUES($1,$2,$3,'Other Buyer','member','active')`, other, handle+"@test.local", handle); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET payer_id=$2 WHERE id=$1`, checkout.PaymentID, other); err != nil {
		t.Fatal(err)
	}
	var item TransferObservation
	if err := pool.QueryRow(ctx, `SELECT ps.destination_id,pi.provider_charge_id,ps.net_amount_cents,ps.currency,ps.live_mode
 FROM product_settlements ps JOIN payment_intents pi ON pi.id=ps.payment_id WHERE ps.id=$1`, settlement).
		Scan(&item.DestinationID, &item.ProviderChargeID, &item.AmountCents, &item.Currency, &item.LiveMode); err != nil {
		t.Fatal(err)
	}
	item.ProviderID = "tr_recovery_binding"
	item.PaymentID = checkout.PaymentID
	item.TransferGroup = transferGroup(checkout.PaymentID)
	item.CreatedAt = time.Now().UTC()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := recordRecoveredProductTransferTx(ctx, tx, settlement, uuid.New(), item); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Fatalf("accepted recovery with mismatched payer: %v", err)
	}
}
