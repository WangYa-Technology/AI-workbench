package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrSellerPayoutFilter = errors.New("invalid seller payout pagination")

type SellerPayoutItem struct {
	SellerPayoutRequest
	Environment   string                    `json:"environment"`
	CanCancel     bool                      `json:"canCancel"`
	CanSelectBank bool                      `json:"canSelectBank"`
	BankTarget    *SellerPayoutBankTarget   `json:"bankTarget,omitempty"`
	LatestReview  *SellerPayoutDecision     `json:"latestReview,omitempty"`
	BankPayout    *SellerBankPayoutStatus   `json:"bankPayout,omitempty"`
	SourceReturn  *SellerSourceReturnStatus `json:"sourceReturn,omitempty"`
}

// SellerSourceReturnStatus is an owner-safe projection. Provider evidence,
// finance identities, reasons and command keys remain finance-only.
type SellerSourceReturnStatus struct {
	Status         string     `json:"status"`
	RequiresReview bool       `json:"requiresReview"`
	ObservedAt     *time.Time `json:"observedAt,omitempty"`
	ClosedAt       *time.Time `json:"closedAt,omitempty"`
	Resolution     *string    `json:"resolution,omitempty"`
}

// Explicit seller-facing projection: never include internal notes, operator
// identity, command keys or financial audit metadata from the review.
type SellerPayoutDecision struct {
	Revision      int       `json:"revision"`
	Decision      string    `json:"decision"`
	SellerMessage string    `json:"sellerMessage,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}
type SellerPayoutPage struct {
	Items      []SellerPayoutItem `json:"items"`
	NextCursor string             `json:"nextCursor,omitempty"`
}
type SellerPayoutOption struct {
	SettlementID uuid.UUID `json:"settlementId"`
	OrderID      uuid.UUID `json:"orderId"`
	Title        string    `json:"title"`
	AmountCents  int       `json:"amountCents"`
	Currency     string    `json:"currency"`
	Environment  string    `json:"environment"`
	AvailableAt  time.Time `json:"availableAt"`
}
type SellerPayoutOptions struct {
	Items        []SellerPayoutOption `json:"items"`
	Availability string               `json:"availability"`
	NextCursor   string               `json:"nextCursor,omitempty"`
}

func payoutPageLimit(cursor string, limit int) (uuid.UUID, int, error) {
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 {
		return uuid.Nil, 0, ErrSellerPayoutFilter
	}
	id := uuid.Nil
	if cursor != "" {
		var err error
		id, err = uuid.Parse(cursor)
		if err != nil || id == uuid.Nil {
			return uuid.Nil, 0, ErrSellerPayoutFilter
		}
	}
	return id, limit, nil
}

const sellerPayoutItemSelect = `SELECT r.id,r.seller_id,r.amount_cents,r.currency,r.idempotency_key,r.status,r.failure_code,r.created_at,r.updated_at,
 CASE WHEN ps.id IS NULL THEN 'unknown' WHEN ps.live_mode THEN 'live' ELSE 'test' END,
 r.status IN ('requested','under_review') AND NOT EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=r.id),
 COALESCE(b.payout_request_id IS NULL AND r.status IN ('requested','under_review') AND a.released_at IS NULL AND ps.status='available'
 AND EXISTS(SELECT 1 FROM product_settlement_settings WHERE singleton=true AND payout_mode='seller_payout')
 AND NOT EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=r.id),false),
 CASE WHEN b.payout_request_id IS NULL THEN NULL ELSE jsonb_build_object('payoutRequestId',b.payout_request_id,
 'destinationId',b.destination_id,'bankDestinationId',b.bank_destination_id,'bankName',b.bank_name,'last4',b.last4,'currency',b.currency,'observedAt',b.observed_at,'createdAt',b.created_at) END,
 (SELECT jsonb_build_object('revision',v.revision,'decision',v.decision,'sellerMessage',v.seller_message,'createdAt',v.created_at)
 FROM seller_payout_reviews v WHERE v.payout_request_id=r.id ORDER BY v.revision DESC LIMIT 1),
 ` + sellerBankPayoutStatusSelect + `,
 (SELECT jsonb_strip_nulls(jsonb_build_object(
  'status',CASE WHEN x.id IS NOT NULL THEN 'closed'
    WHEN result.command_id IS NOT NULL THEN 'observed'
    WHEN EXISTS(SELECT 1 FROM seller_source_reversal_reads r2 WHERE r2.command_id=c.id AND r2.requires_review) THEN 'requires_review'
    ELSE 'pending' END,
  'requiresReview',x.id IS NULL AND EXISTS(SELECT 1 FROM seller_source_reversal_reads r2 WHERE r2.command_id=c.id
    AND (r2.requires_review OR (r2.finished_at IS NULL AND r2.deadline_at+interval '5 seconds'<statement_timestamp()))),
  'observedAt',result.created_at,'closedAt',x.created_at,'resolution',x.resolution))
  FROM seller_source_reversal_commands c
  LEFT JOIN seller_source_reversal_results result ON result.command_id=c.id
  LEFT JOIN seller_source_reversal_closures x ON x.command_id=c.id
  WHERE c.payout_request_id=r.id AND c.seller_id=r.seller_id)
 FROM seller_payout_requests r JOIN users u ON u.id=r.seller_id AND u.status='active'
 LEFT JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id
 LEFT JOIN product_settlements ps ON ps.id=a.settlement_id AND ps.seller_id=r.seller_id
 LEFT JOIN seller_payout_bank_targets b ON b.payout_request_id=r.id AND b.seller_id=r.seller_id`

func (s *Service) scanSellerPayoutItem(row pgx.Row) (SellerPayoutItem, error) {
	var item SellerPayoutItem
	var bank, review, bankPayout, sourceReturn []byte
	err := row.Scan(&item.ID, &item.SellerID, &item.AmountCents, &item.Currency, &item.IdempotencyKey, &item.Status, &item.FailureCode, &item.CreatedAt, &item.UpdatedAt, &item.Environment, &item.CanCancel, &item.CanSelectBank, &bank, &review, &bankPayout, &sourceReturn)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrSellerPayoutNotFound
	}
	if err != nil {
		return item, err
	}
	item.CanSelectBank = item.CanSelectBank && s.config.Enabled
	if bank != nil {
		if err := json.Unmarshal(bank, &item.BankTarget); err != nil {
			return item, err
		}
	}
	if review != nil {
		if err := json.Unmarshal(review, &item.LatestReview); err != nil {
			return item, err
		}
	}
	if bankPayout != nil {
		if err := json.Unmarshal(bankPayout, &item.BankPayout); err != nil {
			return item, err
		}
	}
	if sourceReturn != nil {
		if err := json.Unmarshal(sourceReturn, &item.SourceReturn); err != nil {
			return item, err
		}
	}
	return item, nil
}

func (s *Service) GetSellerPayoutRequest(ctx context.Context, sellerID, requestID uuid.UUID) (SellerPayoutItem, error) {
	if s == nil || s.pool == nil {
		return SellerPayoutItem{}, ErrDisabled
	}
	if sellerID == uuid.Nil || requestID == uuid.Nil {
		return SellerPayoutItem{}, ErrSellerPayoutNotFound
	}
	return s.scanSellerPayoutItem(s.pool.QueryRow(ctx, sellerPayoutItemSelect+` WHERE r.seller_id=$1 AND r.id=$2`, sellerID, requestID))
}

func (s *Service) ListSellerPayoutRequests(ctx context.Context, sellerID uuid.UUID, cursor string, limit int) (SellerPayoutPage, error) {
	out := SellerPayoutPage{Items: []SellerPayoutItem{}}
	if s == nil || s.pool == nil || sellerID == uuid.Nil {
		return out, ErrDisabled
	}
	after, limit, err := payoutPageLimit(cursor, limit)
	if err != nil {
		return out, err
	}
	// Cursor records are owner checked; even a guessed UUID cannot change the
	// seller predicate or reveal whether another seller owns that record.
	var afterTime time.Time
	if after != uuid.Nil {
		if err := s.pool.QueryRow(ctx, `SELECT created_at FROM seller_payout_requests WHERE id=$1 AND seller_id=$2`, after, sellerID).Scan(&afterTime); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return out, ErrSellerPayoutFilter
			}
			return out, err
		}
	}
	rows, err := s.pool.Query(ctx, sellerPayoutItemSelect+`
 WHERE r.seller_id=$1 AND ($2::uuid='00000000-0000-0000-0000-000000000000'::uuid OR (r.created_at,r.id)<($3::timestamptz,$2::uuid))
 ORDER BY r.created_at DESC,r.id DESC LIMIT $4`, sellerID, after, afterTime, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := s.scanSellerPayoutItem(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].ID.String()
	}
	return out, nil
}

func (s *Service) SellerPayoutOptions(ctx context.Context, sellerID uuid.UUID, cursor string, limit int) (SellerPayoutOptions, error) {
	out := SellerPayoutOptions{Items: []SellerPayoutOption{}, Availability: "unavailable"}
	if s == nil || s.pool == nil || sellerID == uuid.Nil {
		return out, ErrDisabled
	}
	after, limit, err := payoutPageLimit(cursor, limit)
	if err != nil {
		return out, err
	}
	var afterTime time.Time
	if after != uuid.Nil {
		if err := s.pool.QueryRow(ctx, `SELECT available_at FROM seller_ledger_entries WHERE settlement_id=$1 AND seller_id=$2 AND entry_type='settlement_credit'`, after, sellerID).Scan(&afterTime); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return out, ErrSellerPayoutFilter
			}
			return out, err
		}
	}
	var mode string
	if err := s.pool.QueryRow(ctx, `SELECT payout_mode FROM product_settlement_settings WHERE singleton=true`).Scan(&mode); err != nil {
		return out, err
	}
	if mode != "seller_payout" {
		out.Availability = "automatic"
		return out, nil
	}
	if !s.config.Enabled {
		return out, nil
	}
	out.Availability = "available"
	rows, err := s.pool.Query(ctx, `SELECT ps.id,ps.order_id,COALESCE(o.product_title_snapshot,''),e.amount_cents,e.currency,
 CASE WHEN ps.live_mode THEN 'live' ELSE 'test' END,e.available_at
 FROM seller_ledger_entries e JOIN product_settlements ps ON ps.id=e.settlement_id AND ps.seller_id=e.seller_id
 JOIN users u ON u.id=e.seller_id AND u.status='active'
 JOIN payment_intents p ON p.id=ps.payment_id JOIN orders o ON o.id=ps.order_id AND p.order_id=o.id
 JOIN product_sale_owners own ON own.order_id=o.id AND own.seller_id=ps.seller_id::text
 JOIN product_checkout_requests original ON original.payment_id=p.id
 JOIN payment_destinations d ON d.provider=ps.provider AND d.user_id=ps.seller_id
 WHERE e.seller_id=$1 AND e.entry_type='settlement_credit' AND e.currency='USD' AND e.amount_cents=ps.net_amount_cents
 AND e.available_at<=clock_timestamp() AND ps.available_at<=clock_timestamp()
 AND ps.status='available' AND ps.provider='stripe' AND ps.currency=e.currency
 AND p.status='paid' AND o.status='fulfilled' AND p.live_mode=ps.live_mode
 AND p.purpose='product' AND p.payer_id=o.buyer_id AND p.resource_id=o.product_id AND p.payee_id=ps.seller_id
 AND p.provider=ps.provider AND p.amount_cents=ps.gross_amount_cents AND p.amount_cents=o.amount_cents
 AND p.currency=ps.currency AND p.currency=o.currency
 AND original.identity->>'provider'=p.provider AND original.identity->'liveMode'=to_jsonb(p.live_mode)
 AND original.identity->>'merchantId'<>'' AND original.identity->>'endpoint'<>''
 AND original.request->>'PaymentID'=p.id::text AND original.request->>'Purpose'='product'
 AND original.request->>'OrderExternalID'=o.id::text AND original.request->>'ResourceID'=p.resource_id::text
 AND original.request->>'BuyerIdentity'=p.payer_id::text AND original.request->'AmountCents'=to_jsonb(p.amount_cents)
 AND original.request->>'Currency'=p.currency
 AND d.status='verified' AND d.charges_enabled AND d.payouts_enabled
 AND d.original_merchant_id=original.identity->>'merchantId' AND d.original_live_mode=ps.live_mode
 AND COALESCE(d.original_store_id,'')=COALESCE(original.identity->>'storeId','')
 AND d.original_endpoint=original.identity->>'endpoint' AND d.original_api_version=original.identity->>'apiVersion'
 AND d.original_request_version=original.identity->>'requestVersion'
 AND NOT EXISTS(SELECT 1 FROM seller_payout_request_allocations WHERE settlement_id=ps.id AND released_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=ps.id AND reserved_at IS NOT NULL)
 AND NOT seller_settlement_has_open_source(ps.id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
 AND NOT seller_funds_recovery_blocks($1,ps.id)
 AND ($2::uuid='00000000-0000-0000-0000-000000000000'::uuid OR (e.available_at,ps.id)<($3::timestamptz,$2::uuid))
 ORDER BY e.available_at DESC,ps.id DESC LIMIT $4`, sellerID, after, afterTime, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item SellerPayoutOption
		if err := rows.Scan(&item.SettlementID, &item.OrderID, &item.Title, &item.AmountCents, &item.Currency, &item.Environment, &item.AvailableAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].SettlementID.String()
	}
	return out, nil
}
