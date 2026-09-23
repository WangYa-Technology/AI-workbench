package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestProductDeliveryRepairHTTPPermissionContractAndPrivateEvidence(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	server := httptest.NewUnstartedServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	server.Config.ReadTimeout = 100 * time.Millisecond
	server.Config.WriteTimeout = time.Second
	server.Start()
	defer server.Close()
	guest, adminClient, buyerClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	actor := registerGovernanceUser(t, adminClient, server.URL, "repair_http_operator")
	buyer := registerGovernanceUser(t, buyerClient, server.URL, "repair_http_buyer")
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	source, product, order := uuid.New(), uuid.New(), uuid.New()
	store := media.NewLocalStore(root)
	if err := store.Put(ctx, "original.txt", []byte("Private accepted delivery bytes"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','Original','/private.txt','text/plain','clean','upload','hcai-commercial-standard-v1','local_file','original.txt')`, source, actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Accepted resource','HTTP recovery fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, actor.ID, source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot,delivery_snapshot_required)
 VALUES($1,$2,$3,1900,'USD','payment_pending',now(),$1::uuid::text,'1.0','Accepted terms','Accepted resource','Accepted license',7,true)`, order, buyer.ID, product); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract)
 SELECT $1,source_asset_id,root_asset_id,offer_version,contract FROM product_offers WHERE product_id=$2`, order, product); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','checkout_pending',false,$1::uuid::text)`, uuid.New(), buyer.ID, actor.ID, product, order); err != nil {
		t.Fatal(err)
	}
	stores := media.NewCatalog(store)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = productdelivery.ReserveTx(ctx, tx, stores, order); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = productdelivery.Ensure(ctx, pool, stores, order); err != nil {
		t.Fatal(err)
	}
	snapshot, err := productdelivery.Load(ctx, pool, order)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, snapshot.Key)); err != nil {
		t.Fatal(err)
	}
	base := server.URL + "/api/v1/admin/product-deliveries/" + order.String()
	var occupied []*media.StagedObject
	defer func() {
		for _, stage := range occupied {
			_ = stage.Close()
		}
	}()
	for range 16 {
		stage, err := media.Stage(ctx, strings.NewReader("x"), 1)
		if err != nil {
			t.Fatal(err)
		}
		occupied = append(occupied, stage)
	}
	var busy map[string]any
	response := requestJSON(t, adminClient, "GET", base, nil, &busy)
	encoded, _ := json.Marshal(busy)
	if response.StatusCode != 503 || response.Header.Get("Retry-After") != "5" || !strings.Contains(string(encoded), "media_stage_busy") || strings.Contains(string(encoded), snapshot.Key) {
		t.Fatal("staging capacity response", response.StatusCode, string(encoded))
	}
	for _, stage := range occupied {
		_ = stage.Close()
	}
	occupied = nil
	for _, endpoint := range []string{base, base + "/repair", base + "/repair-upload", base + "/repairs/" + uuid.NewString() + "/resume"} {
		method := "POST"
		if endpoint == base {
			method = "GET"
		}
		if r := requestJSON(t, guest, method, endpoint, nil, nil); r.StatusCode != 401 {
			t.Fatalf("guest endpoint: %s %d", endpoint, r.StatusCode)
		}
		if r := requestJSON(t, buyerClient, method, endpoint, nil, nil); r.StatusCode != 403 {
			t.Fatalf("buyer endpoint: %s %d", endpoint, r.StatusCode)
		}
	}
	var inspected map[string]any
	if r := requestJSON(t, adminClient, "GET", base, nil, &inspected); r.StatusCode != 200 || r.Header.Get("Cache-Control") != "private, no-store" || inspected["health"] != "missing" || inspected["canRepair"] != true {
		t.Fatalf("inspect: %d %+v", r.StatusCode, inspected)
	}
	raw, _ := json.Marshal(inspected)
	for _, private := range []string{snapshot.Key, "original.txt", "storage", "buyerId", buyer.ID.String(), "repair_http_buyer"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private evidence projected", private)
		}
	}
	input := map[string]any{"expectedRevision": 0, "confirmed": true, "reason": "Restore the independently verified original."}
	for _, key := range []string{"", "short"} {
		if r := requestPaymentJSON(t, adminClient, "POST", base+"/repair", key, input, nil); r.StatusCode != 422 {
			t.Fatal("invalid key", r.StatusCode)
		}
	}
	for _, invalid := range []map[string]any{
		{"confirmed": true, "reason": input["reason"]},
		{"expectedRevision": 0, "confirmed": false, "reason": input["reason"]},
		{"expectedRevision": 2147483647, "confirmed": true, "reason": input["reason"]},
		{"expectedRevision": 0, "confirmed": true, "reason": "short"},
	} {
		if r := requestPaymentJSON(t, adminClient, "POST", base+"/repair", uuid.NewString(), invalid, nil); r.StatusCode != 422 {
			t.Fatal("invalid input", r.StatusCode, invalid)
		}
	}
	var result productdelivery.RepairStatus
	for range 2 {
		if r := requestPaymentJSON(t, adminClient, "POST", base+"/repair", "http-repair-command", input, &result); r.StatusCode != 200 || result.Health != "healthy" || result.Revision != 1 || r.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("repair/replay: %d %+v", r.StatusCode, result)
		}
	}
	var repair uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT id FROM product_delivery_repairs WHERE order_id=$1`, order).Scan(&repair); err != nil {
		t.Fatal(err)
	}
	resume := base + "/repairs/" + repair.String() + "/resume"
	if r := requestJSON(t, adminClient, "POST", resume, map[string]bool{"confirmed": false}, nil); r.StatusCode != 422 {
		t.Fatal("unconfirmed resume", r.StatusCode)
	}
	if r := requestJSON(t, adminClient, "POST", resume, map[string]bool{"confirmed": true}, &result); r.StatusCode != 200 || result.Revision != 1 {
		t.Fatal("resume replay", r.StatusCode, result)
	}

	// A direct upload restores the same bytes without requiring the original.
	current, err := productdelivery.Load(ctx, pool, order)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, current.Key)); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, "original.txt")); err != nil {
		t.Fatal(err)
	}
	input["expectedRevision"] = 1
	if r := requestPaymentJSON(t, adminClient, "POST", base+"/repair-upload", "http-upload-command", input, &result); r.StatusCode != 200 || result.PendingID == nil || result.PendingSource != "upload" {
		t.Fatalf("prepare upload: %d %+v", r.StatusCode, result)
	}
	contentURL := base + "/repairs/" + result.PendingID.String() + "/content"
	rawUpload := func(client *http.Client, suffix, contentType string, body io.Reader, size int64, want int) {
		t.Helper()
		req, e := http.NewRequest("PUT", contentURL+suffix, body)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Origin", "http://localhost:5173")
		if size > 0 {
			req.ContentLength = size
			req.Header.Set("Expect", "100-continue")
		}
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		payload, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		if res.StatusCode != want {
			t.Fatalf("upload %d expected %d: %s", res.StatusCode, want, payload)
		}
	}
	for _, c := range []struct {
		client *http.Client
		want   int
	}{{guest, 401}, {buyerClient, 403}} {
		rawUpload(c.client, "?confirmed=true", "application/octet-stream", strings.NewReader("bytes"), 0, c.want)
	}
	for _, query := range []string{"", "?confirmed=false", "?confirmed=true&confirmed=true", "?confirmed=true&extra=1"} {
		rawUpload(adminClient, query, "application/octet-stream", strings.NewReader("bytes"), 0, 422)
	}
	for _, kind := range []string{"application/json", "multipart/form-data", "application/octet-stream; charset=utf-8"} {
		rawUpload(adminClient, "?confirmed=true", kind, strings.NewReader("bytes"), 0, 415)
	}
	rawUpload(adminClient, "?confirmed=true", "application/octet-stream", strings.NewReader("bytes"), productdelivery.MaxBytes+1, 413)
	rawUpload(adminClient, "?confirmed=true", "application/octet-stream", strings.NewReader("wrong"), 0, 422)
	// Body arrives after the server's normal read/write deadlines. The upload
	// endpoint's ResponseController must reach the underlying wrapped writer.
	reader, writer := io.Pipe()
	go func() {
		time.Sleep(1200 * time.Millisecond)
		_, _ = writer.Write([]byte("Private accepted delivery bytes"))
		_ = writer.Close()
	}()
	rawUpload(adminClient, "?confirmed=true", "application/octet-stream", reader, 0, 200)
	reader.Close()
	rawUpload(adminClient, "?confirmed=true", "application/octet-stream", strings.NewReader("Private accepted delivery bytes"), 0, 200)
	if r := requestJSON(t, adminClient, "GET", base, nil, &result); r.StatusCode != 200 || result.Health != "healthy" || result.Revision != 2 {
		t.Fatal("uploaded delivery", r.StatusCode, result)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	if r := requestJSON(t, adminClient, "GET", base, nil, nil); r.StatusCode != 401 && r.StatusCode != 403 {
		t.Fatal("inactive operator", r.StatusCode)
	}
}
