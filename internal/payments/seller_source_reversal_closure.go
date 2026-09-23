package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrSellerSourceClosureInvalid  = errors.New("invalid confirmed source return closure")
	ErrSellerSourceClosureConflict = errors.New("source return closure requires current complete funds proof")
)

type SellerSourceClosureInput struct {
	ReadID            uuid.UUID `json:"readId"`
	ExpectedUpdatedAt time.Time `json:"expectedUpdatedAt"`
	Reason            string    `json:"reason"`
	Confirmed         bool      `json:"confirmed"`
}

type SellerSourceClosure struct {
	ID              uuid.UUID `json:"id"`
	CommandID       uuid.UUID `json:"commandId"`
	PayoutRequestID uuid.UUID `json:"payoutRequestId"`
	ReadID          uuid.UUID `json:"readId"`
	Resolution      string    `json:"resolution"`
	CreatedAt       time.Time `json:"createdAt"`
}

type SellerSourceClosureSubmission struct {
	Closure  SellerSourceClosure `json:"closure"`
	Replayed bool                `json:"replayed"`
}

// CloseSellerSourceReversal consumes an already observed full source return.
// It never calls a provider or creates a settlement credit. A current finance
// actor confirms the original evidence; returned funds close a refund debt or
// release the existing reservation, in the same transaction as event/audit.
func (s *Service) CloseSellerSourceReversal(ctx context.Context, actor, commandID uuid.UUID, input SellerSourceClosureInput, key, trace string) (SellerSourceClosureSubmission, error) {
	out, err := s.closeSellerSourceReversal(ctx, actor, commandID, input, key, trace)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && oneOf(pgerr.Code, "23505", "23514", "40001", "40P01", "55000") {
		err = ErrSellerSourceClosureConflict
	}
	return out, err
}

func (s *Service) closeSellerSourceReversal(ctx context.Context, actor, commandID uuid.UUID, input SellerSourceClosureInput, key, trace string) (SellerSourceClosureSubmission, error) {
	var out SellerSourceClosureSubmission
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if actor == uuid.Nil || commandID == uuid.Nil || input.ReadID == uuid.Nil || !input.Confirmed || input.ExpectedUpdatedAt.Unix() <= 0 ||
		!sellerPayoutReviewKey.MatchString(key) || !utf8.ValidString(input.Reason) || strings.ContainsRune(input.Reason, 0) ||
		utf8.RuneCountInString(input.Reason) < 10 || utf8.RuneCountInString(input.Reason) > 1000 {
		return out, ErrSellerSourceClosureInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err := paymentFinanceAuthority(ctx, tx, actor, false); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "seller-source-close:"+actor.String()+":"+key); err != nil {
		return out, err
	}
	var expected time.Time
	var reason string
	err = tx.QueryRow(ctx, `SELECT id,command_id,payout_request_id,read_id,resolution,created_at,expected_updated_at,reason
 FROM seller_source_reversal_closures WHERE actor_id=$1 AND idempotency_key=$2`, actor, key).Scan(&out.Closure.ID, &out.Closure.CommandID,
		&out.Closure.PayoutRequestID, &out.Closure.ReadID, &out.Closure.Resolution, &out.Closure.CreatedAt, &expected, &reason)
	if err == nil {
		if out.Closure.CommandID != commandID || out.Closure.ReadID != input.ReadID || !expected.Equal(input.ExpectedUpdatedAt) || reason != input.Reason {
			return out, ErrSellerSourceClosureConflict
		}
		if _, err := tx.Exec(ctx, `SELECT lock_seller_payout_disposition($1)`, out.Closure.PayoutRequestID); err != nil {
			return out, err
		}
		if err := paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
			return out, err
		}
		out.Replayed = true
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	var requestID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT payout_request_id FROM seller_source_reversal_commands WHERE id=$1`, commandID).Scan(&requestID); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrSellerSourceClosureConflict
	} else if err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `SELECT lock_seller_payout_disposition($1)`, requestID); err != nil {
		return out, err
	}
	if err := paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
		return out, err
	}
	var sellerID, settlementID uuid.UUID
	var amount int
	var currency, status string
	var resolution *string
	var recoveryID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT c.seller_id,c.settlement_id,c.amount_cents,c.currency,r.status,
 seller_source_reversal_close_resolution(c.id),d.id
 FROM seller_source_reversal_commands c JOIN seller_payout_requests r ON r.id=c.payout_request_id
 LEFT JOIN seller_recovery_obligations d ON d.settlement_id=c.settlement_id
 WHERE c.id=$1`, commandID).Scan(&sellerID, &settlementID, &amount, &currency, &status, &resolution, &recoveryID); err != nil {
		return out, err
	}
	if sellerID == actor || resolution == nil {
		return out, ErrSellerSourceClosureConflict
	}
	if *resolution == "released" {
		recoveryID = nil
	}
	out.Closure = SellerSourceClosure{ID: uuid.New(), CommandID: commandID, PayoutRequestID: requestID, ReadID: input.ReadID, Resolution: *resolution}
	releaseID := uuid.New()
	if err := tx.QueryRow(ctx, `INSERT INTO seller_source_reversal_closures(id,command_id,read_id,payout_request_id,settlement_id,seller_id,
 actor_id,expected_updated_at,resolution,recovery_id,release_entry_id,amount_cents,currency,reason,idempotency_key,request_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING created_at`,
		out.Closure.ID, commandID, input.ReadID, requestID, settlementID, sellerID, actor, input.ExpectedUpdatedAt, *resolution, recoveryID,
		releaseID, amount, currency, input.Reason, key, trace).Scan(&out.Closure.CreatedAt); err != nil {
		return out, err
	}
	if recoveryID != nil {
		if _, err := tx.Exec(ctx, `UPDATE seller_recovery_obligations SET remaining_cents=0,status='settled',updated_at=clock_timestamp() WHERE id=$1`, *recoveryID); err != nil {
			return out, err
		}
		if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status='cancelled',recovery_amount_cents=0,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, settlementID); err != nil {
			return out, err
		}
		if err := recordProductSettlementEventTx(ctx, tx, settlementID, "source_return.recovered", "recovery_required", "cancelled", map[string]any{"closureId": out.Closure.ID, "recoveredCents": amount}); err != nil {
			return out, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE seller_payout_requests SET status='cancelled',failure_code=NULL,updated_at=clock_timestamp() WHERE id=$1`, requestID); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO seller_ledger_entries(id,seller_id,entry_type,amount_cents,currency,payout_request_id,idempotency_key,evidence)
 VALUES($1,$2,'payout_release',$3,$4,$5,$6,jsonb_build_object('sourceReversalClosureId',$7::uuid))`,
		releaseID, sellerID, amount, currency, requestID, "payout-release:"+requestID.String(), out.Closure.ID); err != nil {
		return out, err
	}
	to := "cancelled"
	if err := recordSellerPayoutEventTx(ctx, tx, requestID, "source_reversal.closed", &status, &to, "source-reversal-closed:"+out.Closure.ID.String(),
		map[string]any{"closureId": out.Closure.ID, "commandId": commandID, "resolution": *resolution}); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
 VALUES($1,'seller_payout.source_reversal_closed','seller_payout_request',$2,$3,jsonb_build_object('closureId',$4::uuid,'commandId',$5::uuid,'resolution',$6::text))`,
		actor, requestID, trace, out.Closure.ID, commandID, *resolution); err != nil {
		return out, err
	}
	kind, title, body := "marketplace.payout_source_released", "Payout reservation released", "The returned payment source was reconciled and the reserved balance is available again."
	if *resolution == "refund_recovered" {
		kind, title, body = "marketplace.payout_refund_recovered", "Refund recovery completed", "The returned payment source was applied to the related refund recovery. The payout request is closed."
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: sellerID, Kind: kind, Title: title, Body: body,
		TargetPath: "/workspace/payouts/" + requestID.String(), ResourceType: "seller_payout_request", ResourceID: &requestID,
		SourceKey: "marketplace:source-return-closed:" + out.Closure.ID.String() + ":" + *resolution,
	}); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// Only immutable consumed full returns may be excluded from a later transfer
// lookup. The provider must independently observe each excluded transfer as
// fully reversed; an arbitrary remote transfer never gains this exception.
func returnedSellerSources(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) ([]TransferObservation, error) {
	rows, err := tx.Query(ctx, `SELECT r.evidence->'transfer' FROM seller_source_reversal_closures x
 JOIN seller_source_reversal_commands c ON c.id=x.command_id JOIN seller_source_reversal_reads r ON r.id=x.read_id
 WHERE c.payment_id=$1 ORDER BY x.created_at,x.id LIMIT 1001`, paymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var returned []TransferObservation
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item TransferObservation
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, ErrCheckoutReconciliation
		}
		returned = append(returned, item)
	}
	if len(returned) > 1000 {
		return nil, ErrCheckoutReconciliation
	}
	return returned, rows.Err()
}
