package payments

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// The caller supplies only IDs already stored with the local transaction.
// No user-entered merchant, transaction or callback is evidence of ownership.
type ProductPaymentBinding struct {
	PaymentID          uuid.UUID `json:"paymentId"`
	ResourceID         uuid.UUID `json:"resourceId"`
	OrderID            uuid.UUID `json:"orderId"`
	BuyerID            uuid.UUID `json:"buyerId"`
	AmountCents        int       `json:"amountCents"`
	Currency           string    `json:"currency"`
	LiveMode           bool      `json:"liveMode"`
	ProviderCheckoutID string    `json:"providerCheckoutId"`
	ProviderPaymentID  string    `json:"providerPaymentId"`
	ProviderChargeID   string    `json:"providerChargeId"`
}

type ProductPaymentIdentityObservation struct {
	Checkout *CheckoutObservation             `json:"checkout,omitempty"`
	Payment  *StripeProductPaymentObservation `json:"payment,omitempty"`
}
type StripeProductPaymentObservation struct {
	ProviderPaymentID string `json:"providerPaymentId"`
	ProviderChargeID  string `json:"providerChargeId"`
	AmountCents       int    `json:"amountCents"`
	Currency          string `json:"currency"`
	LiveMode          bool   `json:"liveMode"`
	Status            string `json:"status"`
}
type productPaymentIdentityReader interface {
	ReadProductPaymentIdentity(context.Context, ProductPaymentBinding) (ProductPaymentIdentityObservation, error)
}

func (r *StripeRuntime) ReadProductPaymentIdentity(ctx context.Context, input ProductPaymentBinding) (ProductPaymentIdentityObservation, error) {
	invalid := func() (ProductPaymentIdentityObservation, error) {
		return ProductPaymentIdentityObservation{}, newProviderFailure("payment_response_invalid", 0)
	}
	if r == nil || input.PaymentID == uuid.Nil || input.ResourceID == uuid.Nil || input.OrderID == uuid.Nil || input.BuyerID == uuid.Nil || input.AmountCents < 50 || input.Currency != "USD" || input.LiveMode != r.config.LiveMode {
		return invalid()
	}
	if input.ProviderCheckoutID != "" {
		result, err := r.ReadProductCheckout(ctx, CheckoutReadRequest{PaymentID: input.PaymentID, ResourceID: input.ResourceID, ProviderCheckoutID: input.ProviderCheckoutID, AmountCents: input.AmountCents, Currency: input.Currency, LiveMode: input.LiveMode})
		if err != nil {
			return ProductPaymentIdentityObservation{}, err
		}
		if (input.ProviderPaymentID != "" && input.ProviderPaymentID != result.ProviderPaymentID) || (input.ProviderChargeID != "" && input.ProviderChargeID != result.ProviderChargeID) {
			return invalid()
		}
		return ProductPaymentIdentityObservation{Checkout: &result}, nil
	}
	if !validStripeID(input.ProviderPaymentID, "pi_") {
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
	if err := r.getJSON(ctx, "/payment_intents/"+url.PathEscape(input.ProviderPaymentID), &intent); err != nil {
		return ProductPaymentIdentityObservation{}, err
	}
	if intent.ID != input.ProviderPaymentID || intent.Object != "payment_intent" || intent.Status != "succeeded" || intent.Amount == nil || *intent.Amount != input.AmountCents || intent.Received == nil || *intent.Received != input.AmountCents || strings.ToUpper(intent.Currency) != input.Currency || intent.LiveMode == nil || *intent.LiveMode != input.LiveMode || !validStripeID(intent.Charge, "ch_") || (input.ProviderChargeID != "" && input.ProviderChargeID != intent.Charge) || intent.Metadata["hcai_payment_id"] != input.PaymentID.String() || intent.Metadata["hcai_resource_id"] != input.ResourceID.String() || intent.Metadata["hcai_purpose"] != "product" {
		return invalid()
	}
	return ProductPaymentIdentityObservation{Payment: &StripeProductPaymentObservation{ProviderPaymentID: intent.ID, ProviderChargeID: intent.Charge, AmountCents: *intent.Amount, Currency: input.Currency, LiveMode: *intent.LiveMode, Status: intent.Status}}, nil
}
