package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Stop well inside Stripe's minimum 24-hour idempotency retention window.
const stripePayoutDispatchWindow = 23 * time.Hour

// CreatePayout moves an already funded connected balance to one frozen bank
// destination. Funding, reservations and uncertain-result recovery belong to
// the caller's durable workflow, not to this adapter.
func (r *StripeRuntime) CreatePayout(ctx context.Context, input PayoutRequest) (Payout, error) {
	if err := r.validatePayoutRequest(input); err != nil {
		return Payout{}, err
	}
	deadline := input.ReservedAt.Add(stripePayoutDispatchWindow)
	if !time.Now().Before(deadline) {
		return Payout{}, newProviderFailure("payment_reconciliation_required", 0)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := r.authenticatePayoutMerchant(ctx, input); err != nil {
		return Payout{}, err
	}
	if _, err := r.readPayoutReadiness(ctx, input); err != nil {
		return Payout{}, err
	}
	if !time.Now().Before(deadline) || ctx.Err() != nil {
		return Payout{}, newProviderFailure("payment_reconciliation_required", 0)
	}
	form := url.Values{
		"amount":                           {strconv.Itoa(input.AmountCents)},
		"currency":                         {"usd"},
		"destination":                      {input.BankDestinationID},
		"method":                           {"standard"},
		"metadata[hcai_payout_request_id]": {input.PayoutRequestID.String()},
	}
	var raw stripePayoutResponse
	if err := r.payoutJSON(ctx, input, http.MethodPost, "/payouts", form, &raw); err != nil {
		return Payout{}, err
	}
	return raw.observation(input)
}

func (r *StripeRuntime) validatePayoutRequest(input PayoutRequest) error {
	if r == nil || input.PayoutRequestID == uuid.Nil || !validStripeID(input.DestinationID, "acct_") ||
		!validStripeID(input.BankDestinationID, "ba_") || input.AmountCents < 1 || input.AmountCents > 99999999 ||
		input.Currency != "USD" || len(input.IdempotencyKey) < 8 || len(input.IdempotencyKey) > 160 ||
		input.ReservedAt.IsZero() || input.ReservedAt.Unix() <= 0 || input.ReservedAt.After(time.Now()) {
		return newProviderFailure("payment_invalid_request", 0)
	}
	for _, c := range input.IdempotencyKey {
		if c < 33 || c > 126 {
			return newProviderFailure("payment_invalid_request", 0)
		}
	}
	return r.validatePayoutDestination(input)
}

func (r *StripeRuntime) validatePayoutDestination(input PayoutRequest) error {
	if r == nil || !validStripeID(input.DestinationID, "acct_") ||
		!validStripeID(input.BankDestinationID, "ba_") || input.Currency != "USD" {
		return newProviderFailure("payment_invalid_request", 0)
	}
	return r.validatePayoutAccount(input)
}

func (r *StripeRuntime) validatePayoutAccount(input PayoutRequest) error {
	if r == nil || !validStripeID(input.DestinationID, "acct_") || input.Currency != "USD" {
		return newProviderFailure("payment_invalid_request", 0)
	}
	expected := input.Identity
	if expected == nil || expected.Provider != r.Provider() || !validStripeID(expected.MerchantID, "acct_") ||
		input.DestinationID == expected.MerchantID ||
		expected.StoreID != "" || expected.LiveMode != r.config.LiveMode || expected.Endpoint != r.config.BaseURL ||
		expected.APIVersion != r.config.APIVersion || expected.RequestVersion != stripeProductCheckoutVersion {
		return newProviderFailure("payment_reconciliation_required", 0)
	}
	return nil
}

func (r *StripeRuntime) authenticatePayoutMerchant(ctx context.Context, input PayoutRequest) error {
	// Identity reads use the platform getJSON helper rather than payoutJSON.
	// Bound them independently even when the caller/client has no timeout.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	identity, err := checkoutIdentity(ctx, r)
	if err != nil {
		return err
	}
	if identity != *input.Identity {
		return newProviderFailure("payment_reconciliation_required", 0)
	}
	return nil
}

func (r *StripeRuntime) ReadPayout(ctx context.Context, input PayoutRequest, providerID string) (Payout, error) {
	if err := r.validatePayoutRequest(input); err != nil {
		return Payout{}, err
	}
	if !validStripeID(providerID, "po_") {
		return Payout{}, newProviderFailure("payment_invalid_request", 0)
	}
	if err := r.authenticatePayoutMerchant(ctx, input); err != nil {
		return Payout{}, err
	}
	var raw stripePayoutResponse
	if err := r.payoutJSON(ctx, input, http.MethodGet, "/payouts/"+providerID, nil, &raw); err != nil {
		return Payout{}, err
	}
	if raw.ID != providerID {
		return Payout{}, newProviderFailure("payment_response_invalid", 0)
	}
	return raw.observation(input)
}

func (r *StripeRuntime) LookupPayout(ctx context.Context, input PayoutRequest) (PayoutLookupResult, error) {
	result := PayoutLookupResult{Outcome: "incomplete", Observations: []Payout{}}
	if err := r.validatePayoutRequest(input); err != nil {
		return result, err
	}
	if err := r.authenticatePayoutMerchant(ctx, input); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	cursor := ""
	for range 10 {
		query := url.Values{"limit": {"100"},
			"created[gte]": {strconv.FormatInt(input.ReservedAt.Add(-5*time.Minute).Unix(), 10)},
			"created[lte]": {strconv.FormatInt(input.ReservedAt.Add(stripePayoutDispatchWindow+5*time.Minute).Unix(), 10)}}
		if cursor != "" {
			query.Set("starting_after", cursor)
		}
		var response struct {
			Object string                 `json:"object"`
			More   *bool                  `json:"has_more"`
			Data   []stripePayoutResponse `json:"data"`
		}
		if err := r.payoutJSON(ctx, input, http.MethodGet, "/payouts?"+query.Encode(), nil, &response); err != nil {
			return result, err
		}
		if response.Object != "list" || response.More == nil || response.Data == nil || len(response.Data) > 100 ||
			(*response.More && len(response.Data) == 0) {
			return result, newProviderFailure("payment_response_invalid", 0)
		}
		result.Pages++
		for _, raw := range response.Data {
			if !raw.validEnvelope(input) || seen[raw.ID] {
				return result, newProviderFailure("payment_response_invalid", 0)
			}
			seen[raw.ID], cursor = true, raw.ID
			if raw.Metadata["hcai_payout_request_id"] != input.PayoutRequestID.String() {
				continue
			}
			item, err := raw.observation(input)
			if err != nil {
				return result, err
			}
			result.Observations = append(result.Observations, item)
			if len(result.Observations) > 1 {
				result.Outcome = "ambiguous"
				return result, nil
			}
		}
		if !*response.More {
			result.Outcome = "not_found"
			if len(result.Observations) == 1 {
				result.Outcome = "found"
			}
			return result, nil
		}
	}
	return result, nil
}

type stripePayoutResponse struct {
	ID          string            `json:"id"`
	Object      string            `json:"object"`
	Destination string            `json:"destination"`
	Amount      int               `json:"amount"`
	Currency    string            `json:"currency"`
	Status      string            `json:"status"`
	LiveMode    *bool             `json:"livemode"`
	Automatic   *bool             `json:"automatic"`
	Created     *int64            `json:"created"`
	Method      string            `json:"method"`
	Type        string            `json:"type"`
	FailureCode string            `json:"failure_code"`
	Metadata    map[string]string `json:"metadata"`
}

func (raw stripePayoutResponse) validEnvelope(input PayoutRequest) bool {
	return raw.Object == "payout" && validStripeID(raw.ID, "po_") && raw.LiveMode != nil &&
		*raw.LiveMode == input.Identity.LiveMode && raw.Created != nil && *raw.Created > 0 &&
		*raw.Created >= input.ReservedAt.Add(-5*time.Minute).Unix() &&
		*raw.Created <= input.ReservedAt.Add(stripePayoutDispatchWindow+5*time.Minute).Unix() &&
		*raw.Created <= time.Now().Add(5*time.Minute).Unix()
}

func (raw stripePayoutResponse) observation(input PayoutRequest) (Payout, error) {
	if !raw.validEnvelope(input) || raw.Amount != input.AmountCents || strings.ToUpper(raw.Currency) != input.Currency ||
		raw.Destination != input.BankDestinationID || raw.Metadata["hcai_payout_request_id"] != input.PayoutRequestID.String() ||
		raw.Automatic == nil || *raw.Automatic || raw.Method != "standard" || raw.Type != "bank_account" ||
		!oneOf(raw.Status, "pending", "in_transit", "paid", "failed", "canceled") ||
		(raw.FailureCode != "" && raw.Status != "failed") || len(raw.FailureCode) > 100 {
		return Payout{}, newProviderFailure("payment_response_invalid", 0)
	}
	for _, c := range raw.FailureCode {
		if (c < 'a' || c > 'z') && c != '_' {
			return Payout{}, newProviderFailure("payment_response_invalid", 0)
		}
	}
	return Payout{ProviderID: raw.ID, Destination: input.DestinationID, BankDestinationID: raw.Destination,
		AmountCents: raw.Amount, Currency: input.Currency, Status: raw.Status,
		CreatedAt: time.Unix(*raw.Created, 0).UTC(), FailureCode: raw.FailureCode}, nil
}

func (r *StripeRuntime) payoutJSON(ctx context.Context, input PayoutRequest, method, path string, form url.Values, target any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, r.config.BaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	request.Header.Set("Authorization", "Bearer "+r.config.SecretKey)
	request.Header.Set("Stripe-Version", r.config.APIVersion)
	request.Header.Set("Stripe-Account", input.DestinationID)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Idempotency-Key", input.IdempotencyKey)
		// Prevent the default transport replaying a money-moving request.
		request.GetBody = nil
	}
	response, err := r.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return newProviderFailure("payment_timeout", 0)
		}
		return newProviderFailure("payment_request_failed", 0)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return classifyStripeStatus(response)
	}
	body, tooLarge, err := readStripeBounded(response.Body, maxStripeResponseBytes)
	if err != nil || tooLarge || json.Unmarshal(body, target) != nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	return nil
}
