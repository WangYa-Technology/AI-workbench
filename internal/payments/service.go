package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var safeStatusPattern = regexp.MustCompile(`^[a-z0-9_]{2,80}$`)

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
	Enabled          bool
	LiveMode         bool
	APIVersion       string
	WebhookSecret    string
	WebhookTolerance time.Duration
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
	return &Service{pool: pool, config: config, verifier: NewStripeWebhookVerifier(config.WebhookSecret, config.WebhookTolerance), runtimes: runtimes}
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
		) VALUES('stripe',$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
		ON CONFLICT(provider,provider_event_id) DO NOTHING RETURNING id`,
		event.ProviderEventID, event.EventType, event.APIVersion, event.LiveMode, event.OccurredAt, event.PayloadSHA256,
		event.ObjectID, event.ObjectType, event.PaymentID, event.ResourceID, event.Purpose, event.AmountCents, event.Currency,
		event.PaymentStatus, event.ProviderPaymentID, event.ProviderChargeID, event.ProviderTransferID, event.DestinationID,
		event.DestinationUserID, event.AccountChargesEnabled, event.AccountPayoutsEnabled, event.AccountDetailsSubmitted, event.AccountRequirementsDue,
	).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingHash, status string
		if err := tx.QueryRow(ctx, `
			SELECT e.id,e.payload_sha256,p.status FROM payment_provider_events e
			JOIN payment_provider_event_processing p ON p.event_id=e.id
			WHERE e.provider='stripe' AND e.provider_event_id=$1`, event.ProviderEventID).Scan(&eventID, &existingHash, &status); err != nil {
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
		if !oneOf(value, "product", "task") {
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
