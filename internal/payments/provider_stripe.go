package payments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxStripeResponseBytes = 1024 * 1024

type StripeRuntimeConfig struct {
	SecretKey  string
	BaseURL    string
	APIVersion string
	LiveMode   bool
	HTTPClient *http.Client
}

type StripeRuntime struct {
	config StripeRuntimeConfig
	client *http.Client
}

func NewStripeRuntime(config StripeRuntimeConfig) *StripeRuntime {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	return &StripeRuntime{config: config, client: client}
}

func (r *StripeRuntime) Provider() string { return "stripe" }

func (r *StripeRuntime) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Checkout: true, Refund: true, Transfer: true, ConnectedAccounts: true}
}

func (r *StripeRuntime) CreateCheckout(ctx context.Context, input CheckoutRequest) (CheckoutSession, error) {
	input.Purpose = strings.TrimSpace(strings.ToLower(input.Purpose))
	input.Currency = strings.TrimSpace(strings.ToLower(input.Currency))
	input.Name = strings.TrimSpace(input.Name)
	if input.PaymentID == uuid.Nil || input.ResourceID == uuid.Nil || !oneOf(input.Purpose, "product", "task", "wallet_topup", "subscription") ||
		input.AmountCents < 50 || input.AmountCents > 99999999 || input.Currency != "usd" ||
		utf8.RuneCountInString(input.Name) < 3 || utf8.RuneCountInString(input.Name) > 120 ||
		!validReturnURL(input.SuccessURL) || !validReturnURL(input.CancelURL) {
		return CheckoutSession{}, newProviderFailure("payment_invalid_request", 0)
	}
	paymentID := input.PaymentID.String()
	form := url.Values{
		"mode":                                {"payment"},
		"client_reference_id":                 {paymentID},
		"line_items[0][price_data][currency]": {input.Currency},
		"line_items[0][price_data][product_data][name]":   {input.Name},
		"line_items[0][price_data][unit_amount]":          {strconv.Itoa(input.AmountCents)},
		"line_items[0][quantity]":                         {"1"},
		"payment_intent_data[transfer_group]":             {transferGroup(input.PaymentID)},
		"payment_intent_data[metadata][hcai_payment_id]":  {paymentID},
		"payment_intent_data[metadata][hcai_resource_id]": {input.ResourceID.String()},
		"payment_intent_data[metadata][hcai_purpose]":     {input.Purpose},
		"metadata[hcai_payment_id]":                       {paymentID},
		"metadata[hcai_resource_id]":                      {input.ResourceID.String()},
		"metadata[hcai_purpose]":                          {input.Purpose},
		"success_url":                                     {input.SuccessURL},
		"cancel_url":                                      {input.CancelURL},
	}
	var response struct {
		ID                string `json:"id"`
		URL               string `json:"url"`
		Status            string `json:"status"`
		PaymentStatus     string `json:"payment_status"`
		ExpiresAt         int64  `json:"expires_at"`
		LiveMode          bool   `json:"livemode"`
		AmountTotal       int    `json:"amount_total"`
		Currency          string `json:"currency"`
		ClientReferenceID string `json:"client_reference_id"`
	}
	if err := r.postForm(ctx, "/checkout/sessions", form, "checkout-"+paymentID, &response); err != nil {
		return CheckoutSession{}, err
	}
	checkoutURL, err := url.Parse(response.URL)
	if err != nil || checkoutURL.Scheme != "https" || !strings.EqualFold(checkoutURL.Hostname(), "checkout.stripe.com") ||
		!validStripeID(response.ID, "cs_") || !oneOf(response.Status, "open", "complete", "expired") ||
		!oneOf(response.PaymentStatus, "unpaid", "paid", "no_payment_required") || response.ExpiresAt <= 0 ||
		response.LiveMode != r.config.LiveMode || response.AmountTotal != input.AmountCents ||
		strings.ToLower(response.Currency) != input.Currency || response.ClientReferenceID != paymentID {
		return CheckoutSession{}, newProviderFailure("payment_response_invalid", 0)
	}
	return CheckoutSession{
		ProviderID: response.ID, CheckoutURL: response.URL, Status: response.Status,
		PaymentStatus: response.PaymentStatus, ExpiresAt: time.Unix(response.ExpiresAt, 0).UTC(), LiveMode: response.LiveMode,
	}, nil
}

// ExpireCheckout closes an unpaid test Checkout Session so a staging smoke
// check cannot leave a usable payment link behind.
func (r *StripeRuntime) ExpireCheckout(ctx context.Context, providerID string) error {
	if r == nil || r.config.LiveMode || !validStripeID(providerID, "cs_") {
		return newProviderFailure("payment_invalid_request", 0)
	}
	var response struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		LiveMode      bool   `json:"livemode"`
		PaymentStatus string `json:"payment_status"`
	}
	if err := r.postForm(ctx, "/checkout/sessions/"+url.PathEscape(providerID)+"/expire", nil, "expire-checkout-"+providerID, &response); err != nil {
		return err
	}
	if response.ID != providerID || response.Status != "expired" || response.LiveMode || response.PaymentStatus != "unpaid" {
		return newProviderFailure("payment_response_invalid", 0)
	}
	return nil
}

func (r *StripeRuntime) CreateRefund(ctx context.Context, input RefundRequest) (Refund, error) {
	if input.PaymentID == uuid.Nil || input.OperationID == uuid.Nil || !validStripeID(input.ProviderPaymentID, "pi_") || input.AmountCents < 1 || input.AmountCents > 99999999 {
		return Refund{}, newProviderFailure("payment_invalid_request", 0)
	}
	form := url.Values{
		"payment_intent":            {input.ProviderPaymentID},
		"amount":                    {strconv.Itoa(input.AmountCents)},
		"metadata[hcai_payment_id]": {input.PaymentID.String()},
		"reason":                    {"requested_by_customer"},
	}
	var response struct {
		ID            string `json:"id"`
		PaymentIntent string `json:"payment_intent"`
		Amount        int    `json:"amount"`
		Currency      string `json:"currency"`
		Status        string `json:"status"`
	}
	if err := r.postForm(ctx, "/refunds", form, "refund-"+input.OperationID.String(), &response); err != nil {
		return Refund{}, err
	}
	if !validStripeID(response.ID, "re_") || response.PaymentIntent != input.ProviderPaymentID || response.Amount != input.AmountCents ||
		strings.ToLower(response.Currency) != "usd" || !oneOf(response.Status, "pending", "requires_action", "succeeded", "failed", "canceled") {
		return Refund{}, newProviderFailure("payment_response_invalid", 0)
	}
	return Refund{ProviderID: response.ID, ProviderPaymentID: response.PaymentIntent, AmountCents: response.Amount, Currency: strings.ToUpper(response.Currency), Status: response.Status}, nil
}

func (r *StripeRuntime) CreateTransfer(ctx context.Context, input TransferRequest) (Transfer, error) {
	input.Currency = strings.TrimSpace(strings.ToLower(input.Currency))
	if input.PaymentID == uuid.Nil || !validStripeID(input.ProviderChargeID, "ch_") || !validStripeID(input.DestinationID, "acct_") ||
		input.AmountCents < 1 || input.AmountCents > 99999999 || input.Currency != "usd" {
		return Transfer{}, newProviderFailure("payment_invalid_request", 0)
	}
	group := transferGroup(input.PaymentID)
	form := url.Values{
		"amount":                    {strconv.Itoa(input.AmountCents)},
		"currency":                  {input.Currency},
		"destination":               {input.DestinationID},
		"source_transaction":        {input.ProviderChargeID},
		"transfer_group":            {group},
		"metadata[hcai_payment_id]": {input.PaymentID.String()},
	}
	var response struct {
		ID            string `json:"id"`
		Destination   string `json:"destination"`
		Amount        int    `json:"amount"`
		Currency      string `json:"currency"`
		TransferGroup string `json:"transfer_group"`
	}
	if err := r.postForm(ctx, "/transfers", form, "transfer-"+input.PaymentID.String(), &response); err != nil {
		return Transfer{}, err
	}
	if !validStripeID(response.ID, "tr_") || response.Destination != input.DestinationID || response.Amount != input.AmountCents ||
		strings.ToLower(response.Currency) != input.Currency || response.TransferGroup != group {
		return Transfer{}, newProviderFailure("payment_response_invalid", 0)
	}
	return Transfer{ProviderID: response.ID, DestinationID: response.Destination, AmountCents: response.Amount, Currency: strings.ToUpper(response.Currency), TransferGroup: response.TransferGroup}, nil
}

func (r *StripeRuntime) CreateConnectAccount(ctx context.Context, input ConnectAccountRequest) (ConnectAccount, error) {
	if input.UserID == uuid.Nil || (input.Email != "" && !strings.Contains(input.Email, "@")) {
		return ConnectAccount{}, newProviderFailure("payment_invalid_request", 0)
	}
	form := url.Values{
		"type":                                   {"express"},
		"capabilities[card_payments][requested]": {"true"},
		"capabilities[transfers][requested]":     {"true"},
		"metadata[hcai_user_id]":                 {input.UserID.String()},
	}
	if strings.TrimSpace(input.Email) != "" {
		form.Set("email", strings.TrimSpace(input.Email))
	}
	var response struct {
		ID               string `json:"id"`
		ChargesEnabled   bool   `json:"charges_enabled"`
		PayoutsEnabled   bool   `json:"payouts_enabled"`
		DetailsSubmitted bool   `json:"details_submitted"`
		LiveMode         bool   `json:"livemode"`
		Requirements     struct {
			CurrentlyDue        []string `json:"currently_due"`
			PastDue             []string `json:"past_due"`
			PendingVerification []string `json:"pending_verification"`
			DisabledReason      string   `json:"disabled_reason"`
		} `json:"requirements"`
	}
	if err := r.postForm(ctx, "/accounts", form, "connect-account-"+input.UserID.String(), &response); err != nil {
		return ConnectAccount{}, err
	}
	if !validStripeID(response.ID, "acct_") || response.LiveMode != r.config.LiveMode {
		return ConnectAccount{}, newProviderFailure("payment_response_invalid", 0)
	}
	return ConnectAccount{
		ID: response.ID, ChargesEnabled: response.ChargesEnabled, PayoutsEnabled: response.PayoutsEnabled,
		DetailsSubmitted: response.DetailsSubmitted,
		RequirementsDue:  len(response.Requirements.CurrentlyDue) > 0 || len(response.Requirements.PastDue) > 0 || len(response.Requirements.PendingVerification) > 0 || response.Requirements.DisabledReason != "",
		LiveMode:         response.LiveMode,
	}, nil
}

func (r *StripeRuntime) CreateAccountLink(ctx context.Context, input AccountLinkRequest) (AccountLink, error) {
	if !validStripeID(input.DestinationID, "acct_") || !validReturnURL(input.RefreshURL) || !validReturnURL(input.ReturnURL) {
		return AccountLink{}, newProviderFailure("payment_invalid_request", 0)
	}
	form := url.Values{"account": {input.DestinationID}, "refresh_url": {input.RefreshURL}, "return_url": {input.ReturnURL}, "type": {"account_onboarding"}}
	var response struct {
		URL       string `json:"url"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := r.postForm(ctx, "/account_links", form, "connect-link-"+uuid.NewString(), &response); err != nil {
		return AccountLink{}, err
	}
	parsed, err := url.Parse(response.URL)
	now := time.Now().UTC()
	expiresAt := time.Unix(response.ExpiresAt, 0).UTC()
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "connect.stripe.com") || response.ExpiresAt <= now.Unix() || expiresAt.After(now.Add(24*time.Hour)) {
		return AccountLink{}, newProviderFailure("payment_response_invalid", 0)
	}
	return AccountLink{URL: response.URL, ExpiresAt: expiresAt}, nil
}

// DeleteConnectAccount removes a disposable test-mode connected account.
func (r *StripeRuntime) DeleteConnectAccount(ctx context.Context, providerID string) error {
	if r == nil || r.config.LiveMode || !validStripeID(providerID, "acct_") {
		return newProviderFailure("payment_invalid_request", 0)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, r.config.BaseURL+"/accounts/"+url.PathEscape(providerID), nil)
	if err != nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	request.Header.Set("Authorization", "Bearer "+r.config.SecretKey)
	request.Header.Set("Stripe-Version", r.config.APIVersion)
	response, err := r.client.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return newProviderFailure("payment_timeout", 0)
		}
		return newProviderFailure("payment_request_failed", 0)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return classifyStripeStatus(response)
	}
	body, tooLarge, err := readStripeBounded(response.Body, maxStripeResponseBytes)
	if err != nil || tooLarge {
		return newProviderFailure("payment_response_invalid", 0)
	}
	var result struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if json.Unmarshal(body, &result) != nil || result.ID != providerID || !result.Deleted {
		return newProviderFailure("payment_response_invalid", 0)
	}
	return nil
}

func (r *StripeRuntime) postForm(ctx context.Context, path string, form url.Values, idempotencyKey string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.config.BaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	request.Header.Set("Authorization", "Bearer "+r.config.SecretKey)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("Stripe-Version", r.config.APIVersion)
	response, err := r.client.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return newProviderFailure("payment_timeout", 0)
		}
		return newProviderFailure("payment_request_failed", 0)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
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

func classifyStripeStatus(response *http.Response) error {
	retryAfter := parseStripeRetryAfter(response.Header.Get("Retry-After"), time.Now())
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return newProviderFailure("payment_authentication", 0)
	case http.StatusRequestTimeout:
		return newProviderFailure("payment_timeout", retryAfter)
	case http.StatusTooManyRequests:
		return newProviderFailure("payment_rate_limited", retryAfter)
	}
	if response.StatusCode >= http.StatusInternalServerError {
		return newProviderFailure("payment_provider_unavailable", retryAfter)
	}
	return newProviderFailure("payment_invalid_request", 0)
}

func readStripeBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limit {
		return nil, true, nil
	}
	return body, false, nil
}

func parseStripeRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var delay time.Duration
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		}
	} else if retryAt, err := http.ParseTime(value); err == nil {
		delay = retryAt.Sub(now)
	}
	if delay < 0 {
		return 0
	}
	if delay > 15*time.Minute {
		return 15 * time.Minute
	}
	return delay
}

func validReturnURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	return parsed.Scheme == "http" && isLoopbackStripeHost(parsed.Hostname())
}

func isLoopbackStripeHost(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

func validStripeID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) < len(prefix)+6 || len(value) > 255 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func transferGroup(paymentID uuid.UUID) string { return "hcai_" + paymentID.String() }

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
