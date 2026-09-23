package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

type sellerSourceReversalPayload struct {
	CommandID uuid.UUID `json:"commandId"`
}

type sellerSourceReversalState struct {
	Input    TransferReversalRequest
	Identity ProductCheckoutIdentity
	SellerID uuid.UUID
	JobID    uuid.UUID
	Parent   string
	Started  bool
}

func lockSellerSourceReversalTx(ctx context.Context, tx pgx.Tx, command uuid.UUID) (sellerSourceReversalState, error) {
	var state sellerSourceReversalState
	if err := tx.QueryRow(ctx, `SELECT payout_request_id FROM seller_source_reversal_commands WHERE id=$1`, command).Scan(&state.Input.PayoutRequestID); err != nil {
		return state, err
	}
	if _, err := tx.Exec(ctx, `SELECT lock_seller_payout_disposition($1)`, state.Input.PayoutRequestID); err != nil {
		return state, err
	}
	var identity []byte
	err := tx.QueryRow(ctx, `SELECT c.source_transfer_id,c.payment_id,c.provider_transfer_id,c.provider_charge_id,c.destination_id,
 c.amount_cents,c.currency,c.live_mode,c.dispatch_key,c.created_at,c.provider_identity,c.seller_id,c.job_id,r.status,
 EXISTS(SELECT 1 FROM seller_source_reversal_dispatches WHERE command_id=c.id)
 FROM seller_source_reversal_commands c JOIN seller_payout_requests r ON r.id=c.payout_request_id WHERE c.id=$1`, command).Scan(
		&state.Input.SourceTransferID, &state.Input.PaymentID, &state.Input.ProviderTransferID, &state.Input.ProviderChargeID, &state.Input.DestinationID,
		&state.Input.AmountCents, &state.Input.Currency, &state.Input.LiveMode, &state.Input.IdempotencyKey, &state.Input.ReservedAt, &identity,
		&state.SellerID, &state.JobID, &state.Parent, &state.Started)
	if err != nil {
		return state, err
	}
	if json.Unmarshal(identity, &state.Identity) != nil || state.Identity.Provider != "stripe" || state.Identity.LiveMode != state.Input.LiveMode {
		return state, ErrCheckoutReconciliation
	}
	state.Input.CommandID, state.Input.ReverseAmountCents = command, state.Input.AmountCents
	state.Input.Identity = &state.Identity
	return state, nil
}

func (s *Service) validateSellerSourceReversalExecutionTx(ctx context.Context, tx pgx.Tx, state sellerSourceReversalState, execution uuid.UUID) error {
	if !s.config.Enabled {
		return ErrDisabled
	}
	_, err := tx.Exec(ctx, `SELECT assert_seller_source_reversal_execution($1,$2)`, state.Input.CommandID, execution)
	return err
}

func (s *Service) HandleSellerSourceReversalJob(ctx context.Context, job jobs.Job) error {
	return s.handleSellerSourceReversalJob(ctx, job, false)
}

// Only the invocation that commits the immutable first-send marker may POST.
// Every later invocation, including a lost response or a stopped-job recovery,
// performs an authenticated lookup. Observed return never releases local funds.
func (s *Service) handleSellerSourceReversalJob(ctx context.Context, job jobs.Job, recovery bool) error {
	if s == nil || s.pool == nil {
		return ErrDisabled
	}
	var payload sellerSourceReversalPayload
	if job.ID == uuid.Nil || json.Unmarshal(job.Payload, &payload) != nil || payload.CommandID == uuid.Nil {
		return ErrSellerSourceReversalInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	state, err := lockSellerSourceReversalTx(ctx, tx, payload.CommandID)
	if err != nil {
		return err
	}
	var bound bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs j WHERE j.id=$2
 AND j.payload=jsonb_build_object('commandId',$1::uuid)
 AND ((NOT $3 AND j.id=$4 AND j.kind='payment.reverse_seller_source')
 OR ($3 AND j.kind='payment.check_seller_source_reversal'
 AND EXISTS(SELECT 1 FROM seller_source_reversal_checks WHERE command_id=$1 AND job_id=j.id))))`,
		payload.CommandID, job.ID, recovery, state.JobID).Scan(&bound); err != nil {
		return err
	}
	if !bound || recovery && !state.Started {
		return ErrSellerSourceReversalInvalid
	}
	if state.Parent == "cancelled" {
		var closed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE command_id=$1)`, payload.CommandID).Scan(&closed); err != nil {
			return err
		}
		if !closed {
			return ErrSellerSourceReversalInvalid
		}
		return tx.Commit(ctx)
	}
	runtime, err := s.runtimes.Runtime(state.Identity.Provider)
	if err != nil {
		return err
	}
	if _, ok := runtime.(TransferReversalProvider); !ok {
		return ErrProviderUnavailable
	}
	create := !recovery && !state.Started
	var readID uuid.UUID
	var deadline time.Time
	if create {
		if err := s.validateSellerSourceReversalExecutionTx(ctx, tx, state, job.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO seller_source_reversal_dispatches(command_id,job_id,started_at,deadline_at)
 SELECT $1,$2,t,t+interval '20 seconds' FROM clock_timestamp() t`, payload.CommandID, job.ID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO seller_source_reversal_reads(command_id,kind,started_at,deadline_at)
 SELECT command_id,'create',started_at,deadline_at FROM seller_source_reversal_dispatches WHERE command_id=$1 RETURNING id,deadline_at`,
			payload.CommandID).Scan(&readID, &deadline); err != nil {
			return err
		}
		if err := recordSellerPayoutEventTx(ctx, tx, state.Input.PayoutRequestID, "source_reversal.started", &state.Parent, &state.Parent,
			"source-reversal-started:"+payload.CommandID.String(), map[string]any{"commandId": payload.CommandID, "jobId": job.ID}); err != nil {
			return err
		}
	} else if err := tx.QueryRow(ctx, `INSERT INTO seller_source_reversal_reads(command_id,kind,started_at,deadline_at)
 SELECT $1,'query',t,t+interval '20 seconds' FROM clock_timestamp() t RETURNING id,deadline_at`, payload.CommandID).Scan(&readID, &deadline); err != nil {
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
	state, err = lockSellerSourceReversalTx(callCtx, tx, payload.CommandID)
	if err != nil {
		return err
	}
	var finished bool
	if err := tx.QueryRow(callCtx, `SELECT finished_at IS NOT NULL FROM seller_source_reversal_reads WHERE id=$1`, readID).Scan(&finished); err != nil {
		return err
	}
	if finished {
		return tx.Commit(callCtx)
	}
	result := TransferReversalResult{Outcome: "incomplete", Observations: []TransferReversal{}}
	runtime, callErr := s.runtimes.Runtime(state.Identity.Provider)
	if callErr == nil {
		var original, current ProductCheckoutIdentity
		original, _, callErr = readOriginalProductPaymentIdentity(callCtx, tx, state.Input.PaymentID)
		if callErr == nil && original != state.Identity {
			callErr = ErrCheckoutReconciliation
		}
		if callErr == nil {
			current, callErr = checkoutIdentity(callCtx, runtime)
			if callErr == nil && current != original {
				callErr = ErrCheckoutReconciliation
			}
		}
	}
	guardRejected := false
	if create && callErr == nil {
		guard, err := tx.Begin(callCtx)
		if err != nil {
			return err
		}
		callErr = s.validateSellerSourceReversalExecutionTx(callCtx, guard, state, job.ID)
		if callErr != nil {
			guardRejected = true
			if err := guard.Rollback(callCtx); err != nil {
				return err
			}
		} else if err := guard.Commit(callCtx); err != nil {
			return err
		}
	}
	if callErr == nil {
		provider, ok := runtime.(TransferReversalProvider)
		if !ok {
			callErr = ErrProviderUnavailable
		} else if create {
			result, callErr = provider.CreateTransferReversal(callCtx, state.Input)
		} else {
			result, callErr = provider.LookupTransferReversal(callCtx, state.Input)
		}
	}
	if callErr == nil {
		callErr = callCtx.Err()
	}
	result, review := validateSellerSourceReversalRead(state.Input, result, create, callErr)
	review = review || guardRejected
	if !time.Now().Before(deadline) {
		result.Outcome, callErr = "error", context.DeadlineExceeded
	}
	saveCtx, stopSave := context.WithDeadline(context.WithoutCancel(ctx), deadline.Add(5*time.Second))
	defer stopSave()
	if len(result.Observations) == 1 {
		var conflict bool
		if err := tx.QueryRow(saveCtx, `SELECT EXISTS(SELECT 1 FROM seller_source_reversal_reads r WHERE r.command_id=$1
 AND r.finished_at IS NOT NULL AND (r.requires_review OR EXISTS(SELECT 1 FROM jsonb_array_elements(r.evidence->'observations') o
 WHERE o->>'providerId' IS DISTINCT FROM $2)))
 OR EXISTS(SELECT 1 FROM seller_source_reversal_results WHERE command_id=$1 AND provider_reversal_id<>$2)`,
			payload.CommandID, result.Observations[0].ProviderID).Scan(&conflict); err != nil {
			return err
		}
		review = review || conflict
	}
	evidence, err := json.Marshal(result)
	if err != nil {
		return err
	}
	code := sourceReversalErrorCode(callErr)
	if review && code == "" {
		code = "payment_reconciliation_required"
	}
	if err := tx.QueryRow(saveCtx, `UPDATE seller_source_reversal_reads SET finished_at=t,
 outcome=CASE WHEN t>deadline_at THEN 'error' ELSE $2 END,requires_review=$3,
 error_code=CASE WHEN t>deadline_at THEN 'payment_provider_timeout' ELSE $4 END,evidence=$5
 FROM clock_timestamp() t WHERE id=$1 RETURNING outcome,error_code`, readID, result.Outcome, review, code, evidence).Scan(&result.Outcome, &code); err != nil {
		return err
	}
	accepted := !review && code == "" && result.Outcome == "found"
	if accepted {
		if _, err := tx.Exec(saveCtx, `INSERT INTO seller_source_reversal_results(command_id,read_id,provider_reversal_id)
 VALUES($1,$2,$3) ON CONFLICT(command_id) DO NOTHING`, payload.CommandID, readID, result.Observations[0].ProviderID); err != nil {
			return err
		}
		if _, err := tx.Exec(saveCtx, `UPDATE seller_source_reversal_reads SET finished_at=clock_timestamp(),outcome='superseded',
 error_code='source_reversal_read_superseded',evidence=jsonb_build_object('observations','[]'::jsonb,'supersededBy',$2::uuid)
 WHERE command_id=$1 AND id<>$2 AND finished_at IS NULL`, payload.CommandID, readID); err != nil {
			return err
		}
	}
	// No release or balance credit here. A full source return still needs the
	// independent bank/debt/allocation checks of the ledger-closing transaction.
	status, failure := "reconciliation_required", "source_reversal_pending"
	if accepted {
		failure = "source_reversal_observed"
	}
	if _, err := tx.Exec(saveCtx, `UPDATE seller_payout_requests SET status=$2,failure_code=$3,updated_at=clock_timestamp() WHERE id=$1`,
		state.Input.PayoutRequestID, status, failure); err != nil {
		return err
	}
	if err := recordSellerPayoutEventTx(saveCtx, tx, state.Input.PayoutRequestID, "source_reversal.checked", &state.Parent, &status,
		"source-reversal-read:"+readID.String(), map[string]any{"commandId": payload.CommandID, "readId": readID, "outcome": result.Outcome, "requiresReview": review}); err != nil {
		return err
	}
	if _, err := tx.Exec(saveCtx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 VALUES('seller_payout.source_reversal_checked','seller_payout_request',$1,$2,
 jsonb_build_object('commandId',$3::uuid,'readId',$4::uuid,'accepted',$5::boolean))`,
		state.Input.PayoutRequestID, "source-reversal-read:"+readID.String(), payload.CommandID, readID, accepted); err != nil {
		return err
	}
	if accepted {
		request := state.Input.PayoutRequestID
		if err := notifications.CreateTx(saveCtx, tx, notifications.CreateInput{
			UserID: state.SellerID, Kind: "marketplace.payout_source_return_observed",
			Title:      "Payout source return observed",
			Body:       "The payment source confirmed a full return. Funds remain reserved until the ledger review is closed.",
			TargetPath: "/workspace/payouts/" + request.String(), ResourceType: "seller_payout_request", ResourceID: &request,
			SourceKey: "marketplace:source-return-observed:" + payload.CommandID.String(),
		}); err != nil {
			return err
		}
	}
	if err := tx.Commit(saveCtx); err != nil {
		return err
	}
	if !accepted {
		return newProviderFailure("payment_settlement_pending", 5*time.Minute)
	}
	return nil
}

func sourceReversalErrorCode(err error) string {
	// Losing the transport or caller does not contradict a later authenticated
	// result. Preserve uncertainty without making it a permanent review hold.
	if errors.Is(err, context.DeadlineExceeded) {
		return "payment_provider_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "payment_request_failed"
	}
	return fundingErrorCode(err)
}

func validateSellerSourceReversalRead(input TransferReversalRequest, result TransferReversalResult, create bool, callErr error) (TransferReversalResult, bool) {
	if result.Observations == nil {
		result.Observations = []TransferReversal{}
	}
	valid := result.Pages >= 0 && result.Pages <= 10 && len(result.Observations) <= 2
	parent := result.Transfer
	parentValid := validTransferObservation(input.TransferLookupRequest, parent) && parent.ProviderID == input.ProviderTransferID &&
		!parent.CreatedAt.After(input.ReservedAt.Add(5*time.Minute))
	// An early network failure can have no parent; retain uncertainty. A
	// nonempty wrong parent is contradictory evidence, even alongside an error.
	valid = valid && (parentValid || callErr != nil && parent == (TransferObservation{}))
	seen := map[string]bool{}
	for _, item := range result.Observations {
		valid = valid && validStripeID(item.ProviderID, "trr_") && !seen[item.ProviderID] && item.CommandID == input.CommandID &&
			item.ProviderTransferID == input.ProviderTransferID && item.AmountCents == input.ReverseAmountCents && item.Currency == input.Currency &&
			!item.CreatedAt.Before(input.ReservedAt.Add(-5*time.Minute)) && !item.CreatedAt.After(input.ReservedAt.Add(stripeReversalDispatchWindow+5*time.Minute)) &&
			!item.CreatedAt.After(time.Now().Add(time.Minute)) && parentValid && !item.CreatedAt.Before(parent.CreatedAt)
		seen[item.ProviderID] = true
	}
	if callErr == nil {
		valid = valid && (create || result.Pages > 0) && ((result.Outcome == "found" && len(result.Observations) == 1 &&
			parent.AmountReversed == input.PriorReversedCents+input.ReverseAmountCents) ||
			(result.Outcome == "not_found" && len(result.Observations) == 0) ||
			(result.Outcome == "ambiguous" && len(result.Observations) == 2) || result.Outcome == "incomplete")
	}
	review := !valid || len(result.Observations) > 1 || oneOf(sourceReversalErrorCode(callErr), "payment_response_invalid", "payment_reconciliation_required") || errors.Is(callErr, ErrCheckoutReconciliation)
	if len(result.Observations) > 2 {
		result.Observations = result.Observations[:2]
	}
	if !valid || callErr != nil {
		result.Outcome = "error"
	}
	return result, review
}
