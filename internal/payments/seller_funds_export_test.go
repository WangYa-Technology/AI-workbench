package payments

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/sellerfunds"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func checkFundsExport(t *testing.T, service *Service, seller uuid.UUID, data map[string][]map[string]any) {
	t.Helper()
	funds, err := service.GetSellerFunds(t.Context(), seller)
	if err != nil {
		t.Fatal(err)
	}
	meta := data["sellerFundsReconciliation"]
	if len(meta) != 1 || len(meta[0]) != 3 || meta[0]["version"] != float64(1) || meta[0]["unresolvedRecords"] != float64(funds.UnresolvedRecords) {
		t.Fatal("missing or expanded reconciliation contract", meta)
	}
	cutoff, err := time.Parse(time.RFC3339Nano, meta[0]["asOf"].(string))
	if err != nil || cutoff.After(funds.AsOf) {
		t.Fatal("invalid financial cutoff", cutoff, err)
	}
	if len(data["sellerFundsUnresolvedRecords"]) != funds.UnresolvedRecords || len(data["sellerFundsAccounts"]) != len(funds.Accounts) {
		t.Fatal("account or unresolved directory disagrees with balance")
	}
	accounts := map[string]SellerFundsAccount{}
	for _, account := range funds.Accounts {
		accounts[account.AccountID] = account
	}
	for _, row := range data["sellerFundsAccounts"] {
		if len(row) != 12 {
			t.Fatal("unexpected account fields", row)
		}
		body, _ := json.Marshal(row)
		var actual SellerFundsAccount
		if err := json.Unmarshal(body, &actual); err != nil || actual != accounts[actual.AccountID] || actual.AccountID == "" {
			t.Fatal("API and export balances differ", actual, err)
		}
		for _, key := range []string{"eligibleCreditCents", "bankDebitedCents", "bankReturnedCents"} {
			if _, ok := row[key].(float64); !ok {
				t.Fatal("missing financial explanation", key)
			}
		}
	}
	for _, row := range data["sellerFundsUnresolvedRecords"] {
		if len(row) != 2 || row["id"] == nil || row["kind"] == nil {
			t.Fatal("unexpected unresolved evidence fields", row)
		}
	}
	// Every classified record has a joinable opaque account ID. Unknown records
	// remain present with a null account; no synthetic all-accounts bucket.
	for _, section := range []string{"sellerSettlements", "sellerLedger", "sellerRecoveryObligations", "sellerPayoutRequests"} {
		for _, row := range data[section] {
			id, present := row["accountId"]
			if !present {
				t.Fatal("missing scope", section)
			}
			if id != nil {
				if _, ok := accounts[id.(string)]; !ok {
					t.Fatal("foreign or absent financial account", section, id)
				}
			}
		}
	}
}

func retireFundsExport(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE data_rights_requests SET status='completed',completed_at=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestSellerFundsExportBankLifecycleAndImmutableSnapshot(t *testing.T) {
	pool, service, runtime, _, request, _, job := bankExecutionFixture(t)
	ctx := t.Context()
	exporter, id, exportJob := sellerExportFixture(t, pool, request.SellerID)
	before, _ := sellerExportData(t, exporter, request.SellerID, id, exportJob)
	checkFundsExport(t, service, request.SellerID, before)
	if row := before["sellerFundsAccounts"][0]; row["reservedCents"] != float64(request.AmountCents) || row["bankDebitedCents"] != float64(0) {
		t.Fatal("source transfer implied bank debit", row)
	}
	retireFundsExport(t, pool, id)
	// Read the shared projection within the same isolation level as the export.
	// A concurrent bank transition cannot mix reserved-before with paid-after.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	read := func() string {
		var body []byte
		if err := tx.QueryRow(ctx, `SELECT to_jsonb(a) FROM (`+sellerfunds.AccountsQuery+`) a`, request.SellerID).Scan(&body); err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	original := read()
	if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if read() != original {
		t.Fatal("funds snapshot mixed a concurrent bank result")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	exporter, id, exportJob = sellerExportFixture(t, pool, request.SellerID)
	paid, immutable := sellerExportData(t, exporter, request.SellerID, id, exportJob)
	checkFundsExport(t, service, request.SellerID, paid)
	if row := paid["sellerFundsAccounts"][0]; row["eligibleCreditCents"] != float64(request.AmountCents) || row["bankDebitedCents"] != float64(request.AmountCents) || row["bankReturnedCents"] != float64(0) || row["availableCents"] != float64(0) || row["reservedCents"] != float64(0) {
		t.Fatal("paid statement does not explain consumed funds", row)
	}
	runtime.status = "failed"
	if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	_, replay := sellerExportData(t, exporter, request.SellerID, id, exportJob)
	if string(replay) != string(immutable) {
		t.Fatal("bank return rewrote an already generated export")
	}
	retireFundsExport(t, pool, id)
	exporter, id, exportJob = sellerExportFixture(t, pool, request.SellerID)
	returned, _ := sellerExportData(t, exporter, request.SellerID, id, exportJob)
	checkFundsExport(t, service, request.SellerID, returned)
	if row := returned["sellerFundsAccounts"][0]; row["bankDebitedCents"] != float64(request.AmountCents) || row["bankReturnedCents"] != float64(request.AmountCents) || row["availableCents"] != float64(request.AmountCents) || row["reservedCents"] != float64(request.AmountCents) || row["withdrawableCents"] != float64(0) {
		t.Fatal("return did not restore reserved funds in the statement", row)
	}
	debits, returns := 0, 0
	for _, row := range returned["sellerLedger"] {
		if row["entryType"] == "payout_debit" {
			debits++
		}
		if row["entryType"] == "payout_return" {
			returns++
		}
	}
	if debits != 1 || returns != 1 || runtime.payouts.Load() != 1 {
		t.Fatal("statement lost bank movements or resent money")
	}
}

func TestSellerFundsExportScopesUnknownEvidenceAndPrivacy(t *testing.T) {
	pool, service, _, request := sellerBankFixture(t)
	ctx := t.Context()
	if _, err := service.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID); err != nil {
		t.Fatal(err)
	}
	var firstSettlement, buyer uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT s.id,o.buyer_id FROM seller_payout_request_allocations a
 JOIN product_settlements s ON s.id=a.settlement_id JOIN orders o ON o.id=s.order_id WHERE a.payout_request_id=$1`, request.ID).Scan(&firstSettlement, &buyer); err != nil {
		t.Fatal(err)
	}
	identity, _ := (&productCheckoutRuntime{}).ProductCheckoutIdentity(ctx)
	identity.MerchantID = "acct_other_export_scope"
	identity.LiveMode = true // Local provider, never a real live-mode transaction.
	useScopeRuntime(t, pool, service, request.SellerID, identity)
	second := addEqualSellerSettlement(t, pool, service, request.SellerID)
	if _, err := service.CreateSellerPayoutRequestForSettlement(ctx, request.SellerID, second, request.AmountCents, "separate-export-account"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status) VALUES($1,$2,17,11,'USD','open')`, request.SellerID, firstSettlement); err != nil {
		t.Fatal(err)
	}
	var unknown uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO seller_ledger_entries(seller_id,entry_type,amount_cents,currency,idempotency_key,evidence)
 VALUES($1,'adjustment',999,'USD','private-export-unclassified','{"private":"unclassified-sensitive-evidence"}') RETURNING id`, request.SellerID).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []uuid.UUID{request.SellerID, buyer} {
		exporter, id, job := sellerExportFixture(t, pool, owner)
		data, body := sellerExportData(t, exporter, owner, id, job)
		checkFundsExport(t, service, owner, data)
		if owner != request.SellerID {
			if len(data["sellerFundsAccounts"]) != 0 || len(data["sellerFundsUnresolvedRecords"]) != 0 {
				t.Fatal("buyer gained seller financial evidence")
			}
			continue
		}
		accounts := data["sellerFundsAccounts"]
		if len(accounts) != 2 || len(data["sellerFundsUnresolvedRecords"]) != 1 || data["sellerFundsUnresolvedRecords"][0]["id"] != unknown.String() {
			t.Fatal("scope separation or unclassified evidence lost")
		}
		for _, a := range accounts {
			if a["eligibleCreditCents"] != float64(request.AmountCents) || a["withdrawableCents"] != float64(0) {
				t.Fatal("unclassified adjustment entered account funds", a)
			}
			if a["environment"] == "live" && (a["recoveryDueCents"] != float64(0) || a["reservedCents"] != float64(request.AmountCents)) {
				t.Fatal("debt or reservation crossed accounts", a)
			}
			if a["environment"] == "test" && (a["recoveryDueCents"] != float64(11) || a["reservedCents"] != float64(0)) {
				t.Fatal("released reservation or original debt incorrect", a)
			}
		}
		for _, row := range data["sellerLedger"] {
			if row["id"] == unknown.String() && row["accountId"] != nil {
				t.Fatal("unknown evidence assigned to current merchant")
			}
		}
		for _, private := range []string{identity.MerchantID, identity.Endpoint, "unclassified-sensitive-evidence", "private-export-unclassified", buyer.String()} {
			if strings.Contains(string(body), private) {
				t.Fatalf("statement leaked private value %q", private)
			}
		}
	}
}
