package payments

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (r *StripeRuntime) ReadPayoutReadiness(ctx context.Context, input PayoutRequest) (PayoutReadiness, error) {
	if err := r.validatePayoutRequest(input); err != nil {
		return PayoutReadiness{}, err
	}
	if err := r.authenticatePayoutMerchant(ctx, input); err != nil {
		return PayoutReadiness{}, err
	}
	return r.readPayoutReadiness(ctx, input)
}

func (r *StripeRuntime) ReadPayoutBankTarget(ctx context.Context, input PayoutBankTargetRequest) (PayoutBankTarget, error) {
	request := PayoutRequest{DestinationID: input.DestinationID, BankDestinationID: input.BankDestinationID,
		Currency: input.Currency, Identity: &input.Identity}
	if err := r.validatePayoutDestination(request); err != nil {
		return PayoutBankTarget{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := r.authenticatePayoutMerchant(ctx, request); err != nil {
		return PayoutBankTarget{}, err
	}
	return r.readPayoutBankTarget(ctx, request)
}

// Do not change a connected account's payout schedule or choose its default
// bank implicitly. A merchant must configure manual payouts before this
// independent outlet is enabled. Reconciliation reads intentionally skip
// these checks: a restricted/deleted bank still needs financial recovery.
func (r *StripeRuntime) readPayoutBankTarget(ctx context.Context, input PayoutRequest) (PayoutBankTarget, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := r.readPayoutAccountEligibility(ctx, input); err != nil {
		return PayoutBankTarget{}, err
	}
	var bank struct {
		ID       string `json:"id"`
		Object   string `json:"object"`
		Account  string `json:"account"`
		Currency string `json:"currency"`
		Status   string `json:"status"`
		BankName string `json:"bank_name"`
		Last4    string `json:"last4"`
	}
	if err := r.getJSON(ctx, "/accounts/"+input.DestinationID+"/external_accounts/"+input.BankDestinationID, &bank); err != nil {
		return PayoutBankTarget{}, err
	}
	if !validPayoutBankOption(PayoutBankOption{BankDestinationID: bank.ID, BankName: bank.BankName, Last4: bank.Last4, Currency: input.Currency}) || bank.Object != "bank_account" || bank.ID != input.BankDestinationID || bank.Account != input.DestinationID || strings.ToUpper(bank.Currency) != input.Currency {
		return PayoutBankTarget{}, newProviderFailure("payment_response_invalid", 0)
	}
	if !oneOf(bank.Status, "new", "validated", "verified") {
		return PayoutBankTarget{}, newProviderFailure("payment_reconciliation_required", 0)
	}
	return PayoutBankTarget{BankName: bank.BankName, Last4: bank.Last4, DestinationID: input.DestinationID, BankDestinationID: input.BankDestinationID, Currency: input.Currency, ObservedAt: time.Now().UTC()}, nil
}

func (r *StripeRuntime) readPayoutAccountEligibility(ctx context.Context, input PayoutRequest) error {
	var account struct {
		ID             string `json:"id"`
		Object         string `json:"object"`
		PayoutsEnabled *bool  `json:"payouts_enabled"`
		Settings       struct {
			Payouts struct {
				Schedule struct {
					Interval string `json:"interval"`
				} `json:"schedule"`
			} `json:"payouts"`
		} `json:"settings"`
	}
	if err := r.payoutJSON(ctx, input, http.MethodGet, "/account", nil, &account); err != nil {
		return err
	}
	if account.Object != "account" || account.ID != input.DestinationID || account.PayoutsEnabled == nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if !*account.PayoutsEnabled || account.Settings.Payouts.Schedule.Interval != "manual" {
		return newProviderFailure("payment_reconciliation_required", 0)
	}
	return nil
}

func (r *StripeRuntime) readPayoutReadiness(ctx context.Context, input PayoutRequest) (PayoutReadiness, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := r.readPayoutBankTarget(ctx, input); err != nil {
		return PayoutReadiness{}, err
	}
	var balance struct {
		Object    string `json:"object"`
		LiveMode  *bool  `json:"livemode"`
		Available []struct {
			Amount   *int64 `json:"amount"`
			Currency string `json:"currency"`
		} `json:"available"`
	}
	if err := r.payoutJSON(ctx, input, http.MethodGet, "/balance", nil, &balance); err != nil {
		return PayoutReadiness{}, err
	}
	if balance.Object != "balance" || balance.LiveMode == nil || *balance.LiveMode != input.Identity.LiveMode || balance.Available == nil {
		return PayoutReadiness{}, newProviderFailure("payment_response_invalid", 0)
	}
	var available int64
	seen := map[string]bool{}
	for _, item := range balance.Available {
		currency := strings.ToUpper(item.Currency)
		if item.Amount == nil || len(currency) != 3 || seen[currency] {
			return PayoutReadiness{}, newProviderFailure("payment_response_invalid", 0)
		}
		seen[currency] = true
		if currency == input.Currency {
			available = *item.Amount
		}
	}
	if available < int64(input.AmountCents) {
		return PayoutReadiness{}, newProviderFailure("payment_settlement_pending", 5*time.Minute)
	}
	return PayoutReadiness{DestinationID: input.DestinationID, BankDestinationID: input.BankDestinationID,
		Currency: input.Currency, AvailableCents: available, ObservedAt: time.Now().UTC()}, nil
}
