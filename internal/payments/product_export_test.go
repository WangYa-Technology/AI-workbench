package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgxpool"
)

type marketExportPackage struct {
	Data struct {
		Orders []struct {
			ID     uuid.UUID `json:"id"`
			Status string    `json:"status"`
		} `json:"orders"`
		Marketplace struct {
			SchemaVersion int                         `json:"schemaVersion"`
			Data          map[string][]map[string]any `json:"data"`
		} `json:"marketplace"`
	} `json:"data"`
}

func requestProductExport(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) (*datarights.Service, datarights.Request, jobs.Job) {
	t.Helper()
	ctx := context.Background()
	token := uuid.NewString()
	var handle string
	if err := pool.QueryRow(ctx, `SELECT handle FROM users WHERE id=$1`, user).Scan(&handle); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1,$2,now()+interval '1 hour')`, user, identity.HashToken(token)); err != nil {
		t.Fatal(err)
	}
	service := datarights.NewService(pool, paymentTestRoot(t, pool))
	request, err := service.Create(ctx, user, token, datarights.CreateInput{RequestType: "data_export", IdentityConfirmation: handle}, "market-export-test")
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err = pool.QueryRow(ctx, `SELECT id,kind,payload FROM jobs WHERE kind=$1 AND payload->>'requestId'=$2`, datarights.ExportJobKind, request.ID.String()).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return service, request, job
}

func readProductExport(t *testing.T, service *datarights.Service, user, request uuid.UUID) (marketExportPackage, []byte) {
	t.Helper()
	body, checksum, err := service.Download(context.Background(), user, request)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if checksum != hex.EncodeToString(sum[:]) {
		t.Fatal("export checksum mismatch")
	}
	var pkg marketExportPackage
	if err = json.Unmarshal(body, &pkg); err != nil {
		t.Fatal(err)
	}
	// Keep the public export contract explicit. Checking only a historical
	// count rejects additive seller records but can still miss a renamed or
	// accidentally exposed private section with the same total cardinality.
	sections := []string{
		"checkoutCheckDispatches", "checkoutClosures", "checkoutCommands", "checkoutDispatches",
		"checkoutLookups", "checkoutRequests", "cleanupJobs", "closedCheckoutRecoveries",
		"closedCheckoutRefundConfirmations", "contracts", "deliveryRepairs", "deliverySnapshots",
		"entitlements", "identityRecoveries", "listingHistory", "orderEvents", "paymentEvents",
		"payments", "products", "providerEvents", "refundAttempts", "refundChecks", "refundReadReceipts",
		"rejectedProviderEventChecks", "rejectedProviderEvents", "sales",
		"sellerFundsReconciliation", "sellerFundsAccounts", "sellerFundsUnresolvedRecords",
		"sellerSettlements", "sellerLedger", "sellerRecoveryObligations", "sellerPayoutRequests",
		"sellerPayoutAllocations", "sellerPayoutBankTargets", "sellerPayoutReviews",
		"sellerPayoutFundingAdmissions", "sellerPayoutEvents", "sellerPayoutSourceTransfers",
		"sellerBankPayoutCommands", "sellerBankPayoutResults", "sellerBankPayoutReads", "sellerBankPayoutResumes",
		"sellerSourceReversalCommands", "sellerSourceReversalReads", "sellerSourceReversalResults", "sellerSourceReversalClosures",
	}
	if pkg.Data.Marketplace.SchemaVersion != 1 || len(pkg.Data.Marketplace.Data) != len(sections) {
		t.Fatalf("unexpected export schema or sections: %+v", pkg.Data.Marketplace)
	}
	for _, name := range sections {
		if rows, ok := pkg.Data.Marketplace.Data[name]; !ok || rows == nil {
			t.Fatalf("missing or null export array: %s", name)
		}
	}
	return pkg, body
}

func runProductExport(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) (marketExportPackage, []byte) {
	t.Helper()
	service, request, job := requestProductExport(t, pool, user)
	if err := service.HandleExportJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	pkg, body := readProductExport(t, service, user, request.ID)
	// Replay does not regenerate an already published snapshot or extend expiry.
	if err := service.HandleExportJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	_, again := readProductExport(t, service, user, request.ID)
	if string(body) != string(again) {
		t.Fatal("export changed on replay")
	}
	if _, _, err := service.Download(context.Background(), uuid.New(), request.ID); !errors.Is(err, datarights.ErrNotReady) {
		t.Fatalf("cross-user download: %v", err)
	}
	return pkg, body
}

func refundProductForExport(t *testing.T, pool *pgxpool.Pool, service *Service, runtime *refundReadRuntime, checkout Checkout, buyer uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "export-refund-command", "test", "Private buyer refund explanation for export."); err != nil {
		t.Fatal(err)
	}
	runtime.failNext = true
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err == nil {
		t.Fatal("expected lost provider reply")
	}
	var providerPayment string
	if err := pool.QueryRow(ctx, `SELECT provider_payment_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&providerPayment); err != nil {
		t.Fatal(err)
	}
	operation := runtime.operations[0]
	runtime.observations = []RefundObservation{{ProviderID: "re_exportconfirmed", ProviderPaymentID: providerPayment, AmountCents: 1900, Currency: "USD", Status: "succeeded", OperationID: &operation}}
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err = pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN product_refund_checks c ON c.job_id=j.id WHERE c.id=$1`, history.LatestCheck.ID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err = service.HandleProductRefundCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
}

func TestProductExportBuyerSellerIsolationAndFrozenEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &refundReadRuntime{}
	service, checkout, buyer, product, _ := fulfilledRefundFixture(t, pool, runtime)
	var seller uuid.UUID
	var originalTerms string
	if err := pool.QueryRow(ctx, `SELECT p.seller_id,c.contract->'license'->>'terms' FROM products p JOIN orders o ON o.product_id=p.id JOIN product_order_contracts c ON c.order_id=o.id WHERE o.id=$1`, checkout.OrderID).Scan(&seller, &originalTerms); err != nil {
		t.Fatal(err)
	}
	snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	stranger, _, _, _ := newProductCheckoutFixture(t, pool)
	// A separate buyer of the same product must not appear in this buyer's export.
	foreignOrder := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,license_version,license_terms_snapshot,refund_reason,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot)
 VALUES($1,$2,$3,1900,'USD','cancelled','foreign-order-command','1','foreign terms','foreign-private-refund-note','Historical order','Original license',7)`, foreignOrder, stranger, product); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,to_status,evidence) VALUES($1,'export.privacy_probe','paid','{"secret":"private-provider-evidence"}')`, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	refundProductForExport(t, pool, service, runtime, checkout, buyer)
	if _, err = pool.Exec(ctx, `UPDATE licenses SET terms='new terms must not replace old acceptance' WHERE code='hcai-commercial-standard-v1'`); err != nil {
		t.Fatal(err)
	}
	pkg, body := runProductExport(t, pool, buyer)
	data := pkg.Data.Marketplace.Data
	if len(pkg.Data.Orders) != 1 || pkg.Data.Orders[0].Status != "refunded" || len(data["entitlements"]) != 1 || data["entitlements"][0]["status"] != "refunded" {
		t.Fatalf("wrong purchase history: %+v", pkg)
	}
	for _, name := range []string{"contracts", "deliverySnapshots", "payments", "refundAttempts", "refundChecks", "checkoutRequests", "checkoutDispatches", "checkoutCommands", "cleanupJobs"} {
		if len(data[name]) != 1 {
			t.Fatalf("%s has %d rows", name, len(data[name]))
		}
	}
	if len(data["orderEvents"]) < 3 || len(data["providerEvents"]) < 2 || len(data["paymentEvents"]) < 3 {
		t.Fatal("events omitted")
	}
	if data["contracts"][0]["license"].(map[string]any)["terms"] != originalTerms || data["deliverySnapshots"][0]["sha256"] != snapshot.SHA256 {
		t.Fatal("frozen contract/delivery changed")
	}
	if data["refundAttempts"][0]["status"] != "succeeded" || data["refundChecks"][0]["status"] != "completed" {
		t.Fatal("refund history omitted")
	}
	if len(data["refundChecks"][0]["observations"].([]any)) != 1 {
		t.Fatal("refund observation missing")
	}
	if len(data["products"]) != 0 || len(data["sales"]) != 0 {
		t.Fatal("buyer received seller data")
	}
	for _, forbidden := range []string{foreignOrder.String(), "foreign-private-refund-note", "private-provider-evidence", snapshot.Key, snapshot.SourceKey, "storageKey", "storage_key", "https://example.test/success", "checkout_url", "durable-refund-checkout", "export-refund-command", seller.String() + "@test.local"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("buyer export leaked %q", forbidden)
		}
	}
	sellerPkg, sellerBody := runProductExport(t, pool, seller)
	sellerData := sellerPkg.Data.Marketplace.Data
	if len(sellerData["products"]) != 1 || len(sellerData["sales"]) != 1 || len(sellerData["payments"]) != 0 || len(sellerData["contracts"]) != 0 {
		t.Fatalf("seller scope wrong: %+v", sellerData)
	}
	for _, forbidden := range []string{buyer.String(), buyer.String() + "@test.local", checkout.PaymentID.String(), "Private buyer refund explanation", "foreign-private-refund-note", snapshot.Key, snapshot.SourceKey} {
		if strings.Contains(string(sellerBody), forbidden) {
			t.Fatalf("seller export leaked %q", forbidden)
		}
	}
	other, _ := runProductExport(t, pool, stranger)
	for _, name := range []string{"payments", "contracts", "entitlements", "refundAttempts", "providerEvents", "cleanupJobs"} {
		if len(other.Data.Marketplace.Data[name]) != 0 {
			t.Fatalf("unrelated buyer sees %s", name)
		}
	}
	// Contract seller attribution must survive reassignment of the current listing.
	if _, err = pool.Exec(ctx, `UPDATE products SET seller_id=$2 WHERE id=$1`, product, stranger); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_rights_requests SET status='completed' WHERE user_id=$1 AND request_type='data_export'`, stranger); err != nil {
		t.Fatal(err)
	}
	reassigned, _ := runProductExport(t, pool, stranger)
	for _, sale := range reassigned.Data.Marketplace.Data["sales"] {
		if sale["orderId"] == checkout.OrderID.String() {
			t.Fatal("new seller inherited historical private sale")
		}
	}
}

func TestProductExportClosureAndCleanupRecovery(t *testing.T) {
	f, snapshot, failed, actor := newFailedProductCleanup(t)
	ctx := context.Background()
	rights := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
	replacement, err := rights.RetryMediaCleanup(ctx, actor, failed.ID, productCleanupRetryInput(), "export-cleanup")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"orderId": f.order})
	if err = productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, jobs.Job{ID: replacement.ID, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	pkg, body := runProductExport(t, f.pool, f.buyer)
	data := pkg.Data.Marketplace.Data
	if len(data["checkoutClosures"]) != 1 || len(data["checkoutDispatches"]) != 0 || len(data["cleanupJobs"]) != 2 || data["deliverySnapshots"][0]["state"] != "removed" {
		t.Fatalf("closure/recovery evidence missing: %+v", data)
	}
	if data["cleanupJobs"][0]["replacementJobId"] != replacement.ID.String() || data["cleanupJobs"][1]["retryOf"] != failed.ID.String() {
		t.Fatal("recovery lineage omitted")
	}
	for _, forbidden := range []string{snapshot.Key, snapshot.SourceKey, "private/storage/path", productCleanupRetryInput().Reason, actor.String()} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("cleanup export leaked %q", forbidden)
		}
	}
}

func TestProductExportConcurrentRefundUsesOneSnapshot(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	runtime := &refundReadRuntime{}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
	rights, request, job := requestProductExport(t, pool, buyer)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	const lock int64 = 918730142
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lock); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION pause_market_export_test() RETURNS trigger AS $$ BEGIN
 IF NEW.status='processing' AND NEW.request_type='data_export' THEN PERFORM pg_advisory_xact_lock(%d); END IF; RETURN NEW; END; $$ LANGUAGE plpgsql;
 CREATE TRIGGER pause_market_export_test BEFORE UPDATE ON data_rights_requests FOR EACH ROW EXECUTE FUNCTION pause_market_export_test()`, lock)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- rights.HandleExportJob(ctx, job) }()
	// Observe the real DB lock wait: the worker has established its MVCC snapshot.
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted)`, lock).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err = <-done:
			t.Fatalf("export unexpectedly ended: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	refundProductForExport(t, pool, service, runtime, checkout, buyer)
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	pkg, _ := readProductExport(t, rights, buyer, request.ID)
	data := pkg.Data.Marketplace.Data
	if pkg.Data.Orders[0].Status != "fulfilled" || data["payments"][0]["status"] != "paid" || data["entitlements"][0]["status"] != "active" || len(data["refundAttempts"]) != 0 {
		t.Fatal("mixed pre/post-refund state in one export")
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER pause_market_export_test ON data_rights_requests`); err != nil {
		t.Fatal(err)
	}
	// The next snapshot includes the committed refund across every related table.
	if _, err = pool.Exec(ctx, `UPDATE data_rights_requests SET status='completed' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := runProductExport(t, pool, buyer)
	if after.Data.Orders[0].Status != "refunded" || after.Data.Marketplace.Data["entitlements"][0]["status"] != "refunded" {
		t.Fatal("new snapshot omitted completed refund")
	}
}

func TestProductExportLargeRecordIsCompleteAndFailureIsAtomic(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	_, seller, _, product := newProductCheckoutFixture(t, pool)
	// A single record spans parts, including multibyte UTF-8. Splitting storage
	// must never truncate the JSON value or expose an incomplete ready artifact.
	description := strings.Repeat("完整交付📦", 400000)
	if _, err := pool.Exec(ctx, `UPDATE products SET description=$2 WHERE id=$1`, product, description); err != nil {
		t.Fatal(err)
	}
	service, request, job := requestProductExport(t, pool, seller)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_export_part_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.part_number=2 THEN RAISE EXCEPTION 'injected part write failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_export_part_test BEFORE INSERT ON data_rights_export_parts FOR EACH ROW EXECUTE FUNCTION fail_export_part_test()`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleExportJob(ctx, job); err == nil {
		t.Fatal("expected part write failure")
	}
	var state string
	var artifacts, parts, notices int
	if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM data_rights_export_artifacts WHERE request_id=$1),
 (SELECT count(*) FROM data_rights_export_parts WHERE request_id=$1),
 (SELECT count(*) FROM notifications WHERE source_key=$2) FROM data_rights_requests WHERE id=$1`, request.ID, "data-export-ready:"+request.ID.String()).Scan(&state, &artifacts, &parts, &notices); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || artifacts != 0 || parts != 0 || notices != 0 {
		t.Fatalf("partial export committed: %s %d %d %d", state, artifacts, parts, notices)
	}
	if _, _, err := service.Download(ctx, seller, request.ID); !errors.Is(err, datarights.ErrNotReady) {
		t.Fatalf("partial download: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_export_part_test ON data_rights_export_parts`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleExportJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	pkg, body := readProductExport(t, service, seller, request.ID)
	if len(body) <= 5<<20 || pkg.Data.Marketplace.Data["products"][0]["description"] != description {
		t.Fatal("large product record was truncated")
	}
	var partCount int
	var sumSize int64
	if err := pool.QueryRow(ctx, `SELECT count(*),sum(size_bytes) FROM data_rights_export_parts WHERE request_id=$1`, request.ID).Scan(&partCount, &sumSize); err != nil || partCount < 2 || sumSize != int64(len(body)) {
		t.Fatalf("invalid parts: %d %d %v", partCount, sumSize, err)
	}
	if err := service.HandleExportJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	_, replayed := readProductExport(t, service, seller, request.ID)
	if string(body) != string(replayed) {
		t.Fatal("job replay changed accepted export bytes")
	}
}

func TestProductExportLocatedCheckoutOmitsPrivateLookupPayload(t *testing.T) {
	pool, service, runtime, checkout, buyer, job := pendingCheckoutLookupFixture(t)
	if err := service.HandleProductCheckoutLookupJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	pkg, body := runProductExport(t, pool, buyer)
	data := pkg.Data.Marketplace.Data
	if len(data["checkoutLookups"]) != 1 || data["checkoutLookups"][0]["outcome"] != "found" || data["checkoutLookups"][0]["paymentId"] != checkout.PaymentID.String() {
		t.Fatal("lookup proof missing")
	}
	if strings.Contains(string(body), runtime.observation.CheckoutURL) || strings.Contains(string(body), "https://example.test") {
		t.Fatal("export revealed checkout capability or return URL")
	}
}
