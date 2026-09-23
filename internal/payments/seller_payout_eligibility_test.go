package payments

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sellerCatalogRuntime struct{ sellerBankRuntime }

func (r *sellerCatalogRuntime) CreateCheckout(_ context.Context, in CheckoutRequest) (CheckoutSession, error) {
	id := strings.ReplaceAll(in.PaymentID.String(), "-", "")
	return CheckoutSession{ProviderID: "cs_" + id, CheckoutURL: "https://checkout.stripe.com/c/pay/" + id,
		Status: "open", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

// Purchase the same product as a second buyer through the actual checkout,
// signed event and fulfillment handlers. No settlement or ledger is fabricated.
func addEqualSellerSettlement(t *testing.T, pool *pgxpool.Pool, service *Service, seller uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var product uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM products WHERE seller_id=$1`, seller).Scan(&product); err != nil {
		t.Fatal(err)
	}
	buyer := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Second buyer','member')`,
		buyer, buyer.String()+"@test.local", "b_"+buyer.String()[:8]); err != nil {
		t.Fatal(err)
	}
	checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "second-equal-checkout", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	id := strings.ReplaceAll(checkout.PaymentID.String(), "-", "")
	body := strings.NewReplacer("evt_productpaid", "evt_"+id, "cs_workflow123", "cs_"+id, "pi_workflow123", "pi_"+id).
		Replace(string(productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents)))
	body = strings.ReplaceAll(body, `"livemode":false`, `"livemode":`+strconv.FormatBool(checkout.LiveMode))
	receipt := receivePaymentWorkflowEvent(t, service, []byte(body), now)
	job := jobs.Job{Kind: PaymentEventJobKind}
	if err := pool.QueryRow(ctx, `SELECT id,payload FROM jobs WHERE kind=$1 AND payload->>'eventId'=$2`, job.Kind, receipt.EventID.String()).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandlePaymentEventJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_charge_id=$2 WHERE id=$1`, checkout.PaymentID, "ch_"+id); err != nil {
		t.Fatal(err)
	}
	var settlement uuid.UUID
	job = jobs.Job{Kind: ProductSettlementJobKind}
	if err := pool.QueryRow(ctx, `SELECT s.id,j.id,j.payload FROM product_settlements s
 JOIN product_settlement_dispatches d ON d.settlement_id=s.id JOIN jobs j ON j.id=d.job_id
 WHERE s.payment_id=$1`, checkout.PaymentID).Scan(&settlement, &job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductSettlementJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	return settlement
}

func TestSellerPayoutEqualSettlementsPaginateAndSelect(t *testing.T) {
	pool, service, _, original := sellerBankFixture(t)
	ctx := t.Context()
	if _, err := service.CancelSellerPayoutRequest(ctx, original.SellerID, original.ID); err != nil {
		t.Fatal(err)
	}
	runtime := &sellerCatalogRuntime{}
	service.runtimes = NewRuntimeCatalog(runtime)
	second := addEqualSellerSettlement(t, pool, service, original.SellerID)
	firstPage, err := service.SellerPayoutOptions(ctx, original.SellerID, "", 1)
	if err != nil || len(firstPage.Items) != 1 || firstPage.NextCursor == "" || firstPage.Items[0].SettlementID != second {
		t.Fatalf("first page %+v: %v", firstPage, err)
	}
	secondPage, err := service.SellerPayoutOptions(ctx, original.SellerID, firstPage.NextCursor, 1)
	if err != nil || len(secondPage.Items) != 1 || secondPage.NextCursor != "" {
		t.Fatalf("second page %+v: %v", secondPage, err)
	}
	a, b := firstPage.Items[0], secondPage.Items[0]
	if a.SettlementID == b.SettlementID || a.OrderID == b.OrderID || a.AmountCents != b.AmountCents || a.AmountCents != original.AmountCents {
		t.Fatalf("expected distinct paid orders with equal amounts: %+v %+v", a, b)
	}
	created, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, b.SettlementID, b.AmountCents, "explicit-equal-selection")
	if err != nil {
		t.Fatal(err)
	}
	var selected uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT settlement_id FROM seller_payout_request_allocations WHERE payout_request_id=$1`, created.ID).Scan(&selected); err != nil || selected != b.SettlementID {
		t.Fatalf("wrong allocation %s: %v", selected, err)
	}
	remaining, err := service.SellerPayoutOptions(ctx, original.SellerID, "", 1)
	if err != nil || len(remaining.Items) != 1 || remaining.Items[0].SettlementID != a.SettlementID || remaining.NextCursor != "" {
		t.Fatalf("remaining %+v: %v", remaining, err)
	}
	for _, changed := range []uuid.UUID{a.SettlementID, uuid.New()} {
		if _, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, changed, b.AmountCents, "explicit-equal-selection"); !errors.Is(err, ErrSellerPayoutConflict) {
			t.Fatalf("changed selection must conflict: %v", err)
		}
	}
	if _, err := service.CancelSellerPayoutRequest(ctx, original.SellerID, created.ID); err != nil {
		t.Fatal(err)
	}
	service.config.Enabled = false
	replay, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, b.SettlementID, b.AmountCents, "explicit-equal-selection")
	if err != nil || replay.ID != created.ID || replay.Status != "cancelled" {
		t.Fatalf("cancelled replay %+v: %v", replay, err)
	}
	service.config.Enabled = true
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start, done := make(chan struct{}), make(chan error, 2)
	for _, item := range []SellerPayoutOption{a, b} {
		go func() {
			<-start
			_, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, item.SettlementID, item.AmountCents, "concurrent-equal-selection")
			done <- err
		}()
	}
	close(start)
	succeeded, conflicted := 0, 0
	for range 2 {
		err := <-done
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrSellerPayoutConflict):
			conflicted++
		default:
			t.Fatalf("concurrent equal selection: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("same key created separate reservations: success=%d conflict=%d", succeeded, conflicted)
	}
	balance, err := singleSellerFunds(t, service, ctx, original.SellerID)
	if err != nil || balance.ReservedCents != original.AmountCents {
		t.Fatalf("concurrent reservation balance %+v: %v", balance, err)
	}
	if runtime.transfers.Load() != 0 || runtime.reads.Load() != 0 {
		t.Fatal("local request/selection contacted the bank or dispatched funds")
	}
}

func sellerPayoutEvidenceCounts(t *testing.T, pool *pgxpool.Pool) [5]int {
	t.Helper()
	var counts [5]int
	if err := pool.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM seller_payout_requests), (SELECT count(*) FROM seller_payout_request_allocations),
 (SELECT count(*) FROM seller_ledger_entries), (SELECT count(*) FROM seller_payout_request_events),
 (SELECT count(*) FROM seller_payout_transfers)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4]); err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestSellerPayoutStaleOptionsCannotReserve(t *testing.T) {
	for _, scenario := range []struct{ name, sql string }{
		{"payouts_disabled", `UPDATE payment_destinations SET status='restricted',payouts_enabled=false WHERE user_id=$1`},
		{"charges_disabled", `UPDATE payment_destinations SET status='restricted',charges_enabled=false WHERE user_id=$1`},
		{"merchant_changed", `UPDATE payment_destinations SET original_merchant_id='acct_other' WHERE user_id=$1`},
		{"store_changed", `UPDATE payment_destinations SET original_store_id='other-store' WHERE user_id=$1`},
		{"mode_changed", `UPDATE payment_destinations SET original_live_mode=true WHERE user_id=$1`},
		{"unbound_destination", `UPDATE payment_destinations SET original_merchant_id=NULL,original_live_mode=NULL,original_endpoint=NULL,original_api_version=NULL,original_request_version=NULL WHERE user_id=$1`},
		{"payment_pending_refund", `UPDATE payment_intents SET status='refund_pending' WHERE payee_id=$1`},
		{"order_pending_refund", `UPDATE orders SET status='refund_requested' WHERE id IN (SELECT order_id FROM product_settlements WHERE seller_id=$1)`},
		{"endpoint_changed", `UPDATE payment_destinations SET original_endpoint='https://other.example.test' WHERE user_id=$1`},
		{"checkout_requires_review", `WITH added AS (
 INSERT INTO jobs(kind,payload) SELECT 'payment.locate_product_checkout',jsonb_build_object('paymentId',id::text)
 FROM payment_intents WHERE payee_id=$1 RETURNING id,payload)
 INSERT INTO product_checkout_lookups(job_id,payment_id,requested_by,outcome,searched_after,searched_before,result)
 SELECT id,(payload->>'paymentId')::uuid,$1,'ambiguous',now()-interval '1 hour',now(),'{"outcome":"ambiguous"}' FROM added`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			pool, service, runtime, original := sellerBankFixture(t)
			ctx := t.Context()
			if _, err := service.CancelSellerPayoutRequest(ctx, original.SellerID, original.ID); err != nil {
				t.Fatal(err)
			}
			page, err := service.SellerPayoutOptions(ctx, original.SellerID, "", 20)
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("initial options %+v: %v", page, err)
			}
			before := sellerPayoutEvidenceCounts(t, pool)
			if _, err := pool.Exec(ctx, scenario.sql, original.SellerID); err != nil {
				t.Fatal(err)
			}
			pageAfter, err := service.SellerPayoutOptions(ctx, original.SellerID, "", 20)
			if err != nil || len(pageAfter.Items) != 0 {
				t.Fatalf("ineligible item stayed in directory %+v: %v", pageAfter, err)
			}
			item := page.Items[0]
			if _, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, item.SettlementID, item.AmountCents, "stale-directory-request"); !errors.Is(err, ErrSellerPayoutAllocation) {
				t.Fatalf("stale option accepted or wrong error: %v", err)
			}
			if after := sellerPayoutEvidenceCounts(t, pool); after != before {
				t.Fatalf("failed request left evidence: before=%v after=%v", before, after)
			}
			if runtime.reads.Load() != 0 || runtime.transfers.Load() != 0 {
				t.Fatal("ineligible request contacted provider")
			}
		})
	}
}

func TestSellerPayoutRechecksDestinationAfterLockWait(t *testing.T) {
	pool, service, _, original := sellerBankFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err := service.CancelSellerPayoutRequest(ctx, original.SellerID, original.ID); err != nil {
		t.Fatal(err)
	}
	page, err := service.SellerPayoutOptions(ctx, original.SellerID, "", 20)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("options %+v: %v", page, err)
	}
	before := sellerPayoutEvidenceCounts(t, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `UPDATE payment_destinations SET status='restricted',payouts_enabled=false WHERE user_id=$1`, original.SellerID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, page.Items[0].SettlementID, original.AmountCents, "destination-lock-wait")
		done <- err
	}()
	waitForProductBlockingTx(t, ctx, pool, int32(tx.Conn().PgConn().PID()))
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrSellerPayoutAllocation) {
		t.Fatalf("capability change was not rechecked after wait: %v", err)
	}
	if after := sellerPayoutEvidenceCounts(t, pool); after != before {
		t.Fatalf("rejected concurrent request left evidence: %v -> %v", before, after)
	}
}
