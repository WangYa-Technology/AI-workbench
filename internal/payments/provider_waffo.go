package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxWaffoResponseBytes int64 = 1024 * 1024
const waffoRefundContractVersion = "waffo-product-refund-v1"
const waffoCheckoutLookupContractVersion = "waffo-product-checkout-lookup-v1"

// WaffoRuntimeConfig contains only deployment-safe connector settings. The
// Pancake private key stays inside the connector process and is never sent to
// the API or stored in the application database.
type WaffoRuntimeConfig struct {
	ConnectorURL          string
	ConnectorToken        string
	Environment           string
	StoreID               string
	ProductIDOnetime      string
	ProductIDSubscription string
	HTTPClient            *http.Client
}

type WaffoRuntime struct {
	config WaffoRuntimeConfig
	client *http.Client
}

func NewWaffoRuntime(config WaffoRuntimeConfig) *WaffoRuntime {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	// The connector identity is pinned to this endpoint. In particular a
	// 307/308 must not replay a financial POST or forward its credentials.
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &copyClient
	config.ConnectorURL = strings.TrimRight(strings.TrimSpace(config.ConnectorURL), "/")
	config.Environment = strings.TrimSpace(strings.ToLower(config.Environment))
	if config.Environment == "" {
		config.Environment = "test"
	}
	return &WaffoRuntime{config: config, client: client}
}

func (r *WaffoRuntime) Provider() string { return "waffo_pancake" }

func (r *WaffoRuntime) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Checkout: true, Refund: true}
}

// LookupProductCheckout only observes a previously dispatched order. The
// connector query is deployment-supplied and schema-reviewed; an absent or
// rejected query remains a reconciliation outcome and can never create a new
// checkout.
func (r *WaffoRuntime) LookupProductCheckout(ctx context.Context, input CheckoutLookupRequest) (CheckoutLookupResult, error) {
	result := CheckoutLookupResult{Matches: []string{}}
	if r == nil || !r.validConnector() || input.PaymentID == uuid.Nil || input.ResourceID == uuid.Nil || input.AmountCents < 50 || input.Currency != "USD" || input.LiveMode != (r.config.Environment == "prod") || input.StoreID == "" || input.StoreID != r.config.StoreID || input.OrderExternalID == "" || input.BuyerIdentity == "" || input.CreatedAfter.IsZero() || !input.CreatedBefore.After(input.CreatedAfter) || input.CreatedBefore.Sub(input.CreatedAfter) > 24*time.Hour {
		return result, newProviderFailure("payment_invalid_request", 0)
	}
	payload := map[string]any{
		"lookupContractVersion": waffoCheckoutLookupContractVersion,
		"paymentId":             input.PaymentID.String(), "resourceId": input.ResourceID.String(),
		"orderMerchantExternalId": input.OrderExternalID, "buyerIdentity": input.BuyerIdentity,
		"storeId": input.StoreID, "amountCents": input.AmountCents, "currency": input.Currency,
		"liveMode": input.LiveMode, "createdAfter": input.CreatedAfter.UTC().Format(time.RFC3339Nano), "createdBefore": input.CreatedBefore.UTC().Format(time.RFC3339Nano),
	}
	var response struct {
		CheckoutLookupResult
		Observation *struct {
			CheckoutObservation
			LiveMode *bool `json:"liveMode"`
		} `json:"observation"`
	}
	if err := r.postJSON(ctx, "/checkout/lookup", payload, &response); err != nil {
		return CheckoutLookupResult{}, err
	}
	result = response.CheckoutLookupResult
	if response.Observation != nil {
		if response.Observation.LiveMode == nil {
			return CheckoutLookupResult{}, newProviderFailure("payment_response_invalid", 0)
		}
		response.Observation.CheckoutObservation.LiveMode = *response.Observation.LiveMode
		result.Observation = &response.Observation.CheckoutObservation
	}
	if !validCheckoutLookupResult(input, result) {
		return CheckoutLookupResult{}, newProviderFailure("payment_response_invalid", 0)
	}
	if result.Outcome == "found" {
		request := input.CheckoutReadRequest
		request.ProviderCheckoutID = result.Observation.ProviderCheckoutID
		if !validCheckoutObservationForProvider(request, *result.Observation, r.Provider()) {
			return CheckoutLookupResult{}, newProviderFailure("payment_response_invalid", 0)
		}
	}
	return result, nil
}

func (r *WaffoRuntime) CreateCheckout(ctx context.Context, input CheckoutRequest) (CheckoutSession, error) {
	if r == nil || !r.validConnector() || input.PaymentID == uuid.Nil || input.ResourceID == uuid.Nil ||
		!oneOf(strings.ToLower(strings.TrimSpace(input.Purpose)), "product", "task", "wallet_topup", "subscription") || input.AmountCents < 50 ||
		input.AmountCents > 99999999 || strings.ToUpper(strings.TrimSpace(input.Currency)) != "USD" ||
		!validReturnURL(input.SuccessURL) || !validReturnURL(input.CancelURL) || strings.TrimSpace(input.ProductID) == "" {
		return CheckoutSession{}, newProviderFailure("payment_invalid_request", 0)
	}
	productType := strings.ToLower(strings.TrimSpace(input.ProductType))
	if productType == "" {
		productType = "onetime"
	}
	if !oneOf(productType, "onetime", "subscription") {
		return CheckoutSession{}, newProviderFailure("payment_invalid_request", 0)
	}
	identity := strings.TrimSpace(input.BuyerIdentity)
	if identity == "" {
		identity = input.PaymentID.String()
	}
	payload := map[string]any{
		"paymentId": input.PaymentID.String(), "resourceId": input.ResourceID.String(), "purpose": input.Purpose,
		"amountCents": input.AmountCents, "currency": strings.ToUpper(strings.TrimSpace(input.Currency)),
		"productId": input.ProductID, "productType": productType, "buyerIdentity": identity,
		"buyerEmail": strings.TrimSpace(input.BuyerEmail), "successUrl": input.SuccessURL, "cancelUrl": input.CancelURL,
		"orderMerchantExternalId": strings.TrimSpace(input.OrderExternalID),
	}
	if expected := input.CheckoutIdentity; expected != nil {
		if expected.Provider != r.Provider() || expected.Endpoint != r.config.ConnectorURL || expected.LiveMode != (r.config.Environment == "prod") ||
			expected.APIVersion != waffoProductCheckoutAPI || expected.RequestVersion != waffoProductCheckoutVersion {
			return CheckoutSession{}, newProviderFailure("payment_reconciliation_required", 0)
		}
		payload["checkoutIdentity"] = expected
	}
	var response struct {
		ProviderID       string                   `json:"providerId"`
		CheckoutURL      string                   `json:"checkoutUrl"`
		Status           string                   `json:"status"`
		PaymentStatus    string                   `json:"paymentStatus"`
		ExpiresAt        string                   `json:"expiresAt"`
		LiveMode         *bool                    `json:"liveMode"`
		CheckoutIdentity *ProductCheckoutIdentity `json:"checkoutIdentity"`
	}
	if err := r.postJSON(ctx, "/checkout", payload, &response); err != nil {
		return CheckoutSession{}, err
	}
	if input.CheckoutIdentity != nil && (response.CheckoutIdentity == nil || *response.CheckoutIdentity != *input.CheckoutIdentity) {
		return CheckoutSession{}, newProviderFailure("payment_response_invalid", 0)
	}
	parsedURL, err := url.Parse(strings.TrimSpace(response.CheckoutURL))
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil ||
		strings.TrimSpace(response.ProviderID) == "" || !oneOf(strings.TrimSpace(response.Status), "open", "pending", "complete", "expired") ||
		strings.TrimSpace(response.PaymentStatus) == "" || response.LiveMode == nil || *response.LiveMode != (r.config.Environment == "prod") {
		return CheckoutSession{}, newProviderFailure("payment_response_invalid", 0)
	}
	expiresAt, err := parseWaffoTime(response.ExpiresAt)
	if err != nil || !expiresAt.After(time.Now().UTC()) || expiresAt.After(time.Now().UTC().Add(24*time.Hour)) {
		return CheckoutSession{}, newProviderFailure("payment_response_invalid", 0)
	}
	return CheckoutSession{ProviderID: response.ProviderID, CheckoutURL: response.CheckoutURL, Status: response.Status,
		PaymentStatus: response.PaymentStatus, ExpiresAt: expiresAt, LiveMode: *response.LiveMode}, nil
}

func (r *WaffoRuntime) CreateRefund(ctx context.Context, input RefundRequest) (Refund, error) {
	if r == nil || !r.validConnector() || input.PaymentID == uuid.Nil || input.OperationID == uuid.Nil ||
		strings.TrimSpace(input.ProviderPaymentID) == "" || input.AmountCents < 1 || input.AmountCents > 99999999 ||
		strings.ToUpper(strings.TrimSpace(input.Currency)) != "USD" || strings.TrimSpace(input.BuyerIdentity) == "" {
		return Refund{}, newProviderFailure("payment_invalid_request", 0)
	}
	expected := input.PaymentIdentity
	if expected == nil || expected.Provider != r.Provider() || expected.Endpoint != r.config.ConnectorURL || expected.MerchantID == "" ||
		expected.StoreID == "" || input.StoreID != expected.StoreID || expected.LiveMode != (r.config.Environment == "prod") ||
		expected.APIVersion != waffoProductCheckoutAPI || expected.RequestVersion != waffoProductCheckoutVersion {
		return Refund{}, ErrCheckoutReconciliation
	}
	payload := map[string]any{
		"paymentId": input.PaymentID.String(), "operationId": input.OperationID.String(), "providerPaymentId": strings.TrimSpace(input.ProviderPaymentID),
		"refundContractVersion": waffoRefundContractVersion,
		"amountCents":           input.AmountCents, "currency": strings.ToUpper(strings.TrimSpace(input.Currency)), "reason": strings.TrimSpace(input.Reason),
		"buyerIdentity": strings.TrimSpace(input.BuyerIdentity), "buyerEmail": strings.TrimSpace(input.BuyerEmail), "storeId": strings.TrimSpace(input.StoreID),
		"paymentIdentity": expected,
	}
	var response struct {
		OperationID       uuid.UUID                `json:"operationId"`
		ContractVersion   string                   `json:"refundContractVersion"`
		ProviderID        string                   `json:"providerId"`
		ProviderPaymentID string                   `json:"providerPaymentId"`
		AmountCents       int                      `json:"amountCents"`
		Currency          string                   `json:"currency"`
		Status            string                   `json:"status"`
		PaymentIdentity   *ProductCheckoutIdentity `json:"paymentIdentity"`
	}
	if err := r.postJSON(ctx, "/refund", payload, &response); err != nil {
		return Refund{}, err
	}
	if response.PaymentIdentity == nil || *response.PaymentIdentity != *expected || response.OperationID != input.OperationID || response.ContractVersion != waffoRefundContractVersion {
		return Refund{}, newProviderFailure("payment_response_invalid", 0)
	}
	if !safeProviderIDPattern.MatchString(response.ProviderID) || response.ProviderPaymentID != input.ProviderPaymentID ||
		response.AmountCents != input.AmountCents || response.Currency != "USD" || !oneOf(response.Status, "pending", "under_review", "approved", "rejected", "returned", "processing", "succeeded", "failed", "cancelled") {
		return Refund{}, newProviderFailure("payment_response_invalid", 0)
	}
	return Refund{ProviderID: response.ProviderID, ProviderPaymentID: response.ProviderPaymentID, AmountCents: response.AmountCents,
		Currency: "USD", Status: response.Status}, nil
}

func (r *WaffoRuntime) CreateTransfer(context.Context, TransferRequest) (Transfer, error) {
	return Transfer{}, newProviderFailure("payment_provider_unsupported", 0)
}

func (r *WaffoRuntime) CreateConnectAccount(context.Context, ConnectAccountRequest) (ConnectAccount, error) {
	return ConnectAccount{}, newProviderFailure("payment_provider_unsupported", 0)
}

func (r *WaffoRuntime) CreateAccountLink(context.Context, AccountLinkRequest) (AccountLink, error) {
	return AccountLink{}, newProviderFailure("payment_provider_unsupported", 0)
}

func (r *WaffoRuntime) validConnector() bool {
	if r == nil || strings.TrimSpace(r.config.ConnectorURL) == "" || strings.TrimSpace(r.config.ConnectorToken) == "" {
		return false
	}
	parsed, err := url.Parse(r.config.ConnectorURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	return parsed.Scheme == "http" && r.config.Environment != "prod" && waffoLoopbackHost(parsed.Hostname())
}

func waffoLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}

func (r *WaffoRuntime) postJSON(ctx context.Context, path string, payload any, destination any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.config.ConnectorURL+path, bytes.NewReader(body))
	if err != nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	request.Header.Set("Authorization", "Bearer "+r.config.ConnectorToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return newProviderFailure("payment_timeout", 0)
		}
		return newProviderFailure("payment_request_failed", 0)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusConflict {
			return newProviderFailure("payment_reconciliation_required", 0)
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return newProviderFailure("payment_authentication", 0)
		}
		if response.StatusCode == http.StatusTooManyRequests {
			return newProviderFailure("payment_rate_limited", 0)
		}
		if response.StatusCode >= 500 {
			return newProviderFailure("payment_provider_unavailable", 0)
		}
		return newProviderFailure("payment_invalid_request", 0)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxWaffoResponseBytes+1))
	if err != nil || int64(len(data)) > maxWaffoResponseBytes || json.Unmarshal(data, destination) != nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	return nil
}

func parseWaffoTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("missing time")
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC(), nil
	}
	return time.Time{}, errors.New("invalid time")
}
