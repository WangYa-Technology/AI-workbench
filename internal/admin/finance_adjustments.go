package admin

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/jackc/pgx/v5"
)

var financeAdjustmentKey = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

type FinanceAdjustmentResult struct {
	FinanceAccount
	OperationID uuid.UUID `json:"operationId"`
	Replayed    bool      `json:"replayed"`
}

func (s *Service) AdjustFinance(ctx context.Context, actorID, userID uuid.UUID, input FinanceAdjustment, key, requestID string) (FinanceAdjustmentResult, error) {
	item, err := s.adjustFinance(ctx, actorID, userID, input, key, requestID)
	return item, financeCommandError(err)
}

func (s *Service) adjustFinance(ctx context.Context, actorID, userID uuid.UUID, input FinanceAdjustment, key, requestID string) (FinanceAdjustmentResult, error) {
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if actorID == uuid.Nil || userID == uuid.Nil || !financeAdjustmentKey.MatchString(key) || input.DeltaCents == 0 || input.Currency != "USD" || input.DeltaCents > 1000000 || input.DeltaCents < -1000000 {
		return FinanceAdjustmentResult{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return FinanceAdjustmentResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := financeAuthorityTx(ctx, tx, actorID, false); err != nil {
		return FinanceAdjustmentResult{}, err
	}
	// Scope the key to the operator, not the target. Reusing it for another
	// account is a conflict, never a second monetary operation. Acquire before
	// billing/authority locks and read again after waiting for a prior commit.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "admin:finance-adjust:"+actorID.String()+":"+key); err != nil {
		return FinanceAdjustmentResult{}, err
	}
	var operationID, previousUser uuid.UUID
	var delta int
	var currency string
	err = tx.QueryRow(ctx, `SELECT operation_id,user_id,delta_cents,currency FROM admin_finance_adjustments WHERE actor_id=$1 AND idempotency_key=$2`, actorID, key).Scan(&operationID, &previousUser, &delta, &currency)
	replayed := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return FinanceAdjustmentResult{}, err
	}
	if replayed {
		if previousUser != userID || delta != input.DeltaCents || currency != input.Currency {
			return FinanceAdjustmentResult{}, ErrConflict
		}
	} else {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM billing_accounts WHERE user_id=$1 AND currency=$2)`, userID, input.Currency).Scan(&exists); err != nil {
			return FinanceAdjustmentResult{}, err
		}
		if !exists {
			return FinanceAdjustmentResult{}, ErrNotFound
		}
		operationID = uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO admin_finance_adjustments(operation_id,actor_id,idempotency_key,user_id,delta_cents,currency,request_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, operationID, actorID, key, userID, input.DeltaCents, input.Currency, requestID); err != nil {
			return FinanceAdjustmentResult{}, err
		}
		if _, err := billing.AdjustTx(ctx, tx, userID, operationID, input.DeltaCents, input.Currency, "Administrative balance adjustment", map[string]any{"actorId": actorID.String(), "requestId": requestID, "source": "admin_adjustment"}); err != nil {
			return FinanceAdjustmentResult{}, err
		}
	}
	// Replays still require current authority; they are not a bypass for
	// reading an account after suspension or role/permission revocation.
	if err := financeAuthorityTx(ctx, tx, actorID, true); err != nil {
		return FinanceAdjustmentResult{}, err
	}
	if !replayed {
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
 VALUES($1,'admin.finance_adjusted','user',$2,'Administrator adjusted account balance',$3,
 jsonb_build_object('operationId',$4::text,'deltaCents',$5::integer,'currency',$6::text))`, actorID, userID, requestID, operationID, input.DeltaCents, input.Currency); err != nil {
			return FinanceAdjustmentResult{}, err
		}
	}
	// Return the account observed in this authorized transaction, not a new
	// post-commit query which may fail after an already committed adjustment.
	account, err := scanFinanceAccount(tx.QueryRow(ctx, financeAccountSelect+` WHERE b.user_id=$1 AND b.currency=$2`, userID, input.Currency))
	if errors.Is(err, pgx.ErrNoRows) {
		return FinanceAdjustmentResult{}, ErrNotFound
	}
	if err != nil {
		return FinanceAdjustmentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FinanceAdjustmentResult{}, err
	}
	return FinanceAdjustmentResult{FinanceAccount: account, OperationID: operationID, Replayed: replayed}, nil
}
