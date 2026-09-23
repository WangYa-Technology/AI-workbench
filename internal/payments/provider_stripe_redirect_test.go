package payments

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestStripeRuntimeNeverFollowsRedirects(t *testing.T) {
	operations := []struct {
		name, method string
		call         func(*StripeRuntime) error
	}{
		{"checkout", http.MethodPost, func(r *StripeRuntime) error {
			_, err := r.CreateCheckout(context.Background(), CheckoutRequest{PaymentID: testPaymentID, ResourceID: testResourceID,
				Purpose: "product", Name: "Redirect boundary", AmountCents: 1900, Currency: "USD",
				SuccessURL: "https://example.test/success", CancelURL: "https://example.test/cancel"})
			return err
		}},
		{"refund", http.MethodPost, func(r *StripeRuntime) error {
			_, err := r.CreateRefund(context.Background(), RefundRequest{PaymentID: testPaymentID, OperationID: uuid.New(),
				ProviderPaymentID: "pi_redirect_fixture", AmountCents: 1900, Currency: "USD"})
			return err
		}},
		{"merchant_identity", http.MethodGet, func(r *StripeRuntime) error {
			_, err := r.ProductCheckoutIdentity(context.Background())
			return err
		}},
		{"refund_query", http.MethodGet, func(r *StripeRuntime) error {
			_, err := r.ReadProductRefunds(context.Background(), RefundReadRequest{PaymentID: testPaymentID, ResourceID: testResourceID,
				ProviderPaymentID: "pi_redirect_fixture", AmountCents: 1900, Currency: "USD"})
			return err
		}},
	}
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, sameOrigin := range []bool{false, true} {
			for _, operation := range operations {
				t.Run(fmt.Sprintf("%d/same_origin_%t/%s", code, sameOrigin, operation.name), func(t *testing.T) {
					var targetCalls, initialCalls, callerRedirectPolicy atomic.Int32
					targetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						targetCalls.Add(1)
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{}`)
					})
					target := httptest.NewServer(targetHandler)
					defer target.Close()
					origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/redirect-target" {
							targetHandler.ServeHTTP(w, r)
							return
						}
						initialCalls.Add(1)
						if r.Method != operation.method || r.Header.Get("Authorization") != "Bearer sk_test_redirect_guard" {
							t.Error("original request did not use its expected method and credential")
						}
						location := target.URL + "/redirect-target"
						if sameOrigin {
							location = "/redirect-target"
						}
						w.Header().Set("Location", location)
						w.WriteHeader(code)
					}))
					defer origin.Close()
					// Even a caller that allows redirects must not override the
					// financial boundary or have its shared client mutated.
					client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
						callerRedirectPolicy.Add(1)
						return nil
					}}
					runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: origin.URL, SecretKey: "sk_test_redirect_guard",
						APIVersion: testStripeAPIVersion, HTTPClient: client})
					err := operation.call(runtime)
					if err == nil || jobs.ShouldRetry(err) || initialCalls.Load() != 1 || targetCalls.Load() != 0 || callerRedirectPolicy.Load() != 0 {
						t.Fatalf("redirect was not contained: err=%v initial=%d target=%d callerPolicy=%d", err, initialCalls.Load(), targetCalls.Load(), callerRedirectPolicy.Load())
					}
					if client.Timeout != 5*time.Second || client.CheckRedirect == nil {
						t.Fatal("shared caller client was modified")
					}
					if err := client.CheckRedirect(nil, nil); err != nil || callerRedirectPolicy.Load() != 1 {
						t.Fatal("financial policy replaced the caller's redirect policy")
					}
				})
			}
		}
	}
}

func TestStripeRedirectPreservesPendingRefund(t *testing.T) {
	for _, stage := range []string{"identity", "refund"} {
		t.Run(stage, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			var targetCalls, refundPosts atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetCalls.Add(1)
				fmt.Fprint(w, `{}`)
			}))
			defer target.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/account":
					if stage == "identity" {
						w.Header().Set("Location", target.URL+"/account")
						w.WriteHeader(http.StatusTemporaryRedirect)
						return
					}
					fmt.Fprint(w, `{"object":"account","id":"acct_workflow"}`)
				case "/balance":
					fmt.Fprint(w, `{"object":"balance","livemode":false}`)
				case "/refunds":
					refundPosts.Add(1)
					w.Header().Set("Location", target.URL+"/refunds")
					w.WriteHeader(http.StatusTemporaryRedirect)
				default:
					t.Error("unexpected original merchant endpoint")
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer origin.Close()
			identity, _ := (&productCheckoutRuntime{}).ProductCheckoutIdentity(ctx)
			identity.Endpoint = origin.URL
			initial := &merchantBoundRuntime{ProviderRuntime: &durableProductRefundRuntime{}, identity: identity}
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, initial)
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "redirect-refund-command", "test", "The delivered content does not match the purchase description."); err != nil {
				t.Fatal(err)
			}
			service.runtimes = NewRuntimeCatalog(NewStripeRuntime(StripeRuntimeConfig{BaseURL: origin.URL,
				SecretKey: "sk_test_redirect_guard", APIVersion: testStripeAPIVersion, HTTPClient: origin.Client()}))
			repository := jobs.NewRepository(pool)
			job, err := repository.Claim(ctx, "redirect-refund-worker", time.Minute)
			if err != nil || job.Kind != ProductRefundJobKind {
				t.Fatalf("claim refund: %+v %v", job, err)
			}
			cause := service.HandleProductRefundJob(ctx, job)
			if cause == nil || jobs.ShouldRetry(cause) {
				t.Fatalf("redirect result must require review: %v", cause)
			}
			if err := repository.Fail(ctx, job, "redirect-refund-worker", cause); err != nil {
				t.Fatal(err)
			}
			assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
			var jobStatus, attemptStatus string
			var remoteID *string
			if err := pool.QueryRow(ctx, `SELECT j.status,a.status,a.provider_refund_id FROM jobs j
 JOIN product_refund_attempts a ON a.operation_id=(j.payload->>'operationId')::uuid WHERE j.id=$1`, job.ID).Scan(&jobStatus, &attemptStatus, &remoteID); err != nil || jobStatus != "failed" || attemptStatus != "requested" || remoteID != nil {
				t.Fatalf("redirect invented a result: job=%s attempt=%s remoteKnown=%t err=%v", jobStatus, attemptStatus, remoteID != nil, err)
			}
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "another-refund-command", "test", "The delivered content does not match the purchase description."); !errors.Is(err, ErrRefundConflict) {
				t.Fatalf("uncertain refund allowed a fresh operation: %v", err)
			}
			wantPosts := int32(1)
			if stage == "identity" {
				wantPosts = 0
			}
			if targetCalls.Load() != 0 || refundPosts.Load() != wantPosts {
				t.Fatalf("request escaped original merchant: target=%d originalPosts=%d", targetCalls.Load(), refundPosts.Load())
			}
		})
	}
}
