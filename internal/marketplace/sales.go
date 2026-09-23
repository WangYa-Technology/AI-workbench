package marketplace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidSaleFilter = errors.New("invalid seller sales filter")

// SellerSale deliberately excludes buyer identity, private refund reasons,
// payment identifiers, source/storage locations and checkout credentials.
// AmountCents is the order's gross amount, never a seller payout or balance.
type SellerSale struct {
	OrderID           uuid.UUID         `json:"orderId"`
	ProductID         uuid.UUID         `json:"productId"`
	Title             string            `json:"title"`
	AmountCents       int               `json:"amountCents"`
	Currency          string            `json:"currency"`
	Status            string            `json:"status"`
	PaymentStatus     string            `json:"paymentStatus"`
	Environment       string            `json:"environment"`
	HasContract       bool              `json:"hasContract"`
	NeedsReview       bool              `json:"needsReview"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
	LicenseAcceptedAt *time.Time        `json:"licenseAcceptedAt,omitempty"`
	PaidAt            *time.Time        `json:"paidAt,omitempty"`
	RefundRequestedAt *time.Time        `json:"refundRequestedAt,omitempty"`
	RefundedAt        *time.Time        `json:"refundedAt,omitempty"`
	Settlement        *SellerSettlement `json:"settlement,omitempty"`
}

// This is the frozen settlement, not a bank balance or permission to withdraw.
// Provider references, destinations, raw hold reasons and query evidence stay private.
type SellerSettlement struct {
	Status              string     `json:"status"`
	Environment         string     `json:"environment"`
	GrossAmountCents    int        `json:"grossAmountCents"`
	FeeBPS              int        `json:"feeBps"`
	FeeCents            int        `json:"feeCents"`
	NetAmountCents      int        `json:"netAmountCents"`
	RecoveryAmountCents int        `json:"recoveryAmountCents"`
	Currency            string     `json:"currency"`
	AvailableAt         *time.Time `json:"availableAt,omitempty"`
	TransferredAt       *time.Time `json:"transferredAt,omitempty"`
}

type SellerSaleDetail struct {
	SellerSale
	LicenseName    string `json:"licenseName"`
	LicenseVersion string `json:"licenseVersion"`
	LicenseTerms   string `json:"licenseTerms"`
}

type SellerSalesFilter struct {
	Status, Environment, Cursor string
	ProductID                   uuid.UUID
	Limit                       int
}

type SellerSalesPage struct {
	Items      []SellerSale `json:"items"`
	Total      int          `json:"total"`
	NextCursor *string      `json:"nextCursor,omitempty"`
}

type SellerSaleEvent struct {
	Sequence   int       `json:"sequence"`
	FromStatus *string   `json:"fromStatus,omitempty"`
	ToStatus   string    `json:"toStatus"`
	CreatedAt  time.Time `json:"createdAt"`
}

type SellerSaleEventsPage struct {
	Items      []SellerSaleEvent `json:"items"`
	NextCursor *string           `json:"nextCursor,omitempty"`
}

type salesCursor struct {
	Version     int       `json:"v"`
	Kind        string    `json:"kind"`
	Actor       uuid.UUID `json:"actor"`
	OrderID     uuid.UUID `json:"order"`
	ProductID   uuid.UUID `json:"product"`
	Status      string    `json:"status"`
	Environment string    `json:"environment"`
	Time        time.Time `json:"time"`
	Sequence    int       `json:"sequence"`
}

func decodeSalesCursor(raw string, expected salesCursor) (salesCursor, error) {
	if raw == "" {
		return expected, nil
	}
	if len(raw) > 1024 {
		return expected, ErrInvalidSaleFilter
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	var c salesCursor
	if err != nil || json.Unmarshal(b, &c) != nil || c.Version != 1 || c.Kind != expected.Kind || c.Actor != expected.Actor || c.ProductID != expected.ProductID || c.Status != expected.Status || c.Environment != expected.Environment {
		return expected, ErrInvalidSaleFilter
	}
	if c.Kind == "sales" && (c.OrderID == uuid.Nil || c.Time.IsZero() || c.Sequence != 0) || c.Kind == "events" && (c.OrderID != expected.OrderID || c.Sequence < 1 || c.Sequence > 2147483647) {
		return expected, ErrInvalidSaleFilter
	}
	return c, nil
}

func encodeSalesCursor(c salesCursor) *string {
	c.Version = 1
	b, _ := json.Marshal(c)
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func salesLimit(n int) (int, error) {
	if n == 0 {
		n = 20
	}
	if n < 1 || n > 50 {
		return 0, ErrInvalidSaleFilter
	}
	return n, nil
}

const salesFrom = ` FROM product_sale_owners own JOIN orders o ON o.id=own.order_id
 LEFT JOIN product_order_contracts c ON c.order_id=o.id
 LEFT JOIN payment_intents pi ON pi.order_id=o.id AND pi.purpose='product'
 LEFT JOIN product_settlements ps ON ps.order_id=o.id `
const saleEnvironment = `CASE WHEN pi.id IS NULL THEN 'unknown' WHEN pi.live_mode THEN 'live' ELSE 'test' END`
const saleSettlementMatches = `COALESCE(ps.seller_id::text=own.seller_id AND ps.payment_id=pi.id
 AND pi.payee_id=ps.seller_id AND pi.payer_id=o.buyer_id AND pi.resource_id=o.product_id
 AND ps.provider=pi.provider AND ps.live_mode=pi.live_mode
 AND ps.gross_amount_cents=pi.amount_cents AND ps.gross_amount_cents=o.amount_cents
 AND ps.currency=pi.currency AND ps.currency=o.currency,false)`
const saleSettlementExpected = `(o.status='fulfilled' OR (o.status='refund_requested' AND pi.compensation_reason IS NULL)
 OR EXISTS(SELECT 1 FROM order_events e WHERE e.order_id=o.id AND e.to_status='fulfilled')
 OR EXISTS(SELECT 1 FROM entitlements e WHERE e.order_id=o.id))`
const saleSettlementJSON = `CASE WHEN ` + saleSettlementMatches + ` THEN jsonb_build_object(
 'status',ps.status,'environment',CASE WHEN ps.live_mode THEN 'live' ELSE 'test' END,
 'grossAmountCents',ps.gross_amount_cents,'feeBps',ps.fee_bps,'feeCents',ps.fee_cents,
 'netAmountCents',ps.net_amount_cents,'recoveryAmountCents',ps.recovery_amount_cents,'currency',ps.currency,
 'availableAt',CASE WHEN isfinite(ps.available_at) THEN ps.available_at END,
 'transferredAt',CASE WHEN isfinite(ps.transferred_at) AND ps.transferred_at<=now() THEN ps.transferred_at END
 ) END`
const salesWhere = ` WHERE own.seller_id=$1::uuid::text AND ($2='' OR o.status=$2)
 AND ($3::uuid IS NULL OR o.product_id=$3) AND ($4='' OR ` + saleEnvironment + `=$4) `
const saleJSON = `jsonb_build_object('orderId',o.id,'productId',o.product_id,'title',o.product_title_snapshot,
 'amountCents',o.amount_cents,'currency',o.currency,'status',o.status,'paymentStatus',COALESCE(pi.status,''),
 'environment',` + saleEnvironment + `,'hasContract',c.order_id IS NOT NULL,
 'needsReview',pi.id IS NULL OR pi.payee_id::text IS DISTINCT FROM own.seller_id OR pi.payer_id<>o.buyer_id
 OR pi.resource_id<>o.product_id OR pi.amount_cents<>o.amount_cents OR pi.currency<>o.currency
 OR NOT CASE o.status
   WHEN 'payment_pending' THEN pi.status IN ('checkout_pending','checkout_open')
   WHEN 'fulfilled' THEN pi.status='paid'
   WHEN 'refund_requested' THEN pi.status IN ('refund_pending','refund_failed')
   WHEN 'refunded' THEN pi.status='refunded'
   WHEN 'cancelled' THEN pi.status='cancelled'
   WHEN 'payment_failed' THEN pi.status='payment_failed'
   ELSE false END
 OR EXISTS(SELECT 1 FROM product_refund_review r WHERE r.payment_id=pi.id)
 OR (ps.id IS NULL AND ` + saleSettlementExpected + `)
 OR (ps.id IS NOT NULL AND (NOT ` + saleSettlementMatches + `
   OR ps.status IN ('recovery_required','provider_unsupported') OR ps.recovery_amount_cents>0
   OR NOT isfinite(ps.available_at)
   OR (ps.transferred_at IS NOT NULL AND (NOT isfinite(ps.transferred_at) OR ps.transferred_at>now())))),
 'createdAt',o.created_at,'updatedAt',o.updated_at,'licenseAcceptedAt',o.license_accepted_at,
 'paidAt',pi.paid_at,'refundRequestedAt',o.refund_requested_at,'refundedAt',o.refunded_at,
 'settlement',` + saleSettlementJSON + `)`

func (s *Service) salesTransaction(ctx context.Context, actor uuid.UUID) (pgx.Tx, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active')`, actor).Scan(&active)
	if err != nil || !active {
		_ = tx.Rollback(ctx)
		if err == nil {
			err = ErrListingForbidden
		}
		return nil, err
	}
	return tx, nil
}

func (s *Service) ListSales(ctx context.Context, actor uuid.UUID, f SellerSalesFilter) (SellerSalesPage, error) {
	page := SellerSalesPage{Items: []SellerSale{}}
	limit, err := salesLimit(f.Limit)
	if err != nil {
		return page, err
	}
	switch f.Status {
	case "", "test_pending", "test_paid", "payment_pending", "payment_paid", "payment_failed", "fulfilled", "refund_requested", "test_refunded", "refunded", "cancelled":
	default:
		return page, ErrInvalidSaleFilter
	}
	switch f.Environment {
	case "", "live", "test", "unknown":
	default:
		return page, ErrInvalidSaleFilter
	}
	c, err := decodeSalesCursor(f.Cursor, salesCursor{Kind: "sales", Actor: actor, ProductID: f.ProductID, Status: f.Status, Environment: f.Environment})
	if err != nil {
		return page, err
	}
	tx, err := s.salesTransaction(ctx, actor)
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	args := []any{actor, f.Status, nullableUUID(f.ProductID), f.Environment}
	if err = tx.QueryRow(ctx, `SELECT count(*)`+salesFrom+salesWhere, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.Query(ctx, `SELECT `+saleJSON+salesFrom+salesWhere+` AND ($5::uuid IS NULL OR (o.created_at,o.id)<($6,$5)) ORDER BY o.created_at DESC,o.id DESC LIMIT $7`, append(args, nullableUUID(c.OrderID), c.Time, limit+1)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item SellerSale
		if err = rows.Scan(&raw); err != nil {
			return page, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		c.OrderID = last.OrderID
		c.Time = last.CreatedAt
		page.NextCursor = encodeSalesCursor(c)
	}
	return page, tx.Commit(ctx)
}

func (s *Service) GetSale(ctx context.Context, actor, id uuid.UUID) (SellerSaleDetail, error) {
	var item SellerSaleDetail
	tx, err := s.salesTransaction(ctx, actor)
	if err != nil {
		return item, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT `+saleJSON+`,o.license_name_snapshot,o.license_version,o.license_terms_snapshot`+salesFrom+` WHERE own.seller_id=$1::uuid::text AND o.id=$2`, actor, id).Scan(&raw, &item.LicenseName, &item.LicenseVersion, &item.LicenseTerms)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	if err = json.Unmarshal(raw, &item.SellerSale); err != nil {
		return item, err
	}
	return item, tx.Commit(ctx)
}

func (s *Service) SaleEvents(ctx context.Context, actor, id uuid.UUID, rawCursor string, requestedLimit int) (SellerSaleEventsPage, error) {
	page := SellerSaleEventsPage{Items: []SellerSaleEvent{}}
	limit, err := salesLimit(requestedLimit)
	if err != nil {
		return page, err
	}
	c, err := decodeSalesCursor(rawCursor, salesCursor{Kind: "events", Actor: actor, OrderID: id})
	if err != nil {
		return page, err
	}
	tx, err := s.salesTransaction(ctx, actor)
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_sale_owners WHERE order_id=$2 AND seller_id=$1::uuid::text)`, actor, id).Scan(&owned); err != nil {
		return page, err
	}
	if !owned {
		return page, ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT sequence,from_status,to_status,created_at FROM order_events WHERE order_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3`, id, c.Sequence, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item SellerSaleEvent
		if err = rows.Scan(&item.Sequence, &item.FromStatus, &item.ToStatus, &item.CreatedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		c.Sequence = page.Items[limit-1].Sequence
		page.NextCursor = encodeSalesCursor(c)
	}
	return page, tx.Commit(ctx)
}
