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
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func approveSellerFundingFixture(t *testing.T, pool *pgxpool.Pool, service *Service, request SellerPayoutRequest) (uuid.UUID, SellerFundingAdmissionInput) {
	t.Helper()
	actor := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO users(id,email,handle,display_name,role)
 VALUES($1,$2,$3,'Funding operator','admin')`, actor, actor.String()+"@test.local", "fund_"+actor.String()[:8]); err != nil {
		t.Fatal(err)
	}
	original := service.runtimes
	service.runtimes = NewRuntimeCatalog(&sellerBankRuntime{})
	_, err := service.BindSellerPayoutBankTarget(t.Context(), request.SellerID, request.ID, "ba_original")
	service.runtimes = original
	if err != nil {
		t.Fatal(err)
	}
	var settlement uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT settlement_id FROM seller_payout_request_allocations WHERE payout_request_id=$1`, request.ID).Scan(&settlement); err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewSellerPayout(t.Context(), actor, request.ID, SellerPayoutReviewInput{
		SettlementID: settlement, AmountCents: request.AmountCents, BankDestinationID: "ba_original", Decision: "approved",
		Reason: "Verified original funds and frozen bank selection.", SellerMessage: "Your request is approved but no bank payout has been sent.",
	}, "funding-fixture-review", "fixture-review")
	if err != nil {
		t.Fatal(err)
	}
	return actor, SellerFundingAdmissionInput{ReviewID: review.Review.ID, ExpectedRevision: 1, SettlementID: settlement,
		AmountCents: request.AmountCents, BankDestinationID: "ba_original", Confirmed: true, Reason: "Authorize this reviewed source transfer."}
}

func insertFundingAdmissionFixture(t *testing.T, tx pgx.Tx, request, transfer, actor, review uuid.UUID) {
	t.Helper()
	if _, err := tx.Exec(t.Context(), `INSERT INTO seller_payout_funding_admissions(payout_request_id,transfer_id,actor_id,review_id,idempotency_key,reason,request_id)
 VALUES($1,$2,$3,$4,$5,'Explicit source admission fixture.','fixture')`, request, transfer, actor, review, "admit-"+transfer.String()); err != nil {
		t.Fatal(err)
	}
}

func TestSellerFundingReviewedAdmissionAtomicReplayAndGuards(t *testing.T) {
	pool, service, _, request := sellerBankFixture(t)
	actor, input := approveSellerFundingFixture(t, pool, service, request)
	ctx := t.Context()
	before, err := service.GetSellerPayoutReview(ctx, actor, request.ID)
	if err != nil || !before.CanAdmitFunding || before.Funding != nil {
		t.Fatalf("approved request cannot be admitted: %+v %v", before, err)
	}
	first, err := service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "admission-first-key", "original-trace")
	if err != nil || first.Replayed || first.Admission.ReviewID != input.ReviewID || first.JobID == uuid.Nil || first.Request.Status != "under_review" {
		t.Fatalf("admission %+v: %v", first, err)
	}
	if first.Request.CanAdmitFunding || first.Request.Funding == nil || first.Request.Funding.TransferID != first.Admission.TransferID || first.Request.Funding.JobID == nil || *first.Request.Funding.JobID != first.JobID || first.Request.Funding.JobStatus == nil || *first.Request.Funding.JobStatus != "queued" {
		t.Fatalf("new admission not projected: %+v", first.Request)
	}
	var buyer uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT o.buyer_id FROM product_settlements s JOIN orders o ON o.id=s.order_id WHERE s.id=$1`, input.SettlementID).Scan(&buyer); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []uuid.UUID{request.SellerID, actor, buyer} {
		exporter, id, job := sellerExportFixture(t, pool, owner)
		data, body := sellerExportData(t, exporter, owner, id, job)
		rows := data["sellerPayoutFundingAdmissions"]
		if owner == request.SellerID {
			if len(rows) != 1 || len(rows[0]) != 5 || rows[0]["id"] != first.Admission.ID.String() || rows[0]["reviewRevision"] != float64(1) {
				t.Fatal("owner admission export", rows)
			}
			if rows[0]["payoutRequestId"] != request.ID.String() || rows[0]["transferId"] != first.Admission.TransferID.String() || rows[0]["createdAt"] == nil {
				t.Fatal("admission export changed its public field allowlist", rows)
			}
			for _, private := range []string{input.Reason, actor.String(), "admission-first-key", "original-trace"} {
				if strings.Contains(string(body), private) {
					t.Fatal("seller export exposed private admission attribution")
				}
			}
		} else if len(rows) != 0 {
			t.Fatal("non-owner exported seller admission")
		}
	}
	service.config.Enabled = false
	again, err := service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "admission-first-key", "retry-trace")
	if err != nil || !again.Replayed || again.Admission.ID != first.Admission.ID || again.JobID != first.JobID {
		t.Fatalf("replay %+v: %v", again, err)
	}
	for _, field := range []string{"review", "revision", "amount", "settlement", "bank", "reason", "request"} {
		changed, id := input, request.ID
		switch field {
		case "review":
			changed.ReviewID = uuid.New()
		case "revision":
			changed.ExpectedRevision++
		case "amount":
			changed.AmountCents++
		case "settlement":
			changed.SettlementID = uuid.New()
		case "bank":
			changed.BankDestinationID = "ba_another"
		case "reason":
			changed.Reason = "A different source funding explanation."
		case "request":
			id = uuid.New()
		}
		if _, err := service.AdmitSellerPayoutFunding(ctx, actor, id, changed, "admission-first-key", "changed"); !errors.Is(err, ErrSellerFundingAdmissionConflict) {
			t.Fatalf("changed %s: %v", field, err)
		}
	}
	if _, err := service.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID); !errors.Is(err, ErrSellerPayoutNotCancellable) {
		t.Fatal("admitted source could be cancelled", err)
	}
	var admissions, sources, dispatches, audits, events int
	var trace string
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_payout_funding_admissions),
 (SELECT count(*) FROM seller_payout_transfers),(SELECT count(*) FROM seller_payout_funding_dispatches),
 (SELECT count(*) FROM audit_events WHERE action='seller_payout.funding_admitted'),
 (SELECT count(*) FROM seller_payout_request_events WHERE event_type='funding.admitted'),
 (SELECT request_id FROM seller_payout_funding_admissions WHERE id=$1)`, first.Admission.ID).Scan(&admissions, &sources, &dispatches, &audits, &events, &trace); err != nil || admissions != 1 || sources != 1 || dispatches != 1 || audits != 1 || events != 1 || trace != "original-trace" {
		t.Fatalf("atomic evidence %d/%d/%d/%d/%d %s: %v", admissions, sources, dispatches, audits, events, trace, err)
	}
	for _, query := range []string{`DELETE FROM seller_payout_funding_admissions`, `UPDATE seller_payout_funding_admissions SET reason='Replaced admission explanation'`} {
		_, err := pool.Exec(ctx, query)
		requirePayoutConstraint(t, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "admission-first-key", "revoked"); !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("revoked replay", err)
	}
	body, err := os.ReadFile("../platform/database/migrations/0162_seller_funding_admissions.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, string(body))
	requirePayoutConstraint(t, err)
}

func TestSellerFundingAdmissionFailureRollsBackAndConcurrentRetries(t *testing.T) {
	pool, service, _, request := sellerBankFixture(t)
	actor, input := approveSellerFundingFixture(t, pool, service, request)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_funding_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.action='seller_payout.funding_admitted' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER fail_funding_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_funding_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "atomic-admission-key", "fail"); err == nil {
		t.Fatal("audit failure committed")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_payout_funding_admissions)+(SELECT count(*) FROM seller_payout_transfers)+
 (SELECT count(*) FROM seller_payout_funding_dispatches)+(SELECT count(*) FROM jobs WHERE kind='payment.fund_seller_payout')+
 (SELECT count(*) FROM seller_payout_request_events WHERE event_type='funding.admitted')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial admission", count, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_funding_audit ON audit_events; DROP FUNCTION fail_funding_audit()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan SellerFundingAdmissionResult, 6)
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "atomic-admission-key", "retry")
			results <- out
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id uuid.UUID
	created := 0
	for out := range results {
		if id == uuid.Nil {
			id = out.Admission.ID
		}
		if id != out.Admission.ID {
			t.Fatal("duplicate admission")
		}
		if !out.Replayed {
			created++
		}
	}
	if created != 1 {
		t.Fatal("multiple initial admissions", created)
	}
}

func TestSellerFundingAdmissionCurrentAuthorityAndEligibility(t *testing.T) {
	for _, scenario := range []string{"stale_review", "superseded_approval", "cancelled", "self", "revoked", "bank_changed", "refund", "mode", "debt", "unconfirmed"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, _, request := sellerBankFixture(t)
			actor, input := approveSellerFundingFixture(t, pool, service, request)
			ctx := t.Context()
			var err error
			switch scenario {
			case "stale_review":
				input.ReviewID = uuid.New()
			case "superseded_approval":
				_, err = service.ReviewSellerPayout(ctx, actor, request.ID, SellerPayoutReviewInput{
					ExpectedRevision: input.ExpectedRevision, SettlementID: input.SettlementID,
					AmountCents: input.AmountCents, BankDestinationID: input.BankDestinationID,
					Decision: "rejected", Reason: "Withdraw the prior approval before funding.",
					SellerMessage: "Your request was rejected before funds were sent.",
				}, "superseding-rejection", "test")
			case "cancelled":
				_, err = service.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID)
			case "self":
				actor = request.SellerID
				_, err = pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor)
			case "revoked":
				_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
			case "bank_changed":
				_, err = pool.Exec(ctx, `UPDATE payment_destinations SET destination_id='acct_replaced' WHERE user_id=$1`, request.SellerID)
			case "refund":
				_, err = pool.Exec(ctx, `UPDATE product_settlements SET status='refund_hold' WHERE id=$1`, input.SettlementID)
			case "mode":
				_, err = pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='automatic'`)
			case "debt":
				_, err = pool.Exec(ctx, `INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status) VALUES($1,$2,1,1,'USD','open')`, request.SellerID, input.SettlementID)
			case "unconfirmed":
				input.Confirmed = false
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "ineligible-admission", "test"); err == nil {
				t.Fatal("ineligible admission succeeded")
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_payout_funding_admissions)+(SELECT count(*) FROM seller_payout_transfers)`).Scan(&count); err != nil || count != 0 {
				t.Fatal("invalid admission wrote evidence", count, err)
			}
		})
	}
}

func TestSellerFundingAdmissionEligibilityDuringPaymentLockWait(t *testing.T) {
	for _, scenario := range []string{"revocation", "refund"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, _, request := sellerBankFixture(t)
			actor, input := approveSellerFundingFixture(t, pool, service, request)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(ctx)
			var payment uuid.UUID
			if err = blocker.QueryRow(ctx, `SELECT p.id FROM payment_intents p JOIN product_settlements s ON s.payment_id=p.id WHERE s.id=$1 FOR UPDATE OF p`, input.SettlementID).Scan(&payment); err != nil {
				t.Fatal(err)
			}
			var blockerPID int
			if err = blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "wait-admission-key", "test")
				done <- err
			}()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%product_settlements%')`, blockerPID).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if ctx.Err() != nil {
					t.Fatal(ctx.Err())
				}
				time.Sleep(10 * time.Millisecond)
			}
			if scenario == "revocation" {
				_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
			} else {
				// Use the real refund transition while owning the original payment lock.
				// Admission's preliminary read has already happened; it must observe the
				// committed refund state after its lock wait, rather than that old view.
				err = lockProductSettlementPaymentTx(ctx, blocker, input.SettlementID)
				if err == nil {
					err = markProductSettlementRefundTx(ctx, blocker, payment)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if err == nil || (scenario == "revocation" && !errors.Is(err, ErrFinanceForbidden)) {
				t.Fatal("eligibility change lost after wait", scenario, err)
			}
			var count int
			if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_payout_funding_admissions)+(SELECT count(*) FROM seller_payout_transfers)+(SELECT count(*) FROM jobs WHERE kind='payment.fund_seller_payout')`).Scan(&count); err != nil || count != 0 {
				t.Fatal("ineligible wait committed funding", count, err)
			}
		})
	}
}

func applyFundingAdmissionMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	// Legacy fixtures must remove the empty dependent bank-command table first.
	// Its own down migration refuses removal when commands exist.
	migrations := []string{"0162_seller_funding_admissions.up", "0165_seller_bank_payout_commands.up", "0166_seller_bank_payout_execution.up", "0167_seller_bank_payout_resumes.up", "0168_transfer_execution_jobs.up", "0169_payment_execution_lease_locks.up", "0170_seller_source_reversal_commands.up", "0171_seller_source_reversal_execution.up", "0172_seller_source_reversal_closure.up"}
	if direction == "down" {
		migrations = []string{"0172_seller_source_reversal_closure.down", "0171_seller_source_reversal_execution.down", "0170_seller_source_reversal_commands.down", "0169_payment_execution_lease_locks.down", "0168_transfer_execution_jobs.down", "0167_seller_bank_payout_resumes.down", "0166_seller_bank_payout_execution.down", "0165_seller_bank_payout_commands.down", "0162_seller_funding_admissions.down"}
	}
	for _, migration := range migrations {
		body, err := os.ReadFile("../platform/database/migrations/" + migration + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(t.Context(), string(body)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSellerFundingAdmissionLegacyBoundary(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "unstarted", true: "started"}[started], func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			t.Cleanup(cleanup)
			service, _, settlement, request := sellerTransferFixture(t, pool)
			runtime := &settlementReadRuntime{productSettlementRuntime: &productSettlementRuntime{}}
			service.runtimes = NewRuntimeCatalog(runtime)
			applyFundingAdmissionMigration(t, pool, "down")
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			var source uuid.UUID
			query := strings.Replace(reserveSellerTransferSQL, "'seller-payout:'||$1::text", "'transfer-'||ps.payment_id::text", 1) + " RETURNING id"
			if err := tx.QueryRow(t.Context(), query, request.ID, settlement).Scan(&source); err != nil {
				t.Fatal(err)
			}
			job := jobs.Job{Kind: SellerPayoutFundingJobKind}
			if job.ID, err = enqueueSellerPayoutFundingTx(t.Context(), tx, source); err != nil {
				t.Fatal(err)
			}
			if started {
				if _, err = tx.Exec(t.Context(), `UPDATE seller_payout_funding_dispatches SET started_at=clock_timestamp();
 UPDATE seller_payout_transfers SET status='processing'; UPDATE seller_payout_requests SET status='processing'`); err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(t.Context(), `SELECT payload FROM jobs WHERE id=$1`, job.ID).Scan(&job.Payload); err != nil {
				t.Fatal(err)
			}
			applyFundingAdmissionMigration(t, pool, "up")
			assertOperationalMetric(t, pool, "problem", "seller_funding_admission_missing", "test", 1, 0, 0)
			err = service.HandleSellerPayoutFundingJob(t.Context(), job)
			if started {
				if err != nil || runtime.reads.Load() != 1 {
					t.Fatal("legacy read recovery failed", err, runtime.reads.Load())
				}
				assertSellerFunding(t, pool, request, "succeeded", "processing")
			} else {
				if !errors.Is(err, ErrSellerFundingAdmissionConflict) {
					t.Fatal("unapproved legacy start", err)
				}
				_, err = pool.Exec(t.Context(), `UPDATE seller_payout_funding_dispatches SET started_at=clock_timestamp()`)
				requirePayoutConstraint(t, err)
			}
			if runtime.transfers.Load() != 0 {
				t.Fatal("legacy funding sent new money")
			}
			assertOperationalMetric(t, pool, "problem", "seller_funding_admission_missing", "test", 1, 0, 0)
		})
	}
}

func TestSellerFundingAdmissionProtocolAndWorkerRevocation(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	assertOperationalMetric(t, pool, "problem", "seller_funding_admission_missing", "test", 0, 0, 0)
	for _, protocol := range []string{"", "old-v0"} {
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), `SELECT set_config('app.seller_funding_admission_protocol',$1,true)`, protocol); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), `UPDATE seller_payout_funding_dispatches SET started_at=clock_timestamp()`)
		requirePayoutConstraint(t, err)
		_ = tx.Rollback(t.Context())
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='member' WHERE id=(SELECT actor_id FROM seller_payout_funding_admissions WHERE payout_request_id=$1)`, request.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err == nil {
		t.Fatal("revoked admission executed")
	}
	if runtime.transfers.Load() != 0 || runtime.reads.Load() != 0 {
		t.Fatal("revoked first execution contacted provider")
	}
	var started bool
	if err := pool.QueryRow(t.Context(), `SELECT started_at IS NOT NULL FROM seller_payout_funding_dispatches WHERE job_id=$1`, job.ID).Scan(&started); err != nil || started {
		t.Fatal("revoked execution claimed dispatch", started, err)
	}
}

func TestSellerFundingAdmissionCannotCommitWithoutJob(t *testing.T) {
	pool, service, _, request := sellerBankFixture(t)
	actor, input := approveSellerFundingFixture(t, pool, service, request)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	var source uuid.UUID
	query := strings.Replace(reserveSellerTransferSQL, "'seller-payout:'||$1::text", "'transfer-'||ps.payment_id::text", 1) + " RETURNING id"
	if err = tx.QueryRow(t.Context(), query, request.ID, input.SettlementID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	insertFundingAdmissionFixture(t, tx, request.ID, source, actor, input.ReviewID)
	err = tx.Commit(t.Context())
	requirePayoutConstraint(t, err)
	var count int
	if err = pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM seller_payout_funding_admissions)+(SELECT count(*) FROM seller_payout_transfers)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphan admission committed", count, err)
	}
}

func TestSellerFundingAdmissionRacesCancellationAndRejection(t *testing.T) {
	for _, action := range []string{"cancel", "reject"} {
		t.Run(action, func(t *testing.T) {
			pool, service, _, request := sellerBankFixture(t)
			actor, input := approveSellerFundingFixture(t, pool, service, request)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			start := make(chan struct{})
			admitted, stopped := make(chan error, 1), make(chan error, 1)
			go func() {
				<-start
				_, err := service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "racing-admission-key", "test")
				admitted <- err
			}()
			go func() {
				<-start
				var err error
				if action == "cancel" {
					_, err = service.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID)
				} else {
					_, err = service.ReviewSellerPayout(ctx, actor, request.ID, SellerPayoutReviewInput{ExpectedRevision: 1, SettlementID: input.SettlementID, AmountCents: input.AmountCents,
						BankDestinationID: input.BankDestinationID, Decision: "rejected", Reason: "Withdraw approval before source admission.", SellerMessage: "The request was rejected before source processing."}, "racing-review-rejection", "test")
				}
				stopped <- err
			}()
			close(start)
			admitErr, stopErr := <-admitted, <-stopped
			if (admitErr == nil) == (stopErr == nil) {
				t.Fatalf("need exactly one successful transition: %v / %v", admitErr, stopErr)
			}
			var status string
			var count int
			if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM seller_payout_funding_admissions WHERE payout_request_id=$1) FROM seller_payout_requests WHERE id=$1`, request.ID).Scan(&status, &count); err != nil {
				t.Fatal(err)
			}
			funds, err := singleSellerFunds(t, service, ctx, request.SellerID)
			if err != nil {
				t.Fatal(err)
			}
			if admitErr == nil {
				if status != "under_review" || count != 1 || funds.ReservedCents != request.AmountCents {
					t.Fatal("admission lost reservation", status, count, funds)
				}
			} else if status != "cancelled" || count != 0 || funds.ReservedCents != 0 {
				t.Fatal("cancel/reject did not release atomically", status, count, funds)
			}
		})
	}
}

func TestSellerFundingEligibilityRequiresFrozenCharge(t *testing.T) {
	pool, service, _, request := sellerBankFixture(t)
	actor, input := approveSellerFundingFixture(t, pool, service, request)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_charge_id=NULL WHERE id=(SELECT payment_id FROM product_settlements WHERE id=$1)`, input.SettlementID); err != nil {
		t.Fatal(err)
	}
	item, err := service.GetSellerPayoutReview(ctx, actor, request.ID)
	if err != nil || item.CanAdmitFunding || item.Funding != nil {
		t.Fatalf("invalid charge advertised funding: %+v %v", item, err)
	}
	if _, err = service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "missing-source-guard", "test"); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Fatalf("missing source: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_payout_transfers)+(SELECT count(*) FROM seller_payout_funding_admissions)+(SELECT count(*) FROM jobs WHERE kind='payment.fund_seller_payout')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid source wrote evidence: %d %v", count, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_charge_id='ch_restored_source' WHERE id=(SELECT payment_id FROM product_settlements WHERE id=$1)`, input.SettlementID); err != nil {
		t.Fatal(err)
	}
	service.config.Enabled = false
	item, err = service.GetSellerPayoutReview(ctx, actor, request.ID)
	if err != nil || item.CanAdmitFunding {
		t.Fatalf("disabled funding offered %+v %v", item, err)
	}
	service.config.Enabled = true
	if _, err := service.AdmitSellerPayoutFunding(ctx, actor, request.ID, input, "missing-source-guard", "restored"); err != nil {
		t.Fatal(err)
	}
}
