package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

const SellerBankPayoutJobKind = "payment.execute_seller_bank_payout"

type sellerBankPayload struct {
	CommandID uuid.UUID `json:"commandId"`
}

type sellerBankState struct {
	CommandID, SettlementID, SellerID, ActorID uuid.UUID
	Input                                      PayoutRequest
	Identity                                   ProductCheckoutIdentity
	Parent                                     string
	StartedAt                                  *time.Time
}

// QueueSellerBankPayout authorizes execution of an existing immutable command.
// Only the original, currently authorized operator can queue it. The durable
// job binding is idempotent, including after payment writes have been disabled.
func (s *Service) QueueSellerBankPayout(ctx context.Context, actor, command uuid.UUID) (uuid.UUID, error) {
	if s == nil || s.pool == nil {
		return uuid.Nil, ErrDisabled
	}
	if actor == uuid.Nil || command == uuid.Nil {
		return uuid.Nil, ErrSellerBankPayoutInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(context.Background())
	job, err := s.queueSellerBankPayoutTx(ctx, tx, actor, command)
	if err != nil {
		return uuid.Nil, err
	}
	return job, tx.Commit(ctx)
}

func (s *Service) queueSellerBankPayoutTx(ctx context.Context, tx pgx.Tx, actor, command uuid.UUID) (uuid.UUID, error) {
	if err := paymentFinanceAuthority(ctx, tx, actor, false); err != nil {
		return uuid.Nil, err
	}
	state, err := lockSellerBankTx(ctx, tx, command)
	if err != nil {
		return uuid.Nil, err
	}
	if actor != state.ActorID || actor == state.SellerID {
		return uuid.Nil, ErrFinanceForbidden
	}
	if err := paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
		return uuid.Nil, err
	}
	var job uuid.UUID
	err = tx.QueryRow(ctx, `SELECT job_id FROM seller_bank_payout_dispatches WHERE command_id=$1`, command).Scan(&job)
	if err == nil {
		return job, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	if err := s.validateSellerBankDispatchTx(ctx, tx, state); err != nil {
		return uuid.Nil, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
 VALUES($1,jsonb_build_object('commandId',$2::uuid),10000) RETURNING id`, SellerBankPayoutJobKind, command).Scan(&job); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO seller_bank_payout_dispatches(command_id,job_id) VALUES($1,$2)`, command, job); err != nil {
		return uuid.Nil, err
	}
	if err := recordSellerPayoutEventTx(ctx, tx, state.Input.PayoutRequestID, "bank.queued", &state.Parent, &state.Parent,
		"bank-queued:"+command.String(), map[string]any{"commandId": command, "jobId": job}); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
 VALUES($1,'seller_payout.bank_queued','seller_payout_request',$2,$3,jsonb_build_object('commandId',$4::uuid,'jobId',$5::uuid))`,
		actor, state.Input.PayoutRequestID, "seller-bank-queue:"+command.String(), command, job); err != nil {
		return uuid.Nil, err
	}
	return job, nil
}

func lockSellerBankTx(ctx context.Context, tx pgx.Tx, command uuid.UUID) (sellerBankState, error) {
	state := sellerBankState{CommandID: command}
	if err := tx.QueryRow(ctx, `SELECT settlement_id FROM seller_bank_payout_commands WHERE id=$1`, command).Scan(&state.SettlementID); err != nil {
		return state, err
	}
	if err := lockProductSettlementPaymentTx(ctx, tx, state.SettlementID); err != nil {
		return state, err
	}
	var identity []byte
	err := tx.QueryRow(ctx, `SELECT c.payout_request_id,c.seller_id,c.actor_id,c.provider_identity,c.destination_id,c.bank_destination_id,
 c.amount_cents,c.currency,c.dispatch_key,c.created_at,r.status,d.started_at
 FROM seller_bank_payout_commands c JOIN seller_payout_requests r ON r.id=c.payout_request_id
 LEFT JOIN seller_bank_payout_dispatches d ON d.command_id=c.id WHERE c.id=$1 FOR UPDATE OF r`, command).Scan(
		&state.Input.PayoutRequestID, &state.SellerID, &state.ActorID, &identity, &state.Input.DestinationID, &state.Input.BankDestinationID,
		&state.Input.AmountCents, &state.Input.Currency, &state.Input.IdempotencyKey, &state.Input.ReservedAt, &state.Parent, &state.StartedAt)
	if err != nil {
		return state, err
	}
	if json.Unmarshal(identity, &state.Identity) != nil || state.Identity.Provider != "stripe" {
		return state, ErrCheckoutReconciliation
	}
	state.Input.Identity = &state.Identity
	return state, nil
}

func (s *Service) validateSellerBankDispatchTx(ctx context.Context, tx pgx.Tx, state sellerBankState) error {
	if !s.config.Enabled {
		return ErrDisabled
	}
	if !time.Now().Before(state.Input.ReservedAt.Add(stripePayoutDispatchWindow)) {
		return ErrCheckoutReconciliation
	}
	_, err := tx.Exec(ctx, `SELECT assert_seller_bank_payout_command(c) FROM seller_bank_payout_commands c WHERE id=$1`, state.CommandID)
	return err
}

// HandleSellerBankPayoutJob commits its first-send marker before network I/O.
// A crash at any later point only permits authenticated lookups, never another
// CreatePayout. The original payment lock also serializes refund and bank writes.
func (s *Service) HandleSellerBankPayoutJob(ctx context.Context, job jobs.Job) error {
	return s.handleSellerBankPayoutJob(ctx, job, false)
}

func (s *Service) handleSellerBankPayoutJob(ctx context.Context, job jobs.Job, recovery bool) error {
	if s == nil || s.pool == nil {
		return ErrDisabled
	}
	var payload sellerBankPayload
	if job.ID == uuid.Nil || json.Unmarshal(job.Payload, &payload) != nil || payload.CommandID == uuid.Nil {
		return ErrSellerBankPayoutInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	state, err := lockSellerBankTx(ctx, tx, payload.CommandID)
	if err != nil {
		return err
	}
	var bound bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_bank_payout_active_dispatches d JOIN jobs j ON j.id=$2
 WHERE d.command_id=$1 AND j.payload=jsonb_build_object('commandId',$1::uuid)
 AND ((NOT $3 AND d.execution_job_id=j.id AND j.kind='payment.execute_seller_bank_payout')
 OR ($3 AND d.started_at IS NOT NULL AND j.kind='payment.check_seller_bank_payout'
 AND EXISTS(SELECT 1 FROM seller_bank_payout_checks WHERE command_id=d.command_id AND job_id=j.id))))`, payload.CommandID, job.ID, recovery).Scan(&bound); err != nil {
		return err
	}
	if !bound {
		return ErrSellerBankPayoutInvalid
	}
	if state.Parent == "cancelled" {
		var closed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE payout_request_id=$1)`, state.Input.PayoutRequestID).Scan(&closed); err != nil {
			return err
		}
		if !closed {
			return ErrSellerBankPayoutInvalid
		}
		return tx.Commit(ctx)
	}
	create := !recovery && state.StartedAt == nil
	runtime, err := s.runtimes.Runtime(state.Identity.Provider)
	if err != nil {
		return err
	}
	if _, ok := runtime.(SellerPayoutReader); !ok {
		return newProviderFailure("payment_provider_unsupported", 0)
	}
	if create {
		if _, ok := runtime.(SellerPayoutRuntime); !ok {
			return newProviderFailure("payment_provider_unsupported", 0)
		}
	}
	var readID uuid.UUID
	var deadline time.Time
	if create {
		if err := s.validateSellerBankDispatchTx(ctx, tx, state); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.seller_bank_execution_job',$1,true)`, job.ID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE seller_bank_payout_dispatches SET started_at=t,deadline_at=t+interval '20 seconds'
 FROM clock_timestamp() t WHERE command_id=$1`, state.CommandID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO seller_bank_payout_reads(command_id,kind,started_at,deadline_at)
 SELECT command_id,'create',started_at,deadline_at FROM seller_bank_payout_dispatches WHERE command_id=$1 RETURNING id,deadline_at`, state.CommandID).Scan(&readID, &deadline); err != nil {
			return err
		}
		if err := recordSellerPayoutEventTx(ctx, tx, state.Input.PayoutRequestID, "bank.started", &state.Parent, &state.Parent,
			"bank-started:"+state.CommandID.String(), map[string]any{"commandId": state.CommandID, "jobId": job.ID}); err != nil {
			return err
		}
	} else if err := tx.QueryRow(ctx, `INSERT INTO seller_bank_payout_reads(command_id,kind,started_at,deadline_at)
 SELECT $1,'query',t,t+interval '20 seconds' FROM clock_timestamp() t RETURNING id,deadline_at`, state.CommandID).Scan(&readID, &deadline); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	callCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tx, err = s.pool.Begin(callCtx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	state, err = lockSellerBankTx(callCtx, tx, payload.CommandID)
	if err != nil {
		return err
	}
	var alreadyFinished bool
	if err := tx.QueryRow(callCtx, `SELECT finished_at IS NOT NULL FROM seller_bank_payout_reads WHERE id=$1`, readID).Scan(&alreadyFinished); err != nil {
		return err
	}
	if alreadyFinished {
		// A serialized, authoritative terminal observation closed this waiting
		// read. Never POST or replace that retained supersession evidence.
		return tx.Commit(callCtx)
	}
	result := PayoutLookupResult{Outcome: "incomplete", Observations: []Payout{}}
	guardRejected := false
	runtime, callErr := s.runtimes.Runtime(state.Identity.Provider)
	if callErr == nil {
		var identity ProductCheckoutIdentity
		identity, callErr = checkoutIdentity(callCtx, runtime)
		if callErr == nil && identity != state.Identity {
			callErr = ErrCheckoutReconciliation
		}
	}
	if create && callErr == nil {
		// SQL guards can reject a job cancelled after the durable first-send
		// marker. Keep their failure inside a savepoint so the outer transaction
		// can retain the failed observation without attempting a bank request.
		guard, guardErr := tx.Begin(callCtx)
		if guardErr != nil {
			return guardErr
		}
		callErr = s.validateSellerBankDispatchTx(callCtx, guard, state)
		if callErr == nil {
			_, callErr = guard.Exec(callCtx, `SELECT assert_seller_bank_execution_job($1,$2)`, state.CommandID, job.ID)
		}
		if callErr != nil {
			guardRejected = true
			if err := guard.Rollback(callCtx); err != nil {
				return err
			}
		} else if err := guard.Commit(callCtx); err != nil {
			return err
		}
		if callErr == nil {
			creator, ok := runtime.(SellerPayoutRuntime)
			if !ok {
				callErr = newProviderFailure("payment_provider_unsupported", 0)
			} else {
				var payout Payout
				payout, callErr = creator.CreatePayout(callCtx, state.Input)
				// Preserve a returned object even if transport/validation later failed.
				if payout.ProviderID != "" {
					result.Observations = append(result.Observations, payout)
				}
				result.Outcome, result.Pages = "found", 1
			}
		}
	} else if !create && callErr == nil {
		reader, ok := runtime.(SellerPayoutReader)
		if !ok {
			callErr = newProviderFailure("payment_provider_unsupported", 0)
		} else {
			// A full scoped lookup also detects a second matching payout. A GET
			// of the first known ID alone could conceal duplicate remote evidence.
			result, callErr = reader.LookupPayout(callCtx, state.Input)
		}
	}
	if callErr == nil {
		callErr = callCtx.Err()
	}
	result, review := validateSellerBankRead(state.Input, result, callErr)
	review = review || guardRejected
	if !time.Now().Before(deadline) {
		result.Outcome = "error"
		callErr = context.DeadlineExceeded
	}
	saveCtx, stopSave := context.WithDeadline(context.WithoutCancel(ctx), deadline.Add(5*time.Second))
	defer stopSave()
	if len(result.Observations) == 1 {
		item := result.Observations[0]
		var priorConflict bool
		if err := tx.QueryRow(saveCtx, `SELECT EXISTS(SELECT 1 FROM seller_bank_payout_reads r
 WHERE r.command_id=$1 AND r.finished_at IS NOT NULL AND (r.requires_review OR EXISTS(
 SELECT 1 FROM jsonb_array_elements(r.evidence->'observations') o WHERE o->>'providerId' IS DISTINCT FROM $2)))
 OR ($3='paid' AND EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=$1 AND status IN ('failed','canceled')))`,
			state.CommandID, item.ProviderID, item.Status).Scan(&priorConflict); err != nil {
			return err
		}
		review = review || priorConflict
	}
	evidence, err := json.Marshal(result)
	if err != nil {
		return err
	}
	code := fundingErrorCode(callErr)
	if review && code == "" {
		code = "payment_reconciliation_required"
	}
	if err := tx.QueryRow(saveCtx, `UPDATE seller_bank_payout_reads SET finished_at=t,
 outcome=CASE WHEN t>deadline_at THEN 'error' ELSE $2 END,
 requires_review=$3,error_code=CASE WHEN t>deadline_at THEN 'payment_provider_timeout' ELSE $4 END,evidence=$5
 FROM clock_timestamp() t WHERE id=$1 RETURNING outcome,error_code`, readID, result.Outcome, review, code, evidence).Scan(&result.Outcome, &code); err != nil {
		return err
	}
	settled, err := applySellerBankReadTx(saveCtx, tx, state, readID, result, review || code != "")
	if err != nil {
		return err
	}
	if settled {
		if _, err := tx.Exec(saveCtx, `UPDATE seller_bank_payout_reads SET finished_at=clock_timestamp(),outcome='superseded',
 error_code='bank_read_superseded',evidence=jsonb_build_object('observations','[]'::jsonb,'supersededBy',$2::uuid)
 WHERE command_id=$1 AND id<>$2 AND finished_at IS NULL`, state.CommandID, readID); err != nil {
			return err
		}
	}
	if err := tx.Commit(saveCtx); err != nil {
		return err
	}
	if !settled {
		return newProviderFailure("payment_settlement_pending", 5*time.Minute)
	}
	return nil
}

func validSellerBankObservation(input PayoutRequest, item Payout) bool {
	if len(item.FailureCode) > 100 || (item.FailureCode != "" && item.Status != "failed") {
		return false
	}
	for _, c := range item.FailureCode {
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return validStripeID(item.ProviderID, "po_") && item.Destination == input.DestinationID &&
		item.BankDestinationID == input.BankDestinationID && item.AmountCents == input.AmountCents && item.Currency == input.Currency &&
		oneOf(item.Status, "pending", "in_transit", "paid", "failed", "canceled") &&
		!item.CreatedAt.IsZero() && !item.CreatedAt.Before(input.ReservedAt.Add(-5*time.Minute)) &&
		!item.CreatedAt.After(input.ReservedAt.Add(stripePayoutDispatchWindow+5*time.Minute)) && !item.CreatedAt.After(time.Now().Add(time.Minute))
}

func validateSellerBankRead(input PayoutRequest, result PayoutLookupResult, callErr error) (PayoutLookupResult, bool) {
	if result.Observations == nil {
		result.Observations = []Payout{}
	}
	valid := result.Pages >= 0 && result.Pages <= 10 && len(result.Observations) <= 2
	seen := map[string]bool{}
	for _, item := range result.Observations {
		valid = valid && validSellerBankObservation(input, item) && !seen[item.ProviderID]
		seen[item.ProviderID] = true
	}
	if callErr == nil {
		valid = valid && result.Pages > 0 && ((result.Outcome == "found" && len(result.Observations) == 1) ||
			(result.Outcome == "not_found" && len(result.Observations) == 0) ||
			(result.Outcome == "ambiguous" && len(result.Observations) == 2) || result.Outcome == "incomplete")
	}
	review := !valid || len(result.Observations) > 1 || fundingErrorCode(callErr) == "payment_response_invalid"
	if len(result.Observations) > 2 {
		result.Observations = result.Observations[:2]
	}
	if callErr != nil || !valid {
		result.Outcome = "error"
	}
	return result, review
}

func applySellerBankReadTx(ctx context.Context, tx pgx.Tx, state sellerBankState, readID uuid.UUID, result PayoutLookupResult, blocked bool) (bool, error) {
	var conflict, paid, returned bool
	var payout Payout
	if len(result.Observations) == 1 {
		payout = result.Observations[0]
	}
	if err := tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM seller_bank_payout_reads r WHERE r.command_id=$1 AND r.finished_at IS NOT NULL AND
 (r.requires_review OR EXISTS(SELECT 1 FROM jsonb_array_elements(r.evidence->'observations') o WHERE o->>'providerId' IS DISTINCT FROM $2))),
 EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=$1 AND status='paid'),
 EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=$1 AND status IN ('failed','canceled'))`, state.CommandID, payout.ProviderID).Scan(&conflict, &paid, &returned); err != nil {
		return false, err
	}
	if blocked || conflict || result.Outcome != "found" || (returned && payout.Status == "paid") {
		// Retain a previously confirmed debit; uncertainty is not a return.
		if state.Parent != "succeeded" {
			if _, err := tx.Exec(ctx, `UPDATE seller_payout_requests SET status='reconciliation_required',failure_code='bank_result_pending',updated_at=clock_timestamp() WHERE id=$1`, state.Input.PayoutRequestID); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	var resultID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM seller_bank_payout_results WHERE command_id=$1 AND status=$2`, state.CommandID, payout.Status).Scan(&resultID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO seller_bank_payout_results(command_id,read_id,provider_payout_id,status) VALUES($1,$2,$3,$4) RETURNING id`,
			state.CommandID, readID, payout.ProviderID, payout.Status).Scan(&resultID)
	}
	if err != nil {
		return false, err
	}
	entry := ""
	parent := state.Parent
	if payout.Status == "paid" {
		entry, parent = "payout_debit", "succeeded"
	} else if oneOf(payout.Status, "failed", "canceled") {
		parent = "reconciliation_required"
		if paid {
			entry = "payout_return"
		}
	}
	if entry != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO seller_ledger_entries(seller_id,entry_type,amount_cents,currency,payout_request_id,idempotency_key,evidence)
 VALUES($1,$2,$3,$4,$5,$2||':'||$6::text,jsonb_build_object('bankResultId',$7::uuid)) ON CONFLICT DO NOTHING`,
			state.SellerID, entry, state.Input.AmountCents, state.Input.Currency, state.Input.PayoutRequestID, state.CommandID, resultID); err != nil {
			return false, err
		}
	}
	if parent != state.Parent || oneOf(payout.Status, "paid", "failed", "canceled") {
		var failureCode any
		if parent == "reconciliation_required" {
			failureCode = "bank_payout_returned"
		}
		if _, err := tx.Exec(ctx, `UPDATE seller_payout_requests SET status=$2,failure_code=$3,updated_at=clock_timestamp() WHERE id=$1`,
			state.Input.PayoutRequestID, parent, failureCode); err != nil {
			return false, err
		}
	}
	if err := recordSellerPayoutEventTx(ctx, tx, state.Input.PayoutRequestID, "bank."+payout.Status, &state.Parent, &parent,
		"bank-result:"+resultID.String(), map[string]any{"commandId": state.CommandID, "bankResultId": resultID}); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 SELECT 'seller_payout.bank_observed','seller_payout_request',$1,$2,jsonb_build_object('bankResultId',$3::uuid)
 WHERE NOT EXISTS(SELECT 1 FROM audit_events WHERE action='seller_payout.bank_observed' AND request_id=$2)`,
		state.Input.PayoutRequestID, "bank-result:"+resultID.String(), resultID); err != nil {
		return false, err
	}
	if err := notifySellerBankResultTx(ctx, tx, state, payout.Status, paid && oneOf(payout.Status, "failed", "canceled")); err != nil {
		return false, err
	}
	return oneOf(payout.Status, "paid", "failed", "canceled"), nil
}
