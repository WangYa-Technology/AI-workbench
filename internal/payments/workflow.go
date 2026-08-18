package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
	"github.com/jackc/pgx/v5"
)

const (
	PaymentEventJobKind  = "payment.process_event"
	TaskTransferJobKind  = "payment.transfer_task"
	TaskRefundJobKind    = "payment.refund_task"
	ProductRefundJobKind = "payment.refund_product"
)

var (
	ErrInvalidCheckout  = errors.New("invalid payment checkout")
	ErrCheckoutConflict = errors.New("payment checkout conflict")
	ErrAlreadyOwned     = errors.New("product already owned")
	ErrInvalidRefund    = errors.New("invalid payment refund")
	ErrRefundConflict   = errors.New("payment refund conflict")
	ErrRefundExpired    = errors.New("payment refund window expired")
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

type paymentEventJobPayload struct {
	EventID uuid.UUID `json:"eventId"`
}

type taskTransferJobPayload struct {
	PaymentID uuid.UUID `json:"paymentId"`
}

type taskRefundJobPayload struct {
	PaymentID uuid.UUID `json:"paymentId"`
}

func (s *Service) BeginTaskCheckout(ctx context.Context, clientID, taskID uuid.UUID, proposalID *uuid.UUID, idempotencyKey, requestID, successURL, cancelURL string) (TaskCheckout, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || s.pool == nil || !s.config.Enabled {
		return TaskCheckout{}, false, ErrDisabled
	}
	if clientID == uuid.Nil || taskID == uuid.Nil || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || !validReturnURL(successURL) || !validReturnURL(cancelURL) || (proposalID != nil && *proposalID == uuid.Nil) {
		return TaskCheckout{}, false, ErrInvalidCheckout
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return TaskCheckout{}, false, err
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
	var ownerID uuid.UUID
	var title, status, currency string
	var budget int
	var direct bool
	if err := tx.QueryRow(ctx, `SELECT client_id,title,status,budget_cents,currency,allow_direct_accept FROM demands WHERE id=$1 FOR UPDATE`, taskID).Scan(&ownerID, &title, &status, &budget, &currency, &direct); errors.Is(err, pgx.ErrNoRows) {
		return TaskCheckout{}, false, ErrInvalidCheckout
	} else if err != nil {
		return TaskCheckout{}, false, err
	}
	if ownerID != clientID || status != "open" || currency != "USD" {
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
		INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,proposal_id,amount_cents,currency,status,live_mode,idempotency_key,created_at,updated_at)
		VALUES($1,'stripe','task',$2,$3,$4,$5,$6,$7,'checkout_pending',$8,$9,$10,$10)`,
		paymentID, clientID, payeeID, taskID, proposalID, amountCents, currency, s.config.LiveMode, idempotencyKey, createdAt); err != nil {
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
	checkout = TaskCheckout{PaymentID: paymentID, TaskID: taskID, ProposalID: proposalID, Purpose: "task", Status: "checkout_pending", AmountCents: amountCents, Currency: currency, PaymentMode: "stripe", RealCharge: s.config.LiveMode, LiveMode: s.config.LiveMode}
	_ = title
	return s.createTaskProviderCheckout(ctx, runtime, checkout, successURL, cancelURL)
}

func (s *Service) createTaskProviderCheckout(ctx context.Context, runtime ProviderRuntime, checkout TaskCheckout, successURL, cancelURL string) (TaskCheckout, bool, error) {
	session, err := runtime.CreateCheckout(ctx, CheckoutRequest{
		PaymentID: checkout.PaymentID, ResourceID: checkout.TaskID, Purpose: "task", Name: "HCAI CHAT task funding " + checkout.TaskID.String(),
		AmountCents: checkout.AmountCents, Currency: checkout.Currency, SuccessURL: successURL, CancelURL: cancelURL,
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
	err := tx.QueryRow(ctx, `
		SELECT id,resource_id,proposal_id,purpose,status,checkout_url,checkout_expires_at,amount_cents,currency,live_mode
		FROM payment_intents WHERE payer_id=$1 AND purpose='task' AND idempotency_key=$2`, clientID, idempotencyKey).Scan(
		&item.PaymentID, &item.TaskID, &item.ProposalID, &item.Purpose, &item.Status, &checkoutURL, &expiresAt, &item.AmountCents, &item.Currency, &item.LiveMode)
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
	item.PaymentMode, item.RealCharge = "stripe", item.LiveMode
	return item, true, nil
}

func sameOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (s *Service) BeginProductCheckout(ctx context.Context, buyerID, productID uuid.UUID, idempotencyKey, requestID, successURL, cancelURL string, licenseAccepted bool) (Checkout, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || s.pool == nil || !s.config.Enabled {
		return Checkout{}, false, ErrDisabled
	}
	if buyerID == uuid.Nil || productID == uuid.Nil || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || !licenseAccepted || !validReturnURL(successURL) || !validReturnURL(cancelURL) {
		return Checkout{}, false, ErrInvalidCheckout
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return Checkout{}, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Checkout{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	checkout, found, err := loadProductCheckoutByKey(ctx, tx, buyerID, idempotencyKey)
	if err != nil {
		return Checkout{}, false, err
	}
	if found {
		if checkout.ResourceID != productID {
			return Checkout{}, false, ErrCheckoutConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Checkout{}, false, err
		}
		if checkout.CheckoutURL != "" {
			checkout.AlreadyCreated = true
			return checkout, false, nil
		}
		return s.createProviderCheckout(ctx, runtime, checkout, successURL, cancelURL)
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Checkout); err != nil {
		return Checkout{}, false, err
	}
	var sellerID uuid.UUID
	var title, currency, licenseName, licenseVersion, licenseTerms, scanStatus string
	var amountCents, refundWindowDays int
	err = tx.QueryRow(ctx, `
		SELECT p.seller_id,p.title,p.price_cents,p.currency,l.name,l.version,l.terms,l.refund_window_days,a.scan_status
		FROM products p JOIN licenses l ON l.code=p.license_code AND l.status='active'
		JOIN assets a ON a.id=p.asset_id
		WHERE p.id=$1 AND p.status='active' FOR UPDATE OF p`, productID).Scan(
		&sellerID, &title, &amountCents, &currency, &licenseName, &licenseVersion, &licenseTerms, &refundWindowDays, &scanStatus)
	if errors.Is(err, pgx.ErrNoRows) || scanStatus != "clean" {
		return Checkout{}, false, ErrInvalidCheckout
	}
	if err != nil {
		return Checkout{}, false, err
	}
	if sellerID == buyerID || amountCents < 50 || currency != "USD" {
		return Checkout{}, false, ErrInvalidCheckout
	}
	var alreadyOwned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM entitlements WHERE user_id=$1 AND product_id=$2 AND status='active')`, buyerID, productID).Scan(&alreadyOwned); err != nil {
		return Checkout{}, false, err
	}
	if alreadyOwned {
		return Checkout{}, false, ErrAlreadyOwned
	}
	paymentID, orderID := uuid.New(), uuid.New()
	createdAt := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,
		  product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,'payment_pending',$6,$7,$8,$9,$10,$11,$12,$6,$6)`,
		orderID, buyerID, productID, amountCents, currency, createdAt, idempotencyKey, title, licenseName, licenseVersion, licenseTerms, refundWindowDays); err != nil {
		return Checkout{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events(order_id,actor_id,from_status,to_status,reason,created_at,sequence)
		VALUES($1,$2,NULL,'payment_pending','License accepted; signed Provider checkout required.',$3,1)`, orderID, buyerID, createdAt); err != nil {
		return Checkout{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,created_at,updated_at)
		VALUES($1,'stripe','product',$2,$3,$4,$5,$6,$7,'checkout_pending',$8,$9,$10,$10)`,
		paymentID, buyerID, sellerID, productID, orderID, amountCents, currency, s.config.LiveMode, idempotencyKey, createdAt); err != nil {
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
		AmountCents: amountCents, Currency: currency, PaymentMode: "stripe", RealCharge: s.config.LiveMode, LiveMode: s.config.LiveMode,
	}
	return s.createProviderCheckout(ctx, runtime, checkout, successURL, cancelURL)
}

func (s *Service) createProviderCheckout(ctx context.Context, runtime ProviderRuntime, checkout Checkout, successURL, cancelURL string) (Checkout, bool, error) {
	session, err := runtime.CreateCheckout(ctx, CheckoutRequest{
		PaymentID: checkout.PaymentID, ResourceID: checkout.ResourceID, Purpose: checkout.Purpose,
		Name: "HCAI CHAT product order " + checkout.OrderID.String(), AmountCents: checkout.AmountCents, Currency: checkout.Currency,
		SuccessURL: successURL, CancelURL: cancelURL,
	})
	if err != nil {
		return Checkout{}, false, SanitizeProviderError(err)
	}
	result, err := s.pool.Exec(ctx, `
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
	return checkout, true, nil
}

func loadProductCheckoutByKey(ctx context.Context, tx pgx.Tx, buyerID uuid.UUID, idempotencyKey string) (Checkout, bool, error) {
	var item Checkout
	var checkoutURL *string
	var expiresAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT p.id,p.order_id,p.resource_id,p.purpose,p.status,p.checkout_url,p.checkout_expires_at,p.amount_cents,p.currency,p.live_mode
		FROM payment_intents p WHERE p.payer_id=$1 AND p.purpose='product' AND p.idempotency_key=$2`, buyerID, idempotencyKey).Scan(
		&item.PaymentID, &item.OrderID, &item.ResourceID, &item.Purpose, &item.Status, &checkoutURL, &expiresAt,
		&item.AmountCents, &item.Currency, &item.LiveMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return Checkout{}, false, nil
	}
	if err != nil {
		return Checkout{}, false, err
	}
	if checkoutURL != nil {
		item.CheckoutURL = *checkoutURL
	}
	if expiresAt != nil {
		item.ExpiresAt = *expiresAt
	}
	item.PaymentMode, item.RealCharge = "stripe", item.LiveMode
	return item, true, nil
}

func (s *Service) BeginProductRefund(ctx context.Context, buyerID, orderID uuid.UUID, idempotencyKey, requestID, reason string) (bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	reason = strings.TrimSpace(reason)
	if s == nil || s.pool == nil || !s.config.Enabled {
		return false, ErrDisabled
	}
	if buyerID == uuid.Nil || orderID == uuid.Nil || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || len(reason) < 10 || len(reason) > 500 {
		return false, ErrInvalidRefund
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var paymentID uuid.UUID
	var intentStatus, orderStatus, providerPaymentID string
	var amountCents, refundWindowDays int
	var createdAt time.Time
	var providerRefundID, existingIdempotencyKey *string
	var existingOperationID *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT pi.id,pi.status,pi.provider_payment_id,pi.provider_refund_id,o.refund_idempotency_key,o.refund_operation_id,
		       o.status,o.amount_cents,o.refund_window_days_snapshot,o.created_at
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
		WHERE o.id=$1 AND o.buyer_id=$2 AND pi.purpose='product' FOR UPDATE OF pi,o`, orderID, buyerID).Scan(
		&paymentID, &intentStatus, &providerPaymentID, &providerRefundID, &existingIdempotencyKey, &existingOperationID,
		&orderStatus, &amountCents, &refundWindowDays, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrRefundConflict
	}
	if err != nil {
		return false, err
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
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		if providerRefundID != nil {
			return false, nil
		}
		if existingOperationID == nil {
			return false, ErrRefundConflict
		}
		return s.createProviderRefund(ctx, runtime, paymentID, *existingOperationID, providerPaymentID, amountCents)
	}
	if intentStatus != "paid" || orderStatus != "fulfilled" {
		return false, ErrRefundConflict
	}
	if refundWindowDays <= 0 || time.Now().After(createdAt.Add(time.Duration(refundWindowDays)*24*time.Hour)) {
		return false, ErrRefundExpired
	}
	operationID := uuid.New()
	if _, err := tx.Exec(ctx, `
		UPDATE orders SET status='refund_requested',refund_reason=$2,refund_idempotency_key=$3,refund_operation_id=$4,
		  refund_requested_at=now(),updated_at=now() WHERE id=$1`, orderID, reason, idempotencyKey, operationID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='refund_pending',updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return s.createProviderRefund(ctx, runtime, paymentID, operationID, providerPaymentID, amountCents)
}

func (s *Service) createProviderRefund(ctx context.Context, runtime ProviderRuntime, paymentID, operationID uuid.UUID, providerPaymentID string, amountCents int) (bool, error) {
	refund, err := runtime.CreateRefund(ctx, RefundRequest{PaymentID: paymentID, OperationID: operationID, ProviderPaymentID: providerPaymentID, AmountCents: amountCents})
	if err != nil {
		return false, SanitizeProviderError(err)
	}
	result, err := s.pool.Exec(ctx, `
		UPDATE payment_intents SET provider_refund_id=$2,updated_at=now(),version=version+1
		WHERE id=$1 AND status='refund_pending' AND (provider_refund_id IS NULL OR provider_refund_id=$2)`, paymentID, refund.ProviderID)
	if err != nil {
		return false, err
	}
	if result.RowsAffected() != 1 {
		return false, ErrRefundConflict
	}
	return true, nil
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
	var eventType, objectID string
	var paymentID *uuid.UUID
	var amount *int64
	var eventPurpose, currency, paymentStatus, providerPaymentID, providerChargeID, destinationID *string
	var destinationUserID *uuid.UUID
	var accountChargesEnabled, accountPayoutsEnabled, accountDetailsSubmitted, accountRequirementsDue *bool
	var occurredAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT event_type,object_id,payment_id,purpose,amount_cents,currency,payment_status,provider_payment_id,provider_charge_id,
		       destination_id,destination_user_id,account_charges_enabled,account_payouts_enabled,account_details_submitted,account_requirements_due,occurred_at
		FROM payment_provider_events WHERE id=$1`, payload.EventID).Scan(
		&eventType, &objectID, &paymentID, &eventPurpose, &amount, &currency, &paymentStatus, &providerPaymentID, &providerChargeID,
		&destinationID, &destinationUserID, &accountChargesEnabled, &accountPayoutsEnabled, &accountDetailsSubmitted, &accountRequirementsDue, &occurredAt); err != nil {
		return err
	}
	if eventType == "account.updated" {
		if destinationID == nil || accountChargesEnabled == nil || accountPayoutsEnabled == nil || accountDetailsSubmitted == nil || accountRequirementsDue == nil {
			return newProviderFailure("payment_response_invalid", 0)
		}
		handled, err := syncPayoutDestinationTx(ctx, tx, payload.EventID, *destinationID, destinationUserID, *accountChargesEnabled, *accountPayoutsEnabled, *accountDetailsSubmitted, *accountRequirementsDue, occurredAt)
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
	if paymentID == nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	var intentPurpose string
	if err := tx.QueryRow(ctx, `SELECT purpose FROM payment_intents WHERE id=$1`, *paymentID).Scan(&intentPurpose); errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	} else if err != nil {
		return err
	}
	if eventPurpose != nil && *eventPurpose != intentPurpose {
		return newProviderFailure("payment_response_invalid", 0)
	}
	switch eventType {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		if paymentStatus == nil || *paymentStatus != "paid" || amount == nil || currency == nil || *currency != "USD" || providerPaymentID == nil {
			return newProviderFailure("payment_response_invalid", 0)
		}
		if intentPurpose == "task" {
			if err := fundTaskPaymentTx(ctx, tx, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID); err != nil {
				return err
			}
		} else if err := fulfillProductPaymentTx(ctx, tx, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID); err != nil {
			return err
		}
	case "payment_intent.succeeded":
		if paymentStatus == nil || *paymentStatus != "succeeded" || amount == nil || currency == nil || *currency != "USD" || providerPaymentID == nil || providerChargeID == nil {
			return newProviderFailure("payment_response_invalid", 0)
		}
		if intentPurpose == "task" {
			if err := fundTaskPaymentTx(ctx, tx, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID); err != nil {
				return err
			}
		} else if err := fulfillProductPaymentTx(ctx, tx, payload.EventID, *paymentID, int(*amount), *currency, *providerPaymentID, providerChargeID); err != nil {
			return err
		}
	case "checkout.session.async_payment_failed", "payment_intent.payment_failed":
		var err error
		if intentPurpose == "task" {
			err = failTaskPaymentTx(ctx, tx, payload.EventID, *paymentID)
		} else {
			err = failProductPaymentTx(ctx, tx, payload.EventID, *paymentID)
		}
		if err != nil {
			return err
		}
	case "refund.updated":
		if paymentStatus == nil || amount == nil || currency == nil || *currency != "USD" || providerPaymentID == nil || !validStripeID(objectID, "re_") {
			return newProviderFailure("payment_response_invalid", 0)
		}
		switch *paymentStatus {
		case "succeeded":
			if intentPurpose == "task" {
				err = refundTaskPaymentTx(ctx, tx, payload.EventID, *paymentID, objectID, *providerPaymentID, int(*amount), *currency)
			} else {
				err = refundProductPaymentTx(ctx, tx, payload.EventID, *paymentID, objectID, *providerPaymentID, int(*amount), *currency)
			}
			if err != nil {
				return err
			}
		case "failed", "canceled":
			if intentPurpose == "task" {
				err = failTaskRefundTx(ctx, tx, payload.EventID, *paymentID, objectID, *providerPaymentID, int(*amount), *currency, *paymentStatus)
			} else {
				err = failProductRefundTx(ctx, tx, payload.EventID, *paymentID, objectID, *providerPaymentID, int(*amount), *currency, *paymentStatus)
			}
			if err != nil {
				return err
			}
		case "pending", "requires_action":
			if intentPurpose == "task" {
				err = validateTaskRefundTx(ctx, tx, *paymentID, objectID, *providerPaymentID, int(*amount), *currency)
			} else {
				err = validateProductRefundTx(ctx, tx, *paymentID, objectID, *providerPaymentID, int(*amount), *currency)
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
	return tx.Commit(ctx)
}

func syncPayoutDestinationTx(ctx context.Context, tx pgx.Tx, providerEventID uuid.UUID, destinationID string, destinationUserID *uuid.UUID, chargesEnabled, payoutsEnabled, detailsSubmitted, requirementsDue bool, occurredAt time.Time) (bool, error) {
	var recordID, userID uuid.UUID
	var oldStatus string
	var adminDisabled bool
	err := tx.QueryRow(ctx, `
		SELECT id,user_id,status,admin_disabled FROM payment_destinations
		WHERE provider='stripe' AND destination_id=$1 FOR UPDATE`, destinationID).Scan(&recordID, &userID, &oldStatus, &adminDisabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if destinationUserID != nil && *destinationUserID != userID {
		return false, newProviderFailure("payment_response_invalid", 0)
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
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return err
	}
	var status, currency, title string
	var payeeID *uuid.UUID
	var taskID uuid.UUID
	var amount int
	var providerChargeID, providerTransferID *string
	err = s.pool.QueryRow(ctx, `
		SELECT pi.status,pi.payee_id,pi.resource_id,pi.amount_cents,pi.currency,pi.provider_charge_id,pi.provider_transfer_id,d.title
		FROM payment_intents pi JOIN demands d ON d.id=pi.resource_id
		WHERE pi.id=$1 AND pi.purpose='task'`, payload.PaymentID).Scan(
		&status, &payeeID, &taskID, &amount, &currency, &providerChargeID, &providerTransferID, &title)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if err != nil {
		return err
	}
	if status == "transferred" && providerTransferID != nil {
		return nil
	}
	if status != "transfer_pending" || payeeID == nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if providerChargeID == nil {
		return newProviderFailure("payment_request_failed", 30*time.Second)
	}
	var destinationID string
	if err := s.pool.QueryRow(ctx, `
		SELECT destination_id FROM payment_destinations
		WHERE provider='stripe' AND user_id=$1 AND status='verified' AND charges_enabled AND payouts_enabled`, *payeeID).Scan(&destinationID); errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_provider_unavailable", 5*time.Minute)
	} else if err != nil {
		return err
	}
	transfer, err := runtime.CreateTransfer(ctx, TransferRequest{
		PaymentID: payload.PaymentID, ProviderChargeID: *providerChargeID, DestinationID: destinationID, AmountCents: amount, Currency: currency,
	})
	if err != nil {
		return SanitizeProviderError(err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var lockedStatus string
	var lockedTransferID *string
	if err := tx.QueryRow(ctx, `SELECT status,provider_transfer_id FROM payment_intents WHERE id=$1 FOR UPDATE`, payload.PaymentID).Scan(&lockedStatus, &lockedTransferID); err != nil {
		return err
	}
	if lockedStatus == "transferred" && lockedTransferID != nil && *lockedTransferID == transfer.ProviderID {
		return tx.Commit(ctx)
	}
	if lockedStatus != "transfer_pending" || (lockedTransferID != nil && *lockedTransferID != transfer.ProviderID) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='transferred',provider_transfer_id=$2,destination_id=$3,transferred_at=now(),updated_at=now(),version=version+1
		WHERE id=$1`, payload.PaymentID, transfer.ProviderID, destinationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE task_settlements SET mode='stripe_transferred' WHERE demand_id=$1 AND mode='stripe_pending'`, taskID); err != nil {
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
		VALUES($1,'task.provider_transfer','task',$2,$3,jsonb_build_object('paymentId',$4::text,'provider','stripe','amountCents',$5::integer,'currency',$6::text))`,
		*payeeID, taskID, "stripe-transfer:"+transfer.ProviderID, payload.PaymentID, amount, currency); err != nil {
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

func (s *Service) HandleTaskRefundJob(ctx context.Context, job jobs.Job) error {
	var payload taskRefundJobPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.PaymentID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if s == nil || s.pool == nil || !s.config.Enabled {
		return ErrDisabled
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return err
	}
	var status, providerPaymentID string
	var providerRefundID *string
	var operationID *uuid.UUID
	var amount int
	err = s.pool.QueryRow(ctx, `
		SELECT status,provider_payment_id,provider_refund_id,refund_operation_id,amount_cents
		FROM payment_intents WHERE id=$1 AND purpose='task'`, payload.PaymentID).Scan(
		&status, &providerPaymentID, &providerRefundID, &operationID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if err != nil {
		return err
	}
	if status == "refunded" || (status == "refund_pending" && providerRefundID != nil) {
		return nil
	}
	if status != "refund_pending" || operationID == nil {
		return newProviderFailure("payment_response_invalid", 0)
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
	var payload taskRefundJobPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.PaymentID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if s == nil || s.pool == nil || !s.config.Enabled {
		return ErrDisabled
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return err
	}
	var status, providerPaymentID string
	var providerRefundID *string
	var operationID *uuid.UUID
	var amount int
	err = s.pool.QueryRow(ctx, `
		SELECT pi.status,pi.provider_payment_id,pi.provider_refund_id,o.refund_operation_id,pi.amount_cents
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
		WHERE pi.id=$1 AND pi.purpose='product'`, payload.PaymentID).Scan(
		&status, &providerPaymentID, &providerRefundID, &operationID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if err != nil {
		return err
	}
	if status == "refunded" || (status == "refund_pending" && providerRefundID != nil) {
		return nil
	}
	if status != "refund_pending" || operationID == nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	refund, err := runtime.CreateRefund(ctx, RefundRequest{
		PaymentID: payload.PaymentID, OperationID: *operationID, ProviderPaymentID: providerPaymentID, AmountCents: amount,
	})
	if err != nil {
		return SanitizeProviderError(err)
	}
	result, err := s.pool.Exec(ctx, `
		UPDATE payment_intents SET provider_refund_id=$2,updated_at=now(),version=version+1
		WHERE id=$1 AND purpose='product' AND status='refund_pending'
		  AND (provider_refund_id IS NULL OR provider_refund_id=$2)`, payload.PaymentID, refund.ProviderID)
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

func fulfillProductPaymentTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID, amount int, currency, providerPaymentID string, providerChargeID *string) error {
	var intentStatus, purpose, orderStatus string
	var payerID, productID, orderID uuid.UUID
	var expectedAmount int
	var expectedCurrency, title, licenseCode, sourceTitle, mediaURL, mimeType, kind string
	var sourceAssetID, rootOriginID uuid.UUID
	var width, height *int
	err := tx.QueryRow(ctx, `
		SELECT pi.status,pi.purpose,pi.payer_id,pi.resource_id,pi.order_id,pi.amount_cents,pi.currency,o.status,o.product_title_snapshot,
		       p.license_code,a.id,COALESCE(a.origin_asset_id,a.id),a.title,a.media_url,a.mime_type,a.kind,a.width,a.height
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id JOIN products p ON p.id=pi.resource_id
		JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean'
		WHERE pi.id=$1 FOR UPDATE OF pi,o,p`, paymentID).Scan(
		&intentStatus, &purpose, &payerID, &productID, &orderID, &expectedAmount, &expectedCurrency, &orderStatus, &title,
		&licenseCode, &sourceAssetID, &rootOriginID, &sourceTitle, &mediaURL, &mimeType, &kind, &width, &height)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if err != nil {
		return err
	}
	if purpose != "product" || amount != expectedAmount || currency != expectedCurrency {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if intentStatus == "paid" && orderStatus == "fulfilled" {
		result, err := tx.Exec(ctx, `
			UPDATE payment_intents SET provider_charge_id=COALESCE(provider_charge_id,$2),updated_at=now()
			WHERE id=$1 AND provider_payment_id=$3 AND (provider_charge_id IS NULL OR provider_charge_id=$2 OR $2::text IS NULL)`, paymentID, providerChargeID, providerPaymentID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return newProviderFailure("payment_response_invalid", 0)
		}
		return nil
	}
	if !oneOf(intentStatus, "checkout_pending", "checkout_open") || orderStatus != "payment_pending" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	assetID, entitlementID := uuid.New(), uuid.New()
	if strings.HasPrefix(mediaURL, "/api/v1/assets/") {
		mediaURL = "/api/v1/assets/" + assetID.String() + "/content"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,source_id,license_code,origin_asset_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'clean','purchase',$9,$10,$11)`,
		assetID, payerID, kind, sourceTitle, mediaURL, mimeType, width, height, orderID, licenseCode, rootOriginID); err != nil {
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
		VALUES($1,'payment_pending','payment_paid','Signed Provider payment confirmed.',2),
		      ($1,'payment_paid','fulfilled','Entitlement and purchased Asset granted after signed payment.',3)`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='paid',provider_payment_id=$2,provider_charge_id=COALESCE($3,provider_charge_id),paid_at=now(),updated_at=now(),version=version+1
		WHERE id=$1`, paymentID, providerPaymentID, providerChargeID); err != nil {
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
		VALUES($1,'marketplace.provider_purchase','order',$2,$3,jsonb_build_object('paymentId',$4::text,'provider','stripe','realCharge',true))`,
		payerID, orderID, "stripe-event:"+providerEventID.String(), paymentID); err != nil {
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
		VALUES($1,'task.provider_funded','task',$2,$3,jsonb_build_object('paymentId',$4::uuid::text,'provider','stripe','liveMode',(SELECT live_mode FROM payment_intents WHERE id=$4::uuid)))`,
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

func failProductPaymentTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID) error {
	var status, purpose string
	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT status,purpose,order_id FROM payment_intents WHERE id=$1 FOR UPDATE`, paymentID).Scan(&status, &purpose, &orderID); err != nil {
		return err
	}
	if purpose != "product" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if status == "payment_failed" {
		return nil
	}
	if !oneOf(status, "checkout_pending", "checkout_open") {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='payment_failed',updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='payment_failed',updated_at=now() WHERE id=$1 AND status='payment_pending'`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_events(order_id,from_status,to_status,reason,sequence) VALUES($1,'payment_pending','payment_failed','Signed Provider payment failure received.',2) ON CONFLICT DO NOTHING`, orderID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status)
		VALUES($1,$2,'payment.failed',$3,'payment_failed') ON CONFLICT DO NOTHING`, paymentID, providerEventID, status)
	return err
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
		VALUES($1,$2,'funding_refunded','cancelled','cancelled','Signed Provider refund confirmed.',jsonb_build_object('paymentId',$3::text,'amountCents',$4::integer,'currency',$5::text))`,
		taskID, payerID, paymentID, amount, currency); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'task.provider_refund','task',$2,$3,jsonb_build_object('paymentId',$4::text,'provider','stripe','amountCents',$5::integer,'currency',$6::text))`,
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

func refundProductPaymentTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID, providerRefundID, providerPaymentID string, amount int, currency string) error {
	var intentStatus, orderStatus, expectedProviderPaymentID, expectedProviderRefundID, expectedCurrency, title string
	var orderID, buyerID, assetID uuid.UUID
	var expectedAmount int
	err := tx.QueryRow(ctx, `
		SELECT pi.status,pi.provider_payment_id,pi.provider_refund_id,pi.amount_cents,pi.currency,pi.order_id,pi.payer_id,o.status,o.product_title_snapshot,e.asset_id
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id JOIN entitlements e ON e.order_id=o.id
		WHERE pi.id=$1 AND pi.purpose='product' FOR UPDATE OF pi,o,e`, paymentID).Scan(
		&intentStatus, &expectedProviderPaymentID, &expectedProviderRefundID, &expectedAmount, &expectedCurrency, &orderID, &buyerID, &orderStatus, &title, &assetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if err != nil {
		return err
	}
	if intentStatus == "refunded" && orderStatus == "refunded" {
		return nil
	}
	if intentStatus != "refund_pending" || orderStatus != "refund_requested" || expectedProviderPaymentID != providerPaymentID ||
		expectedProviderRefundID != providerRefundID || expectedAmount != amount || expectedCurrency != currency {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if _, err := tx.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1 AND status='active'`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='refunded',refunded_at=now(),updated_at=now() WHERE id=$1`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='refunded',provider_refund_id=$2,refunded_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, paymentID, providerRefundID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events(order_id,from_status,to_status,reason,sequence)
		SELECT $1,'refund_requested','refunded','Signed Provider refund confirmed.',COALESCE(max(sequence),0)+1 FROM order_events WHERE order_id=$1`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'refund.confirmed','refund_pending','refunded',jsonb_build_object('orderId',$3::text,'assetId',$4::text))
		ON CONFLICT DO NOTHING`, paymentID, providerEventID, orderID, assetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'marketplace.provider_refund','order',$2,$3,jsonb_build_object('paymentId',$4::text,'provider','stripe','realRefund',true))`,
		buyerID, orderID, "stripe-event:"+providerEventID.String(), paymentID); err != nil {
		return err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: buyerID, Kind: "marketplace.order_refunded", Title: "Refund completed",
		Body:       "\u201c" + title + "\u201d was refunded by the payment Provider. Its access and reuse rights were revoked.",
		TargetPath: "/workspace/orders", ResourceType: "order", ResourceID: &orderID,
		SourceKey: "marketplace:order:" + orderID.String() + ":refunded",
	}); err != nil {
		return err
	}
	return webhooks.EnqueueTx(ctx, tx, webhooks.EventInput{OwnerID: buyerID, EventType: "marketplace.order.refunded", ResourceType: "order", ResourceID: &orderID, SourceKey: "marketplace:order:" + orderID.String() + ":refunded"})
}

func validateProductRefundTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID, providerRefundID, providerPaymentID string, amount int, currency string) error {
	var intentStatus, orderStatus, expectedProviderPaymentID, expectedProviderRefundID, expectedCurrency string
	var expectedAmount int
	err := tx.QueryRow(ctx, `
		SELECT pi.status,pi.provider_payment_id,pi.provider_refund_id,pi.amount_cents,pi.currency,o.status
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
		WHERE pi.id=$1 AND pi.purpose='product' FOR UPDATE OF pi,o`, paymentID).Scan(
		&intentStatus, &expectedProviderPaymentID, &expectedProviderRefundID, &expectedAmount, &expectedCurrency, &orderStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if err != nil {
		return err
	}
	if intentStatus != "refund_pending" || orderStatus != "refund_requested" || expectedProviderPaymentID != providerPaymentID ||
		expectedProviderRefundID != providerRefundID || expectedAmount != amount || expectedCurrency != currency {
		return newProviderFailure("payment_response_invalid", 0)
	}
	return nil
}

func failProductRefundTx(ctx context.Context, tx pgx.Tx, providerEventID, paymentID uuid.UUID, providerRefundID, providerPaymentID string, amount int, currency, providerStatus string) error {
	if err := validateProductRefundTx(ctx, tx, paymentID, providerRefundID, providerPaymentID, amount, currency); err != nil {
		return err
	}
	var orderID, buyerID uuid.UUID
	var title string
	if err := tx.QueryRow(ctx, `
		SELECT pi.order_id,pi.payer_id,o.product_title_snapshot
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1`, paymentID).Scan(&orderID, &buyerID, &title); err != nil {
		return err
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
		VALUES($1,'marketplace.provider_refund_failed','order',$2,$3,jsonb_build_object('paymentId',$4::text,'provider','stripe','providerStatus',$5::text))`,
		buyerID, orderID, "stripe-event:"+providerEventID.String(), paymentID, providerStatus)
	return err
}
