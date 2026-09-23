package payments

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func bankSummaryMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	body, err := os.ReadFile("../platform/database/migrations/0163_seller_payout_bank_summary." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSellerPayoutBankSummaryLegacyEvidence(t *testing.T) {
	pool, service, runtime, request := sellerBankFixture(t)
	ctx := t.Context()
	bankSummaryMigration(t, pool, "down")
	// Insert authentic old-format evidence under the original 0159 guards.
	_, err := pool.Exec(ctx, `INSERT INTO seller_payout_bank_targets
 (payout_request_id,seller_id,settlement_id,payment_id,provider_identity,destination_id,bank_destination_id,amount_cents,currency,observed_at)
 SELECT r.id,r.seller_id,s.id,s.payment_id,original.identity,d.destination_id,'ba_original',r.amount_cents,r.currency,clock_timestamp()
 FROM seller_payout_requests r JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id
 JOIN product_settlements s ON s.id=a.settlement_id
 JOIN product_checkout_requests original ON original.payment_id=s.payment_id
 JOIN payment_destinations d ON d.user_id=r.seller_id AND d.provider='stripe' WHERE r.id=$1`, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	bankSummaryMigration(t, pool, "up")
	old, err := service.BindSellerPayoutBankTarget(ctx, request.SellerID, request.ID, "ba_original")
	if err != nil || old.BankName != "" || old.Last4 != "" || runtime.reads.Load() != 0 {
		t.Fatalf("legacy binding refetched or fabricated: %+v %v", old, err)
	}
	detail, err := service.GetSellerPayoutRequest(ctx, request.SellerID, request.ID)
	if err != nil || detail.BankTarget == nil || !sameBankSummary(*detail.BankTarget, old) {
		t.Fatalf("legacy projection: %+v %v", detail, err)
	}
	exporter, id, job := sellerExportFixture(t, pool, request.SellerID)
	data, _ := sellerExportData(t, exporter, request.SellerID, id, job)
	rows := data["sellerPayoutBankTargets"]
	if len(rows) != 1 || rows[0]["bankName"] != nil || rows[0]["last4"] != nil {
		t.Fatalf("legacy export was backfilled: %v", rows)
	}
	_, err = pool.Exec(ctx, `UPDATE seller_payout_bank_targets SET bank_name='Current directory',last4='9999'`)
	requirePayoutConstraint(t, err)
	// Null historical evidence can survive a safe down/up cycle unchanged.
	bankSummaryMigration(t, pool, "down")
	bankSummaryMigration(t, pool, "up")
}

func TestSellerPayoutBankSummaryDatabaseAndDowngradeGuards(t *testing.T) {
	pool, service, _, request := sellerBankFixture(t)
	ctx := t.Context()
	first, err := service.BindSellerPayoutBankTarget(ctx, request.SellerID, request.ID, "ba_original")
	if err != nil {
		t.Fatal(err)
	}
	// INSERT triggers/checks must reject old writers and malformed data, before
	// uniqueness is considered. No trigger is disabled for these assertions.
	for _, test := range []struct {
		name       string
		bank, tail any
	}{
		{"missing", nil, nil}, {"half", "Bank", nil}, {"full number", "Bank", "12345678"},
		{"unicode digits", "Bank", "１２３４"}, {"bidi", "Bank\u202Espoof", "1234"},
		{"format supplementary", "Bank\U000E0001", "1234"}, {"control", "Bank\nspoof", "1234"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `INSERT INTO seller_payout_bank_targets
   (payout_request_id,seller_id,settlement_id,payment_id,provider_identity,destination_id,bank_destination_id,amount_cents,currency,observed_at,bank_name,last4)
   SELECT payout_request_id,seller_id,settlement_id,payment_id,provider_identity,destination_id,bank_destination_id,amount_cents,currency,clock_timestamp(),$1,$2
   FROM seller_payout_bank_targets WHERE payout_request_id=$3`, test.bank, test.tail, request.ID)
			requirePayoutConstraint(t, err)
		})
	}
	detail, err := service.GetSellerPayoutRequest(ctx, request.SellerID, request.ID)
	if err != nil || detail.BankTarget == nil || !sameBankSummary(*detail.BankTarget, first) {
		t.Fatalf("seller projection lost summary: %+v %v", detail, err)
	}
	body, err := os.ReadFile("../platform/database/migrations/0163_seller_payout_bank_summary.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, string(body))
	requirePayoutConstraint(t, err)
	_ = tx.Rollback(ctx)
	stored, err := service.GetSellerPayoutBankTarget(ctx, request.SellerID, request.ID)
	if err != nil || stored != first {
		t.Fatalf("downgrade destroyed evidence: %+v %v", stored, err)
	}
}

// SQL timestamps and JSON projections may use different Location objects for
// the same instant. Compare canonical instants without dropping any field.
func sameBankSummary(a, b SellerPayoutBankTarget) bool {
	a.ObservedAt, b.ObservedAt = a.ObservedAt.UTC(), b.ObservedAt.UTC()
	a.CreatedAt, b.CreatedAt = a.CreatedAt.UTC(), b.CreatedAt.UTC()
	return a == b
}
