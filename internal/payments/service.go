package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var safeStatusPattern = regexp.MustCompile(`^[a-z0-9_]{2,80}$`)
var safeProviderIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,255}$`)

var waffoWebhookClient = &http.Client{Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

var supportedStripeEvents = map[string]bool{
	"checkout.session.completed":               true,
	"checkout.session.async_payment_succeeded": true,
	"checkout.session.async_payment_failed":    true,
	"payment_intent.payment_failed":            true,
	"payment_intent.succeeded":                 true,
	"refund.updated":                           true,
	"transfer.created":                         true,
	"account.updated":                          true,
	"charge.dispute.created":                   true,
	"charge.dispute.updated":                   true,
	"charge.dispute.closed":                    true,
}

type ServiceConfig struct {
	MediaStores                *media.Catalog
	Enabled                    bool
	Provider                   string
	LiveMode                   bool
	APIVersion                 string
	WebhookSecret              string
	WebhookTolerance           time.Duration
	WaffoWebhookURL            string
	WaffoConnectorToken        string
	WaffoEnvironment           string
	WaffoMerchantID            string
	WaffoStoreID               string
	WaffoProductIDOnetime      string
	WaffoProductIDSubscription string
}

type Service struct {
	refundReconciliation         paymentReconciliationScan
	checkoutReconciliation       paymentReconciliationScan
	settlementReconciliation     paymentReconciliationScan
	sellerFundingReconciliation  paymentReconciliationScan
	sellerBankReconciliation     paymentReconciliationScan
	sellerReversalReconciliation paymentReconciliationScan
	pool                         *pgxpool.Pool
	config                       ServiceConfig
	verifier                     StripeWebhookVerifier
	runtimes                     *RuntimeCatalog
}

type Receipt struct {
	EventID         uuid.UUID `json:"eventId"`
	ProviderEventID string    `json:"providerEventId"`
	EventType       string    `json:"eventType"`
	Status          string    `json:"status"`
	Duplicate       bool      `json:"duplicate"`
}

type stripeEventEnvelope struct {
	ID         string          `json:"id"`
	Object     string          `json:"object"`
	APIVersion string          `json:"api_version"`
	Created    int64           `json:"created"`
	LiveMode   bool            `json:"livemode"`
	Account    string          `json:"account"`
	Context    string          `json:"context"`
	Type       string          `json:"type"`
	Data       stripeEventData `json:"data"`
}

type waffoWebhookEnvelope struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	EventType string `json:"eventType"`
	EventID   string `json:"eventId"`
	StoreID   string `json:"storeId"`
	Mode      string `json:"mode"`
	Data      struct {
		OrderID                        string            `json:"orderId"`
		OrderStatus                    string            `json:"orderStatus"`
		BuyerEmail                     string            `json:"buyerEmail"`
		MerchantProvidedBuyerIdentity  string            `json:"merchantProvidedBuyerIdentity"`
		OrderMerchantExternalID        string            `json:"orderMerchantExternalId"`
		RefundTicketMerchantExternalID string            `json:"refundTicketMerchantExternalId"`
		Currency                       string            `json:"currency"`
		OrderMetadata                  map[string]string `json:"orderMetadata"`
		Amount                         string            `json:"amount"`
		PaymentID                      string            `json:"paymentId"`
		PaymentStatus                  string            `json:"paymentStatus"`
		RefundStatus                   string            `json:"refundStatus"`
	} `json:"data"`
}

type stripeEventData struct {
	Object stripeEventObject `json:"object"`
}

type stripeEventObject struct {
	ID                string                        `json:"id"`
	Object            string                        `json:"object"`
	Status            string                        `json:"status"`
	PaymentStatus     string                        `json:"payment_status"`
	AmountTotal       *int64                        `json:"amount_total"`
	Amount            *int64                        `json:"amount"`
	AmountReceived    *int64                        `json:"amount_received"`
	Currency          string                        `json:"currency"`
	PaymentIntent     string                        `json:"payment_intent"`
	Charge            string                        `json:"charge"`
	LatestCharge      string                        `json:"latest_charge"`
	Destination       string                        `json:"destination"`
	Metadata          map[string]string             `json:"metadata"`
	ChargesEnabled    *bool                         `json:"charges_enabled"`
	PayoutsEnabled    *bool                         `json:"payouts_enabled"`
	DetailsSubmitted  *bool                         `json:"details_submitted"`
	Requirements      *stripeAccountRequirements    `json:"requirements"`
	Reason            string                        `json:"reason"`
	NetworkReasonCode string                        `json:"network_reason_code"`
	EvidenceDetails   *stripeDisputeEvidenceDetails `json:"evidence_details"`
}

type stripeDisputeEvidenceDetails struct {
	DueBy *int64 `json:"due_by"`
}

type minimizedProviderEvent struct {
	StripeVerificationVersion string
	WaffoStoreID              string
	WaffoOrderExternalID      string
	WaffoBuyerIdentity        string
	WaffoVerificationVersion  string
	ProviderEventID           string
	EventType                 string
	APIVersion                string
	LiveMode                  bool
	OccurredAt                time.Time
	PayloadSHA256             string
	ObjectID                  string
	ObjectType                string
	PaymentID                 *uuid.UUID
	ResourceID                *uuid.UUID
	Purpose                   *string
	AmountCents               *int64
	Currency                  *string
	PaymentStatus             *string
	ProviderPaymentID         *string
	ProviderChargeID          *string
	ProviderTransferID        *string
	RefundOperationID         *uuid.UUID
	DestinationID             *string
	DestinationUserID         *uuid.UUID
	AccountChargesEnabled     *bool
	AccountPayoutsEnabled     *bool
	AccountDetailsSubmitted   *bool
	AccountRequirementsDue    *bool
	DisputeStatus             *string
	DisputeReason             *string
	DisputeNetworkReasonCode  *string
	DisputeDueBy              *time.Time
	Supported                 bool
}

func NewService(pool *pgxpool.Pool, config ServiceConfig) *Service {
	return NewServiceWithRuntimes(pool, config, NewRuntimeCatalog())
}

func NewServiceWithRuntimes(pool *pgxpool.Pool, config ServiceConfig, runtimes *RuntimeCatalog) *Service {
	if runtimes == nil {
		runtimes = NewRuntimeCatalog()
	}
	config.Provider = strings.ToLower(strings.TrimSpace(config.Provider))
	if config.Provider == "" {
		config.Provider = "stripe"
	}
	return &Service{pool: pool, config: config, verifier: NewStripeWebhookVerifier(config.WebhookSecret, config.WebhookTolerance), runtimes: runtimes}
}

// ProductProviderStatus exposes the same effective Provider selection used by
// product checkout, without exposing credentials or connector details. The
// metadata endpoint uses this so the UI cannot advertise checkout after an
// administrator disables the active Provider.
func (s *Service) ProductProviderStatus(ctx context.Context) (provider string, enabled, liveMode bool) {
	if s == nil {
		return "stripe", false, false
	}
	provider = s.productProvider()
	liveMode = s.productLiveMode()
	if s.pool == nil {
		if !s.config.Enabled || s.runtimes == nil {
			return provider, false, liveMode
		}
		fallback := persistedProviderConfig{Provider: provider, Enabled: s.config.Enabled, Environment: "test"}
		if provider == "waffo_pancake" {
			fallback.Environment = s.config.WaffoEnvironment
			fallback.MerchantID = s.config.WaffoMerchantID
			fallback.StoreID = s.config.WaffoStoreID
			fallback.ProductIDOnetime = s.config.WaffoProductIDOnetime
			fallback.ProductIDSubscription = s.config.WaffoProductIDSubscription
		}
		if !s.providerConfigReady(fallback) {
			return provider, false, liveMode
		}
		if _, err := s.runtimes.Runtime(provider); err != nil {
			return provider, false, liveMode
		}
		return provider, true, liveMode
	}
	configured, err := s.resolveProductProvider(ctx)
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			return provider, false, liveMode
		}
		// Configuration mismatches and database errors fail closed. Checkout
		// itself returns the underlying error, so metadata must not advertise a
		// route that cannot be started.
		return provider, false, liveMode
	}
	provider = configured.Provider
	liveMode = s.productLiveModeFor(configured)
	if !s.providerConfigReady(configured) || s.runtimes == nil {
		return provider, false, liveMode
	}
	if _, err := s.runtimes.Runtime(provider); err != nil {
		return provider, false, liveMode
	}
	return provider, configured.Enabled && s.config.Enabled, liveMode
}

// TaskProviderStatus is true only when the effective Provider can complete the
// whole task lifecycle: hosted funding, refunds, creator onboarding, and payout.
func (s *Service) TaskProviderStatus(ctx context.Context) bool {
	if s == nil || !s.config.Enabled || s.runtimes == nil {
		return false
	}
	configured, err := s.resolveProductProvider(ctx)
	if err != nil || !s.providerConfigReadyForPurpose(configured, "task") {
		return false
	}
	runtime, err := s.runtimes.Runtime(configured.Provider)
	if err != nil {
		return false
	}
	capabilities := runtimeCapabilities(runtime)
	return configured.Enabled && capabilities.Checkout && capabilities.Refund && capabilities.Transfer && capabilities.ConnectedAccounts
}

func (s *Service) ReceiveStripeWebhook(ctx context.Context, rawBody []byte, signatureHeader string) (Receipt, error) {
	if s == nil || s.pool == nil || !s.config.Enabled {
		return Receipt{}, ErrDisabled
	}
	if err := s.verifier.Verify(rawBody, signatureHeader); err != nil {
		return Receipt{}, err
	}
	event, err := minimizeStripeEvent(rawBody, s.config.APIVersion, s.config.LiveMode)
	if err != nil {
		return Receipt{}, err
	}
	event.StripeVerificationVersion = stripeBillingWebhookVersion
	return s.receiveSignedProductEvent(ctx, "stripe", event)
}

// ReceiveWaffoWebhook verifies a raw Waffo body through the local connector,
// which is the process that owns the Pancake private key and SDK verifier.
func (s *Service) ReceiveWaffoWebhook(ctx context.Context, rawBody []byte, signatureHeader string) (Receipt, error) {
	if s == nil || s.pool == nil || !s.config.Enabled {
		return Receipt{}, ErrDisabled
	}
	// Verifier keys belong to the deployment environment, not today's sales
	// selection. Existing obligations continue receiving signed results.
	waffoEnvironment := strings.ToLower(strings.TrimSpace(s.config.WaffoEnvironment))
	if !oneOf(waffoEnvironment, "test", "prod") {
		return Receipt{}, ErrProviderConfigMismatch
	}
	boundary := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: s.config.WaffoWebhookURL, ConnectorToken: s.config.WaffoConnectorToken, Environment: waffoEnvironment})
	if !boundary.validConnector() {
		return Receipt{}, ErrProviderConfigMismatch
	}
	if len(rawBody) == 0 || int64(len(rawBody)) > maxWaffoResponseBytes || len(signatureHeader) > 4096 || strings.TrimSpace(signatureHeader) == "" ||
		strings.TrimSpace(s.config.WaffoWebhookURL) == "" || strings.TrimSpace(s.config.WaffoConnectorToken) == "" {
		return Receipt{}, ErrInvalidSignature
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(verifyCtx, http.MethodPost, strings.TrimRight(s.config.WaffoWebhookURL, "/")+"/webhook/verify", bytes.NewReader(rawBody))
	if err != nil {
		return Receipt{}, ErrInvalidSignature
	}
	request.Header.Set("Authorization", "Bearer "+s.config.WaffoConnectorToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-waffo-signature", signatureHeader)
	response, err := waffoWebhookClient.Do(request)
	if err != nil {
		return Receipt{}, newProviderFailure("payment_provider_unavailable", 0)
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxWaffoResponseBytes+1))
	if readErr != nil || int64(len(data)) > maxWaffoResponseBytes {
		return Receipt{}, ErrInvalidEvent
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return Receipt{}, ErrInvalidSignature
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Receipt{}, ErrInvalidEvent
	}
	var verified struct {
		Verification struct {
			ContractVersion string `json:"contractVersion"`
			Environment     string `json:"environment"`
			PayloadSHA256   string `json:"payloadSHA256"`
		} `json:"verification"`
	}
	hash := sha256.Sum256(rawBody)
	if json.Unmarshal(data, &verified) != nil || verified.Verification.ContractVersion != waffoWebhookContractVersion ||
		verified.Verification.Environment != waffoEnvironment || verified.Verification.PayloadSHA256 != hex.EncodeToString(hash[:]) {
		return Receipt{}, ErrInvalidSignature
	}
	var envelope waffoWebhookEnvelope
	if json.Unmarshal(rawBody, &envelope) != nil {
		return Receipt{}, ErrInvalidEvent
	}
	event, err := minimizeWaffoEvent(envelope, rawBody, waffoEnvironment, "")
	if err != nil {
		return Receipt{}, err
	}
	event.WaffoVerificationVersion = verified.Verification.ContractVersion
	var purpose string
	if err := s.pool.QueryRow(ctx, `SELECT purpose FROM payment_intents WHERE id=$1 AND provider='waffo_pancake'`, event.PaymentID).Scan(&purpose); errors.Is(err, pgx.ErrNoRows) {
		if recordErr := s.quarantineProductEvent(ctx, "waffo_pancake", event, "payment_unknown"); recordErr != nil {
			return Receipt{}, fmt.Errorf("record rejected Waffo evidence: %w", recordErr)
		}
		return Receipt{}, ErrInvalidEvent
	} else if err != nil {
		return Receipt{}, err
	}
	return s.receiveSignedProductEvent(ctx, "waffo_pancake", event)
}

// webhookProviderEnabled is the current sales/billing routing boundary. Known
// product obligations are checked separately against their original payment.
// A deployment can register more than one runtime, while Admin chooses the one
// enabled row in payment_provider_configs. The static config provider remains
// the fallback for installations that have not created the table row yet.
func (s *Service) webhookProviderEnabled(ctx context.Context, provider string) bool {
	if s == nil || s.pool == nil || !s.config.Enabled {
		return false
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return false
	}
	var enabled bool
	err := s.pool.QueryRow(ctx, `SELECT enabled FROM payment_provider_configs WHERE provider=$1`, provider).Scan(&enabled)
	if err == nil {
		if !enabled {
			return false
		}
		if s.config.Provider == provider {
			return true
		}
		if s.runtimes == nil {
			return false
		}
		_, runtimeErr := s.runtimes.Runtime(provider)
		return runtimeErr == nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	return s.config.Provider == provider
}

func (s *Service) receiveProviderEvent(ctx context.Context, provider string, event minimizedProviderEvent) (Receipt, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Receipt{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockWebhookAdmission(ctx, tx, provider, event.ProviderEventID); err != nil {
		return Receipt{}, err
	}
	item, err := s.receiveProviderEventTx(ctx, tx, provider, event)
	if err != nil {
		return Receipt{}, err
	}
	if err = admitMatchingQuarantinesTx(ctx, tx, provider, event); err != nil {
		return Receipt{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Receipt{}, err
	}
	return item, nil
}
func (s *Service) receiveProviderEventTx(ctx context.Context, tx pgx.Tx, provider string, event minimizedProviderEvent) (Receipt, error) {
	var err error
	paymentExists := false
	stripeProduct := false
	var stripeBilling *stripeBillingWebhookBinding
	if event.PaymentID != nil {
		// Receipt admission must remain available while outbound refunds hold
		// the payment lock: a provider may deliver its callback before returning
		// the refund response. Financial application rechecks under its own lock;
		// operator rechecks explicitly lock the payment before this helper.
		err = tx.QueryRow(ctx, `SELECT true FROM payment_intents WHERE id=$1`, event.PaymentID).Scan(&paymentExists)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Receipt{}, err
		}
	}

	if provider == "stripe" {
		if isStripeDisputeEvent(event.EventType) {
			// Disputes are admitted as provider evidence before local binding. A
			// dispute payload normally has no HCAI metadata, so binding is done
			// from the immutable PaymentIntent/Charge/amount tuple below.
			if event.PaymentID == nil {
				paymentID, resourceID, purpose, bindErr := bindStripeDisputePaymentTx(ctx, tx, event)
				if bindErr != nil {
					return Receipt{}, bindErr
				}
				event.PaymentID, event.ResourceID, event.Purpose = paymentID, resourceID, purpose
			}
			stripeProduct = event.Purpose != nil && *event.Purpose == "product"
			// Do not route an unmatched dispute into billing or ordinary payment
			// processing. It is retained and marked for review by the worker.
			if event.Purpose == nil || !stripeProduct {
				event.Purpose = nil
				event.PaymentID = nil
				event.ResourceID = nil
			}
		} else {
			product := false
			if event.Supported {
				product, err = validateStripeProductEvent(ctx, tx, event)
				if err != nil {
					return Receipt{}, err
				}
			}
			stripeProduct = product
			if event.Supported && !product {
				stripeBilling, err = bindStripeBillingWebhookTx(ctx, tx, event)
				if err != nil {
					return Receipt{}, err
				}
			}
			if !product && stripeBilling == nil && !s.webhookProviderEnabled(ctx, provider) {
				return Receipt{}, ErrDisabled
			}
			if event.Supported && !product && event.Purpose != nil && *event.Purpose == "product" {
				if !paymentExists {
					return Receipt{}, errProductPaymentUnknown
				}
				return Receipt{}, ErrInvalidEvent
			}
		}
	}
	var waffoBinding *waffoProductWebhookBinding
	var billingWaffoBinding *waffoBillingWebhookBinding
	if provider == "waffo_pancake" {
		waffoBinding, err = bindWaffoProductWebhookTx(ctx, tx, event)
		if errors.Is(err, pgx.ErrNoRows) && event.Purpose != nil && *event.Purpose == "product" {
			return Receipt{}, errProductPaymentUnknown
		}
		if err != nil {
			return Receipt{}, err
		}
		if waffoBinding == nil && event.Purpose != nil && *event.Purpose == "product" {
			return Receipt{}, ErrInvalidEvent
		}
		if waffoBinding == nil {
			billingWaffoBinding, err = bindWaffoBillingWebhookTx(ctx, tx, event)
			if err != nil {
				return Receipt{}, err
			}
		}
	}
	var eventID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_provider_events(
		  provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,
		  payment_id,resource_id,purpose,amount_cents,currency,payment_status,provider_payment_id,provider_charge_id,provider_transfer_id,destination_id,
		  destination_user_id,account_charges_enabled,account_payouts_enabled,account_details_submitted,account_requirements_due,refund_operation_id,
		  dispute_status,dispute_reason,dispute_network_reason_code,dispute_due_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)
		ON CONFLICT(provider,provider_event_id) DO NOTHING RETURNING id`,
		provider, event.ProviderEventID, event.EventType, event.APIVersion, event.LiveMode, event.OccurredAt, event.PayloadSHA256,
		event.ObjectID, event.ObjectType, event.PaymentID, event.ResourceID, event.Purpose, event.AmountCents, event.Currency,
		event.PaymentStatus, event.ProviderPaymentID, event.ProviderChargeID, event.ProviderTransferID, event.DestinationID,
		event.DestinationUserID, event.AccountChargesEnabled, event.AccountPayoutsEnabled, event.AccountDetailsSubmitted, event.AccountRequirementsDue,
		event.RefundOperationID, event.DisputeStatus, event.DisputeReason, event.DisputeNetworkReasonCode, event.DisputeDueBy,
	).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingHash, status string
		if err := tx.QueryRow(ctx, `
			SELECT e.id,e.payload_sha256,p.status FROM payment_provider_events e
			JOIN payment_provider_event_processing p ON p.event_id=e.id
			WHERE e.provider=$1 AND e.provider_event_id=$2`, provider, event.ProviderEventID).Scan(&eventID, &existingHash, &status); err != nil {
			return Receipt{}, err
		}
		if existingHash != event.PayloadSHA256 {
			return Receipt{}, ErrEventConflict
		}
		if stripeProduct {
			if err := verifyStoredStripeWebhookEventTx(ctx, tx, eventID, event); err != nil {
				return Receipt{}, err
			}
		}
		if stripeBilling != nil {
			if err := saveStripeBillingWebhookBindingTx(ctx, tx, eventID, *stripeBilling, event); err != nil {
				return Receipt{}, err
			}
		}
		if waffoBinding != nil {
			if err := saveWaffoProductWebhookBindingTx(ctx, tx, eventID, *waffoBinding, event); err != nil {
				return Receipt{}, err
			}
		}
		if billingWaffoBinding != nil {
			if err := saveWaffoBillingWebhookBindingTx(ctx, tx, eventID, *billingWaffoBinding, event); err != nil {
				return Receipt{}, err
			}
		}
		return Receipt{EventID: eventID, ProviderEventID: event.ProviderEventID, EventType: event.EventType, Status: status, Duplicate: true}, nil
	}
	if err != nil {
		return Receipt{}, err
	}
	if stripeBilling != nil {
		if err := saveStripeBillingWebhookBindingTx(ctx, tx, eventID, *stripeBilling, event); err != nil {
			return Receipt{}, err
		}
	}
	if waffoBinding != nil {
		if err := saveWaffoProductWebhookBindingTx(ctx, tx, eventID, *waffoBinding, event); err != nil {
			return Receipt{}, err
		}
	}
	if billingWaffoBinding != nil {
		if err := saveWaffoBillingWebhookBindingTx(ctx, tx, eventID, *billingWaffoBinding, event); err != nil {
			return Receipt{}, err
		}
	}
	status := "received"
	var processedAt *time.Time
	if !event.Supported {
		status = "ignored"
		now := time.Now().UTC()
		processedAt = &now
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_provider_event_processing(event_id,status,processed_at) VALUES($1,$2,$3)`, eventID, status, processedAt); err != nil {
		return Receipt{}, err
	}
	if event.Supported {
		if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('eventId',$2::text),8)`, PaymentEventJobKind, eventID); err != nil {
			return Receipt{}, err
		}
	}
	return Receipt{EventID: eventID, ProviderEventID: event.ProviderEventID, EventType: event.EventType, Status: status}, nil
}

func minimizeStripeEvent(rawBody []byte, apiVersion string, liveMode bool) (minimizedProviderEvent, error) {
	var envelope stripeEventEnvelope
	if json.Unmarshal(rawBody, &envelope) != nil || envelope.Object != "event" || !validStripeID(envelope.ID, "evt_") ||
		envelope.Created <= 0 || !regexp.MustCompile(`^[a-z0-9_.]{3,120}$`).MatchString(envelope.Type) ||
		!validStripeID(envelope.Data.Object.ID, objectPrefix(envelope.Data.Object.Object)) {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	if envelope.APIVersion != apiVersion {
		return minimizedProviderEvent{}, ErrVersionMismatch
	}
	if envelope.LiveMode != liveMode {
		return minimizedProviderEvent{}, ErrModeMismatch
	}
	// Outbound financial requests charge the platform directly; they never use
	// Stripe-Account/Stripe-Context. A Connect/organization event can share an
	// endpoint signature while belonging to a different merchant. Its metadata
	// must not authorize money or rights for a platform transaction.
	if supportedStripeEvents[envelope.Type] && envelope.Type != "account.updated" && (envelope.Account != "" || envelope.Context != "") {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	if envelope.Type == "account.updated" && (envelope.Context != "" || (envelope.Account != "" && envelope.Account != envelope.Data.Object.ID)) {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	hash := sha256.Sum256(rawBody)
	event := minimizedProviderEvent{
		ProviderEventID: envelope.ID, EventType: envelope.Type, APIVersion: envelope.APIVersion, LiveMode: envelope.LiveMode,
		OccurredAt: time.Unix(envelope.Created, 0).UTC(), PayloadSHA256: hex.EncodeToString(hash[:]), ObjectID: envelope.Data.Object.ID,
		ObjectType: envelope.Data.Object.Object, Supported: supportedStripeEvents[envelope.Type],
	}
	object := envelope.Data.Object
	if event.EventType == "refund.updated" {
		if value, present := object.Metadata["hcai_refund_operation_id"]; present {
			parsed, err := uuid.Parse(value)
			if err != nil || parsed == uuid.Nil {
				return minimizedProviderEvent{}, ErrInvalidEvent
			}
			event.RefundOperationID = &parsed
		}
	}
	if value := strings.TrimSpace(object.Metadata["hcai_payment_id"]); value != "" {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.PaymentID = &parsed
	}
	if value := strings.TrimSpace(object.Metadata["hcai_resource_id"]); value != "" {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.ResourceID = &parsed
	}
	if value := strings.TrimSpace(object.Metadata["hcai_user_id"]); value != "" {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.DestinationUserID = &parsed
	}
	if value := strings.TrimSpace(strings.ToLower(object.Metadata["hcai_purpose"])); value != "" {
		if !oneOf(value, "product", "task", "wallet_topup", "subscription") {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.Purpose = &value
	}
	if event.Supported && event.EventType != "account.updated" && !isStripeDisputeEvent(event.EventType) && event.PaymentID == nil {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	if isStripeDisputeEvent(event.EventType) {
		// Stripe dispute metadata is provider-controlled and is not a binding
		// authority. The worker resolves the local payment from the tuple.
		event.PaymentID, event.ResourceID, event.Purpose = nil, nil, nil
		if object.Object != "dispute" || !validStripeID(object.ID, "dp_") || !validStripeID(object.PaymentIntent, "pi_") || !validStripeID(object.Charge, "ch_") ||
			object.Amount == nil || object.Currency == "" || strings.TrimSpace(object.Status) == "" || strings.TrimSpace(object.Reason) == "" ||
			strings.TrimSpace(object.NetworkReasonCode) == "" || object.EvidenceDetails == nil || object.EvidenceDetails.DueBy == nil || *object.EvidenceDetails.DueBy <= 0 {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		if *object.Amount < 1 || *object.Amount > 99999999 || strings.ToUpper(strings.TrimSpace(object.Currency)) != "USD" {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		status := strings.ToLower(strings.TrimSpace(object.Status))
		reason := strings.ToLower(strings.TrimSpace(object.Reason))
		networkReason := strings.ToLower(strings.TrimSpace(object.NetworkReasonCode))
		if !safeStatusPattern.MatchString(status) || !safeStatusPattern.MatchString(reason) || !safeStatusPattern.MatchString(networkReason) {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		amount := *object.Amount
		currency := "USD"
		event.AmountCents, event.Currency, event.PaymentStatus = &amount, &currency, &status
		event.ProviderPaymentID, event.ProviderChargeID = stringPointer(object.PaymentIntent), stringPointer(object.Charge)
		event.DisputeStatus, event.DisputeReason, event.DisputeNetworkReasonCode = stringPointer(status), stringPointer(reason), stringPointer(networkReason)
		due := time.Unix(*object.EvidenceDetails.DueBy, 0).UTC()
		event.DisputeDueBy = &due
	}
	if event.EventType == "account.updated" {
		if object.Object != "account" || !validStripeID(object.ID, "acct_") || object.ChargesEnabled == nil || object.PayoutsEnabled == nil || object.DetailsSubmitted == nil {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		destinationID := object.ID
		event.DestinationID = &destinationID
		due := object.Requirements.unresolved()
		event.AccountChargesEnabled = object.ChargesEnabled
		event.AccountPayoutsEnabled = object.PayoutsEnabled
		event.AccountDetailsSubmitted = object.DetailsSubmitted
		event.AccountRequirementsDue = &due
	}
	event.AmountCents = object.AmountTotal
	if event.AmountCents == nil {
		event.AmountCents = object.Amount
	}
	if event.AmountCents == nil {
		event.AmountCents = object.AmountReceived
	}
	if event.AmountCents != nil && (*event.AmountCents < 0 || *event.AmountCents > 99999999) {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	if object.Currency != "" {
		currency := strings.ToUpper(strings.TrimSpace(object.Currency))
		if currency != "USD" {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.Currency = &currency
	}
	status := strings.TrimSpace(strings.ToLower(object.PaymentStatus))
	if status == "" {
		status = strings.TrimSpace(strings.ToLower(object.Status))
	}
	if status != "" {
		if !safeStatusPattern.MatchString(status) {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.PaymentStatus = &status
	}
	if object.Object == "payment_intent" {
		event.ProviderPaymentID = stringPointer(object.ID)
	} else if validStripeID(object.PaymentIntent, "pi_") {
		event.ProviderPaymentID = stringPointer(object.PaymentIntent)
	}
	if object.Object == "charge" {
		event.ProviderChargeID = stringPointer(object.ID)
	} else if validStripeID(object.Charge, "ch_") {
		event.ProviderChargeID = stringPointer(object.Charge)
	} else if validStripeID(object.LatestCharge, "ch_") {
		event.ProviderChargeID = stringPointer(object.LatestCharge)
	}
	if object.Object == "transfer" {
		event.ProviderTransferID = stringPointer(object.ID)
	}
	if object.Destination != "" {
		if !validStripeID(object.Destination, "acct_") {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.DestinationID = stringPointer(object.Destination)
	}
	return event, nil
}

func minimizeWaffoEvent(envelope waffoWebhookEnvelope, rawBody []byte, environment, expectedStoreID string) (minimizedProviderEvent, error) {
	eventType := strings.ToLower(strings.TrimSpace(envelope.EventType))
	// Waffo separates the first successful subscription payment from later
	// renewal payments. Both events retain the checkout metadata used to bind
	// the Provider order to the HCAI payment intent.
	supported := oneOf(eventType, "order.completed", "subscription.activated", "subscription.payment_succeeded", "refund.succeeded", "refund.failed")
	if len(strings.TrimSpace(envelope.ID)) < 8 || !safeProviderIDPattern.MatchString(strings.TrimSpace(envelope.ID)) ||
		!safeProviderIDPattern.MatchString(strings.TrimSpace(envelope.EventID)) ||
		strings.TrimSpace(envelope.Timestamp) == "" || !safeProviderIDPattern.MatchString(strings.TrimSpace(envelope.StoreID)) ||
		(strings.TrimSpace(expectedStoreID) != "" && envelope.StoreID != expectedStoreID) ||
		!oneOf(strings.ToLower(strings.TrimSpace(envelope.Mode)), "test", "prod") ||
		(strings.TrimSpace(environment) != "" && strings.ToLower(strings.TrimSpace(environment)) != strings.ToLower(strings.TrimSpace(envelope.Mode))) ||
		!supported {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	// These merchant-supplied fields are local UUIDs in our checkout contract.
	// Reject malformed values before they can become replayable typed evidence;
	// in particular, do not retain an email or arbitrary text as a buyer ID.
	for _, identity := range []string{envelope.Data.OrderMerchantExternalID, envelope.Data.MerchantProvidedBuyerIdentity} {
		value := strings.TrimSpace(identity)
		if value != "" {
			parsed, err := uuid.Parse(value)
			if err != nil || parsed == uuid.Nil || parsed.String() != value {
				return minimizedProviderEvent{}, ErrInvalidEvent
			}
		}
	}
	occurredAt, err := parseWaffoTime(envelope.Timestamp)
	if err != nil {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	if !safeProviderIDPattern.MatchString(strings.TrimSpace(envelope.Data.OrderID)) {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	objectID := strings.TrimSpace(envelope.Data.OrderID)
	objectType := "order"
	if eventType == "refund.succeeded" || eventType == "refund.failed" {
		objectID = strings.TrimSpace(envelope.Data.RefundTicketMerchantExternalID)
		if objectID == "" {
			objectID = strings.TrimSpace(envelope.EventID)
		}
		objectType = "refund"
	}
	if !safeProviderIDPattern.MatchString(objectID) {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	paymentIDValue := strings.TrimSpace(envelope.Data.OrderMetadata["hcaiPaymentId"])
	if paymentIDValue == "" {
		paymentIDValue = strings.TrimSpace(envelope.Data.OrderMetadata["hcai_payment_id"])
	}
	paymentID, err := uuid.Parse(paymentIDValue)
	if err != nil || paymentID == uuid.Nil {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	hash := sha256.Sum256(rawBody)
	mode := strings.EqualFold(strings.TrimSpace(envelope.Mode), "prod")
	event := minimizedProviderEvent{
		WaffoStoreID:         strings.TrimSpace(envelope.StoreID),
		WaffoOrderExternalID: strings.TrimSpace(envelope.Data.OrderMerchantExternalID),
		WaffoBuyerIdentity:   strings.TrimSpace(envelope.Data.MerchantProvidedBuyerIdentity),
		ProviderEventID:      strings.TrimSpace(envelope.ID), EventType: eventType, APIVersion: "waffo-pancake-v1", LiveMode: mode,
		OccurredAt: occurredAt, PayloadSHA256: hex.EncodeToString(hash[:]), ObjectID: objectID, ObjectType: objectType,
		PaymentID: &paymentID, Supported: true,
	}
	if value := strings.TrimSpace(envelope.Data.OrderMetadata["hcaiResourceId"]); value != "" {
		if parsed, parseErr := uuid.Parse(value); parseErr == nil && parsed != uuid.Nil {
			event.ResourceID = &parsed
		}
	}
	if value := strings.ToLower(strings.TrimSpace(envelope.Data.OrderMetadata["hcaiPurpose"])); value != "" {
		if !oneOf(value, "product", "task", "wallet_topup", "subscription") {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.Purpose = &value
	}
	if amount := strings.TrimSpace(envelope.Data.Amount); amount != "" {
		cents, parseErr := parseWaffoAmountCents(amount)
		if parseErr != nil {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.AmountCents = &cents
	}
	if currency := strings.ToUpper(strings.TrimSpace(envelope.Data.Currency)); currency != "" {
		if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(currency) {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.Currency = &currency
	}
	status := strings.ToLower(strings.TrimSpace(envelope.Data.PaymentStatus))
	if eventType == "refund.succeeded" || eventType == "refund.failed" {
		status = strings.ToLower(strings.TrimSpace(envelope.Data.RefundStatus))
	}
	if status != "" {
		if !safeStatusPattern.MatchString(status) {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.PaymentStatus = &status
	}
	if providerPaymentID := strings.TrimSpace(envelope.Data.PaymentID); providerPaymentID != "" {
		if !safeProviderIDPattern.MatchString(providerPaymentID) {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		event.ProviderPaymentID = &providerPaymentID
	}
	if oneOf(eventType, "order.completed", "subscription.activated", "subscription.payment_succeeded") && (event.AmountCents == nil || event.Currency == nil || *event.Currency != "USD" || event.PaymentStatus == nil || *event.PaymentStatus != "succeeded" || event.ProviderPaymentID == nil) {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	if (eventType == "refund.succeeded" || eventType == "refund.failed") && (event.AmountCents == nil || event.Currency == nil || *event.Currency != "USD" || event.ProviderPaymentID == nil || event.PaymentStatus == nil) {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	return event, nil
}

func parseWaffoAmountCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, errors.New("invalid amount")
	}
	whole, fraction, found := strings.Cut(value, ".")
	if !found {
		fraction = ""
	}
	if whole == "" || len(fraction) > 2 {
		return 0, errors.New("invalid amount")
	}
	for _, part := range []string{whole, fraction} {
		for _, char := range part {
			if char < '0' || char > '9' {
				return 0, errors.New("invalid amount")
			}
		}
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	major, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || major > 99999999 {
		return 0, errors.New("invalid amount")
	}
	minor := int64(0)
	if fraction != "" {
		minor, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, errors.New("invalid amount")
		}
	}
	result := major*100 + minor
	if result < 0 || result > 99999999 {
		return 0, errors.New("invalid amount")
	}
	return result, nil
}

func objectPrefix(objectType string) string {
	switch objectType {
	case "checkout.session":
		return "cs_"
	case "payment_intent":
		return "pi_"
	case "refund":
		return "re_"
	case "charge":
		return "ch_"
	case "transfer":
		return "tr_"
	case "account":
		return "acct_"
	case "dispute":
		return "dp_"
	default:
		return "never_"
	}
}

func stringPointer(value string) *string {
	value = strings.TrimSpace(value)
	return &value
}
