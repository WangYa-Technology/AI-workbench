package payments

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CheckoutReader only observes an existing session. It cannot open, expire or
// charge a session; the provider's terminal status is the authority for closure.
type CheckoutReader interface {
	ReadProductCheckout(context.Context, CheckoutReadRequest) (CheckoutObservation, error)
}

type CheckoutReadRequest struct {
	PaymentID          uuid.UUID
	ResourceID         uuid.UUID
	ProviderCheckoutID string
	AmountCents        int
	Currency           string
	LiveMode           bool
}

type CheckoutObservation struct {
	CheckoutURL        string    `json:"-"` // Buyer-only recovery URL; excluded from public/audit evidence.
	ProviderCheckoutID string    `json:"providerCheckoutId"`
	Status             string    `json:"status"`
	PaymentStatus      string    `json:"paymentStatus"`
	ProviderPaymentID  string    `json:"providerPaymentId,omitempty"`
	ProviderChargeID   string    `json:"providerChargeId,omitempty"`
	IntentStatus       string    `json:"intentStatus,omitempty"`
	AmountReceived     int       `json:"amountReceived"`
	AmountCents        int       `json:"amountCents"`
	Currency           string    `json:"currency"`
	LiveMode           bool      `json:"liveMode"`
	ExpiresAt          time.Time `json:"expiresAt"`
}

func (r *StripeRuntime) ReadProductCheckout(ctx context.Context, input CheckoutReadRequest) (CheckoutObservation, error) {
	invalid := func() (CheckoutObservation, error) {
		return CheckoutObservation{}, newProviderFailure("payment_response_invalid", 0)
	}
	if r == nil || input.PaymentID == uuid.Nil || input.ResourceID == uuid.Nil || !validStripeID(input.ProviderCheckoutID, "cs_") || input.AmountCents < 50 || input.Currency != "USD" || input.LiveMode != r.config.LiveMode {
		return CheckoutObservation{}, newProviderFailure("payment_invalid_request", 0)
	}
	var session struct {
		URL               string            `json:"url"`
		ID                string            `json:"id"`
		Object            string            `json:"object"`
		Mode              string            `json:"mode"`
		Status            string            `json:"status"`
		PaymentStatus     string            `json:"payment_status"`
		PaymentIntent     string            `json:"payment_intent"`
		Amount            *int              `json:"amount_total"`
		Currency          string            `json:"currency"`
		LiveMode          *bool             `json:"livemode"`
		ExpiresAt         int64             `json:"expires_at"`
		ClientReferenceID string            `json:"client_reference_id"`
		Metadata          map[string]string `json:"metadata"`
	}
	if err := r.getJSON(ctx, "/checkout/sessions/"+url.PathEscape(input.ProviderCheckoutID), &session); err != nil {
		return CheckoutObservation{}, err
	}
	metadataMatches := func(m map[string]string) bool {
		return m["hcai_payment_id"] == input.PaymentID.String() && m["hcai_resource_id"] == input.ResourceID.String() && m["hcai_purpose"] == "product"
	}
	if session.ID != input.ProviderCheckoutID || session.Object != "checkout.session" || session.Mode != "payment" || !oneOf(session.Status, "open", "complete", "expired") || !oneOf(session.PaymentStatus, "paid", "unpaid") || session.Amount == nil || *session.Amount != input.AmountCents || strings.ToUpper(session.Currency) != input.Currency || session.LiveMode == nil || *session.LiveMode != input.LiveMode || session.ExpiresAt <= 0 || session.ClientReferenceID != input.PaymentID.String() || !metadataMatches(session.Metadata) {
		return invalid()
	}
	observation := CheckoutObservation{CheckoutURL: session.URL, ProviderCheckoutID: session.ID, Status: session.Status, PaymentStatus: session.PaymentStatus, ProviderPaymentID: session.PaymentIntent, AmountCents: *session.Amount, Currency: input.Currency, LiveMode: *session.LiveMode, ExpiresAt: time.Unix(session.ExpiresAt, 0).UTC()}
	if session.PaymentIntent != "" {
		if !validStripeID(session.PaymentIntent, "pi_") {
			return invalid()
		}
		var intent struct {
			ID       string            `json:"id"`
			Object   string            `json:"object"`
			Status   string            `json:"status"`
			Amount   *int              `json:"amount"`
			Received *int              `json:"amount_received"`
			Currency string            `json:"currency"`
			LiveMode *bool             `json:"livemode"`
			Charge   string            `json:"latest_charge"`
			Metadata map[string]string `json:"metadata"`
		}
		if err := r.getJSON(ctx, "/payment_intents/"+url.PathEscape(session.PaymentIntent), &intent); err != nil {
			return CheckoutObservation{}, err
		}
		if intent.ID != session.PaymentIntent || intent.Object != "payment_intent" || intent.Amount == nil || *intent.Amount != input.AmountCents || intent.Received == nil || *intent.Received < 0 || *intent.Received > input.AmountCents || strings.ToUpper(intent.Currency) != input.Currency || intent.LiveMode == nil || *intent.LiveMode != input.LiveMode || !metadataMatches(intent.Metadata) || !oneOf(intent.Status, "requires_payment_method", "requires_confirmation", "requires_action", "processing", "requires_capture", "canceled", "succeeded") || (intent.Charge != "" && !validStripeID(intent.Charge, "ch_")) {
			return invalid()
		}
		observation.IntentStatus, observation.AmountReceived, observation.ProviderChargeID = intent.Status, *intent.Received, intent.Charge
	}
	if !validCheckoutObservation(input, observation) {
		return invalid()
	}
	return observation, nil
}

func validCheckoutObservation(input CheckoutReadRequest, value CheckoutObservation) bool {
	return validCheckoutObservationForProvider(input, value, "stripe")
}

func validCheckoutObservationForProvider(input CheckoutReadRequest, value CheckoutObservation, provider string) bool {
	if value.ProviderCheckoutID != input.ProviderCheckoutID || value.AmountCents != input.AmountCents || value.Currency != input.Currency || value.LiveMode != input.LiveMode || value.ExpiresAt.IsZero() || !oneOf(value.Status, "open", "complete", "expired") || !oneOf(value.PaymentStatus, "paid", "unpaid") {
		return false
	}
	// Waffo's lookup contract proves a completed order, not a Stripe intent or
	// charge. Unknown/unpaid Waffo results cannot authorize expiry or fulfillment.
	if provider == "waffo_pancake" {
		return strings.TrimSpace(value.ProviderCheckoutID) != "" && len(value.ProviderCheckoutID) <= 255 &&
			value.ProviderCheckoutID == strings.TrimSpace(value.ProviderCheckoutID) &&
			strings.TrimSpace(value.ProviderPaymentID) != "" && len(value.ProviderPaymentID) <= 255 &&
			value.ProviderPaymentID == strings.TrimSpace(value.ProviderPaymentID) &&
			value.ProviderChargeID == "" && value.IntentStatus == "" && value.Status == "complete" &&
			value.PaymentStatus == "paid" && input.AmountCents >= 50 && value.AmountReceived == input.AmountCents
	}
	if provider != "stripe" {
		return false
	}
	if value.ProviderPaymentID == "" {
		return value.PaymentStatus == "unpaid" && value.Status != "complete" && value.IntentStatus == "" && value.AmountReceived == 0 && value.ProviderChargeID == ""
	}
	if !validStripeID(value.ProviderPaymentID, "pi_") || (value.ProviderChargeID != "" && !validStripeID(value.ProviderChargeID, "ch_")) || !oneOf(value.IntentStatus, "requires_payment_method", "requires_confirmation", "requires_action", "processing", "requires_capture", "canceled", "succeeded") || value.AmountReceived < 0 || value.AmountReceived > input.AmountCents {
		return false
	}
	if value.PaymentStatus == "paid" {
		return value.Status == "complete" && value.IntentStatus == "succeeded" && value.AmountReceived == input.AmountCents && value.ProviderChargeID != ""
	}
	// A session and its intent can change between the two GETs. Do not use a
	// contradictory pair to release the active checkout or issue delivery.
	return value.IntentStatus != "succeeded" && value.AmountReceived == 0
}

func (o CheckoutObservation) safelyExpired() bool {
	return o.Status == "expired" && o.PaymentStatus == "unpaid" && o.AmountReceived == 0 && (o.ProviderPaymentID == "" || o.IntentStatus == "canceled")
}
