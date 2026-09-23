package payments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type snapshotCheckoutRuntime struct {
	returnURLRuntime
	identity    ProductCheckoutIdentity
	identityErr error
}

func (r *snapshotCheckoutRuntime) ProductCheckoutIdentity(context.Context) (ProductCheckoutIdentity, error) {
	return r.identity, r.identityErr
}

func TestProductCheckoutRequestEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	base, _ := (&productCheckoutRuntime{}).ProductCheckoutIdentity(ctx)
	for _, scenario := range []string{"profile_change", "merchant_change", "store_change", "endpoint_change", "api_change", "serializer_change", "mode_change", "missing_snapshot", "old_request", "future_request", "identity_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			buyer, _, _, product := newProductCheckoutFixture(t, pool)
			runtime := &snapshotCheckoutRuntime{identity: base}
			service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
			version := productOfferVersion(t, pool, product)
			begin := func() (Checkout, error) {
				result, _, err := service.BeginProductCheckout(ctx, buyer, product, "snapshot-evidence", "test", "https://example.test/success", "https://example.test/cancel", true, version)
				return result, err
			}
			if _, err := begin(); err == nil || len(runtime.requests) != 1 {
				t.Fatalf("expected lost first response: %v", err)
			}
			first := runtime.requests[0]
			var encodedIdentity, encodedRequest []byte
			if err := pool.QueryRow(ctx, `SELECT identity,request FROM product_checkout_requests WHERE payment_id=$1`, first.PaymentID).Scan(&encodedIdentity, &encodedRequest); err != nil {
				t.Fatal(err)
			}
			var stored CheckoutRequest
			if err := json.Unmarshal(encodedRequest, &stored); err != nil {
				t.Fatal(err)
			}
			first.CheckoutIdentity = nil
			if !reflect.DeepEqual(stored, first) {
				t.Fatal("first outbound request was not durably frozen")
			}
			for _, sql := range []string{
				`UPDATE product_checkout_requests SET request='{}' WHERE payment_id=$1`,
				`DELETE FROM product_checkout_requests WHERE payment_id=$1`,
			} {
				if _, err := pool.Exec(ctx, sql, first.PaymentID); err == nil {
					t.Fatal("request evidence was mutable")
				}
			}
			switch scenario {
			case "profile_change":
				if _, err := pool.Exec(ctx, `UPDATE users SET email=$2 WHERE id=$1`, buyer, buyer.String()+"@changed.test"); err != nil {
					t.Fatal(err)
				}
			case "merchant_change":
				runtime.identity.MerchantID = "acct_different"
			case "store_change":
				runtime.identity.StoreID = "different-store"
			case "endpoint_change":
				runtime.identity.Endpoint = "https://other.example.test/v1"
			case "api_change":
				runtime.identity.APIVersion = "other-version"
			case "serializer_change":
				runtime.identity.RequestVersion = "other-serializer"
			case "mode_change":
				runtime.identity.LiveMode = true
			case "identity_unavailable":
				runtime.identityErr = ErrProviderUnavailable
			case "missing_snapshot", "old_request", "future_request":
				// Fixture-only historical state: rebuild this schema's snapshot table
				// from saved evidence, without weakening the production trigger.
				if _, err := pool.Exec(ctx, `CREATE TABLE request_fixture AS SELECT * FROM product_checkout_requests; TRUNCATE product_checkout_dispatches, product_checkout_requests`); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_requests SELECT * FROM request_fixture WHERE payment_id<>$1`, first.PaymentID); err != nil {
					t.Fatal(err)
				}
				if scenario != "missing_snapshot" {
					offset := "-24 hours"
					if scenario == "future_request" {
						offset = "24 hours"
					}
					if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_requests(payment_id,identity,request,created_at) VALUES($1,$2,$3,now()+$4::interval)`, first.PaymentID, encodedIdentity, encodedRequest, offset); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := pool.Exec(ctx, `DROP TABLE request_fixture`); err != nil {
					t.Fatal(err)
				}
			}
			checkout, err := begin()
			if scenario == "profile_change" {
				if err != nil || checkout.PaymentID != first.PaymentID || len(runtime.requests) != 2 {
					t.Fatalf("replay: %v", err)
				}
				second := runtime.requests[1]
				second.CheckoutIdentity = nil
				if !reflect.DeepEqual(first, second) {
					t.Fatal("mutable profile changed the retried request")
				}
				// A usable saved URL needs neither a fresh merchant read nor another checkout.
				runtime.identityErr = ErrProviderUnavailable
				if _, err := begin(); err != nil || len(runtime.requests) != 2 {
					t.Fatalf("saved URL recovery: %v", err)
				}
			} else {
				if scenario != "identity_unavailable" && !errors.Is(err, ErrCheckoutReconciliation) {
					t.Fatalf("want reconciliation, got %v", err)
				}
				if err == nil || len(runtime.requests) != 1 {
					t.Fatal("uncertain command was sent again")
				}
			}
		})
	}
	down, err := os.ReadFile("../platform/database/migrations/0085_product_checkout_requests.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard original product checkout requests") {
		t.Fatalf("rollback lost original requests: %v", err)
	}
}

func TestStripeProductMerchantIdentity(t *testing.T) {
	for _, scenario := range []string{"test", "live", "wrong_mode", "missing_mode", "wrong_account", "credentials_rejected"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
					t.Error("identity request not authenticated")
				}
				if scenario == "credentials_rejected" {
					w.WriteHeader(401)
					return
				}
				if r.URL.Path == "/account" {
					id := "acct_original"
					if scenario == "wrong_account" {
						id = "cus_invalid"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"object": "account", "id": id})
					return
				}
				if r.URL.Path != "/balance" {
					t.Error("unexpected identity endpoint")
					w.WriteHeader(404)
					return
				}
				body := map[string]any{"object": "balance", "livemode": scenario == "live" || scenario == "wrong_mode"}
				if scenario == "missing_mode" {
					delete(body, "livemode")
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "test-key", APIVersion: testStripeAPIVersion, LiveMode: scenario == "live"})
			identity, err := checkoutIdentity(context.Background(), runtime)
			if scenario == "test" || scenario == "live" {
				if err != nil || identity.MerchantID != "acct_original" || identity.LiveMode != (scenario == "live") {
					t.Fatalf("identity: %+v %v", identity, err)
				}
			} else if err == nil {
				t.Fatal("unproven merchant/mode accepted")
			}
		})
	}
}

func TestWaffoProductOriginalRequestRecovery(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	merchant, store := "MER_original", "STO_original"
	var requests []map[string]any
	connector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-connector-token" {
			t.Error("missing connector auth")
			w.WriteHeader(401)
			return
		}
		identity := ProductCheckoutIdentity{Provider: "waffo_pancake", MerchantID: merchant, StoreID: store, APIVersion: waffoProductCheckoutAPI, RequestVersion: waffoProductCheckoutVersion}
		if r.URL.Path == "/checkout/identity" {
			_ = json.NewEncoder(w).Encode(identity)
			return
		}
		if r.URL.Path != "/checkout" {
			w.WriteHeader(404)
			return
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var committed bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_checkout_requests WHERE payment_id=$1)`, input["paymentId"]).Scan(&committed); err != nil || !committed {
			t.Errorf("request was dispatched before evidence committed: %v", err)
			w.WriteHeader(500)
			return
		}
		requests = append(requests, input)
		if len(requests) == 1 {
			w.WriteHeader(502)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"providerId": "CHK_original", "checkoutUrl": "https://checkout.waffo.ai/session/CHK_original", "status": "open", "paymentStatus": "pending", "expiresAt": time.Now().Add(time.Hour), "liveMode": false, "checkoutIdentity": input["checkoutIdentity"]})
	}))
	defer connector.Close()
	runtime := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: connector.URL, ConnectorToken: "test-connector-token", Environment: "test"})
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, Provider: "waffo_pancake", WaffoMerchantID: merchant, WaffoStoreID: store, WaffoProductIDOnetime: "PROD_original", WaffoEnvironment: "test"}, NewRuntimeCatalog(runtime))
	version := productOfferVersion(t, pool, product)
	begin := func() error {
		_, _, err := service.BeginProductCheckout(ctx, buyer, product, "waffo-original-request", "test", "https://example.test/success", "https://example.test/cancel", true, version)
		return err
	}
	if err := begin(); err == nil || len(requests) != 1 {
		t.Fatalf("first request: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email=$2 WHERE id=$1`, buyer, buyer.String()+"@changed.test"); err != nil {
		t.Fatal(err)
	}
	service.config.WaffoProductIDOnetime = "PROD_changed"
	merchant = "MER_changed"
	if err := begin(); !errors.Is(err, ErrCheckoutReconciliation) || len(requests) != 1 {
		t.Fatalf("changed merchant got another request: %v", err)
	}
	merchant = "MER_original"
	if err := begin(); !errors.Is(err, ErrCheckoutReconciliation) || len(requests) != 1 {
		t.Fatalf("uncertain Waffo checkout was dispatched again: %v", err)
	}
	var original CheckoutRequest
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT request FROM product_checkout_requests WHERE payment_id=$1`, requests[0]["paymentId"]).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &original); err != nil || original.ProductID != "PROD_original" {
		t.Fatalf("original checkout evidence changed: %+v %v", original, err)
	}
}

func TestProductIdentityFailureDoesNotCreateOrder(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	runtime := &snapshotCheckoutRuntime{identityErr: ErrProviderUnavailable}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	if _, _, err := service.BeginProductCheckout(ctx, buyer, product, "unproven-merchant", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product)); err == nil {
		t.Fatal("unproven merchant accepted")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE buyer_id=$1`, buyer).Scan(&count); err != nil || count != 0 || len(runtime.requests) != 0 {
		t.Fatalf("identity failure left an order: %d %v", count, err)
	}
}

func TestWaffoProductIdentityRejectsIncompleteAssertions(t *testing.T) {
	for _, field := range []string{"provider", "merchantId", "storeId", "liveMode", "apiVersion", "requestVersion"} {
		t.Run(field, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/checkout/identity" || r.Header.Get("Authorization") != "Bearer connector-test-token" {
					t.Error("unauthenticated identity request")
				}
				response := map[string]any{"provider": "waffo_pancake", "merchantId": "MER_original", "storeId": "STO_original", "liveMode": false, "apiVersion": waffoProductCheckoutAPI, "requestVersion": waffoProductCheckoutVersion}
				delete(response, field)
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			runtime := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: server.URL, ConnectorToken: "connector-test-token", Environment: "test"})
			if _, err := checkoutIdentity(context.Background(), runtime); err == nil {
				t.Fatal("incomplete connector assertion was trusted")
			}
		})
	}
}

func TestStripeProductCredentialRotationPreservesWireRequest(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	var forms, keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/account":
			account := "acct_original"
			if r.Header.Get("Authorization") == "Bearer other-account-key" {
				account = "acct_other123"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": account, "object": "account"})
		case "/balance":
			_, _ = io.WriteString(w, `{"object":"balance","livemode":false}`)
		case "/checkout/sessions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			forms = append(forms, string(body))
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			if len(forms) == 1 {
				w.WriteHeader(502)
				return
			}
			if r.Header.Get("Authorization") != "Bearer rotated-key" {
				t.Error("rotated credential was not used")
			}
			var paymentID string
			if err := pool.QueryRow(ctx, `SELECT id::text FROM payment_intents WHERE payer_id=$1`, buyer).Scan(&paymentID); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cs_original", "url": "https://checkout.stripe.com/c/pay/original", "status": "open", "payment_status": "unpaid", "expires_at": time.Now().Add(time.Hour).Unix(), "livemode": false, "amount_total": 1900, "currency": "usd", "client_reference_id": paymentID})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, nil)
	begin := func(credential string) error {
		service.runtimes = NewRuntimeCatalog(NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: credential, APIVersion: testStripeAPIVersion}))
		_, _, err := service.BeginProductCheckout(ctx, buyer, product, "credential-rotation", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
		return err
	}
	if err := begin("original-key"); err == nil || len(forms) != 1 {
		t.Fatalf("first request: %v", err)
	}
	if err := begin("other-account-key"); !errors.Is(err, ErrCheckoutReconciliation) || len(forms) != 1 {
		t.Fatalf("different account was allowed: %v", err)
	}
	if err := begin("rotated-key"); err != nil || len(forms) != 2 {
		t.Fatalf("same-account rotation failed: %v", err)
	}
	if forms[0] != forms[1] || keys[0] == "" || keys[0] != keys[1] {
		t.Fatal("rotation changed the original wire request or idempotency key")
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT row_to_json(r)::text FROM product_checkout_requests r JOIN payment_intents p ON p.id=r.payment_id WHERE p.payer_id=$1`, buyer).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"original-key", "other-account-key", "rotated-key"} {
		if strings.Contains(stored, secret) {
			t.Fatal("stored a payment credential")
		}
	}
}
