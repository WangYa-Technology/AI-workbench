package payments

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func applyRefundReadReceiptMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	if direction == "down" {
		applyRefundReadExecutionMigration(t, pool, "down")
	}
	t.Helper()
	body, err := os.ReadFile("../platform/database/migrations/0127_product_refund_read_receipts." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
	if direction == "up" {
		applyRefundReadExecutionMigration(t, pool, "up")
	}
}

func TestProductRefundReadReceiptHistory(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	applyRefundReadReceiptMigration(t, pool, "down")
	applyRefundReadReceiptMigration(t, pool, "up")
	runtime := &refundReadRuntime{}
	service, checkout, _, _, _ := fulfilledRefundFixture(t, pool, runtime)
	h, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	h, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, h.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	checkID := h.LatestCheck.ID
	runAutomaticRefundCheck(t, service)
	var payment string
	if err = pool.QueryRow(ctx, `SELECT provider_payment_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&payment); err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for _, id := range []uuid.UUID{uuid.New(), uuid.New(), uuid.New()} {
		observations, _ := json.Marshal([]RefundObservation{{ProviderID: "re_receipt_history", ProviderPaymentID: payment, AmountCents: 100, Currency: "USD", Status: "succeeded"}})
		quarantineExec(t, pool, `INSERT INTO product_refund_read_receipts(id,check_id,attempt_number,complete,observations,evidence_sha256,created_at) VALUES($1::uuid,$2,0,true,$3,encode(public.digest($1::uuid::text,'sha256'),'hex'),'2026-01-01')`, id, checkID, observations)
		ids = append(ids, id)
	}
	page, err := service.ListRefundReadReceipts(ctx, checkout.PaymentID, checkID, "")
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	seen := map[uuid.UUID]bool{}
	for {
		if len(page.Items) != 1 || seen[page.Items[0].ID] {
			t.Fatal("duplicate or missing receipt", page)
		}
		seen[page.Items[0].ID] = true
		if page.NextCursor == nil {
			break
		}
		page, err = service.ListRefundReadReceipts(ctx, checkout.PaymentID, checkID, *page.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != len(ids) {
		t.Fatal("receipt pagination incomplete", seen)
	}
	if _, err = service.ListRefundReadReceipts(ctx, uuid.New(), checkID, ""); !errors.Is(err, ErrRefundHistoryNotFound) {
		t.Fatal("foreign payment exposed receipts", err)
	}
	if _, err = service.ListRefundReadReceipts(ctx, checkout.PaymentID, checkID, uuid.NewString()); !errors.Is(err, ErrInvalidRefund) {
		t.Fatal("foreign cursor accepted", err)
	}
	if _, err = service.ListRefundReadReceipts(ctx, checkout.PaymentID, checkID, "bad"); !errors.Is(err, ErrInvalidRefund) {
		t.Fatal(err)
	}
}

func TestProductRefundReadReceiptResolvesWithBoundEvidence(t *testing.T) {
	pool, service, runtime, checkout, _ := automaticRefundFixture(t, true)
	ctx := t.Context()
	verified := runtime.observations
	runtime.observations = nil
	h, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	h, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, h.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	checkID := h.LatestCheck.ID
	job := runAutomaticRefundCheck(t, service)
	var request RefundReadRequest
	if err = pool.QueryRow(ctx, `SELECT id,resource_id,provider_payment_id,amount_cents,currency,live_mode FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&request.PaymentID, &request.ResourceID, &request.ProviderPaymentID, &request.AmountCents, &request.Currency, &request.LiveMode); err != nil {
		t.Fatal(err)
	}
	late := append([]RefundObservation{}, verified...)
	late[0].OperationID = nil
	if _, err = service.saveRefundReadResult(ctx, checkID, request, late, nil, &refundCheckExecution{jobID: job.ID, attempts: job.Attempts, leaseToken: &job.LeaseToken, status: "running"}); err != nil {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_observation_review WHERE payment_id=$1`, 1, checkout.PaymentID)
	runtime.observations = verified
	h, err = service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, h.PaymentVersion); err != nil {
		t.Fatal(err)
	}
	runAutomaticRefundCheck(t, service)
	assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_review WHERE payment_id=$1`, 0, checkout.PaymentID)
	page, err := service.ListRefundReadReceipts(ctx, checkout.PaymentID, checkID, "")
	if err != nil || len(page.Items) != 1 || len(page.Items[0].UnresolvedProviderRefundIDs) != 0 || page.Items[0].Observations[0].OperationID != nil {
		t.Fatal("bound recovery rewrote or failed to resolve late receipt", page, err)
	}
	if len(runtime.operations) != 1 {
		t.Fatal("receipt resolution sent another refund", runtime.operations)
	}
}

func applyRefundReadExecutionMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	// Older refund migrations must first remove the newer dependent checkout
	// views in deployment order. Each down migration keeps its evidence guards.
	migrations := []string{
		"0128_product_refund_read_executions",
		"0129_product_checkout_lookup_check_sharing",
		"0130_product_checkout_session_evidence",
		"0131_product_checkout_evidence_conflicts",
		"0132_product_closed_checkout_recovery",
		"0133_product_closed_checkout_refund_confirmation",
		"0134_product_closed_refund_reconciliation",
	}
	for index := range migrations {
		if direction == "down" {
			index = len(migrations) - 1 - index
		}
		body, err := os.ReadFile("../platform/database/migrations/" + migrations[index] + "." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(t.Context(), string(body)); err != nil {
			t.Fatalf("%s %s: %v", migrations[index], direction, err)
		}
	}
}
