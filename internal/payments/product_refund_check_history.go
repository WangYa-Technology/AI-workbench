package payments

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Summaries deliberately omit observation arrays; opening one check retrieves
// its bounded original evidence. RequiresReview reflects current bindings,
// while UnresolvedCount remains the immutable result at the time of the check.
type RefundCheckSummary struct {
	ID              uuid.UUID  `json:"id"`
	Origin          string     `json:"origin"`
	Status          string     `json:"status"`
	UnresolvedCount int        `json:"unresolvedCount"`
	RequiresReview  bool       `json:"requiresReview"`
	CreatedAt       time.Time  `json:"createdAt"`
	ObservedAt      *time.Time `json:"observedAt,omitempty"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}

type RefundCheckPage struct {
	Items      []RefundCheckSummary `json:"items"`
	NextCursor *string              `json:"nextCursor,omitempty"`
}

type RefundCheckDetail struct {
	UnrecordedReadCount int `json:"unrecordedReadCount"`
	RecoveredReadCount  int `json:"recoveredReadCount"`
	LateReceiptCount    int `json:"lateReceiptCount"`
	RefundCheck
	UnresolvedProviderRefundIDs []string `json:"unresolvedProviderRefundIds"`
}

type refundCheckCursor struct {
	Payment uuid.UUID `json:"p"`
	Check   uuid.UUID `json:"c"`
	Review  string    `json:"r"`
}

func (s *Service) ListRefundChecks(ctx context.Context, paymentID uuid.UUID, review, cursor string, limit int) (RefundCheckPage, error) {
	page := RefundCheckPage{Items: []RefundCheckSummary{}}
	if s == nil || s.pool == nil || paymentID == uuid.Nil {
		return page, ErrRefundHistoryNotFound
	}
	if review == "" {
		review = "all"
	}
	if limit == 0 {
		limit = 20
	}
	if !oneOf(review, "all", "unresolved") || limit < 1 || limit > 50 || len(cursor) > 512 {
		return page, ErrInvalidRefund
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_intents WHERE id=$1 AND purpose='product')`, paymentID).Scan(&exists); err != nil {
		return page, err
	}
	if !exists {
		return page, ErrRefundHistoryNotFound
	}
	var afterID *uuid.UUID
	var afterTime *time.Time
	if cursor != "" {
		var decoded refundCheckCursor
		body, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(body, &decoded) != nil || decoded.Payment != paymentID || decoded.Review != review || decoded.Check == uuid.Nil {
			return page, ErrInvalidRefund
		}
		var at time.Time
		// The cursor is a stable historical watermark even if a pending binding
		// was resolved since the previous page. It never selects another payment.
		if err := s.pool.QueryRow(ctx, `SELECT created_at FROM product_refund_checks WHERE id=$1 AND payment_id=$2`, decoded.Check, paymentID).Scan(&at); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return page, ErrInvalidRefund
			}
			return page, err
		}
		afterID, afterTime = &decoded.Check, &at
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id,c.origin,
 CASE WHEN j.status IN ('failed','cancelled') AND c.status IN ('requested','observed') THEN 'failed' ELSE c.status END,
 c.unresolved_count,(EXISTS(SELECT 1 FROM product_refund_observation_gaps g WHERE g.check_id=c.id) OR EXISTS(SELECT 1 FROM product_refund_read_gaps g WHERE g.check_id=c.id)),
 c.created_at,c.observed_at,c.completed_at
 FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.payment_id=$1
 AND ($2='all' OR (EXISTS(SELECT 1 FROM product_refund_observation_gaps g WHERE g.check_id=c.id) OR EXISTS(SELECT 1 FROM product_refund_read_gaps g WHERE g.check_id=c.id)))
 AND ($3::timestamptz IS NULL OR (c.created_at,c.id)<($3,$4::uuid))
 ORDER BY c.created_at DESC,c.id DESC LIMIT $5`, paymentID, review, afterTime, afterID, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item RefundCheckSummary
		if err := rows.Scan(&item.ID, &item.Origin, &item.Status, &item.UnresolvedCount, &item.RequiresReview, &item.CreatedAt, &item.ObservedAt, &item.CompletedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		body, _ := json.Marshal(refundCheckCursor{Payment: paymentID, Check: page.Items[limit-1].ID, Review: review})
		encoded := base64.RawURLEncoding.EncodeToString(body)
		page.NextCursor = &encoded
	}
	return page, nil
}

func (s *Service) GetRefundCheck(ctx context.Context, paymentID, checkID uuid.UUID) (RefundCheckDetail, error) {
	var result RefundCheckDetail
	if s == nil || s.pool == nil || paymentID == uuid.Nil || checkID == uuid.Nil {
		return result, ErrRefundHistoryNotFound
	}
	var observations, unresolved []byte
	err := s.pool.QueryRow(ctx, `SELECT c.origin,c.id,
 CASE WHEN j.status IN ('failed','cancelled') AND c.status IN ('requested','observed') THEN 'failed' ELSE c.status END,
 c.unresolved_count,COALESCE(c.error_code,j.last_error_code),c.created_at,c.observed_at,c.completed_at,
 COALESCE(c.observations,'[]'::jsonb),
 COALESCE((SELECT jsonb_agg(x.provider_refund_id ORDER BY x.provider_refund_id) FROM
  (SELECT DISTINCT provider_refund_id FROM product_refund_observation_gaps g WHERE g.check_id=c.id AND g.provider_refund_id IN (SELECT value->>'providerId' FROM jsonb_array_elements(COALESCE(c.observations,'[]'::jsonb)))) x),'[]'::jsonb),
 (SELECT count(*) FROM product_refund_read_receipts r WHERE r.check_id=c.id),
 (SELECT count(*) FROM product_refund_read_gaps g WHERE g.check_id=c.id),
 (SELECT count(*) FROM product_refund_read_recoveries r JOIN product_refund_read_executions e ON e.id=r.execution_id WHERE e.check_id=c.id)
 FROM product_refund_checks c JOIN payment_intents p ON p.id=c.payment_id AND p.purpose='product'
 JOIN jobs j ON j.id=c.job_id WHERE c.payment_id=$1 AND c.id=$2`, paymentID, checkID).Scan(
		&result.Origin, &result.ID, &result.Status, &result.UnresolvedCount, &result.ErrorCode, &result.CreatedAt, &result.ObservedAt, &result.CompletedAt, &observations, &unresolved, &result.LateReceiptCount, &result.UnrecordedReadCount, &result.RecoveredReadCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrRefundHistoryNotFound
	}
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(observations, &result.Observations); err != nil {
		return result, err
	}
	if err = json.Unmarshal(unresolved, &result.UnresolvedProviderRefundIDs); err != nil {
		return result, err
	}
	return result, nil
}
