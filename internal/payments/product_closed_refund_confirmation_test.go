package payments

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

func TestClosedCheckoutPreviouslyRefunded(t *testing.T) {
	for _, scenario := range []string{"cancelled", "payment_failed", "correlated", "unknown", "partial", "pending", "multiple", "with_rights", "prior_failure", "unregistered_saved", "active_dispatch", "webhook_before_query", "authenticated_restart", "unexplained_refund_id"} {
		t.Run(scenario, func(t *testing.T) {
			status := "cancelled"
			if scenario == "payment_failed" {
				status = scenario
			}
			pool, service, checkout, buyer, check := closedCheckoutFixture(t, status)
			ctx := t.Context()
			operation := uuid.New()
			var bound any = "re_historical_confirmed"
			if scenario == "correlated" {
				bound = nil
			}
			// Real immutable local operation identity predates the recovery marker;
			// only its authenticated remote outcome is missing from the projection.
			quarantineExec(t, pool, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,provider_refund_id,status,requested_at) VALUES($1,$2,'stripe','pi_closed_saved',1900,'USD',true,$3,'pending',now()-interval '1 day')`, operation, checkout.PaymentID, bound)
			quarantineExec(t, pool, `UPDATE orders SET refund_operation_id=$2,refund_correlation_enabled=true,refund_requested_at=now()-interval '1 day' WHERE id=$1`, checkout.OrderID, operation)
			var purchased uuid.UUID
			if scenario == "with_rights" {
				purchased = uuid.New()
				quarantineExec(t, pool, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,source_id,origin_asset_id,license_code)
 SELECT $1,p.payer_id,a.kind,a.title,a.media_url,a.mime_type,'clean','purchase',p.order_id,a.id,a.license_code FROM payment_intents p JOIN products pr ON pr.id=p.resource_id JOIN assets a ON a.id=pr.asset_id WHERE p.id=$2`, purchased, checkout.PaymentID)
				quarantineExec(t, pool, `INSERT INTO entitlements(user_id,product_id,order_id,asset_id,license_code) VALUES($1,$2,$3,$4,'hcai-commercial-standard-v1')`, buyer, checkout.ResourceID, checkout.OrderID, purchased)
			}
			if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
				t.Fatal(err)
			}
			observed := RefundObservation{ProviderID: "re_historical_confirmed", ProviderPaymentID: "pi_closed_saved", AmountCents: 1900, Currency: "USD", Status: "succeeded", OperationID: &operation}
			if scenario == "partial" {
				observed.AmountCents = 100
			}
			if scenario == "pending" {
				observed.Status = "pending"
			}
			runtime := &refundReadRuntime{observations: []RefundObservation{observed}}
			if scenario == "prior_failure" {
				prior := uuid.New()
				quarantineExec(t, pool, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,provider_refund_id,status,requested_at) VALUES($1,$2,'stripe','pi_closed_saved',1900,'USD',true,'re_prior_failure','failed',now()-interval '2 days')`, prior, checkout.PaymentID)
				runtime.observations = append(runtime.observations, RefundObservation{ProviderID: "re_prior_failure", ProviderPaymentID: "pi_closed_saved", AmountCents: 1900, Currency: "USD", Status: "failed", OperationID: &prior})
			}
			if scenario == "active_dispatch" {
				quarantineExec(t, pool, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('paymentId',$2::text,'operationId',$3::text),20)`, ProductRefundJobKind, checkout.PaymentID, operation)
			}
			if scenario == "unknown" || scenario == "multiple" {
				other := RefundObservation{ProviderID: "re_historical_other", ProviderPaymentID: "pi_closed_saved", AmountCents: 1900, Currency: "USD", Status: "succeeded"}
				if scenario == "multiple" {
					id := uuid.New()
					other.OperationID = &id
					quarantineExec(t, pool, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,provider_refund_id,status,requested_at) VALUES($1,$2,'stripe','pi_closed_saved',1900,'USD',true,'re_historical_other','pending',now()-interval '1 day')`, id, checkout.PaymentID)
				}
				runtime.observations = append(runtime.observations, other)
			}
			if scenario == "unexplained_refund_id" {
				quarantineExec(t, pool, `UPDATE payment_intents SET provider_refund_id='re_unexplained_prior' WHERE id=$1`, checkout.PaymentID)
			}
			service.runtimes = NewRuntimeCatalog(runtime)
			historyJob := closedCheckoutHistoryJob(t, pool, checkout.PaymentID)
			if scenario == "webhook_before_query" {
				now := time.Now().UTC().Truncate(time.Second)
				service.verifier.now = func() time.Time { return now }
				body := strings.ReplaceAll(string(productRefundEvent("evt_historical_refund", "re_historical_confirmed", "succeeded", checkout.PaymentID, checkout.ResourceID, now.Unix(), 1900)), "pi_workflow123", "pi_closed_saved")
				receipt := receivePaymentWorkflowEvent(t, service, []byte(body), now)
				payload, _ := json.Marshal(map[string]uuid.UUID{"eventId": receipt.EventID})
				if err := service.HandlePaymentEventJob(ctx, jobs.Job{Payload: payload}); err != nil {
					t.Fatal(err)
				}
				assertCheckoutState(t, pool, checkout, status, status, 0)
			}
			if scenario == "unregistered_saved" {
				var id uuid.UUID
				if err := pool.QueryRow(ctx, `SELECT id FROM product_refund_checks WHERE job_id=$1`, historyJob.ID).Scan(&id); err != nil {
					t.Fatal(err)
				}
				request := RefundReadRequest{PaymentID: checkout.PaymentID, ResourceID: checkout.ResourceID, ProviderPaymentID: "pi_closed_saved", AmountCents: 1900, Currency: "USD"}
				if err := service.saveRefundObservations(ctx, id, request, runtime.observations); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "authenticated_restart" {
				requests := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer historical-refund-fixture" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
						t.Error("unexpected financial command or unauthenticated request")
						http.Error(w, "rejected", 400)
						return
					}
					switch r.URL.Path {
					case "/account":
						fmt.Fprint(w, `{"id":"acct_workflow","object":"account"}`)
					case "/balance":
						fmt.Fprint(w, `{"object":"balance","livemode":false}`)
					case "/payment_intents/pi_closed_saved":
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "pi_closed_saved", "amount": 1900, "amount_received": 1900, "currency": "usd", "livemode": false, "status": "succeeded", "metadata": map[string]string{"hcai_payment_id": checkout.PaymentID.String(), "hcai_resource_id": checkout.ResourceID.String(), "hcai_purpose": "product"}})
					case "/refunds":
						if r.URL.Query().Get("payment_intent") != "pi_closed_saved" {
							t.Error("unscoped refund history")
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "has_more": false, "data": []map[string]any{{"id": "re_historical_confirmed", "payment_intent": "pi_closed_saved", "amount": 1900, "currency": "usd", "status": "succeeded", "metadata": map[string]string{"hcai_payment_id": checkout.PaymentID.String(), "hcai_refund_operation_id": operation.String()}}}})
					default:
						t.Error("unexpected provider request")
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(upstream.Close)
				verifiedRuntime := originalMerchantHTTPRuntime(t, upstream, "historical-refund-fixture")
				service.runtimes = NewRuntimeCatalog(verifiedRuntime)
				if _, _, err := verifyProductPaymentIdentity(ctx, pool, verifiedRuntime, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				var checkID uuid.UUID
				if err := pool.QueryRow(ctx, `SELECT id FROM product_refund_checks WHERE job_id=$1`, historyJob.ID).Scan(&checkID); err != nil {
					t.Fatal(err)
				}
				execution := refundCheckExecution{jobID: historyJob.ID, status: "queued"}
				if _, err := service.beginRefundRead(ctx, checkID, checkout.PaymentID, &execution); err != nil {
					t.Fatal(err)
				}
				request := RefundReadRequest{PaymentID: checkout.PaymentID, ResourceID: checkout.ResourceID, ProviderPaymentID: "pi_closed_saved", AmountCents: 1900, Currency: "USD"}
				observations, err := verifiedRuntime.ReadProductRefunds(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				if stored, err := service.saveRefundReadResult(ctx, checkID, request, observations, nil, &execution); err != nil || !stored {
					t.Fatal("authenticated result not saved", stored, err)
				}
				assertCheckoutState(t, pool, checkout, status, status, 0)
				if requests != 4 {
					t.Fatal("wrong number of authenticated reads", requests)
				}
				upstream.Close()
				service = newPaymentTestService(t, pool, service.config, NewRuntimeCatalog())
			}
			if err := service.HandleProductRefundCheckJob(ctx, historyJob); err != nil {
				t.Fatal("known historical refund could not be reconciled", err)
			}
			if len(runtime.operations) != 0 {
				t.Fatal("history confirmation sent a new refund")
			}
			queued := 0
			if scenario == "active_dispatch" {
				queued = 1
			}
			quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'paymentId'=$1`, queued, checkout.PaymentID.String(), ProductRefundJobKind)
			if scenario == "unknown" || scenario == "partial" || scenario == "pending" || scenario == "multiple" || scenario == "unregistered_saved" || scenario == "active_dispatch" || scenario == "unexplained_refund_id" {
				assertCheckoutState(t, pool, checkout, status, status, 0)
				assertOperationalMetric(t, pool, "problem", "closed_checkout_paid", "test", 1, 0, 0)
				quarantineCount(t, pool, `SELECT count(*) FROM product_closed_checkout_refund_confirmations WHERE payment_id=$1`, 0, checkout.PaymentID)
				if scenario != "active_dispatch" && scenario != "unregistered_saved" {
					return
				}
				if scenario == "active_dispatch" {
					oldJob := currentProductRefundJob(t, pool, checkout.PaymentID)
					if err := service.HandleProductRefundJob(ctx, oldJob); err == nil || len(runtime.operations) != 0 {
						t.Fatal("closed historical job sent another refund", err)
					}
					quarantineExec(t, pool, `UPDATE jobs SET status='failed',last_error_code='payment_response_invalid' WHERE id=$1`, oldJob.ID)
				}
				history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
				if err != nil || !history.CanCheck {
					t.Fatal("historical funds cannot be rechecked", history, err)
				}
				if _, err := service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion); err != nil {
					t.Fatal(err)
				}
				historyJob = closedCheckoutHistoryJob(t, pool, checkout.PaymentID)
				if err := service.HandleProductRefundCheckJob(ctx, historyJob); err != nil {
					t.Fatal(err)
				}
			}
			assertCheckoutState(t, pool, checkout, "refunded", "refunded", 0)
			if scenario == "with_rights" {
				quarantineCount(t, pool, `SELECT count(*) FROM entitlements WHERE order_id=$1 AND status='refunded' AND revoked_at IS NOT NULL`, 1, checkout.OrderID)
				if _, err := assets.NewService(pool, paymentTestRoot(t, pool)).Content(ctx, buyer, purchased); err == nil {
					t.Fatal("refunded historical entitlement still permits downloads")
				}
			}
			if scenario == "cancelled" {
				var early jobs.Job
				if err := pool.QueryRow(ctx, `SELECT id,kind,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2 ORDER BY created_at DESC LIMIT 1`, productdelivery.CleanupJobKind, checkout.OrderID.String()).Scan(&early.ID, &early.Kind, &early.Payload); err != nil {
					t.Fatal(err)
				}
				if err := productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, early); err != nil {
					t.Fatal(err)
				}
				quarantineExec(t, pool, `UPDATE jobs SET status='succeeded' WHERE id=$1`, early.ID)
				snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
				if err != nil || snapshot.State != "ready" {
					t.Fatal("cleanup ignored unfinished original payment event", snapshot.State, err)
				}
			}
			var original jobs.Job
			if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM product_closed_checkout_recoveries r JOIN jobs j ON j.payload->>'eventId'=r.event_id::text AND j.kind=$2 WHERE r.payment_id=$1`, checkout.PaymentID, PaymentEventJobKind).Scan(&original.ID, &original.Kind, &original.Payload); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := service.HandlePaymentEventJob(ctx, original); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.HandleProductRefundCheckJob(ctx, historyJob); err != nil {
				t.Fatal(err)
			}
			assertOperationalMetric(t, pool, "problem", "closed_checkout_paid", "test", 0, 0, 0)
			attempts := 1
			if scenario == "prior_failure" {
				attempts = 2
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_attempts WHERE payment_id=$1`, attempts, checkout.PaymentID)
			// Exercise the cleanup handler using the real persisted job and bytes.
			var cleanup jobs.Job
			if err := pool.QueryRow(ctx, `SELECT id,kind,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2 ORDER BY created_at DESC LIMIT 1`, productdelivery.CleanupJobKind, checkout.OrderID.String()).Scan(&cleanup.ID, &cleanup.Kind, &cleanup.Payload); err != nil {
				t.Fatal(err)
			}
			if err := productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, cleanup); err != nil {
				t.Fatal(err)
			}
			snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
			if err != nil || snapshot.State != "removed" {
				t.Fatal("confirmed old refund left the copy retained", snapshot.State, err)
			}
			var event uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT id FROM payment_provider_events WHERE payment_status='succeeded' AND refund_check_id=(SELECT id FROM product_refund_checks WHERE job_id=$1)`, historyJob.ID).Scan(&event); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]uuid.UUID{"eventId": event})
			if err := service.HandlePaymentEventJob(ctx, jobs.Job{Payload: payload}); err != nil {
				t.Fatal(err)
			}
			assertCheckoutState(t, pool, checkout, "refunded", "refunded", 0)
			if scenario == "cancelled" {
				pkg, _ := runProductExport(t, pool, buyer)
				rows := pkg.Data.Marketplace.Data["closedCheckoutRefundConfirmations"]
				if len(rows) != 1 || rows[0]["operationId"] != operation.String() || rows[0]["providerEventId"] != event.String() {
					t.Fatal("export omitted original refund confirmation", rows)
				}
				var seller uuid.UUID
				if err := pool.QueryRow(ctx, `SELECT payee_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&seller); err != nil {
					t.Fatal(err)
				}
				pkg, _ = runProductExport(t, pool, seller)
				if len(pkg.Data.Marketplace.Data["closedCheckoutRefundConfirmations"]) != 0 {
					t.Fatal("seller received buyer confirmation")
				}
				down, err := os.ReadFile("../platform/database/migrations/0133_product_closed_checkout_refund_confirmation.down.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot remove closed checkout refund confirmation evidence") {
					t.Fatal("rollback erased confirmed funds", err)
				}
				_, err = pool.Exec(ctx, `DELETE FROM product_closed_checkout_refund_confirmations WHERE payment_id=$1`, checkout.PaymentID)
				if err == nil {
					t.Fatal("confirmation evidence mutable")
				}
			}
		})
	}
}
