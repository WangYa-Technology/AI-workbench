package payments

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const SellerSourceReversalJobKind = "payment.reverse_seller_source"

var (
	ErrSellerSourceReversalInvalid  = errors.New("invalid confirmed source reversal command")
	ErrSellerSourceReversalConflict = errors.New("source reversal requires current source and resolved bank evidence")
)

// Only confirmation context comes from the caller. The original merchant,
// charge, destination and amount are read from immutable financial evidence.
type SellerSourceReversalInput struct {
	SourceTransferID  uuid.UUID  `json:"sourceTransferId"`
	ExpectedUpdatedAt time.Time  `json:"expectedUpdatedAt"`
	BankCommandID     *uuid.UUID `json:"bankCommandId"`
	BankResultID      *uuid.UUID `json:"bankResultId"`
	Reason            string     `json:"reason"`
	Confirmed         bool       `json:"confirmed"`
}

type SellerSourceReversalCommand struct {
	ID               uuid.UUID  `json:"id"`
	PayoutRequestID  uuid.UUID  `json:"payoutRequestId"`
	SourceTransferID uuid.UUID  `json:"sourceTransferId"`
	RequestUpdatedAt time.Time  `json:"requestUpdatedAt"`
	BankCommandID    *uuid.UUID `json:"bankCommandId,omitempty"`
	BankResultID     *uuid.UUID `json:"bankResultId,omitempty"`
	BankDisposition  string     `json:"bankDisposition"`
	AmountCents      int        `json:"amountCents"`
	Currency         string     `json:"currency"`
	JobID            uuid.UUID  `json:"jobId"`
	CreatedAt        time.Time  `json:"createdAt"`
}

type SellerSourceReversalSubmission struct {
	Command  SellerSourceReversalCommand `json:"command"`
	Replayed bool                        `json:"replayed"`
}

const sellerSourceReversalSelect = `SELECT id,payout_request_id,source_transfer_id,request_updated_at,
 bank_command_id,bank_result_id,bank_disposition,amount_cents,currency,job_id,created_at,reason
 FROM seller_source_reversal_commands`

func scanSellerSourceReversal(row pgx.Row) (SellerSourceReversalCommand, string, error) {
	var out SellerSourceReversalCommand
	var reason string
	err := row.Scan(&out.ID, &out.PayoutRequestID, &out.SourceTransferID, &out.RequestUpdatedAt,
		&out.BankCommandID, &out.BankResultID, &out.BankDisposition, &out.AmountCents, &out.Currency,
		&out.JobID, &out.CreatedAt, &reason)
	return out, reason, err
}

func sameReversalUUID(a, b *uuid.UUID) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// SubmitSellerSourceReversal persists the decision, one original job, event and
// audit atomically. This internal entry point does not call Stripe or release
// any reservation. The worker records source-return evidence; a separate
// explicitly confirmed command consumes that proof and closes the ledger.
func (s *Service) SubmitSellerSourceReversal(ctx context.Context, actor, request uuid.UUID, input SellerSourceReversalInput, key, trace string) (SellerSourceReversalSubmission, error) {
	out, err := s.submitSellerSourceReversal(ctx, actor, request, input, key, trace)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && oneOf(pgerr.Code, "23505", "23514", "40001", "40P01") {
		err = ErrSellerSourceReversalConflict
	}
	return out, err
}

func (s *Service) submitSellerSourceReversal(ctx context.Context, actor, request uuid.UUID, input SellerSourceReversalInput, key, trace string) (SellerSourceReversalSubmission, error) {
	var out SellerSourceReversalSubmission
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if actor == uuid.Nil || request == uuid.Nil || input.SourceTransferID == uuid.Nil || !input.Confirmed ||
		input.ExpectedUpdatedAt.IsZero() || input.ExpectedUpdatedAt.Unix() <= 0 ||
		input.BankCommandID != nil && *input.BankCommandID == uuid.Nil || input.BankResultID != nil && *input.BankResultID == uuid.Nil ||
		input.BankResultID != nil && input.BankCommandID == nil || !sellerPayoutReviewKey.MatchString(key) ||
		!utf8.ValidString(input.Reason) || strings.ContainsRune(input.Reason, 0) ||
		utf8.RuneCountInString(input.Reason) < 10 || utf8.RuneCountInString(input.Reason) > 1000 {
		return out, ErrSellerSourceReversalInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err := paymentFinanceAuthority(ctx, tx, actor, false); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "seller-source-reversal:"+actor.String()+":"+key); err != nil {
		return out, err
	}
	prior, reason, err := scanSellerSourceReversal(tx.QueryRow(ctx, sellerSourceReversalSelect+` WHERE actor_id=$1 AND idempotency_key=$2`, actor, key))
	if err == nil {
		if prior.PayoutRequestID != request || prior.SourceTransferID != input.SourceTransferID ||
			!prior.RequestUpdatedAt.Equal(input.ExpectedUpdatedAt) || !sameReversalUUID(prior.BankCommandID, input.BankCommandID) ||
			!sameReversalUUID(prior.BankResultID, input.BankResultID) || reason != input.Reason {
			return out, ErrSellerSourceReversalConflict
		}
		if _, err := tx.Exec(ctx, `SELECT lock_seller_payout_disposition($1)`, request); err != nil {
			return out, err
		}
		if err := paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
			return out, err
		}
		// Same-key recovery is allowed after new writes are disabled. Return the
		// original stopped/expired job as evidence, never reset or replace it.
		out.Command, out.Replayed = prior, true
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if !s.config.Enabled {
		return out, ErrDisabled
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return out, ErrProviderUnavailable
	}
	if _, ok := runtime.(TransferReversalProvider); !ok {
		return out, ErrProviderUnavailable
	}
	var seller uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT seller_id FROM seller_payout_requests WHERE id=$1`, request).Scan(&seller); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrSellerPayoutNotFound
		}
		return out, err
	}
	if actor == seller {
		return out, ErrFinanceForbidden
	}
	if _, err := tx.Exec(ctx, `SELECT lock_seller_payout_disposition($1)`, request); err != nil {
		return out, err
	}
	if err := paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
		return out, err
	}
	id := uuid.New()
	var job uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
 VALUES($1,jsonb_build_object('commandId',$2::uuid),10000) RETURNING id`, SellerSourceReversalJobKind, id).Scan(&job); err != nil {
		return out, err
	}
	var inserted uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO seller_source_reversal_commands(id,payout_request_id,source_transfer_id,settlement_id,payment_id,seller_id,actor_id,
 request_updated_at,bank_command_id,bank_result_id,bank_disposition,provider_identity,provider_transfer_id,provider_charge_id,destination_id,
 amount_cents,currency,live_mode,idempotency_key,dispatch_key,reason,request_id,job_id)
 SELECT $1::uuid,r.id,t.id,s.id,s.payment_id,r.seller_id,$2,r.updated_at,bank.command_id,bank.result_id,bank.disposition,
 t.provider_identity,t.provider_transfer_id,d.provider_charge_id,t.destination_id,t.amount_cents,t.currency,t.live_mode,
 $3,'seller-source-reversal-'||$1::uuid::text,$4,$5,$6
 FROM seller_payout_requests r JOIN seller_payout_transfers t ON t.payout_request_id=r.id
 JOIN product_settlements s ON s.id=t.settlement_id JOIN seller_payout_funding_dispatches d ON d.transfer_id=t.id
 CROSS JOIN LATERAL seller_source_reversal_bank_state(r.id) bank
 WHERE r.id=$7 AND t.id=$8 AND r.updated_at=$9 AND t.status='succeeded' AND t.provider_transfer_id IS NOT NULL
 AND bank.command_id IS NOT DISTINCT FROM $10::uuid AND bank.result_id IS NOT DISTINCT FROM $11::uuid RETURNING id`,
		id, actor, key, input.Reason, trace, job, request, input.SourceTransferID, input.ExpectedUpdatedAt, input.BankCommandID, input.BankResultID).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrSellerSourceReversalConflict
	}
	if err != nil {
		return out, err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM seller_payout_requests WHERE id=$1`, request).Scan(&status); err != nil {
		return out, err
	}
	if err := recordSellerPayoutEventTx(ctx, tx, request, "source_reversal.queued", &status, &status,
		"source-reversal:"+id.String(), map[string]any{"commandId": id, "sourceTransferId": input.SourceTransferID, "jobId": job}); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
 VALUES($1,'seller_payout.source_reversal_queued','seller_payout_request',$2,$3,$4,
 jsonb_build_object('commandId',$5::uuid,'sourceTransferId',$6::uuid,'jobId',$7::uuid))`,
		actor, request, input.Reason, trace, id, input.SourceTransferID, job); err != nil {
		return out, err
	}
	out.Command, _, err = scanSellerSourceReversal(tx.QueryRow(ctx, sellerSourceReversalSelect+` WHERE id=$1`, id))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
