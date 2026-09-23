package payments

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type settlementReadRuntime struct {
	*productSettlementRuntime
	reads  atomic.Int32
	lookup func(context.Context, TransferLookupRequest) (TransferLookupResult, error)
}

func (r *settlementReadRuntime) LookupProductTransfer(ctx context.Context, input TransferLookupRequest) (TransferLookupResult, error) {
	r.reads.Add(1)
	if r.lookup != nil {
		return r.lookup(ctx, input)
	}
	return observedSettlementTransfer(input), nil
}

func observedSettlementTransfer(input TransferLookupRequest) TransferLookupResult {
	return TransferLookupResult{Outcome: "found", Pages: 1, Observations: []TransferObservation{{
		Transfer: Transfer{ProviderID: "tr_" + strings.ReplaceAll(input.PaymentID.String(), "-", ""),
			DestinationID: input.DestinationID, AmountCents: input.AmountCents, Currency: input.Currency,
			TransferGroup: transferGroup(input.PaymentID)},
		PaymentID: input.PaymentID, ProviderChargeID: input.ProviderChargeID, LiveMode: input.LiveMode,
		CreatedAt: time.Now().UTC().Add(-time.Second).Truncate(time.Second),
	}}}
}

func settlementCheckFixture(t *testing.T) (*pgxpool.Pool, *Service, *settlementReadRuntime, Checkout, jobs.Job, uuid.UUID) {
	t.Helper()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	runtime := &settlementReadRuntime{productSettlementRuntime: &productSettlementRuntime{lost: true}}
	service, checkout, job, settlement := productSettlementFixture(t, pool, runtime.productSettlementRuntime)
	service.runtimes = NewRuntimeCatalog(runtime)
	if err := service.HandleProductSettlementJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	return pool, service, runtime, checkout, job, settlement
}

func scheduleSettlementCheck(t *testing.T, service *Service, settlement uuid.UUID) jobs.Job {
	t.Helper()
	ctx := context.Background()
	var due time.Time
	if err := service.pool.QueryRow(ctx, `SELECT due_at FROM product_settlement_check_candidates WHERE settlement_id=$1`, settlement).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileProductSettlements(ctx, 100, due.Add(-time.Microsecond)); err != nil || n != 0 {
		t.Fatalf("premature check: %d %v", n, err)
	}
	if n, err := service.reconcileProductSettlements(ctx, 100, due); err != nil || n != 1 {
		t.Fatalf("schedule check: %d %v", n, err)
	}
	job := jobs.Job{Kind: ProductSettlementCheckJobKind}
	if err := service.pool.QueryRow(ctx, `SELECT j.id,j.payload FROM product_settlement_checks c JOIN jobs j ON j.id=c.job_id
 WHERE c.settlement_id=$1 ORDER BY c.created_at DESC,c.id DESC LIMIT 1`, settlement).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return job
}

func finishSettlementCheckJob(t *testing.T, pool *pgxpool.Pool, job jobs.Job, status string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE jobs SET status=$2,updated_at=clock_timestamp() WHERE id=$1`, job.ID, status); err != nil {
		t.Fatal(err)
	}
}

func TestProductSettlementCheckRecoveryAndReplay(t *testing.T) {
	pool, service, runtime, checkout, transferJob, settlement := settlementCheckFixture(t)
	ctx := context.Background()
	job := scheduleSettlementCheck(t, service, settlement)
	// Recovery uses the original provider even after new sales are disabled.
	service.config.Enabled = false
	if n, err := service.reconcileProductSettlements(ctx, 100, time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("queued check duplicated: %d %v", n, err)
	}
	for range 2 {
		if err := service.HandleProductSettlementCheckJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	finishSettlementCheckJob(t, pool, job, "succeeded")
	var status, transfer, batchStatus, itemStatus, outcome string
	var notifications, events int
	if err := pool.QueryRow(ctx, `SELECT ps.status,ps.provider_transfer_id,b.status,i.status,c.outcome,
 (SELECT count(*) FROM notifications WHERE resource_id=ps.id AND kind='marketplace.product_settlement_transferred'),
 (SELECT count(*) FROM product_settlement_events WHERE settlement_id=ps.id AND event_type='transfer.recovered')
 FROM product_settlements ps JOIN product_payout_batches b ON b.id=ps.payout_batch_id
 JOIN product_payout_batch_items i ON i.batch_id=b.id AND i.settlement_id=ps.id
 JOIN product_settlement_checks c ON c.settlement_id=ps.id WHERE ps.id=$1`, settlement).
		Scan(&status, &transfer, &batchStatus, &itemStatus, &outcome, &notifications, &events); err != nil {
		t.Fatal(err)
	}
	if status != "transferred" || batchStatus != "completed" || itemStatus != "succeeded" || outcome != "found" ||
		transfer != "tr_"+strings.ReplaceAll(checkout.PaymentID.String(), "-", "") || notifications != 1 || events != 1 || runtime.reads.Load() != 1 {
		t.Fatalf("incomplete recovery: %s %s %s %s %s notifications=%d events=%d reads=%d", status, transfer, batchStatus, itemStatus, outcome, notifications, events, runtime.reads.Load())
	}
	service.config.Enabled = true
	if err := service.HandleProductSettlementJob(ctx, transferJob); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileProductSettlements(ctx, 100, time.Now().Add(time.Hour)); err != nil || n != 0 || runtime.transfers.Load() != 1 {
		t.Fatalf("recovered transfer reissued: %d %v transfers=%d", n, err, runtime.transfers.Load())
	}
}

func TestProductSettlementCheckUncertainResultsDoNotPay(t *testing.T) {
	for _, scenario := range []string{"not_found", "ambiguous", "incomplete", "error", "partial_reversal", "full_reversal", "wrong_payment", "wrong_amount", "wrong_destination", "duplicate", "invalid_pages", "merchant_changed"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, _, transferJob, settlement := settlementCheckFixture(t)
			ctx := context.Background()
			job := scheduleSettlementCheck(t, service, settlement)
			want := "error"
			switch scenario {
			case "not_found", "ambiguous", "incomplete":
				want = scenario
			case "partial_reversal", "full_reversal":
				want = "reversed"
			case "merchant_changed":
				runtime.merchant = "acct_other"
			}
			runtime.lookup = func(_ context.Context, input TransferLookupRequest) (TransferLookupResult, error) {
				result := observedSettlementTransfer(input)
				switch scenario {
				case "not_found":
					result.Outcome, result.Observations = "not_found", []TransferObservation{}
				case "ambiguous", "duplicate":
					result.Outcome = "ambiguous"
					second := result.Observations[0]
					if scenario == "ambiguous" {
						second.ProviderID = "tr_second"
					}
					result.Observations = append(result.Observations, second)
				case "incomplete":
					result.Outcome, result.Pages = "incomplete", 10
				case "error":
					return result, errors.New("private transport failure")
				case "partial_reversal":
					result.Observations[0].AmountReversed = 1
				case "full_reversal":
					result.Observations[0].AmountReversed = input.AmountCents
				case "wrong_payment":
					result.Observations[0].PaymentID = uuid.New()
				case "wrong_amount":
					result.Observations[0].AmountCents++
				case "wrong_destination":
					result.Observations[0].DestinationID = "acct_other"
				case "invalid_pages":
					result.Pages = 0
				}
				return result, nil
			}
			if err := service.HandleProductSettlementCheckJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			finishSettlementCheckJob(t, pool, job, "succeeded")
			var status, outcome, code string
			var hasTransfer bool
			var notifications int
			if err := pool.QueryRow(ctx, `SELECT ps.status,ps.provider_transfer_id IS NOT NULL,c.outcome,c.error_code,
 (SELECT count(*) FROM notifications WHERE resource_id=ps.id) FROM product_settlements ps
 JOIN product_settlement_checks c ON c.settlement_id=ps.id WHERE c.job_id=$1`, job.ID).Scan(&status, &hasTransfer, &outcome, &code, &notifications); err != nil {
				t.Fatal(err)
			}
			if status != "recovery_required" || hasTransfer || outcome != want || notifications != 0 || strings.Contains(code, "private") {
				t.Fatalf("unsafe recovery: %s transfer=%t outcome=%s code=%s notifications=%d", status, hasTransfer, outcome, code, notifications)
			}
			if err := service.HandleProductSettlementJob(ctx, transferJob); err != nil || runtime.transfers.Load() != 1 {
				t.Fatalf("uncertain transfer retried: %v calls=%d", err, runtime.transfers.Load())
			}
			if scenario == "merchant_changed" && runtime.reads.Load() != 0 {
				t.Fatal("queried another merchant")
			}
			var delay float64
			if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM (v.due_at-c.finished_at))
 FROM product_settlement_check_candidates v JOIN product_settlement_checks c ON c.settlement_id=v.settlement_id WHERE c.job_id=$1`, job.ID).Scan(&delay); err != nil || delay != 300 {
				t.Fatalf("read retry delay=%v err=%v", delay, err)
			}
		})
	}
}

func TestProductSettlementCheckPersistenceAndRefund(t *testing.T) {
	for _, scenario := range []string{"cancelled_context", "local_failure", "refund_before", "refund_during", "pending_refund", "payment_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, checkout, _, settlement := settlementCheckFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			job := scheduleSettlementCheck(t, service, settlement)
			markRefund := func() error {
				tx, err := pool.Begin(ctx)
				if err != nil {
					return err
				}
				defer tx.Rollback(context.Background())
				if err := lockProductSettlementPaymentTx(ctx, tx, settlement); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='refunded' WHERE id=$1`, checkout.PaymentID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE orders SET status='refunded' WHERE id=$1`, checkout.OrderID); err != nil {
					return err
				}
				if err := markProductSettlementRefundTx(ctx, tx, checkout.PaymentID); err != nil {
					return err
				}
				return tx.Commit(ctx)
			}
			refundDone := make(chan error, 1)
			switch scenario {
			case "cancelled_context":
				runtime.lookup = func(_ context.Context, input TransferLookupRequest) (TransferLookupResult, error) {
					cancel()
					return observedSettlementTransfer(input), nil
				}
			case "local_failure":
				if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_check_audit() RETURNS trigger AS $$ BEGIN
 IF NEW.action='marketplace.product_settlement_checked' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql;
 CREATE TRIGGER reject_check_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_check_audit()`); err != nil {
					t.Fatal(err)
				}
			case "refund_before":
				if err := markRefund(); err != nil {
					t.Fatal(err)
				}
			case "refund_during":
				runtime.lookup = func(_ context.Context, input TransferLookupRequest) (TransferLookupResult, error) {
					go func() { refundDone <- markRefund() }()
					return observedSettlementTransfer(input), nil
				}
			case "pending_refund":
				if _, err := pool.Exec(ctx, `UPDATE orders SET status='refund_requested' WHERE id=$1`, checkout.OrderID); err != nil {
					t.Fatal(err)
				}
			case "payment_conflict":
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='payment_failed' WHERE id=$1`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
			}
			err := service.HandleProductSettlementCheckJob(ctx, job)
			if scenario == "local_failure" {
				if err == nil {
					t.Fatal("expected local save failure")
				}
				var rolledBack bool
				if err := pool.QueryRow(ctx, `SELECT ps.provider_transfer_id IS NULL AND c.finished_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM notifications WHERE resource_id=ps.id)
 FROM product_settlements ps JOIN product_settlement_checks c ON c.settlement_id=ps.id WHERE c.job_id=$1`, job.ID).Scan(&rolledBack); err != nil || !rolledBack {
					t.Fatalf("partial commit: %t %v", rolledBack, err)
				}
				if _, err := pool.Exec(ctx, `DROP TRIGGER reject_check_audit ON audit_events`); err != nil {
					t.Fatal(err)
				}
				err = service.HandleProductSettlementCheckJob(ctx, job)
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "refund_during" {
				select {
				case err := <-refundDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("refund blocked")
				}
			}
			var status string
			var debt, net, notifications int
			var proof bool
			if err := pool.QueryRow(context.Background(), `SELECT ps.status,ps.recovery_amount_cents,ps.net_amount_cents,
 ps.provider_transfer_id IS NOT NULL AND ps.transferred_at IS NOT NULL,
 (SELECT count(*) FROM notifications WHERE resource_id=ps.id) FROM product_settlements ps WHERE ps.id=$1`, settlement).Scan(&status, &debt, &net, &proof, &notifications); err != nil {
				t.Fatal(err)
			}
			wantStatus, wantDebt, wantNotifications, wantReads := "transferred", 0, 1, int32(1)
			if strings.HasPrefix(scenario, "refund_") {
				wantStatus, wantDebt = "recovery_required", net
			}
			if scenario == "payment_conflict" {
				wantStatus = "recovery_required"
			}
			if scenario == "refund_before" || scenario == "payment_conflict" {
				wantNotifications = 0
			}
			if scenario == "local_failure" {
				wantReads = 2
			}
			if status != wantStatus || debt != wantDebt || !proof || notifications != wantNotifications || runtime.reads.Load() != wantReads || runtime.transfers.Load() != 1 {
				t.Fatalf("recovery=%s debt=%d/%d proof=%t notifications=%d reads=%d transfers=%d", status, debt, net, proof, notifications, runtime.reads.Load(), runtime.transfers.Load())
			}
		})
	}
}

func TestProductSettlementCheckConcurrencyAndJobBinding(t *testing.T) {
	pool, service, runtime, _, _, settlement := settlementCheckFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 6 {
		other := newPaymentTestService(t, pool, service.config, NewRuntimeCatalog(runtime))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := other.reconcileProductSettlements(ctx, 100, time.Now().Add(time.Minute)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_settlement_checks WHERE settlement_id=$1`, settlement).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate checks: %d %v", count, err)
	}
	job := jobs.Job{Kind: ProductSettlementCheckJobKind}
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM product_settlement_checks c JOIN jobs j ON j.id=c.job_id WHERE c.settlement_id=$1`, settlement).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	spoof := job
	spoof.ID = uuid.New()
	if err := service.HandleProductSettlementCheckJob(ctx, spoof); err == nil || runtime.reads.Load() != 0 {
		t.Fatal("unbound check accepted")
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := service.HandleProductSettlementCheckJob(ctx, job); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if runtime.reads.Load() != 1 || runtime.transfers.Load() != 1 {
		t.Fatalf("concurrent replay: reads=%d transfers=%d", runtime.reads.Load(), runtime.transfers.Load())
	}
}

func TestProductSettlementCheckMigrationAndFailedJob(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	down, err := os.ReadFile("../platform/database/migrations/0145_product_settlement_checks.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0145_product_settlement_checks.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range [][]byte{down, up} {
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &settlementReadRuntime{productSettlementRuntime: &productSettlementRuntime{lost: true}}
	service, _, transferJob, settlement := productSettlementFixture(t, pool, runtime.productSettlementRuntime)
	service.runtimes = NewRuntimeCatalog(runtime)
	if err := service.HandleProductSettlementJob(ctx, transferJob); err != nil {
		t.Fatal(err)
	}
	first := scheduleSettlementCheck(t, service, settlement)
	finishSettlementCheckJob(t, pool, first, "failed")
	second := scheduleSettlementCheck(t, service, settlement)
	if first.ID == second.ID {
		t.Fatal("failed job reset instead of preserving evidence")
	}
	if err := service.HandleProductSettlementCheckJob(ctx, second); err != nil {
		t.Fatal(err)
	}
	var failed, count int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE j.status='failed') FROM product_settlement_checks c JOIN jobs j ON j.id=c.job_id WHERE c.settlement_id=$1`, settlement).Scan(&count, &failed); err != nil || count != 2 || failed != 1 {
		t.Fatalf("history lost: %d/%d %v", count, failed, err)
	}
	var payload productSettlementCheckPayload
	if err := json.Unmarshal(second.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE product_settlement_checks SET evidence='{}' WHERE id=$1`,
		`UPDATE product_settlement_checks SET job_id=gen_random_uuid() WHERE id=$1`,
		`UPDATE product_settlement_checks SET finished_at=NULL,outcome=NULL WHERE id=$1`,
		`DELETE FROM product_settlement_checks WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, sql, payload.CheckID); err == nil {
			t.Fatalf("evidence mutation permitted: %s", sql)
		}
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard") {
		t.Fatalf("destructive downgrade accepted: %v", err)
	}
}

func TestProductSettlementCheckRequiresOriginalBatch(t *testing.T) {
	for _, tc := range productPayoutBatchConflicts {
		t.Run(tc.name, func(t *testing.T) {
			pool, service, _, _, _, settlement := settlementCheckFixture(t)
			ctx := context.Background()
			job := scheduleSettlementCheck(t, service, settlement)
			if _, err := pool.Exec(ctx, tc.sql, settlement); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleProductSettlementCheckJob(ctx, job); err == nil {
				t.Fatal("inconsistent payout batch marked successful")
			}
			var safe bool
			if err := pool.QueryRow(ctx, `SELECT ps.status='recovery_required' AND ps.provider_transfer_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM notifications WHERE resource_id=ps.id) FROM product_settlements ps WHERE ps.id=$1`, settlement).Scan(&safe); err != nil || !safe {
				t.Fatalf("unsafe payout projection: %t %v", safe, err)
			}
		})
	}
}
