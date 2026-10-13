package payments

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestProductDisputeAdjudication(t *testing.T) {
	for _, outcome := range []string{"lost", "won", "withdrawn", "unknown_result", "refund_pending", "conflict", "sibling", "concurrent", "transferred", "rollback", "late", "withdrawn_then_won", "competing", "refund_inflight", "refund_succeeded"} {
		t.Run(outcome, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			runtime := &durableProductRefundRuntime{}
			service, checkout, buyer, product, now := fulfilledRefundFixture(t, pool, runtime)
			ctx := context.Background()
			var charge string
			if err := pool.QueryRow(ctx, `SELECT COALESCE(provider_charge_id,'') FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&charge); err != nil {
				t.Fatal(err)
			}
			if charge == "" {
				charge = "ch_workflow123"
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_charge_id=$2 WHERE id=$1`, checkout.PaymentID, charge); err != nil {
					t.Fatal(err)
				}
			}
			admit := func(id, dispute, status string, at time.Time) jobs.Job {
				body := []byte(fmt.Sprintf(`{"id":%q,"object":"event","api_version":%q,"created":%d,"livemode":false,"type":"charge.dispute.closed","data":{"object":{"id":%q,"object":"dispute","payment_intent":"pi_workflow123","charge":%q,"amount":1900,"currency":"usd","status":%q,"reason":"fraudulent","network_reason_code":"visa_10_4","evidence_details":{"due_by":%d}}}}`, id, testStripeAPIVersion, at.Unix(), dispute, charge, status, now.Add(time.Hour).Unix()))
				receipt := receivePaymentWorkflowEvent(t, service, body, now)
				return jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}
			}
			run := func(id, dispute, status string, at time.Time) {
				t.Helper()
				if err := service.HandlePaymentEventJob(ctx, admit(id, dispute, status, at)); err != nil {
					t.Fatal(err)
				}
			}
			if outcome == "transferred" {
				if _, err := pool.Exec(ctx, `UPDATE product_settlements SET status='transferred',provider_transfer_id='tr_dispute123',transferred_at=clock_timestamp() WHERE payment_id=$1`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
			}
			if oneOf(outcome, "refund_pending", "refund_inflight", "refund_succeeded") {
				if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "dispute_refund", "test", "The content does not match the licensed description."); err != nil {
					t.Fatal(err)
				}
			}
			confirmRefund := func() {
				t.Helper()
				operation := runtime.operations[0]
				body := refundOperationEvent(t, "evt_dispute_refund_success", "re_"+strings.ReplaceAll(operation.String(), "-", ""), "succeeded", checkout.PaymentID, product, now, operation.String())
				receipt := receivePaymentWorkflowEvent(t, service, body, now)
				if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
					t.Fatal(err)
				}
			}
			if oneOf(outcome, "refund_inflight", "refund_succeeded") {
				if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err != nil {
					t.Fatal(err)
				}
			}
			if outcome == "refund_succeeded" {
				confirmRefund()
			}
			run("evt_open123", "dp_case123", "needs_response", now)

			if outcome == "sibling" {
				run("evt_sibling", "dp_sibling", "under_review", now)
			}
			status := outcome
			switch outcome {
			case "refund_pending", "conflict", "concurrent", "transferred", "rollback", "late", "competing", "refund_inflight", "refund_succeeded":
				status = "lost"
			case "sibling", "withdrawn_then_won":
				status = "won"
			}
			if outcome == "withdrawn_then_won" {
				run("evt_withdrawn", "dp_case123", "withdrawn", now)
			}
			if outcome == "rollback" {
				if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_dispute_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='marketplace.provider_dispute_lost' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER fail_dispute_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_dispute_audit()`); err != nil {
					t.Fatal(err)
				}
				job := admit("evt_failure", "dp_case123", "lost", now.Add(time.Second))
				if err := service.HandlePaymentEventJob(ctx, job); err == nil {
					t.Fatal("injection did not fail")
				}
				var n int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_payment_dispute_adjudications`).Scan(&n); err != nil || n != 0 {
					t.Fatalf("decision not rolled back: %d %v", n, err)
				}
				if _, err := pool.Exec(ctx, `DROP TRIGGER fail_dispute_audit ON audit_events`); err != nil {
					t.Fatal(err)
				}
			}
			if outcome == "concurrent" || outcome == "competing" {
				job := admit("evt_terminal", "dp_case123", status, now.Add(time.Second))
				jobsToRun := []jobs.Job{job}
				if outcome == "competing" {
					jobsToRun = append(jobsToRun, admit("evt_competing", "dp_case123", "won", now.Add(time.Second)))
				}
				var wg sync.WaitGroup
				errs := make(chan error, 4)
				for i := 0; i < 4; i++ {
					job := jobsToRun[i%len(jobsToRun)]
					wg.Add(1)
					go func() {
						defer wg.Done()
						var err error
						for attempt := 0; attempt < 8; attempt++ {
							err = service.HandlePaymentEventJob(ctx, job)
							var pgerr *pgconn.PgError
							if !errors.As(err, &pgerr) || (pgerr.Code != "40001" && pgerr.Code != "40P01") {
								break
							}
						}
						errs <- err
					}()
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					if err != nil {
						t.Fatal(err)
					}
				}
			} else {
				run("evt_terminal", "dp_case123", status, now.Add(time.Second))
			}
			run("evt_replay", "dp_case123", status, now.Add(2*time.Second))
			if outcome == "late" {
				run("evt_late123", "dp_case123", "under_review", now.Add(-time.Second))
			}
			if outcome == "conflict" {
				run("evt_conflict", "dp_case123", "won", now.Add(3*time.Second))
			}
			if outcome == "refund_inflight" {
				confirmRefund()
			}
			var pi, order, entitlement, settlement, action string
			if err := pool.QueryRow(ctx, `SELECT pi.status,o.status,e.status,s.status,d.action_status FROM payment_intents pi JOIN orders o ON o.id=pi.order_id JOIN entitlements e ON e.order_id=o.id JOIN product_settlements s ON s.payment_id=pi.id JOIN product_payment_disputes d ON d.payment_id=pi.id AND d.provider_dispute_id='dp_case123' WHERE pi.id=$1`, checkout.PaymentID).Scan(&pi, &order, &entitlement, &settlement, &action); err != nil {
				t.Fatal(err)
			}
			switch outcome {
			case "lost", "concurrent", "conflict", "rollback", "late":
				if pi != "refunded" || order != "refunded" || entitlement != "refunded" || settlement != "refund_hold" {
					t.Fatalf("loss state: %s %s %s %s", pi, order, entitlement, settlement)
				}
			case "competing":
				if action != "requires_review" || !oneOf(settlement, "dispute_hold", "refund_hold") || !oneOf(pi, "paid", "refunded") {
					t.Fatalf("competing decision escaped hold: %s %s %s", action, settlement, pi)
				}
			case "transferred":
				if pi != "refunded" || settlement != "recovery_required" {
					t.Fatalf("recovery state %s %s", pi, settlement)
				}
				var n int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_recovery_obligations WHERE settlement_id=(SELECT id FROM product_settlements WHERE payment_id=$1) AND remaining_cents=amount_cents AND remaining_cents>0`, checkout.PaymentID).Scan(&n); err != nil || n != 1 {
					t.Fatalf("recovery obligation %d %v", n, err)
				}
			case "refund_inflight", "refund_succeeded":
				if pi != "refunded" || entitlement != "refunded" || action != "requires_review" || settlement != "refund_hold" {
					t.Fatalf("inflight confirmation %s %s %s %s", pi, entitlement, action, settlement)
				}
			case "refund_pending":
				if pi != "refund_pending" || entitlement != "active" || action != "requires_review" {
					t.Fatalf("refund race: %s %s %s", pi, entitlement, action)
				}
			case "won", "withdrawn_then_won":
				if pi != "paid" || entitlement != "active" || settlement != "pending_hold" {
					t.Fatalf("win state: %s %s %s", pi, entitlement, settlement)
				}
			default:
				if pi != "paid" || entitlement != "active" || settlement != "dispute_hold" {
					t.Fatalf("hold state: %s %s %s", pi, entitlement, settlement)
				}
			}
			if outcome == "conflict" && action != "requires_review" {
				t.Fatalf("contradiction: %s", action)
			}
			if outcome == "refund_pending" {
				if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err == nil {
					t.Fatal("refund dispatched through dispute hold")
				}
			}
			expectedRefunds := 0
			if oneOf(outcome, "refund_inflight", "refund_succeeded") {
				expectedRefunds = 1
			}
			if len(runtime.operations) != expectedRefunds {
				t.Fatal("dispute dispatched refund")
			}
			var n int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='marketplace.provider_dispute_lost' AND resource_id=$1`, checkout.OrderID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if (outcome == "lost" || outcome == "concurrent" || outcome == "conflict") && n != 1 {
				t.Fatalf("duplicate audit: %d", n)
			}
			if outcome == "lost" {
				down, err := os.ReadFile("../platform/database/migrations/0178_product_dispute_adjudication.down.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, string(down)); err == nil {
					t.Fatal("downgrade destroyed decision evidence")
				}
			}
			if _, err := pool.Exec(ctx, `UPDATE product_payment_dispute_adjudications SET outcome='won'`); err == nil && (outcome == "lost" || outcome == "concurrent") {
				t.Fatal("mutable decision")
			}
		})
	}
}

func TestProductDisputeAfterBankPayout(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			pool, service, runtime, checkout, request, _, bankJob := bankExecutionFixture(t)
			if unknown {
				runtime.create = func(context.Context, PayoutRequest) (Payout, error) {
					return Payout{}, errors.New("provider response lost")
				}
			}
			err := service.HandleSellerBankPayoutJob(t.Context(), bankJob)
			if (err != nil) != unknown {
				t.Fatalf("bank dispatch: %v", err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			service.verifier.now = func() time.Time { return now }
			body := []byte(fmt.Sprintf(`{"id":"evt_bankdisputeloss","object":"event","api_version":%q,"created":%d,"livemode":false,"type":"charge.dispute.closed","data":{"object":{"id":"dp_bankdispute","object":"dispute","payment_intent":"pi_workflow123","charge":"ch_settlement","amount":1900,"currency":"usd","status":"lost","reason":"fraudulent","network_reason_code":"visa_10_4","evidence_details":{"due_by":%d}}}}`, testStripeAPIVersion, now.Unix(), now.Add(time.Hour).Unix()))
			receipt := receivePaymentWorkflowEvent(t, service, body, now)
			job := jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}
			for i := 0; i < 2; i++ {
				if err := service.HandlePaymentEventJob(t.Context(), job); err != nil {
					t.Fatal(err)
				}
			}
			var status string
			var debt int
			if err := pool.QueryRow(t.Context(), `SELECT s.status,r.remaining_cents FROM product_settlements s JOIN seller_recovery_obligations r ON r.settlement_id=s.id WHERE s.payment_id=$1`, checkout.PaymentID).Scan(&status, &debt); err != nil {
				t.Fatal(err)
			}
			if status != "recovery_required" || debt != request.AmountCents {
				t.Fatalf("post-bank recovery %s %d", status, debt)
			}
		})
	}
}

func TestProductDisputeMigrationRoundTrip(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, direction := range []string{"down", "up"} {
		body, err := os.ReadFile("../platform/database/migrations/0178_product_dispute_adjudication." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), string(body)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProductDisputeRacesRefundAndSettlement(t *testing.T) {
	for _, competitor := range []string{"refund", "settlement"} {
		t.Run(competitor, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			runtime := &productSettlementRuntime{}
			var service *Service
			var checkout Checkout
			var settleJob jobs.Job
			if competitor == "refund" {
				service, checkout, _, _, _ = fulfilledRefundFixture(t, pool, runtime)
				if _, err := pool.Exec(t.Context(), `UPDATE payment_intents SET provider_charge_id='ch_settlement' WHERE id=$1`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
			} else {
				service, checkout, settleJob, _ = productSettlementFixture(t, pool, runtime)
			}
			now := time.Now().UTC().Truncate(time.Second)
			service.verifier.now = func() time.Time { return now }
			body := []byte(fmt.Sprintf(`{"id":"evt_racedispute","object":"event","api_version":%q,"created":%d,"livemode":false,"type":"charge.dispute.closed","data":{"object":{"id":"dp_racedispute","object":"dispute","payment_intent":"pi_workflow123","charge":"ch_settlement","amount":1900,"currency":"usd","status":"lost","reason":"fraudulent","network_reason_code":"visa_10_4","evidence_details":{"due_by":%d}}}}`, testStripeAPIVersion, now.Unix(), now.Add(time.Hour).Unix()))
			receipt := receivePaymentWorkflowEvent(t, service, body, now)
			job := jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}
			var buyer uuid.UUID
			if err := pool.QueryRow(t.Context(), `SELECT payer_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&buyer); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				<-start
				var err error
				for i := 0; i < 8; i++ {
					err = service.HandlePaymentEventJob(t.Context(), job)
					var p *pgconn.PgError
					if !errors.As(err, &p) || (p.Code != "40001" && p.Code != "40P01") {
						break
					}
				}
				results <- err
			}()
			go func() {
				<-start
				var err error
				if competitor == "refund" {
					_, err = service.BeginProductRefund(t.Context(), buyer, checkout.OrderID, "race-refund", "test", "The licensed content does not match its description.")
				} else {
					err = service.HandleProductSettlementJob(t.Context(), settleJob)
				}
				results <- err
			}()
			close(start)
			for i := 0; i < 2; i++ {
				err := <-results
				if err != nil {
					var p *pgconn.PgError
					if !(errors.As(err, &p) && (p.Code == "40001" || p.Code == "40P01")) && !errors.Is(err, ErrRefundConflict) && !errors.Is(err, ErrCheckoutReconciliation) && !jobs.ShouldRetry(err) {
						t.Fatal(err)
					}
				}
			}
			// Recover a serialization loser through the same durable event, never by SQL state repair.
			if err := service.HandlePaymentEventJob(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			var payment, settlement string
			if err := pool.QueryRow(t.Context(), `SELECT pi.status,s.status FROM payment_intents pi JOIN product_settlements s ON s.payment_id=pi.id WHERE pi.id=$1`, checkout.PaymentID).Scan(&payment, &settlement); err != nil {
				t.Fatal(err)
			}
			if !oneOf(settlement, "dispute_hold", "refund_hold", "recovery_required") || !oneOf(payment, "refunded", "refund_pending") {
				t.Fatalf("race escaped protection: %s %s", payment, settlement)
			}
			if settlement == "recovery_required" {
				var n int
				if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_recovery_obligations WHERE settlement_id=(SELECT id FROM product_settlements WHERE payment_id=$1)`, checkout.PaymentID).Scan(&n); err != nil || n != 1 {
					t.Fatalf("missing race recovery %d %v", n, err)
				}
			}
		})
	}
}

func TestProductDisputeHistoricalReplay(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	service, checkout, _, _ := productSettlementFixture(t, pool, &productSettlementRuntime{})
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	body := []byte(fmt.Sprintf(`{"id":"evt_legacydispute","object":"event","api_version":%q,"created":%d,"livemode":false,"type":"charge.dispute.closed","data":{"object":{"id":"dp_legacydispute","object":"dispute","payment_intent":"pi_workflow123","charge":"ch_settlement","amount":1900,"currency":"usd","status":"lost","reason":"fraudulent","network_reason_code":"visa_10_4","evidence_details":{"due_by":%d}}}}`, testStripeAPIVersion, now.Unix(), now.Add(time.Hour).Unix()))
	receipt := receivePaymentWorkflowEvent(t, service, body, now)
	// Represent the pre-0178 worker's root/event projections without financial effects.
	if _, err := pool.Exec(t.Context(), `INSERT INTO product_payment_disputes(provider,live_mode,provider_dispute_id,payment_id,order_id,seller_id,settlement_id,provider_payment_id,provider_charge_id,amount_cents,currency,provider_status,action_status,due_by,latest_event_at,latest_event_id)
 SELECT 'stripe',false,e.object_id,pi.id,pi.order_id,pi.payee_id,s.id,e.provider_payment_id,e.provider_charge_id,e.amount_cents,e.currency,'lost','lost',e.dispute_due_by,e.occurred_at,e.id FROM payment_provider_events e JOIN payment_intents pi ON pi.id=e.payment_id JOIN product_settlements s ON s.payment_id=pi.id WHERE e.id=$1`, receipt.EventID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO product_payment_dispute_events(dispute_id,provider_event_id,event_type,provider_status,reason,network_reason_code,due_by,occurred_at,applied) SELECT d.id,e.id,e.event_type,e.dispute_status,e.dispute_reason,e.dispute_network_reason_code,e.dispute_due_by,e.occurred_at,true FROM product_payment_disputes d JOIN payment_provider_events e ON e.id=d.latest_event_id WHERE e.id=$1`, receipt.EventID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE payment_provider_event_processing SET status='processed',processed_at=now() WHERE event_id=$1`, receipt.EventID); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"down", "up"} {
		sql, err := os.ReadFile("../platform/database/migrations/0178_product_dispute_adjudication." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	job := jobs.Job{Kind: PaymentEventJobKind}
	if err := pool.QueryRow(t.Context(), `SELECT payload FROM jobs WHERE payload->>'disputeAdjudicationBackfill'='true'`).Scan(&job.Payload); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := service.HandlePaymentEventJob(t.Context(), job); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='dispute.loss.applied'`, checkout.PaymentID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("legacy effect count %d %v", n, err)
	}
}
