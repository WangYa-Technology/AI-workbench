package payments

import (
	"context"
	"github.com/google/uuid"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Lookup only recovers an existing remote ID. An empty or incomplete scan is
// never evidence that checkout was not created or that money was not collected.
type CheckoutLookupReader interface {
	LookupProductCheckout(context.Context, CheckoutLookupRequest) (CheckoutLookupResult, error)
}
type CheckoutLookupRequest struct {
	CheckoutReadRequest
	OrderExternalID string
	BuyerIdentity   string
	StoreID         string
	CreatedAfter    time.Time
	CreatedBefore   time.Time
}
type CheckoutLookupResult struct {
	Outcome     string               `json:"outcome"`
	Pages       int                  `json:"pages"`
	Scanned     int                  `json:"scanned"`
	Matches     []string             `json:"matches"`
	Observation *CheckoutObservation `json:"observation,omitempty"`
}

func (r *StripeRuntime) LookupProductCheckout(ctx context.Context, input CheckoutLookupRequest) (CheckoutLookupResult, error) {
	result := CheckoutLookupResult{Matches: []string{}}
	invalid := func() (CheckoutLookupResult, error) { return result, newProviderFailure("payment_response_invalid", 0) }
	if r == nil || input.PaymentID == uuid.Nil || input.ResourceID == uuid.Nil || input.ProviderCheckoutID != "" || input.AmountCents < 50 || input.Currency != "USD" || input.LiveMode != r.config.LiveMode || input.CreatedAfter.Unix() < 0 || !input.CreatedBefore.After(input.CreatedAfter) {
		return result, newProviderFailure("payment_invalid_request", 0)
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 10; page++ {
		query := url.Values{"limit": {"100"}, "created[gte]": {strconv.FormatInt(input.CreatedAfter.Unix(), 10)}, "created[lte]": {strconv.FormatInt(input.CreatedBefore.Unix(), 10)}}
		if cursor != "" {
			query.Set("starting_after", cursor)
		}
		var response struct {
			Object string `json:"object"`
			More   *bool  `json:"has_more"`
			Data   []struct {
				ID                string            `json:"id"`
				Object            string            `json:"object"`
				Created           *int64            `json:"created"`
				LiveMode          *bool             `json:"livemode"`
				ClientReferenceID string            `json:"client_reference_id"`
				Metadata          map[string]string `json:"metadata"`
			} `json:"data"`
		}
		if err := r.getJSON(ctx, "/checkout/sessions?"+query.Encode(), &response); err != nil {
			return result, err
		}
		if response.Object != "list" || response.More == nil || response.Data == nil || len(response.Data) > 100 || (*response.More && len(response.Data) == 0) {
			return invalid()
		}
		result.Pages++
		for _, item := range response.Data {
			if !validStripeID(item.ID, "cs_") || seen[item.ID] || item.Object != "checkout.session" || item.Created == nil || *item.Created < input.CreatedAfter.Unix() || *item.Created > input.CreatedBefore.Unix() || item.LiveMode == nil || *item.LiveMode != input.LiveMode {
				return invalid()
			}
			seen[item.ID] = true
			cursor = item.ID
			result.Scanned++
			if item.ClientReferenceID == input.PaymentID.String() || item.Metadata["hcai_payment_id"] == input.PaymentID.String() {
				result.Matches = append(result.Matches, item.ID)
				if len(result.Matches) > 1 {
					result.Outcome = "ambiguous"
					return result, nil
				}
			}
		}
		if !*response.More {
			if len(result.Matches) == 0 {
				result.Outcome = "not_found"
				return result, nil
			}
			request := input.CheckoutReadRequest
			request.ProviderCheckoutID = result.Matches[0]
			observation, err := r.ReadProductCheckout(ctx, request)
			if err != nil {
				return result, err
			}
			// Open sessions must retain an authentic hosted URL. Completed/expired
			// sessions legitimately return null, and must never be recreated for a URL.
			if observation.Status == "open" && !validStripeCheckoutURL(observation.CheckoutURL) {
				return invalid()
			}
			if observation.Status != "open" {
				observation.CheckoutURL = ""
			}
			result.Observation = &observation
			result.Outcome = "found"
			return result, nil
		}
	}
	result.Outcome = "incomplete"
	return result, nil
}

func validStripeCheckoutURL(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	value, err := url.Parse(raw)
	return err == nil && value.Scheme == "https" && strings.EqualFold(value.Hostname(), "checkout.stripe.com") && value.User == nil && (value.Port() == "" || value.Port() == "443") && value.Path != ""
}
