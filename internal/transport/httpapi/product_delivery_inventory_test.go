package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestProductDeliveryEvidenceInventory(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := t.Context()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	guest, operator, ordinary, second := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	admin := registerGovernanceUser(t, operator, server.URL, "evidence_operator")
	buyer := registerGovernanceUser(t, ordinary, server.URL, "evidence_buyer")
	other := registerGovernanceUser(t, second, server.URL, "evidence_second")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE users SET role='admin' WHERE id=ANY($1)`, []uuid.UUID{admin.ID, other.ID})
	source, product := uuid.New(), uuid.New()
	exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','Private original','/private','text/plain','clean','upload','hcai-commercial-standard-v1','local_file','private-evidence-key')`, source, admin.ID)
	exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Current mutable title','Private product description','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, admin.ID, source)
	seed := func(contract, required, snapshot, rights, pending bool, environment string) uuid.UUID {
		t.Helper()
		fixtureProduct := uuid.New()
		exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 SELECT $1,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status FROM products WHERE id=$2`, fixtureProduct, product)
		order := uuid.New()
		exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot,delivery_snapshot_required,created_at)
 VALUES($1,$2,$3,1900,'USD','payment_pending',now(),$1::uuid::text,'1.0','Private accepted terms','Original accepted title','License',7,$4,'2020-01-01T00:00:00Z')`, order, buyer.ID, fixtureProduct, required)
		if contract {
			exec(`INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract)
 SELECT $1,source_asset_id,root_asset_id,offer_version,contract FROM product_offers WHERE product_id=$2`, order, fixtureProduct)
		}
		if snapshot {
			exec(`INSERT INTO product_delivery_snapshots(order_id,source_backend,source_key,storage_backend,storage_key,sha256,size_bytes,mime_type,state,ready_at)
 VALUES($1,'local_file','private-evidence-key','local_file',$1::uuid::text,repeat('a',64),5,'text/plain','ready',now())`, order)
		}
		if environment != "unknown" {
			status := "paid"
			if pending {
				status = "checkout_pending"
			}
			exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD',$6,$7,$1::uuid::text)`, uuid.New(), buyer.ID, admin.ID, fixtureProduct, order, status, environment == "live")
		}
		if rights {
			asset := uuid.New()
			exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,source_id,license_code,origin_asset_id)
 VALUES($1,$2,'document','Purchased','/private','text/plain','clean','purchase',$3,'hcai-commercial-standard-v1',$4)`, asset, buyer.ID, order, source)
			exec(`INSERT INTO entitlements(user_id,product_id,order_id,asset_id,license_code,status) VALUES($1,$2,$3,$4,'hcai-commercial-standard-v1','active')`, buyer.ID, fixtureProduct, order, asset)
		}
		return order
	}
	expected := map[uuid.UUID]string{
		seed(false, false, false, true, false, "unknown"): "contract_missing",
		seed(true, false, false, false, false, "live"):    "legacy_unfrozen",
		seed(true, true, false, false, true, "test"):      "required_snapshot_missing",
		seed(true, false, true, false, false, "test"):     "legacy_snapshot_unbound",
	}
	seed(true, true, true, false, false, "live") // Normal snapshot must not appear.
	base := server.URL + "/api/v1/admin/product-deliveries/evidence-gaps"
	if r := requestJSON(t, guest, "GET", base, nil, nil); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	if r := requestJSON(t, ordinary, "GET", base, nil, nil); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	var cursor string
	var firstCursor string
	seen := map[uuid.UUID]bool{}
	for pageIndex := 0; pageIndex < 3; pageIndex++ {
		var page productdelivery.EvidenceGapPage
		r := requestJSON(t, operator, "GET", base+"?limit=2&cursor="+url.QueryEscape(cursor), nil, &page)
		if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal(r.StatusCode)
		}
		for _, item := range page.Items {
			if seen[item.OrderID] || expected[item.OrderID] != item.Gap || item.Title != "Original accepted title" {
				t.Fatal(item)
			}
			seen[item.OrderID] = true
		}
		raw, _ := json.Marshal(page)
		for _, secret := range []string{"private-evidence-key", "Private accepted terms", "Current mutable title", buyer.ID.String(), "storageKey", "buyerId"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("private data leaked", secret)
			}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		if firstCursor == "" {
			firstCursor = cursor
		}
	}
	if len(seen) != 4 || firstCursor == "" {
		t.Fatal("incomplete or unstable page", seen)
	}
	for _, query := range []string{"?gap=nope", "?limit=0", "?limit=51", "?scope=all", "?environment=local", "?cursor=nope", "?gap=&gap=", "?buyerId=" + buyer.ID.String(), "?scope=active&cursor=" + firstCursor, "?environment=live&cursor=" + firstCursor, "?gap=legacy_unfrozen&cursor=" + firstCursor} {
		if r := requestJSON(t, operator, "GET", base+query, nil, nil); r.StatusCode != 422 {
			t.Fatal(query, r.StatusCode)
		}
	}
	if r := requestJSON(t, second, "GET", base+"?cursor="+firstCursor, nil, nil); r.StatusCode != 422 {
		t.Fatal("cross-actor cursor", r.StatusCode)
	}
	for _, selection := range []struct {
		query string
		count int
	}{{"?scope=active", 2}, {"?environment=unknown", 1}, {"?gap=legacy_unfrozen&environment=live", 1}} {
		var page productdelivery.EvidenceGapPage
		if r := requestJSON(t, operator, "GET", base+selection.query, nil, &page); r.StatusCode != 200 || len(page.Items) != selection.count {
			t.Fatal(selection, page, r.StatusCode)
		}
	}
	// The inventory does not inspect missing local files, manufacture contracts,
	// enable immutable-delivery flags, grant rights or modify any order evidence.
	var contracts, snapshots, required int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_order_contracts),(SELECT count(*) FROM product_delivery_snapshots),(SELECT count(*) FROM orders WHERE delivery_snapshot_required)`).Scan(&contracts, &snapshots, &required); err != nil || contracts != 4 || snapshots != 2 || required != 2 {
		t.Fatal(contracts, snapshots, required, err)
	}
	// A large prefix of normal orders must not hide older gaps or cause an
	// unbounded join. An empty first batch must retain a usable continuation.
	ids := make([]uuid.UUID, 501)
	for i := range ids {
		ids[i] = uuid.New()
	}
	exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot,delivery_snapshot_required,created_at)
 SELECT id,$2,$3,1900,'USD','payment_pending',now(),id::text,'1.0','terms','Normal new order','License',7,true,'2030-01-01T00:00:00Z' FROM unnest($1::uuid[]) id`, ids, buyer.ID, product)
	exec(`INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract)
 SELECT id,p.source_asset_id,p.root_asset_id,p.offer_version,p.contract FROM unnest($1::uuid[]) id CROSS JOIN product_offers p WHERE p.product_id=$2`, ids, product)
	exec(`INSERT INTO product_delivery_snapshots(order_id,source_backend,source_key,storage_backend,storage_key,sha256,size_bytes,mime_type,state,ready_at)
 SELECT id,'local_file','private-evidence-key','local_file',id::text,repeat('a',64),5,'text/plain','ready',now() FROM unnest($1::uuid[]) id`, ids)
	var batch productdelivery.EvidenceGapPage
	if r := requestJSON(t, operator, "GET", base, nil, &batch); r.StatusCode != 200 || len(batch.Items) != 0 || batch.Scanned != 500 || batch.NextCursor == nil {
		t.Fatal("bounded empty batch", r.StatusCode, batch)
	}
	var tail productdelivery.EvidenceGapPage
	if r := requestJSON(t, operator, "GET", base+"?cursor="+*batch.NextCursor, nil, &tail); r.StatusCode != 200 || len(tail.Items) != 4 || tail.Scanned != 6 || tail.NextCursor != nil {
		t.Fatal("older gaps skipped", r.StatusCode, tail)
	}
	// Restrict subsequent permission/eligibility checks to the original window.
	base += "?cursor=" + *batch.NextCursor
	exec(`UPDATE users SET status='deleted' WHERE id=$1`, buyer.ID)
	var active productdelivery.EvidenceGapPage
	// Cursors cannot change scope: begin a new active review, then continue its
	// empty prefix using that filter's own cursor.
	activeBase := server.URL + "/api/v1/admin/product-deliveries/evidence-gaps?scope=active"
	if r := requestJSON(t, operator, "GET", activeBase, nil, &batch); r.StatusCode != 200 || batch.NextCursor == nil {
		t.Fatal(r.StatusCode, batch)
	}
	if r := requestJSON(t, operator, "GET", activeBase+"&cursor="+*batch.NextCursor, nil, &active); r.StatusCode != 200 || len(active.Items) != 1 || !active.Items[0].HasPendingPayment {
		t.Fatal(r.StatusCode, active)
	}
	// Deleted buyers have no active rights. Their known pending/failed refunds
	// and refunded orders with missing original merchant evidence must remain
	// discoverable without mislabelling them as pending checkout payments.
	unsettledOrders := map[uuid.UUID]bool{}
	for order, gap := range expected {
		status := map[string]string{"required_snapshot_missing": "refund_pending", "legacy_unfrozen": "refund_failed", "legacy_snapshot_unbound": "refunded"}[gap]
		if status == "" {
			continue
		}
		exec(`UPDATE payment_intents SET status=$2 WHERE order_id=$1`, order, status)
		unsettledOrders[order] = true
	}
	unsettledBase := server.URL + "/api/v1/admin/product-deliveries/evidence-gaps?scope=unsettled&limit=1"
	var jobsBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&jobsBefore); err != nil {
		t.Fatal(err)
	}
	if r := requestJSON(t, operator, "GET", unsettledBase, nil, &batch); r.StatusCode != 200 || len(batch.Items) != 0 || batch.Scanned != 500 || batch.NextCursor == nil {
		t.Fatal("unsettled review lost bounded empty batch", r.StatusCode, batch)
	}
	cursor = *batch.NextCursor
	if r := requestJSON(t, operator, "GET", activeBase+"&cursor="+url.QueryEscape(cursor), nil, nil); r.StatusCode != 422 {
		t.Fatal("unsettled cursor accepted under active filter", r.StatusCode)
	}
	seen = map[uuid.UUID]bool{}
	for i := 0; i < 5; i++ {
		var page productdelivery.EvidenceGapPage
		r := requestJSON(t, operator, "GET", unsettledBase+"&cursor="+url.QueryEscape(cursor), nil, &page)
		if r.StatusCode != 200 || len(page.Items) > 1 {
			t.Fatal(r.StatusCode, page)
		}
		for _, item := range page.Items {
			if !unsettledOrders[item.OrderID] || seen[item.OrderID] || !item.HasUnsettledFunds || item.HasPendingPayment || item.HasActiveRights {
				t.Fatal("incorrect unsettled evidence projection", item)
			}
			seen[item.OrderID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != len(unsettledOrders) {
		t.Fatal("unsettled historical evidence omitted", seen)
	}
	var jobsAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&jobsAfter); err != nil || jobsAfter != jobsBefore {
		t.Fatal("read-only inventory scheduled work", jobsBefore, jobsAfter, err)
	}
	// The existing active scope retains its original meaning.
	batch = productdelivery.EvidenceGapPage{}
	if r := requestJSON(t, operator, "GET", activeBase, nil, &batch); r.StatusCode != 200 || batch.NextCursor == nil {
		t.Fatal(r.StatusCode, batch)
	}
	active = productdelivery.EvidenceGapPage{}
	if r := requestJSON(t, operator, "GET", activeBase+"&cursor="+*batch.NextCursor, nil, &active); r.StatusCode != 200 || len(active.Items) != 0 {
		t.Fatal("refunds reclassified as pending checkout", r.StatusCode, active)
	}
	exec(`UPDATE users SET role='creator' WHERE id=$1`, admin.ID)
	if r := requestJSON(t, operator, "GET", base, nil, nil); r.StatusCode != 403 {
		t.Fatal("revoked permission", r.StatusCode)
	}
	if _, err := productdelivery.NewRepairService(pool, nil).ListEvidenceGaps(ctx, admin.ID, productdelivery.EvidenceGapFilter{}); err != productdelivery.ErrRepairForbidden {
		t.Fatal("service permission", err)
	}
}
