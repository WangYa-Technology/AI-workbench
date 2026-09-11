package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var safeStatusPattern = regexp.MustCompile(`^[a-z0-9_]{2,80}$`)
var safeProviderIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,255}$`)

var waffoWebhookClient = &http.Client{Timeout: 15 * time.Second}

var supportedStripeEvents = map[string]bool{
	"checkout.session.completed":               true,
	"checkout.session.async_payment_succeeded": true,
	"checkout.session.async_payment_failed":    true,
	"payment_intent.payment_failed":            true,
	"payment_intent.succeeded":                 true,
	"refund.updated":                           true,
	"transfer.created":                         true,
	"account.updated":                          true,
}

type ServiceConfig struct {
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
	pool     *pgxpool.Pool
	config   ServiceConfig
	verifier StripeWebhookVerifier
	runtimes *RuntimeCatalog
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
	ID               string            `json:"id"`
	Object           string            `json:"object"`
	Status           string            `json:"status"`
	PaymentStatus    string            `json:"payment_status"`
	AmountTotal      *int64            `json:"amount_total"`
	Amount           *int64            `json:"amount"`
	AmountReceived   *int64            `json:"amount_received"`
	Currency         string            `json:"currency"`
	PaymentIntent    string            `json:"payment_intent"`
	Charge           string            `json:"charge"`
	LatestCharge     string            `json:"latest_charge"`
	Destination      string            `json:"destination"`
	Metadata         map[string]string `json:"metadata"`
	ChargesEnabled   *bool             `json:"charges_enabled"`
	PayoutsEnabled   *bool             `json:"payouts_enabled"`
	DetailsSubmitted *bool             `json:"details_submitted"`
	Requirements     struct {
		CurrentlyDue        []string `json:"currently_due"`
		PastDue             []string `json:"past_due"`
		PendingVerification []string `json:"pending_verification"`
		DisabledReason      string   `json:"disabled_reason"`
	} `json:"requirements"`
}

type minimizedProviderEvent struct {
	ProviderEventID         string
	EventType               string
	APIVersion              string
	LiveMode                bool
	OccurredAt              time.Time
	PayloadSHA256           string
	ObjectID                string
	ObjectType              string
	PaymentID               *uuid.UUID
	ResourceID              *uuid.UUID
	Purpose                 *string
	AmountCents             *int64
	Currency                *string
	PaymentStatus           *string
	ProviderPaymentID       *string
	ProviderChargeID        *string
	ProviderTransferID      *string
	DestinationID           *string
	DestinationUserID       *uuid.UUID
	AccountChargesEnabled   *bool
	AccountPayoutsEnabled   *bool
	AccountDetailsSubmitted *bool
	AccountRequirementsDue  *bool
	Supported               bool
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
	if !s.webhookProviderEnabled(ctx, "stripe") {
		return Receipt{}, ErrDisabled
	}
	if err := s.verifier.Verify(rawBody, signatureHeader); err != nil {
		return Receipt{}, err
	}
	event, err := minimizeStripeEvent(rawBody, s.config.APIVersion, s.config.LiveMode)
	if err != nil {
		return Receipt{}, err
	}
	return s.receiveProviderEvent(ctx, "stripe", event)
}

// ReceiveWaffoWebhook verifies a raw Waffo body through the local connector,
// which is the process that owns the Pancake private key and SDK verifier.
func (s *Service) ReceiveWaffoWebhook(ctx context.Context, rawBody []byte, signatureHeader string) (Receipt, error) {
	if !s.webhookProviderEnabled(ctx, "waffo_pancake") {
		return Receipt{}, ErrDisabled
	}
	waffoEnvironment, waffoStoreID := s.waffoWebhookSettings(ctx)
	if strings.TrimSpace(waffoEnvironment) == "" || strings.TrimSpace(waffoStoreID) == "" {
		return Receipt{}, ErrProviderConfigMismatch
	}
	if len(rawBody) == 0 || int64(len(rawBody)) > maxWaffoResponseBytes || strings.TrimSpace(signatureHeader) == "" ||
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
		Event waffoWebhookEnvelope `json:"event"`
	}
	if json.Unmarshal(data, &verified) != nil {
		return Receipt{}, ErrInvalidEvent
	}
	event, err := minimizeWaffoEvent(verified.Event, rawBody, waffoEnvironment, waffoStoreID)
	if err != nil {
		return Receipt{}, err
	}
	return s.receiveProviderEvent(ctx, "waffo_pancake", event)
}

// webhookProviderEnabled mirrors the product-provider selection boundary. A
// deployment can register more than one runtime, while Admin chooses the one
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
	defer func() { _ = tx.Rollback(ctx) }()

	var eventID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_provider_events(
		  provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,
		  payment_id,resource_id,purpose,amount_cents,currency,payment_status,provider_payment_id,provider_charge_id,provider_transfer_id,destination_id,
		  destination_user_id,account_charges_enabled,account_payouts_enabled,account_details_submitted,account_requirements_due
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
		ON CONFLICT(provider,provider_event_id) DO NOTHING RETURNING id`,
		provider, event.ProviderEventID, event.EventType, event.APIVersion, event.LiveMode, event.OccurredAt, event.PayloadSHA256,
		event.ObjectID, event.ObjectType, event.PaymentID, event.ResourceID, event.Purpose, event.AmountCents, event.Currency,
		event.PaymentStatus, event.ProviderPaymentID, event.ProviderChargeID, event.ProviderTransferID, event.DestinationID,
		event.DestinationUserID, event.AccountChargesEnabled, event.AccountPayoutsEnabled, event.AccountDetailsSubmitted, event.AccountRequirementsDue,
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
		if err := tx.Commit(ctx); err != nil {
			return Receipt{}, err
		}
		return Receipt{EventID: eventID, ProviderEventID: event.ProviderEventID, EventType: event.EventType, Status: status, Duplicate: true}, nil
	}
	if err != nil {
		return Receipt{}, err
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
	if err := tx.Commit(ctx); err != nil {
		return Receipt{}, err
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
	hash := sha256.Sum256(rawBody)
	event := minimizedProviderEvent{
		ProviderEventID: envelope.ID, EventType: envelope.Type, APIVersion: envelope.APIVersion, LiveMode: envelope.LiveMode,
		OccurredAt: time.Unix(envelope.Created, 0).UTC(), PayloadSHA256: hex.EncodeToString(hash[:]), ObjectID: envelope.Data.Object.ID,
		ObjectType: envelope.Data.Object.Object, Supported: supportedStripeEvents[envelope.Type],
	}
	object := envelope.Data.Object
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
	if event.Supported && event.EventType != "account.updated" && event.PaymentID == nil {
		return minimizedProviderEvent{}, ErrInvalidEvent
	}
	if event.EventType == "account.updated" {
		if object.Object != "account" || !validStripeID(object.ID, "acct_") || object.ChargesEnabled == nil || object.PayoutsEnabled == nil || object.DetailsSubmitted == nil {
			return minimizedProviderEvent{}, ErrInvalidEvent
		}
		destinationID := object.ID
		event.DestinationID = &destinationID
		due := len(object.Requirements.CurrentlyDue) > 0 || len(object.Requirements.PastDue) > 0 || len(object.Requirements.PendingVerification) > 0 || strings.TrimSpace(object.Requirements.DisabledReason) != ""
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
		strings.TrimSpace(envelope.Timestamp) == "" || strings.TrimSpace(envelope.StoreID) == "" ||
		(strings.TrimSpace(expectedStoreID) != "" && envelope.StoreID != expectedStoreID) ||
		!oneOf(strings.ToLower(strings.TrimSpace(envelope.Mode)), "test", "prod") ||
		(strings.TrimSpace(environment) != "" && strings.ToLower(strings.TrimSpace(environment)) != strings.ToLower(strings.TrimSpace(envelope.Mode))) ||
		!supported {
		return minimizedProviderEvent{}, ErrInvalidEvent
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
		ProviderEventID: strings.TrimSpace(envelope.ID), EventType: eventType, APIVersion: "waffo-pancake-v1", LiveMode: mode,
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
	default:
		return "never_"
	}
}

func stringPointer(value string) *string {
	value = strings.TrimSpace(value)
	return &value
}
