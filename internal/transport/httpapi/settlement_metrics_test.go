package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/observability"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestMetricsSettlementObligationsAlerts(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	for _, kind := range []string{
		observability.LegalHoldExpiry, observability.LegalHoldCleanup, observability.ProductCleanupReconciliation,
		observability.AccountDeletionReconciliation, observability.OriginalMediaCleanupReconciliation,
		observability.ProductRefundReconciliation, observability.ProductCheckoutReconciliation, observability.ProductSettlementReconciliation,
		observability.SellerFundingReconciliation, observability.SellerBankReconciliation,
		observability.GenerationOutputCleanup, observability.GenerationExecutionRecovery, observability.AssetScanExecutionRecovery, observability.UploadWriteCleanup,
	} {
		if err := observability.NewRepository(pool).RecordMaintenance(t.Context(), kind, true); err != nil {
			t.Fatal(err)
		}
	}
	execute := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	buyer, seller, asset, product, order, payment, settlement, job := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	execute(`INSERT INTO users(id,email,handle,display_name,role) VALUES
 ($1,'metricsbuyer@test.local','metricsbuyer','Buyer','member'),($2,'metricsseller@test.local','metricsseller','Seller','creator')`, buyer, seller)
	execute(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'image','Private source','/private.jpg','image/jpeg','clean','upload','hcai-commercial-standard-v1','local_file','private-settlement-source.jpg')`, asset, seller)
	execute(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
 VALUES($1,$2,$3,'Private product','Settlement metrics fixture','workflow',1900,'USD','hcai-commercial-standard-v1','active','AI-assisted','[]','HCAI')`, product, seller, asset)
	execute(`INSERT INTO orders(id,buyer_id,product_id,status,amount_cents,currency,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot)
 SELECT $1,$2,$3,'fulfilled',1900,'USD','metrics-order','Private product',name,version,terms,refund_window_days
 FROM licenses WHERE code='hcai-commercial-standard-v1'`, order, buyer, product)
	execute(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','paid',true,'metrics-payment')`, payment, buyer, seller, product, order)
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173"}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	check := func(want string, success bool) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "bash", "../../../scripts/metrics-alert-check.sh")
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "ALERT_") && !strings.HasPrefix(value, "METRICS_URL=") {
				command.Env = append(command.Env, value)
			}
		}
		command.Env = append(command.Env, "METRICS_URL="+server.URL+"/metrics")
		output, err := command.CombinedOutput()
		if (err == nil) != success || !strings.Contains(string(output), want) {
			t.Fatalf("check err=%v output=%s want=%s", err, output, want)
		}
	}
	// Fresh scanner heartbeats cannot hide a missing financial snapshot.
	check("ALERT payment_problems_exceeded", false)
	execute(`INSERT INTO product_settlements(id,order_id,payment_id,seller_id,provider,live_mode,gross_amount_cents,fee_bps,fee_cents,net_amount_cents,
 currency,status,available_at,transfer_idempotency_key,created_at)
 VALUES($1,$2,$3,$4,'stripe',true,1900,0,0,1900,'USD','pending_hold',now()-interval '2 hours','metrics-transfer',now()-interval '3 hours')`, settlement, order, payment, seller)
	check("ALERT live_settlement_due_age_exceeded", false)
	// The transfer execution guard models the production first-dispatch contract:
	// the bound job must be executable, carry the exact settlement payload, and
	// remain queued/running until the dispatch evidence has been recorded.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `SELECT set_config('app.payment_transfer_execution_protocol','job-v1',true)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(t.Context(), `INSERT INTO jobs(id,kind,payload,status)
 VALUES($1,'payment.settle_product',jsonb_build_object('settlementId',$2::text),'queued') RETURNING id`, job, settlement).Scan(&job); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO product_settlement_dispatches(settlement_id,job_id,reserved_at)
 VALUES($1,$2,now()-interval '1 hour')`, settlement, job); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE jobs SET status='succeeded' WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	check("ALERT live_settlement_unresolved_age_exceeded", false)
	// Recovered transfer proof alone does not resolve a recorded refund debt.
	execute(`UPDATE product_settlements SET status='recovery_required',provider_transfer_id='tr_private_metrics',transferred_at=now(),
 recovery_amount_cents=net_amount_cents WHERE id=$1`, settlement)
	check("ALERT live_settlement_unresolved_age_exceeded", false)
	response, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode, err)
	}
	for _, private := range []string{buyer.String(), seller.String(), order.String(), payment.String(), settlement.String(), "tr_private_metrics", "metricsbuyer@test.local", "private-settlement-source.jpg"} {
		if strings.Contains(string(body), private) {
			t.Fatalf("settlement metric leaked %q", private)
		}
	}
	// Model a resolved obligation for the projection; this is not a recovery API.
	execute(`UPDATE product_settlements SET status='transferred',recovery_amount_cents=0 WHERE id=$1`, settlement)
	check("ok ", true)
	// A missing migration must make the endpoint fail, never emit healthy zeros.
	execute(`ALTER TABLE product_settlement_checks RENAME TO unavailable_settlement_checks`)
	check("ALERT metrics endpoint unavailable", false)
}
