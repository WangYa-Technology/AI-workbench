package payments

import (
	"context"
	"errors"
	"testing"
	"time"
)

type catalogRuntime struct{ provider string }

func (r catalogRuntime) Provider() string { return r.provider }
func (catalogRuntime) CreateCheckout(context.Context, CheckoutRequest) (CheckoutSession, error) {
	return CheckoutSession{}, nil
}
func (catalogRuntime) CreateRefund(context.Context, RefundRequest) (Refund, error) {
	return Refund{}, nil
}
func (catalogRuntime) CreateTransfer(context.Context, TransferRequest) (Transfer, error) {
	return Transfer{}, nil
}
func (catalogRuntime) CreateConnectAccount(context.Context, ConnectAccountRequest) (ConnectAccount, error) {
	return ConnectAccount{}, nil
}
func (catalogRuntime) CreateAccountLink(context.Context, AccountLinkRequest) (AccountLink, error) {
	return AccountLink{}, nil
}

func TestRuntimeCatalogDefaultsUnavailable(t *testing.T) {
	catalog := NewRuntimeCatalog(nil, catalogRuntime{provider: " Stripe "})
	runtime, err := catalog.Runtime("STRIPE")
	if err != nil || runtime.Provider() != " Stripe " {
		t.Fatalf("registered runtime was not resolved: runtime=%#v err=%v", runtime, err)
	}
	if _, err := NewRuntimeCatalog().Runtime("stripe"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("empty catalog did not fail closed: %v", err)
	}
	var nilCatalog *RuntimeCatalog
	if _, err := nilCatalog.Runtime("stripe"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("nil catalog did not fail closed: %v", err)
	}
}

func TestSanitizeProviderError(t *testing.T) {
	err := SanitizeProviderError(errors.New("secret upstream response"))
	if err.Error() != "payment_request_failed" || err.Error() == "secret upstream response" {
		t.Fatalf("provider error was not sanitized: %v", err)
	}
	delayed := newProviderFailure("payment_rate_limited", 12*time.Second)
	sanitized := SanitizeProviderError(delayed)
	var retryable interface {
		Retryable() bool
		RetryDelay() time.Duration
	}
	if !errors.As(sanitized, &retryable) || !retryable.Retryable() || retryable.RetryDelay() != 12*time.Second {
		t.Fatalf("safe retry evidence was not preserved: %#v", sanitized)
	}
}
