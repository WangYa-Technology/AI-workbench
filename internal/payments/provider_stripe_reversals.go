package payments

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// TransferReversalProvider is intentionally separate from ProviderRuntime.
// Its caller must durably authorize/register the first send and switch to
// read-only recovery after that attempt, including when the response is lost.
// The seller-source journal owns first-send authorization; provider calls alone
// never grant a local balance release.
type TransferReversalProvider interface {
	CreateTransferReversal(context.Context, TransferReversalRequest) (TransferReversalResult, error)
	LookupTransferReversal(context.Context, TransferReversalRequest) (TransferReversalResult, error)
}

type TransferReversalRequest struct {
	TransferLookupRequest
	CommandID, PayoutRequestID, SourceTransferID uuid.UUID
	ProviderTransferID                           string
	ReverseAmountCents, PriorReversedCents       int
	IdempotencyKey                               string
	ReservedAt                                   time.Time
	Identity                                     *ProductCheckoutIdentity
}

type TransferReversal struct {
	ProviderID         string    `json:"providerId"`
	ProviderTransferID string    `json:"providerTransferId"`
	CommandID          uuid.UUID `json:"commandId"`
	AmountCents        int       `json:"amountCents"`
	Currency           string    `json:"currency"`
	CreatedAt          time.Time `json:"createdAt"`
}

// A found result has one command-bound reversal and a freshly authenticated
// parent matching the expected aggregate. Lookup also verifies the complete
// list total. No outcome independently authorizes a POST or local release.
type TransferReversalResult struct {
	Outcome      string              `json:"outcome"`
	Pages        int                 `json:"pages"`
	Transfer     TransferObservation `json:"transfer"`
	Observations []TransferReversal  `json:"observations"`
}

const stripeReversalDispatchWindow = 23 * time.Hour

func (r *StripeRuntime) validateTransferReversal(input TransferReversalRequest) error {
	if r == nil || input.CommandID == uuid.Nil || input.PayoutRequestID == uuid.Nil || input.SourceTransferID == uuid.Nil ||
		input.PaymentID == uuid.Nil || !validStripeID(input.ProviderTransferID, "tr_") ||
		!validStripeID(input.ProviderChargeID, "ch_") || input.AmountCents < 1 || input.AmountCents > 99999999 ||
		input.PriorReversedCents < 0 || input.PriorReversedCents >= input.AmountCents ||
		input.ReverseAmountCents < 1 || input.ReverseAmountCents > input.AmountCents-input.PriorReversedCents ||
		input.ReservedAt.IsZero() || input.ReservedAt.Unix() <= 0 || input.ReservedAt.After(time.Now()) ||
		len(input.IdempotencyKey) < 8 || len(input.IdempotencyKey) > 160 {
		return newProviderFailure("payment_invalid_request", 0)
	}
	for _, c := range input.IdempotencyKey {
		if c < 33 || c > 126 {
			return newProviderFailure("payment_invalid_request", 0)
		}
	}
	if input.Identity == nil || input.LiveMode != input.Identity.LiveMode {
		return newProviderFailure("payment_reconciliation_required", 0)
	}
	return r.validatePayoutAccount(PayoutRequest{DestinationID: input.DestinationID, Currency: input.Currency, Identity: input.Identity})
}

func (r *StripeRuntime) authenticateTransferReversal(ctx context.Context, input TransferReversalRequest) error {
	// Only the platform merchant owns the transfer. Never send Stripe-Account
	// for this API, and authenticate test/live using Balance, not reversal JSON.
	return r.authenticatePayoutMerchant(ctx, PayoutRequest{Identity: input.Identity})
}

func (r *StripeRuntime) readReversalTransfer(ctx context.Context, input TransferReversalRequest) (TransferObservation, error) {
	var raw stripeTransferResponseData
	if err := r.getJSON(ctx, "/transfers/"+input.ProviderTransferID, &raw); err != nil {
		return TransferObservation{}, err
	}
	item, valid := raw.observation(input.TransferLookupRequest)
	if !valid || item.ProviderID != input.ProviderTransferID || item.CreatedAt.After(input.ReservedAt.Add(5*time.Minute)) {
		return TransferObservation{}, newProviderFailure("payment_response_invalid", 0)
	}
	return item, nil
}

func (r *StripeRuntime) CreateTransferReversal(ctx context.Context, input TransferReversalRequest) (TransferReversalResult, error) {
	result := TransferReversalResult{Outcome: "incomplete", Observations: []TransferReversal{}}
	if err := r.validateTransferReversal(input); err != nil {
		return result, err
	}
	deadline := input.ReservedAt.Add(stripeReversalDispatchWindow)
	if !time.Now().Before(deadline) {
		return result, newProviderFailure("payment_reconciliation_required", 0)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ctx, cancelCall := context.WithTimeout(ctx, 20*time.Second)
	defer cancelCall()
	if err := r.authenticateTransferReversal(ctx, input); err != nil {
		return result, err
	}
	parent, err := r.readReversalTransfer(ctx, input)
	if err != nil {
		return result, err
	}
	result.Transfer = parent
	if parent.AmountReversed != input.PriorReversedCents || !time.Now().Before(deadline) || ctx.Err() != nil {
		return result, newProviderFailure("payment_reconciliation_required", 0)
	}
	form := url.Values{
		"amount":                             {strconv.Itoa(input.ReverseAmountCents)},
		"refund_application_fee":             {"false"},
		"metadata[hcai_reversal_command_id]": {input.CommandID.String()},
		"metadata[hcai_payout_request_id]":   {input.PayoutRequestID.String()},
		"metadata[hcai_source_transfer_id]":  {input.SourceTransferID.String()},
		"metadata[hcai_payment_id]":          {input.PaymentID.String()},
	}
	var raw stripeTransferReversalResponse
	if err := r.postForm(ctx, "/transfers/"+input.ProviderTransferID+"/reversals", form, input.IdempotencyKey, &raw); err != nil {
		return result, err
	}
	item, valid := raw.observation(input, parent.CreatedAt)
	if !valid {
		return result, newProviderFailure("payment_response_invalid", 0)
	}
	// Retain a validated response even when the final parent read is unknown.
	// The workflow must persist both the observation and that uncertainty.
	result.Observations = append(result.Observations, item)
	parent, err = r.readReversalTransfer(ctx, input)
	if err != nil {
		return result, err
	}
	result.Transfer = parent
	if parent.AmountReversed != input.PriorReversedCents+input.ReverseAmountCents {
		return result, newProviderFailure("payment_reconciliation_required", 0)
	}
	result.Outcome = "found"
	return result, nil
}

func (r *StripeRuntime) LookupTransferReversal(ctx context.Context, input TransferReversalRequest) (TransferReversalResult, error) {
	result := TransferReversalResult{Outcome: "incomplete", Observations: []TransferReversal{}}
	if err := r.validateTransferReversal(input); err != nil {
		return result, err
	}
	// The command's send window never limits reads of existing evidence.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := r.authenticateTransferReversal(ctx, input); err != nil {
		return result, err
	}
	parent, err := r.readReversalTransfer(ctx, input)
	if err != nil {
		return result, err
	}
	result.Transfer = parent
	seen := map[string]bool{}
	cursor, total := "", 0
	for range 10 {
		query := url.Values{"limit": {"100"}}
		if cursor != "" {
			query.Set("starting_after", cursor)
		}
		var page struct {
			Object string                           `json:"object"`
			More   *bool                            `json:"has_more"`
			Data   []stripeTransferReversalResponse `json:"data"`
		}
		if err := r.getJSON(ctx, "/transfers/"+input.ProviderTransferID+"/reversals?"+query.Encode(), &page); err != nil {
			return result, err
		}
		invalid := func() (TransferReversalResult, error) {
			return result, newProviderFailure("payment_response_invalid", 0)
		}
		if page.Object != "list" || page.More == nil || page.Data == nil || len(page.Data) > 100 || (*page.More && len(page.Data) == 0) {
			return invalid()
		}
		result.Pages++
		for _, raw := range page.Data {
			if !raw.validEnvelope(input, parent.CreatedAt) || seen[raw.ID] || raw.Amount > input.AmountCents-total {
				return invalid()
			}
			seen[raw.ID], cursor = true, raw.ID
			total += raw.Amount
			if raw.Metadata["hcai_reversal_command_id"] != input.CommandID.String() {
				continue
			}
			item, valid := raw.observation(input, parent.CreatedAt)
			if !valid {
				return invalid()
			}
			result.Observations = append(result.Observations, item)
			if len(result.Observations) > 1 {
				result.Outcome = "ambiguous"
				return result, nil
			}
		}
		if !*page.More {
			parent, err := r.readReversalTransfer(ctx, input)
			if err != nil {
				return result, err
			}
			result.Transfer = parent
			if parent.AmountReversed != total || (len(result.Observations) == 1 && total != input.PriorReversedCents+input.ReverseAmountCents) {
				return result, newProviderFailure("payment_reconciliation_required", 0)
			}
			result.Outcome = "not_found"
			if len(result.Observations) == 1 {
				result.Outcome = "found"
			}
			return result, nil
		}
	}
	return result, nil
}

type stripeTransferReversalResponse struct {
	ID       string            `json:"id"`
	Object   string            `json:"object"`
	Transfer string            `json:"transfer"`
	Amount   int               `json:"amount"`
	Currency string            `json:"currency"`
	Created  *int64            `json:"created"`
	Metadata map[string]string `json:"metadata"`
}

func (raw stripeTransferReversalResponse) validEnvelope(input TransferReversalRequest, transferCreatedAt time.Time) bool {
	return raw.Object == "transfer_reversal" && validStripeID(raw.ID, "trr_") && raw.Transfer == input.ProviderTransferID &&
		raw.Amount > 0 && raw.Amount <= input.AmountCents && strings.ToUpper(raw.Currency) == input.Currency &&
		raw.Created != nil && *raw.Created > 0 && *raw.Created >= transferCreatedAt.Unix() &&
		*raw.Created <= time.Now().Add(5*time.Minute).Unix()
}

func (raw stripeTransferReversalResponse) observation(input TransferReversalRequest, transferCreatedAt time.Time) (TransferReversal, bool) {
	if !raw.validEnvelope(input, transferCreatedAt) || raw.Amount != input.ReverseAmountCents ||
		raw.Metadata["hcai_reversal_command_id"] != input.CommandID.String() ||
		raw.Metadata["hcai_payout_request_id"] != input.PayoutRequestID.String() ||
		raw.Metadata["hcai_source_transfer_id"] != input.SourceTransferID.String() ||
		raw.Metadata["hcai_payment_id"] != input.PaymentID.String() ||
		*raw.Created < input.ReservedAt.Add(-5*time.Minute).Unix() ||
		*raw.Created > input.ReservedAt.Add(stripeReversalDispatchWindow+5*time.Minute).Unix() {
		return TransferReversal{}, false
	}
	return TransferReversal{ProviderID: raw.ID, ProviderTransferID: raw.Transfer, CommandID: input.CommandID,
		AmountCents: raw.Amount, Currency: input.Currency, CreatedAt: time.Unix(*raw.Created, 0).UTC()}, true
}
