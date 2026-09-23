package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func quarantineExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func quarantineCount(t *testing.T, pool *pgxpool.Pool, sql string, want int, args ...any) {
	t.Helper()
	var got int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&got); err != nil || got != want {
		t.Fatalf("count=%d want=%d err=%v query=%s", got, want, err, sql)
	}
}

func signedQuarantineReceipt(s *Service, body []byte, now time.Time) (Receipt, error) {
	return s.ReceiveStripeWebhook(context.Background(), body, fmt.Sprintf("t=%d,v1=%s", now.Unix(), stripeSignature(testStripeWebhookSecret, now.Unix(), body)))
}

func TestProductWebhookQuarantineReceiptBoundary(t *testing.T) {
	s, pool, checkout, _, product := pendingStripeWebhookFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents+1)
	if _, err := s.ReceiveStripeWebhook(t.Context(), body, "invalid"); !errors.Is(err, ErrInvalidSignature) {
		t.Fatal(err)
	}
	for _, malformed := range [][]byte{[]byte(`{"object":"event"}`), []byte(strings.Replace(string(body), testStripeAPIVersion, "unknown-version", 1))} {
		if _, err := signedQuarantineReceipt(s, malformed, now); err == nil {
			t.Fatal("malformed signed envelope accepted")
		}
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines`, 0)
	for range 2 {
		if _, err := signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrInvalidEvent) {
			t.Fatal(err)
		}
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines WHERE state='pending' AND candidate_payment_id=$1`, 1, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events`, 0)
	quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1`, 0, PaymentEventJobKind)
	quarantineCount(t, pool, `SELECT count(*) FROM entitlements`, 0)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_review WHERE payment_id=$1`, 1, checkout.PaymentID)
	// A different signed body must not overwrite or collapse into the first.
	if _, err := signedQuarantineReceipt(s, append(append([]byte{}, body...), ' '), now); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines`, 2)
	for _, sql := range []string{`UPDATE product_webhook_quarantines SET event='{}'`, `DELETE FROM product_webhook_quarantines`, `UPDATE product_webhook_quarantines SET state='admitted'`, `UPDATE product_webhook_quarantines SET version=version+1`} {
		if _, err := pool.Exec(t.Context(), sql); err == nil {
			t.Fatalf("mutable rejected evidence: %s", sql)
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0118_product_webhook_quarantine.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), string(down)); err == nil || !strings.Contains(err.Error(), "rollback refused") {
		t.Fatal(err)
	}
}

func TestProductWebhookQuarantineOnlyHoldsOriginalRemoteIdentity(t *testing.T) {
	for _, scenario := range []string{"foreign_remote", "foreign_environment", "amount_conflict", "unknown_payment"} {
		t.Run(scenario, func(t *testing.T) {
			s, pool, checkout, buyer, product := pendingStripeWebhookFixture(t)
			now := time.Now().UTC().Truncate(time.Second)
			body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents+1)
			switch scenario {
			case "foreign_remote":
				body = []byte(strings.NewReplacer("cs_workflow123", "cs_foreign", "pi_workflow123", "pi_foreign").Replace(string(body)))
			case "foreign_environment":
				s.config.LiveMode = true
				body = []byte(strings.Replace(string(body), `"livemode":false`, `"livemode":true`, 1))
			case "unknown_payment":
				body = []byte(strings.Replace(string(body), checkout.PaymentID.String(), uuid.NewString(), 1))
			}
			if _, err := signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrInvalidEvent) {
				t.Fatal(err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines`, 1)
			want := 0
			if scenario == "amount_conflict" {
				want = 1
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantine_review WHERE payment_id=$1`, want, checkout.PaymentID)
			if want == 1 {
				if _, _, err := s.BeginProductCheckout(t.Context(), buyer, product, "retry-after-quarantine", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product)); !errors.Is(err, ErrCheckoutReconciliation) {
					t.Fatalf("checkout ignored retained financial conflict: %v", err)
				}
			}
		})
	}
}

func TestProductWebhookQuarantineRecheckAndAdmission(t *testing.T) {
	s, pool, checkout, buyer, product := pendingStripeWebhookFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	// Preserve the actual checkout identity for restoration. No signed fields,
	// contract or receipt are edited to make the recheck pass.
	var original string
	if err := pool.QueryRow(t.Context(), `SELECT provider_checkout_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE payment_intents SET provider_checkout_id='cs_missing_original' WHERE id=$1`, checkout.PaymentID)
	body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents)
	if _, err := signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	if _, err := s.ListWebhookQuarantines(t.Context(), buyer, WebhookQuarantineFilter{}); !errors.Is(err, ErrQuarantineForbidden) {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE users SET role='admin' WHERE id=$1`, buyer)
	page, err := s.ListWebhookQuarantines(t.Context(), buyer, WebhookQuarantineFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	q := page.Items[0]
	input := RecheckWebhookInput{ExpectedVersion: q.Version, Reason: strings.Repeat("核", 10)}
	still, err := s.RecheckWebhookQuarantine(t.Context(), buyer, q.ID, input)
	if err != nil || still.State != "pending" || still.Version != q.Version+1 {
		t.Fatalf("pending check=%+v err=%v", still, err)
	}
	if _, err := s.RecheckWebhookQuarantine(t.Context(), buyer, q.ID, input); !errors.Is(err, ErrQuarantineConflict) {
		t.Fatalf("stale version: %v", err)
	}
	quarantineExec(t, pool, `UPDATE payment_intents SET provider_checkout_id=$2 WHERE id=$1`, checkout.PaymentID, original)
	matched, err := s.ListWebhookQuarantines(t.Context(), buyer, WebhookQuarantineFilter{})
	if err != nil || !matched.Items[0].HasReviewHold || matched.Items[0].CandidatePaymentID != nil {
		t.Fatalf("current match was confused with initial candidate: %+v %v", matched, err)
	}
	input.ExpectedVersion = still.Version
	admitted, err := s.RecheckWebhookQuarantine(t.Context(), buyer, q.ID, input)
	if err != nil || admitted.State != "admitted" || admitted.AdmittedEventID == nil || admitted.HasReviewHold {
		t.Fatalf("admission=%+v err=%v", admitted, err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM entitlements`, 0)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events`, 1)
	quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1`, 1, PaymentEventJobKind)
	quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantine_checks WHERE quarantine_id=$1`, 2, q.ID)
	quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.webhook_rechecked' AND resource_id=$1`, 2, q.ID)
	if _, err := signedQuarantineReceipt(s, body, now); err != nil {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1`, 1, PaymentEventJobKind)
	processStripeReceipt(t, s, pool, Receipt{EventID: *admitted.AdmittedEventID})
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
	for _, sql := range []string{`DELETE FROM product_webhook_quarantine_checks`, `UPDATE product_webhook_quarantine_checks SET reason='modified operator explanation'`, `UPDATE product_webhook_quarantines SET checked_at=now(),version=version+1`} {
		if _, err := pool.Exec(t.Context(), sql); err == nil {
			t.Fatal("admitted evidence/history was mutable")
		}
	}
}

func TestProductWebhookQuarantinePersistenceFailureIsRetryable(t *testing.T) {
	s, pool, checkout, _, product := pendingStripeWebhookFixture(t)
	quarantineExec(t, pool, `CREATE FUNCTION reject_quarantine_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture persistence unavailable'; END $$;
CREATE TRIGGER fail_quarantine_fixture BEFORE INSERT ON product_webhook_quarantines FOR EACH ROW EXECUTE FUNCTION reject_quarantine_fixture()`)
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := signedQuarantineReceipt(s, productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents+1), now); err == nil || errors.Is(err, ErrInvalidEvent) || errors.Is(err, ErrEventConflict) {
		t.Fatalf("lost receipt returned definitive rejection: %v", err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events`, 0)
	quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines`, 0)
}

func TestProductWebhookQuarantineWaffoReservationRace(t *testing.T) {
	f := newWaffoRefundFixture(t, "pending")
	ctx := t.Context()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	version, err := reserveWaffoRefundTx(ctx, tx, f.checkout.PaymentID, f.operation)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Signed contradictory evidence arrives after reservation commit and before
	// reacquiring the dispatch lock. The pending operation itself is unchanged.
	event := waffoProductWebhookBody(t, f, "order.completed", uuid.Nil)
	event["data"].(map[string]any)["amount"] = "19.01"
	body, _ := json.Marshal(event)
	if _, err := f.service.ReceiveWaffoWebhook(ctx, body, "fixture-signature"); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	next, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Rollback(ctx)
	if err = lockWaffoRefundReservationTx(ctx, next, f.checkout.PaymentID, f.operation, version); err == nil {
		t.Fatal("reservation bypassed a new signed financial conflict")
	}
	if f.calls.Load() != 0 {
		t.Fatal("funds dispatched despite hold")
	}
}

func TestProductWebhookQuarantineUnknownWaffoRecheck(t *testing.T) {
	f := newWaffoRefundFixture(t, "await_payment")
	event := waffoProductWebhookBody(t, f, "order.completed", uuid.Nil)
	event["data"].(map[string]any)["orderMetadata"].(map[string]any)["hcaiPaymentId"] = uuid.NewString()
	body, _ := json.Marshal(event)
	if _, err := f.service.ReceiveWaffoWebhook(t.Context(), body, "fixture-signature"); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	quarantineExec(t, f.pool, `UPDATE users SET role='admin' WHERE id=$1`, f.buyer)
	page, err := f.service.ListWebhookQuarantines(t.Context(), f.buyer, WebhookQuarantineFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("%+v %v", page, err)
	}
	q, err := f.service.RecheckWebhookQuarantine(t.Context(), f.buyer, page.Items[0].ID, RecheckWebhookInput{ExpectedVersion: 1, Reason: "Original provider record is not available yet."})
	if err != nil || q.State != "pending" || q.LastErrorCode == nil || *q.LastErrorCode != "payment_unknown" {
		t.Fatalf("unknown original must remain reviewable: %+v %v", q, err)
	}
	quarantineCount(t, f.pool, `SELECT count(*) FROM payment_provider_events`, 0)
}

func TestProductWebhookQuarantineConcurrentRechecks(t *testing.T) {
	s, pool, checkout, buyer, product := pendingStripeWebhookFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := signedQuarantineReceipt(s, productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents+1), now); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE users SET role='admin' WHERE id=$1`, buyer)
	page, err := s.ListWebhookQuarantines(t.Context(), buyer, WebhookQuarantineFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("%+v %v", page, err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	for range 8 {
		go func() {
			<-start
			_, err := s.RecheckWebhookQuarantine(t.Context(), buyer, page.Items[0].ID, RecheckWebhookInput{ExpectedVersion: 1, Reason: "The original provider transaction is still inconsistent."})
			results <- err
		}()
	}
	close(start)
	winners := 0
	for range 8 {
		err := <-results
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrQuarantineConflict) {
			t.Errorf("concurrent recheck returned internal failure: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("recheck winners=%d", winners)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantine_checks`, 1)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events`, 0)
	quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines WHERE state='pending' AND version=2`, 1)
}

func TestProductWebhookQuarantineStopsRefundDispatch(t *testing.T) {
	for _, stage := range []string{"before_request", "after_request"} {
		t.Run(stage, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			runtime := &durableProductRefundRuntime{}
			s, checkout, buyer, product, now := fulfilledRefundFixture(t, pool, runtime)
			if stage == "after_request" {
				if _, err := s.BeginProductRefund(t.Context(), buyer, checkout.OrderID, "before-conflict", "test", "The delivered product did not meet the agreement."); err != nil {
					t.Fatal(err)
				}
			}
			body := []byte(strings.Replace(string(productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents+1)), "evt_productpaid", "evt_conflicting_receipt", 1))
			if _, err := signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrInvalidEvent) {
				t.Fatal(err)
			}
			if stage == "before_request" {
				if _, err := s.BeginProductRefund(t.Context(), buyer, checkout.OrderID, "after-conflict", "test", "The delivered product did not meet the agreement."); !errors.Is(err, ErrRefundConflict) {
					t.Fatalf("request bypassed hold: %v", err)
				}
			} else if err := s.HandleProductRefundJob(t.Context(), currentProductRefundJob(t, pool, checkout.PaymentID)); err == nil {
				t.Fatal("dispatch bypassed hold")
			}
			runtime.mu.Lock()
			calls := len(runtime.operations)
			runtime.mu.Unlock()
			if calls != 0 {
				t.Fatal("provider received a refund despite financial conflict")
			}
			quarantineCount(t, pool, `SELECT count(*) FROM entitlements WHERE order_id=$1 AND status='active'`, 1, checkout.OrderID)
		})
	}
}

func TestProductWebhookQuarantineStopsCompensationDispatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	buyer, _, source, product := newProductCheckoutFixture(t, pool)
	runtime := &productCheckoutRuntime{}
	s := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	checkout, _, err := s.BeginProductCheckout(t.Context(), buyer, product, "quarantined-compensation", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, source)
	now := time.Now().UTC().Truncate(time.Second)
	s.verifier.now = func() time.Time { return now }
	body := []byte(strings.Replace(string(productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents+1)), "evt_productpaid", "evt_compensation_conflict", 1))
	if _, err := signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("conflicting signed payment: %v", err)
	}
	receipt := receivePaymentWorkflowEvent(t, s, productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents), now)
	if err := s.HandlePaymentEventJob(t.Context(), jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleProductRefundJob(t.Context(), currentProductRefundJob(t, pool, checkout.PaymentID)); err == nil || !strings.Contains(err.Error(), "payment_reconciliation_required") {
		t.Fatalf("compensation bypassed financial review: %v", err)
	}
	if runtime.refundCalls != 0 {
		t.Fatal("provider received compensation despite financial conflict")
	}
	quarantineCount(t, pool, `SELECT count(*) FROM payment_intents WHERE id=$1 AND status='refund_pending' AND compensation_reason='source_unavailable'`, 1, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines WHERE state='pending'`, 1)
	quarantineCount(t, pool, `SELECT count(*) FROM entitlements WHERE order_id=$1`, 0, checkout.OrderID)
}

func TestProductWebhookQuarantineProtectsRefundedCopy(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	runtime := &durableProductRefundRuntime{}
	s, checkout, buyer, product, now := fulfilledRefundFixture(t, pool, runtime)
	if _, err := s.BeginProductRefund(t.Context(), buyer, checkout.OrderID, "refund-before-late-conflict", "test", "The delivered product did not meet the agreement."); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleProductRefundJob(t.Context(), currentProductRefundJob(t, pool, checkout.PaymentID)); err != nil {
		t.Fatal(err)
	}
	var remote string
	if err := pool.QueryRow(t.Context(), `SELECT provider_refund_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&remote); err != nil {
		t.Fatal(err)
	}
	refunded := receivePaymentWorkflowEvent(t, s, productRefundEvent("evt_refund_before_conflict", remote, "succeeded", checkout.PaymentID, product, now.Unix(), checkout.AmountCents), now)
	processStripeReceipt(t, s, pool, refunded)
	assertCheckoutState(t, pool, checkout, "refunded", "refunded", 0)
	snapshot, err := productdelivery.Load(t.Context(), pool, checkout.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	var cleanupJob jobs.Job
	if err = pool.QueryRow(t.Context(), `SELECT id,kind,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2`, productdelivery.CleanupJobKind, checkout.OrderID.String()).Scan(&cleanupJob.ID, &cleanupJob.Kind, &cleanupJob.Payload); err != nil {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_delivery_cleanup_policy WHERE order_id=$1 AND NOT needed AND NOT held`, 1, checkout.OrderID)
	body := []byte(strings.Replace(string(productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents+1)), "evt_productpaid", "evt_late_financial_conflict", 1))
	if _, err = signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	if err = productdelivery.CleanupHandler(pool, s.config.MediaStores)(t.Context(), cleanupJob); err != nil {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_delivery_snapshots WHERE order_id=$1 AND state='ready'`, 1, checkout.OrderID)
	quarantineExec(t, pool, `UPDATE users SET status='deleted' WHERE id=$1`, buyer)
	var deletionJob jobs.Job
	if err = pool.QueryRow(t.Context(), `INSERT INTO jobs(kind,payload) VALUES($1,jsonb_build_object('userId',$2::text)) RETURNING id,kind,payload`, datarights.MediaCleanupJobKind, buyer).Scan(&deletionJob.ID, &deletionJob.Kind, &deletionJob.Payload); err != nil {
		t.Fatal(err)
	}
	if err = datarights.NewService(pool, paymentTestRoot(t, pool)).HandleMediaCleanupJob(t.Context(), deletionJob); err != nil {
		t.Fatal(err)
	}
	store, err := s.config.MediaStores.Get(snapshot.Backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stat(t.Context(), snapshot.Key); err != nil {
		t.Fatalf("account cleanup deleted financial evidence: %v", err)
	}
}

func TestProductWebhookQuarantineBuyerExportScope(t *testing.T) {
	s, pool, checkout, buyer, product := pendingStripeWebhookFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents+1)
	if _, err := signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	foreign := []byte(strings.NewReplacer("evt_productpaid", "evt_foreign_claim", "cs_workflow123", "cs_foreign", "pi_workflow123", "pi_foreign").Replace(string(body)))
	if _, err := signedQuarantineReceipt(s, foreign, now); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE users SET role='admin' WHERE id=$1`, buyer)
	page, err := s.ListWebhookQuarantines(t.Context(), buyer, WebhookQuarantineFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range page.Items {
		if _, err = s.RecheckWebhookQuarantine(t.Context(), buyer, q.ID, RecheckWebhookInput{ExpectedVersion: 1, Reason: "Private operator note excluded from buyer export."}); err != nil {
			t.Fatal(err)
		}
	}
	pkg, raw := runProductExport(t, pool, buyer)
	if len(pkg.Data.Marketplace.Data["rejectedProviderEvents"]) != 1 || len(pkg.Data.Marketplace.Data["rejectedProviderEventChecks"]) != 1 {
		t.Fatal("missing or overbroad buyer evidence")
	}
	for _, secret := range []string{"evt_foreign_claim", "Private operator note excluded", "cs_foreign", "pi_foreign"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("unassociated receipt or private review note disclosed", secret)
		}
	}
	other, _, _, _ := newProductCheckoutFixture(t, pool)
	otherExport, _ := runProductExport(t, pool, other)
	if len(otherExport.Data.Marketplace.Data["rejectedProviderEvents"]) != 0 || len(otherExport.Data.Marketplace.Data["rejectedProviderEventChecks"]) != 0 {
		t.Fatal("cross-buyer rejected evidence leak")
	}
}

func TestProductWebhookQuarantineMigrationConsistency(t *testing.T) {
	s, pool, checkout, _, product := pendingStripeWebhookFixture(t)
	apply := func(name, direction string) {
		t.Helper()
		body, err := os.ReadFile("../platform/database/migrations/" + name + "." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		quarantineExec(t, pool, string(body))
	}
	// Unwind later policies through their guarded migrations before exercising
	// the older receipt migration. Never hide dependencies with DROP CASCADE.
	dependents := []string{
		"0134_product_closed_refund_reconciliation",
		"0133_product_closed_checkout_refund_confirmation",
		"0132_product_closed_checkout_recovery",
		"0131_product_checkout_evidence_conflicts",
		"0130_product_checkout_session_evidence",
		"0129_product_checkout_lookup_check_sharing",
		"0128_product_refund_read_executions",
		"0127_product_refund_read_receipts",
		"0126_product_funds_media_retention",
		"0125_product_refund_observation_review",
		"0120_waffo_checkout_dispatch",
	}
	for _, name := range dependents {
		apply(name, "down")
	}
	apply("0118_product_webhook_quarantine", "down")
	apply("0118_product_webhook_quarantine", "up")
	for i := len(dependents) - 1; i >= 0; i-- {
		apply(dependents[i], "up")
	}
	now := time.Now().UTC().Truncate(time.Second)
	body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents+1)
	if _, err := signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	for _, expression := range []string{`jsonb_set(event,'{ProviderEventID}','"evt_changed"')`, `jsonb_set(event,'{LiveMode}','true')`, `jsonb_set(event,'{Supported}','false')`, `event-'PaymentID'`, `jsonb_set(event,'{PayloadSHA256}','"different"')`} {
		_, err := pool.Exec(t.Context(), `INSERT INTO product_webhook_quarantines(provider,provider_event_id,payload_sha256,live_mode,claimed_payment_id,event,rejection_code)
 SELECT provider,provider_event_id,payload_sha256,live_mode,claimed_payment_id,`+expression+`,rejection_code FROM product_webhook_quarantines LIMIT 1`)
		if err == nil || !strings.Contains(err.Error(), "consistent normalized signed receipt") {
			t.Fatalf("invalid receipt was not rejected by identity guard: %v", err)
		}
	}
}

func TestProductWebhookQuarantineRejectsInconsistentHistoricalEvent(t *testing.T) {
	s, pool, checkout, buyer, product := pendingStripeWebhookFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents)
	event, err := minimizeStripeEvent(body, testStripeAPIVersion, false)
	if err != nil {
		t.Fatal(err)
	}
	oldID := uuid.New()
	// Reproduce a historically inconsistent minimized receipt carrying the same
	// original digest. A matching digest must not validate the wrong amount.
	quarantineExec(t, pool, `INSERT INTO payment_provider_events(id,provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,
 object_id,object_type,payment_id,resource_id,purpose,amount_cents,currency,payment_status,provider_payment_id)
 VALUES($1,'stripe',$2,$3,$4,false,$5,$6,$7,$8,$9,$10,'product',$11,'USD','paid','pi_workflow123')`, oldID, event.ProviderEventID, event.EventType, event.APIVersion, event.OccurredAt, event.PayloadSHA256, event.ObjectID, event.ObjectType, checkout.PaymentID, product, checkout.AmountCents+1)
	quarantineExec(t, pool, `INSERT INTO payment_provider_event_processing(event_id,status) VALUES($1,'received')`, oldID)
	if _, err = signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("historical contradictory fields accepted: %v", err)
	}
	quarantineExec(t, pool, `UPDATE users SET role='admin' WHERE id=$1`, buyer)
	page, err := s.ListWebhookQuarantines(t.Context(), buyer, WebhookQuarantineFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	q, err := s.RecheckWebhookQuarantine(t.Context(), buyer, page.Items[0].ID, RecheckWebhookInput{ExpectedVersion: 1, Reason: "The stored transaction fields remain inconsistent."})
	if err != nil || q.State != "pending" || q.LastErrorCode == nil || *q.LastErrorCode != "event_identity_conflict" {
		t.Fatal(q, err)
	}
	if _, err = pool.Exec(t.Context(), `UPDATE product_webhook_quarantines SET state='admitted',admitted_event_id=$2,version=version+1,checked_at=now() WHERE id=$1`, q.ID, oldID); err == nil {
		t.Fatal("database blessed an inconsistent historical event")
	}
	quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1`, 0, PaymentEventJobKind)
	quarantineCount(t, pool, `SELECT count(*) FROM entitlements`, 0)
}

func TestProductWebhookQuarantineIngressAndRecheckRace(t *testing.T) {
	s, pool, checkout, buyer, product := pendingStripeWebhookFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents)
	quarantineExec(t, pool, `UPDATE payment_intents SET provider_checkout_id='cs_identity_unavailable' WHERE id=$1`, checkout.PaymentID)
	if _, err := signedQuarantineReceipt(s, body, now); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE users SET role='admin' WHERE id=$1`, buyer)
	page, err := s.ListWebhookQuarantines(t.Context(), buyer, WebhookQuarantineFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	quarantineExec(t, pool, `UPDATE payment_intents SET provider_checkout_id='cs_workflow123' WHERE id=$1`, checkout.PaymentID)
	start := make(chan struct{})
	results := make(chan error, 8)
	for i := range 8 {
		go func() {
			<-start
			var err error
			if i%2 == 0 {
				_, err = signedQuarantineReceipt(s, body, now)
			} else {
				_, err = s.RecheckWebhookQuarantine(t.Context(), buyer, page.Items[0].ID, RecheckWebhookInput{ExpectedVersion: 1, Reason: "Original checkout identity has been recovered."})
			}
			results <- err
		}()
	}
	close(start)
	for range 8 {
		err := <-results
		var pgErr *pgconn.PgError
		if err != nil && !errors.Is(err, ErrQuarantineConflict) && !errors.Is(err, ErrWebhookAdmissionBusy) && !(errors.As(err, &pgErr) && pgErr.Code == "40001") {
			t.Errorf("unexpected ingress/recheck failure: %v", err)
		}
	}
	// Provider retries a serialization response with the original signed body.
	receipt, err := signedQuarantineReceipt(s, body, now)
	if err != nil {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events`, 1)
	quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1`, 1, PaymentEventJobKind)
	quarantineCount(t, pool, `SELECT count(*) FROM product_webhook_quarantines WHERE state='admitted'`, 1)
	processStripeReceipt(t, s, pool, receipt)
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
	pkg, _ := runProductExport(t, pool, buyer)
	if len(pkg.Data.Marketplace.Data["rejectedProviderEvents"]) != 1 {
		t.Fatal("admitted event did not establish buyer export ownership")
	}
}

func TestProductWebhookQuarantineRejectsUntypedWaffoIdentity(t *testing.T) {
	f := newWaffoRefundFixture(t, "await_payment")
	for _, field := range []string{"merchantProvidedBuyerIdentity", "orderMerchantExternalId", "storeId"} {
		for _, value := range []string{"private-email@example.test", strings.Repeat("private-unbounded", 4096)} {
			event := waffoProductWebhookBody(t, f, "order.completed", uuid.Nil)
			if field == "storeId" {
				event[field] = value
			} else {
				event["data"].(map[string]any)[field] = value
			}
			body, _ := json.Marshal(event)
			if _, err := f.service.ReceiveWaffoWebhook(t.Context(), body, "fixture-signature"); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("untyped merchant identity was not rejected: %v", err)
			}
		}
	}
	quarantineCount(t, f.pool, `SELECT count(*) FROM product_webhook_quarantines`, 0)
	quarantineCount(t, f.pool, `SELECT count(*) FROM payment_provider_events`, 0)
}
