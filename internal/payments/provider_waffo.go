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
	var response struct {
		ProviderID    string `json:"providerId"`
		CheckoutURL   string `json:"checkoutUrl"`
		Status        string `json:"status"`
		PaymentStatus string `json:"paymentStatus"`
		ExpiresAt     string `json:"expiresAt"`
		LiveMode      bool   `json:"liveMode"`
	}
	if err := r.postJSON(ctx, "/checkout", payload, &response); err != nil {
		return CheckoutSession{}, err
	}
	parsedURL, err := url.Parse(strings.TrimSpace(response.CheckoutURL))
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil ||
		strings.TrimSpace(response.ProviderID) == "" || !oneOf(strings.TrimSpace(response.Status), "open", "pending", "complete", "expired") ||
		strings.TrimSpace(response.PaymentStatus) == "" || response.LiveMode != (r.config.Environment == "prod") {
		return CheckoutSession{}, newProviderFailure("payment_response_invalid", 0)
	}
	expiresAt, err := parseWaffoTime(response.ExpiresAt)
	if err != nil || !expiresAt.After(time.Now().UTC()) || expiresAt.After(time.Now().UTC().Add(24*time.Hour)) {
		return CheckoutSession{}, newProviderFailure("payment_response_invalid", 0)
	}
	return CheckoutSession{ProviderID: response.ProviderID, CheckoutURL: response.CheckoutURL, Status: response.Status,
		PaymentStatus: response.PaymentStatus, ExpiresAt: expiresAt, LiveMode: response.LiveMode}, nil
}

func (r *WaffoRuntime) CreateRefund(ctx context.Context, input RefundRequest) (Refund, error) {
	if r == nil || !r.validConnector() || input.PaymentID == uuid.Nil || input.OperationID == uuid.Nil ||
		strings.TrimSpace(input.ProviderPaymentID) == "" || input.AmountCents < 1 || input.AmountCents > 99999999 ||
		strings.ToUpper(strings.TrimSpace(input.Currency)) != "USD" {
		return Refund{}, newProviderFailure("payment_invalid_request", 0)
	}
	payload := map[string]any{
		"paymentId": input.PaymentID.String(), "operationId": input.OperationID.String(), "providerPaymentId": strings.TrimSpace(input.ProviderPaymentID),
		"amountCents": input.AmountCents, "currency": strings.ToUpper(strings.TrimSpace(input.Currency)), "reason": strings.TrimSpace(input.Reason),
		"buyerIdentity": strings.TrimSpace(input.BuyerIdentity), "buyerEmail": strings.TrimSpace(input.BuyerEmail), "storeId": strings.TrimSpace(input.StoreID),
	}
	var response struct {
		ProviderID        string `json:"providerId"`
		ProviderPaymentID string `json:"providerPaymentId"`
		AmountCents       int    `json:"amountCents"`
		Currency          string `json:"currency"`
		Status            string `json:"status"`
	}
	if err := r.postJSON(ctx, "/refund", payload, &response); err != nil {
		return Refund{}, err
	}
	if strings.TrimSpace(response.ProviderID) == "" || response.ProviderPaymentID != input.ProviderPaymentID ||
		response.AmountCents != input.AmountCents || strings.ToUpper(response.Currency) != "USD" || strings.TrimSpace(response.Status) == "" {
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
