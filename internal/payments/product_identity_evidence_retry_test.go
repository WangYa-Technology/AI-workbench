package payments

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type identityEvidenceRetryRuntime struct {
	durableProductRefundRuntime
	read func(context.Context, ProductPaymentBinding) (ProductPaymentIdentityObservation, error)
}

func (r *identityEvidenceRetryRuntime) ReadProductPaymentIdentity(ctx context.Context, binding ProductPaymentBinding) (ProductPaymentIdentityObservation, error) {
	return r.read(ctx, binding)
}

func TestProductIdentityEvidenceSurvivesWriteAbortAndCancellation(t *testing.T) {
	for _, scenario := range []string{"40001", "40P01", "23514", "cancel", "invalid", "read_error"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			runtime := &identityEvidenceRetryRuntime{}
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
			// Only this isolated fixture loses its original identity evidence,
			// matching the existing legacy-merchant recovery integration tests.
			quarantineExec(t, pool, `TRUNCATE product_checkout_dispatches,product_checkout_requests`)
			job := identityRecoveryJob(t, pool, checkout.PaymentID, buyer)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			runtime.read = func(_ context.Context, b ProductPaymentBinding) (ProductPaymentIdentityObservation, error) {
				reads++
				if reads > 1 {
					t.Error("database retry refetched merchant identity evidence")
				}
				if scenario == "cancel" || scenario == "invalid" || scenario == "read_error" {
					cancel()
				}
				if scenario == "invalid" {
					return ProductPaymentIdentityObservation{}, nil
				}
				if scenario == "read_error" {
					return ProductPaymentIdentityObservation{}, context.Canceled
				}
				charge := b.ProviderChargeID
				if charge == "" {
					charge = "ch_identity_retry"
				}
				return ProductPaymentIdentityObservation{Checkout: &CheckoutObservation{ProviderCheckoutID: b.ProviderCheckoutID, AmountCents: b.AmountCents, Currency: b.Currency, LiveMode: b.LiveMode, Status: "complete", PaymentStatus: "paid", ProviderPaymentID: b.ProviderPaymentID, ProviderChargeID: charge, IntentStatus: "succeeded", AmountReceived: b.AmountCents, ExpiresAt: time.Now().Add(-time.Minute)}}, nil
			}
			if scenario == "40001" || scenario == "40P01" || scenario == "23514" {
				quarantineExec(t, pool, `CREATE SEQUENCE identity_evidence_write_attempt`)
				quarantineExec(t, pool, fmt.Sprintf(`CREATE FUNCTION abort_identity_evidence_test() RETURNS trigger AS $$ BEGIN
 IF nextval('identity_evidence_write_attempt')<=2 THEN RAISE EXCEPTION 'injected identity write abort' USING ERRCODE='%s'; END IF;
 RETURN NEW; END; $$ LANGUAGE plpgsql;
 CREATE TRIGGER abort_identity_evidence_test BEFORE INSERT ON audit_events FOR EACH ROW WHEN(NEW.action='payment.identity_verified') EXECUTE FUNCTION abort_identity_evidence_test()`, scenario))
			}
			err := service.HandleProductIdentityRecoveryJob(ctx, job)
			if scenario == "23514" || scenario == "invalid" || scenario == "read_error" {
				if err == nil {
					t.Fatal("invalid or failed identity evidence accepted")
				}
				if scenario == "23514" {
					var pgerr *pgconn.PgError
					if !errors.As(err, &pgerr) || pgerr.Code != scenario {
						t.Fatal(err)
					}
					quarantineCount(t, pool, `SELECT last_value FROM identity_evidence_write_attempt`, 1)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM product_payment_identity_recoveries WHERE payment_id=$1`, 0, checkout.PaymentID)
				quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 0, checkout.PaymentID)
				return
			}
			if err != nil {
				t.Fatal("verified merchant evidence lost", err)
			}
			if scenario != "cancel" {
				quarantineCount(t, pool, `SELECT last_value FROM identity_evidence_write_attempt`, 3)
			}
			if err = service.HandleProductIdentityRecoveryJob(t.Context(), job); err != nil || reads != 1 {
				t.Fatal("identity replay repeated remote read", reads, err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_payment_identity_recoveries WHERE payment_id=$1`, 1, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 1, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.identity_verified' AND resource_id=$1`, 1, checkout.PaymentID)
			assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			if len(runtime.operations) != 0 {
				t.Fatal("identity write retry dispatched a refund")
			}
		})
	}
}
