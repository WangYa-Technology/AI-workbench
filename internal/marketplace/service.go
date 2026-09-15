package marketplace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/risk"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound            = errors.New("product not found")
	ErrInvalidPurchase     = errors.New("invalid purchase")
	ErrSellerPurchase      = errors.New("seller cannot purchase own product")
	ErrIdempotencyConflict = errors.New("idempotency key conflict")
	ErrOrderNotFound       = errors.New("order not found")
	ErrInvalidRefund       = errors.New("invalid refund request")
	ErrRefundConflict      = errors.New("order cannot be refunded")
	ErrRefundWindowExpired = errors.New("refund window expired")
	ErrInvalidOrderFilter  = errors.New("invalid order filter")
)

type ListFilter struct {
	Query       string
	ProductType string
	Category    string
	LicenseCode string
	Sort        string
	Limit       int
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
	ID            uuid.UUID  `json:"id"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	ProductType   string     `json:"productType"`
	Category      string     `json:"category"`
	PriceCents    int        `json:"priceCents"`
	Currency      string     `json:"currency"`
	Status        string     `json:"status"`
	AssetID       uuid.UUID  `json:"assetId"`
	MediaURL      string     `json:"mediaUrl"`
	MediaKind     string     `json:"mediaKind"`
	Width         *int       `json:"width,omitempty"`
	Height        *int       `json:"height,omitempty"`
	AIDisclosure  string     `json:"aiDisclosure"`
	IncludedFiles []string   `json:"includedFiles"`
	Compatibility string     `json:"compatibility"`
	Seller        Seller     `json:"seller"`
	License       License    `json:"license"`
	OwnedAssetID  *uuid.UUID `json:"ownedAssetId,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

type Purchase struct {
	OrderID       uuid.UUID `json:"orderId"`
	ProductID     uuid.UUID `json:"productId"`
	ProductTitle  string    `json:"productTitle"`
	EntitlementID uuid.UUID `json:"entitlementId"`
	AssetID       uuid.UUID `json:"assetId"`
	Status        string    `json:"status"`
	LicenseCode   string    `json:"licenseCode"`
	AmountCents   int       `json:"amountCents"`
	Currency      string    `json:"currency"`
	PaymentMode   string    `json:"paymentMode"`
	RealCharge    bool      `json:"realCharge"`
	AlreadyOwned  bool      `json:"alreadyOwned"`
	CreatedAt     time.Time `json:"createdAt"`
}

type OrderEvent struct {
	FromStatus *string   `json:"fromStatus,omitempty"`
	ToStatus   string    `json:"toStatus"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Order struct {
	ID                uuid.UUID    `json:"id"`
	ProductID         uuid.UUID    `json:"productId"`
	ProductTitle      string       `json:"productTitle"`
	AssetID           *uuid.UUID   `json:"assetId,omitempty"`
	AmountCents       int          `json:"amountCents"`
	Currency          string       `json:"currency"`
	Status            string       `json:"status"`
	LicenseCode       string       `json:"licenseCode"`
	LicenseName       string       `json:"licenseName"`
	LicenseVersion    string       `json:"licenseVersion"`
	LicenseTerms      string       `json:"licenseTerms"`
	RefundWindowDays  int          `json:"refundWindowDays"`
	PaymentMode       string       `json:"paymentMode"`
	RealCharge        bool         `json:"realCharge"`
	RefundRequestedAt *time.Time   `json:"refundRequestedAt,omitempty"`
	RefundedAt        *time.Time   `json:"refundedAt,omitempty"`
	CreatedAt         time.Time    `json:"createdAt"`
	Events            []OrderEvent `json:"events"`
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

func (s *Service) ListProducts(ctx context.Context, viewerID uuid.UUID, filter ListFilter) ([]Product, error) {
	filter.Query = strings.TrimSpace(strings.ToLower(filter.Query))
	filter.ProductType = strings.TrimSpace(strings.ToLower(filter.ProductType))
	filter.Category = strings.TrimSpace(strings.ToLower(filter.Category))
	filter.LicenseCode = strings.TrimSpace(filter.LicenseCode)
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 50
	}
	order := "p.created_at DESC,p.id DESC"
	switch filter.Sort {
	case "price_asc":
		order = "p.price_cents ASC,p.created_at DESC"
	case "price_desc":
		order = "p.price_cents DESC,p.created_at DESC"
	}
	rows, err := s.pool.Query(ctx, productSelect+fmt.Sprintf(`
		WHERE p.status='active'
		  AND ($2='' OR lower(p.title||' '||p.description||' '||u.display_name) LIKE '%%'||$2||'%%')
		  AND ($3='' OR p.product_type=$3)
		  AND ($4='' OR p.category=$4)
		  AND ($5='' OR p.license_code=$5)
		ORDER BY %s LIMIT $6`, order), viewerID, filter.Query, filter.ProductType, filter.Category, filter.LicenseCode, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	defer rows.Close()
	items := make([]Product, 0)
	for rows.Next() {
		item, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CategoryCounts(ctx context.Context, filter ListFilter) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.category,count(*) FROM products p JOIN users u ON u.id=p.seller_id AND u.status='active' JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean' JOIN licenses l ON l.code=p.license_code AND l.status='active'
		WHERE p.status='active' AND ($1='' OR lower(p.title||' '||p.description||' '||u.display_name) LIKE '%'||$1||'%')
		AND ($2='' OR p.product_type=$2) AND ($3='' OR p.license_code=$3) GROUP BY p.category`, strings.ToLower(strings.TrimSpace(filter.Query)), strings.ToLower(strings.TrimSpace(filter.ProductType)), strings.TrimSpace(filter.LicenseCode))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var category string
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			return nil, err
		}
		counts[category] = count
	}
	return counts, rows.Err()
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

func (s *Service) Purchase(ctx context.Context, buyerID, productID uuid.UUID, idempotencyKey, requestID string, licenseAccepted bool) (Purchase, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if productID == uuid.Nil || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || !licenseAccepted {
		return Purchase{}, false, ErrInvalidPurchase
	}
	if requestID == "" {
		requestID = "marketplace-purchase"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Purchase{}, false, fmt.Errorf("begin purchase: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if existing, found, err := purchaseByIdempotencyKey(ctx, tx, idempotencyKey); err != nil {
		return Purchase{}, false, err
	} else if found {
		if existing.ProductID != productID {
			return Purchase{}, false, ErrIdempotencyConflict
		}
		var actualBuyer uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT buyer_id FROM orders WHERE id=$1`, existing.OrderID).Scan(&actualBuyer); err != nil {
			return Purchase{}, false, fmt.Errorf("check idempotent purchase owner: %w", err)
		}
		if actualBuyer != buyerID {
			return Purchase{}, false, ErrIdempotencyConflict
		}
		existing.AlreadyOwned = true
		return existing, false, tx.Commit(ctx)
	}

	if existing, found, err := activePurchase(ctx, tx, buyerID, productID); err != nil {
		return Purchase{}, false, err
	} else if found {
		existing.AlreadyOwned = true
		return existing, false, tx.Commit(ctx)
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Checkout); err != nil {
		return Purchase{}, false, err
	}

	var sellerID, sourceAssetID, rootOriginID uuid.UUID
	var title, currency, licenseCode, licenseName, licenseVersion, licenseTerms, sourceTitle, mediaURL, mimeType, kind, scanStatus string
	var priceCents, refundWindowDays int
	var width, height *int
	err = tx.QueryRow(ctx, `
		SELECT p.seller_id,p.asset_id,COALESCE(a.origin_asset_id,a.id),p.title,p.price_cents,p.currency,p.license_code,
		       l.name,l.version,l.terms,l.refund_window_days,
		       a.title,a.media_url,a.mime_type,a.kind,a.width,a.height,a.scan_status
		FROM products p JOIN assets a ON a.id=p.asset_id JOIN licenses l ON l.code=p.license_code AND l.status='active'
		WHERE p.id=$1 AND p.status='active'
		FOR UPDATE OF p`, productID).Scan(
		&sellerID, &sourceAssetID, &rootOriginID, &title, &priceCents, &currency, &licenseCode, &licenseName, &licenseVersion, &licenseTerms, &refundWindowDays,
		&sourceTitle, &mediaURL, &mimeType, &kind, &width, &height, &scanStatus)
	if errors.Is(err, pgx.ErrNoRows) || scanStatus != "clean" {
		return Purchase{}, false, ErrNotFound
	}
	if err != nil {
		return Purchase{}, false, fmt.Errorf("load purchase product: %w", err)
	}
	if sellerID == buyerID {
		return Purchase{}, false, ErrSellerPurchase
	}

	orderID := uuid.New()
	assetID := uuid.New()
	entitlementID := uuid.New()
	createdAt := time.Now()
	_, err = tx.Exec(ctx, `
		INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,
		                   product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,
		                   refund_window_days_snapshot,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,'test_pending',now(),$6,$7,$8,$9,$10,$11,$12,$12)`,
		orderID, buyerID, productID, priceCents, currency, idempotencyKey, title, licenseName, licenseVersion, licenseTerms, refundWindowDays, createdAt)
	if err != nil {
		return Purchase{}, false, fmt.Errorf("insert test order: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events(order_id,actor_id,from_status,to_status,reason,created_at,sequence)
		VALUES
		  ($1,$2,NULL,'test_pending','License accepted; test checkout initialized.',$3,1),
		  ($1,$2,'test_pending','test_paid','Local test payment authorized; no real charge occurred.',$3,2),
		  ($1,$2,'test_paid','fulfilled','Entitlement and purchased asset granted.',$3,3)`, orderID, buyerID, createdAt); err != nil {
		return Purchase{}, false, fmt.Errorf("insert order events: %w", err)
	}
	if strings.HasPrefix(mediaURL, "/api/v1/assets/") {
		mediaURL = "/api/v1/assets/" + assetID.String() + "/content"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,source_id,license_code,origin_asset_id,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'clean','purchase',$9,$10,$11,$12)`,
		assetID, buyerID, kind, sourceTitle, mediaURL, mimeType, width, height, orderID, licenseCode, rootOriginID, createdAt)
	if err != nil {
		return Purchase{}, false, fmt.Errorf("grant purchased asset: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO entitlements(id,user_id,product_id,order_id,asset_id,license_code,status,granted_at)
		VALUES($1,$2,$3,$4,$5,$6,'active',$7)`, entitlementID, buyerID, productID, orderID, assetID, licenseCode, createdAt)
	if err != nil {
		return Purchase{}, false, fmt.Errorf("grant entitlement: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='fulfilled',updated_at=$2 WHERE id=$1`, orderID, createdAt); err != nil {
		return Purchase{}, false, fmt.Errorf("fulfill test order: %w", err)
	}
	if priceCents > 0 {
		if err := billing.TransferTx(ctx, tx, buyerID, sellerID, orderID, priceCents, currency,
			"product_purchase", "product_sale", "Local Test product purchase: "+title); err != nil {
			return Purchase{}, false, fmt.Errorf("apply product billing transfer: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO ledger_entries(account_id,operation_id,direction,amount_cents,currency,reason,created_at)
			VALUES
			  ($1,$2,'debit',$3,$4,'test_purchase_debit_no_real_charge',$5),
			  ($6,$2,'credit',$3,$4,'test_purchase_credit_no_real_payout',$5)`,
			buyerID, orderID, priceCents, currency, createdAt, sellerID)
		if err != nil {
			return Purchase{}, false, fmt.Errorf("record balanced test ledger: %w", err)
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'marketplace.test_purchase','order',$2,$3,jsonb_build_object('productId',$4::text,'realCharge',false,'paymentMode','test'))`,
		buyerID, orderID, requestID, productID)
	if err != nil {
		return Purchase{}, false, fmt.Errorf("audit test purchase: %w", err)
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: buyerID, Kind: "marketplace.order_fulfilled", Title: "Purchase ready",
		Body:       "\u201c" + title + "\u201d is now available in Assets under the accepted license.",
		TargetPath: "/workspace/assets/" + assetID.String(), ResourceType: "order", ResourceID: &orderID,
		SourceKey: "marketplace:order:" + orderID.String() + ":fulfilled",
	}); err != nil {
		return Purchase{}, false, fmt.Errorf("notify fulfilled purchase: %w", err)
	}
	if err := webhooks.EnqueueTx(ctx, tx, webhooks.EventInput{OwnerID: buyerID, EventType: "marketplace.order.fulfilled", ResourceType: "order", ResourceID: &orderID, SourceKey: "marketplace:order:" + orderID.String() + ":fulfilled"}); err != nil {
		return Purchase{}, false, fmt.Errorf("enqueue fulfilled-order webhook: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Purchase{}, false, fmt.Errorf("commit test purchase: %w", err)
	}
	return Purchase{
		OrderID: orderID, ProductID: productID, ProductTitle: title, EntitlementID: entitlementID, AssetID: assetID,
		Status: "fulfilled", LicenseCode: licenseCode, AmountCents: priceCents, Currency: currency,
		PaymentMode: "test", RealCharge: false, AlreadyOwned: false, CreatedAt: createdAt,
	}, true, nil
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

func (s *Service) RequestRefund(ctx context.Context, buyerID, orderID uuid.UUID, idempotencyKey, requestID, reason string) (Order, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	reason = strings.TrimSpace(reason)
	if orderID == uuid.Nil || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || len(reason) < 10 || len(reason) > 500 {
		return Order{}, ErrInvalidRefund
	}
	if requestID == "" {
		requestID = "marketplace-refund"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Order{}, fmt.Errorf("begin refund: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var createdAt time.Time
	var refundWindowDays int
	var sellerID, assetID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT o.status,o.created_at,o.refund_window_days_snapshot,p.seller_id,e.asset_id
		FROM orders o
		JOIN products p ON p.id=o.product_id
		JOIN entitlements e ON e.order_id=o.id
		WHERE o.id=$1 AND o.buyer_id=$2 FOR UPDATE OF o,e`, orderID, buyerID).Scan(
		&status, &createdAt, &refundWindowDays, &sellerID, &assetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("load refund order: %w", err)
	}
	if status == "test_refunded" {
		if err := tx.Commit(ctx); err != nil {
			return Order{}, fmt.Errorf("commit refund replay: %w", err)
		}
		return s.GetOrder(ctx, buyerID, orderID)
	}
	if status != "fulfilled" {
		return Order{}, ErrRefundConflict
	}
	if refundWindowDays <= 0 || time.Now().After(createdAt.Add(time.Duration(refundWindowDays)*24*time.Hour)) {
		return Order{}, ErrRefundWindowExpired
	}
	refundOperationID := uuid.New()
	refundedAt := time.Now()
	result, err := tx.Exec(ctx, `
		UPDATE orders SET status='test_refunded',refund_reason=$3,refund_idempotency_key=$4,refund_operation_id=$5,
		                  refund_requested_at=$6,refunded_at=$6,updated_at=$6
		WHERE id=$1 AND buyer_id=$2 AND status='fulfilled'`, orderID, buyerID, reason, idempotencyKey, refundOperationID, refundedAt)
	if err != nil {
		return Order{}, fmt.Errorf("refund order: %w", err)
	}
	if result.RowsAffected() != 1 {
		return Order{}, ErrRefundConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=$2 WHERE order_id=$1 AND status='active'`, orderID, refundedAt); err != nil {
		return Order{}, fmt.Errorf("revoke entitlement: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events(order_id,actor_id,from_status,to_status,reason,created_at,sequence)
		VALUES ($1,$2,'fulfilled','refund_requested',$3,$4,4),
		       ($1,$2,'refund_requested','test_refunded','Local test refund completed; no real funds moved.',$4,5)`,
		orderID, buyerID, reason, refundedAt); err != nil {
		return Order{}, fmt.Errorf("record refund events: %w", err)
	}
	var amountCents int
	var currency, productTitle string
	if err := tx.QueryRow(ctx, `SELECT amount_cents,currency,product_title_snapshot FROM orders WHERE id=$1`, orderID).Scan(&amountCents, &currency, &productTitle); err != nil {
		return Order{}, fmt.Errorf("load refund amount: %w", err)
	}
	if amountCents > 0 {
		if err := billing.TransferTx(ctx, tx, sellerID, buyerID, refundOperationID, amountCents, currency,
			"product_refund", "product_refund", "Local Test product refund: "+productTitle); err != nil {
			return Order{}, fmt.Errorf("apply product refund transfer: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries(account_id,operation_id,direction,amount_cents,currency,reason,created_at)
			VALUES ($1,$3,'debit',$4,$5,'test_refund_debit_no_real_payout',$6),
			       ($2,$3,'credit',$4,$5,'test_refund_credit_no_real_charge',$6)`,
			sellerID, buyerID, refundOperationID, amountCents, currency, refundedAt); err != nil {
			return Order{}, fmt.Errorf("record balanced test refund: %w", err)
		}
	}
	if _, err := risk.RecordTx(ctx, tx, risk.SignalInput{
		SourceKey: "order_refund:" + orderID.String(), ResourceType: "order", ResourceID: orderID,
		SubjectUserID: buyerID, ActorUserID: &buyerID, SignalType: "transaction_refund", Severity: "medium", Score: 55,
		Summary:  "Local Test refund requires transaction review.",
		Evidence: map[string]any{"orderStatus": "test_refunded", "amountCents": amountCents, "currency": currency, "paymentMode": "local_test"},
	}); err != nil {
		return Order{}, fmt.Errorf("record refund risk signal: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'marketplace.test_refund','order',$2,$3,jsonb_build_object('assetId',$4::text,'realCharge',false,'paymentMode','test'))`,
		buyerID, orderID, requestID, assetID); err != nil {
		return Order{}, fmt.Errorf("audit test refund: %w", err)
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: buyerID, Kind: "marketplace.order_refunded", Title: "Local Test refund completed",
		Body:       "\u201c" + productTitle + "\u201d was refunded in Local Test USD. Its access and reuse rights were revoked.",
		TargetPath: "/workspace/orders", ResourceType: "order", ResourceID: &orderID,
		SourceKey: "marketplace:order:" + orderID.String() + ":refunded",
	}); err != nil {
		return Order{}, fmt.Errorf("notify refunded purchase: %w", err)
	}
	if err := webhooks.EnqueueTx(ctx, tx, webhooks.EventInput{OwnerID: buyerID, EventType: "marketplace.order.refunded", ResourceType: "order", ResourceID: &orderID, SourceKey: "marketplace:order:" + orderID.String() + ":refunded"}); err != nil {
		return Order{}, fmt.Errorf("enqueue refunded-order webhook: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, fmt.Errorf("commit refund: %w", err)
	}
	return s.GetOrder(ctx, buyerID, orderID)
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
	       a.media_url,a.kind,a.width,a.height,p.ai_disclosure,p.included_files,p.compatibility,
	       u.id,u.handle,u.display_name,
	       l.code,l.name,l.summary,l.terms,l.version,l.allows_commercial,l.allows_derivatives,l.allows_redistribution,
	       l.attribution_required,l.refund_window_days,e.asset_id,p.created_at
	FROM products p
	JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean'
	JOIN users u ON u.id=p.seller_id AND u.status='active'
	JOIN licenses l ON l.code=p.license_code AND l.status='active'
	LEFT JOIN entitlements e ON e.product_id=p.id AND e.user_id=$1 AND e.status='active'`

const orderSelect = `
	SELECT o.id,p.id,o.product_title_snapshot,e.asset_id,o.amount_cents,o.currency,o.status,p.license_code,o.license_name_snapshot,
	       o.license_version,o.license_terms_snapshot,o.refund_window_days_snapshot,o.refund_requested_at,o.refunded_at,o.created_at,
	       CASE WHEN pi.provider IS NULL THEN 'test' ELSE pi.provider END,COALESCE(pi.live_mode,false)
	FROM orders o
	JOIN products p ON p.id=o.product_id
	LEFT JOIN entitlements e ON e.order_id=o.id
	LEFT JOIN payment_intents pi ON pi.order_id=o.id`

func scanOrder(row scanner, item *Order) error {
	err := row.Scan(&item.ID, &item.ProductID, &item.ProductTitle, &item.AssetID, &item.AmountCents,
		&item.Currency, &item.Status, &item.LicenseCode, &item.LicenseName, &item.LicenseVersion,
		&item.LicenseTerms, &item.RefundWindowDays, &item.RefundRequestedAt, &item.RefundedAt, &item.CreatedAt,
		&item.PaymentMode, &item.RealCharge)
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
		&item.License.AttributionRequired, &item.License.RefundWindowDays, &item.OwnedAssetID, &item.CreatedAt)
	return item, err
}

func purchaseByIdempotencyKey(ctx context.Context, tx pgx.Tx, key string) (Purchase, bool, error) {
	var item Purchase
	err := tx.QueryRow(ctx, `
		SELECT o.id,o.product_id,o.product_title_snapshot,e.id,e.asset_id,o.status,e.license_code,o.amount_cents,o.currency,o.created_at
		FROM orders o JOIN products p ON p.id=o.product_id JOIN entitlements e ON e.order_id=o.id
		WHERE o.idempotency_key=$1`, key).Scan(
		&item.OrderID, &item.ProductID, &item.ProductTitle, &item.EntitlementID, &item.AssetID, &item.Status,
		&item.LicenseCode, &item.AmountCents, &item.Currency, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Purchase{}, false, nil
	}
	if err != nil {
		return Purchase{}, false, fmt.Errorf("load idempotent purchase: %w", err)
	}
	item.PaymentMode = "test"
	item.RealCharge = false
	return item, true, nil
}

func activePurchase(ctx context.Context, tx pgx.Tx, buyerID, productID uuid.UUID) (Purchase, bool, error) {
	var item Purchase
	err := tx.QueryRow(ctx, `
		SELECT o.id,p.id,o.product_title_snapshot,e.id,e.asset_id,o.status,e.license_code,o.amount_cents,o.currency,o.created_at
		FROM entitlements e JOIN orders o ON o.id=e.order_id JOIN products p ON p.id=e.product_id
		WHERE e.user_id=$1 AND e.product_id=$2 AND e.status='active'`, buyerID, productID).Scan(
		&item.OrderID, &item.ProductID, &item.ProductTitle, &item.EntitlementID, &item.AssetID, &item.Status,
		&item.LicenseCode, &item.AmountCents, &item.Currency, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Purchase{}, false, nil
	}
	if err != nil {
		return Purchase{}, false, fmt.Errorf("load active entitlement: %w", err)
	}
	item.PaymentMode = "test"
	item.RealCharge = false
	return item, true, nil
}
