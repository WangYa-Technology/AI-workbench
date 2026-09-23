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

type SellerBankPayoutResumeInput struct {
	CommandID     uuid.UUID `json:"commandId"`
	ExpectedJobID uuid.UUID `json:"expectedJobId"`
	Reason        string    `json:"reason"`
	Confirmed     bool      `json:"confirmed"`
}

type SellerBankPayoutResume struct {
	ID               uuid.UUID `json:"id"`
	CommandID        uuid.UUID `json:"commandId"`
	Revision         int       `json:"revision"`
	PredecessorJobID uuid.UUID `json:"predecessorJobId"`
	JobID            uuid.UUID `json:"jobId"`
	CreatedAt        time.Time `json:"createdAt"`
}

type SellerBankPayoutResumeResult struct {
	Resume    SellerBankPayoutResume    `json:"resume"`
	Operation SellerBankPayoutOperation `json:"operation"`
	Replayed  bool                      `json:"replayed"`
}

// ResumeSellerBankPayout never resets first-send evidence or extends the bank
// command window. It atomically binds a new queued job to an unstarted command,
// retaining the stopped predecessor, reason, event and audit as history.
func (s *Service) ResumeSellerBankPayout(ctx context.Context, actor, request uuid.UUID, input SellerBankPayoutResumeInput, key, trace string) (SellerBankPayoutResumeResult, error) {
	out, err := s.resumeSellerBankPayout(ctx, actor, request, input, key, trace)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && oneOf(pgerr.Code, "23505", "23514", "40001", "40P01") {
		err = ErrSellerBankPayoutConflict
	}
	return out, err
}

func (s *Service) resumeSellerBankPayout(ctx context.Context, actor, request uuid.UUID, input SellerBankPayoutResumeInput, key, trace string) (SellerBankPayoutResumeResult, error) {
	var out SellerBankPayoutResumeResult
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if actor == uuid.Nil || request == uuid.Nil || input.CommandID == uuid.Nil || input.ExpectedJobID == uuid.Nil || !input.Confirmed ||
		!sellerPayoutReviewKey.MatchString(key) || !utf8.ValidString(input.Reason) || strings.ContainsRune(input.Reason, 0) || utf8.RuneCountInString(input.Reason) < 10 || utf8.RuneCountInString(input.Reason) > 1000 {
		return out, ErrSellerBankPayoutInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err := paymentFinanceAuthority(ctx, tx, actor, false); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "seller-bank-resume:"+actor.String()+":"+key); err != nil {
		return out, err
	}
	var priorReason string
	err = tx.QueryRow(ctx, `SELECT id,command_id,revision,predecessor_job_id,job_id,created_at,reason
 FROM seller_bank_payout_resumes WHERE actor_id=$1 AND idempotency_key=$2`, actor, key).Scan(
		&out.Resume.ID, &out.Resume.CommandID, &out.Resume.Revision, &out.Resume.PredecessorJobID, &out.Resume.JobID, &out.Resume.CreatedAt, &priorReason)
	if err == nil {
		if out.Resume.CommandID != input.CommandID || out.Resume.PredecessorJobID != input.ExpectedJobID || priorReason != input.Reason {
			return out, ErrSellerBankPayoutConflict
		}
		out.Replayed = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	state, err := lockSellerBankTx(ctx, tx, input.CommandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrSellerPayoutNotFound
	}
	if err != nil {
		return out, err
	}
	if state.Input.PayoutRequestID != request {
		return out, ErrSellerBankPayoutConflict
	}
	if actor == state.SellerID {
		return out, ErrFinanceForbidden
	}
	if err := paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
		return out, err
	}
	if !out.Replayed {
		if state.StartedAt != nil || !s.bankRuntimeAvailable() {
			return out, ErrSellerBankPayoutConflict
		}
		if err := s.validateSellerBankDispatchTx(ctx, tx, state); err != nil {
			return out, err
		}
		var currentJob uuid.UUID
		var currentRevision int
		var status string
		if err := tx.QueryRow(ctx, `SELECT d.execution_job_id,d.resume_revision,j.status
 FROM seller_bank_payout_active_dispatches d JOIN jobs j ON j.id=d.execution_job_id
 WHERE d.command_id=$1 FOR UPDATE OF j`, input.CommandID).Scan(&currentJob, &currentRevision, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return out, ErrSellerBankPayoutConflict
			}
			return out, err
		}
		if currentJob != input.ExpectedJobID || !oneOf(status, "failed", "cancelled", "succeeded") {
			return out, ErrSellerBankPayoutConflict
		}
		if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
 VALUES($1,jsonb_build_object('commandId',$2::uuid),10000) RETURNING id`, SellerBankPayoutJobKind, input.CommandID).Scan(&out.Resume.JobID); err != nil {
			return out, err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO seller_bank_payout_resumes(command_id,revision,predecessor_job_id,job_id,actor_id,idempotency_key,reason)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,command_id,revision,predecessor_job_id,created_at`,
			input.CommandID, currentRevision+1, input.ExpectedJobID, out.Resume.JobID, actor, key, input.Reason).Scan(
			&out.Resume.ID, &out.Resume.CommandID, &out.Resume.Revision, &out.Resume.PredecessorJobID, &out.Resume.CreatedAt); err != nil {
			return out, err
		}
		if err := recordSellerPayoutEventTx(ctx, tx, request, "bank.resumed", &state.Parent, &state.Parent,
			"bank-resumed:"+out.Resume.ID.String(), map[string]any{"resumeId": out.Resume.ID, "jobId": out.Resume.JobID}); err != nil {
			return out, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
 VALUES($1,'seller_payout.bank_resumed','seller_payout_request',$2,$3,$4,
 jsonb_build_object('resumeId',$5::uuid,'commandId',$6::uuid,'predecessorJobId',$7::uuid,'jobId',$8::uuid))`,
			actor, request, input.Reason, trace, out.Resume.ID, input.CommandID, input.ExpectedJobID, out.Resume.JobID); err != nil {
			return out, err
		}
	}
	out.Operation, err = s.sellerBankPayoutOperationTx(ctx, tx, actor, request)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
