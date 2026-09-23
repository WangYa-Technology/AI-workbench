package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

type refundReadRuntime struct {
	durableProductRefundRuntime
	observations []RefundObservation
	readError    error
	reads        int
}

func (r *refundReadRuntime) ReadProductRefunds(_ context.Context, _ RefundReadRequest) ([]RefundObservation, error) {
	r.reads++
	return r.observations, r.readError
}

func TestProductRefundCheckWorkflow(t *testing.T) {
	for _, scenario := range []string{"success", "failure", "pending", "missing", "unknown", "partial", "invalid", "resume", "remote_error", "failure_with_unknown"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			runtime := &refundReadRuntime{}
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "query-refund-command", "test", "The delivered content does not match its description."); err != nil {
				t.Fatal(err)
			}
			// Lose dispatch response: only the persisted operation and provider query
			// can restore the remote refund identity here.
			runtime.failNext = true
			if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err == nil {
				t.Fatal("expected lost response")
			}
			operation := runtime.operations[0]
			remote := RefundObservation{ProviderID: "re_queryrefund", ProviderPaymentID: "pi_product123", AmountCents: 1900, Currency: "USD", Status: "succeeded", OperationID: &operation}
			if err := pool.QueryRow(ctx, `SELECT provider_payment_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&remote.ProviderPaymentID); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "failure", "failure_with_unknown":
				remote.Status = "failed"
			case "pending":
				remote.Status = "pending"
			case "unknown":
				id := uuid.New()
				remote.OperationID = &id
			case "partial":
				remote.AmountCents = 100
			case "invalid":
				remote.ProviderPaymentID = "pi_wrongquery"
			case "remote_error":
				runtime.readError = newProviderFailure("payment_authentication", 0)
			}
			runtime.observations = []RefundObservation{remote}
			if scenario == "failure_with_unknown" {
				runtime.observations = append(runtime.observations, RefundObservation{ProviderID: "re_manualunknown", ProviderPaymentID: remote.ProviderPaymentID, AmountCents: 1900, Currency: "USD", Status: "succeeded"})
			}

			if scenario == "missing" {
				runtime.observations = []RefundObservation{}
			}
			history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 1)
			if err != nil || !history.CanCheck || len(history.Items) != 1 {
				t.Fatalf("initial history: %#v %v", history, err)
			}
			if _, err := service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion+1); !errors.Is(err, ErrRefundConflict) {
				t.Fatalf("stale version accepted: %v", err)
			}
			history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
			if err != nil || history.LatestCheck == nil || history.CanCheck {
				t.Fatalf("request: %#v %v", history, err)
			}
			if _, err := service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion); !errors.Is(err, ErrRefundConflict) {
				t.Fatalf("parallel check accepted: %v", err)
			}
			callsBefore := len(runtime.operations)
			dispatchErr := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID))
			if dispatchErr == nil || jobs.ShouldRetry(dispatchErr) || len(runtime.operations) != callsBefore {
				t.Fatalf("active check allowed another dispatch: %v", dispatchErr)
			}
			var job jobs.Job
			if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.id=$1`, history.LatestCheck.ID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
				t.Fatal(err)
			}
			if scenario == "resume" {
				var request RefundReadRequest
				if err := pool.QueryRow(ctx, `SELECT id,resource_id,provider_payment_id,amount_cents,currency,live_mode FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&request.PaymentID, &request.ResourceID, &request.ProviderPaymentID, &request.AmountCents, &request.Currency, &request.LiveMode); err != nil {
					t.Fatal(err)
				}
				if err := service.saveRefundObservations(ctx, history.LatestCheck.ID, request, runtime.observations); err != nil {
					t.Fatal(err)
				}
				// Crash after durable evidence commit, then provider goes offline. Resume
				// consumes saved observations without querying again.
				runtime.readError = ErrProviderUnavailable
			}
			err = service.HandleProductRefundCheckJob(ctx, job)
			if scenario == "invalid" || scenario == "remote_error" {
				if err == nil {
					t.Fatal("invalid provider evidence accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			after, err := service.RefundHistory(ctx, checkout.PaymentID, "", 1)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "success" || scenario == "resume" {
				assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
				if after.LatestCheck.Status != "completed" || after.LatestCheck.UnresolvedCount != 0 || after.Items[0].ReconciliationRequired {
					t.Fatalf("successful query not reconciled: %#v", after)
				}
			} else if scenario == "failure" || scenario == "failure_with_unknown" {
				assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
			} else {
				assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
			}
			if scenario == "pending" || scenario == "missing" || scenario == "unknown" || scenario == "partial" {
				if after.LatestCheck.UnresolvedCount == 0 || !after.Items[0].ReconciliationRequired {
					t.Fatalf("uncertain query marked resolved: %#v", after)
				}
			}
			if scenario == "invalid" || scenario == "remote_error" {
				if after.LatestCheck.Status != "failed" {
					t.Fatalf("query failure hidden: %#v", after)
				}
			}
			if scenario == "failure_with_unknown" {
				order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
				if err != nil || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" {
					t.Fatalf("buyer capability ignores unresolved funds: %#v %v", order, err)
				}
				if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "unresolved-new-refund", "test", "The delivered content does not match its description."); !errors.Is(err, ErrRefundConflict) {
					t.Fatalf("buyer could refund uncertain funds: %v", err)
				}
			}
			count := len(runtime.operations)
			reads := runtime.reads
			if err := service.HandleProductRefundCheckJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if len(runtime.operations) != count || runtime.reads != reads {
				t.Fatal("completed job moved money or queried again")
			}
			if scenario == "resume" && runtime.reads != 0 {
				t.Fatal("resume ignored durable evidence")
			}
			if scenario == "success" || scenario == "resume" || scenario == "failure" || scenario == "pending" {
				var source, eventType, state string
				if err := pool.QueryRow(ctx, `SELECT e.evidence_source,e.event_type,p.status FROM payment_provider_events e JOIN payment_provider_event_processing p ON p.event_id=e.id WHERE e.refund_check_id=$1`, history.LatestCheck.ID).Scan(&source, &eventType, &state); err != nil || source != "provider_query" || eventType != "refund.observed" || state != "processed" {
					t.Fatalf("query mislabeled as webhook: %s %s %s %v", source, eventType, state, err)
				}
			}
		})
	}
}

func TestProductRefundCheckHistoryPaginationAndExhaustion(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &refundReadRuntime{}
	service, checkout, _, _, _ := fulfilledRefundFixture(t, pool, runtime)
	for i := 0; i < 3; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,status,requested_at)
    SELECT $1,id,provider,provider_payment_id,amount_cents,currency,true,'failed','2026-01-01'::timestamptz FROM payment_intents WHERE id=$2`, uuid.New(), checkout.PaymentID); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for i := 0; i < 3; i++ {
		history, err := service.RefundHistory(ctx, checkout.PaymentID, cursor, 1)
		if err != nil || len(history.Items) != 1 {
			t.Fatalf("history page: %#v %v", history, err)
		}
		id := history.Items[0].OperationID
		if seen[id] {
			t.Fatal("duplicate history row")
		}
		seen[id] = true
		if i < 2 {
			if history.NextCursor == nil {
				t.Fatal("missing cursor")
			}
			cursor = *history.NextCursor
		} else if history.NextCursor != nil {
			t.Fatal("extra page")
		}
	}
	if _, err := service.RefundHistory(ctx, checkout.PaymentID, uuid.NewString(), 1); !errors.Is(err, ErrInvalidRefund) {
		t.Fatalf("foreign cursor accepted: %v", err)
	}
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	previous := history.LatestCheck.ID
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=max_attempts,last_error_code='payment_timeout' WHERE id=(SELECT job_id FROM product_refund_checks WHERE id=$1)`, previous); err != nil {
		t.Fatal(err)
	}
	history, err = service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil || history.LatestCheck.Status != "failed" || !history.CanCheck {
		t.Fatalf("exhausted check holds active slot: %#v %v", history, err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil || history.LatestCheck.ID == previous {
		t.Fatalf("cannot retry exhausted check: %#v %v", history, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM product_refund_checks WHERE id=$1`, previous).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("prior evidence lost: %s %v", status, err)
	}
	// The webhook public boundary must not be able to forge API-query evidence.
	body, _ := json.Marshal(map[string]any{"id": "evt_forgedquery", "object": "event", "api_version": testStripeAPIVersion, "created": 1, "livemode": false, "type": "refund.observed", "data": map[string]any{"object": map[string]any{"id": "re_forgedquery", "object": "refund"}}})
	event, err := minimizeStripeEvent(body, testStripeAPIVersion, false)
	if err != nil {
		t.Fatal(err)
	}
	if event.Supported {
		t.Fatal("external webhook can forge query observations")
	}
}

func TestProductRefundCheckAuthenticatedHTTP(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &durableProductRefundRuntime{failNext: true}
	service, checkout, buyer, product, _ := fulfilledRefundFixture(t, pool, runtime)
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "http-query-refund", "test", "The delivered content does not match its description."); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err == nil {
		t.Fatal("expected lost response")
	}
	operation := runtime.operations[0]
	var providerPayment string
	if err := pool.QueryRow(ctx, `SELECT provider_payment_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&providerPayment); err != nil {
		t.Fatal(err)
	}
	requests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-query-key" {
			t.Error("query attempted money movement or omitted authentication")
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/account" {
			fmt.Fprint(w, `{"id":"acct_workflow","object":"account"}`)
		} else if r.URL.Path == "/balance" {
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
		} else if r.URL.Path == "/payment_intents/"+providerPayment {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": providerPayment, "amount": 1900, "amount_received": 1900, "currency": "usd", "livemode": false, "status": "succeeded", "metadata": map[string]string{"hcai_payment_id": checkout.PaymentID.String(), "hcai_resource_id": product.String(), "hcai_purpose": "product"}})
		} else if r.URL.Path == "/refunds" {
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "has_more": false, "data": []map[string]any{{"id": "re_authenticatedquery", "payment_intent": providerPayment, "amount": 1900, "currency": "usd", "status": "succeeded", "metadata": map[string]string{"hcai_payment_id": checkout.PaymentID.String(), "hcai_refund_operation_id": operation.String()}}}})
		} else {
			t.Error("unexpected provider path")
			w.WriteHeader(404)
		}
	}))
	defer provider.Close()
	service.runtimes = NewRuntimeCatalog(originalMerchantHTTPRuntime(t, provider, "test-query-key"))
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.id=$1`, history.LatestCheck.ID).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductRefundCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if requests != 4 {
		t.Fatalf("unexpected provider calls: %d", requests)
	}
	assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
	for _, query := range []string{
		`UPDATE product_refund_checks SET observations='[]' WHERE id=$1`,
		`DELETE FROM product_refund_checks WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, query, history.LatestCheck.ID); err == nil {
			t.Fatal("query evidence can be rewritten")
		}
	}
	// Each migration's own evidence guard must reject before dependency DDL.
	// New execution evidence intentionally prevents peeling back newer schemas.
	for _, version := range []string{"0128_product_refund_read_executions", "0083_product_refund_checks"} {
		down, err := os.ReadFile("../platform/database/migrations/" + version + ".down.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard refund") {
			t.Fatal("rollback did not preserve authenticated evidence", version, err)
		}
	}

}

type blockedRefundReader struct {
	durableProductRefundRuntime
	entered     chan struct{}
	release     chan struct{}
	observation RefundObservation
}

func (r *blockedRefundReader) ReadProductRefunds(ctx context.Context, _ RefundReadRequest) ([]RefundObservation, error) {
	close(r.entered)
	select {
	case <-r.release:
		return []RefundObservation{r.observation}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestProductRefundCheckRacingSuccessfulWebhook(t *testing.T) {
	testProductRefundCheckRacingSuccessfulWebhook(t, false)
}

func TestProductRefundAutomaticRacingSuccessfulWebhook(t *testing.T) {
	testProductRefundCheckRacingSuccessfulWebhook(t, true)
}

func testProductRefundCheckRacingSuccessfulWebhook(t *testing.T, automatic bool) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runtime := &blockedRefundReader{entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(runtime.release) }) }
	defer release()
	service, checkout, buyer, product, now := fulfilledRefundFixture(t, pool, runtime)
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "query-webhook-race", "test", "The delivered content does not match its description."); err != nil {
		t.Fatal(err)
	}
	runtime.failNext = true
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err == nil {
		t.Fatal("expected response loss")
	}
	operation := runtime.operations[0]
	var providerPayment string
	if err := pool.QueryRow(ctx, `SELECT provider_payment_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&providerPayment); err != nil {
		t.Fatal(err)
	}
	runtime.observation = RefundObservation{ProviderID: "re_racingquery", ProviderPaymentID: providerPayment, AmountCents: 1900, Currency: "USD", Status: "pending", OperationID: &operation}
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if automatic {
		if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=max_attempts WHERE id=$1`, currentProductRefundJob(t, pool, checkout.PaymentID).ID); err != nil {
			t.Fatal(err)
		}
		if n, err := service.reconcileProductRefunds(ctx, 100, time.Now().Add(16*time.Minute)); err != nil || n != 1 {
			t.Fatalf("automatic race dispatch: %d %v", n, err)
		}
		history, err = service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	} else {
		history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	}
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.id=$1`, history.LatestCheck.ID).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- service.HandleProductRefundCheckJob(ctx, job) }()
	select {
	case <-runtime.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	receipt := receivePaymentWorkflowEvent(t, service, refundOperationEvent(t, "evt_racingquerysuccess", "re_racingquery", "succeeded", checkout.PaymentID, product, now, operation.String()), now)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
	history, err = service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil || history.LatestCheck.UnresolvedCount != 1 || !history.Items[0].ReconciliationRequired {
		t.Fatalf("stale query hid conflicting evidence: %#v %v", history, err)
	}
	var confirmations int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='refund.confirmed'`, checkout.PaymentID).Scan(&confirmations); err != nil || confirmations != 1 {
		t.Fatalf("racing query repeated confirmation: %d %v", confirmations, err)
	}
}
