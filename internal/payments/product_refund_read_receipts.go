package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func saveLateRefundReceiptTx(ctx context.Context, tx pgx.Tx, checkID, paymentID uuid.UUID, observations []RefundObservation, readErr error, execution *refundCheckExecution) error {
	if execution == nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	var code *string
	if readErr != nil {
		value := SanitizeProviderError(readErr).Error()
		code = &value
	}
	encoded, err := json.Marshal(observations)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(struct {
		Check        uuid.UUID
		Attempt      int
		Lease        *uuid.UUID
		Complete     bool
		Error        *string
		Observations []RefundObservation
	}{checkID, execution.attempts, execution.leaseToken, readErr == nil, code, observations})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(evidence)
	id := uuid.New()
	result, err := tx.Exec(ctx, `INSERT INTO product_refund_read_receipts(id,check_id,attempt_number,lease_token,complete,error_code,observations,evidence_sha256)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(check_id,attempt_number,evidence_sha256) DO NOTHING`, id, checkID, execution.attempts, execution.leaseToken, readErr == nil, code, encoded, hex.EncodeToString(digest[:]))
	if err != nil || result.RowsAffected() == 0 {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE payment_intents SET version=version+1,updated_at=now() WHERE id=$1`, paymentID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 VALUES('payment.refund_read_receipt_saved','payment',$1,$2,jsonb_build_object('checkId',$3::uuid,'receiptId',$4::uuid,'observationCount',$5::integer,'complete',$6::boolean))`, paymentID, "refund-receipt:"+id.String(), checkID, id, len(observations), readErr == nil)
	return err
}

type RefundReadReceipt struct {
	UnresolvedProviderRefundIDs []string            `json:"unresolvedProviderRefundIds"`
	ID                          uuid.UUID           `json:"id"`
	CheckID                     uuid.UUID           `json:"checkId"`
	AttemptNumber               int                 `json:"attemptNumber"`
	Complete                    bool                `json:"complete"`
	ErrorCode                   *string             `json:"errorCode,omitempty"`
	CreatedAt                   time.Time           `json:"createdAt"`
	Observations                []RefundObservation `json:"observations"`
}

type RefundReadReceiptPage struct {
	Items      []RefundReadReceipt `json:"items"`
	NextCursor *string             `json:"nextCursor,omitempty"`
}

// One immutable receipt per page bounds both response bytes and rendering.
// The cursor is an existing receipt under this exact payment/check, not a
// globally reusable record ID. Reading history never calls the provider.
func (s *Service) ListRefundReadReceipts(ctx context.Context, paymentID, checkID uuid.UUID, cursor string) (RefundReadReceiptPage, error) {
	page := RefundReadReceiptPage{Items: []RefundReadReceipt{}}
	if s == nil || s.pool == nil || paymentID == uuid.Nil || checkID == uuid.Nil {
		return page, ErrRefundHistoryNotFound
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_checks c JOIN payment_intents p ON p.id=c.payment_id WHERE c.id=$1 AND p.id=$2 AND p.purpose='product')`, checkID, paymentID).Scan(&exists); err != nil {
		return page, err
	}
	if !exists {
		return page, ErrRefundHistoryNotFound
	}
	var afterID *uuid.UUID
	var afterTime *time.Time
	if cursor != "" {
		id, err := uuid.Parse(cursor)
		if err != nil || id == uuid.Nil || len(cursor) > 36 {
			return page, ErrInvalidRefund
		}
		var at time.Time
		if err = s.pool.QueryRow(ctx, `SELECT created_at FROM product_refund_read_receipts WHERE id=$1 AND check_id=$2`, id, checkID).Scan(&at); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return page, ErrInvalidRefund
			}
			return page, err
		}
		afterID, afterTime = &id, &at
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id,r.check_id,r.attempt_number,r.complete,r.error_code,r.created_at,r.observations,
 COALESCE((SELECT jsonb_agg(x.provider_refund_id ORDER BY x.provider_refund_id) FROM
 (SELECT DISTINCT g.provider_refund_id FROM product_refund_observation_gaps g WHERE g.check_id=r.check_id
 AND g.provider_refund_id IN(SELECT value->>'providerId' FROM jsonb_array_elements(r.observations))) x),'[]'::jsonb)
 FROM product_refund_read_receipts r WHERE r.check_id=$1
 AND ($2::timestamptz IS NULL OR (r.created_at,r.id)<($2,$3::uuid)) ORDER BY r.created_at DESC,r.id DESC LIMIT 2`, checkID, afterTime, afterID)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item RefundReadReceipt
		var body, unresolved []byte
		if err = rows.Scan(&item.ID, &item.CheckID, &item.AttemptNumber, &item.Complete, &item.ErrorCode, &item.CreatedAt, &body, &unresolved); err != nil {
			return page, err
		}
		if err = json.Unmarshal(body, &item.Observations); err != nil {
			return page, err
		}
		if err = json.Unmarshal(unresolved, &item.UnresolvedProviderRefundIDs); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > 1 {
		page.Items = page.Items[:1]
		next := page.Items[0].ID.String()
		page.NextCursor = &next
	}
	return page, nil
}
