package payments

import (
	"context"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Return only a complete, authenticated directory of eligible USD bank
// accounts. Never expose account numbers, holder identities or raw responses.
func (r *StripeRuntime) ListPayoutBanks(ctx context.Context, input PayoutBankTargetRequest) (PayoutBankDirectory, error) {
	out := PayoutBankDirectory{Items: []PayoutBankOption{}}
	request := PayoutRequest{DestinationID: input.DestinationID, Currency: input.Currency, Identity: &input.Identity}
	if input.BankDestinationID != "" {
		return out, newProviderFailure("payment_invalid_request", 0)
	}
	if err := r.validatePayoutAccount(request); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := r.authenticatePayoutMerchant(ctx, request); err != nil {
		return out, err
	}
	if err := r.readPayoutAccountEligibility(ctx, request); err != nil {
		return out, err
	}
	seen := map[string]bool{}
	cursor := ""
	for range 10 {
		query := url.Values{"object": {"bank_account"}, "limit": {"100"}}
		if cursor != "" {
			query.Set("starting_after", cursor)
		}
		var page struct {
			Object string `json:"object"`
			More   *bool  `json:"has_more"`
			Data   []struct {
				ID       string `json:"id"`
				Object   string `json:"object"`
				Account  string `json:"account"`
				Currency string `json:"currency"`
				Status   string `json:"status"`
				BankName string `json:"bank_name"`
				Last4    string `json:"last4"`
			} `json:"data"`
		}
		if err := r.getJSON(ctx, "/accounts/"+input.DestinationID+"/external_accounts?"+query.Encode(), &page); err != nil {
			return PayoutBankDirectory{}, err
		}
		if page.Object != "list" || page.More == nil || page.Data == nil || len(page.Data) > 100 || (*page.More && len(page.Data) == 0) {
			return PayoutBankDirectory{}, newProviderFailure("payment_response_invalid", 0)
		}
		for _, bank := range page.Data {
			if bank.Object != "bank_account" || bank.Account != input.DestinationID || !validStripeID(bank.ID, "ba_") || seen[bank.ID] {
				return PayoutBankDirectory{}, newProviderFailure("payment_response_invalid", 0)
			}
			seen[bank.ID], cursor = true, bank.ID
			if strings.ToUpper(bank.Currency) != input.Currency || !oneOf(bank.Status, "new", "validated", "verified") {
				continue
			}
			option := PayoutBankOption{BankDestinationID: bank.ID, BankName: bank.BankName, Last4: bank.Last4, Currency: input.Currency}
			if !validPayoutBankOption(option) {
				return PayoutBankDirectory{}, newProviderFailure("payment_response_invalid", 0)
			}
			out.Items = append(out.Items, option)
		}
		if !*page.More {
			if err := ctx.Err(); err != nil {
				return PayoutBankDirectory{}, err
			}
			out.ObservedAt = time.Now().UTC()
			return out, nil
		}
	}
	return PayoutBankDirectory{}, newProviderFailure("payment_response_invalid", 0)
}

func validPayoutBankOption(option PayoutBankOption) bool {
	if !validStripeID(option.BankDestinationID, "ba_") || option.Currency != "USD" || len(option.Last4) != 4 || !utf8.ValidString(option.BankName) || utf8.RuneCountInString(option.BankName) > 120 {
		return false
	}
	for _, r := range option.Last4 {
		if r < '0' || r > '9' {
			return false
		}
	}
	for _, r := range option.BankName {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}
