package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// RefundReader is optional. Querying never creates, retries or cancels money
// movements. Unsupported providers must not advertise this capability.
// A non-nil error may accompany independently verified observations. Callers
// must retain them as incomplete evidence, never as proof of a complete list.
type RefundReader interface {
	ReadProductRefunds(context.Context, RefundReadRequest) ([]RefundObservation, error)
}
type RefundReadRequest struct {
	PaymentID         uuid.UUID
	ResourceID        uuid.UUID
	ProviderPaymentID string
	AmountCents       int
	Currency          string
	LiveMode          bool
}
type RefundObservation struct {
	ProviderID        string     `json:"providerId"`
	ProviderPaymentID string     `json:"providerPaymentId"`
	AmountCents       int        `json:"amountCents"`
	Currency          string     `json:"currency"`
	Status            string     `json:"status"`
	OperationID       *uuid.UUID `json:"operationId,omitempty"`
}

func (r *StripeRuntime) ReadProductRefunds(ctx context.Context, input RefundReadRequest) ([]RefundObservation, error) {
	if r == nil || input.PaymentID == uuid.Nil || input.ResourceID == uuid.Nil || !validStripeID(input.ProviderPaymentID, "pi_") || input.Currency != "USD" || input.AmountCents < 1 || input.LiveMode != r.config.LiveMode {
		return nil, newProviderFailure("payment_invalid_request", 0)
	}
	var payment struct {
		ID       string            `json:"id"`
		Amount   int               `json:"amount"`
		Received int               `json:"amount_received"`
		Currency string            `json:"currency"`
		LiveMode *bool             `json:"livemode"`
		Status   string            `json:"status"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := r.getJSON(ctx, "/payment_intents/"+url.PathEscape(input.ProviderPaymentID), &payment); err != nil {
		return nil, err
	}
	if payment.ID != input.ProviderPaymentID || payment.Amount != input.AmountCents || payment.Received != input.AmountCents || strings.ToUpper(payment.Currency) != input.Currency || payment.LiveMode == nil || *payment.LiveMode != input.LiveMode || payment.Status != "succeeded" || payment.Metadata["hcai_payment_id"] != input.PaymentID.String() || payment.Metadata["hcai_resource_id"] != input.ResourceID.String() || payment.Metadata["hcai_purpose"] != "product" {
		return nil, newProviderFailure("payment_response_invalid", 0)
	}
	result := []RefundObservation{}
	seen := map[string]bool{}
	cursor := ""
	// Keep the verified prefix on later failure. It proves these refunds were
	// observed, not that another refund does not exist. More than 1,000 entries
	// require review; no incomplete result is returned without an error.
	for page := 0; page < 10; page++ {
		query := url.Values{"payment_intent": {input.ProviderPaymentID}, "limit": {"100"}}
		if cursor != "" {
			query.Set("starting_after", cursor)
		}
		var response struct {
			Object string `json:"object"`
			More   *bool  `json:"has_more"`
			Data   []struct {
				ID       string            `json:"id"`
				Payment  string            `json:"payment_intent"`
				Amount   int               `json:"amount"`
				Currency string            `json:"currency"`
				Status   string            `json:"status"`
				Metadata map[string]string `json:"metadata"`
			} `json:"data"`
		}
		if err := r.getJSON(ctx, "/refunds?"+query.Encode(), &response); err != nil {
			return result, err
		}
		if response.Object != "list" || response.More == nil || response.Data == nil || len(response.Data) > 100 || *response.More && len(response.Data) == 0 {
			return result, newProviderFailure("payment_response_invalid", 0)
		}
		for _, item := range response.Data {
			if !validStripeID(item.ID, "re_") || seen[item.ID] || item.Payment != input.ProviderPaymentID || item.Amount < 1 || item.Amount > input.AmountCents || strings.ToUpper(item.Currency) != input.Currency || !oneOf(item.Status, "pending", "requires_action", "succeeded", "failed", "canceled") {
				return result, newProviderFailure("payment_response_invalid", 0)
			}
			if value, present := item.Metadata["hcai_payment_id"]; present && value != input.PaymentID.String() {
				return result, newProviderFailure("payment_response_invalid", 0)
			}
			var operation *uuid.UUID
			if value, present := item.Metadata["hcai_refund_operation_id"]; present {
				id, err := uuid.Parse(value)
				if err != nil || id == uuid.Nil {
					return result, newProviderFailure("payment_response_invalid", 0)
				}
				operation = &id
			}
			seen[item.ID] = true
			cursor = item.ID
			result = append(result, RefundObservation{ProviderID: item.ID, ProviderPaymentID: item.Payment, AmountCents: item.Amount, Currency: input.Currency, Status: item.Status, OperationID: operation})
		}
		if !*response.More {
			return result, nil
		}
	}
	return result, newProviderFailure("payment_response_invalid", 0)
}

func (r *StripeRuntime) getJSON(ctx context.Context, path string, destination any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.config.BaseURL+path, nil)
	if err != nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	req.Header.Set("Authorization", "Bearer "+r.config.SecretKey)
	req.Header.Set("Stripe-Version", r.config.APIVersion)
	response, err := r.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return newProviderFailure("payment_timeout", 0)
		}
		return newProviderFailure("payment_request_failed", 0)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyStripeStatus(response)
	}
	body, tooLarge, err := readStripeBounded(response.Body, maxStripeResponseBytes)
	if err != nil {
		return newProviderFailure("payment_request_failed", 0)
	}
	if tooLarge || json.Unmarshal(body, destination) != nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	return nil
}
