package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

type productSettlementJobPayload struct {
	SettlementID uuid.UUID `json:"settlementId"`
}

// ProductSettlement is the seller-facing financial snapshot for one accepted
// product order. Gross order amounts are kept in SellerSale; this projection
// records the immutable fee and net amount used for a payout decision.
type ProductSettlement struct {
	ID                  uuid.UUID  `json:"id"`
	OrderID             uuid.UUID  `json:"orderId"`
	PaymentID           uuid.UUID  `json:"paymentId"`
	SellerID            uuid.UUID  `json:"sellerId"`
	Provider            string     `json:"provider"`
	LiveMode            bool       `json:"liveMode"`
	GrossAmountCents    int        `json:"grossAmountCents"`
	FeeBPS              int        `json:"feeBps"`
	FeeCents            int        `json:"feeCents"`
	NetAmountCents      int        `json:"netAmountCents"`
	Currency            string     `json:"currency"`
	Status              string     `json:"status"`
	HoldReason          string     `json:"holdReason"`
	AvailableAt         time.Time  `json:"availableAt"`
	PayoutBatchID       *uuid.UUID `json:"payoutBatchId,omitempty"`
	DestinationID       *string    `json:"destinationId,omitempty"`
	ProviderTransferID  *string    `json:"providerTransferId,omitempty"`
	RecoveryAmountCents int        `json:"recoveryAmountCents"`
	TransferredAt       *time.Time `json:"transferredAt,omitempty"`
	Version             int64      `json:"version"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// ensureProductSettlementTx creates the immutable economic snapshot and one
// durable dispatch job. It is safe to call from every successful fulfillment
// replay: payment/order locks make the insert and dispatch binding idempotent.
func (s *Service) ensureProductSettlementTx(ctx context.Context, tx pgx.Tx, orderID, paymentID uuid.UUID) error {
	var provider string
	var sellerID *uuid.UUID
	var liveMode bool
	var gross int
	var currency, orderStatus string
	var paidAt *time.Time
	var refundWindowDays int
	if err := tx.QueryRow(ctx, `
		SELECT pi.provider,pi.payee_id,pi.live_mode,pi.amount_cents,pi.currency,pi.paid_at,
		       o.status,o.refund_window_days_snapshot
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
		WHERE pi.id=$1 AND pi.order_id=$2 AND pi.purpose='product'
		FOR UPDATE OF pi,o`, paymentID, orderID).Scan(
		&provider, &sellerID, &liveMode, &gross, &currency, &paidAt, &orderStatus, &refundWindowDays); err != nil {
		return err
	}
	if sellerID == nil || gross <= 0 || currency != "USD" || orderStatus != "fulfilled" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	// Fulfillment replays retain the original fee and hold even after policy edits.
	var existingID uuid.UUID
	var matches bool
	err := tx.QueryRow(ctx, `SELECT id,order_id=$2 AND seller_id=$3 AND provider=$4 AND live_mode=$5
	 AND gross_amount_cents=$6 AND currency=$7 FROM product_settlements WHERE payment_id=$1 FOR UPDATE`,
		paymentID, orderID, *sellerID, provider, liveMode, gross, currency).Scan(&existingID, &matches)
	if err == nil {
		if !matches {
			return ErrCheckoutReconciliation
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var feeBPS, holdDays int
	if err := tx.QueryRow(ctx, `SELECT platform_fee_bps,hold_days FROM product_settlement_settings WHERE singleton=true FOR SHARE`).Scan(&feeBPS, &holdDays); err != nil {
		return err
	}
	fee64 := (int64(gross)*int64(feeBPS) + 9999) / 10000
	if fee64 < 0 || fee64 > int64(gross) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	fee := int(fee64)
	base := time.Now().UTC()
	if paidAt != nil && !paidAt.IsZero() {
		base = paidAt.UTC()
	}
	if refundWindowDays > holdDays {
		holdDays = refundWindowDays
	}
	availableAt := base.Add(time.Duration(holdDays) * 24 * time.Hour)
	settlementID := uuid.New()
	var createdID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO product_settlements(
		  id,order_id,payment_id,seller_id,provider,live_mode,gross_amount_cents,fee_bps,fee_cents,net_amount_cents,
		  currency,status,hold_reason,available_at,transfer_idempotency_key)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'pending_hold','refund_window',$12,$13)
		RETURNING id`, settlementID, orderID, paymentID, *sellerID, provider, liveMode, gross, feeBPS, fee, gross-fee, currency, availableAt,
		"product-settlement:"+paymentID.String()).Scan(&createdID)
	if err != nil {
		return err
	}
	if err := ensureSellerSettlementCreditTx(ctx, tx, createdID, *sellerID, gross-fee, currency, availableAt, "pending_hold"); err != nil {
		return err
	}
	payload, err := json.Marshal(productSettlementJobPayload{SettlementID: createdID})
	if err != nil {
		return err
	}
	var jobID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT job_id FROM product_settlement_dispatches WHERE settlement_id=$1 FOR UPDATE`, createdID).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			INSERT INTO jobs(kind,payload,max_attempts,available_at)
			VALUES($1,$2,10000,$3) RETURNING id`, ProductSettlementJobKind, payload, availableAt).Scan(&jobID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO product_settlement_dispatches(settlement_id,job_id) VALUES($1,$2)`, createdID, jobID); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func ensureSellerSettlementCreditTx(ctx context.Context, tx pgx.Tx, settlementID, sellerID uuid.UUID, amount int, currency string, availableAt time.Time, status string) error {
	if settlementID == uuid.Nil || sellerID == uuid.Nil || amount <= 0 || currency != "USD" {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO seller_ledger_entries(seller_id,entry_type,amount_cents,currency,settlement_id,idempotency_key,available_at,evidence)
		VALUES($1,'settlement_credit',$2,$3,$4,$5,$6,jsonb_build_object('status',$7::text))
		ON CONFLICT DO NOTHING`, sellerID, amount, currency, settlementID, "settlement-credit:"+settlementID.String(), availableAt, status)
	return err
}

func recordProductSettlementEventTx(ctx context.Context, tx pgx.Tx, settlementID uuid.UUID, eventType, fromStatus, toStatus string, evidence map[string]any) error {
	body, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO product_settlement_events(settlement_id,event_type,from_status,to_status,evidence,event_key)
		SELECT id,$2,$3,$4,$5,$2||':'||version::text FROM product_settlements WHERE id=$1
		ON CONFLICT(settlement_id,event_key) DO NOTHING`, settlementID, eventType, nullableSettlementStatus(fromStatus), toStatus, body)
	return err
}

func nullableSettlementStatus(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// HandleProductSettlementJob waits for the immutable hold, then performs one
// provider transfer. An outbound error is treated as an unknown financial
// result and quarantined for reconciliation; it is never blindly retried.
func (s *Service) HandleProductSettlementJob(ctx context.Context, job jobs.Job) error {
	var payload productSettlementJobPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.SettlementID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if s == nil || s.pool == nil || !s.config.Enabled {
		return ErrDisabled
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := lockProductSettlementPaymentTx(ctx, tx, payload.SettlementID); err != nil {
		return err
	}
	var settlement ProductSettlement
	var providerChargeID *string
	var paymentStatus, orderStatus string
	var sellerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT ps.id,ps.order_id,ps.payment_id,ps.seller_id,ps.provider,ps.live_mode,ps.gross_amount_cents,
		       ps.fee_bps,ps.fee_cents,ps.net_amount_cents,ps.currency,ps.status,ps.hold_reason,ps.available_at,
		       ps.payout_batch_id,ps.destination_id,ps.provider_transfer_id,ps.recovery_amount_cents,ps.transferred_at,
		       ps.version,ps.created_at,ps.updated_at,pi.status,o.status,pi.provider_charge_id
		FROM product_settlements ps
		JOIN orders o ON o.id=ps.order_id
		JOIN payment_intents pi ON pi.id=ps.payment_id
		WHERE ps.id=$1
		FOR UPDATE OF ps`, payload.SettlementID).Scan(
		&settlement.ID, &settlement.OrderID, &settlement.PaymentID, &sellerID, &settlement.Provider, &settlement.LiveMode,
		&settlement.GrossAmountCents, &settlement.FeeBPS, &settlement.FeeCents, &settlement.NetAmountCents, &settlement.Currency,
		&settlement.Status, &settlement.HoldReason, &settlement.AvailableAt, &settlement.PayoutBatchID, &settlement.DestinationID,
		&settlement.ProviderTransferID, &settlement.RecoveryAmountCents, &settlement.TransferredAt, &settlement.Version,
		&settlement.CreatedAt, &settlement.UpdatedAt, &paymentStatus, &orderStatus, &providerChargeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return newProviderFailure("payment_invalid_request", 0)
		}
		return err
	}
	if settlement.Status == "transferred" || settlement.Status == "refund_hold" || settlement.Status == "cancelled" || settlement.Status == "recovery_required" || settlement.Status == "provider_unsupported" {
		return tx.Commit(ctx)
	}
	// A prior dispatch may have succeeded remotely, including after process loss.
	// Only an authenticated observation can resolve it; a retry cannot resend.
	var reserved bool
	if err := tx.QueryRow(ctx, `SELECT reserved_at IS NOT NULL FROM product_settlement_dispatches
	 WHERE settlement_id=$1 AND job_id=$2 FOR UPDATE`, settlement.ID, job.ID).Scan(&reserved); err != nil {
		return err
	}
	if reserved || settlement.Status == "transfer_pending" {
		return ErrCheckoutReconciliation
	}
	if err := requireTransferExecutionJobTx(ctx, tx, job.ID, ProductSettlementJobKind, payload); err != nil {
		return err
	}
	if paymentStatus == "refunded" || orderStatus == "refunded" {
		from := settlement.Status
		to := "refund_hold"
		if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status=$2,hold_reason='buyer_refund',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, settlement.ID, to); err != nil {
			return err
		}
		if err := recordProductSettlementEventTx(ctx, tx, settlement.ID, "refund.hold", from, to, map[string]any{"recoveryAmountCents": 0}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if paymentStatus != "paid" || orderStatus != "fulfilled" || providerChargeID == nil || strings.TrimSpace(*providerChargeID) == "" {
		return newProviderFailure("payment_settlement_pending", 5*time.Minute)
	}
	if time.Now().UTC().Before(settlement.AvailableAt) {
		delay := time.Until(settlement.AvailableAt)
		if delay > 15*time.Minute {
			delay = 15 * time.Minute
		}
		return newProviderFailure("payment_settlement_not_due", delay)
	}
	var payoutMode string
	if err := tx.QueryRow(ctx, `SELECT payout_mode FROM product_settlement_settings WHERE singleton=true FOR SHARE`).Scan(&payoutMode); err != nil {
		return err
	}
	runtime, err := s.runtimes.Runtime(settlement.Provider)
	if err != nil {
		return err
	}
	identity, _, err := verifyProductPaymentIdentity(ctx, tx, runtime, settlement.PaymentID)
	if err != nil {
		return err
	}
	if err := validateProductSettlementFundsTx(ctx, tx, settlement); err != nil {
		return err
	}
	if settlement.NetAmountCents == 0 {
		if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status='cancelled',hold_reason='no_seller_amount',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, settlement.ID); err != nil {
			return err
		}
		if err := recordProductSettlementEventTx(ctx, tx, settlement.ID, "transfer.not_required", settlement.Status, "cancelled", map[string]any{"amountCents": 0}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if payoutMode == "seller_payout" {
		if settlement.Status == "available" {
			return tx.Commit(ctx)
		}
		if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status='available',hold_reason='',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, settlement.ID); err != nil {
			return err
		}
		if err := recordProductSettlementEventTx(ctx, tx, settlement.ID, "settlement.available", settlement.Status, "available", map[string]any{"payoutMode": payoutMode}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err := validateAutomaticSellerFundsTx(ctx, tx, settlement.ID, sellerID); err != nil {
		return err
	}
	if !runtimeCapabilities(runtime).Transfer {
		if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status='provider_unsupported',hold_reason='provider_transfer_unsupported',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, settlement.ID); err != nil {
			return err
		}
		if err := recordProductSettlementEventTx(ctx, tx, settlement.ID, "provider.unsupported", settlement.Status, "provider_unsupported", map[string]any{"provider": settlement.Provider}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var destinationID string
	if err := tx.QueryRow(ctx, `SELECT destination_id FROM payment_destinations WHERE provider=$1 AND user_id=$2 AND status='verified' AND charges_enabled AND payouts_enabled
		AND original_merchant_id=$3 AND COALESCE(original_store_id,'')=$4 AND original_live_mode=$5
		AND original_endpoint=$6 AND original_api_version=$7 AND original_request_version=$8`, settlement.Provider, sellerID,
		identity.MerchantID, identity.StoreID, identity.LiveMode, identity.Endpoint, identity.APIVersion, identity.RequestVersion).Scan(&destinationID); errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_provider_unavailable", 15*time.Minute)
	} else if err != nil {
		return err
	}
	batchID := uuid.New()
	if err := tx.QueryRow(ctx, `
		INSERT INTO product_payout_batches(id,provider,live_mode,status,scheduled_at,started_at)
		VALUES($1,$2,$3,'dispatching',clock_timestamp(),clock_timestamp()) RETURNING id`, batchID, settlement.Provider, settlement.LiveMode).Scan(&batchID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO product_payout_batch_items(batch_id,settlement_id,status) VALUES($1,$2,'requested')`, batchID, settlement.ID); err != nil {
		return err
	}
	from := settlement.Status
	if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status='transfer_pending',payout_batch_id=$2,destination_id=$3,hold_reason='',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, settlement.ID, batchID, destinationID); err != nil {
		return err
	}
	if err := recordProductSettlementEventTx(ctx, tx, settlement.ID, "transfer.requested", from, "transfer_pending", map[string]any{"batchId": batchID, "destinationId": destinationID, "amountCents": settlement.NetAmountCents}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE product_settlement_dispatches SET reserved_at=clock_timestamp() WHERE settlement_id=$1`, settlement.ID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// The reservation survives rollback/process loss. Reacquire locks in the same
	// order as refunds and retain them through the external call and local result.
	tx, err = s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := lockProductSettlementPaymentTx(ctx, tx, settlement.ID); err != nil {
		return err
	}
	var dispatchable bool
	if err := tx.QueryRow(ctx, `SELECT ps.status='transfer_pending' AND ps.payout_batch_id=$2 AND ps.destination_id=$3
	 AND pi.status='paid' AND o.status='fulfilled' AND pi.provider_charge_id=$4
	 FROM product_settlements ps JOIN payment_intents pi ON pi.id=ps.payment_id JOIN orders o ON o.id=ps.order_id
	 WHERE ps.id=$1 FOR UPDATE OF ps`, settlement.ID, batchID, destinationID, *providerChargeID).Scan(&dispatchable); err != nil {
		return err
	}
	if !dispatchable {
		return ErrCheckoutReconciliation
	}
	identity, _, err = verifyProductPaymentIdentity(ctx, tx, runtime, settlement.PaymentID)
	if err != nil {
		return err
	}
	if err := validateProductSettlementFundsTx(ctx, tx, settlement); err != nil {
		return err
	}
	if err := validateAutomaticSellerFundsTx(ctx, tx, settlement.ID, sellerID); err != nil {
		return err
	}
	var destinationValid bool
	if err := tx.QueryRow(ctx, `SELECT status='verified' AND charges_enabled AND payouts_enabled AND destination_id=$3
	 AND original_merchant_id=$4 AND COALESCE(original_store_id,'')=$5 AND original_live_mode=$6
	 AND original_endpoint=$7 AND original_api_version=$8 AND original_request_version=$9
	 FROM payment_destinations WHERE provider=$1 AND user_id=$2 FOR SHARE`, settlement.Provider, sellerID, destinationID,
		identity.MerchantID, identity.StoreID, identity.LiveMode, identity.Endpoint, identity.APIVersion, identity.RequestVersion).Scan(&destinationValid); err != nil {
		return err
	}
	if !destinationValid {
		return ErrCheckoutReconciliation
	}
	if err := validateProductPayoutBatchTx(ctx, tx, settlement.ID, batchID, ""); err != nil {
		return err
	}
	if err := requireTransferExecutionJobTx(ctx, tx, job.ID, ProductSettlementJobKind, payload); err != nil {
		if !errors.Is(err, ErrCheckoutReconciliation) {
			return err
		}
		if saveErr := markProductSettlementReconciliationTx(ctx, tx, settlement.ID, batchID, err); saveErr != nil {
			return saveErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return commitErr
		}
		return err
	}
	transferInput := TransferRequest{
		PaymentID: settlement.PaymentID, ProviderChargeID: *providerChargeID, DestinationID: destinationID,
		AmountCents: settlement.NetAmountCents, Currency: settlement.Currency,
	}
	var returnedSource bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE settlement_id=$1)`, settlement.ID).Scan(&returnedSource); err != nil {
		return err
	}
	if returnedSource {
		transferInput.SettlementBatchID = batchID
	}
	transfer, transferErr := runtime.CreateTransfer(ctx, transferInput)
	// Preserve a received result even if the worker's request was just cancelled.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if transferErr != nil {
		if err := markProductSettlementReconciliationTx(saveCtx, tx, settlement.ID, batchID, transferErr); err != nil {
			return err
		}
		return tx.Commit(saveCtx)
	}
	if transfer.ProviderID == "" || transfer.AmountCents != settlement.NetAmountCents || transfer.Currency != settlement.Currency || transfer.DestinationID != destinationID || transfer.TransferGroup != transferGroup(settlement.PaymentID) {
		if err := markProductSettlementReconciliationTx(saveCtx, tx, settlement.ID, batchID, newProviderFailure("payment_response_invalid", 0)); err != nil {
			return err
		}
		return tx.Commit(saveCtx)
	}
	if err := completeProductSettlementTx(saveCtx, tx, settlement.ID, batchID, sellerID, destinationID, transfer.ProviderID); err != nil {
		return err
	}
	return tx.Commit(saveCtx)
}

func lockProductSettlementPaymentTx(ctx context.Context, tx pgx.Tx, settlementID uuid.UUID) error {
	var sellerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT ps.seller_id FROM product_settlements ps JOIN payment_intents pi ON pi.id=ps.payment_id
	 JOIN orders o ON o.id=ps.order_id AND pi.order_id=o.id WHERE ps.id=$1 FOR UPDATE OF pi,o`, settlementID).Scan(&sellerID); err != nil {
		return err
	}
	return lockSellerFundsTx(ctx, tx, sellerID)
}

func validateAutomaticSellerFundsTx(ctx context.Context, tx pgx.Tx, settlementID, sellerID uuid.UUID) error {
	var blocked bool
	if err := tx.QueryRow(ctx, `SELECT
	 EXISTS(SELECT 1 FROM seller_payout_request_allocations WHERE settlement_id=$1 AND released_at IS NULL)
	 OR seller_settlement_has_open_source($1)
	 OR seller_funds_recovery_blocks($2,$1)`, settlementID, sellerID).Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return ErrCheckoutReconciliation
	}
	return nil
}

func validateProductSettlementFundsTx(ctx context.Context, tx pgx.Tx, settlement ProductSettlement) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1
		FROM payment_intents pi
		JOIN product_settlements ps ON ps.payment_id=pi.id
		JOIN orders o ON o.id=ps.order_id
		JOIN product_sale_owners own ON own.order_id=o.id AND own.seller_id=ps.seller_id::text
		WHERE ps.id=$1 AND pi.id=$2 AND pi.purpose='product'
		AND pi.order_id=ps.order_id AND pi.payer_id=o.buyer_id AND pi.resource_id=o.product_id
		AND pi.provider=$3 AND pi.live_mode=$4 AND pi.amount_cents=$5 AND pi.amount_cents=o.amount_cents
		AND pi.currency=$6 AND pi.currency=o.currency AND pi.payee_id=ps.seller_id
		AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=pi.id)
		AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=pi.id)
		AND NOT EXISTS(SELECT 1 FROM product_payment_disputes d WHERE d.settlement_id=ps.id
		  AND d.action_status <> 'won'))`,
		settlement.ID, settlement.PaymentID, settlement.Provider, settlement.LiveMode, settlement.GrossAmountCents, settlement.Currency).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrCheckoutReconciliation
	}
	return nil
}

func markProductSettlementReconciliationTx(ctx context.Context, tx pgx.Tx, settlementID, batchID uuid.UUID, cause error) error {
	var from string
	if err := tx.QueryRow(ctx, `SELECT status FROM product_settlements WHERE id=$1 FOR UPDATE`, settlementID).Scan(&from); err != nil {
		return err
	}
	if from == "transferred" || from == "recovery_required" || from == "refund_hold" {
		return nil
	}
	code := "payment_reconciliation_required"
	if cause != nil {
		code = safeSettlementErrorCode(cause)
	}
	if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status='recovery_required',hold_reason=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, settlementID, code); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE product_payout_batch_items SET status='reconciliation_required',error_code=$2,updated_at=clock_timestamp() WHERE batch_id=$1 AND settlement_id=$3`, batchID, code, settlementID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE product_payout_batches SET status='reconciliation_required',updated_at=clock_timestamp() WHERE id=$1`, batchID); err != nil {
		return err
	}
	if err := recordProductSettlementEventTx(ctx, tx, settlementID, "transfer.reconciliation_required", from, "recovery_required", map[string]any{"errorCode": code}); err != nil {
		return err
	}
	return nil
}

func safeSettlementErrorCode(err error) string {
	if coded, ok := err.(interface{ ErrorCode() string }); ok {
		value := strings.TrimSpace(coded.ErrorCode())
		if value != "" && len(value) <= 80 {
			return value
		}
	}
	return "payment_reconciliation_required"
}

// Callers hold the original payment/order and settlement locks. Lock the batch
// and item too, so neither dispatch nor completion can accept changed evidence.
func validateProductPayoutBatchTx(ctx context.Context, tx pgx.Tx, settlementID, batchID uuid.UUID, transferID string) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT COALESCE(b.provider=ps.provider AND b.live_mode=ps.live_mode
 AND b.started_at IS NOT NULL AND d.reserved_at IS NOT NULL
 AND (b.status IN ('dispatching','reconciliation_required') OR (b.status='completed' AND i.status='succeeded'))
 AND (i.status IN ('requested','reconciliation_required') OR (i.status='succeeded' AND i.provider_transfer_id=$3))
 AND (i.provider_transfer_id IS NULL OR i.provider_transfer_id=$3)
 AND (ps.provider_transfer_id IS NULL OR ps.provider_transfer_id=$3)
 AND ($3<>'' OR (b.status='dispatching' AND i.status='requested' AND i.provider_transfer_id IS NULL AND ps.provider_transfer_id IS NULL)),false)
 FROM product_settlements ps
 JOIN product_settlement_dispatches d ON d.settlement_id=ps.id
 JOIN product_payout_batches b ON b.id=ps.payout_batch_id
 JOIN product_payout_batch_items i ON i.batch_id=b.id AND i.settlement_id=ps.id
 WHERE ps.id=$1 AND b.id=$2 FOR UPDATE OF b,i`, settlementID, batchID, transferID).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutReconciliation
	}
	if err != nil {
		return err
	}
	if !valid {
		return ErrCheckoutReconciliation
	}
	return nil
}

func completeProductPayoutBatchTx(ctx context.Context, tx pgx.Tx, settlementID, batchID uuid.UUID, transferID string) error {
	if strings.TrimSpace(transferID) == "" {
		return ErrCheckoutReconciliation
	}
	if err := validateProductPayoutBatchTx(ctx, tx, settlementID, batchID, transferID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE product_payout_batch_items SET status='succeeded',provider_transfer_id=$3,error_code=NULL,updated_at=clock_timestamp()
 WHERE batch_id=$1 AND settlement_id=$2`, batchID, settlementID, transferID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrCheckoutReconciliation
	}
	_, err = tx.Exec(ctx, `UPDATE product_payout_batches SET status='completed',completed_at=COALESCE(completed_at,clock_timestamp()),updated_at=clock_timestamp()
 WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM product_payout_batch_items WHERE batch_id=$1 AND status NOT IN ('succeeded','cancelled'))`, batchID)
	return err
}

func completeProductSettlementTx(ctx context.Context, tx pgx.Tx, settlementID, batchID, sellerID uuid.UUID, destinationID, transferID string) error {
	var from, status string
	var amount int
	var paymentID uuid.UUID
	var matches bool
	if err := tx.QueryRow(ctx, `SELECT status,net_amount_cents,payment_id,
 COALESCE(payout_batch_id=$2 AND seller_id=$3 AND destination_id=$4 AND (provider_transfer_id IS NULL OR provider_transfer_id=$5),false)
 FROM product_settlements WHERE id=$1 FOR UPDATE`, settlementID, batchID, sellerID, destinationID, transferID).Scan(&from, &amount, &paymentID, &matches); err != nil {
		return err
	}
	if !matches {
		return ErrCheckoutReconciliation
	}
	if from == "transferred" {
		return nil
	}
	if from != "transfer_pending" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if err := tx.QueryRow(ctx, `SELECT status FROM payment_intents WHERE id=$1`, paymentID).Scan(&status); err != nil {
		return err
	}
	if status != "paid" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if err := completeProductPayoutBatchTx(ctx, tx, settlementID, batchID, transferID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status='transferred',provider_transfer_id=$2,destination_id=$3,transferred_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE id=$1`, settlementID, transferID, destinationID); err != nil {
		return err
	}
	if err := recordProductSettlementEventTx(ctx, tx, settlementID, "transfer.completed", from, "transferred", map[string]any{"destinationId": destinationID, "providerTransferId": transferID, "amountCents": amount}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata) VALUES('marketplace.product_settlement_transferred','product_settlement',$1,$2,jsonb_build_object('paymentId',$3::text,'providerTransferId',$4::text,'amountCents',$5::integer))`, settlementID, "product-transfer:"+transferID, paymentID, transferID, amount); err != nil {
		return err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: sellerID, Kind: "marketplace.product_settlement_transferred", Title: "Product sale settled",
		Body: "The net amount for your product sale was transferred to your verified payout account.", TargetPath: "/workspace/sales", ResourceType: "product_settlement", ResourceID: &settlementID,
		SourceKey: "marketplace:settlement:" + settlementID.String() + ":transferred",
	}); err != nil {
		return err
	}
	return nil
}

// markProductSettlementRefundTx is called by the verified product refund
// transaction. A refund before payout cancels the payable; a refund after a
// payout creates an explicit recovery obligation instead of hiding the loss.
func markProductSettlementRefundTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	var sellerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT seller_id FROM product_settlements WHERE payment_id=$1`, paymentID).Scan(&sellerID); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if err := lockSellerFundsTx(ctx, tx, sellerID); err != nil {
		return err
	}
	var id uuid.UUID
	var status, reason string
	var net, previousRecovery int
	if err := tx.QueryRow(ctx, `SELECT id,status,net_amount_cents,recovery_amount_cents,hold_reason FROM product_settlements WHERE payment_id=$1 FOR UPDATE`, paymentID).Scan(&id, &status, &net, &previousRecovery, &reason); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	to := "refund_hold"
	recovery := 0
	// A reserved independent transfer may have succeeded remotely even when
	// the settlement still says available and no provider response was saved.
	var payoutAtRisk bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_payout_transfers
		WHERE settlement_id=$1 AND status IN ('processing','succeeded','reconciliation_required')
		AND NOT seller_source_transfer_closed(id))`, id).Scan(&payoutAtRisk); err != nil {
		return err
	}
	if payoutAtRisk || status == "transferred" || status == "transfer_pending" || status == "recovery_required" {
		to = "recovery_required"
		recovery = net
	}
	if (!payoutAtRisk && (status == "refund_hold" || status == "cancelled")) || (status == "recovery_required" && previousRecovery == net && reason == "buyer_refund") {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status=$2,recovery_amount_cents=$3,hold_reason='buyer_refund',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, to, recovery); err != nil {
		return err
	}
	if recovery > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status)
			SELECT seller_id,id,$2,$2,currency,'open' FROM product_settlements WHERE id=$1
			ON CONFLICT (settlement_id) DO UPDATE SET amount_cents=EXCLUDED.amount_cents,remaining_cents=GREATEST(seller_recovery_obligations.remaining_cents,EXCLUDED.remaining_cents),status='open',updated_at=clock_timestamp()`, id, recovery); err != nil {
			return err
		}
	}
	return recordProductSettlementEventTx(ctx, tx, id, "refund.confirmed", status, to, map[string]any{"recoveryAmountCents": recovery})
}
