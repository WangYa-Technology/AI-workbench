package payments

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

type returnURLRuntime struct {
	productCheckoutRuntime
	requests []CheckoutRequest
}

func (r *returnURLRuntime) CreateCheckout(ctx context.Context, input CheckoutRequest) (CheckoutSession, error) {
	r.requests = append(r.requests, input)
	if len(r.requests) == 1 {
		return CheckoutSession{}, errors.New("checkout response lost")
	}
	return r.productCheckoutRuntime.CreateCheckout(ctx, input)
}

func TestProductCheckoutReturnParametersSurviveRetry(t *testing.T) {
	for _, clearedReturnColumns := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "return_columns_cleared"}[clearedReturnColumns], func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			buyer, _, _, product := newProductCheckoutFixture(t, pool)
			runtime := &returnURLRuntime{}
			service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
			version := productOfferVersion(t, pool, product)
			success, cancel := "https://app.example.test/workspace/orders?payment=success&locale=zh", "https://app.example.test/market?payment=cancelled"
			if _, _, err := service.BeginProductCheckout(ctx, buyer, product, "return-url-retry", "test", success, cancel, true, version); err == nil {
				t.Fatal("expected uncertain checkout")
			}
			var paymentID, orderID uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT id,order_id FROM payment_intents WHERE payer_id=$1`, buyer).Scan(&paymentID, &orderID); err != nil {
				t.Fatal(err)
			}
			nextSuccess, nextCancel := "https://new.example.test/success", "https://new.example.test/cancel"
			if clearedReturnColumns {
				// The immutable request stays authoritative if redundant intent return columns are lost.
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET product_success_url=NULL,product_cancel_url=NULL WHERE id=$1`, paymentID); err != nil {
					t.Fatal(err)
				}
				nextSuccess, nextCancel = success, cancel
			}
			checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "return-url-retry", "retry", nextSuccess, nextCancel, true, version)
			if err != nil || checkout.PaymentID != paymentID || checkout.OrderID != orderID || len(runtime.requests) != 2 {
				t.Fatalf("retry state: %+v %v", checkout, err)
			}
			second := runtime.requests[1]

			first := runtime.requests[0]
			if first.SuccessURL != second.SuccessURL || first.CancelURL != second.CancelURL {
				t.Fatal("checkout retry changed persisted return URLs")
			}
			for _, raw := range []string{second.SuccessURL, second.CancelURL} {
				parsed, err := url.Parse(raw)
				if err != nil || parsed.Host != "app.example.test" || parsed.Query().Get("orderId") != orderID.String() || parsed.Query().Get("paymentId") != paymentID.String() {
					t.Fatalf("invalid bound return: %q %v", raw, err)
				}
			}
		})
	}
}
