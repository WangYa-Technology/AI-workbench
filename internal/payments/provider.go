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
		"payment_authentication":          false,
		"payment_invalid_request":         false,
		"payment_rate_limited":            true,
		"payment_request_failed":          true,
		"payment_response_invalid":        false,
		"payment_reconciliation_required": false,
		"payment_timeout":                 true,
		"payment_provider_unavailable":    true,
		"payment_provider_unsupported":    false,
		"payment_settlement_not_due":      true,
		"payment_settlement_pending":      true,
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
	PaymentID       uuid.UUID
	ResourceID      uuid.UUID
	Purpose         string
	Name            string
	AmountCents     int
	Currency        string
	SuccessURL      string
	CancelURL       string
	BuyerIdentity   string
	BuyerEmail      string
	ProductID       string
	ProductType     string
	OrderExternalID string
	// Supplied from the immutable product request record, never caller input.
	CheckoutIdentity *ProductCheckoutIdentity `json:"-"`
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
	PaymentID                uuid.UUID
	OperationID              uuid.UUID
	IncludeOperationMetadata bool
	ProviderPaymentID        string
	StoreID                  string
	AmountCents              int
	Currency                 string
	Reason                   string
	BuyerIdentity            string
	BuyerEmail               string
	// Original product merchant, authenticated before dispatch. Other payment
	// purposes have their own workflows and do not supply this field.
	PaymentIdentity *ProductCheckoutIdentity
}

type Refund struct {
	ProviderID        string
	ProviderPaymentID string
	AmountCents       int
	Currency          string
	Status            string
}

type TransferRequest struct {
	PaymentID uuid.UUID
	// Set only for a new seller source after a consumed return. Legacy and automatic
	// transfers retain their original payment-scoped idempotency key.
	SourceRequestID uuid.UUID
	// An automatic settlement following a consumed source return uses its
	// already-persisted batch instead of a seller request. Never set both.
	SettlementBatchID uuid.UUID
	ProviderChargeID  string
	DestinationID     string
	AmountCents       int
	Currency          string
}

type Transfer struct {
	ProviderID    string
	DestinationID string
	AmountCents   int
	Currency      string
	TransferGroup string
}

// PayoutRequest is an independent bank payout from a connected account. It
// must never be implemented by CreateTransfer: a platform transfer only moves
// funds between Stripe accounts and is not evidence of a bank payout.
type PayoutRequest struct {
	PayoutRequestID   uuid.UUID
	DestinationID     string
	BankDestinationID string
	AmountCents       int
	Currency          string
	IdempotencyKey    string
	// ReservedAt and Identity come from the immutable dispatch record.
	ReservedAt time.Time
	Identity   *ProductCheckoutIdentity
}

type Payout struct {
	ProviderID        string    `json:"providerId"`
	Destination       string    `json:"destination"`
	BankDestinationID string    `json:"bankDestinationId"`
	AmountCents       int       `json:"amountCents"`
	Currency          string    `json:"currency"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"createdAt"`
	FailureCode       string    `json:"failureCode,omitempty"`
}

// Read results are observations, never authorization to resend a payout.
type PayoutLookupResult struct {
	Outcome      string   `json:"outcome"`
	Pages        int      `json:"pages"`
	Observations []Payout `json:"observations"`
}

type SellerPayoutReader interface {
	ReadPayout(context.Context, PayoutRequest, string) (Payout, error)
	LookupPayout(context.Context, PayoutRequest) (PayoutLookupResult, error)
}

// This snapshot is a preflight observation, not a funds reservation or a
// promise of bank settlement. Only minimized identifiers cross this boundary.
type PayoutReadiness struct {
	DestinationID     string    `json:"destinationId"`
	BankDestinationID string    `json:"bankDestinationId"`
	Currency          string    `json:"currency"`
	AvailableCents    int64     `json:"availableCents"`
	ObservedAt        time.Time `json:"observedAt"`
}

type SellerPayoutPreflight interface {
	ReadPayoutReadiness(context.Context, PayoutRequest) (PayoutReadiness, error)
}

// Bank ownership can be verified before the connected balance is funded.
// A bank observation is not a balance check or authorization to dispatch.
type PayoutBankTargetRequest struct {
	DestinationID     string
	BankDestinationID string
	Currency          string
	Identity          ProductCheckoutIdentity
}

type PayoutBankTarget struct {
	BankName          string    `json:"bankName,omitempty"`
	Last4             string    `json:"last4,omitempty"`
	DestinationID     string    `json:"destinationId"`
	BankDestinationID string    `json:"bankDestinationId"`
	Currency          string    `json:"currency"`
	ObservedAt        time.Time `json:"observedAt"`
}

type SellerPayoutBankReader interface {
	ReadPayoutBankTarget(context.Context, PayoutBankTargetRequest) (PayoutBankTarget, error)
}

type PayoutBankOption struct {
	BankDestinationID string `json:"bankDestinationId"`
	BankName          string `json:"bankName"`
	Last4             string `json:"last4"`
	Currency          string `json:"currency"`
}

type PayoutBankDirectory struct {
	Items      []PayoutBankOption `json:"items"`
	ObservedAt time.Time          `json:"observedAt"`
}

type SellerPayoutBankLister interface {
	ListPayoutBanks(context.Context, PayoutBankTargetRequest) (PayoutBankDirectory, error)
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
	UserID      uuid.UUID
	Email       string
	Identity    *ProductCheckoutIdentity
	RetryBefore time.Time
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

// ProviderCapabilities describes workflow support independently from whether a
// runtime is configured. This prevents a checkout-only Provider from being
// advertised as capable of completing marketplace task payouts.
type ProviderCapabilities struct {
	Checkout          bool
	Refund            bool
	Transfer          bool
	Payout            bool
	ConnectedAccounts bool
}

type capabilityProviderRuntime interface {
	Capabilities() ProviderCapabilities
}

// SellerPayoutRuntime is optional until every payment connector implements
// independent bank payouts. A connector lacking it must fail closed before an
// external write; platform CreateTransfer is never an acceptable fallback.
type SellerPayoutRuntime interface {
	CreatePayout(context.Context, PayoutRequest) (Payout, error)
}

func runtimeCapabilities(runtime ProviderRuntime) ProviderCapabilities {
	if runtime == nil {
		return ProviderCapabilities{}
	}
	if capable, ok := runtime.(capabilityProviderRuntime); ok {
		capabilities := capable.Capabilities()
		_, supportsPayout := runtime.(SellerPayoutRuntime)
		capabilities.Payout = capabilities.Payout && supportsPayout
		return capabilities
	}
	// Preserve compatibility with existing Stripe runtime test doubles. New
	// Providers should implement Capabilities explicitly.
	if strings.EqualFold(strings.TrimSpace(runtime.Provider()), "stripe") {
		_, supportsPayout := runtime.(SellerPayoutRuntime)
		return ProviderCapabilities{Checkout: true, Refund: true, Transfer: true, Payout: supportsPayout, ConnectedAccounts: true}
	}
	return ProviderCapabilities{Checkout: true}
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
