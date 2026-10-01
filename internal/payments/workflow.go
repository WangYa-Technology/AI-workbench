package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/productpolicy"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	PaymentEventJobKind      = "payment.process_event"
	TaskTransferJobKind      = "payment.transfer_task"
	TaskRefundJobKind        = "payment.refund_task"
	ProductRefundJobKind     = "payment.refund_product"
	ProductSettlementJobKind = "payment.settle_product"
)

var (
	ErrInvalidCheckout        = errors.New("invalid payment checkout")
	ErrCheckoutConflict       = errors.New("payment checkout conflict")
	ErrCheckoutBusy           = errors.New("payment checkout state changed concurrently")
	ErrAlreadyOwned           = errors.New("product already owned")
	ErrInvalidRefund          = errors.New("invalid payment refund")
	ErrRefundConflict         = errors.New("payment refund conflict")
	ErrRefundExpired          = errors.New("payment refund window expired")
	ErrProviderConfigMismatch = errors.New("payment provider configuration does not match deployment")
)

type Checkout struct {
	PaymentID      uuid.UUID `json:"paymentId"`
	OrderID        uuid.UUID `json:"orderId"`
	ResourceID     uuid.UUID `json:"resourceId"`
	Purpose        string    `json:"purpose"`
	Status         string    `json:"status"`
	CheckoutURL    string    `json:"checkoutUrl"`
	ExpiresAt      time.Time `json:"expiresAt"`
	AmountCents    int       `json:"amountCents"`
	Currency       string    `json:"currency"`
	PaymentMode    string    `json:"paymentMode"`
	RealCharge     bool      `json:"realCharge"`
	LiveMode       bool      `json:"liveMode"`
	AlreadyCreated bool      `json:"alreadyCreated"`
}

type TaskCheckout struct {
	PaymentID      uuid.UUID  `json:"paymentId"`
	TaskID         uuid.UUID  `json:"taskId"`
	ProposalID     *uuid.UUID `json:"proposalId,omitempty"`
	Purpose        string     `json:"purpose"`
	Status         string     `json:"status"`
	CheckoutURL    string     `json:"checkoutUrl"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	AmountCents    int        `json:"amountCents"`
	Currency       string     `json:"currency"`
	PaymentMode    string     `json:"paymentMode"`
	RealCharge     bool       `json:"realCharge"`
	LiveMode       bool       `json:"liveMode"`
	AlreadyCreated bool       `json:"alreadyCreated"`
}

// BillingCheckout is the hosted checkout projection used by wallet top-ups
// and externally paid subscription purchases. ResourceID is the user ID for
// top-ups and the subscription plan ID for subscription purchases.
type BillingCheckout struct {
	PaymentID      uuid.UUID `json:"paymentId"`
	ResourceID     uuid.UUID `json:"resourceId"`
	Purpose        string    `json:"purpose"`
	Status         string    `json:"status"`
	CheckoutURL    string    `json:"checkoutUrl"`
	ExpiresAt      time.Time `json:"expiresAt"`
	AmountCents    int       `json:"amountCents"`
	Currency       string    `json:"currency"`
	PaymentMode    string    `json:"paymentMode"`
	RealCharge     bool      `json:"realCharge"`
	LiveMode       bool      `json:"liveMode"`
	AlreadyCreated bool      `json:"alreadyCreated"`
}

type paymentEventJobPayload struct {
	EventID uuid.UUID `json:"eventId"`
}

type taskTransferJobPayload struct {
	PaymentID uuid.UUID `json:"paymentId"`
}

type taskRefundJobPayload struct {
	PaymentID uuid.UUID `json:"paymentId"`
}

type productRefundJobPayload struct {
	PaymentID   uuid.UUID `json:"paymentId"`
	OperationID uuid.UUID `json:"operationId"`
}

func (s *Service) BeginTaskCheckout(ctx context.Context, clientID, taskID uuid.UUID, proposalID *uuid.UUID, idempotencyKey, requestID, successURL, cancelURL string) (TaskCheckout, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || s.pool == nil || !s.config.Enabled {
		return TaskCheckout{}, false, ErrDisabled
	}
	if clientID == uuid.Nil || taskID == uuid.Nil || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || !validReturnURL(successURL) || !validReturnURL(cancelURL) || (proposalID != nil && *proposalID == uuid.Nil) {
		return TaskCheckout{}, false, ErrInvalidCheckout
	}
	// Reject non-owners before resolving provider configuration. Provider
	// failures must not disclose capability or configuration details to users
	// who cannot fund the task in the first place. The transaction below keeps
	// the authoritative row lock and repeats this check to close the TOCTOU gap.
	var preflightOwnerID uuid.UUID
	var preflightStatus string
	var preflightDeadline time.Time
	if err := s.pool.QueryRow(ctx, `SELECT client_id,status,deadline FROM demands WHERE id=$1`, taskID).Scan(&preflightOwnerID, &preflightStatus, &preflightDeadline); errors.Is(err, pgx.ErrNoRows) {
		return TaskCheckout{}, false, ErrInvalidCheckout
	} else if err != nil {
		return TaskCheckout{}, false, err
	}
	if preflightOwnerID != clientID || preflightStatus != "open" || !preflightDeadline.After(time.Now()) {
		return TaskCheckout{}, false, ErrInvalidCheckout
	}
	providerConfig, err := s.resolveProductProvider(ctx)
	if err != nil {
		return TaskCheckout{}, false, err
	}
	if !s.providerConfigReadyForPurpose(providerConfig, "task") {
		return TaskCheckout{}, false, ErrProviderConfigMismatch
	}
	runtime, err := s.runtimes.Runtime(providerConfig.Provider)
	if err != nil {
		return TaskCheckout{}, false, err
	}
	identity, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return TaskCheckout{}, false, err
	}
	liveMode := s.productLiveModeFor(providerConfig)
	if !taskProviderIdentityMatchesConfig(providerConfig, identity, liveMode) {
		return TaskCheckout{}, false, ErrProviderConfigMismatch
	}
	capabilities := runtimeCapabilities(runtime)
	if !capabilities.Checkout || !capabilities.Refund || !capabilities.Transfer || !capabilities.ConnectedAccounts {
		return TaskCheckout{}, false, newProviderFailure("payment_provider_unsupported", 0)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return TaskCheckout{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	checkout, found, err := loadTaskCheckoutByKey(ctx, tx, clientID, idempotencyKey)
	if err != nil {
		return TaskCheckout{}, false, err
	}
	if found {
		if checkout.PaymentMode != providerConfig.Provider {
			return TaskCheckout{}, false, ErrCheckoutConflict
		}
		if checkout.TaskID != taskID || !sameOptionalUUID(checkout.ProposalID, proposalID) {
			return TaskCheckout{}, false, ErrCheckoutConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return TaskCheckout{}, false, err
		}
		if checkout.CheckoutURL != "" {
			checkout.AlreadyCreated = true
			return checkout, false, nil
		}
		return s.createTaskProviderCheckout(ctx, runtime, checkout, successURL, cancelURL)
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Checkout); err != nil {
		return TaskCheckout{}, false, err
	}
	var deadline time.Time
	var ownerID uuid.UUID
	var title, status, currency string
	var budget int
	var direct bool
	if err := tx.QueryRow(ctx, `SELECT client_id,title,status,budget_cents,currency,allow_direct_accept,deadline FROM demands WHERE id=$1 FOR UPDATE`, taskID).Scan(&ownerID, &title, &status, &budget, &currency, &direct, &deadline); errors.Is(err, pgx.ErrNoRows) {
		return TaskCheckout{}, false, ErrInvalidCheckout
	} else if err != nil {
		return TaskCheckout{}, false, err
	}
	if !deadline.After(time.Now()) || ownerID != clientID || status != "open" || currency != "USD" {
		return TaskCheckout{}, false, ErrInvalidCheckout
	}
	amountCents := budget
	var payeeID *uuid.UUID
	if proposalID != nil {
		var creatorID uuid.UUID
		var proposalStatus string
		if err := tx.QueryRow(ctx, `SELECT creator_id,amount_cents,status FROM proposals WHERE id=$1 AND demand_id=$2 FOR UPDATE`, *proposalID, taskID).Scan(&creatorID, &amountCents, &proposalStatus); errors.Is(err, pgx.ErrNoRows) {
			return TaskCheckout{}, false, ErrInvalidCheckout
		} else if err != nil {
			return TaskCheckout{}, false, err
		}
		if proposalStatus != "submitted" {
			return TaskCheckout{}, false, ErrCheckoutConflict
		}
		payeeID = &creatorID
	} else if !direct {
		return TaskCheckout{}, false, ErrInvalidCheckout
	}
	if amountCents < 50 || amountCents > 99999999 {
		return TaskCheckout{}, false, ErrInvalidCheckout
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_intents WHERE purpose='task' AND resource_id=$1 AND status IN ('checkout_pending','checkout_open','paid','transfer_pending','transferred','refund_pending'))`, taskID).Scan(&active); err != nil {
		return TaskCheckout{}, false, err
	}
	if active {
		return TaskCheckout{}, false, ErrCheckoutConflict
	}
	paymentID, createdAt := uuid.New(), time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,proposal_id,amount_cents,currency,status,live_mode,idempotency_key,
		  task_original_merchant_id,task_original_store_id,task_original_live_mode,task_original_endpoint,task_original_api_version,task_original_request_version,created_at,updated_at)
		VALUES($1,$2,'task',$3,$4,$5,$6,$7,$8,'checkout_pending',$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)`,
		paymentID, providerConfig.Provider, clientID, payeeID, taskID, proposalID, amountCents, currency, liveMode, idempotencyKey,
		identity.MerchantID, identity.StoreID, identity.LiveMode, identity.Endpoint, identity.APIVersion, identity.RequestVersion, createdAt); err != nil {
		return TaskCheckout{}, false, ErrCheckoutConflict
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
		VALUES($1,'checkout.requested',NULL,'checkout_pending',jsonb_build_object('taskId',$2::text,'requestId',$3::text,'proposalId',$4::text))`, paymentID, taskID, requestID, proposalID); err != nil {
		return TaskCheckout{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events(demand_id,actor_id,kind,from_status,to_status,note,metadata)
		VALUES($1,$2,'funding_requested','open','open','Secure Provider funding requested.',jsonb_build_object('paymentId',$3::text,'amountCents',$4::integer,'currency',$5::text))`, taskID, clientID, paymentID, amountCents, currency); err != nil {
		return TaskCheckout{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TaskCheckout{}, false, err
	}
	checkout = TaskCheckout{PaymentID: paymentID, TaskID: taskID, ProposalID: proposalID, Purpose: "task", Status: "checkout_pending", AmountCents: amountCents, Currency: currency, PaymentMode: providerConfig.Provider, RealCharge: liveMode, LiveMode: liveMode}
	_ = title
	return s.createTaskProviderCheckout(ctx, runtime, checkout, successURL, cancelURL)
}

func (s *Service) createTaskProviderCheckout(ctx context.Context, runtime ProviderRuntime, checkout TaskCheckout, successURL, cancelURL string) (TaskCheckout, bool, error) {
	var buyerEmail string
	_ = s.pool.QueryRow(ctx, `SELECT email FROM users WHERE id=(SELECT payer_id FROM payment_intents WHERE id=$1)`, checkout.PaymentID).Scan(&buyerEmail)
	session, err := runtime.CreateCheckout(ctx, CheckoutRequest{
		PaymentID: checkout.PaymentID, ResourceID: checkout.TaskID, Purpose: "task", Name: "HCAI CHAT task funding " + checkout.TaskID.String(),
		AmountCents: checkout.AmountCents, Currency: checkout.Currency, SuccessURL: successURL, CancelURL: cancelURL,
		BuyerIdentity: checkout.PaymentID.String(), BuyerEmail: buyerEmail,
	})
	if err != nil {
		return TaskCheckout{}, false, SanitizeProviderError(err)
	}
	result, err := s.pool.Exec(ctx, `
		UPDATE payment_intents SET status='checkout_open',provider_checkout_id=$2,checkout_url=$3,checkout_expires_at=$4,updated_at=now(),version=version+1
		WHERE id=$1 AND purpose='task' AND status IN ('checkout_pending','checkout_open') AND (provider_checkout_id IS NULL OR provider_checkout_id=$2)`,
		checkout.PaymentID, session.ProviderID, session.CheckoutURL, session.ExpiresAt)
	if err != nil {
		return TaskCheckout{}, false, err
	}
	if result.RowsAffected() != 1 {
		return TaskCheckout{}, false, ErrCheckoutConflict
	}
	checkout.Status, checkout.CheckoutURL, checkout.ExpiresAt, checkout.LiveMode = "checkout_open", session.CheckoutURL, session.ExpiresAt, session.LiveMode
	checkout.RealCharge = session.LiveMode
	return checkout, true, nil
}

func loadTaskCheckoutByKey(ctx context.Context, tx pgx.Tx, clientID uuid.UUID, idempotencyKey string) (TaskCheckout, bool, error) {
	var item TaskCheckout
	var checkoutURL *string
	var expiresAt *time.Time
	var provider string
	err := tx.QueryRow(ctx, `
		SELECT id,resource_id,proposal_id,purpose,status,checkout_url,checkout_expires_at,amount_cents,currency,live_mode,provider
		FROM payment_intents WHERE payer_id=$1 AND purpose='task' AND idempotency_key=$2`, clientID, idempotencyKey).Scan(
		&item.PaymentID, &item.TaskID, &item.ProposalID, &item.Purpose, &item.Status, &checkoutURL, &expiresAt, &item.AmountCents, &item.Currency, &item.LiveMode, &provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskCheckout{}, false, nil
	}
	if err != nil {
		return TaskCheckout{}, false, err
	}
	if checkoutURL != nil {
		item.CheckoutURL = *checkoutURL
	}
	if expiresAt != nil {
		item.ExpiresAt = *expiresAt
	}
	item.PaymentMode, item.RealCharge = provider, item.LiveMode
	return item, true, nil
}

func sameOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func providerEventRequestID(provider string, eventID uuid.UUID) string {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider == "" {
		provider = "payment"
	}
	return provider + "-event:" + eventID.String()
}

func (s *Service) productProvider() string {
	provider := strings.ToLower(strings.TrimSpace(s.config.Provider))
	if provider == "" {
		return "stripe"
	}
	return provider
}

func (s *Service) productLiveMode() bool {
	if s.productProvider() == "waffo_pancake" {
		return strings.EqualFold(strings.TrimSpace(s.config.WaffoEnvironment), "prod")
	}
	return s.config.LiveMode
}

func (s *Service) productLiveModeFor(provider persistedProviderConfig) bool {
	if provider.Provider == "waffo_pancake" {
		return strings.EqualFold(strings.TrimSpace(provider.Environment), "prod")
	}
	return s.config.LiveMode
}

func (s *Service) BeginProductCheckout(ctx context.Context, buyerID, productID uuid.UUID, idempotencyKey, requestID, successURL, cancelURL string, licenseAccepted bool, offerVersion string) (Checkout, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || s.pool == nil || !s.config.Enabled {
		return Checkout{}, false, ErrDisabled
	}
	if buyerID == uuid.Nil || productID == uuid.Nil || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || !licenseAccepted || !validOfferVersion(offerVersion) || !validReturnURL(successURL) || !validReturnURL(cancelURL) {
		return Checkout{}, false, ErrInvalidCheckout
	}
	providerConfig, err := s.resolveProductProvider(ctx)
	if err != nil {
		return Checkout{}, false, err
	}
	runtime, err := s.runtimes.Runtime(providerConfig.Provider)
	if err != nil {
		return Checkout{}, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Checkout{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Bind each command key before serializing a buyer/product. Different products
	// cannot race to claim the same key, including keys that reuse a live intent.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "product-command:"+buyerID.String()+":"+idempotencyKey); err != nil {
		return Checkout{}, false, err
	}
	// Serialize commands for a buyer/product before reading either keys or active
	// intents. READ COMMITTED sees the preceding command after the lock is acquired.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "product-checkout:"+buyerID.String()+":"+productID.String()); err != nil {
		return Checkout{}, false, err
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Checkout); err != nil {
		return Checkout{}, false, err
	}
	var checkoutReview bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_intents p WHERE p.payer_id=$1 AND p.resource_id=$2 AND p.purpose='product'
 AND (EXISTS(SELECT 1 FROM product_webhook_quarantine_review q WHERE q.payment_id=p.id)
 OR EXISTS(SELECT 1 FROM product_checkout_lookup_review r WHERE r.payment_id=p.id)))`, buyerID, productID).Scan(&checkoutReview); err != nil {
		return Checkout{}, false, err
	}
	if checkoutReview {
		return Checkout{}, false, ErrCheckoutReconciliation
	}
	checkout, found, err := loadProductCheckoutByKey(ctx, tx, buyerID, idempotencyKey)
	if err != nil {
		return Checkout{}, false, err
	}
	if found {
		if checkout.ResourceID == productID && checkout.Status == "cancelled" {
			var closed bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_checkout_closures WHERE payment_id=$1)`, checkout.PaymentID).Scan(&closed); err != nil {
				return Checkout{}, false, err
			}
			if closed {
				return Checkout{}, false, ErrCheckoutClosed
			}
		}
		if checkout.ResourceID != productID || checkout.PaymentMode != providerConfig.Provider || checkout.LiveMode != s.productLiveModeFor(providerConfig) {
			return Checkout{}, false, ErrCheckoutConflict
		}
		if checkout.Status == "cancelled" {
			var verified bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.expired_verified')`, checkout.PaymentID).Scan(&verified); err != nil {
				return Checkout{}, false, err
			}
			if verified {
				return Checkout{}, false, ErrCheckoutExpired
			}
		}
		if err := validateProductContractVersion(ctx, tx, checkout.OrderID, offerVersion); err != nil {
			return Checkout{}, false, err
		}
		if err := validProductCheckoutReplay(checkout); err != nil {
			return Checkout{}, false, err
		}
		if err := validateProductCheckoutReconciliation(ctx, tx, checkout.PaymentID); err != nil {
			return Checkout{}, false, err
		}
		if err := validateProductCheckoutEligibility(ctx, tx, buyerID, productID); err != nil {
			return Checkout{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Checkout{}, false, err
		}
		if checkout.CheckoutURL != "" {
			if err := productdelivery.Ensure(ctx, s.pool, s.config.MediaStores, checkout.OrderID); err != nil {
				return Checkout{}, false, preparationCheckoutError(err)
			}
			checkout.AlreadyCreated = true
			return checkout, false, nil
		}
		return s.createProviderCheckout(ctx, runtime, checkout)
	}
	checkout, found, err = loadActiveProductCheckout(ctx, tx, buyerID, productID)
	if err != nil {
		return Checkout{}, false, err
	}
	if found {
		if err := validateProductCheckoutEligibility(ctx, tx, buyerID, productID); err != nil {
			return Checkout{}, false, err
		}
		if checkout.PaymentMode != providerConfig.Provider || checkout.LiveMode != s.productLiveModeFor(providerConfig) {
			return Checkout{}, false, ErrCheckoutConflict
		}
		if err := validateProductContractVersion(ctx, tx, checkout.OrderID, offerVersion); err != nil {
			return Checkout{}, false, err
		}
		if err := validProductCheckoutReplay(checkout); err != nil {
			return Checkout{}, false, err
		}
		if err := validateProductCheckoutReconciliation(ctx, tx, checkout.PaymentID); err != nil {
			return Checkout{}, false, err
		}
		if err := bindProductCheckoutCommand(ctx, tx, buyerID, idempotencyKey, checkout.PaymentID); err != nil {
			return Checkout{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Checkout{}, false, err
		}
		if checkout.CheckoutURL != "" {
			if err := productdelivery.Ensure(ctx, s.pool, s.config.MediaStores, checkout.OrderID); err != nil {
				return Checkout{}, false, preparationCheckoutError(err)
			}
			checkout.AlreadyCreated = true
			return checkout, false, nil
		}
		return s.createProviderCheckout(ctx, runtime, checkout)
	}
	if err := lockProductCheckoutSources(ctx, tx, buyerID, productID); err != nil {
		return Checkout{}, false, err
	}
	var sellerID uuid.UUID
	var title, currency, licenseName, licenseVersion, licenseTerms, scanStatus string
	var amountCents, refundWindowDays int
	var currentOfferVersion string
	err = tx.QueryRow(ctx, `
		SELECT p.seller_id,p.title,p.price_cents,p.currency,l.name,l.version,l.terms,l.refund_window_days,a.scan_status,offer.offer_version
		FROM products p JOIN licenses l ON l.code=p.license_code AND l.status='active'
		JOIN product_offers offer ON offer.product_id=p.id
		JOIN users seller ON seller.id=p.seller_id AND seller.status='active'
		JOIN assets a ON a.id=p.asset_id
		LEFT JOIN assets origin ON origin.id=a.origin_asset_id
		JOIN assets root ON root.id=COALESCE(a.origin_asset_id,a.id)
		WHERE p.id=$1 AND p.status='active' AND (a.origin_asset_id IS NULL OR origin.scan_status='clean')
        AND EXISTS(SELECT 1 FROM public_products visible WHERE visible.id=p.id)
		AND EXISTS(SELECT 1 FROM users buyer WHERE buyer.id=$2 AND buyer.status='active')`, productID, buyerID).Scan(
		&sellerID, &title, &amountCents, &currency, &licenseName, &licenseVersion, &licenseTerms, &refundWindowDays, &scanStatus, &currentOfferVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Checkout{}, false, ErrInvalidCheckout
	}
	if err != nil {
		return Checkout{}, false, err
	}
	if scanStatus != "clean" || sellerID == buyerID || amountCents < 50 || amountCents > 99999999 || currency != "USD" {
		return Checkout{}, false, ErrInvalidCheckout
	}
	var alreadyOwned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM entitlements WHERE user_id=$1 AND product_id=$2 AND status='active')`, buyerID, productID).Scan(&alreadyOwned); err != nil {
		return Checkout{}, false, err
	}
	if alreadyOwned {
		return Checkout{}, false, ErrAlreadyOwned
	}
	if currentOfferVersion != offerVersion {
		return Checkout{}, false, ErrOfferChanged
	}
	paymentID, orderID := uuid.New(), uuid.New()
	createdAt := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,
		  product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot,created_at,updated_at,delivery_snapshot_required)
		VALUES($1,$2,$3,$4,$5,'payment_pending',$6,$7,$8,$9,$10,$11,$12,$6,$6,true)`,
		orderID, buyerID, productID, amountCents, currency, createdAt, idempotencyKey, title, licenseName, licenseVersion, licenseTerms, refundWindowDays); err != nil {
		return Checkout{}, false, err
	}
	result, err := tx.Exec(ctx, `INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract)
		SELECT $1,source_asset_id,root_asset_id,offer_version,contract FROM product_offers WHERE product_id=$2 AND offer_version=$3`, orderID, productID, offerVersion)
	if err != nil {
		return Checkout{}, false, err
	}
	if result.RowsAffected() != 1 {
		return Checkout{}, false, ErrOfferChanged
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events(order_id,actor_id,from_status,to_status,reason,created_at,sequence)
		VALUES($1,$2,NULL,'payment_pending','License accepted; signed Provider checkout required.',$3,1)`, orderID, buyerID, createdAt); err != nil {
		return Checkout{}, false, err
	}
	if err := productdelivery.ReserveTx(ctx, tx, s.config.MediaStores, orderID); err != nil {
		return Checkout{}, false, deliveryCheckoutError(err)
	}
	productLiveMode := s.productLiveModeFor(providerConfig)
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,created_at,updated_at,product_success_url,product_cancel_url)
		VALUES($1,$2,'product',$3,$4,$5,$6,$7,$8,'checkout_pending',$9,$10,$11,$11,$12,$13)`,
		paymentID, providerConfig.Provider, buyerID, sellerID, productID, orderID, amountCents, currency, productLiveMode, idempotencyKey, createdAt,
		productCheckoutReturnURL(successURL, orderID, paymentID), productCheckoutReturnURL(cancelURL, orderID, paymentID)); err != nil {
		return Checkout{}, false, err
	}
	identity, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return Checkout{}, false, err
	}
	if identity.LiveMode != productLiveMode || (providerConfig.MerchantID != "" && identity.MerchantID != providerConfig.MerchantID) ||
		(providerConfig.StoreID != "" && identity.StoreID != providerConfig.StoreID) {
		return Checkout{}, false, ErrProviderConfigMismatch
	}
	var buyerEmail string
	if err := tx.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, buyerID).Scan(&buyerEmail); err != nil {
		return Checkout{}, false, err
	}
	request := CheckoutRequest{
		PaymentID: paymentID, ResourceID: productID, Purpose: "product", Name: "HCAI CHAT product order " + orderID.String(),
		AmountCents: amountCents, Currency: currency, BuyerIdentity: buyerID.String(), BuyerEmail: buyerEmail,
		SuccessURL: productCheckoutReturnURL(successURL, orderID, paymentID), CancelURL: productCheckoutReturnURL(cancelURL, orderID, paymentID),
		ProductID: providerConfig.ProductIDOnetime, ProductType: "onetime", OrderExternalID: orderID.String(),
	}
	if err := saveProductCheckoutRequestTx(ctx, tx, identity, request); err != nil {
		return Checkout{}, false, err
	}
	if err := bindProductCheckoutCommand(ctx, tx, buyerID, idempotencyKey, paymentID); err != nil {
		return Checkout{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
		VALUES($1,'checkout.requested',NULL,'checkout_pending',jsonb_build_object('orderId',$2::text,'requestId',$3::text))`, paymentID, orderID, requestID); err != nil {
		return Checkout{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Checkout{}, false, err
	}
	checkout = Checkout{
		PaymentID: paymentID, OrderID: orderID, ResourceID: productID, Purpose: "product", Status: "checkout_pending",
		AmountCents: amountCents, Currency: currency, PaymentMode: providerConfig.Provider, RealCharge: productLiveMode, LiveMode: productLiveMode,
	}
	return s.createProviderCheckout(ctx, runtime, checkout)
}

func (s *Service) createProviderCheckout(ctx context.Context, runtime ProviderRuntime, checkout Checkout) (Checkout, bool, error) {
	if err := productdelivery.Ensure(ctx, s.pool, s.config.MediaStores, checkout.OrderID); err != nil {
		return Checkout{}, false, preparationCheckoutError(err)
	}
	// The intent has already committed. Serialize external creation for that
	// durable intent so simultaneous recoveries cannot open separate sessions.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Checkout{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanProductCheckout(tx.QueryRow(ctx, productCheckoutSelect+` WHERE p.id=$1 FOR UPDATE OF p`, checkout.PaymentID))
	if err != nil {
		return Checkout{}, false, err
	}
	if current.PaymentMode != runtime.Provider() || current.ResourceID != checkout.ResourceID {
		return Checkout{}, false, ErrCheckoutConflict
	}
	if err := validProductCheckoutReplay(current); err != nil {
		return Checkout{}, false, err
	}
	if err := validateProductCheckoutReconciliation(ctx, tx, checkout.PaymentID); err != nil {
		return Checkout{}, false, err
	}
	if current.CheckoutURL != "" {
		current.AlreadyCreated = true
		return current, false, tx.Commit(ctx)
	}
	checkout = current
	identity, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return Checkout{}, false, err
	}
	request, err := loadProductCheckoutRequestTx(ctx, tx, checkout, identity)
	if err != nil {
		return Checkout{}, false, err
	}
	buyerID, err := uuid.Parse(request.BuyerIdentity)
	if err != nil {
		return Checkout{}, false, newProviderFailure("payment_response_invalid", 0)
	}
	var originalBuyer uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT payer_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&originalBuyer); err != nil {
		return Checkout{}, false, err
	}
	if originalBuyer != buyerID {
		return Checkout{}, false, newProviderFailure("payment_reconciliation_required", 0)
	}
	if err := validateProductCheckoutEligibility(ctx, tx, buyerID, checkout.ResourceID); err != nil {
		return Checkout{}, false, err
	}
	// Persist the "may have dispatched" fence before any remote mutation. A
	// crash after this commit cannot make a potentially charged order closable.
	var waffoPermit int64
	if runtime.Provider() == "waffo_pancake" {
		waffoPermit, err = reserveWaffoCheckoutTx(ctx, tx, checkout.PaymentID)
	} else {
		err = reserveProductDispatchTx(ctx, tx, checkout.PaymentID)
	}
	if err != nil {
		return Checkout{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Checkout{}, false, err
	}
	nextTx, err := s.pool.Begin(ctx)
	if err != nil {
		return Checkout{}, false, err
	}
	tx = nextTx
	current, err = scanProductCheckout(tx.QueryRow(ctx, productCheckoutSelect+` WHERE p.id=$1 FOR UPDATE OF p`, checkout.PaymentID))
	if err != nil {
		return Checkout{}, false, err
	}
	if current.PaymentMode != runtime.Provider() || current.ResourceID != checkout.ResourceID {
		return Checkout{}, false, ErrCheckoutConflict
	}
	if err = validProductCheckoutReplay(current); err != nil {
		return Checkout{}, false, err
	}
	if waffoPermit > 0 {
		err = validateWaffoCheckoutPermitTx(ctx, tx, checkout.PaymentID, waffoPermit)
	} else {
		err = validateProductCheckoutReconciliation(ctx, tx, checkout.PaymentID)
	}
	if err != nil {
		return Checkout{}, false, err
	}
	if current.CheckoutURL != "" {
		current.AlreadyCreated = true
		return current, false, tx.Commit(ctx)
	}
	if err = validateProductCheckoutEligibility(ctx, tx, buyerID, checkout.ResourceID); err != nil {
		return Checkout{}, false, err
	}
	// Recheck the retry deadline after waiting for another dispatcher.
	request, err = loadProductCheckoutRequestTx(ctx, tx, current, identity)
	if err != nil {
		return Checkout{}, false, err
	}
	checkout = current
	request.CheckoutIdentity = &identity
	// Keep the remote dispatch bounded while this intent is serialized. An
	// uncertain timeout retains the committed request and its original key.
	dispatchCtx, cancelDispatch := context.WithTimeout(ctx, 20*time.Second)
	defer cancelDispatch()
	session, err := runtime.CreateCheckout(dispatchCtx, request)
	if err != nil {
		return Checkout{}, false, SanitizeProviderError(err)
	}
	if session.LiveMode != checkout.LiveMode || !session.ExpiresAt.After(time.Now()) {
		return Checkout{}, false, newProviderFailure("payment_response_invalid", 0)
	}
	result, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='checkout_open',provider_checkout_id=$2,checkout_url=$3,checkout_expires_at=$4,updated_at=now(),version=version+1
		WHERE id=$1 AND status IN ('checkout_pending','checkout_open') AND (provider_checkout_id IS NULL OR provider_checkout_id=$2)`,
		checkout.PaymentID, session.ProviderID, session.CheckoutURL, session.ExpiresAt)
	if err != nil {
		return Checkout{}, false, err
	}
	if result.RowsAffected() != 1 {
		return Checkout{}, false, ErrCheckoutConflict
	}
	checkout.Status = "checkout_open"
	checkout.CheckoutURL = session.CheckoutURL
	checkout.ExpiresAt = session.ExpiresAt
	checkout.LiveMode = session.LiveMode
	checkout.RealCharge = session.LiveMode
	if _, supported := runtime.(CheckoutReader); supported && runtime.Provider() == "stripe" {
		if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts,available_at)
			VALUES($1,jsonb_build_object('paymentId',$2::text),20,$3)`, ProductCheckoutCheckJobKind, checkout.PaymentID, session.ExpiresAt.Add(5*time.Second)); err != nil {
			return Checkout{}, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Checkout{}, false, err
	}
	return checkout, true, nil
}

const productCheckoutSelect = `SELECT p.id,p.order_id,p.resource_id,p.purpose,p.status,p.checkout_url,p.checkout_expires_at,p.amount_cents,p.currency,p.live_mode,p.provider FROM payment_intents p`

func loadProductCheckoutByKey(ctx context.Context, tx pgx.Tx, buyerID uuid.UUID, idempotencyKey string) (Checkout, bool, error) {
	item, err := scanProductCheckout(tx.QueryRow(ctx, productCheckoutSelect+`
		JOIN product_checkout_commands c ON c.payment_id=p.id
		WHERE c.buyer_id=$1 AND c.idempotency_key=$2 AND p.payer_id=$1 AND p.purpose='product'`, buyerID, idempotencyKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return Checkout{}, false, nil
	}
	return item, err == nil, err
}

func loadActiveProductCheckout(ctx context.Context, tx pgx.Tx, buyerID, productID uuid.UUID) (Checkout, bool, error) {
	item, err := scanProductCheckout(tx.QueryRow(ctx, productCheckoutSelect+` WHERE p.payer_id=$1 AND p.resource_id=$2 AND p.purpose='product'
		AND p.compensation_reason IS NULL
		AND p.status IN ('checkout_pending','checkout_open','paid','transfer_pending','transferred','refund_pending','refund_failed')`, buyerID, productID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Checkout{}, false, nil
	}
	return item, err == nil, err
}

func scanProductCheckout(row pgx.Row) (Checkout, error) {
	var item Checkout
	var checkoutURL *string
	var expiresAt *time.Time
	var provider string
	err := row.Scan(
		&item.PaymentID, &item.OrderID, &item.ResourceID, &item.Purpose, &item.Status, &checkoutURL, &expiresAt,
		&item.AmountCents, &item.Currency, &item.LiveMode, &provider)
	if err != nil {
		return Checkout{}, err
	}
	if checkoutURL != nil {
		item.CheckoutURL = *checkoutURL
	}
	if expiresAt != nil {
		item.ExpiresAt = *expiresAt
	}
	item.PaymentMode, item.RealCharge = provider, item.LiveMode
	return item, nil
}

func (s *Service) BeginWalletTopupCheckout(ctx context.Context, userID uuid.UUID, amountCents int, idempotencyKey, requestID, successURL, cancelURL string) (result BillingCheckout, created bool, err error) {
	defer func() {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "40001", "40P01":
				err = ErrCheckoutBusy
			case "23505":
				err = ErrCheckoutConflict
			case "23514":
				if pgErr.ConstraintName == "wallet_topup_minimum" {
					err = billing.ErrTopupAmountOutOfRange
				}
			}
		}
	}()
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || s.pool == nil || !s.config.Enabled {
		return BillingCheckout{}, false, ErrDisabled
	}
	if userID == uuid.Nil || amountCents < 50 || amountCents > 99999999 || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || !validReturnURL(successURL) || !validReturnURL(cancelURL) {
		return BillingCheckout{}, false, ErrInvalidCheckout
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return BillingCheckout{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	checkout, found, err := loadBillingCheckoutByKey(ctx, tx, userID, "wallet_topup", idempotencyKey)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	if found {
		if checkout.ResourceID != userID || checkout.AmountCents != amountCents {
			return BillingCheckout{}, false, ErrCheckoutConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return BillingCheckout{}, false, err
		}
		return s.createBillingProviderCheckout(ctx, checkout)
	}
	providerConfig, err := s.resolveProductProvider(ctx)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	if !s.providerConfigReadyForPurpose(providerConfig, "wallet_topup") {
		return BillingCheckout{}, false, ErrProviderConfigMismatch
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Checkout); err != nil {
		return BillingCheckout{}, false, err
	}
	var accountExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM billing_accounts WHERE user_id=$1 AND currency='USD')`, userID).Scan(&accountExists); err != nil {
		return BillingCheckout{}, false, err
	}
	if !accountExists {
		return BillingCheckout{}, false, ErrInvalidCheckout
	}
	topupSettings, err := billing.ReadWalletTopupSettings(ctx, tx, true)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	if amountCents < topupSettings.MinimumAmountCents {
		return BillingCheckout{}, false, billing.ErrTopupAmountOutOfRange
	}
	paymentID, createdAt := uuid.New(), time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intents(id,provider,purpose,payer_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key,created_at,updated_at)
		VALUES($1,$2,'wallet_topup',$3,$3,$4,'USD','checkout_pending',$5,$6,$7,$7)`,
		paymentID, providerConfig.Provider, userID, amountCents, s.productLiveModeFor(providerConfig), idempotencyKey, createdAt); err != nil {
		return BillingCheckout{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence) VALUES($1,'checkout.requested',NULL,'checkout_pending',jsonb_build_object('requestId',$2::text,'purpose','wallet_topup','topupSettingsVersion',$3::bigint,'minimumTopupCents',$4::integer))`, paymentID, requestID, topupSettings.Version, topupSettings.MinimumAmountCents); err != nil {
		return BillingCheckout{}, false, err
	}
	checkout = BillingCheckout{PaymentID: paymentID, ResourceID: userID, Purpose: "wallet_topup", Status: "checkout_pending", AmountCents: amountCents, Currency: "USD", PaymentMode: providerConfig.Provider, RealCharge: s.productLiveModeFor(providerConfig), LiveMode: s.productLiveModeFor(providerConfig)}
	if err := s.saveBillingCheckoutRequestTx(ctx, tx, checkout, providerConfig, successURL, cancelURL); err != nil {
		return BillingCheckout{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BillingCheckout{}, false, err
	}
	return s.createBillingProviderCheckout(ctx, checkout)
}

func (s *Service) BeginSubscriptionCheckout(ctx context.Context, userID, planID uuid.UUID, idempotencyKey, requestID, successURL, cancelURL string) (BillingCheckout, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || s.pool == nil || !s.config.Enabled {
		return BillingCheckout{}, false, ErrDisabled
	}
	if userID == uuid.Nil || planID == uuid.Nil || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || !validReturnURL(successURL) || !validReturnURL(cancelURL) {
		return BillingCheckout{}, false, ErrInvalidCheckout
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return BillingCheckout{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	checkout, found, err := loadBillingCheckoutByKey(ctx, tx, userID, "subscription", idempotencyKey)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	if found {
		if checkout.ResourceID != planID {
			return BillingCheckout{}, false, ErrCheckoutConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return BillingCheckout{}, false, err
		}
		return s.createBillingProviderCheckout(ctx, checkout)
	}
	providerConfig, err := s.resolveProductProvider(ctx)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	if !s.providerConfigReadyForPurpose(providerConfig, "subscription") {
		return BillingCheckout{}, false, ErrProviderConfigMismatch
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Checkout); err != nil {
		return BillingCheckout{}, false, err
	}
	var currency string
	var amountCents int
	if err := tx.QueryRow(ctx, `SELECT price_cents,currency FROM subscription_plans WHERE id=$1 AND active=true FOR SHARE`, planID).Scan(&amountCents, &currency); errors.Is(err, pgx.ErrNoRows) {
		return BillingCheckout{}, false, ErrInvalidCheckout
	} else if err != nil {
		return BillingCheckout{}, false, err
	}
	if amountCents < 50 || currency != "USD" {
		return BillingCheckout{}, false, ErrInvalidCheckout
	}
	var activePlanID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT plan_id FROM user_subscriptions WHERE user_id=$1 AND status='active' AND current_period_end>now() FOR UPDATE`, userID).Scan(&activePlanID); err == nil && activePlanID == planID {
		return BillingCheckout{}, false, ErrCheckoutConflict
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return BillingCheckout{}, false, err
	}
	paymentID, createdAt := uuid.New(), time.Now().UTC()
	liveMode := s.productLiveModeFor(providerConfig)
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intents(id,provider,purpose,payer_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key,created_at,updated_at)
		VALUES($1,$2,'subscription',$3,$4,$5,$6,'checkout_pending',$7,$8,$9,$9)`,
		paymentID, providerConfig.Provider, userID, planID, amountCents, currency, liveMode, idempotencyKey, createdAt); err != nil {
		return BillingCheckout{}, false, ErrCheckoutConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence) VALUES($1,'checkout.requested',NULL,'checkout_pending',jsonb_build_object('requestId',$2::text,'purpose','subscription','planId',$3::text))`, paymentID, requestID, planID); err != nil {
		return BillingCheckout{}, false, err
	}
	checkout = BillingCheckout{PaymentID: paymentID, ResourceID: planID, Purpose: "subscription", Status: "checkout_pending", AmountCents: amountCents, Currency: currency, PaymentMode: providerConfig.Provider, RealCharge: liveMode, LiveMode: liveMode}
	if err := saveSubscriptionContractTx(ctx, tx, paymentID); err != nil {
		return BillingCheckout{}, false, err
	}
	if err := s.saveBillingCheckoutRequestTx(ctx, tx, checkout, providerConfig, successURL, cancelURL); err != nil {
		return BillingCheckout{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BillingCheckout{}, false, err
	}
	return s.createBillingProviderCheckout(ctx, checkout)
}

func loadBillingCheckoutByKey(ctx context.Context, tx pgx.Tx, userID uuid.UUID, purpose, idempotencyKey string) (BillingCheckout, bool, error) {
	item, err := scanBillingCheckout(tx.QueryRow(ctx, billingCheckoutSelect+` WHERE payer_id=$1 AND purpose=$2 AND idempotency_key=$3`, userID, purpose, idempotencyKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return BillingCheckout{}, false, nil
	}
	if err != nil {
		return BillingCheckout{}, false, err
	}
	return item, true, nil
}

func billingCheckoutReturnURL(value string, paymentID uuid.UUID) string {
	parsed, err := url.Parse(value)
	if err != nil || paymentID == uuid.Nil {
		return value
	}
	query := parsed.Query()
	query.Set("paymentId", paymentID.String())
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func fulfillWalletTopupPaymentTx(ctx context.Context, tx pgx.Tx, provider string, providerEventID, paymentID uuid.UUID, amount int, currency, providerPaymentID string, providerChargeID *string) error {
	var status, purpose string
	var payerID, resourceID uuid.UUID
	var expectedAmount int
	var expectedCurrency string
	var expectedProviderPaymentID *string
	if err := tx.QueryRow(ctx, `SELECT status,purpose,payer_id,resource_id,amount_cents,currency,provider_payment_id FROM payment_intents WHERE id=$1 AND purpose='wallet_topup' FOR UPDATE`, paymentID).Scan(&status, &purpose, &payerID, &resourceID, &expectedAmount, &expectedCurrency, &expectedProviderPaymentID); err != nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if purpose != "wallet_topup" || resourceID != payerID || amount != expectedAmount || currency != expectedCurrency || strings.TrimSpace(providerPaymentID) == "" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if status == "paid" {
		if expectedProviderPaymentID == nil || *expectedProviderPaymentID != providerPaymentID {
			return newProviderFailure("payment_response_invalid", 0)
		}
		_, err := tx.Exec(ctx, `UPDATE payment_intents SET provider_charge_id=COALESCE(provider_charge_id,$2),updated_at=now() WHERE id=$1`, paymentID, providerChargeID)
		return err
	}
	if !oneOf(status, "checkout_pending", "checkout_open") {
		return newProviderFailure("payment_response_invalid", 0)
	}
	var balance int64
	if err := tx.QueryRow(ctx, `SELECT balance_cents FROM billing_accounts WHERE user_id=$1 AND currency='USD' FOR UPDATE`, payerID).Scan(&balance); err != nil {
		return err
	}
	balanceAfter := balance + int64(amount)
	if _, err := tx.Exec(ctx, `UPDATE billing_accounts SET balance_cents=$3,version=version+1,updated_at=now() WHERE user_id=$1 AND currency=$2`, payerID, "USD", balanceAfter); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO billing_entries(user_id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description,metadata) VALUES($1,$2,'wallet_topup','credit',$3,'USD',$4,$5,$6)`, payerID, paymentID, amount, balanceAfter, "Wallet top-up", map[string]any{"provider": provider, "paymentId": paymentID.String(), "providerPaymentId": providerPaymentID}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='paid',provider_payment_id=$2,provider_charge_id=COALESCE($3,provider_charge_id),paid_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, paymentID, providerPaymentID, providerChargeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence) VALUES($1,$2,'wallet_topup.confirmed',$3,'paid',jsonb_build_object('amountCents',$4::integer,'currency',$5::text,'provider',$6::text)) ON CONFLICT DO NOTHING`, paymentID, providerEventID, status, amount, currency, provider); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'billing.wallet_topup','billing_account',$2,$3,$4)`, payerID, payerID, providerEventRequestID(provider, providerEventID), map[string]any{"paymentId": paymentID.String(), "amountCents": amount, "currency": currency, "provider": provider}); err != nil {
		return err
	}
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{UserID: payerID, Kind: "billing.wallet_topup_completed", Title: "Wallet top-up completed", Body: "Your USD wallet balance has been updated.", TargetPath: "/workspace/billing", ResourceType: "billing_account", ResourceID: &payerID, SourceKey: "billing:wallet-topup:" + paymentID.String()})
}

func fulfillSubscriptionPaymentTx(ctx context.Context, tx pgx.Tx, provider string, providerEventID, paymentID uuid.UUID, amount int, currency, providerPaymentID string, providerChargeID *string) error {
	var status, purpose, idempotencyKey string
	var payerID, planID uuid.UUID
	var expectedAmount int
	var expectedCurrency string
	var expectedProviderPaymentID *string
	if err := tx.QueryRow(ctx, `SELECT status,purpose,payer_id,resource_id,amount_cents,currency,idempotency_key,provider_payment_id FROM payment_intents WHERE id=$1 AND purpose='subscription' FOR UPDATE`, paymentID).Scan(&status, &purpose, &payerID, &planID, &expectedAmount, &expectedCurrency, &idempotencyKey, &expectedProviderPaymentID); err != nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if purpose != "subscription" || amount != expectedAmount || currency != expectedCurrency || strings.TrimSpace(providerPaymentID) == "" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if status == "paid" {
		if expectedProviderPaymentID != nil && *expectedProviderPaymentID == providerPaymentID {
			_, err := tx.Exec(ctx, `UPDATE payment_intents SET provider_charge_id=COALESCE(provider_charge_id,$2),updated_at=now() WHERE id=$1`, paymentID, providerChargeID)
			return err
		}
		if expectedProviderPaymentID == nil {
			return newProviderFailure("payment_response_invalid", 0)
		}
		return newProviderFailure("payment_response_invalid", 0)
	}
	if !oneOf(status, "checkout_pending", "checkout_open") {
		return newProviderFailure("payment_response_invalid", 0)
	}
	contract, err := loadSubscriptionContractTx(ctx, tx, paymentID)
	if err != nil {
		return err
	}
	planName, tierCode, includedPoints, billingPeriodDays := contract.Name, contract.Tier, contract.Points, contract.Days
	var activePlanID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT plan_id FROM user_subscriptions WHERE user_id=$1 AND status='active' AND current_period_end>now() FOR UPDATE`, payerID).Scan(&activePlanID); err == nil && activePlanID == planID {
		return newProviderFailure("payment_response_invalid", 0)
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE user_subscriptions SET status=CASE WHEN current_period_end<=now() THEN 'expired' ELSE 'cancelled' END,cancelled_at=CASE WHEN current_period_end>now() THEN now() ELSE cancelled_at END,updated_at=now() WHERE user_id=$1 AND status='active'`, payerID); err != nil {
		return err
	}
	subscriptionID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO user_subscriptions(id,user_id,plan_id,price_cents,currency,granted_points,current_period_end,purchase_operation_id,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,now()+make_interval(days => $7),$8,$9)`, subscriptionID, payerID, planID, amount, currency, includedPoints, billingPeriodDays, paymentID, idempotencyKey); err != nil {
		return err
	}
	var pointsAfter int64
	if err := tx.QueryRow(ctx, `UPDATE point_accounts SET balance_points=balance_points+$2,lifetime_earned_points=lifetime_earned_points+$2,version=version+1,updated_at=now() WHERE user_id=$1 RETURNING balance_points`, payerID, includedPoints).Scan(&pointsAfter); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO point_entries(user_id,operation_id,entry_type,direction,amount_points,balance_after_points,description,metadata) VALUES($1,$2,'subscription_credit','credit',$3,$4,$5,$6)`, payerID, paymentID, includedPoints, pointsAfter, "Subscription points: "+planName, map[string]any{"planId": planID.String(), "subscriptionId": subscriptionID.String(), "provider": provider, "paymentId": paymentID.String()}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='paid',provider_payment_id=$2,provider_charge_id=COALESCE($3,provider_charge_id),paid_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, paymentID, providerPaymentID, providerChargeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence) VALUES($1,$2,'subscription.confirmed',$3,'paid',jsonb_build_object('planId',$4::text,'tierCode',$5::text,'provider',$6::text)) ON CONFLICT DO NOTHING`, paymentID, providerEventID, status, planID, tierCode, provider); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'billing.subscription_purchase','subscription',$2,$3,$4)`, payerID, subscriptionID, providerEventRequestID(provider, providerEventID), map[string]any{"paymentId": paymentID.String(), "planId": planID.String(), "provider": provider, "amountCents": amount}); err != nil {
		return err
	}
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{UserID: payerID, Kind: "billing.subscription_completed", Title: "Subscription activated", Body: "Your subscription is active and its points are available.", TargetPath: "/workspace/billing", ResourceType: "subscription", ResourceID: &subscriptionID, SourceKey: "billing:subscription:" + paymentID.String()})
}

func fulfillOrRenewWaffoSubscriptionTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID, amount int, currency, providerPaymentID string, occurredAt time.Time) error {
	var status, purpose string
	var payerID, planID uuid.UUID
	var expectedAmount int
	var expectedCurrency string
	var initialProviderPaymentID *string
	if err := tx.QueryRow(ctx, `
		SELECT status,purpose,payer_id,resource_id,amount_cents,currency,provider_payment_id
		FROM payment_intents WHERE id=$1 AND purpose='subscription' FOR UPDATE`, paymentID).Scan(
		&status, &purpose, &payerID, &planID, &expectedAmount, &expectedCurrency, &initialProviderPaymentID); errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	} else if err != nil {
		return err
	}
	if purpose != "subscription" || amount != expectedAmount || currency != expectedCurrency || strings.TrimSpace(providerPaymentID) == "" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if status != "paid" {
		return fulfillSubscriptionPaymentTx(ctx, tx, "waffo_pancake", providerEventID, paymentID, amount, currency, providerPaymentID, nil)
	}
	// Some installations deliver the initial payment event again under the
	// renewal type. The same Provider payment must not grant another period.
	if initialProviderPaymentID != nil && *initialProviderPaymentID == providerPaymentID {
		return nil
	}
	if initialProviderPaymentID == nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	// Delivery IDs identify notifications, not captures. Reconcile the durable
	// credit and renewal evidence under the original payment lock before granting.
	var credited, renewed bool
	if err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM point_entries WHERE entry_type='subscription_credit'
		 AND metadata->>'provider'='waffo_pancake' AND metadata->>'paymentId'=$1
		 AND metadata->>'providerPaymentId'=$2),
		EXISTS(SELECT 1 FROM payment_intent_events WHERE payment_id=$3 AND event_type='subscription.renewed'
		 AND evidence->>'provider'='waffo_pancake' AND evidence->>'providerPaymentId'=$2)`,
		paymentID.String(), providerPaymentID, paymentID).Scan(&credited, &renewed); err != nil {
		return err
	}
	if credited != renewed {
		return ErrCheckoutReconciliation
	}
	if credited {
		return nil
	}

	var subscriptionID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT s.id
		FROM user_subscriptions s
		WHERE s.user_id=$1 AND s.plan_id=$2 AND s.purchase_operation_id=$3 AND s.status IN ('active','expired')
		AND NOT EXISTS(SELECT 1 FROM user_subscriptions newer WHERE newer.user_id=s.user_id AND newer.id<>s.id AND (newer.status='active' OR (newer.created_at,newer.id)>(s.created_at,s.id)))
		FOR UPDATE OF s`, payerID, planID, paymentID).Scan(&subscriptionID); errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	} else if err != nil {
		return err
	}
	contract, err := loadSubscriptionContractTx(ctx, tx, paymentID)
	if err != nil {
		return err
	}
	planName, includedPoints, billingPeriodDays := contract.Name, contract.Points, contract.Days

	var pointsAfter int64
	if err := tx.QueryRow(ctx, `
		UPDATE point_accounts
		SET balance_points=balance_points+$2,lifetime_earned_points=lifetime_earned_points+$2,version=version+1,updated_at=now()
		WHERE user_id=$1 RETURNING balance_points`, payerID, includedPoints).Scan(&pointsAfter); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE user_subscriptions
		SET current_period_end=GREATEST(current_period_end,$2)+make_interval(days => $3),status='active',updated_at=now()
		WHERE id=$1`, subscriptionID, occurredAt, billingPeriodDays); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO point_entries(user_id,operation_id,entry_type,direction,amount_points,balance_after_points,description,metadata)
		VALUES($1,$2,'subscription_credit','credit',$3,$4,$5,$6)`, payerID, providerEventID, includedPoints, pointsAfter,
		"Subscription renewal points: "+planName, map[string]any{"planId": planID.String(), "subscriptionId": subscriptionID.String(), "provider": "waffo_pancake", "paymentId": paymentID.String(), "providerPaymentId": providerPaymentID}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'subscription.renewed','paid','paid',jsonb_build_object('planId',$3::text,'provider','waffo_pancake','providerPaymentId',$4::text))`, paymentID, providerEventID, planID, providerPaymentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'billing.subscription_renewal','subscription',$2,$3,$4)`, payerID, subscriptionID,
		providerEventRequestID("waffo_pancake", providerEventID), map[string]any{"paymentId": paymentID.String(), "planId": planID.String(), "provider": "waffo_pancake", "amountCents": amount, "providerPaymentId": providerPaymentID}); err != nil {
		return err
	}
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{UserID: payerID, Kind: "billing.subscription_completed", Title: "Subscription renewed", Body: "Your subscription period and points have been renewed.", TargetPath: "/workspace/billing", ResourceType: "subscription", ResourceID: &subscriptionID, SourceKey: "billing:subscription-renewal:" + providerEventID.String()})
}

func failBillingPaymentTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID) error {
	var status, purpose string
	if err := tx.QueryRow(ctx, `SELECT status,purpose FROM payment_intents WHERE id=$1 AND purpose IN ('wallet_topup','subscription') FOR UPDATE`, paymentID).Scan(&status, &purpose); err != nil {
		return err
	}
	if err := verifyStripeBillingWebhookBindingTx(ctx, tx, providerEventID); err != nil {
		return err
	}
	// An attempt failure does not prove the Checkout is closed and must not
	// prevent a later success, or demote money already credited.
	_, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence) VALUES($1,$2,'payment.attempt_failed',$3,$3,jsonb_build_object('purpose',$4::text,'checkoutClosureVerified',false)) ON CONFLICT DO NOTHING`, paymentID, providerEventID, status, purpose)
	return err
}

func (s *Service) BeginProductRefund(ctx context.Context, buyerID, orderID uuid.UUID, idempotencyKey, requestID, reason string) (bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	reason = strings.TrimSpace(reason)
	if s == nil || s.pool == nil || !s.config.Enabled {
		return false, ErrDisabled
	}
	if buyerID == uuid.Nil || orderID == uuid.Nil || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || !productpolicy.ValidRefundReason(reason) {
		return false, ErrInvalidRefund
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var paymentID uuid.UUID
	var intentStatus, orderStatus, provider string
	var refundWindowDays int
	var createdAt time.Time
	var providerRefundID, existingIdempotencyKey *string
	var existingOperationID *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT pi.id,pi.status,pi.provider,pi.provider_refund_id,o.refund_idempotency_key,o.refund_operation_id,
		       o.status,o.refund_window_days_snapshot,o.created_at
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
		WHERE o.id=$1 AND o.buyer_id=$2 AND pi.purpose='product' FOR UPDATE OF pi,o`, orderID, buyerID).Scan(
		&paymentID, &intentStatus, &provider, &providerRefundID, &existingIdempotencyKey, &existingOperationID,
		&orderStatus, &refundWindowDays, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrRefundConflict
	}
	if err != nil {
		return false, err
	}
	var historicalCommand bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_attempts
      WHERE payment_id=$1 AND idempotency_key=$2 AND operation_id IS DISTINCT FROM $3::uuid)`, paymentID, idempotencyKey, existingOperationID).Scan(&historicalCommand); err != nil {
		return false, err
	}
	if historicalCommand {
		return false, tx.Commit(ctx)
	}
	if oneOf(intentStatus, "refund_pending", "refunded") && (existingIdempotencyKey == nil || *existingIdempotencyKey != idempotencyKey) {
		return false, ErrRefundConflict
	}
	if intentStatus == "refunded" && orderStatus == "refunded" {
		return false, tx.Commit(ctx)
	}
	if intentStatus == "paid" && orderStatus == "fulfilled" && existingIdempotencyKey != nil && *existingIdempotencyKey == idempotencyKey {
		return false, tx.Commit(ctx)
	}
	if intentStatus == "refund_pending" && orderStatus == "refund_requested" {
		if existingOperationID == nil {
			return false, ErrRefundConflict
		}
		if providerRefundID == nil {
			// Restore pre-queue requests, but do not restart an exhausted attempt
			// or create a second job for an already accepted command.
			if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
				SELECT $1,jsonb_build_object('paymentId',$2::text,'operationId',$3::text),20
				WHERE NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind=$1 AND j.payload->>'paymentId'=$2::text
				 AND (j.payload->>'operationId'=$3::text OR (NOT j.payload ? 'operationId'
				 AND j.created_at >= (SELECT refund_requested_at FROM orders WHERE id=$4))))`,
				ProductRefundJobKind, paymentID, existingOperationID, orderID); err != nil {
				return false, err
			}
		}
		return false, tx.Commit(ctx)
	}
	if intentStatus != "paid" || orderStatus != "fulfilled" {
		return false, ErrRefundConflict
	}
	var needsReview bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=$1)`, paymentID).Scan(&needsReview); err != nil {
		return false, err
	}
	if needsReview {
		return false, ErrRefundConflict
	}
	if !s.CanRefundProduct(provider) {
		return false, ErrProviderUnavailable
	}
	if !productpolicy.RefundWindowOpen(createdAt, refundWindowDays, time.Now()) {
		return false, ErrRefundExpired
	}
	if err := RecordProductRefundAttemptTx(ctx, tx, paymentID); err != nil {
		return false, err
	}
	operationID := uuid.New()
	if _, err := tx.Exec(ctx, `
		UPDATE orders SET status='refund_requested',refund_reason=$2,refund_idempotency_key=$3,refund_operation_id=$4,
		  refund_correlation_enabled=true,refund_requested_at=now(),updated_at=now() WHERE id=$1`, orderID, reason, idempotencyKey, operationID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='refund_pending',updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
		return false, err
	}
	if err := RecordNewProductRefundAttemptTx(ctx, tx, paymentID, operationID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events(order_id,actor_id,from_status,to_status,reason,sequence)
		SELECT $1,$2,'fulfilled','refund_requested',$3,COALESCE(max(sequence),0)+1 FROM order_events WHERE order_id=$1`, orderID, buyerID, reason); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
		VALUES($1,'refund.requested','paid','refund_pending',jsonb_build_object('orderId',$2::text,'requestId',$3::text))`, paymentID, orderID, requestID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
		VALUES($1,jsonb_build_object('paymentId',$2::text,'operationId',$3::text),20)`, ProductRefundJobKind, paymentID, operationID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) createProviderRefund(ctx context.Context, runtime ProviderRuntime, paymentID, operationID uuid.UUID, providerPaymentID string, amountCents int) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currency, reason, provider, status, orderStatus, expectedPayment string
	var expectedOperation *uuid.UUID
	var recordedRefund *string
	var expectedAmount int
	var includeOperationMetadata bool
	err = tx.QueryRow(ctx, `SELECT pi.currency,COALESCE(o.refund_reason,''),
		pi.provider,pi.status,o.status,pi.provider_payment_id,pi.amount_cents,o.refund_operation_id,pi.provider_refund_id,o.refund_correlation_enabled
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
		WHERE pi.id=$1 AND pi.purpose='product' FOR UPDATE OF pi,o`, paymentID).Scan(
		&currency, &reason, &provider, &status, &orderStatus, &expectedPayment, &expectedAmount, &expectedOperation, &recordedRefund, &includeOperationMetadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrRefundConflict
	}
	if err != nil {
		return false, err
	}
	if runtime.Provider() != provider || expectedOperation == nil || *expectedOperation != operationID || expectedPayment != providerPaymentID || expectedAmount != amountCents {
		return false, ErrRefundConflict
	}
	if status == "refunded" && orderStatus == "refunded" {
		return false, tx.Commit(ctx)
	}
	if status != "refund_pending" || orderStatus != "refund_requested" {
		return false, ErrRefundConflict
	}
	if recordedRefund != nil {
		return false, tx.Commit(ctx)
	}
	var needsReview bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=$1)`, paymentID).Scan(&needsReview); err != nil {
		return false, err
	}
	if needsReview {
		return false, newProviderFailure("payment_reconciliation_required", 0)
	}
	if err := RecordProductRefundAttemptTx(ctx, tx, paymentID); err != nil {
		return false, err
	}
	identity, original, err := verifyProductPaymentIdentity(ctx, tx, runtime, paymentID)
	if err != nil {
		return false, err
	}
	if provider == "waffo_pancake" {
		version, err := reserveWaffoRefundTx(ctx, tx, paymentID, operationID)
		if err != nil {
			return false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		nextTx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return false, err
		}
		tx = nextTx
		if err := lockWaffoRefundReservationTx(ctx, tx, paymentID, operationID, version); err != nil {
			return false, err
		}
		// Configuration can change across the commit. Authenticate the original
		// merchant again; any uncertainty leaves the consumed permit for review.
		identity, original, err = verifyProductPaymentIdentity(ctx, tx, runtime, paymentID)
		if err != nil {
			return false, err
		}
	}
	// Refund customer/store routing comes from the original checkout, never a
	// current profile or the provider selected for new sales.
	if provider == "stripe" {
		if err := validateStripeProductRefundDispatchTx(ctx, tx, paymentID, operationID); err != nil {
			return false, err
		}
	}
	dispatchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	refund, err := runtime.CreateRefund(dispatchCtx, RefundRequest{PaymentID: paymentID, OperationID: operationID, IncludeOperationMetadata: includeOperationMetadata, ProviderPaymentID: providerPaymentID, StoreID: identity.StoreID, AmountCents: amountCents, Currency: currency, Reason: reason, BuyerIdentity: original.BuyerIdentity, BuyerEmail: original.BuyerEmail, PaymentIdentity: &identity})
	if err != nil {
		return false, SanitizeProviderError(err)
	}
	if strings.TrimSpace(refund.ProviderID) == "" || refund.ProviderPaymentID != expectedPayment || refund.AmountCents != expectedAmount || refund.Currency != currency || strings.TrimSpace(refund.Status) == "" {
		return false, newProviderFailure("payment_response_invalid", 0)
	}
	if provider == "waffo_pancake" {
		result, err := tx.Exec(ctx, `UPDATE product_refund_dispatches SET responded_at=now(),provider_refund_id=$2
 WHERE operation_id=$1 AND reserved_at IS NOT NULL AND responded_at IS NULL`, operationID, refund.ProviderID)
		if err != nil {
			return false, err
		}
		if result.RowsAffected() != 1 {
			return false, ErrRefundConflict
		}
	}
	attemptResult, err := tx.Exec(ctx, `UPDATE product_refund_attempts SET provider_refund_id=$2,status='pending',updated_at=now()
        WHERE operation_id=$1 AND payment_id=$3 AND (provider_refund_id IS NULL OR provider_refund_id=$2)`, operationID, refund.ProviderID, paymentID)
	if err != nil {
		return false, err
	}
	if attemptResult.RowsAffected() != 1 {
		return false, ErrRefundConflict
	}
	result, err := tx.Exec(ctx, `
		UPDATE payment_intents SET provider_refund_id=$2,updated_at=now(),version=version+1
		WHERE id=$1 AND status='refund_pending' AND (provider_refund_id IS NULL OR provider_refund_id=$2)`, paymentID, refund.ProviderID)
	if err != nil {
		return false, err
	}
	if result.RowsAffected() != 1 {
		return false, ErrRefundConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
		VALUES($1,'refund.provider_requested','refund_pending','refund_pending',jsonb_build_object('operationId',$2::text,'providerStatus',$3::text,'providerRefundId',$4::text))`,
		paymentID, operationID, refund.Status, refund.ProviderID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Service) HandlePaymentEventJob(ctx context.Context, job jobs.Job) error {
	var payload paymentEventJobPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.EventID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM payment_provider_event_processing WHERE event_id=$1 FOR UPDATE`, payload.EventID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_invalid_request", 0)
	} else if err != nil {
		return err
	}
	if status == "processed" || status == "ignored" {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_provider_event_processing SET status='processing',attempt_count=attempt_count+1,version=version+1,updated_at=now() WHERE event_id=$1`, payload.EventID); err != nil {
		return err
	}
	var provider, eventType, objectID, objectType string
	var eventResourceID *uuid.UUID
	var eventLiveMode bool
	var paymentID *uuid.UUID
	var amount *int64
	var eventPurpose, currency, paymentStatus, providerPaymentID, providerChargeID, destinationID *string
	var destinationUserID *uuid.UUID
	var accountChargesEnabled, accountPayoutsEnabled, accountDetailsSubmitted, accountRequirementsDue *bool
	var disputeStatus, disputeReason, disputeNetworkReasonCode *string
	var disputeDueBy *time.Time
	var occurredAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT provider,event_type,object_id,payment_id,purpose,amount_cents,currency,payment_status,provider_payment_id,provider_charge_id,
		       destination_id,destination_user_id,account_charges_enabled,account_payouts_enabled,account_details_submitted,account_requirements_due,occurred_at,
		       object_type,resource_id,live_mode,dispute_status,dispute_reason,dispute_network_reason_code,dispute_due_by
		FROM payment_provider_events WHERE id=$1`, payload.EventID).Scan(
		&provider, &eventType, &objectID, &paymentID, &eventPurpose, &amount, &currency, &paymentStatus, &providerPaymentID, &providerChargeID,
		&destinationID, &destinationUserID, &accountChargesEnabled, &accountPayoutsEnabled, &accountDetailsSubmitted, &accountRequirementsDue, &occurredAt,
		&objectType, &eventResourceID, &eventLiveMode, &disputeStatus, &disputeReason, &disputeNetworkReasonCode, &disputeDueBy); err != nil {
		return err
	}
	if eventType == "account.updated" {
		if destinationID == nil || accountChargesEnabled == nil || accountPayoutsEnabled == nil || accountDetailsSubmitted == nil || accountRequirementsDue == nil {
			return newProviderFailure("payment_response_invalid", 0)
		}
		var identity *ProductCheckoutIdentity
		if runtime, runtimeErr := s.runtimes.Runtime("stripe"); runtimeErr == nil {
			current, identityErr := checkoutIdentity(ctx, runtime)
			if identityErr != nil {
				return identityErr
			}
			identity = &current
		}
		handled, err := syncPayoutDestinationTx(ctx, tx, payload.EventID, *destinationID, destinationUserID, *accountChargesEnabled, *accountPayoutsEnabled, *accountDetailsSubmitted, *accountRequirementsDue, occurredAt, identity)
		if err != nil {
			return err
		}
		processingStatus := "processed"
		if !handled {
			processingStatus = "ignored"
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_provider_event_processing SET status=$2,processed_at=now(),version=version+1,updated_at=now() WHERE event_id=$1`, payload.EventID, processingStatus); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if provider == "stripe" && isStripeDisputeEvent(eventType) {
		err := handleProductDisputeEventTx(ctx, tx, payload.EventID, paymentID, eventType, objectID, eventLiveMode,
			amount, currency, providerPaymentID, providerChargeID, disputeStatus, disputeReason, disputeNetworkReasonCode, disputeDueBy, occurredAt)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_provider_event_processing SET status='processed',processed_at=now(),version=version+1,updated_at=now() WHERE event_id=$1`, payload.EventID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if paymentID == nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	var intentPurpose, intentProvider string
	if err := tx.QueryRow(ctx, `SELECT purpose,provider FROM payment_intents WHERE id=$1`, *paymentID).Scan(&intentPurpose, &intentProvider); errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	} else if err != nil {
		return err
	}
	if eventPurpose != nil && *eventPurpose != intentPurpose {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if intentProvider != provider {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if provider == "stripe" && oneOf(intentPurpose, "wallet_topup", "subscription") {
		if _, err := tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, *paymentID); err != nil {
			return err
		}
		if err := verifyStripeBillingWebhookBindingTx(ctx, tx, payload.EventID); err != nil {
			return err
		}
		if eventType == "checkout.session.completed" && paymentStatus != nil && *paymentStatus == "unpaid" {
			// Async Checkout completion is not payment success. Retain the
			// verified session/capture relation for subsequent signed results.
			if _, err := tx.Exec(ctx, `UPDATE payment_provider_event_processing SET status='processed',processed_at=now(),version=version+1,updated_at=now() WHERE event_id=$1`, payload.EventID); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
	}
	if provider == "stripe" && intentPurpose == "product" {
		matched, err := validateStripeProductEvent(ctx, tx, minimizedProviderEvent{
			EventType: eventType, ObjectID: objectID, ObjectType: objectType, PaymentID: paymentID, ResourceID: eventResourceID,
			LiveMode: eventLiveMode, Purpose: eventPurpose, AmountCents: amount, Currency: currency,
			ProviderPaymentID: providerPaymentID, ProviderChargeID: providerChargeID,
		})
		if errors.Is(err, ErrInvalidEvent) {
			return newProviderFailure("payment_response_invalid", 0)
		}
		if err != nil {
			return err
		}
		if !matched {
			return newProviderFailure("payment_response_invalid", 0)
		}
	}
	if eventType == "checkout.observed" && provider != "waffo_pancake" {
		var verified bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_provider_events e JOIN payment_intents pi ON pi.id=e.payment_id
		 JOIN jobs j ON j.id=e.checkout_job_id AND j.kind=$2 AND j.payload->>'paymentId'=pi.id::text
		 WHERE e.id=$1 AND e.evidence_source='provider_query' AND e.provider='stripe' AND pi.purpose='product'
		 AND e.object_id=pi.provider_checkout_id AND e.resource_id=pi.resource_id AND e.live_mode=pi.live_mode)`, payload.EventID, ProductCheckoutCheckJobKind).Scan(&verified); err != nil {
			return err
		}
		if !verified {
			return newProviderFailure("payment_response_invalid", 0)
		}
	}
	if provider == "waffo_pancake" {
		if intentPurpose == "product" {
			if eventType == "checkout.observed" {
				if err := verifyWaffoProductCheckoutQueryTx(ctx, tx, payload.EventID); err != nil {
					return err
				}
			} else {
				if err := verifyWaffoProductWebhookBindingTx(ctx, tx, payload.EventID); err != nil {
					return err
				}
			}
		} else {
			// Keep the original financial tuple stable through verification and
			// fulfillment; ingress deliberately avoids this lock during dispatch.
			if _, err := tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, *paymentID); err != nil {
				return err
			}
			if err := verifyWaffoBillingWebhookBindingTx(ctx, tx, payload.EventID); err != nil {
				return err
			}
		}

		if !oneOf(intentPurpose, "product", "wallet_topup", "subscription") || amount == nil || currency == nil || *currency != "USD" || providerPaymentID == nil || paymentStatus == nil {
			return newProviderFailure("payment_response_invalid", 0)
		}
		if intentPurpose == "product" && oneOf(eventType, "refund.succeeded", "refund.failed") {
			if (eventType == "refund.succeeded" && *paymentStatus != "succeeded") || (eventType == "refund.failed" && *paymentStatus != "failed") {
				return newProviderFailure("payment_response_invalid", 0)
			}
			apply, err := recordProductRefundEventTx(ctx, tx, payload.EventID, *paymentID, provider, objectID, *providerPaymentID, int(*amount), *currency, *paymentStatus)
			if err != nil {
				return err
			}
			if !apply {
				if _, err := tx.Exec(ctx, `UPDATE payment_provider_event_processing SET status='processed',processed_at=now(),version=version+1,updated_at=now() WHERE event_id=$1`, payload.EventID); err != nil {
					return err
				}
				return tx.Commit(ctx)
			}
		}
		switch eventType {
		case "checkout.observed":
			if intentPurpose != "product" || *paymentStatus != "paid" {
				return newProviderFailure("payment_response_invalid", 0)
			}
			if err := s.fulfillProductPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, nil); err != nil {
				return err
			}
		case "order.completed", "subscription.activated", "subscription.payment_succeeded":
			if oneOf(eventType, "subscription.activated", "subscription.payment_succeeded") && intentPurpose != "subscription" {
				return newProviderFailure("payment_response_invalid", 0)
			}
			if *paymentStatus != "succeeded" {
				return newProviderFailure("payment_response_invalid", 0)
			}
			var fulfillmentErr error
			switch intentPurpose {
			case "product":
				fulfillmentErr = s.fulfillProductPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, nil)
			case "wallet_topup":
				fulfillmentErr = fulfillWalletTopupPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, nil)
			case "subscription":
				if eventType == "subscription.payment_succeeded" {
					fulfillmentErr = fulfillOrRenewWaffoSubscriptionTx(ctx, tx, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, occurredAt)
				} else {
					fulfillmentErr = fulfillSubscriptionPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, nil)
				}
			}
			if fulfillmentErr != nil {
				return fulfillmentErr
			}
		case "refund.succeeded":
			if intentPurpose != "product" || *paymentStatus != "succeeded" {
				return newProviderFailure("payment_response_invalid", 0)
			}
			if err := refundProductPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, objectID, *providerPaymentID, int(*amount), *currency); err != nil {
				return err
			}
		case "refund.failed":
			if intentPurpose != "product" || *paymentStatus != "failed" {
				return newProviderFailure("payment_response_invalid", 0)
			}
			if err := failProductRefundTx(ctx, tx, provider, payload.EventID, *paymentID, objectID, *providerPaymentID, int(*amount), *currency, *paymentStatus); err != nil {
				return err
			}
		default:
			return newProviderFailure("payment_response_invalid", 0)
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_provider_event_processing SET status='processed',processed_at=now(),version=version+1,updated_at=now() WHERE event_id=$1`, payload.EventID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	switch eventType {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.observed":
		if paymentStatus == nil || *paymentStatus != "paid" || amount == nil || currency == nil || *currency != "USD" || providerPaymentID == nil {
			return newProviderFailure("payment_response_invalid", 0)
		}
		if intentPurpose == "task" {
			if err := fundTaskPaymentTx(ctx, tx, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID); err != nil {
				return err
			}
		} else {
			var fulfillmentErr error
			switch intentPurpose {
			case "product":
				fulfillmentErr = s.fulfillProductPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID)
			case "wallet_topup":
				fulfillmentErr = fulfillWalletTopupPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID)
			case "subscription":
				fulfillmentErr = fulfillSubscriptionPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID)
			default:
				return newProviderFailure("payment_response_invalid", 0)
			}
			if fulfillmentErr != nil {
				return fulfillmentErr
			}
		}
	case "payment_intent.succeeded":
		if paymentStatus == nil || *paymentStatus != "succeeded" || amount == nil || currency == nil || *currency != "USD" || providerPaymentID == nil || providerChargeID == nil {
			return newProviderFailure("payment_response_invalid", 0)
		}
		if intentPurpose == "task" {
			if err := fundTaskPaymentTx(ctx, tx, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID); err != nil {
				return err
			}
		} else {
			var fulfillmentErr error
			switch intentPurpose {
			case "product":
				fulfillmentErr = s.fulfillProductPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID)
			case "wallet_topup":
				fulfillmentErr = fulfillWalletTopupPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID)
			case "subscription":
				fulfillmentErr = fulfillSubscriptionPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID)
			default:
				return newProviderFailure("payment_response_invalid", 0)
			}
			if fulfillmentErr != nil {
				return fulfillmentErr
			}
		}
	case "checkout.session.async_payment_failed", "payment_intent.payment_failed":
		var err error
		if intentPurpose == "task" {
			err = failTaskPaymentTx(ctx, tx, payload.EventID, *paymentID)
		} else if oneOf(intentPurpose, "wallet_topup", "subscription") {
			err = failBillingPaymentTx(ctx, tx, payload.EventID, *paymentID)
		} else {
			err = failProductPaymentTx(ctx, tx, payload.EventID, *paymentID)
		}
		if err != nil {
			return err
		}
	case "refund.updated", "refund.observed":
		if paymentStatus == nil || amount == nil || currency == nil || *currency != "USD" || providerPaymentID == nil || !validStripeID(objectID, "re_") {
			return newProviderFailure("payment_response_invalid", 0)
		}
		if intentPurpose == "product" {
			apply, err := recordProductRefundEventTx(ctx, tx, payload.EventID, *paymentID, provider, objectID, *providerPaymentID, int(*amount), *currency, *paymentStatus)
			if err != nil {
				return err
			}
			if !apply {
				break
			}
		}
		switch *paymentStatus {
		case "succeeded":
			if intentPurpose == "task" {
				err = refundTaskPaymentTx(ctx, tx, payload.EventID, *paymentID, objectID, *providerPaymentID, int(*amount), *currency)
			} else {
				err = refundProductPaymentTx(ctx, tx, provider, payload.EventID, *paymentID, objectID, *providerPaymentID, int(*amount), *currency)
			}
			if err != nil {
				return err
			}
		case "failed", "canceled":
			if intentPurpose == "task" {
				err = failTaskRefundTx(ctx, tx, payload.EventID, *paymentID, objectID, *providerPaymentID, int(*amount), *currency, *paymentStatus)
			} else {
				err = failProductRefundTx(ctx, tx, provider, payload.EventID, *paymentID, objectID, *providerPaymentID, int(*amount), *currency, *paymentStatus)
			}
			if err != nil {
				return err
			}
		case "pending", "requires_action":
			if intentPurpose == "task" {
				err = validateTaskRefundTx(ctx, tx, *paymentID, objectID, *providerPaymentID, int(*amount), *currency)
			} else {
				err = validateProductRefundTx(ctx, tx, provider, *paymentID, objectID, *providerPaymentID, int(*amount), *currency)
			}
			if err != nil {
				return err
			}
		default:
			return newProviderFailure("payment_response_invalid", 0)
		}
	default:
		if _, err := tx.Exec(ctx, `UPDATE payment_provider_event_processing SET status='ignored',processed_at=now(),version=version+1,updated_at=now() WHERE event_id=$1`, payload.EventID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_provider_event_processing SET status='processed',processed_at=now(),version=version+1,updated_at=now() WHERE event_id=$1`, payload.EventID); err != nil {
		return err
	}
	if intentPurpose == "product" && provider == "stripe" && oneOf(eventType, "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.observed", "payment_intent.succeeded") {
		if err := enqueueConfirmedClosedCheckoutCleanupTx(ctx, tx, *paymentID, payload.EventID); err != nil {
			return err
		}
		if err := enqueueFirstRecoveredFundsCheckTx(ctx, tx, *paymentID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func syncPayoutDestinationTx(ctx context.Context, tx pgx.Tx, providerEventID uuid.UUID, destinationID string, destinationUserID *uuid.UUID, chargesEnabled, payoutsEnabled, detailsSubmitted, requirementsDue bool, occurredAt time.Time, identity *ProductCheckoutIdentity) (bool, error) {
	var recordID, userID uuid.UUID
	var oldStatus string
	var adminDisabled bool
	var originalMerchant, originalStore, originalEndpoint, originalAPIVersion, originalRequestVersion *string
	var originalLiveMode *bool
	err := tx.QueryRow(ctx, `
		SELECT id,user_id,status,admin_disabled,original_merchant_id,original_store_id,original_live_mode,original_endpoint,original_api_version,original_request_version FROM payment_destinations
		WHERE provider='stripe' AND destination_id=$1 FOR UPDATE`, destinationID).Scan(&recordID, &userID, &oldStatus, &adminDisabled,
		&originalMerchant, &originalStore, &originalLiveMode, &originalEndpoint, &originalAPIVersion, &originalRequestVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if destinationUserID != nil && *destinationUserID != userID {
		return false, newProviderFailure("payment_response_invalid", 0)
	}
	if originalMerchant != nil {
		if identity == nil || originalLiveMode == nil || originalEndpoint == nil || originalAPIVersion == nil || originalRequestVersion == nil {
			return false, newProviderFailure("payment_reconciliation_required", 0)
		}
		store := ""
		if originalStore != nil {
			store = *originalStore
		}
		if *originalMerchant != identity.MerchantID || store != identity.StoreID || *originalLiveMode != identity.LiveMode ||
			*originalEndpoint != identity.Endpoint || *originalAPIVersion != identity.APIVersion || *originalRequestVersion != identity.RequestVersion {
			return false, newProviderFailure("payment_reconciliation_required", 0)
		}
	}
	// Delivery order is not provider order. Immutable processed evidence also
	// supplies a watermark for destinations created before this protection.
	var latestAt time.Time
	var priorCharges, priorPayouts, priorDetails, priorDue bool
	err = tx.QueryRow(ctx, `
		WITH prior AS NOT MATERIALIZED (
		  SELECT e.occurred_at,e.account_charges_enabled,e.account_payouts_enabled,
		    e.account_details_submitted,e.account_requirements_due
		  FROM payment_provider_events e JOIN payment_provider_event_processing p ON p.event_id=e.id
		  WHERE e.provider='stripe' AND e.event_type='account.updated' AND e.destination_id=$1
		    AND (e.destination_user_id IS NULL OR e.destination_user_id=$2) AND p.status='processed'
		), latest AS (SELECT occurred_at FROM prior ORDER BY occurred_at DESC LIMIT 1)
		SELECT latest.occurred_at,bool_and(COALESCE(prior.account_charges_enabled,false)),
		  bool_and(COALESCE(prior.account_payouts_enabled,false)),
		  bool_and(COALESCE(prior.account_details_submitted,false)),
		  bool_or(COALESCE(prior.account_requirements_due,true))
		FROM latest JOIN prior USING(occurred_at) GROUP BY latest.occurred_at`, destinationID, userID).Scan(
		&latestAt, &priorCharges, &priorPayouts, &priorDetails, &priorDue)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err == nil && occurredAt.Before(latestAt) {
		return false, nil
	}
	mergedSameSecond := err == nil && occurredAt.Equal(latestAt)
	if mergedSameSecond {
		// Stripe timestamps have second precision; conflicting events in the
		// same second cannot prove a later grant. Preserve every restriction.
		chargesEnabled = chargesEnabled && priorCharges
		payoutsEnabled = payoutsEnabled && priorPayouts
		detailsSubmitted = detailsSubmitted && priorDetails
		requirementsDue = requirementsDue || priorDue
	}
	newStatus := payoutStatusFromEvidence(chargesEnabled, payoutsEnabled, detailsSubmitted, requirementsDue, adminDisabled)
	if _, err := tx.Exec(ctx, `
		UPDATE payment_destinations SET account_type='express',status=$2,charges_enabled=$3,payouts_enabled=$4,
		  details_submitted=$5,requirements_due=$6,
		  verified_at=CASE WHEN $2='verified' THEN COALESCE(verified_at,$7) ELSE NULL END,
		  onboarding_completed_at=CASE WHEN $5 THEN COALESCE(onboarding_completed_at,$7) ELSE onboarding_completed_at END,
		  version=version+1,updated_at=now() WHERE id=$1`,
		recordID, newStatus, chargesEnabled, payoutsEnabled, detailsSubmitted, requirementsDue, occurredAt); err != nil {
		return false, err
	}
	metadata, err := json.Marshal(map[string]any{
		"provider": "stripe", "providerEventId": providerEventID, "previousStatus": oldStatus, "status": newStatus,
		"occurredAt": occurredAt, "mergedSameSecond": mergedSameSecond,
		"chargesEnabled": chargesEnabled, "payoutsEnabled": payoutsEnabled, "detailsSubmitted": detailsSubmitted, "requirementsDue": requirementsDue,
	})
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata)
		VALUES('payment.payout_destination_synced','payment_destination',$1,'Signed Stripe account.updated evidence applied',$2,$3)`,
		recordID, "stripe-event:"+providerEventID.String(), metadata); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) HandleTaskTransferJob(ctx context.Context, job jobs.Job) error {
	var payload taskTransferJobPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.PaymentID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if s == nil || s.pool == nil || !s.config.Enabled {
		return ErrDisabled
	}
	var provider string
	if err := s.pool.QueryRow(ctx, `SELECT provider FROM payment_intents WHERE id=$1 AND purpose='task'`, payload.PaymentID).Scan(&provider); errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_invalid_request", 0)
	} else if err != nil {
		return err
	}
	runtime, err := s.runtimes.Runtime(provider)
	if err != nil {
		return err
	}
	if !runtimeCapabilities(runtime).Transfer {
		return newProviderFailure("payment_provider_unsupported", 0)
	}
	currentIdentity, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, currency, title string
	var payeeID *uuid.UUID
	var taskID uuid.UUID
	var amount int
	var providerChargeID, providerTransferID *string
	if err := tx.QueryRow(ctx, `
		SELECT pi.status,pi.payee_id,pi.resource_id,pi.amount_cents,pi.currency,pi.provider_charge_id,pi.provider_transfer_id,d.title
		FROM payment_intents pi JOIN demands d ON d.id=pi.resource_id
		WHERE pi.id=$1 AND pi.purpose='task' FOR UPDATE OF pi`, payload.PaymentID).Scan(
		&status, &payeeID, &taskID, &amount, &currency, &providerChargeID, &providerTransferID, &title); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return newProviderFailure("payment_invalid_request", 0)
		}
		return err
	}
	if status == "transferred" && providerTransferID != nil {
		return tx.Commit(ctx)
	}
	if status == "recovery_required" {
		return tx.Commit(ctx)
	}
	if status != "transfer_pending" || payeeID == nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if providerChargeID == nil {
		return newProviderFailure("payment_request_failed", 30*time.Second)
	}
	originalIdentity, hasIdentity, identityErr := readTaskPaymentIdentityTx(ctx, tx, payload.PaymentID)
	if identityErr != nil || !hasIdentity || !taskPaymentIdentityMatches(provider, originalIdentity, currentIdentity) {
		if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='recovery_required',updated_at=now(),version=version+1 WHERE id=$1 AND status='transfer_pending'`, payload.PaymentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
			VALUES($1,'transfer.reconciliation_required','transfer_pending','recovery_required',jsonb_build_object('reason',$2::text))`, payload.PaymentID, recoveryReason(identityErr, hasIdentity)); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return newProviderFailure("payment_reconciliation_required", 0)
	}
	var destinationID string
	if err := tx.QueryRow(ctx, `
		SELECT destination_id FROM payment_destinations
		WHERE provider=$1 AND user_id=$2 AND status='verified' AND charges_enabled AND payouts_enabled
		FOR SHARE`, provider, *payeeID).Scan(&destinationID); errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_provider_unavailable", 5*time.Minute)
	} else if err != nil {
		return err
	}
	matched, err := destinationIdentityMatchesTx(ctx, tx, provider, *payeeID, destinationID, currentIdentity)
	if err != nil {
		return err
	}
	if !matched {
		if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='recovery_required',updated_at=now(),version=version+1 WHERE id=$1 AND status='transfer_pending'`, payload.PaymentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
			VALUES($1,'transfer.reconciliation_required','transfer_pending','recovery_required',jsonb_build_object('reason','destination_identity_mismatch','destinationId',$2::text))`, payload.PaymentID, destinationID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return newProviderFailure("payment_reconciliation_required", 0)
	}
	// Keep the payment and destination locks until the remote idempotent
	// command returns. A capability update cannot race this identity check.
	transfer, err := runtime.CreateTransfer(ctx, TransferRequest{
		PaymentID: payload.PaymentID, ProviderChargeID: *providerChargeID, DestinationID: destinationID, AmountCents: amount, Currency: currency,
	})
	if err != nil {
		return SanitizeProviderError(err)
	}
	if strings.TrimSpace(transfer.ProviderID) == "" || transfer.DestinationID != destinationID || transfer.AmountCents != amount || transfer.Currency != currency {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='transferred',provider_transfer_id=$2,destination_id=$3,transferred_at=now(),updated_at=now(),version=version+1
		WHERE id=$1`, payload.PaymentID, transfer.ProviderID, destinationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE task_settlements SET mode='provider_transferred' WHERE demand_id=$1 AND mode='provider_pending'`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
		VALUES($1,'transfer.completed','transfer_pending','transferred',jsonb_build_object('taskId',$2::text,'destinationId',$3::text))`, payload.PaymentID, taskID, destinationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events(demand_id,actor_id,kind,from_status,to_status,note,metadata)
		VALUES($1,$2,'payout_transferred','accepted','accepted','Verified Provider transfer created.',jsonb_build_object('paymentId',$3::text,'amountCents',$4::integer,'currency',$5::text))`, taskID, *payeeID, payload.PaymentID, amount, currency); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'task.provider_transfer','task',$2,$3,jsonb_build_object('paymentId',$4::text,'provider',$5::text,'amountCents',$6::integer,'currency',$7::text))`,
		*payeeID, taskID, provider+"-transfer:"+transfer.ProviderID, payload.PaymentID, provider, amount, currency); err != nil {
		return err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: *payeeID, Kind: "task.payout_transferred", Title: "Task payout transferred",
		Body: "The verified Provider transfer for \u201c" + title + "\u201d was created.", TargetPath: "/market/demands/" + taskID.String(),
		ResourceType: "task", ResourceID: &taskID, SourceKey: "task:" + taskID.String() + ":transferred",
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func recoveryReason(identityErr error, hasIdentity bool) string {
	if identityErr != nil {
		return "identity_snapshot_incomplete"
	}
	if !hasIdentity {
		return "identity_snapshot_missing"
	}
	return "provider_identity_mismatch"
}

func (s *Service) HandleTaskRefundJob(ctx context.Context, job jobs.Job) error {
	var payload taskRefundJobPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.PaymentID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if s == nil || s.pool == nil || !s.config.Enabled {
		return ErrDisabled
	}
	var provider, status, providerPaymentID string
	var providerRefundID *string
	var operationID *uuid.UUID
	var amount int
	err := s.pool.QueryRow(ctx, `
		SELECT provider,status,provider_payment_id,provider_refund_id,refund_operation_id,amount_cents
		FROM payment_intents WHERE id=$1 AND purpose='task'`, payload.PaymentID).Scan(
		&provider, &status, &providerPaymentID, &providerRefundID, &operationID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if err != nil {
		return err
	}
	runtime, err := s.runtimes.Runtime(provider)
	if err != nil {
		return err
	}
	if !runtimeCapabilities(runtime).Refund {
		return newProviderFailure("payment_provider_unsupported", 0)
	}
	if status == "refunded" || (status == "refund_pending" && providerRefundID != nil) {
		return nil
	}
	if status != "refund_pending" || operationID == nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	currentIdentity, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	originalIdentity, hasIdentity, identityErr := readTaskPaymentIdentityTx(ctx, tx, payload.PaymentID)
	if identityErr != nil || !hasIdentity || !taskPaymentIdentityMatches(provider, originalIdentity, currentIdentity) {
		if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='recovery_required',updated_at=now(),version=version+1 WHERE id=$1 AND status='refund_pending'`, payload.PaymentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
			VALUES($1,'refund.reconciliation_required','refund_pending','recovery_required',jsonb_build_object('reason',$2::text))`, payload.PaymentID, recoveryReason(identityErr, hasIdentity)); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return newProviderFailure("payment_reconciliation_required", 0)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	refund, err := runtime.CreateRefund(ctx, RefundRequest{
		PaymentID: payload.PaymentID, OperationID: *operationID, ProviderPaymentID: providerPaymentID, AmountCents: amount,
	})
	if err != nil {
		return SanitizeProviderError(err)
	}
	result, err := s.pool.Exec(ctx, `
		UPDATE payment_intents SET provider_refund_id=$2,updated_at=now(),version=version+1
		WHERE id=$1 AND purpose='task' AND status='refund_pending' AND refund_operation_id=$3
		  AND (provider_refund_id IS NULL OR provider_refund_id=$2)`, payload.PaymentID, refund.ProviderID, operationID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return newProviderFailure("payment_response_invalid", 0)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
		VALUES($1,'refund.provider_requested','refund_pending','refund_pending',jsonb_build_object('providerStatus',$2::text,'providerRefundId',$3::text))`,
		payload.PaymentID, refund.Status, refund.ProviderID)
	return err
}

func (s *Service) HandleProductRefundJob(ctx context.Context, job jobs.Job) error {
	var payload productRefundJobPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.PaymentID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if s == nil || s.pool == nil || !s.config.Enabled {
		return ErrDisabled
	}
	var status, providerPaymentID, provider string
	var providerRefundID *string
	var operationID *uuid.UUID
	var requestedAt *time.Time
	var amount int
	err := s.pool.QueryRow(ctx, `
		SELECT pi.status,pi.provider,pi.provider_payment_id,pi.provider_refund_id,o.refund_operation_id,pi.amount_cents,o.refund_requested_at
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
		WHERE pi.id=$1 AND pi.purpose='product'`, payload.PaymentID).Scan(
		&status, &provider, &providerPaymentID, &providerRefundID, &operationID, &amount, &requestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if err != nil {
		return err
	}
	// A delayed job must never dispatch a later refund operation.
	if payload.OperationID != uuid.Nil && (operationID == nil || *operationID != payload.OperationID) {
		return nil
	}
	if operationID != nil && oneOf(status, "paid", "refund_failed") {
		return nil
	}
	if status == "refunded" || (status == "refund_pending" && providerRefundID != nil) {
		return nil
	}
	if status != "refund_pending" || operationID == nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if payload.OperationID == uuid.Nil {
		// Legacy queue entries predate operation IDs. Only a persisted job
		// created during this refund attempt can dispatch it.
		var createdAt time.Time
		err := s.pool.QueryRow(ctx, `SELECT created_at FROM jobs
			WHERE id=$1 AND kind=$2 AND payload->>'paymentId'=$3`, job.ID, ProductRefundJobKind, payload.PaymentID.String()).Scan(&createdAt)
		if errors.Is(err, pgx.ErrNoRows) || requestedAt == nil {
			return newProviderFailure("payment_invalid_request", 0)
		}
		if err != nil {
			return err
		}
		if createdAt.Before(*requestedAt) {
			return nil
		}
	}
	runtime, err := s.runtimes.Runtime(provider)
	if err != nil {
		return err
	}
	_, err = s.createProviderRefund(ctx, runtime, payload.PaymentID, *operationID, providerPaymentID, amount)
	return err
}

func (s *Service) fulfillProductPaymentTx(ctx context.Context, tx pgx.Tx, provider string, providerEventID, paymentID uuid.UUID, amount int, currency, providerPaymentID string, providerChargeID *string) error {
	var intentStatus, purpose, orderStatus string
	var expectedPaymentID, expectedChargeID, compensationReason *string
	var payerID, productID, orderID uuid.UUID
	var expectedAmount int
	var expectedCurrency, title, licenseCode, sourceTitle, mediaURL, mimeType, kind string
	var sourceAssetID, rootOriginID uuid.UUID
	var width, height *int
	var deliveredOrigin *uuid.UUID
	var deliveredSize *int64
	// Serialize against new checkouts before locking the payment/order rows.
	if err := tx.QueryRow(ctx, `SELECT payer_id,resource_id FROM payment_intents WHERE id=$1 AND purpose='product'`, paymentID).Scan(&payerID, &productID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return newProviderFailure("payment_response_invalid", 0)
		}
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "product-checkout:"+payerID.String()+":"+productID.String()); err != nil {
		return err
	}
	err := tx.QueryRow(ctx, `
		SELECT pi.status,pi.purpose,pi.payer_id,pi.resource_id,pi.order_id,pi.amount_cents,pi.currency,o.status,o.product_title_snapshot,
		       pi.provider_payment_id,pi.provider_charge_id,pi.compensation_reason
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
		WHERE pi.id=$1 FOR UPDATE OF pi,o`, paymentID).Scan(
		&intentStatus, &purpose, &payerID, &productID, &orderID, &expectedAmount, &expectedCurrency, &orderStatus, &title,
		&expectedPaymentID, &expectedChargeID, &compensationReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if err != nil {
		return err
	}
	if purpose != "product" || amount != expectedAmount || currency != expectedCurrency || providerPaymentID == "" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	// A different authenticated receipt may have arrived after this event was
	// queued. Keep both records and require reconciliation before rights or
	// compensation can be created, including on event replay.
	if err := validatePaidCheckoutEvidenceAgreement(ctx, tx, paymentID); err != nil {
		return err
	}
	if err := validateClosedCheckoutFundsTx(ctx, tx, paymentID); err != nil {
		return err
	}
	if (expectedPaymentID != nil && *expectedPaymentID != providerPaymentID) ||
		(expectedChargeID != nil && providerChargeID != nil && *expectedChargeID != *providerChargeID) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if oneOf(intentStatus, "paid", "refund_pending", "refund_failed", "refunded") {
		validOrder := (intentStatus == "paid" && orderStatus == "fulfilled") ||
			(intentStatus == "refund_pending" && orderStatus == "refund_requested") ||
			(intentStatus == "refund_failed" && compensationReason != nil && orderStatus == "refund_requested") ||
			(intentStatus == "refunded" && orderStatus == "refunded")
		if !validOrder {
			return newProviderFailure("payment_response_invalid", 0)
		}
		result, err := tx.Exec(ctx, `
			UPDATE payment_intents SET provider_charge_id=COALESCE(provider_charge_id,$2),updated_at=now()
			WHERE id=$1 AND provider_payment_id=$3 AND (provider_charge_id IS NULL OR provider_charge_id=$2 OR $2::text IS NULL)`, paymentID, providerChargeID, providerPaymentID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return newProviderFailure("payment_response_invalid", 0)
		}
		if intentStatus == "paid" && orderStatus == "fulfilled" {
			if err := s.ensureProductSettlementTx(ctx, tx, orderID, paymentID); err != nil {
				return err
			}
		}
		return nil
	}
	compensate := func(reason string) error {
		return compensateProductPaymentTx(ctx, tx, provider, providerEventID, paymentID, orderID, payerID, intentStatus, orderStatus, reason, providerPaymentID, providerChargeID)
	}
	if oneOf(intentStatus, "cancelled", "payment_failed") && oneOf(orderStatus, "cancelled", "payment_failed", "payment_pending") {
		return compensate("checkout_closed")
	}
	if !oneOf(intentStatus, "checkout_pending", "checkout_open") || orderStatus != "payment_pending" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	var buyerStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR SHARE`, payerID).Scan(&buyerStatus); err != nil {
		return err
	}
	if buyerStatus != "active" {
		return compensate("buyer_unavailable")
	}
	var owned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM entitlements WHERE user_id=$1 AND product_id=$2 AND status='active')`, payerID, productID).Scan(&owned); err != nil {
		return err
	}
	if owned {
		return compensate("already_owned")
	}
	// A repeated confirmation must not redeliver or depend on later moderation.
	// New fulfillment alone resolves the accepted contract and checks source safety.
	if err := productdelivery.LockSources(ctx, tx, orderID); err != nil {
		return err
	}
	var sourceScan, rootScan string
	err = tx.QueryRow(ctx, `
		SELECT c.contract->'license'->>'code',c.source_asset_id,c.root_asset_id,c.contract->'asset'->>'title',
		       '/api/v1/assets/',c.contract->'asset'->>'mimeType',c.contract->'asset'->>'kind',
		       (c.contract->'asset'->>'width')::int,(c.contract->'asset'->>'height')::int,a.scan_status,root.scan_status
		FROM product_order_contracts c
		JOIN assets a ON a.id=c.source_asset_id
		JOIN assets root ON root.id=c.root_asset_id
		WHERE c.order_id=$1`, orderID).Scan(
		&licenseCode, &sourceAssetID, &rootOriginID, &sourceTitle, &mediaURL, &mimeType, &kind, &width, &height, &sourceScan, &rootScan)
	if errors.Is(err, pgx.ErrNoRows) {
		return compensate("contract_unavailable")
	}
	if err != nil {
		return err
	}
	if sourceScan != "clean" || rootScan != "clean" {
		return compensate("source_unavailable")
	}
	clean, err := productdelivery.SourcesClean(ctx, tx, orderID)
	if err != nil {
		return err
	}
	if !clean {
		return compensate("source_unavailable")
	}
	deliveredOrigin = &rootOriginID
	var required bool
	if err := tx.QueryRow(ctx, `SELECT delivery_snapshot_required FROM orders WHERE id=$1`, orderID).Scan(&required); err != nil {
		return err
	}
	if required {
		snapshot, err := productdelivery.Load(ctx, tx, orderID)
		if errors.Is(err, pgx.ErrNoRows) {
			return compensate("source_unavailable")
		}
		if err != nil {
			return err
		}
		object, err := snapshot.Open(ctx, s.config.MediaStores, nil)
		if errors.Is(err, media.ErrNotFound) || errors.Is(err, media.ErrIntegrity) || errors.Is(err, productdelivery.ErrUnavailable) {
			return compensate("source_unavailable")
		}
		if err != nil {
			return err
		}
		if err = object.Body.Close(); err != nil {
			return err
		}
		deliveredSize = &snapshot.Size
		if snapshot.Format == productdelivery.FormatZIPV1 {
			// The purchase represents the full package, not its first image.
			// Accepted member provenance remains in the immutable contract.
			kind, mimeType, sourceTitle = "document", productdelivery.BundleMIME, title
			width, height, deliveredOrigin = nil, nil, nil
		}
	}
	assetID, entitlementID := uuid.New(), uuid.New()
	if strings.HasPrefix(mediaURL, "/api/v1/assets/") {
		mediaURL = "/api/v1/assets/" + assetID.String() + "/content"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,source_id,license_code,origin_asset_id,size_bytes)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'clean','purchase',$9,$10,$11,$12)`,
		assetID, payerID, kind, sourceTitle, mediaURL, mimeType, width, height, orderID, licenseCode, deliveredOrigin, deliveredSize); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO entitlements(id,user_id,product_id,order_id,asset_id,license_code,status)
		VALUES($1,$2,$3,$4,$5,$6,'active')`, entitlementID, payerID, productID, orderID, assetID, licenseCode); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='fulfilled',updated_at=now() WHERE id=$1`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events(order_id,from_status,to_status,reason,sequence)
		VALUES($1,'payment_pending','payment_paid','Verified Provider payment confirmed.',2),
		      ($1,'payment_paid','fulfilled','Entitlement and purchased Asset granted after verified payment.',3)`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='paid',provider_payment_id=$2,provider_charge_id=COALESCE($3,provider_charge_id),paid_at=now(),updated_at=now(),version=version+1
		WHERE id=$1`, paymentID, providerPaymentID, providerChargeID); err != nil {
		return err
	}
	if err := s.ensureProductSettlementTx(ctx, tx, orderID, paymentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'payment.confirmed',$3,'paid',jsonb_build_object('orderId',$4::text,'assetId',$5::text))
		ON CONFLICT DO NOTHING`, paymentID, providerEventID, intentStatus, orderID, assetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'marketplace.provider_purchase','order',$2,$3,jsonb_build_object('paymentId',$4::text,'provider',$5::text,'realCharge',(SELECT live_mode FROM payment_intents WHERE id=$4::uuid)))`,
		payerID, orderID, providerEventRequestID(provider, providerEventID), paymentID, provider); err != nil {
		return err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: payerID, Kind: "marketplace.order_fulfilled", Title: "Purchase ready",
		Body:       "\u201c" + title + "\u201d is now available in Assets under the accepted license.",
		TargetPath: "/workspace/assets/" + assetID.String(), ResourceType: "order", ResourceID: &orderID,
		SourceKey: "marketplace:order:" + orderID.String() + ":fulfilled",
	}); err != nil {
		return err
	}
	return webhooks.EnqueueTx(ctx, tx, webhooks.EventInput{OwnerID: payerID, EventType: "marketplace.order.fulfilled", ResourceType: "order", ResourceID: &orderID, SourceKey: "marketplace:order:" + orderID.String() + ":fulfilled"})
}

func fundTaskPaymentTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID, amount int, currency, providerPaymentID string, providerChargeID *string) error {
	var intentStatus, expectedCurrency, taskStatus, title string
	var payerID, taskID uuid.UUID
	var expectedAmount int
	var expectedProviderPaymentID *string
	err := tx.QueryRow(ctx, `
		SELECT pi.status,pi.payer_id,pi.resource_id,pi.amount_cents,pi.currency,pi.provider_payment_id,d.status,d.title
		FROM payment_intents pi JOIN demands d ON d.id=pi.resource_id
		WHERE pi.id=$1 AND pi.purpose='task' FOR UPDATE OF pi,d`, paymentID).Scan(
		&intentStatus, &payerID, &taskID, &expectedAmount, &expectedCurrency, &expectedProviderPaymentID, &taskStatus, &title)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if err != nil {
		return err
	}
	if amount != expectedAmount || currency != expectedCurrency {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if oneOf(intentStatus, "paid", "transfer_pending", "transferred", "refund_pending", "refund_failed", "refunded") {
		if expectedProviderPaymentID == nil || *expectedProviderPaymentID != providerPaymentID {
			return newProviderFailure("payment_response_invalid", 0)
		}
		result, err := tx.Exec(ctx, `
			UPDATE payment_intents SET provider_charge_id=COALESCE(provider_charge_id,$2),updated_at=now()
			WHERE id=$1 AND (provider_charge_id IS NULL OR provider_charge_id=$2 OR $2::text IS NULL)`, paymentID, providerChargeID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return newProviderFailure("payment_response_invalid", 0)
		}
		return nil
	}
	if intentStatus == "cancelled" && taskStatus == "cancelled" {
		operationID := uuid.New()
		if _, err := tx.Exec(ctx, `
			UPDATE payment_intents SET status='refund_pending',provider_payment_id=$2,provider_charge_id=COALESCE($3,provider_charge_id),
			  refund_operation_id=$4,paid_at=now(),updated_at=now(),version=version+1 WHERE id=$1`,
			paymentID, providerPaymentID, providerChargeID, operationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
			VALUES($1,$2,'payment.confirmed_after_cancellation','cancelled','refund_pending',jsonb_build_object('taskId',$3::text))
			ON CONFLICT DO NOTHING`, paymentID, providerEventID, taskID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_events(demand_id,actor_id,kind,from_status,to_status,note,metadata)
			VALUES($1,$2,'funding_refund_requested','cancelled','cancelled','Late Provider payment received after cancellation; refund queued.',jsonb_build_object('paymentId',$3::text))`,
			taskID, payerID, paymentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('paymentId',$2::text),20)`, TaskRefundJobKind, paymentID); err != nil {
			return err
		}
		return notifications.CreateTx(ctx, tx, notifications.CreateInput{
			UserID: payerID, Kind: "task.refund_requested", Title: "Task refund requested",
			Body: "Payment for the cancelled task \u201c" + title + "\u201d was received and queued for a Provider refund.", TargetPath: "/market/demands/" + taskID.String(),
			ResourceType: "task", ResourceID: &taskID, SourceKey: "task:" + taskID.String() + ":late-payment-refund",
		})
	}
	if !oneOf(intentStatus, "checkout_pending", "checkout_open") || taskStatus != "open" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='paid',provider_payment_id=$2,provider_charge_id=COALESCE($3,provider_charge_id),paid_at=now(),updated_at=now(),version=version+1
		WHERE id=$1`, paymentID, providerPaymentID, providerChargeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'payment.confirmed',$3,'paid',jsonb_build_object('taskId',$4::text)) ON CONFLICT DO NOTHING`, paymentID, providerEventID, intentStatus, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events(demand_id,actor_id,kind,from_status,to_status,note,metadata)
		VALUES($1,$2,'funding_confirmed','open','open','Signed Provider funding confirmed.',jsonb_build_object('paymentId',$3::text,'amountCents',$4::integer,'currency',$5::text))`, taskID, payerID, paymentID, amount, currency); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'task.provider_funded','task',$2,$3,jsonb_build_object('paymentId',$4::uuid::text,'provider',(SELECT provider FROM payment_intents WHERE id=$4::uuid),'liveMode',(SELECT live_mode FROM payment_intents WHERE id=$4::uuid)))`,
		payerID, taskID, "stripe-event:"+providerEventID.String(), paymentID); err != nil {
		return err
	}
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: payerID, Kind: "task.funding_confirmed", Title: "Task funding confirmed",
		Body: "Secure funding for \u201c" + title + "\u201d was confirmed. You can now assign the funded brief.", TargetPath: "/market/demands/" + taskID.String(),
		ResourceType: "task", ResourceID: &taskID, SourceKey: "task:" + taskID.String() + ":funded",
	})
}

func failTaskPaymentTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID) error {
	var status string
	var taskID, payerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT status,resource_id,payer_id FROM payment_intents WHERE id=$1 AND purpose='task' FOR UPDATE`, paymentID).Scan(&status, &taskID, &payerID); err != nil {
		return err
	}
	if status == "payment_failed" || status == "cancelled" {
		return nil
	}
	if !oneOf(status, "checkout_pending", "checkout_open") {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='payment_failed',updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status)
		VALUES($1,$2,'payment.failed',$3,'payment_failed') ON CONFLICT DO NOTHING`, paymentID, providerEventID, status); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO task_events(demand_id,actor_id,kind,from_status,to_status,note,metadata)
		VALUES($1,$2,'funding_failed','open','open','Signed Provider funding failure received.',jsonb_build_object('paymentId',$3::text))`, taskID, payerID, paymentID)
	return err
}

// A failed payment attempt does not close a Stripe Checkout Session: the buyer
// may still retry there. Preserve its active slot until an authenticated read
// proves closure, and do not let a late failure regress confirmed fulfillment.
func failProductPaymentTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID) error {
	var status string
	var expiresAt *time.Time
	var checkoutID *string
	if err := tx.QueryRow(ctx, `SELECT status,checkout_expires_at,provider_checkout_id FROM payment_intents WHERE id=$1 AND purpose='product' AND provider='stripe' FOR UPDATE`, paymentID).Scan(&status, &expiresAt, &checkoutID); err != nil {
		return err
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_provider_events e JOIN payment_intents p ON p.id=e.payment_id
 WHERE e.id=$1 AND p.id=$2 AND e.evidence_source='webhook' AND e.provider=p.provider AND e.live_mode=p.live_mode
 AND e.resource_id=p.resource_id AND e.amount_cents=p.amount_cents AND e.currency=p.currency
 AND ((e.event_type='checkout.session.async_payment_failed' AND e.object_id=p.provider_checkout_id AND e.payment_status='unpaid')
   OR (e.event_type='payment_intent.payment_failed' AND e.object_id=e.provider_payment_id))
 AND (p.provider_payment_id IS NULL OR p.provider_payment_id=e.provider_payment_id))`, providerEventID, paymentID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
 VALUES($1,$2,'payment.attempt_failed',$3,$3,'{"checkoutClosureVerified":false}') ON CONFLICT DO NOTHING`, paymentID, providerEventID, status); err != nil {
		return err
	}
	if status == "checkout_open" && checkoutID != nil && expiresAt != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts,available_at)
 SELECT $1,jsonb_build_object('paymentId',$2::text),20,GREATEST(now(),$3::timestamptz+interval '5 seconds')
 WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2::text AND status IN ('queued','running'))
 ON CONFLICT DO NOTHING`, ProductCheckoutCheckJobKind, paymentID, *expiresAt); err != nil {
			return err
		}
	}
	return nil
}

func validateTaskRefundTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID, providerRefundID, providerPaymentID string, amount int, currency string) error {
	var intentStatus, expectedProviderPaymentID, expectedProviderRefundID, expectedCurrency, taskStatus string
	var expectedAmount int
	err := tx.QueryRow(ctx, `
		SELECT pi.status,pi.provider_payment_id,pi.provider_refund_id,pi.amount_cents,pi.currency,d.status
		FROM payment_intents pi JOIN demands d ON d.id=pi.resource_id
		WHERE pi.id=$1 AND pi.purpose='task' FOR UPDATE OF pi,d`, paymentID).Scan(
		&intentStatus, &expectedProviderPaymentID, &expectedProviderRefundID, &expectedAmount, &expectedCurrency, &taskStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if err != nil {
		return err
	}
	if intentStatus != "refund_pending" || taskStatus != "cancelled" || expectedProviderPaymentID != providerPaymentID ||
		expectedProviderRefundID != providerRefundID || expectedAmount != amount || expectedCurrency != currency {
		return newProviderFailure("payment_response_invalid", 0)
	}
	return nil
}

func refundTaskPaymentTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID, providerRefundID, providerPaymentID string, amount int, currency string) error {
	var currentStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM payment_intents WHERE id=$1 AND purpose='task' FOR UPDATE`, paymentID).Scan(&currentStatus); err != nil {
		return err
	}
	if currentStatus == "refunded" {
		return nil
	}
	if err := validateTaskRefundTx(ctx, tx, paymentID, providerRefundID, providerPaymentID, amount, currency); err != nil {
		return err
	}
	var taskID, payerID uuid.UUID
	var title string
	if err := tx.QueryRow(ctx, `
		SELECT pi.resource_id,pi.payer_id,d.title FROM payment_intents pi JOIN demands d ON d.id=pi.resource_id WHERE pi.id=$1`, paymentID).Scan(&taskID, &payerID, &title); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='refunded',refunded_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'refund.confirmed','refund_pending','refunded',jsonb_build_object('taskId',$3::text)) ON CONFLICT DO NOTHING`,
		paymentID, providerEventID, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events(demand_id,actor_id,kind,from_status,to_status,note,metadata)
		VALUES($1,$2,'funding_refunded','cancelled','cancelled','Provider refund confirmed.',jsonb_build_object('paymentId',$3::text,'amountCents',$4::integer,'currency',$5::text))`,
		taskID, payerID, paymentID, amount, currency); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'task.provider_refund','task',$2,$3,jsonb_build_object('paymentId',$4::text,'provider',(SELECT provider FROM payment_intents WHERE id=$4::uuid),'amountCents',$5::integer,'currency',$6::text))`,
		payerID, taskID, "stripe-event:"+providerEventID.String(), paymentID, amount, currency); err != nil {
		return err
	}
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: payerID, Kind: "task.refunded", Title: "Task funding refunded",
		Body: "The Provider refund for \u201c" + title + "\u201d was confirmed.", TargetPath: "/market/demands/" + taskID.String(),
		ResourceType: "task", ResourceID: &taskID, SourceKey: "task:" + taskID.String() + ":refunded",
	})
}

func failTaskRefundTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID, providerRefundID, providerPaymentID string, amount int, currency, providerStatus string) error {
	if err := validateTaskRefundTx(ctx, tx, paymentID, providerRefundID, providerPaymentID, amount, currency); err != nil {
		return err
	}
	var taskID, payerID uuid.UUID
	var title string
	if err := tx.QueryRow(ctx, `
		SELECT pi.resource_id,pi.payer_id,d.title FROM payment_intents pi JOIN demands d ON d.id=pi.resource_id WHERE pi.id=$1`, paymentID).Scan(&taskID, &payerID, &title); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='refund_failed',updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'refund.failed','refund_pending','refund_failed',jsonb_build_object('taskId',$3::text,'providerStatus',$4::text)) ON CONFLICT DO NOTHING`,
		paymentID, providerEventID, taskID, providerStatus); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events(demand_id,actor_id,kind,from_status,to_status,note,metadata)
		VALUES($1,$2,'funding_refund_failed','cancelled','cancelled','Provider refund did not complete; operations review required.',jsonb_build_object('paymentId',$3::text,'providerStatus',$4::text))`,
		taskID, payerID, paymentID, providerStatus); err != nil {
		return err
	}
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: payerID, Kind: "task.refund_failed", Title: "Task refund needs review",
		Body: "The Provider did not complete the refund for \u201c" + title + "\u201d. Operations review is required.", TargetPath: "/market/demands/" + taskID.String(),
		ResourceType: "task", ResourceID: &taskID, SourceKey: "task:" + taskID.String() + ":refund-failed:" + providerEventID.String(),
	})
}

func refundProductPaymentTx(ctx context.Context, tx pgx.Tx, provider string, providerEventID, paymentID uuid.UUID, providerRefundID, providerPaymentID string, amount int, currency string) error {
	var intentStatus, orderStatus, expectedProviderPaymentID, expectedCurrency, title string
	var expectedProviderRefundID, compensationReason *string
	var orderID, buyerID uuid.UUID
	var assetID *uuid.UUID
	var expectedAmount int
	err := tx.QueryRow(ctx, `
		SELECT pi.status,pi.provider_payment_id,pi.provider_refund_id,pi.amount_cents,pi.currency,pi.order_id,pi.payer_id,o.status,o.product_title_snapshot,pi.compensation_reason
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
		WHERE pi.id=$1 AND pi.purpose='product' FOR UPDATE OF pi,o`, paymentID).Scan(
		&intentStatus, &expectedProviderPaymentID, &expectedProviderRefundID, &expectedAmount, &expectedCurrency, &orderID, &buyerID, &orderStatus, &title, &compensationReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if err != nil {
		return err
	}
	refundIDMatches := expectedProviderRefundID != nil && *expectedProviderRefundID == providerRefundID
	if provider == "waffo_pancake" {
		// Waffo refund Webhooks expose the merchant external ticket ID. That is
		// our refund_operation_id; the Provider ticket ID is intentionally not
		// trusted from an unsigned payload field.
		var operationID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT operation_id FROM product_refund_attempts WHERE payment_id=$1 AND operation_id::text=$2 AND status='succeeded'`, paymentID, providerRefundID).Scan(&operationID); err != nil {
			return err
		}
		refundIDMatches = operationID != nil && operationID.String() == providerRefundID
	}
	if expectedProviderPaymentID != providerPaymentID || !refundIDMatches || expectedAmount != amount || expectedCurrency != currency {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if intentStatus == "refunded" && orderStatus == "refunded" {
		return nil
	}
	historicalConfirmation := false
	if provider == "stripe" && oneOf(intentStatus, "cancelled", "payment_failed") && oneOf(orderStatus, "cancelled", "payment_failed", "payment_pending") {
		historicalConfirmation, err = closedCheckoutRefundConfirmedTx(ctx, tx, paymentID, providerEventID)
		if err != nil {
			return err
		}
	}
	if !historicalConfirmation && (!oneOf(intentStatus, "refund_pending", "refund_failed", "paid") || !oneOf(orderStatus, "refund_requested", "fulfilled")) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	err = tx.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1 FOR UPDATE`, orderID).Scan(&assetID)
	if err != nil && (!errors.Is(err, pgx.ErrNoRows) || (compensationReason == nil && !historicalConfirmation)) {
		return err
	}
	if compensationReason != nil && assetID != nil && !historicalConfirmation {
		return newProviderFailure("payment_response_invalid", 0)
	}
	recordedRefundID := &providerRefundID
	if provider == "waffo_pancake" {
		// Keep the Provider ticket ID recorded at refund creation; the Webhook
		// identifier above is our merchant external operation ID.
		recordedRefundID = expectedProviderRefundID
	}
	if _, err := tx.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1 AND status='active'`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='refunded',refunded_at=now(),updated_at=now() WHERE id=$1`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='refunded',provider_refund_id=$2,refunded_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, paymentID, recordedRefundID); err != nil {
		return err
	}
	if err := datarights.EnqueueProductMediaCleanupTx(ctx, tx, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events(order_id,from_status,to_status,reason,sequence)
		SELECT $1,$2,'refunded','Provider refund confirmed.',COALESCE(max(sequence),0)+1 FROM order_events WHERE order_id=$1`, orderID, orderStatus); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'refund.confirmed',$5,'refunded',jsonb_build_object('orderId',$3::text,'assetId',$4::text))
		ON CONFLICT DO NOTHING`, paymentID, providerEventID, orderID, assetID, intentStatus); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'marketplace.provider_refund','order',$2,$3,jsonb_build_object('paymentId',$4::text,'provider',$5::text,'realRefund',true))`,
		buyerID, orderID, providerEventRequestID(provider, providerEventID), paymentID, provider); err != nil {
		return err
	}
	refundBody := "\u201c" + title + "\u201d was refunded by the payment Provider. Its access and reuse rights were revoked."
	if compensationReason != nil {
		refundBody = "\u201c" + title + "\u201d could not be delivered. The payment Provider confirmed a full refund."
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: buyerID, Kind: "marketplace.order_refunded", Title: "Refund completed",
		Body:       refundBody,
		TargetPath: "/workspace/orders", ResourceType: "order", ResourceID: &orderID,
		SourceKey: "marketplace:order:" + orderID.String() + ":refunded",
	}); err != nil {
		return err
	}
	if err := markProductSettlementRefundTx(ctx, tx, paymentID); err != nil {
		return err
	}
	return webhooks.EnqueueTx(ctx, tx, webhooks.EventInput{OwnerID: buyerID, EventType: "marketplace.order.refunded", ResourceType: "order", ResourceID: &orderID, SourceKey: "marketplace:order:" + orderID.String() + ":refunded"})
}

func validateProductRefundTx(ctx context.Context, tx pgx.Tx, provider string, paymentID uuid.UUID, providerRefundID, providerPaymentID string, amount int, currency string) error {
	var intentStatus, orderStatus, expectedProviderPaymentID, expectedCurrency string
	var expectedProviderRefundID, compensationReason *string
	var orderID uuid.UUID
	var refundOperationID *uuid.UUID
	var expectedAmount int
	err := tx.QueryRow(ctx, `
		SELECT pi.status,pi.provider_payment_id,pi.provider_refund_id,pi.amount_cents,pi.currency,o.status,o.id,o.refund_operation_id,pi.compensation_reason
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
		WHERE pi.id=$1 AND pi.purpose='product' FOR UPDATE OF pi,o`, paymentID).Scan(
		&intentStatus, &expectedProviderPaymentID, &expectedProviderRefundID, &expectedAmount, &expectedCurrency, &orderStatus, &orderID, &refundOperationID, &compensationReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if err != nil {
		return err
	}
	refundIDMatches := expectedProviderRefundID != nil && *expectedProviderRefundID == providerRefundID
	if provider == "waffo_pancake" {
		refundIDMatches = refundOperationID != nil && refundOperationID.String() == providerRefundID
	}
	validStatus := intentStatus == "refund_pending" || (intentStatus == "refund_failed" && compensationReason != nil)
	validState := (validStatus && orderStatus == "refund_requested") || (intentStatus == "refunded" && orderStatus == "refunded")
	if !validState || expectedProviderPaymentID != providerPaymentID ||
		!refundIDMatches || expectedAmount != amount || expectedCurrency != currency {
		return newProviderFailure("payment_response_invalid", 0)
	}
	return nil
}

func failProductRefundTx(ctx context.Context, tx pgx.Tx, provider string, providerEventID, paymentID uuid.UUID, providerRefundID, providerPaymentID string, amount int, currency, providerStatus string) error {
	if err := validateProductRefundTx(ctx, tx, provider, paymentID, providerRefundID, providerPaymentID, amount, currency); err != nil {
		return err
	}
	var orderID, buyerID uuid.UUID
	var title, intentStatus string
	var compensationReason *string
	if err := tx.QueryRow(ctx, `
		SELECT pi.order_id,pi.payer_id,o.product_title_snapshot,pi.compensation_reason,pi.status
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1`, paymentID).Scan(&orderID, &buyerID, &title, &compensationReason, &intentStatus); err != nil {
		return err
	}
	// A matching late failure cannot undo a confirmed refund or restore access.
	if intentStatus == "refunded" {
		return nil
	}
	if compensationReason != nil {
		return failProductCompensationTx(ctx, tx, provider, providerEventID, paymentID, orderID, buyerID, providerStatus)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='paid',provider_refund_id=NULL,updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='fulfilled',updated_at=now() WHERE id=$1`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events(order_id,from_status,to_status,reason,sequence)
		SELECT $1,'refund_requested','fulfilled','Payment Provider refund did not complete; access remains active.',COALESCE(max(sequence),0)+1
		FROM order_events WHERE order_id=$1`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'refund.failed','refund_pending','paid',jsonb_build_object('orderId',$3::text,'providerStatus',$4::text))
		ON CONFLICT DO NOTHING`, paymentID, providerEventID, orderID, providerStatus); err != nil {
		return err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: buyerID, Kind: "marketplace.refund_failed", Title: "Refund not completed",
		Body:       "The payment Provider did not complete the refund for \u201c" + title + "\u201d. Your access remains active.",
		TargetPath: "/workspace/orders", ResourceType: "order", ResourceID: &orderID,
		SourceKey: "marketplace:order:" + orderID.String() + ":refund-failed:" + providerEventID.String(),
	}); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'marketplace.provider_refund_failed','order',$2,$3,jsonb_build_object('paymentId',$4::text,'provider',$5::text,'providerStatus',$6::text))`,
		buyerID, orderID, providerEventRequestID(provider, providerEventID), paymentID, provider, providerStatus)
	return err
}

func deliveryCheckoutError(err error) error {
	if errors.Is(err, productdelivery.ErrUnavailable) {
		return ErrCheckoutReconciliation
	}
	if errors.Is(err, media.ErrNotFound) || errors.Is(err, media.ErrIntegrity) {
		return ErrInvalidCheckout
	}
	return err
}

func preparationCheckoutError(err error) error {
	if errors.Is(err, productdelivery.ErrUnavailable) {
		return ErrCheckoutReconciliation
	}
	return fmt.Errorf("%w: %w", ErrCheckoutPreparation, err)
}
