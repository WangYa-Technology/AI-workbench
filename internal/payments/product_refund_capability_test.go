package payments

import (
	"context"
	"errors"
	"testing"
)

type checkoutOnlyProductRuntime struct{ productCheckoutRuntime }

func (*checkoutOnlyProductRuntime) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Checkout: true}
}

func TestProductRefundRejectsProviderWithoutRefundCapability(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	original, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, &durableProductRefundRuntime{})
	service := newPaymentTestService(t, pool, original.config, NewRuntimeCatalog(&checkoutOnlyProductRuntime{}))
	if service.CanRefundProduct("stripe") {
		t.Fatal("checkout-only runtime advertised refund support")
	}
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "unsupported-refund", "test", "The licensed resource did not meet the documented requirement."); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("unsupported refund accepted: %v", err)
	}
	var jobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, ProductRefundJobKind, checkout.PaymentID.String()).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("unsupported provider received %d queued refunds", jobs)
	}
	assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
}
