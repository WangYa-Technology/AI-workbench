package payments

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// TransferLookupReader can only observe existing transfers. Neither an empty
// result nor a failed scan authorizes another money-moving request.
type TransferLookupReader interface {
	LookupProductTransfer(context.Context, TransferLookupRequest) (TransferLookupResult, error)
}

type TransferLookupRequest struct {
	TransferRequest
	LiveMode bool
	// Full returns consumed by the local ledger, loaded from immutable proof.
	// Each is excluded only if Stripe still returns exactly that full return.
	ReturnedSources []TransferObservation
}

type TransferObservation struct {
	Transfer
	PaymentID        uuid.UUID `json:"paymentId"`
	ProviderChargeID string    `json:"providerChargeId"`
	LiveMode         bool      `json:"liveMode"`
	CreatedAt        time.Time `json:"createdAt"`
	AmountReversed   int       `json:"amountReversed"`
}

type TransferLookupResult struct {
	Outcome      string                `json:"outcome"`
	Pages        int                   `json:"pages"`
	Observations []TransferObservation `json:"observations"`
}

type stripeTransferResponseData struct {
	ID                string            `json:"id"`
	Object            string            `json:"object"`
	Destination       string            `json:"destination"`
	SourceTransaction string            `json:"source_transaction"`
	Amount            int               `json:"amount"`
	Currency          string            `json:"currency"`
	TransferGroup     string            `json:"transfer_group"`
	LiveMode          *bool             `json:"livemode"`
	Created           *int64            `json:"created"`
	Reversed          *bool             `json:"reversed"`
	AmountReversed    *int              `json:"amount_reversed"`
	Metadata          map[string]string `json:"metadata"`
}

func (raw stripeTransferResponseData) observation(input TransferLookupRequest) (TransferObservation, bool) {
	if raw.Object != "transfer" || raw.LiveMode == nil || raw.Created == nil || raw.Reversed == nil || raw.AmountReversed == nil {
		return TransferObservation{}, false
	}
	item := TransferObservation{
		Transfer: Transfer{ProviderID: raw.ID, DestinationID: raw.Destination, AmountCents: raw.Amount,
			Currency: strings.ToUpper(raw.Currency), TransferGroup: raw.TransferGroup},
		PaymentID: input.PaymentID, ProviderChargeID: raw.SourceTransaction, LiveMode: *raw.LiveMode,
		CreatedAt: time.Unix(*raw.Created, 0).UTC(), AmountReversed: *raw.AmountReversed,
	}
	if raw.Metadata["hcai_payment_id"] != input.PaymentID.String() || !validTransferObservation(input, item) ||
		*raw.Reversed != (item.AmountReversed == item.AmountCents) {
		return TransferObservation{}, false
	}
	return item, true
}

func validTransferObservation(input TransferLookupRequest, item TransferObservation) bool {
	return validStripeID(item.ProviderID, "tr_") && item.PaymentID == input.PaymentID &&
		item.ProviderChargeID == input.ProviderChargeID && item.DestinationID == input.DestinationID &&
		item.AmountCents == input.AmountCents && item.Currency == input.Currency &&
		item.LiveMode == input.LiveMode && item.TransferGroup == transferGroup(input.PaymentID) &&
		item.CreatedAt.Unix() > 0 && !item.CreatedAt.After(time.Now().Add(5*time.Minute)) &&
		item.AmountReversed >= 0 && item.AmountReversed <= item.AmountCents
}

func (r *StripeRuntime) LookupProductTransfer(ctx context.Context, input TransferLookupRequest) (TransferLookupResult, error) {
	result := TransferLookupResult{Observations: []TransferObservation{}}
	if r == nil || input.PaymentID == uuid.Nil || !validStripeID(input.ProviderChargeID, "ch_") ||
		!validStripeID(input.DestinationID, "acct_") || input.AmountCents < 1 || input.AmountCents > 99999999 ||
		input.Currency != "USD" || input.LiveMode != r.config.LiveMode {
		return result, newProviderFailure("payment_invalid_request", 0)
	}
	invalid := func() (TransferLookupResult, error) { return result, newProviderFailure("payment_response_invalid", 0) }
	returned := make(map[string]TransferObservation, len(input.ReturnedSources))
	if len(input.ReturnedSources) > 1000 {
		return invalid()
	}
	for _, item := range input.ReturnedSources {
		original := input
		original.DestinationID = item.DestinationID
		if _, duplicate := returned[item.ProviderID]; duplicate || !validStripeID(item.DestinationID, "acct_") ||
			!validTransferObservation(original, item) || item.AmountReversed != item.AmountCents {
			return invalid()
		}
		returned[item.ProviderID] = item
	}
	seen := map[string]bool{}
	cursor := ""
	for range 10 {
		query := url.Values{"limit": {"100"}, "transfer_group": {transferGroup(input.PaymentID)}}
		if cursor != "" {
			query.Set("starting_after", cursor)
		}
		var response struct {
			Object string                       `json:"object"`
			More   *bool                        `json:"has_more"`
			Data   []stripeTransferResponseData `json:"data"`
		}
		if err := r.getJSON(ctx, "/transfers?"+query.Encode(), &response); err != nil {
			return result, err
		}
		if response.Object != "list" || response.More == nil || response.Data == nil || len(response.Data) > 100 || (*response.More && len(response.Data) == 0) {
			return invalid()
		}
		result.Pages++
		for _, raw := range response.Data {
			expected := input
			prior, consumed := returned[raw.ID]
			if consumed {
				expected.DestinationID = prior.DestinationID
			}
			item, valid := raw.observation(expected)
			if !valid || seen[raw.ID] {
				return invalid()
			}
			seen[raw.ID], cursor = true, raw.ID
			if consumed {
				if !item.CreatedAt.Equal(prior.CreatedAt) {
					return invalid()
				}
				prior.CreatedAt = item.CreatedAt
				if item != prior {
					return invalid()
				}
				continue
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
	result.Outcome = "incomplete"
	return result, nil
}
