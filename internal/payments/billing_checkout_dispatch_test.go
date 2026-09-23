package payments

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

type billingDispatchRuntime struct {
	productCheckoutRuntime
	mu       sync.Mutex
	identity ProductCheckoutIdentity
	requests []CheckoutRequest
	fail     bool
}

func (r *billingDispatchRuntime) Provider() string { return r.identity.Provider }
func (r *billingDispatchRuntime) ProductCheckoutIdentity(context.Context) (ProductCheckoutIdentity, error) {
	return r.identity, nil
}
func (r *billingDispatchRuntime) CreateCheckout(_ context.Context, request CheckoutRequest) (CheckoutSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	request.CheckoutIdentity = nil
	r.requests = append(r.requests, request)
	if r.fail {
		return CheckoutSession{}, context.DeadlineExceeded
	}
	return CheckoutSession{ProviderID: "cs_" + strings.ReplaceAll(request.PaymentID.String(), "-", ""), CheckoutURL: "https://checkout.example.test/" + request.PaymentID.String(), Status: "open", ExpiresAt: time.Now().Add(time.Hour), LiveMode: r.identity.LiveMode}, nil
}

type billingDispatchFixture struct {
	pool     *pgxpool.Pool
	runtime  *billingDispatchRuntime
	service  *Service
	buyer    uuid.UUID
	operator uuid.UUID
	plan     uuid.UUID
	purpose  string
	key      string
}

func newBillingDispatchFixture(t *testing.T, pool *pgxpool.Pool, provider, purpose string) *billingDispatchFixture {
	t.Helper()
	ctx := t.Context()
	f := &billingDispatchFixture{pool: pool, buyer: uuid.New(), operator: uuid.New(), purpose: purpose, key: uuid.NewString()}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Billing dispatch buyer','member')`, f.buyer, f.buyer.String()+"@test.local", "dispatch_"+f.buyer.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Billing plan operator','admin')`, f.operator, f.operator.String()+"@test.local", "planop_"+f.operator.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM subscription_plans WHERE tier_code='creator' AND active`).Scan(&f.plan); err != nil {
		t.Fatal(err)
	}
	f.runtime = &billingDispatchRuntime{identity: ProductCheckoutIdentity{Provider: provider, MerchantID: "MER_test", StoreID: "STO_test", Endpoint: "https://provider.example.test", APIVersion: "test-v1", RequestVersion: "test-checkout-v1"}}
	f.service = newPaymentTestService(t, pool, ServiceConfig{Enabled: true, Provider: provider, WaffoEnvironment: "test", WaffoMerchantID: "MER_test", WaffoStoreID: "STO_test", WaffoProductIDOnetime: "PROD_one", WaffoProductIDSubscription: "PROD_sub"}, NewRuntimeCatalog(f.runtime))
	return f
}

func (f *billingDispatchFixture) begin(ctx context.Context, returnPath string) (BillingCheckout, bool, error) {
	if f.purpose == "subscription" {
		return f.service.BeginSubscriptionCheckout(ctx, f.buyer, f.plan, f.key, "dispatch-test", "https://app.example.test/"+returnPath, "https://app.example.test/cancel")
	}
	return f.service.BeginWalletTopupCheckout(ctx, f.buyer, 4200, f.key, "dispatch-test", "https://app.example.test/"+returnPath, "https://app.example.test/cancel")
}

func TestBillingCheckoutOriginalRequestRetry(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		t.Run(purpose, func(t *testing.T) {
			f := newBillingDispatchFixture(t, pool, "stripe", purpose)
			f.runtime.fail = true
			if _, _, err := f.begin(t.Context(), "original"); err == nil || len(f.runtime.requests) != 1 {
				t.Fatalf("first uncertain dispatch: calls=%d err=%v", len(f.runtime.requests), err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE users SET email=$2 WHERE id=$1`, f.buyer, f.buyer.String()+"@changed.test"); err != nil {
				t.Fatal(err)
			}
			f.runtime.fail = false
			checkout, _, err := f.begin(t.Context(), "changed")
			if err != nil || checkout.Status != "checkout_open" || len(f.runtime.requests) != 2 {
				t.Fatalf("retry: checkout=%+v calls=%d err=%v", checkout, len(f.runtime.requests), err)
			}
			if !reflect.DeepEqual(f.runtime.requests[0], f.runtime.requests[1]) {
				t.Fatal("retry changed the accepted provider request")
			}
		})
	}
}

func TestBillingCheckoutWaffoUnknownResultDoesNotRedispatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		t.Run(purpose, func(t *testing.T) {
			f := newBillingDispatchFixture(t, pool, "waffo_pancake", purpose)
			f.runtime.fail = true
			if _, _, err := f.begin(t.Context(), "original"); err == nil || len(f.runtime.requests) != 1 {
				t.Fatalf("first uncertain dispatch: calls=%d err=%v", len(f.runtime.requests), err)
			}
			// Reconstruct the service to exercise the durable boundary across restart.
			f.service = newPaymentTestService(t, pool, f.service.config, NewRuntimeCatalog(f.runtime))
			f.runtime.fail = false
			if _, _, err := f.begin(t.Context(), "retry"); !errors.Is(err, ErrCheckoutReconciliation) || len(f.runtime.requests) != 1 {
				t.Fatalf("uncertain Waffo checkout resent: calls=%d err=%v", len(f.runtime.requests), err)
			}
		})
	}
}

func TestBillingCheckoutRejectsExpiredAndTerminalReplay(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		for _, state := range []string{"expired", "paid", "cancelled"} {
			t.Run(purpose+"/"+state, func(t *testing.T) {
				f := newBillingDispatchFixture(t, pool, "stripe", purpose)
				checkout, _, err := f.begin(t.Context(), "original")
				if err != nil {
					t.Fatal(err)
				}
				if state == "expired" {
					_, err = pool.Exec(t.Context(), `UPDATE payment_intents SET checkout_expires_at=now()-interval '1 minute' WHERE id=$1`, checkout.PaymentID)
				} else {
					_, err = pool.Exec(t.Context(), `UPDATE payment_intents SET status=$2 WHERE id=$1`, checkout.PaymentID, state)
				}
				if err != nil {
					t.Fatal(err)
				}
				if result, _, err := f.begin(t.Context(), "retry"); !errors.Is(err, ErrCheckoutConflict) || result.CheckoutURL != "" || len(f.runtime.requests) != 1 {
					t.Fatalf("invalid checkout replay: result=%+v calls=%d err=%v", result, len(f.runtime.requests), err)
				}
			})
		}
	}
}

func TestBillingCheckoutOriginalIdentityAndRetryWindow(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		for _, change := range []string{"merchant", "store", "mode", "endpoint", "api", "serializer", "old", "future", "inactive"} {
			t.Run(purpose+"/"+change, func(t *testing.T) {
				f := newBillingDispatchFixture(t, pool, "stripe", purpose)
				f.runtime.fail = true
				if _, _, err := f.begin(t.Context(), "original"); err == nil {
					t.Fatal("expected uncertain response")
				}
				var payment uuid.UUID
				if err := pool.QueryRow(t.Context(), `SELECT id FROM payment_intents WHERE payer_id=$1`, f.buyer).Scan(&payment); err != nil {
					t.Fatal(err)
				}
				expected := ErrCheckoutReconciliation
				switch change {
				case "merchant":
					f.runtime.identity.MerchantID += "_changed"
				case "store":
					f.runtime.identity.StoreID += "_changed"
				case "mode":
					f.runtime.identity.LiveMode = true
				case "endpoint":
					f.runtime.identity.Endpoint += "/changed"
				case "api":
					f.runtime.identity.APIVersion += "_changed"
				case "serializer":
					f.runtime.identity.RequestVersion += "_changed"
				case "old", "future":
					// Simulate historical/corrupt evidence only in the isolated schema.
					tx, err := pool.Begin(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(t.Context())
					if _, err := tx.Exec(t.Context(), `ALTER TABLE billing_checkout_requests DISABLE TRIGGER billing_checkout_requests_immutable`); err != nil {
						t.Fatal(err)
					}
					stamp := time.Now().Add(-24 * time.Hour)
					if change == "future" {
						stamp = time.Now().Add(time.Hour)
					}
					if _, err := tx.Exec(t.Context(), `UPDATE billing_checkout_requests SET created_at=$2 WHERE payment_id=$1`, payment, stamp); err != nil {
						t.Fatal(err)
					}
					if _, err := tx.Exec(t.Context(), `ALTER TABLE billing_checkout_requests ENABLE TRIGGER billing_checkout_requests_immutable`); err != nil {
						t.Fatal(err)
					}
					if err := tx.Commit(t.Context()); err != nil {
						t.Fatal(err)
					}
				case "inactive":
					if _, err := pool.Exec(t.Context(), `UPDATE users SET status='suspended' WHERE id=$1`, f.buyer); err != nil {
						t.Fatal(err)
					}
					expected = ErrInvalidCheckout
				}
				f.runtime.fail = false
				if _, _, err := f.begin(t.Context(), "retry"); !errors.Is(err, expected) || len(f.runtime.requests) != 1 {
					t.Fatalf("changed original evidence dispatched: calls=%d err=%v", len(f.runtime.requests), err)
				}
			})
		}
	}
}

func TestBillingCheckoutConcurrentReservationAndInterruption(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, provider := range []string{"stripe", "waffo_pancake"} {
		for _, purpose := range []string{"wallet_topup", "subscription"} {
			for _, interrupt := range []bool{false, true} {
				name := provider + "/" + purpose + "/concurrent"
				if interrupt {
					name = provider + "/" + purpose + "/interrupted"
				}
				t.Run(name, func(t *testing.T) {
					f := newBillingDispatchFixture(t, pool, provider, purpose)
					originalService := f.service
					traced, entered, release := testutil.GateNthQuery(t, pool, billingCheckoutSelect+` WHERE id=$1 FOR UPDATE`, 2)
					f.service = newPaymentTestService(t, traced, f.service.config, NewRuntimeCatalog(f.runtime))
					ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
					defer cancel()
					var wg sync.WaitGroup
					t.Cleanup(func() { cancel(); release(); wg.Wait() })
					done := make(chan error, 1)
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, _, err := f.begin(ctx, "original")
						done <- err
					}()
					select {
					case <-entered:
					case err := <-done:
						t.Fatalf("reservation gate not reached: %v", err)
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					var dispatches int
					if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_checkout_dispatches d JOIN payment_intents p ON p.id=d.payment_id WHERE p.payer_id=$1`, f.buyer).Scan(&dispatches); err != nil || dispatches != 1 {
						t.Fatalf("reservation not committed before send: count=%d err=%v", dispatches, err)
					}
					if interrupt {
						cancel()
						if err := <-done; err == nil {
							t.Fatal("interrupted invocation succeeded")
						}
					}
					other := *f
					other.service = originalService
					_, _, err := other.begin(t.Context(), "retry")
					if provider == "stripe" && err != nil {
						t.Fatalf("Stripe original request could not resume: %v", err)
					}
					if provider == "waffo_pancake" && !errors.Is(err, ErrCheckoutReconciliation) {
						t.Fatalf("Waffo retry reconstructed a permit: %v", err)
					}
					release()
					if !interrupt {
						if err := <-done; err != nil {
							t.Fatalf("original invocation: %v", err)
						}
					}
					want := 1
					if interrupt && provider == "waffo_pancake" {
						want = 0
					}
					if len(f.runtime.requests) != want {
						t.Fatalf("dispatches=%d want=%d", len(f.runtime.requests), want)
					}
				})
			}
		}
	}
}

func TestBillingCheckoutRemoteSuccessLocalFailure(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, provider := range []string{"stripe", "waffo_pancake"} {
		for _, purpose := range []string{"wallet_topup", "subscription"} {
			t.Run(provider+"/"+purpose, func(t *testing.T) {
				f := newBillingDispatchFixture(t, pool, provider, purpose)
				if _, err := pool.Exec(t.Context(), `CREATE FUNCTION fail_billing_checkout_save() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
				 IF NEW.status='checkout_open' THEN RAISE EXCEPTION 'isolated checkout persistence failure'; END IF;
				 RETURN NEW; END $$;
				 CREATE TRIGGER fail_billing_checkout_save BEFORE UPDATE ON payment_intents FOR EACH ROW EXECUTE FUNCTION fail_billing_checkout_save()`); err != nil {
					t.Fatal(err)
				}
				if _, _, err := f.begin(t.Context(), "original"); err == nil || len(f.runtime.requests) != 1 {
					t.Fatalf("missing injected persistence failure: calls=%d err=%v", len(f.runtime.requests), err)
				}
				if _, err := pool.Exec(t.Context(), `DROP TRIGGER fail_billing_checkout_save ON payment_intents; DROP FUNCTION fail_billing_checkout_save()`); err != nil {
					t.Fatal(err)
				}
				_, _, err := f.begin(t.Context(), "retry")
				if provider == "waffo_pancake" {
					if !errors.Is(err, ErrCheckoutReconciliation) || len(f.runtime.requests) != 1 {
						t.Fatalf("Waffo local loss duplicated remote session: calls=%d err=%v", len(f.runtime.requests), err)
					}
				} else if err != nil || len(f.runtime.requests) != 2 || !reflect.DeepEqual(f.runtime.requests[0], f.runtime.requests[1]) {
					t.Fatalf("Stripe original request not preserved: calls=%d err=%v", len(f.runtime.requests), err)
				}
			})
		}
	}
}

func TestBillingCheckoutEvidenceImmutableAndLegacyNotInferred(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	f := newBillingDispatchFixture(t, pool, "stripe", "wallet_topup")
	checkout, _, err := f.begin(t.Context(), "original")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE billing_checkout_requests SET request='{}' WHERE payment_id=$1`,
		`DELETE FROM billing_checkout_requests WHERE payment_id=$1`,
		`UPDATE billing_checkout_dispatches SET reserved_at=now() WHERE payment_id=$1`,
		`DELETE FROM billing_checkout_dispatches WHERE payment_id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), query, checkout.PaymentID); err == nil {
			t.Fatalf("immutable evidence changed: %s", query)
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0137_billing_checkout_dispatch.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), string(down)); err == nil {
		_ = tx.Rollback(t.Context())
		t.Fatal("downgrade discarded original billing evidence")
	}
	_ = tx.Rollback(t.Context())
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		legacy := newBillingDispatchFixture(t, pool, "stripe", purpose)
		resource := legacy.buyer
		if purpose == "subscription" {
			resource = legacy.plan
		}
		// Model a pre-0138 intent in this isolated schema. Restore the new-writer
		// guard in the same transaction; production must never infer old terms.
		legacyTx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := legacyTx.Exec(t.Context(), `ALTER TABLE payment_intents DISABLE TRIGGER payment_intents_subscription_contract`); err != nil {
			t.Fatal(err)
		}
		if _, err := legacyTx.Exec(t.Context(), `INSERT INTO payment_intents(id,provider,purpose,payer_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key) VALUES($1,'stripe',$2,$3,$4,4200,'USD','checkout_pending',false,$5)`, uuid.New(), purpose, legacy.buyer, resource, legacy.key); err != nil {
			t.Fatal(err)
		}
		if err := legacyTx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		// PostgreSQL cannot alter a table while the transaction still has
		// deferred trigger events. Re-enable the writer guard after the legacy
		// fixture transaction has committed.
		if _, err := pool.Exec(t.Context(), `ALTER TABLE payment_intents ENABLE TRIGGER payment_intents_subscription_contract`); err != nil {
			t.Fatal(err)
		}
		if _, _, err := legacy.begin(t.Context(), "retry"); !errors.Is(err, ErrCheckoutReconciliation) || len(legacy.runtime.requests) != 0 {
			t.Fatalf("legacy request reconstructed: calls=%d err=%v", len(legacy.runtime.requests), err)
		}
	}
}
