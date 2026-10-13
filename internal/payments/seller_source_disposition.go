package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// No amount or Provider outcome is accepted. Approval authorizes reads only.
type SellerSourceDispositionInput struct {
	Phase             string     `json:"phase"`
	ProposalID        *uuid.UUID `json:"proposalId,omitempty"`
	ExpectedUpdatedAt time.Time  `json:"expectedUpdatedAt"`
	Reason            string     `json:"reason"`
	Confirmed         bool       `json:"confirmed"`
}
type SellerSourceDisposition struct {
	Reason     string     `json:"reason"`
	ID         uuid.UUID  `json:"id"`
	CommandID  uuid.UUID  `json:"commandId"`
	Phase      string     `json:"phase"`
	ProposalID *uuid.UUID `json:"proposalId,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	Replayed   bool       `json:"replayed"`
}

func (s *Service) DisposeSellerSourceReversal(ctx context.Context, actor, command uuid.UUID, input SellerSourceDispositionInput, key, trace string) (out SellerSourceDisposition, err error) {
	defer func() {
		var p *pgconn.PgError
		if errors.As(err, &p) && oneOf(p.Code, "23505", "23514", "40001", "40P01", "P0002") {
			err = ErrSellerSourceReversalConflict
		}
	}()
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if actor == uuid.Nil || command == uuid.Nil || !input.Confirmed || !oneOf(input.Phase, "propose", "approve") ||
		(input.Phase == "approve") != (input.ProposalID != nil) || input.ProposalID != nil && *input.ProposalID == uuid.Nil || input.ExpectedUpdatedAt.Unix() <= 0 ||
		!sellerPayoutReviewKey.MatchString(key) || !utf8.ValidString(input.Reason) || strings.ContainsRune(input.Reason, 0) || utf8.RuneCountInString(input.Reason) < 10 || utf8.RuneCountInString(input.Reason) > 1000 {
		return out, ErrSellerSourceReversalInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err = paymentFinanceAuthority(ctx, tx, actor, false); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "source-disposition:"+actor.String()+":"+key); err != nil {
		return out, err
	}
	var request uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT payout_request_id FROM seller_source_reversal_commands WHERE id=$1`, command).Scan(&request); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrSellerSourceReversalConflict
	} else if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `SELECT lock_seller_payout_disposition($1)`, request); err != nil {
		return out, err
	}
	if err = paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
		return out, err
	}
	var expected time.Time
	var reason string
	err = tx.QueryRow(ctx, `SELECT id,command_id,phase,proposal_id,created_at,expected_updated_at,reason FROM seller_source_reversal_dispositions WHERE actor_id=$1 AND idempotency_key=$2`, actor, key).Scan(&out.ID, &out.CommandID, &out.Phase, &out.ProposalID, &out.CreatedAt, &expected, &reason)
	if err == nil {
		if out.CommandID != command || out.Phase != input.Phase || !sameReversalUUID(out.ProposalID, input.ProposalID) || !expected.Equal(input.ExpectedUpdatedAt) || reason != input.Reason {
			return out, ErrSellerSourceReversalConflict
		}
		out.Reason = reason
		out.Replayed = true
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO seller_source_reversal_dispositions(command_id,phase,proposal_id,actor_id,expected_updated_at,reason,snapshot,idempotency_key,request_id)
 VALUES($1,$2,$3,$4,$5,$6,seller_source_disposition_snapshot($1),$7,$8) RETURNING id,command_id,phase,proposal_id,created_at`, command, input.Phase, input.ProposalID, actor, input.ExpectedUpdatedAt, input.Reason, key, trace).Scan(&out.ID, &out.CommandID, &out.Phase, &out.ProposalID, &out.CreatedAt)
	if err != nil {
		return out, err
	}
	if input.Phase == "approve" {
		body, e := json.Marshal(sellerSourceReversalPayload{CommandID: command})
		if e != nil {
			return out, e
		}
		var job uuid.UUID
		if err = tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,created_at) VALUES($1,$2,clock_timestamp()) RETURNING id`, SellerSourceReversalCheckJobKind, body).Scan(&job); err != nil {
			return out, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO seller_source_reversal_checks(job_id,command_id) VALUES($1,$2)`, job, command); err != nil {
			return out, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'seller_payout.source_disposition_'||$2::text,'seller_payout_request',$3,$4,jsonb_build_object('dispositionId',$5::uuid,'commandId',$6::uuid,'readOnly',true))`, actor, input.Phase, request, trace, out.ID, command); err != nil {
		return out, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM seller_payout_requests WHERE id=$1`, request).Scan(&status); err != nil {
		return out, err
	}
	if err = recordSellerPayoutEventTx(ctx, tx, request, "source_reversal.disposition", &status, &status, "source-disposition:"+out.ID.String(), map[string]any{"dispositionId": out.ID, "phase": input.Phase, "readOnly": true}); err != nil {
		return out, err
	}
	out.Reason = input.Reason
	return out, tx.Commit(ctx)
}
