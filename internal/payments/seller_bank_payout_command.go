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

var (
	ErrSellerBankPayoutInvalid  = errors.New("invalid confirmed seller bank payout command")
	ErrSellerBankPayoutConflict = errors.New("seller bank payout source or approval requires reconciliation")
)

type SellerBankPayoutInput struct {
	SourceTransferID  uuid.UUID `json:"sourceTransferId"`
	ReviewID          uuid.UUID `json:"reviewId"`
	ExpectedRevision  int       `json:"expectedRevision"`
	AmountCents       int       `json:"amountCents"`
	BankDestinationID string    `json:"bankDestinationId"`
	Reason            string    `json:"reason"`
	Confirmed         bool      `json:"confirmed"`
}

// A reservation is not a provider result. Keep the immutable provider identity
// private; bank dispatch will load it from the command, never from a caller.
type SellerBankPayoutCommand struct {
	ID                uuid.UUID `json:"id"`
	PayoutRequestID   uuid.UUID `json:"payoutRequestId"`
	SourceTransferID  uuid.UUID `json:"sourceTransferId"`
	ReviewID          uuid.UUID `json:"reviewId"`
	ReviewRevision    int       `json:"reviewRevision"`
	AmountCents       int       `json:"amountCents"`
	Currency          string    `json:"currency"`
	BankDestinationID string    `json:"bankDestinationId"`
	CreatedAt         time.Time `json:"createdAt"`
}

type SellerBankPayoutReservation struct {
	Command  SellerBankPayoutCommand `json:"command"`
	Replayed bool                    `json:"replayed"`
}

const sellerBankCommandSelect = `SELECT id,payout_request_id,source_transfer_id,review_id,review_revision,amount_cents,currency,bank_destination_id,created_at,reason
 FROM seller_bank_payout_commands`

func scanSellerBankCommand(row pgx.Row) (SellerBankPayoutCommand, string, error) {
	var command SellerBankPayoutCommand
	var reason string
	err := row.Scan(&command.ID, &command.PayoutRequestID, &command.SourceTransferID, &command.ReviewID, &command.ReviewRevision, &command.AmountCents, &command.Currency, &command.BankDestinationID, &command.CreatedAt, &reason)
	return command, reason, err
}

// ReserveSellerBankPayout freezes one bank intent, separately from funding
// approval/admission. It never calls a provider or marks a request paid.
// HTTP uses SubmitSellerBankPayout to reserve and queue atomically.
func (s *Service) ReserveSellerBankPayout(ctx context.Context, actor, request uuid.UUID, input SellerBankPayoutInput, key, trace string) (SellerBankPayoutReservation, error) {
	out, err := s.reserveSellerBankPayout(ctx, actor, request, input, key, trace)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && oneOf(pgerr.Code, "23505", "23514", "40001", "40P01") {
		err = ErrSellerBankPayoutConflict
	}
	return out, err
}

func (s *Service) reserveSellerBankPayout(ctx context.Context, actor, request uuid.UUID, input SellerBankPayoutInput, key, trace string) (SellerBankPayoutReservation, error) {
	var out SellerBankPayoutReservation
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	out, err = s.reserveSellerBankPayoutTx(ctx, tx, actor, request, input, key, trace)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *Service) reserveSellerBankPayoutTx(ctx context.Context, tx pgx.Tx, actor, request uuid.UUID, input SellerBankPayoutInput, key, trace string) (SellerBankPayoutReservation, error) {
	var out SellerBankPayoutReservation
	var err error
	input.Reason = strings.TrimSpace(input.Reason)
	if actor == uuid.Nil || request == uuid.Nil || input.SourceTransferID == uuid.Nil || input.ReviewID == uuid.Nil || input.ExpectedRevision < 1 || input.ExpectedRevision >= 2147483647 || input.AmountCents < 1 || input.AmountCents > 99999999 || !input.Confirmed ||
		!validStripeID(input.BankDestinationID, "ba_") || !sellerPayoutReviewKey.MatchString(key) || !utf8.ValidString(input.Reason) || strings.ContainsRune(input.Reason, 0) || utf8.RuneCountInString(input.Reason) < 10 || utf8.RuneCountInString(input.Reason) > 1000 {
		return out, ErrSellerBankPayoutInvalid
	}
	if err = paymentFinanceAuthority(ctx, tx, actor, false); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "seller-bank-command:"+actor.String()+":"+key); err != nil {
		return out, err
	}
	prior, reason, err := scanSellerBankCommand(tx.QueryRow(ctx, sellerBankCommandSelect+` WHERE actor_id=$1 AND idempotency_key=$2`, actor, key))
	if err == nil {
		if prior.PayoutRequestID != request || prior.SourceTransferID != input.SourceTransferID || prior.ReviewID != input.ReviewID || prior.ReviewRevision != input.ExpectedRevision || prior.AmountCents != input.AmountCents || prior.BankDestinationID != input.BankDestinationID || reason != input.Reason {
			return out, ErrSellerBankPayoutConflict
		}
		// Recovery reads an immutable command even after new writes are disabled,
		// but permission must still be current after waiting for the key lock.
		if _, err = lockSellerBankTx(ctx, tx, prior.ID); err != nil {
			return out, err
		}
		if err = paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
			return out, err
		}
		out.Command, out.Replayed = prior, true
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if !s.config.Enabled {
		return out, ErrDisabled
	}
	var seller uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT seller_id FROM seller_payout_requests WHERE id=$1`, request).Scan(&seller); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrSellerPayoutNotFound
		}
		return out, err
	}
	if seller == actor {
		return out, ErrFinanceForbidden
	}
	settlement, err := lockSellerPayoutBankRequest(ctx, tx, seller, request)
	if err != nil {
		return out, err
	}
	if err = paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
		return out, err
	}
	var id uuid.UUID
	// All economic fields come from the original bank/source/approval evidence.
	// The trigger rechecks the complete financial graph and current authority.
	err = tx.QueryRow(ctx, `INSERT INTO seller_bank_payout_commands(payout_request_id,source_transfer_id,funding_admission_id,review_id,review_revision,seller_id,settlement_id,payment_id,provider_identity,destination_id,bank_destination_id,source_provider_transfer_id,amount_cents,currency,actor_id,idempotency_key,dispatch_key,reason,request_id)
 SELECT r.id,t.id,f.id,v.id,v.revision,r.seller_id,s.id,s.payment_id,t.provider_identity,t.destination_id,b.bank_destination_id,t.provider_transfer_id,r.amount_cents,r.currency,$8,$9,'seller-bank-payout-'||r.id::text,$10,$11
 FROM seller_payout_requests r JOIN seller_payout_transfers t ON t.payout_request_id=r.id
 JOIN seller_payout_funding_admissions f ON f.transfer_id=t.id
 JOIN seller_payout_reviews v ON v.id=f.review_id JOIN seller_payout_bank_targets b ON b.payout_request_id=r.id
 JOIN product_settlements s ON s.id=t.settlement_id
 WHERE r.id=$1 AND t.id=$2 AND v.id=$3 AND v.revision=$4 AND r.amount_cents=$5 AND b.bank_destination_id=$6 AND s.id=$7
 AND t.status='succeeded' AND t.provider_transfer_id IS NOT NULL RETURNING id`, request, input.SourceTransferID, input.ReviewID, input.ExpectedRevision, input.AmountCents, input.BankDestinationID, settlement, actor, key, input.Reason, trace).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrSellerBankPayoutConflict
	}
	if err != nil {
		return out, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM seller_payout_requests WHERE id=$1`, request).Scan(&status); err != nil {
		return out, err
	}
	if err = recordSellerPayoutEventTx(ctx, tx, request, "bank.reserved", &status, &status, "bank-command:"+id.String(), map[string]any{"commandId": id, "sourceTransferId": input.SourceTransferID}); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
 VALUES($1,'seller_payout.bank_reserved','seller_payout_request',$2,$3,$4,jsonb_build_object('commandId',$5::text,'sourceTransferId',$6::text))`, actor, request, input.Reason, trace, id, input.SourceTransferID); err != nil {
		return out, err
	}
	out.Command, _, err = scanSellerBankCommand(tx.QueryRow(ctx, sellerBankCommandSelect+` WHERE id=$1`, id))
	if err != nil {
		return out, err
	}
	return out, nil
}
