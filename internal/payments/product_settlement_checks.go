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

const ProductSettlementCheckJobKind = "payment.check_product_settlement"

type productSettlementCheckPayload struct {
	CheckID uuid.UUID `json:"checkId"`
}

func (s *Service) ReconcileProductSettlements(ctx context.Context, limit int) (int, error) {
	return s.reconcileProductSettlements(ctx, limit, time.Now().UTC())
}

func (s *Service) reconcileProductSettlements(ctx context.Context, limit int, asOf time.Time) (int, error) {
	if s == nil || s.pool == nil {
		return 0, ErrDisabled
	}
	if limit < 1 || limit > 100 {
		return 0, newProviderFailure("payment_invalid_request", 0)
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return 0, nil
	}
	if _, ok := runtime.(TransferLookupReader); !ok {
		return 0, nil
	}
	scan := &s.settlementReconciliation
	if !scan.mu.TryLock() {
		return 0, nil
	}
	defer scan.mu.Unlock()
	read := func() ([]uuid.UUID, error) {
		rows, err := s.pool.Query(ctx, `SELECT settlement_id FROM product_settlement_check_candidates
 WHERE settlement_id>$1 AND due_at<=$3 ORDER BY settlement_id LIMIT $2`, scan.after, limit, asOf)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	ids, err := read()
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 && scan.after != uuid.Nil {
		scan.after = uuid.Nil
		ids, err = read()
		if err != nil {
			return 0, err
		}
	}
	count := 0
	var failures []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return count, errors.Join(append(failures, err)...)
		}
		changed, err := s.scheduleProductSettlementCheck(ctx, id, asOf)
		scan.after = id
		if err != nil {
			failures = append(failures, err)
		} else if changed {
			count++
		}
	}
	return count, errors.Join(failures...)
}

func (s *Service) scheduleProductSettlementCheck(ctx context.Context, settlementID uuid.UUID, asOf time.Time) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var paymentID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT pi.id FROM payment_intents pi JOIN product_settlements ps ON ps.payment_id=pi.id
 WHERE ps.id=$1 FOR UPDATE OF pi SKIP LOCKED`, settlementID).Scan(&paymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var eligible bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_settlement_check_candidates
 WHERE settlement_id=$1 AND due_at<=$2)`, settlementID, asOf).Scan(&eligible); err != nil || !eligible {
		return false, err
	}
	id := uuid.New()
	payload, err := json.Marshal(productSettlementCheckPayload{CheckID: id})
	if err != nil {
		return false, err
	}
	var jobID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,$2) RETURNING id`, ProductSettlementCheckJobKind, payload).Scan(&jobID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO product_settlement_checks(id,settlement_id,job_id) VALUES($1,$2,$3)`, id, settlementID, jobID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 VALUES('marketplace.product_settlement_check_scheduled','product_settlement',$1,$2,jsonb_build_object('checkId',$3::uuid,'paymentId',$4::uuid))`,
		settlementID, "settlement-check:"+id.String(), id, paymentID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Service) HandleProductSettlementCheckJob(ctx context.Context, job jobs.Job) error {
	var payload productSettlementCheckPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.CheckID == uuid.Nil || job.ID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if s == nil || s.pool == nil {
		return ErrDisabled
	}
	// Financial reads remain available when new sales or transfers are disabled.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var settlementID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT settlement_id FROM product_settlement_checks WHERE id=$1 AND job_id=$2`, payload.CheckID, job.ID).Scan(&settlementID); err != nil {
		return err
	}
	if err := lockProductSettlementPaymentTx(ctx, tx, settlementID); err != nil {
		return err
	}
	var finished *time.Time
	if err := tx.QueryRow(ctx, `SELECT finished_at FROM product_settlement_checks WHERE id=$1 FOR UPDATE`, payload.CheckID).Scan(&finished); err != nil {
		return err
	}
	if finished != nil {
		return tx.Commit(ctx)
	}
	var input TransferLookupRequest
	var provider, status string
	var transferID *string
	var reserved bool
	err = tx.QueryRow(ctx, `SELECT ps.payment_id,ps.provider,ps.live_mode,ps.net_amount_cents,ps.currency,
 COALESCE(ps.destination_id,''),COALESCE(pi.provider_charge_id,''),ps.status,ps.provider_transfer_id,d.reserved_at IS NOT NULL
 FROM product_settlements ps JOIN payment_intents pi ON pi.id=ps.payment_id
 JOIN product_settlement_dispatches d ON d.settlement_id=ps.id WHERE ps.id=$1 FOR UPDATE OF ps`, settlementID).Scan(
		&input.PaymentID, &provider, &input.LiveMode, &input.AmountCents, &input.Currency, &input.DestinationID,
		&input.ProviderChargeID, &status, &transferID, &reserved)
	if err != nil {
		return err
	}
	result := TransferLookupResult{Observations: []TransferObservation{}}
	var readErr error
	if transferID != nil || !reserved || !oneOf(status, "transfer_pending", "recovery_required") {
		result.Outcome = "skipped"
	} else {
		runtime, err := s.runtimes.Runtime(provider)
		readErr = err
		if err == nil {
			reader, ok := runtime.(TransferLookupReader)
			if !ok || provider != "stripe" {
				readErr = newProviderFailure("payment_provider_unsupported", 0)
			} else {
				readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				_, _, readErr = verifyProductPaymentIdentity(readCtx, tx, runtime, input.PaymentID)
				if readErr == nil {
					input.ReturnedSources, readErr = returnedSellerSources(readCtx, tx, input.PaymentID)
				}
				if readErr == nil {
					result, readErr = reader.LookupProductTransfer(readCtx, input)
				}
				cancel()
			}
		}
	}
	if readErr == nil && result.Outcome != "skipped" {
		valid := result.Pages > 0 && result.Pages <= 10 && len(result.Observations) <= 2 &&
			((result.Outcome == "found" && len(result.Observations) == 1) ||
				(result.Outcome == "not_found" && len(result.Observations) == 0) ||
				(result.Outcome == "ambiguous" && len(result.Observations) == 2) || result.Outcome == "incomplete")
		for _, item := range result.Observations {
			valid = valid && validTransferObservation(input, item)
		}
		if len(result.Observations) == 2 && result.Observations[0].ProviderID == result.Observations[1].ProviderID {
			valid = false
		}
		if !valid {
			readErr = newProviderFailure("payment_response_invalid", 0)
		}
	}
	code := ""
	if readErr != nil {
		result.Outcome = "error"
		code = safeSettlementErrorCode(readErr)
	}
	// A fully reversed or partially reversed transfer needs the recovery ledger;
	// it must not be projected as an unreversed successful payout.
	if result.Outcome == "found" && result.Observations[0].AmountReversed != 0 {
		result.Outcome = "reversed"
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if result.Outcome == "found" {
		if err := recordRecoveredProductTransferTx(saveCtx, tx, settlementID, payload.CheckID, result.Observations[0]); err != nil {
			return err
		}
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(saveCtx, `UPDATE product_settlement_checks SET outcome=$2,error_code=$3,evidence=$4,finished_at=clock_timestamp() WHERE id=$1`, payload.CheckID, result.Outcome, code, body); err != nil {
		return err
	}
	if _, err := tx.Exec(saveCtx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 VALUES('marketplace.product_settlement_checked','product_settlement',$1,$2,jsonb_build_object('checkId',$3::uuid,'outcome',$4::text,'errorCode',$5::text))`,
		settlementID, "settlement-check:"+payload.CheckID.String(), payload.CheckID, result.Outcome, code); err != nil {
		return err
	}
	return tx.Commit(saveCtx)
}

func recordRecoveredProductTransferTx(ctx context.Context, tx pgx.Tx, settlementID, checkID uuid.UUID, item TransferObservation) error {
	var from, reason, paymentStatus, orderStatus string
	var batchID, sellerID uuid.UUID
	var recovery, net int
	var review bool
	if err := tx.QueryRow(ctx, `SELECT ps.status,ps.hold_reason,ps.payout_batch_id,ps.seller_id,ps.recovery_amount_cents,ps.net_amount_cents,pi.status,o.status,
	 EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=pi.id) OR EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=pi.id)
	 FROM product_settlements ps JOIN payment_intents pi ON pi.id=ps.payment_id JOIN orders o ON o.id=ps.order_id
	 WHERE ps.id=$1 AND pi.purpose='product' AND ps.order_id=pi.order_id AND pi.order_id=o.id
	 AND pi.payer_id=o.buyer_id AND pi.resource_id=o.product_id AND pi.amount_cents=o.amount_cents AND pi.currency=o.currency
	 AND ps.provider=pi.provider AND ps.live_mode=pi.live_mode AND ps.gross_amount_cents=pi.amount_cents
	 AND ps.currency=pi.currency AND ps.seller_id=pi.payee_id
	 AND EXISTS(SELECT 1 FROM product_sale_owners own WHERE own.order_id=o.id AND own.seller_id=ps.seller_id::text)`, settlementID).Scan(
		&from, &reason, &batchID, &sellerID, &recovery, &net, &paymentStatus, &orderStatus, &review); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCheckoutReconciliation
		}
		return err
	}
	to := "transferred"
	if paymentStatus == "refunded" || orderStatus == "refunded" || reason == "buyer_refund" {
		recovery, reason = net, "buyer_refund"
	}
	if recovery > 0 || reason == "buyer_refund" || review ||
		!oneOf(paymentStatus, "paid", "refund_pending", "refund_failed") || !oneOf(orderStatus, "fulfilled", "refund_requested") {
		to = "recovery_required"
		if reason == "" {
			reason = "payment_reconciliation_required"
		}
	} else {
		reason = ""
	}
	if err := completeProductPayoutBatchTx(ctx, tx, settlementID, batchID, item.ProviderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status=$2,hold_reason=$3,recovery_amount_cents=$4,
 provider_transfer_id=$5,transferred_at=$6,version=version+1,updated_at=clock_timestamp() WHERE id=$1`,
		settlementID, to, reason, recovery, item.ProviderID, item.CreatedAt); err != nil {
		return err
	}
	if recovery > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status)
			SELECT seller_id,id,$2,$2,currency,'open' FROM product_settlements WHERE id=$1
			ON CONFLICT (settlement_id) DO UPDATE SET amount_cents=EXCLUDED.amount_cents,remaining_cents=GREATEST(seller_recovery_obligations.remaining_cents,EXCLUDED.remaining_cents),status='open',updated_at=clock_timestamp()`, settlementID, recovery); err != nil {
			return err
		}
	}
	if err := recordProductSettlementEventTx(ctx, tx, settlementID, "transfer.recovered", from, to,
		map[string]any{"checkId": checkID, "providerTransferId": item.ProviderID, "amountCents": item.AmountCents, "recoveryAmountCents": recovery}); err != nil {
		return err
	}
	if to == "transferred" {
		return notifications.CreateTx(ctx, tx, notifications.CreateInput{
			UserID: sellerID, Kind: "marketplace.product_settlement_transferred", Title: "Product sale settled",
			Body: "The net amount for your product sale was transferred to your verified payout account.", TargetPath: "/workspace/sales",
			ResourceType: "product_settlement", ResourceID: &settlementID, SourceKey: "marketplace:settlement:" + settlementID.String() + ":transferred",
		})
	}
	return nil
}
