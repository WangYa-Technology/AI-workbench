package payments

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestProductRefundReadWriteTransientAbort(t *testing.T) {
	for _, code := range []string{"40001", "40P01", "23514"} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/partial_%t", code, partial), func(t *testing.T) {
				pool, cleanup := paymentTestPool(t)
				defer cleanup()
				ctx := t.Context()
				service, checkout, _, _, _ := fulfilledRefundFixture(t, pool, &refundReadRuntime{})
				history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
				if err != nil {
					t.Fatal(err)
				}
				history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
				if err != nil {
					t.Fatal(err)
				}
				checkID := history.LatestCheck.ID
				var request RefundReadRequest
				if err = pool.QueryRow(ctx, `SELECT id,resource_id,provider_payment_id,amount_cents,currency,live_mode FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&request.PaymentID, &request.ResourceID, &request.ProviderPaymentID, &request.AmountCents, &request.Currency, &request.LiveMode); err != nil {
					t.Fatal(err)
				}
				observations := []RefundObservation{{ProviderID: "re_write_retry", ProviderPaymentID: request.ProviderPaymentID, AmountCents: 100, Currency: "USD", Status: "succeeded"}}
				// Sequences survive transaction rollback, so two aborted writes are followed
				// by one accepted write without mutating the production service or driver.
				quarantineExec(t, pool, `CREATE SEQUENCE refund_read_write_test_attempt`)
				quarantineExec(t, pool, fmt.Sprintf(`CREATE FUNCTION abort_refund_read_test_write() RETURNS trigger AS $$
    BEGIN
     IF nextval('refund_read_write_test_attempt')<=2 THEN
      RAISE EXCEPTION 'injected evidence write abort' USING ERRCODE='%s';
     END IF;
     RETURN NEW;
    END; $$ LANGUAGE plpgsql;
    CREATE TRIGGER refund_read_write_test BEFORE UPDATE OF observations ON product_refund_checks
    FOR EACH ROW EXECUTE FUNCTION abort_refund_read_test_write()`, code))
				var readErr error
				if partial {
					readErr = newProviderFailure("payment_timeout", 0)
				}
				stored, saveErr := service.saveRefundReadResult(ctx, checkID, request, observations, readErr, nil)
				if code == "23514" {
					var rejected *pgconn.PgError
					if stored || !errors.As(saveErr, &rejected) || rejected.Code != code {
						t.Fatal("constraint rejection hidden", stored, saveErr)
					}
					quarantineCount(t, pool, `SELECT last_value FROM refund_read_write_test_attempt`, 1)
					quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE id=$1 AND status='requested' AND observations IS NULL`, 1, checkID)
					quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.refund_check_incomplete' AND resource_id=$1`, 0, checkout.PaymentID)
					return
				}
				if saveErr != nil || !stored {
					t.Fatal("verified response lost on transaction abort", stored, saveErr)
				}
				quarantineCount(t, pool, `SELECT last_value FROM refund_read_write_test_attempt`, 3)
				state, audits := "observed", 0
				if partial {
					state, audits = "failed", 1
				}
				quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE id=$1 AND status=$2 AND jsonb_array_length(observations)=1`, 1, checkID, state)
				quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.refund_check_incomplete' AND resource_id=$1`, audits, checkout.PaymentID)
				quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 1, checkout.PaymentID)
				quarantineCount(t, pool, `SELECT count(*) FROM product_refund_read_receipts WHERE check_id=$1`, 0, checkID)
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE refund_check_id=$1`, 0, checkID)
				quarantineCount(t, pool, `SELECT count(*) FROM product_refund_observation_review WHERE payment_id=$1`, 1, checkout.PaymentID)
			})
		}
	}
}

func TestProductRefundReadWriteCancellationAndUnknownFailure(t *testing.T) {
	t.Run("default_budget", func(t *testing.T) {
		before := time.Now()
		_, err := retryPaymentEvidenceWrite(t.Context(), func(ctx context.Context) (bool, error) {
			<-ctx.Done()
			return false, ctx.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(before) > 7*time.Second {
			t.Fatal("evidence persistence has no default budget", err)
		}
	})
	t.Run("retry_wait_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		entered := make(chan struct{})
		result := make(chan error, 1)
		attempts := 0
		go func() {
			_, err := retryPaymentEvidenceWrite(ctx, func(context.Context) (bool, error) {
				attempts++
				if attempts == 1 {
					close(entered)
				}
				return false, &pgconn.PgError{Code: "40001"}
			})
			result <- err
		}()
		<-entered
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled write retried", err)
			}
		case <-time.After(time.Second):
			t.Fatal("write backoff ignored cancellation")
		}
	})
	t.Run("caller_deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
		defer cancel()
		before := time.Now()
		attempts := 0
		_, err := retryPaymentEvidenceWrite(ctx, func(context.Context) (bool, error) {
			attempts++
			return false, &pgconn.PgError{Code: "40P01"}
		})
		if !errors.Is(err, context.DeadlineExceeded) || attempts < 2 || time.Since(before) > time.Second {
			t.Fatal("write budget ignored", attempts, err)
		}
	})
	t.Run("unknown_commit_outcome", func(t *testing.T) {
		uncertain := errors.New("connection lost while committing")
		attempts := 0
		stored, err := retryPaymentEvidenceWrite(t.Context(), func(context.Context) (bool, error) {
			attempts++
			return false, uncertain
		})
		if stored || !errors.Is(err, uncertain) || attempts != 1 {
			t.Fatal("unknown outcome was retried", stored, attempts, err)
		}
	})
}
