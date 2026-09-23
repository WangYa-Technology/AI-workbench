package payments

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sellerFundingFixture(t *testing.T) (*pgxpool.Pool, *Service, *settlementReadRuntime, Checkout, SellerPayoutRequest, jobs.Job) {
	t.Helper()
	ctx := context.Background()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	service, checkout, settlement, request := sellerTransferFixture(t, pool)
	runtime := &settlementReadRuntime{productSettlementRuntime: &productSettlementRuntime{}}
	service.runtimes = NewRuntimeCatalog(runtime)
	actor, input := approveSellerFundingFixture(t, pool, service, request)
	if input.SettlementID != settlement {
		t.Fatal("fixture settlement changed")
	}
	admitted, err := service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "source-fixture-admission", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	job := jobs.Job{ID: admitted.JobID, Kind: SellerPayoutFundingJobKind}
	if again, err := service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "source-fixture-admission", "retry"); err != nil || again.JobID != job.ID {
		t.Fatalf("duplicate admission: %v %v", again, err)
	}
	if err := pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, job.ID).Scan(&job.Payload); err != nil {
		t.Fatal(err)
	}
	return pool, service, runtime, checkout, request, job
}

func assertSellerFunding(t *testing.T, pool *pgxpool.Pool, request SellerPayoutRequest, transferStatus, requestStatus string) {
	t.Helper()
	var transfer, parent string
	if err := pool.QueryRow(context.Background(), `SELECT t.status,r.status FROM seller_payout_transfers t
 JOIN seller_payout_requests r ON r.id=t.payout_request_id WHERE r.id=$1`, request.ID).Scan(&transfer, &parent); err != nil {
		t.Fatal(err)
	}
	if transfer != transferStatus || parent != requestStatus {
		t.Fatalf("funding=%s request=%s; want %s/%s", transfer, parent, transferStatus, requestStatus)
	}
}

func TestSellerFundingSuccessIsNotBankPayout(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	ctx := context.Background()
	for range 2 {
		if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	assertSellerFunding(t, pool, request, "succeeded", "processing")
	var actor uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT actor_id FROM seller_payout_funding_admissions WHERE payout_request_id=$1`, request.ID).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	detail, err := service.GetSellerPayoutReview(ctx, actor, request.ID)
	if err != nil || detail.CanAdmitFunding || detail.Status != "processing" || detail.Funding == nil ||
		detail.Funding.Status != "succeeded" || detail.Funding.StartedAt == nil || detail.Funding.ProviderTransferID == nil ||
		!strings.HasPrefix(*detail.Funding.ProviderTransferID, "tr_") || detail.Funding.JobID == nil || *detail.Funding.JobID != job.ID {
		t.Fatalf("finance projection confused funding and bank payout: %+v %v", detail, err)
	}
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 0 {
		t.Fatalf("calls: transfers=%d reads=%d", runtime.transfers.Load(), runtime.reads.Load())
	}
	balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.WithdrawableCents != 0 || balance.ReservedCents != request.AmountCents {
		t.Fatalf("source funding released reservation: %+v %v", balance, err)
	}
	_, err = pool.Exec(ctx, `UPDATE seller_payout_requests SET status='succeeded' WHERE id=$1`, request.ID)
	requirePayoutConstraint(t, err)
	if _, err := service.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID); !errors.Is(err, ErrSellerPayoutNotCancellable) {
		t.Fatalf("funded payout cancelled: %v", err)
	}
}

func TestSellerFundingStoppedJobCannotStart(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='cancelled' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err == nil {
		t.Fatal("stopped source job started a transfer")
	}
	if runtime.transfers.Load() != 0 {
		t.Fatal("stopped source job moved funds")
	}
	assertSellerFunding(t, pool, request, "requested", "under_review")
}

func TestSellerFundingUnknownReadOnlyRecovery(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	ctx := context.Background()
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("unknown result was completed")
	}
	assertSellerFunding(t, pool, request, "reconciliation_required", "reconciliation_required")
	runtime.lookup = func(context.Context, TransferLookupRequest) (TransferLookupResult, error) {
		return TransferLookupResult{Outcome: "not_found", Pages: 1, Observations: []TransferObservation{}}, nil
	}
	// Recovery remains possible after disabling new financial writes.
	service.config.Enabled = false
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("empty query completed funding")
	}
	runtime.lookup = nil
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertSellerFunding(t, pool, request, "succeeded", "reconciliation_required")
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 2 {
		t.Fatalf("recovery resent funds: transfers=%d reads=%d", runtime.transfers.Load(), runtime.reads.Load())
	}
	var reads int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_payout_funding_reads WHERE finished_at IS NOT NULL`).Scan(&reads); err != nil || reads != 2 {
		t.Fatalf("read history=%d %v", reads, err)
	}
	for _, sql := range []string{
		`DELETE FROM seller_payout_funding_reads`,
		`UPDATE seller_payout_funding_reads SET evidence='{}'`,
		`UPDATE seller_payout_funding_dispatches SET started_at=NULL`,
		`UPDATE seller_payout_funding_dispatches SET provider_charge_id='ch_other'`,
		`DELETE FROM seller_payout_funding_dispatches`,
	} {
		_, err := pool.Exec(ctx, sql)
		requirePayoutConstraint(t, err)
	}
}

func TestSellerFundingCrashAfterClaimNeverResends(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_funding_dispatches SET started_at=clock_timestamp();
 UPDATE seller_payout_transfers SET status='processing'; UPDATE seller_payout_requests SET status='processing'`); err != nil {
		t.Fatal(err)
	}
	runtime.lookup = func(context.Context, TransferLookupRequest) (TransferLookupResult, error) {
		return TransferLookupResult{Outcome: "not_found", Pages: 1, Observations: []TransferObservation{}}, nil
	}
	for range 2 {
		if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
			t.Fatal("crash gap completed funding")
		}
	}
	if runtime.transfers.Load() != 0 || runtime.reads.Load() != 2 {
		t.Fatalf("crash replay dispatched: transfers=%d reads=%d", runtime.transfers.Load(), runtime.reads.Load())
	}
	assertSellerFunding(t, pool, request, "reconciliation_required", "reconciliation_required")
}

func TestSellerFundingReadEvidenceCannotBeForgotten(t *testing.T) {
	for _, scenario := range []string{"partial_different", "reversed", "ambiguous", "old_transfer"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, _, request, job := sellerFundingFixture(t)
			ctx := context.Background()
			runtime.lost = true
			if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
				t.Fatal("expected lost response")
			}
			runtime.lookup = func(_ context.Context, input TransferLookupRequest) (TransferLookupResult, error) {
				result := observedSettlementTransfer(input)
				switch scenario {
				case "partial_different":
					result.Observations[0].ProviderID = "tr_prior_evidence"
					result.Outcome = ""
					return result, errors.New("later page timed out")
				case "reversed":
					result.Observations[0].AmountReversed = 1
				case "ambiguous":
					other := result.Observations[0]
					other.ProviderID = "tr_second_evidence"
					result.Observations = append(result.Observations, other)
					result.Outcome = "ambiguous"
				case "old_transfer":
					result.Observations[0].CreatedAt = time.Now().Add(-24 * time.Hour)
				}
				return result, nil
			}
			if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
				t.Fatal("partial/reversed/ambiguous evidence was completed")
			}
			runtime.lookup = nil
			if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
				t.Fatal("later read erased prior conflict")
			}
			assertSellerFunding(t, pool, request, "reconciliation_required", "reconciliation_required")
			var observed int
			if err := pool.QueryRow(ctx, `SELECT sum(jsonb_array_length(evidence->'observations')) FROM seller_payout_funding_reads`).Scan(&observed); err != nil || observed < 2 {
				t.Fatalf("lost evidence: %d %v", observed, err)
			}
			if runtime.transfers.Load() != 1 {
				t.Fatal("conflicting evidence caused another transfer")
			}
		})
	}
}

func TestSellerFundingRefusesChangedAuthorityAndBindings(t *testing.T) {
	for _, scenario := range []string{"merchant", "charge", "destination", "suspended", "mode", "job"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, checkout, request, job := sellerFundingFixture(t)
			ctx := context.Background()
			var err error
			switch scenario {
			case "merchant":
				runtime.merchant = "acct_other_merchant"
			case "charge":
				_, err = pool.Exec(ctx, `UPDATE payment_intents SET provider_charge_id='ch_changed' WHERE id=$1`, checkout.PaymentID)
			case "destination":
				_, err = pool.Exec(ctx, `UPDATE payment_destinations SET status='restricted',payouts_enabled=false WHERE user_id=$1`, request.SellerID)
			case "suspended":
				_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, request.SellerID)
			case "mode":
				_, err = pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='automatic'`)
			case "job":
				job.ID = uuid.New()
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
				t.Fatal("invalid funding command succeeded")
			}
			if runtime.transfers.Load() != 0 || runtime.reads.Load() != 0 {
				t.Fatal("invalid context reached provider")
			}
		})
	}
}

func TestSellerFundingRefundSerializesWithTransfer(t *testing.T) {
	pool, service, runtime, checkout, request, job := sellerFundingFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	runtime.onTransfer = func(ctx context.Context, _ TransferRequest) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	done := make(chan error, 1)
	go func() { done <- service.HandleSellerPayoutFundingJob(ctx, job) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("transfer never entered")
	}
	refund := make(chan error, 1)
	go func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			refund <- err
			return
		}
		defer tx.Rollback(context.Background())
		var settlement uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM product_settlements WHERE payment_id=$1`, checkout.PaymentID).Scan(&settlement)
		if err == nil {
			err = lockProductSettlementPaymentTx(ctx, tx, settlement)
		}
		if err == nil {
			err = markProductSettlementRefundTx(ctx, tx, checkout.PaymentID)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		refund <- err
	}()
	// Observe the actual PostgreSQL wait, rather than assuming the goroutine ran.
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE query LIKE '%FOR UPDATE OF pi,o%' AND wait_event_type='Lock' AND pid<>pg_backend_pid())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("refund did not wait for transfer")
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-refund; err != nil {
		t.Fatal(err)
	}
	balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.RecoveryDueCents != request.AmountCents || balance.WithdrawableCents != 0 {
		t.Fatalf("refund lost recovery: %+v %v", balance, err)
	}
	if runtime.transfers.Load() != 1 {
		t.Fatal("refund caused duplicate funding")
	}
}

func TestSellerFundingMigrationRoundTripAndEvidenceGuard(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, migration := range []string{"0172_seller_source_reversal_closure.down", "0171_seller_source_reversal_execution.down", "0170_seller_source_reversal_commands.down", "0169_payment_execution_lease_locks.down", "0168_transfer_execution_jobs.down", "0167_seller_bank_payout_resumes.down", "0166_seller_bank_payout_execution.down", "0165_seller_bank_payout_commands.down", "0162_seller_funding_admissions.down", "0158_seller_funding_read_deadlines.down", "0157_seller_funding_recovery.down", "0156_seller_payout_funding.down", "0156_seller_payout_funding.up", "0157_seller_funding_recovery.up", "0158_seller_funding_read_deadlines.up", "0162_seller_funding_admissions.up", "0165_seller_bank_payout_commands.up", "0166_seller_bank_payout_execution.up", "0167_seller_bank_payout_resumes.up", "0168_transfer_execution_jobs.up", "0169_payment_execution_lease_locks.up", "0170_seller_source_reversal_commands.up", "0171_seller_source_reversal_execution.up", "0172_seller_source_reversal_closure.up"} {
		body, err := os.ReadFile("../platform/database/migrations/" + migration + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	occupied, _, _, _, _, _ := sellerFundingFixture(t)
	body, err := os.ReadFile("../platform/database/migrations/0156_seller_payout_funding.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = occupied.Exec(ctx, string(body))
	requirePayoutConstraint(t, err)
}

func TestSellerFundingMigrationRejectsHistoricalBankSuccess(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	_, _, _, request := sellerTransferFixture(t, pool)
	applyFundingAdmissionMigration(t, pool, "down")
	applyFundingReadDeadlineMigration(t, pool, "down")
	recoveryDown, err := os.ReadFile("../platform/database/migrations/0157_seller_funding_recovery.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(recoveryDown)); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0156_seller_payout_funding.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_requests SET status='succeeded' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("../platform/database/migrations/0156_seller_payout_funding.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(body)); err == nil {
		t.Fatal("historical bank success was silently accepted")
	} else {
		requirePayoutConstraint(t, err)
	}
}

func TestSellerFundingClosesDanglingReadAfterTerminalRace(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	ctx := context.Background()
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("expected lost response")
	}
	var transferID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM seller_payout_transfers WHERE payout_request_id=$1`, request.ID).Scan(&transferID); err != nil {
		t.Fatal(err)
	}
	var readID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO seller_payout_funding_reads(transfer_id) VALUES($1) RETURNING id`, transferID).Scan(&readID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var outcome string
	var finished bool
	if err := pool.QueryRow(ctx, `SELECT outcome,finished_at IS NOT NULL FROM seller_payout_funding_reads WHERE id=$1`, readID).Scan(&outcome, &finished); err != nil {
		t.Fatal(err)
	}
	if outcome != "skipped" || !finished {
		t.Fatalf("dangling read outcome=%q finished=%t", outcome, finished)
	}
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal(err)
	}
}

func TestSellerFundingPartialSameEvidenceCanRecover(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	ctx := context.Background()
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("expected lost response")
	}
	runtime.lookup = func(_ context.Context, input TransferLookupRequest) (TransferLookupResult, error) {
		return observedSettlementTransfer(input), errors.New("later page timed out")
	}
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("partial query completed transfer")
	}
	runtime.lookup = nil
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertSellerFunding(t, pool, request, "succeeded", "reconciliation_required")
	var retained int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_payout_funding_reads
 WHERE finished_at IS NOT NULL AND NOT requires_review AND jsonb_array_length(evidence->'observations')=1`).Scan(&retained); err != nil || retained != 2 {
		t.Fatalf("partial and final evidence not retained: %d %v", retained, err)
	}
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 2 {
		t.Fatal("recovery resent source funds")
	}
}

func TestSellerFundingRefundBeforeDispatchDoesNotTransfer(t *testing.T) {
	pool, service, runtime, checkout, request, job := sellerFundingFixture(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var settlement uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM product_settlements WHERE payment_id=$1`, checkout.PaymentID).Scan(&settlement); err != nil {
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
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("refunded source was funded")
	}
	if runtime.transfers.Load() != 0 || runtime.reads.Load() != 0 {
		t.Fatal("refunded source reached provider")
	}
	assertSellerFunding(t, pool, request, "requested", "under_review")
}

func TestSellerFundingCancellationPersistsUnknownAndRecovers(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime.lost = true
	runtime.onTransfer = func(context.Context, TransferRequest) { cancel() }
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("lost response completed transfer")
	}
	assertSellerFunding(t, pool, request, "reconciliation_required", "reconciliation_required")
	if err := service.HandleSellerPayoutFundingJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	assertSellerFunding(t, pool, request, "succeeded", "reconciliation_required")
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 1 {
		t.Fatal("lease loss caused a second write")
	}
}

func TestSellerFundingRecoveryAuthenticatesOriginalMerchant(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	ctx := context.Background()
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("expected lost response")
	}
	runtime.merchant = "acct_other_merchant"
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("changed merchant accepted for recovery")
	}
	assertSellerFunding(t, pool, request, "reconciliation_required", "reconciliation_required")
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 0 {
		t.Fatal("changed identity queried or resent transfer")
	}
	runtime.merchant = ""
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal(err)
	}
}

func TestSellerFundingConcurrentHandlersDoNotResend(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	runtime.lost = true
	runtime.onTransfer = func(ctx context.Context, _ TransferRequest) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- service.HandleSellerPayoutFundingJob(ctx, job) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("transfer did not start")
	}
	go func() { second <- service.HandleSellerPayoutFundingJob(ctx, job) }()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE query LIKE '%FOR UPDATE OF pi,o%' AND wait_event_type='Lock' AND pid<>pg_backend_pid())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("duplicate handler did not wait on original payment")
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(release)
	if err := <-first; err == nil {
		t.Fatal("lost response was treated as success")
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	assertSellerFunding(t, pool, request, "succeeded", "reconciliation_required")
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 1 {
		t.Fatal("duplicate handler resent transfer")
	}
	var unfinished int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_payout_funding_reads WHERE finished_at IS NULL`).Scan(&unfinished); err != nil || unfinished != 0 {
		t.Fatalf("unfinished reads=%d err=%v", unfinished, err)
	}
}

func TestSellerFundingDurableJobRetryRetainsAttempts(t *testing.T) {
	pool, service, runtime, _, request, scheduled := sellerFundingFixture(t)
	ctx := context.Background()
	// The fixture invokes its earlier business jobs directly; keep those
	// unrelated queued jobs out of this repository-claim integration test.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=clock_timestamp()+interval '1 day' WHERE id<>$1`, scheduled.ID); err != nil {
		t.Fatal(err)
	}
	repository := jobs.NewRepository(pool)
	claimed, err := repository.Claim(ctx, "seller-funding-test", time.Minute)
	if err != nil || claimed.ID != scheduled.ID || claimed.Attempts != 1 {
		t.Fatalf("first claim: %+v %v", claimed, err)
	}
	runtime.lost = true
	cause := service.HandleSellerPayoutFundingJob(ctx, claimed)
	if cause == nil {
		t.Fatal("lost response completed funding")
	}
	if err := repository.Fail(ctx, claimed, "seller-funding-test", cause); err != nil {
		t.Fatal(err)
	}
	var status, code string
	var delayed bool
	if err := pool.QueryRow(ctx, `SELECT status,last_error_code,available_at>clock_timestamp()+interval '4 minutes' FROM jobs WHERE id=$1`, claimed.ID).Scan(&status, &code, &delayed); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || code != "payment_settlement_pending" || !delayed {
		t.Fatalf("retry status=%s code=%s delayed=%t", status, code, delayed)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=clock_timestamp() WHERE id=$1`, claimed.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = repository.Claim(ctx, "seller-funding-recovery", time.Minute)
	if err != nil || claimed.ID != scheduled.ID || claimed.Attempts != 2 {
		t.Fatalf("recovery claim: %+v %v", claimed, err)
	}
	if err := service.HandleSellerPayoutFundingJob(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, claimed, "seller-funding-recovery"); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM job_attempts WHERE job_id=$1 AND finished_at IS NOT NULL) FROM jobs WHERE id=$1`, claimed.ID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || attempts != 2 || runtime.transfers.Load() != 1 || runtime.reads.Load() != 1 {
		t.Fatalf("job=%s attempts=%d transfers=%d reads=%d", status, attempts, runtime.transfers.Load(), runtime.reads.Load())
	}
	assertSellerFunding(t, pool, request, "succeeded", "reconciliation_required")
}

func TestSellerFundingAdmissionRejectsMismatchedEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	service, _, settlement, request := sellerTransferFixture(t, pool)
	actor, input := approveSellerFundingFixture(t, pool, service, request)
	ctx := context.Background()
	for _, scenario := range []string{"charge", "dispatch_key", "job_kind", "job_payload", "job_terminal", "already_started", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if err := lockProductSettlementPaymentTx(ctx, tx, settlement); err != nil {
				t.Fatal(err)
			}
			reserve := strings.Replace(reserveSellerTransferSQL, "'seller-payout:'||$1::text", "'transfer-'||ps.payment_id::text", 1)
			if scenario == "dispatch_key" {
				reserve = reserveSellerTransferSQL
			}
			var transferID, jobID uuid.UUID
			if err := tx.QueryRow(ctx, reserve+" RETURNING id", request.ID, settlement).Scan(&transferID); err != nil {
				t.Fatal(err)
			}
			insertFundingAdmissionFixture(t, tx, request.ID, transferID, actor, input.ReviewID)
			kind, target, status := SellerPayoutFundingJobKind, transferID, "queued"
			switch scenario {
			case "job_kind":
				kind = ProductSettlementJobKind
			case "job_payload":
				target = uuid.New()
			case "job_terminal":
				status = "succeeded"
			}
			if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status) VALUES($1,jsonb_build_object('transferId',$2::uuid),$3) RETURNING id`, kind, target, status).Scan(&jobID); err != nil {
				t.Fatal(err)
			}
			query := `INSERT INTO seller_payout_funding_dispatches(transfer_id,job_id,payment_id,provider_charge_id,started_at)
 SELECT $1,$2,p.id,p.provider_charge_id,NULL::timestamptz FROM product_settlements s JOIN payment_intents p ON p.id=s.payment_id WHERE s.id=$3`
			if scenario == "charge" {
				query = strings.Replace(query, "p.provider_charge_id,NULL", "'ch_another_source',NULL", 1)
			}
			if scenario == "already_started" {
				query = strings.Replace(query, "NULL::timestamptz", "clock_timestamp()", 1)
			}
			_, err = tx.Exec(ctx, query, transferID, jobID, settlement)
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requirePayoutConstraint(t, err)
			}
		})
	}
}
