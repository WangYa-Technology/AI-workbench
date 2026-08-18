package payments

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrProviderUnavailable = providerFailure{code: "payment_provider_unavailable", retryable: true}

type providerFailure struct {
	code       string
	retryAfter time.Duration
	retryable  bool
}

func (e providerFailure) Error() string             { return e.code }
func (e providerFailure) ErrorCode() string         { return e.code }
func (e providerFailure) RetryDelay() time.Duration { return e.retryAfter }
func (e providerFailure) Retryable() bool           { return e.retryable }

func newProviderFailure(code string, retryAfter time.Duration) error {
	retryableCodes := map[string]bool{
		"payment_authentication":       false,
		"payment_invalid_request":      false,
		"payment_rate_limited":         true,
		"payment_request_failed":       true,
		"payment_response_invalid":     false,
		"payment_timeout":              true,
		"payment_provider_unavailable": true,
	}
	retryable, allowed := retryableCodes[code]
	if !allowed {
		code = "payment_request_failed"
		retryable = true
	}
	if retryAfter < 0 || retryAfter > 15*time.Minute {
		retryAfter = 0
	}
	return providerFailure{code: code, retryAfter: retryAfter, retryable: retryable}
}

type CheckoutRequest struct {
	PaymentID   uuid.UUID
	ResourceID  uuid.UUID
	Purpose     string
	Name        string
	AmountCents int
	Currency    string
	SuccessURL  string
	CancelURL   string
}

type CheckoutSession struct {
	ProviderID    string
	CheckoutURL   string
	Status        string
	PaymentStatus string
	ExpiresAt     time.Time
	LiveMode      bool
}

type RefundRequest struct {
	PaymentID         uuid.UUID
	OperationID       uuid.UUID
	ProviderPaymentID string
	AmountCents       int
}

type Refund struct {
	ProviderID        string
	ProviderPaymentID string
	AmountCents       int
	Currency          string
	Status            string
}

type TransferRequest struct {
	PaymentID        uuid.UUID
	ProviderChargeID string
	DestinationID    string
	AmountCents      int
	Currency         string
}

type Transfer struct {
	ProviderID    string
	DestinationID string
	AmountCents   int
	Currency      string
	TransferGroup string
}

// ConnectAccount is the minimized connected-account projection used by the
// payout onboarding boundary. Identity documents, requirements, and bank data
// never cross this interface.
type ConnectAccount struct {
	ID               string
	ChargesEnabled   bool
	PayoutsEnabled   bool
	DetailsSubmitted bool
	RequirementsDue  bool
	LiveMode         bool
}

type ConnectAccountRequest struct {
	UserID uuid.UUID
	Email  string
}

type AccountLinkRequest struct {
	DestinationID string
	RefreshURL    string
	ReturnURL     string
}

type AccountLink struct {
	URL       string
	ExpiresAt time.Time
}

type ProviderRuntime interface {
	Provider() string
	CreateCheckout(context.Context, CheckoutRequest) (CheckoutSession, error)
	CreateRefund(context.Context, RefundRequest) (Refund, error)
	CreateTransfer(context.Context, TransferRequest) (Transfer, error)
	CreateConnectAccount(context.Context, ConnectAccountRequest) (ConnectAccount, error)
	CreateAccountLink(context.Context, AccountLinkRequest) (AccountLink, error)
}

type RuntimeCatalog struct {
	runtimes map[string]ProviderRuntime
}

func NewRuntimeCatalog(runtimes ...ProviderRuntime) *RuntimeCatalog {
	catalog := &RuntimeCatalog{runtimes: make(map[string]ProviderRuntime, len(runtimes))}
	for _, runtime := range runtimes {
		if runtime == nil {
			continue
		}
		provider := strings.TrimSpace(strings.ToLower(runtime.Provider()))
		if provider != "" {
			catalog.runtimes[provider] = runtime
		}
	}
	return catalog
}

func (c *RuntimeCatalog) Runtime(provider string) (ProviderRuntime, error) {
	if c == nil {
		return nil, ErrProviderUnavailable
	}
	runtime, ok := c.runtimes[strings.TrimSpace(strings.ToLower(provider))]
	if !ok {
		return nil, ErrProviderUnavailable
	}
	return runtime, nil
}

func SanitizeProviderError(err error) error {
	if err == nil {
		return nil
	}
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		var delayed interface{ RetryDelay() time.Duration }
		if errors.As(err, &delayed) {
			return newProviderFailure(coded.ErrorCode(), delayed.RetryDelay())
		}
		return newProviderFailure(coded.ErrorCode(), 0)
	}
	return newProviderFailure("payment_request_failed", 0)
}
