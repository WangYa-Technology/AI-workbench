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

const SellerPayoutFundingJobKind = "payment.fund_seller_payout"

type sellerPayoutFundingPayload struct {
	TransferID uuid.UUID `json:"transferId"`
}

// Admission must bind the latest review and reserve the source in the same
// transaction. The database requires that admission before new dispatches.
func enqueueSellerPayoutFundingTx(ctx context.Context, tx pgx.Tx, transferID uuid.UUID) (uuid.UUID, error) {
	var existing uuid.UUID
	err := tx.QueryRow(ctx, `SELECT job_id FROM seller_payout_funding_dispatches WHERE transfer_id=$1`, transferID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	body, err := json.Marshal(sellerPayoutFundingPayload{TransferID: transferID})
	if err != nil {
		return uuid.Nil, err
	}
	var jobID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,10000) RETURNING id`, SellerPayoutFundingJobKind, body).Scan(&jobID); err != nil {
		return uuid.Nil, err
	}
	result, err := tx.Exec(ctx, `INSERT INTO seller_payout_funding_dispatches(transfer_id,job_id,payment_id,provider_charge_id)
 SELECT t.id,$2,p.id,p.provider_charge_id FROM seller_payout_transfers t
 JOIN product_settlements s ON s.id=t.settlement_id JOIN payment_intents p ON p.id=s.payment_id WHERE t.id=$1`, transferID, jobID)
	if err != nil {
		return uuid.Nil, err
	}
	if result.RowsAffected() != 1 {
		return uuid.Nil, ErrCheckoutReconciliation
	}
	return jobID, nil
}

type sellerFundingState struct {
	TransferID, RequestID uuid.UUID
	Settlement            ProductSettlement
	Input                 TransferLookupRequest
	Identity              ProductCheckoutIdentity
	Status, RequestStatus string
	StartedAt             *time.Time
}

func lockSellerFundingTx(ctx context.Context, tx pgx.Tx, transferID, jobID uuid.UUID, recovery bool) (sellerFundingState, error) {
	state := sellerFundingState{TransferID: transferID}
	if err := tx.QueryRow(ctx, `SELECT t.settlement_id FROM seller_payout_funding_dispatches d
 JOIN seller_payout_transfers t ON t.id=d.transfer_id JOIN jobs j ON j.id=$2
 WHERE t.id=$1 AND j.payload=jsonb_build_object('transferId',t.id)
 AND ((NOT $3 AND d.job_id=j.id AND j.kind='payment.fund_seller_payout')
 OR ($3 AND d.started_at IS NOT NULL AND j.kind='payment.check_seller_payout_funding'
 AND EXISTS(SELECT 1 FROM seller_payout_funding_checks c WHERE c.job_id=j.id AND c.transfer_id=t.id)))`,
		transferID, jobID, recovery).Scan(&state.Settlement.ID); err != nil {
		return state, err
	}
	if err := lockProductSettlementPaymentTx(ctx, tx, state.Settlement.ID); err != nil {
		return state, err
	}
	var identity []byte
	var dispatchKey string
	err := tx.QueryRow(ctx, `SELECT t.payout_request_id,t.status,r.status,d.started_at,t.provider_identity,
 d.payment_id,d.provider_charge_id,t.destination_id,t.amount_cents,t.currency,t.live_mode,
 s.payment_id,s.provider,s.live_mode,s.gross_amount_cents,s.currency,s.seller_id,t.dispatch_key
 FROM seller_payout_funding_dispatches d JOIN seller_payout_transfers t ON t.id=d.transfer_id
 JOIN seller_payout_requests r ON r.id=t.payout_request_id JOIN product_settlements s ON s.id=t.settlement_id
 WHERE t.id=$1 FOR UPDATE OF d,t,r,s`, transferID).Scan(
		&state.RequestID, &state.Status, &state.RequestStatus, &state.StartedAt, &identity,
		&state.Input.PaymentID, &state.Input.ProviderChargeID, &state.Input.DestinationID, &state.Input.AmountCents, &state.Input.Currency, &state.Input.LiveMode,
		&state.Settlement.PaymentID, &state.Settlement.Provider, &state.Settlement.LiveMode, &state.Settlement.GrossAmountCents, &state.Settlement.Currency, &state.Settlement.SellerID, &dispatchKey)
	if err != nil {
		return state, err
	}
	if json.Unmarshal(identity, &state.Identity) != nil || state.Settlement.PaymentID != state.Input.PaymentID ||
		state.Identity.Provider != "stripe" || state.Identity.LiveMode != state.Input.LiveMode {
		return state, ErrCheckoutReconciliation
	}
	if dispatchKey == "seller-source-"+state.RequestID.String() {
		state.Input.SourceRequestID = state.RequestID
	}
	if dispatchKey != transferDispatchKey(state.Input.TransferRequest) {
		return state, ErrCheckoutReconciliation
	}
	return state, nil
}

func (s *Service) validateSellerFundingDispatchTx(ctx context.Context, tx pgx.Tx, state sellerFundingState) error {
	if !s.config.Enabled {
		return ErrDisabled
	}
	var actor, review uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT actor_id,review_id FROM seller_payout_funding_admissions WHERE transfer_id=$1`, state.TransferID).Scan(&actor, &review); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSellerFundingAdmissionConflict
		}
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT assert_seller_funding_approval($1,$2,$3)`, state.TransferID, actor, review); err != nil {
		return err
	}
	if err := validateProductSettlementFundsTx(ctx, tx, state.Settlement); err != nil {
		return err
	}
	// Pin mutable eligibility, then evaluate it in a fresh Read Committed
	// statement so a revocation committed during a lock wait is observed.
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' FOR SHARE`, state.Settlement.SellerID).Scan(&id); err != nil {
		return err
	}
	var destination, mode string
	if err := tx.QueryRow(ctx, `SELECT destination_id FROM payment_destinations WHERE provider='stripe' AND user_id=$1 FOR SHARE`, id).Scan(&destination); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT payout_mode FROM product_settlement_settings WHERE singleton=true FOR SHARE`).Scan(&mode); err != nil {
		return err
	}
	var eligible bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_payout_transfers t
 JOIN product_settlements ps ON ps.id=t.settlement_id JOIN payment_intents p ON p.id=ps.payment_id
 JOIN orders o ON o.id=ps.order_id JOIN seller_payout_requests r ON r.id=t.payout_request_id
 JOIN users u ON u.id=r.seller_id JOIN payment_destinations pd ON pd.provider=t.provider AND pd.user_id=r.seller_id
 JOIN product_settlement_settings settings ON settings.singleton=true
 WHERE t.id=$1 AND t.status IN ('requested','processing') AND r.status IN ('requested','under_review','processing')
 AND u.status='active' AND settings.payout_mode='seller_payout' AND ps.status='available'
 AND ps.available_at<=clock_timestamp() AND p.status='paid' AND o.status='fulfilled'
 AND p.provider_charge_id=$2 AND pd.destination_id=t.destination_id AND pd.status='verified' AND pd.charges_enabled AND pd.payouts_enabled
 AND pd.original_merchant_id=t.provider_identity->>'merchantId'
 AND COALESCE(pd.original_store_id,'')=COALESCE(t.provider_identity->>'storeId','')
 AND pd.original_live_mode=t.live_mode AND pd.original_endpoint=t.provider_identity->>'endpoint'
 AND pd.original_api_version=t.provider_identity->>'apiVersion' AND pd.original_request_version=t.provider_identity->>'requestVersion'
 AND EXISTS(SELECT 1 FROM seller_payout_request_allocations a WHERE a.payout_request_id=r.id AND a.settlement_id=ps.id AND a.released_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches d WHERE d.settlement_id=ps.id AND d.reserved_at IS NOT NULL)
 AND NOT seller_funds_recovery_blocks(r.seller_id,ps.id)
 )`, state.TransferID, state.Input.ProviderChargeID).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible {
		return ErrCheckoutReconciliation
	}
	return nil
}

func authenticateSellerFunding(ctx context.Context, tx pgx.Tx, runtime ProviderRuntime, state sellerFundingState) error {
	original, _, err := readOriginalProductPaymentIdentity(ctx, tx, state.Input.PaymentID)
	if err != nil {
		return err
	}
	if original != state.Identity {
		return ErrCheckoutReconciliation
	}
	current, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return err
	}
	if current != original {
		return ErrCheckoutReconciliation
	}
	return nil
}

func finalizeSkippedSellerFundingReads(ctx context.Context, tx pgx.Tx, transferID uuid.UUID, terminalStatus string) error {
	evidence, err := json.Marshal(map[string]any{
		"status": "skipped", "terminalTransferStatus": terminalStatus,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE seller_payout_funding_reads
 SET finished_at=clock_timestamp(),outcome='skipped',error_code='terminal_transfer_already_recorded',requires_review=false,evidence=$2
 WHERE transfer_id=$1 AND finished_at IS NULL`, transferID, evidence)
	return err
}

// HandleSellerPayoutFundingJob moves a single reserved source charge to its
// frozen Connect account. Only the invocation that commits started_at may POST;
// every replay uses authenticated reads, including after a crash before POST.
func (s *Service) HandleSellerPayoutFundingJob(ctx context.Context, job jobs.Job) error {
	return s.handleSellerPayoutFundingJob(ctx, job, false)
}

func (s *Service) handleSellerPayoutFundingJob(ctx context.Context, job jobs.Job, recovery bool) error {
	var payload sellerPayoutFundingPayload
	if job.ID == uuid.Nil || json.Unmarshal(job.Payload, &payload) != nil || payload.TransferID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if s == nil || s.pool == nil {
		return ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	state, err := lockSellerFundingTx(ctx, tx, payload.TransferID, job.ID, recovery)
	if err != nil {
		return err
	}
	if oneOf(state.Status, "succeeded", "failed") {
		if err := finalizeSkippedSellerFundingReads(ctx, tx, state.TransferID, state.Status); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	runtime, err := s.runtimes.Runtime(state.Identity.Provider)
	if err != nil {
		return err
	}
	reader, ok := runtime.(TransferLookupReader)
	if !ok || !runtimeCapabilities(runtime).Transfer {
		return newProviderFailure("payment_provider_unsupported", 0)
	}
	create := !recovery && state.StartedAt == nil
	readID := uuid.Nil
	var readDeadline time.Time
	if create {
		if err := requireTransferExecutionJobTx(ctx, tx, job.ID, SellerPayoutFundingJobKind, payload); err != nil {
			return err
		}
		if err := s.validateSellerFundingDispatchTx(ctx, tx, state); err != nil {
			return err
		}
		if state.Status != "requested" {
			return ErrCheckoutReconciliation
		}
		if _, err := tx.Exec(ctx, `UPDATE seller_payout_funding_dispatches SET started_at=clock_timestamp() WHERE transfer_id=$1`, state.TransferID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE seller_payout_transfers SET status='processing',updated_at=clock_timestamp() WHERE id=$1`, state.TransferID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE seller_payout_requests SET status='processing',updated_at=clock_timestamp() WHERE id=$1`, state.RequestID); err != nil {
			return err
		}
		to := "processing"
		if err := recordSellerPayoutEventTx(ctx, tx, state.RequestID, "funding.started", &state.RequestStatus, &to,
			"funding:"+state.TransferID.String(), map[string]any{"transferId": state.TransferID, "jobId": job.ID}); err != nil {
			return err
		}
	} else {
		if err := tx.QueryRow(ctx, `INSERT INTO seller_payout_funding_reads(transfer_id) VALUES($1) RETURNING id,read_deadline`, state.TransferID).Scan(&readID, &readDeadline); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// The durable read budget includes pool and lock waits after registration.
	// A paused worker must not acquire a fresh network window when it resumes.
	workCtx := ctx
	if !create {
		var cancelRead context.CancelFunc
		workCtx, cancelRead = context.WithDeadline(ctx, readDeadline)
		defer cancelRead()
	}
	tx, err = s.pool.Begin(workCtx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	state, err = lockSellerFundingTx(workCtx, tx, payload.TransferID, job.ID, recovery)
	if err != nil {
		return err
	}
	if oneOf(state.Status, "succeeded", "failed") {
		// Another worker may have reached a terminal source result after this
		// invocation registered its read. Close every unfinished reservation so
		// crash/retry dashboards do not retain an open observation.
		if err := finalizeSkippedSellerFundingReads(workCtx, tx, state.TransferID, state.Status); err != nil {
			return err
		}
		return tx.Commit(workCtx)
	}
	deadline := time.Now().Add(20 * time.Second)
	if !create && readDeadline.Before(deadline) {
		deadline = readDeadline
	}
	if create && state.StartedAt != nil && state.StartedAt.Add(23*time.Hour).Before(deadline) {
		deadline = state.StartedAt.Add(23 * time.Hour)
	}
	callCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	callErr := authenticateSellerFunding(callCtx, tx, runtime, state)
	var transfer Transfer
	result := TransferLookupResult{Observations: []TransferObservation{}}
	if create {
		if state.StartedAt == nil || !time.Now().Before(state.StartedAt.Add(23*time.Hour)) {
			callErr = ErrCheckoutReconciliation
		}
		if callErr == nil {
			callErr = s.validateSellerFundingDispatchTx(callCtx, tx, state)
		}
		if callErr == nil {
			callErr = requireTransferExecutionJobTx(callCtx, tx, job.ID, SellerPayoutFundingJobKind, payload)
		}
		if callErr == nil {
			transfer, callErr = runtime.CreateTransfer(callCtx, state.Input.TransferRequest)
		}
		if callErr == nil && (!validStripeID(transfer.ProviderID, "tr_") || transfer.DestinationID != state.Input.DestinationID ||
			transfer.AmountCents != state.Input.AmountCents || transfer.Currency != state.Input.Currency || transfer.TransferGroup != transferGroup(state.Input.PaymentID)) {
			callErr = newProviderFailure("payment_response_invalid", 0)
		}
	} else if callErr == nil {
		state.Input.ReturnedSources, callErr = returnedSellerSources(callCtx, tx, state.Input.PaymentID)
		if callErr == nil {
			result, callErr = reader.LookupProductTransfer(callCtx, state.Input)
		}
	}
	if callErr == nil {
		callErr = callCtx.Err()
	}
	// Preserve observations returned before a later page failed. Persist under a
	// bounded independent context, even when the worker lost its lease mid-call.
	saveDeadline := time.Now().Add(5 * time.Second)
	if !create && readDeadline.Add(5*time.Second).Before(saveDeadline) {
		saveDeadline = readDeadline.Add(5 * time.Second)
	}
	saveCtx, stopSave := context.WithDeadline(context.WithoutCancel(ctx), saveDeadline)
	defer stopSave()
	success := callErr == nil
	if !create {
		review := false
		result, review, callErr = validateSellerFundingRead(state.Input, result, callErr)
		for _, item := range result.Observations {
			if state.StartedAt == nil || item.CreatedAt.Before(state.StartedAt.Add(-5*time.Minute)) {
				review, callErr, result.Outcome = true, ErrCheckoutReconciliation, "error"
			}
		}
		success = callErr == nil && result.Outcome == "found"
		if success {
			transfer = result.Observations[0].Transfer
			var conflict bool
			if err := tx.QueryRow(saveCtx, `SELECT EXISTS(SELECT 1 FROM seller_payout_funding_reads r
 WHERE transfer_id=$1 AND finished_at IS NOT NULL AND (requires_review OR EXISTS(
 SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(r.evidence->'observations','null'::jsonb),'[]'::jsonb)) item
 WHERE item->>'ProviderID' IS DISTINCT FROM $2 OR (item->>'amountReversed')::integer<>0)))`, state.TransferID, transfer.ProviderID).Scan(&conflict); err != nil {
				return err
			}
			if conflict {
				success, review = false, true
				callErr = ErrCheckoutReconciliation
			}
		}
		body, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(saveCtx, `UPDATE seller_payout_funding_reads SET finished_at=clock_timestamp(),outcome=$2,error_code=$3,requires_review=$4,evidence=$5 WHERE id=$1`,
			readID, result.Outcome, fundingErrorCode(callErr), review, body); err != nil {
			return err
		}
	}
	if err := finishSellerFundingTx(saveCtx, tx, state, transfer, readID, success, callErr); err != nil {
		return err
	}
	if success {
		if err := finalizeSkippedSellerFundingReads(saveCtx, tx, state.TransferID, "succeeded"); err != nil {
			return err
		}
	}
	if err := tx.Commit(saveCtx); err != nil {
		return err
	}
	if !success {
		return newProviderFailure("payment_settlement_pending", 5*time.Minute)
	}
	return nil
}

func fundingErrorCode(err error) string {
	if err == nil {
		return ""
	}
	return safeSettlementErrorCode(err)
}

func validateSellerFundingRead(input TransferLookupRequest, result TransferLookupResult, readErr error) (TransferLookupResult, bool, error) {
	// Failed reads may return a zero-value slice; persist an empty array so
	// later checks can inspect the history without treating JSON null as an array.
	if result.Observations == nil {
		result.Observations = []TransferObservation{}
	}
	valid := result.Pages >= 0 && result.Pages <= 10 && len(result.Observations) <= 2
	seen := map[string]bool{}
	review := false
	for _, item := range result.Observations {
		valid = valid && validTransferObservation(input, item) && !seen[item.ProviderID]
		seen[item.ProviderID] = true
		review = review || item.AmountReversed != 0
	}
	if readErr == nil {
		valid = valid && result.Pages > 0 && ((result.Outcome == "found" && len(result.Observations) == 1) ||
			(result.Outcome == "not_found" && len(result.Observations) == 0) ||
			(result.Outcome == "ambiguous" && len(result.Observations) == 2) || result.Outcome == "incomplete")
	}
	if !valid {
		review = true
		readErr = newProviderFailure("payment_response_invalid", 0)
	}
	if len(result.Observations) > 2 {
		result.Observations = result.Observations[:2]
	}
	review = review || len(result.Observations) > 1
	if readErr != nil {
		result.Outcome = "error"
	} else if result.Outcome == "found" && review {
		result.Outcome = "reversed"
	}
	return result, review, readErr
}

func finishSellerFundingTx(ctx context.Context, tx pgx.Tx, state sellerFundingState, transfer Transfer, readID uuid.UUID, success bool, cause error) error {
	status, event := "reconciliation_required", "funding.reconciliation_required"
	var providerID *string
	if success {
		status, event, providerID = "succeeded", "funding.confirmed", &transfer.ProviderID
	}
	evidenceKey := "dispatch"
	if readID != uuid.Nil {
		evidenceKey = "read:" + readID.String()
	}
	evidence, err := json.Marshal(map[string]any{evidenceKey: map[string]any{
		"status": status, "errorCode": fundingErrorCode(cause), "providerTransferId": providerID,
	}})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE seller_payout_transfers SET status=$2,provider_transfer_id=$3,error_code=$4,
 evidence=evidence||$5::jsonb,updated_at=clock_timestamp() WHERE id=$1`, state.TransferID, status, providerID, fundingErrorCode(cause), evidence); err != nil {
		return err
	}
	// Funding success retains the payout reservation. Only separately confirmed
	// bank evidence can eventually complete the parent; this handler cannot.
	to := state.RequestStatus
	if !success {
		to = "reconciliation_required"
		if _, err := tx.Exec(ctx, `UPDATE seller_payout_requests SET status=$2,updated_at=clock_timestamp() WHERE id=$1`, state.RequestID, to); err != nil {
			return err
		}
	}
	return recordSellerPayoutEventTx(ctx, tx, state.RequestID, event, &state.RequestStatus, &to,
		"funding:"+state.TransferID.String()+":"+evidenceKey, map[string]any{"transferId": state.TransferID, "readId": readID, "providerTransferId": providerID, "errorCode": fundingErrorCode(cause)})
}
