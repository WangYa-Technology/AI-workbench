package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// These are finance-only, allowlisted projections. No provider payload,
// merchant credentials, dispatch key or raw request is returned.
type SellerSourceReversalBankState struct {
	CommandID   *uuid.UUID `json:"commandId,omitempty"`
	ResultID    *uuid.UUID `json:"resultId,omitempty"`
	Disposition string     `json:"disposition"`
}

type SellerSourceReversalRead struct {
	ID             uuid.UUID  `json:"id"`
	Kind           string     `json:"kind"`
	StartedAt      time.Time  `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	Outcome        *string    `json:"outcome,omitempty"`
	RequiresReview bool       `json:"requiresReview"`
}

type SellerSourceReversalOperation struct {
	Request           SellerPayoutReviewItem         `json:"request"`
	ExpectedUpdatedAt time.Time                      `json:"expectedUpdatedAt"`
	Bank              *SellerSourceReversalBankState `json:"bank,omitempty"`
	Command           *SellerSourceReversalCommand   `json:"command,omitempty"`
	JobStatus         *string                        `json:"jobStatus,omitempty"`
	StartedAt         *time.Time                     `json:"startedAt,omitempty"`
	LatestRead        *SellerSourceReversalRead      `json:"latestRead,omitempty"`
	AcceptedReadID    *uuid.UUID                     `json:"acceptedReadId,omitempty"`
	Closure           *SellerSourceClosure           `json:"closure,omitempty"`
	RequiresReview    bool                           `json:"requiresReview"`
	CanSubmit         bool                           `json:"canSubmit"`
	CanClose          bool                           `json:"canClose"`
	CloseResolution   *string                        `json:"closeResolution,omitempty"`
}

// GetSellerSourceReversalOperation performs no financial writes, scheduling
// or provider calls. All fields use one snapshot while current finance
// authority is locked. Eligibility is a hint, never authorization to write.
func (s *Service) GetSellerSourceReversalOperation(ctx context.Context, actor, request uuid.UUID) (SellerSourceReversalOperation, error) {
	var out SellerSourceReversalOperation
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err := paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
		return out, err
	}
	out.Request, err = s.scanSellerPayoutReviewItem(actor, tx.QueryRow(ctx, sellerPayoutReviewSelect+` WHERE r.id=$1`, request))
	if err != nil {
		return out, err
	}
	var bank, latest, closure []byte
	var ready, proven bool
	err = tx.QueryRow(ctx, `SELECT r.updated_at,
 CASE WHEN bank.disposition IS NOT NULL THEN jsonb_strip_nulls(jsonb_build_object(
 'commandId',bank.command_id,'resultId',bank.result_id,'disposition',bank.disposition)) END,
 EXISTS(SELECT 1 FROM seller_payout_transfers t
 JOIN seller_payout_funding_dispatches d ON d.transfer_id=t.id AND d.started_at IS NOT NULL
 JOIN seller_payout_funding_admissions f ON f.transfer_id=t.id AND f.payout_request_id=r.id
 JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id AND a.settlement_id=t.settlement_id
 JOIN product_settlements s ON s.id=t.settlement_id AND s.seller_id=r.seller_id
 WHERE t.payout_request_id=r.id AND t.status='succeeded' AND t.provider='stripe'
 AND t.provider_transfer_id IS NOT NULL AND t.amount_cents=r.amount_cents AND t.currency=r.currency
 AND a.released_at IS NULL AND a.amount_cents=r.amount_cents AND s.net_amount_cents=r.amount_cents
 AND r.status IN ('processing','reconciliation_required') AND bank.disposition IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_commands WHERE payout_request_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM seller_payout_funding_reads WHERE transfer_id=t.id AND (finished_at IS NULL OR requires_review))
 AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=s.id AND reserved_at IS NOT NULL)
 AND EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=r.id AND entry_type='payout_reservation')
 AND NOT EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=r.id AND entry_type='payout_release')),
 j.status,d.started_at,
 CASE WHEN last_read.id IS NOT NULL THEN jsonb_strip_nulls(jsonb_build_object(
 'id',last_read.id,'kind',last_read.kind,'startedAt',last_read.started_at,
 'finishedAt',last_read.finished_at,'outcome',last_read.outcome,'requiresReview',last_read.requires_review)) END,
 accepted.read_id,
 CASE WHEN x.id IS NOT NULL THEN jsonb_build_object('id',x.id,'commandId',x.command_id,
 'payoutRequestId',x.payout_request_id,'readId',x.read_id,'resolution',x.resolution,'createdAt',x.created_at) END,
 EXISTS(SELECT 1 FROM seller_source_reversal_reads WHERE command_id=c.id AND
 (requires_review OR (finished_at IS NULL AND deadline_at+interval '5 seconds'<statement_timestamp()))),
 COALESCE(seller_source_reversal_return_proven(c.id),false),
 CASE WHEN x.id IS NULL THEN seller_source_reversal_close_resolution(c.id) END
 FROM seller_payout_requests r
 LEFT JOIN LATERAL seller_source_reversal_bank_state(r.id) bank ON true
 LEFT JOIN seller_source_reversal_commands c ON c.payout_request_id=r.id
 LEFT JOIN jobs j ON j.id=c.job_id
 LEFT JOIN seller_source_reversal_dispatches d ON d.command_id=c.id
 LEFT JOIN LATERAL (SELECT id,kind,started_at,finished_at,outcome,requires_review
 FROM seller_source_reversal_reads WHERE command_id=c.id AND outcome IS DISTINCT FROM 'superseded'
 ORDER BY started_at DESC,id DESC LIMIT 1) last_read ON true
 LEFT JOIN seller_source_reversal_results accepted ON accepted.command_id=c.id
 LEFT JOIN seller_source_reversal_closures x ON x.command_id=c.id
 WHERE r.id=$1`, request).Scan(&out.ExpectedUpdatedAt, &bank, &ready, &out.JobStatus, &out.StartedAt,
		&latest, &out.AcceptedReadID, &closure, &out.RequiresReview, &proven, &out.CloseResolution)
	if err != nil {
		return out, err
	}
	for _, field := range []struct {
		raw  []byte
		dest any
	}{{bank, &out.Bank}, {latest, &out.LatestRead}, {closure, &out.Closure}} {
		if field.raw != nil {
			if err := json.Unmarshal(field.raw, field.dest); err != nil {
				return out, err
			}
		}
	}
	command, _, err := scanSellerSourceReversal(tx.QueryRow(ctx, sellerSourceReversalSelect+` WHERE payout_request_id=$1`, request))
	if err == nil {
		out.Command = &command
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	runtime, runtimeErr := s.runtimes.Runtime("stripe")
	_, supported := runtime.(TransferReversalProvider)
	out.CanSubmit = ready && out.Request.SellerID != actor && s.config.Enabled && runtimeErr == nil && supported
	// Closing local, verified evidence deliberately does not depend on new
	// external-write configuration or the original command author's authority.
	out.CanClose = out.Command != nil && out.Closure == nil && proven && out.CloseResolution != nil &&
		out.AcceptedReadID != nil && out.Request.SellerID != actor && out.Request.Status == "reconciliation_required"
	return out, tx.Commit(ctx)
}
