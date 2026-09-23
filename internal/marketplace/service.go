package marketplace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/productpolicy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound             = errors.New("product not found")
	ErrIdempotencyConflict  = errors.New("idempotency key conflict")
	ErrOrderNotFound        = errors.New("order not found")
	ErrLegacyRefundEvidence = errors.New("historical internal payment evidence requires reconciliation")
	ErrInvalidRefund        = errors.New("invalid refund request")
	ErrRefundConflict       = errors.New("order cannot be refunded")
	ErrRefundWindowExpired  = errors.New("refund window expired")
	ErrInvalidOrderFilter   = errors.New("invalid order filter")
	ErrInvalidProductFilter = errors.New("invalid product filter")
)

type ListFilter struct {
	Query       string
	ProductType string
	Category    string
	LicenseCode string
	Sort        string
	Limit       int
	Cursor      string
}

type Seller struct {
	ID          uuid.UUID `json:"id"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"displayName"`
}

type License struct {
	Code                 string `json:"code"`
	Name                 string `json:"name"`
	Summary              string `json:"summary"`
	Terms                string `json:"terms"`
	Version              string `json:"version"`
	AllowsCommercial     bool   `json:"allowsCommercial"`
	AllowsDerivatives    bool   `json:"allowsDerivatives"`
	AllowsRedistribution bool   `json:"allowsRedistribution"`
	AttributionRequired  bool   `json:"attributionRequired"`
	RefundWindowDays     int    `json:"refundWindowDays"`
}

type Product struct {
	ListingManaged bool       `json:"listingManaged"`
	PreviewAssetID *uuid.UUID `json:"previewAssetId,omitempty"`
	OfferVersion   string     `json:"offerVersion"`
	ID             uuid.UUID  `json:"id"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	ProductType    string     `json:"productType"`
	Category       string     `json:"category"`
	PriceCents     int        `json:"priceCents"`
	Currency       string     `json:"currency"`
	Status         string     `json:"status"`
	AssetID        uuid.UUID  `json:"assetId"`
	MediaURL       string     `json:"mediaUrl"`
	MediaKind      string     `json:"mediaKind"`
	Width          *int       `json:"width,omitempty"`
	Height         *int       `json:"height,omitempty"`
	AIDisclosure   string     `json:"aiDisclosure"`
	IncludedFiles  []string   `json:"includedFiles"`
	Compatibility  string     `json:"compatibility"`
	Seller         Seller     `json:"seller"`
	License        License    `json:"license"`
	OwnedAssetID   *uuid.UUID `json:"ownedAssetId,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

type OrderEvent struct {
	FromStatus *string   `json:"fromStatus,omitempty"`
	ToStatus   string    `json:"toStatus"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Order struct {
	CheckoutReconciliationRequired bool         `json:"checkoutReconciliationRequired"`
	PaymentVersion                 *int64       `json:"paymentVersion,omitempty"`
	CanCloseCheckout               bool         `json:"canCloseCheckout"`
	CheckoutClosedBeforePayment    bool         `json:"checkoutClosedBeforePayment"`
	ID                             uuid.UUID    `json:"id"`
	ProductID                      uuid.UUID    `json:"productId"`
	ProductTitle                   string       `json:"productTitle"`
	AssetID                        *uuid.UUID   `json:"assetId,omitempty"`
	AmountCents                    int          `json:"amountCents"`
	Currency                       string       `json:"currency"`
	Status                         string       `json:"status"`
	LicenseCode                    string       `json:"licenseCode"`
	LicenseName                    string       `json:"licenseName"`
	LicenseVersion                 string       `json:"licenseVersion"`
	LicenseTerms                   string       `json:"licenseTerms"`
	RefundWindowDays               int          `json:"refundWindowDays"`
	RefundDeadlineAt               *time.Time   `json:"refundDeadlineAt,omitempty"`
	CanRequestRefund               bool         `json:"canRequestRefund"`
	RefundUnavailableReason        string       `json:"refundUnavailableReason,omitempty"`
	PaymentMode                    string       `json:"paymentMode"`
	PaymentID                      *uuid.UUID   `json:"paymentId,omitempty"`
	PaymentStatus                  string       `json:"paymentStatus,omitempty"`
	CheckoutExpiresAt              *time.Time   `json:"checkoutExpiresAt,omitempty"`
	RealCharge                     bool         `json:"realCharge"`
	RefundRequestedAt              *time.Time   `json:"refundRequestedAt,omitempty"`
	RefundedAt                     *time.Time   `json:"refundedAt,omitempty"`
	CreatedAt                      time.Time    `json:"createdAt"`
	Events                         []OrderEvent `json:"events"`
}

type OrderListInput struct {
	Cursor string
	Limit  int
}

type OrderPage struct {
	Items      []Order `json:"items"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

type orderCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) GetProduct(ctx context.Context, viewerID, productID uuid.UUID) (Product, error) {
	item, err := scanProduct(s.pool.QueryRow(ctx, productSelect+` WHERE p.id=$2 AND p.status='active'`, viewerID, productID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Product{}, ErrNotFound
	}
	if err != nil {
		return Product{}, fmt.Errorf("get product: %w", err)
	}
	return item, nil
}

func (s *Service) ListOrders(ctx context.Context, buyerID uuid.UUID, input OrderListInput) (OrderPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return OrderPage{}, ErrInvalidOrderFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeOrderCursor(input.Cursor)
		if err != nil {
			return OrderPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, orderSelect+`
		WHERE o.buyer_id=$1 AND ($2::timestamptz IS NULL OR (o.created_at,o.id)<($2,$3::uuid))
		ORDER BY o.created_at DESC,o.id DESC LIMIT $4`, buyerID, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return OrderPage{}, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()
	items := make([]Order, 0)
	for rows.Next() {
		var item Order
		if err := scanOrder(rows, &item); err != nil {
			return OrderPage{}, fmt.Errorf("scan order: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return OrderPage{}, err
	}
	page := OrderPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeOrderCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	for index := range page.Items {
		events, err := s.orderEvents(ctx, page.Items[index].ID)
		if err != nil {
			return OrderPage{}, err
		}
		page.Items[index].Events = events
	}
	return page, nil
}

func (s *Service) GetOrder(ctx context.Context, buyerID, orderID uuid.UUID) (Order, error) {
	var item Order
	if err := scanOrder(s.pool.QueryRow(ctx, orderSelect+` WHERE o.buyer_id=$1 AND o.id=$2`, buyerID, orderID), &item); errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	} else if err != nil {
		return Order{}, fmt.Errorf("get order: %w", err)
	}
	events, err := s.orderEvents(ctx, item.ID)
	if err != nil {
		return Order{}, err
	}
	item.Events = events
	return item, nil
}

func (s *Service) orderEvents(ctx context.Context, orderID uuid.UUID) ([]OrderEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT from_status,to_status,reason,created_at FROM order_events WHERE order_id=$1 ORDER BY sequence`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list order events: %w", err)
	}
	defer rows.Close()
	events := make([]OrderEvent, 0)
	for rows.Next() {
		var event OrderEvent
		if err := rows.Scan(&event.FromStatus, &event.ToStatus, &event.Reason, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan order event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

const productSelect = `
	SELECT p.id,p.title,p.description,p.product_type,p.category,p.price_cents,p.currency,p.status,p.asset_id,
	       COALESCE(preview.media_url,''),COALESCE(preview.kind,CASE WHEN offer.contract ? 'delivery' THEN 'document' ELSE a.kind END),preview.width,preview.height,p.ai_disclosure,p.included_files,p.compatibility,
	       u.id,u.handle,u.display_name,
	       l.code,l.name,l.summary,l.terms,l.version,l.allows_commercial,l.allows_derivatives,l.allows_redistribution,
	       l.attribution_required,l.refund_window_days,e.asset_id,p.created_at,offer.offer_version,preview.asset_id,EXISTS(SELECT 1 FROM product_publications m WHERE m.product_id=p.id)` + productFrom

const productFrom = ` FROM public_products p
	LEFT JOIN public_product_previews preview ON preview.product_id=p.id
	JOIN product_offers offer ON offer.product_id=p.id
	JOIN assets a ON a.id=p.asset_id
	JOIN users u ON u.id=p.seller_id
	JOIN licenses l ON l.code=p.license_code
	LEFT JOIN entitlements e ON e.product_id=p.id AND e.user_id=$1 AND e.status='active'`

const orderSelect = `
	SELECT o.id,p.id,o.product_title_snapshot,e.asset_id,o.amount_cents,o.currency,o.status,COALESCE(c.contract->'license'->>'code',e.license_code,p.license_code),o.license_name_snapshot,
	       o.license_version,o.license_terms_snapshot,o.refund_window_days_snapshot,o.refund_requested_at,o.refunded_at,o.created_at,
	       CASE WHEN pi.provider IS NOT NULL THEN pi.provider WHEN legacy.order_id IS NOT NULL THEN 'test' ELSE 'unverified' END,COALESCE(pi.live_mode,false),COALESCE(pi.status,''),pi.id,pi.checkout_expires_at,EXISTS(SELECT 1 FROM product_refund_review r WHERE r.payment_id=pi.id),
         pi.version,EXISTS(SELECT 1 FROM product_checkout_locally_closable closable WHERE closable.payment_id=pi.id),
         EXISTS(SELECT 1 FROM product_checkout_closures closed WHERE closed.payment_id=pi.id),
         legacy.order_id IS NOT NULL
	FROM orders o
	LEFT JOIN product_order_contracts c ON c.order_id=o.id
	JOIN products p ON p.id=o.product_id
	LEFT JOIN entitlements e ON e.order_id=o.id
	LEFT JOIN payment_intents pi ON pi.order_id=o.id
 LEFT JOIN legacy_product_refund_evidence legacy ON legacy.order_id=o.id AND pi.id IS NULL`

func scanOrder(row scanner, item *Order) error {
	var needsReview, legacyEvidence bool
	err := row.Scan(&item.ID, &item.ProductID, &item.ProductTitle, &item.AssetID, &item.AmountCents,
		&item.Currency, &item.Status, &item.LicenseCode, &item.LicenseName, &item.LicenseVersion,
		&item.LicenseTerms, &item.RefundWindowDays, &item.RefundRequestedAt, &item.RefundedAt, &item.CreatedAt,
		&item.PaymentMode, &item.RealCharge, &item.PaymentStatus, &item.PaymentID, &item.CheckoutExpiresAt, &needsReview, &item.PaymentVersion, &item.CanCloseCheckout, &item.CheckoutClosedBeforePayment, &legacyEvidence)
	if err == nil {
		item.CheckoutReconciliationRequired = needsReview && item.Status == "payment_pending"
		item.RefundDeadlineAt = productpolicy.RefundDeadline(item.CreatedAt, item.RefundWindowDays)
		switch {
		case item.Status != "fulfilled" || (item.PaymentStatus != "" && item.PaymentStatus != "paid"):
			item.RefundUnavailableReason = "order_state"
		case needsReview || (item.PaymentID == nil && !legacyEvidence):
			item.RefundUnavailableReason = "reconciliation_required"
		case !productpolicy.RefundWindowOpen(item.CreatedAt, item.RefundWindowDays, time.Now()):
			item.RefundUnavailableReason = "window_expired"
		default:
			item.CanRequestRefund = true
		}
	}
	return err
}

func encodeOrderCursor(item Order) string {
	body, _ := json.Marshal(orderCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeOrderCursor(value string) (orderCursor, error) {
	var cursor orderCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return orderCursor{}, ErrInvalidOrderFilter
	}
	return cursor, nil
}

type scanner interface {
	Scan(...any) error
}

func scanProduct(row scanner) (Product, error) {
	var item Product
	err := row.Scan(&item.ID, &item.Title, &item.Description, &item.ProductType, &item.Category, &item.PriceCents, &item.Currency,
		&item.Status, &item.AssetID, &item.MediaURL, &item.MediaKind, &item.Width, &item.Height, &item.AIDisclosure, &item.IncludedFiles, &item.Compatibility,
		&item.Seller.ID, &item.Seller.Handle, &item.Seller.DisplayName,
		&item.License.Code, &item.License.Name, &item.License.Summary, &item.License.Terms, &item.License.Version,
		&item.License.AllowsCommercial, &item.License.AllowsDerivatives, &item.License.AllowsRedistribution,
		&item.License.AttributionRequired, &item.License.RefundWindowDays, &item.OwnedAssetID, &item.CreatedAt, &item.OfferVersion, &item.PreviewAssetID, &item.ListingManaged)
	return item, err
}
