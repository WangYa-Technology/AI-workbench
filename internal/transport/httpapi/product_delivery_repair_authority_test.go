package httpapi_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestDeliveryRepairHTTPInFlightRevocation(t *testing.T) {
	for _, action := range []string{"inspect", "repair", "prepare_upload", "resume", "upload"} {
		t.Run(action, func(t *testing.T) {
			pool, cleanup := httpTestPool(t)
			t.Cleanup(cleanup)
			query := "SELECT o.id,o.product_title_snapshot,d.sha256,d.size_bytes,d.state,p.needed"
			occurrence := int32(1)
			if action == "resume" {
				query = "SELECT revision,COALESCE(source_backend"
			}
			if action == "upload" {
				query = "SELECT revision,storage_backend,storage_key,state,source_kind FROM product_delivery_repairs"
				occurrence = 2
			}
			traced, entered, release := testutil.GateNthQuery(t, pool, query, occurrence)
			root := t.TempDir()
			server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, traced, slog.New(slog.NewTextHandler(io.Discard, nil))))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			var workers sync.WaitGroup
			t.Cleanup(func() { release(); cancel(); workers.Wait() })
			client := testHTTPClient(t)
			actor := registerGovernanceUser(t, client, server.URL, "repair_revoke")
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, q, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`UPDATE users SET role='admin' WHERE id=$1`, actor.ID)
			buyer, seller, source, product, order := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			for _, id := range []uuid.UUID{buyer, seller} {
				exec(`INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Repair HTTP owner')`, id, id.String()+"@test.local", "repair_"+id.String()[:8])
			}
			store := media.NewLocalStore(root)
			body := "Private accepted recovery bytes"
			if err := store.Put(ctx, "private-original.txt", []byte(body), "text/plain"); err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','Private original','/private','text/plain','clean','upload','hcai-commercial-standard-v1','local_file','private-original.txt')`, source, seller)
			exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Private repaired resource','Authority fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller, source)
			exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot,delivery_snapshot_required)
 VALUES($1,$2,$3,1900,'USD','payment_pending',now(),$1::uuid::text,'1.0','Accepted terms','Private repaired resource','Accepted license',7,true)`, order, buyer, product)
			exec(`INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract) SELECT $1,source_asset_id,root_asset_id,offer_version,contract FROM product_offers WHERE product_id=$2`, order, product)
			exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','checkout_pending',false,$1::uuid::text)`, uuid.New(), buyer, seller, product, order)
			stores := media.NewCatalog(store)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = productdelivery.ReserveTx(ctx, tx, stores, order); err != nil {
				_ = tx.Rollback(ctx)
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
			method, path, input, contentType := http.MethodGet, "/api/v1/admin/product-deliveries/"+order.String(), "", "application/json"
			var targetKey string
			if action == "repair" || action == "prepare_upload" {
				method = http.MethodPost
				input = `{"expectedRevision":0,"confirmed":true,"reason":"Restore the accepted immutable delivery bytes."}`
				if action == "repair" {
					path += "/repair"
				} else {
					path += "/repair-upload"
				}
			} else if action == "resume" || action == "upload" {
				revision := 0
				status, err := productdelivery.NewRepairService(pool, stores).PrepareUpload(ctx, actor.ID, order, "http-reservation", "fixture", productdelivery.RepairInput{ExpectedRevision: &revision, Confirmed: true, Reason: "Restore the accepted immutable delivery bytes."})
				if err != nil {
					t.Fatal(err)
				}
				if err := pool.QueryRow(ctx, `SELECT storage_key FROM product_delivery_repairs WHERE id=$1`, status.PendingID).Scan(&targetKey); err != nil {
					t.Fatal(err)
				}
				path += "/repairs/" + status.PendingID.String()
				if action == "resume" {
					if err := store.Put(ctx, targetKey, []byte(body), "text/plain"); err != nil {
						t.Fatal(err)
					}
					method, input = http.MethodPost, `{"confirmed":true}`
					path += "/resume"
				} else {
					method, input, contentType = http.MethodPut, body, "application/octet-stream"
					path += "/content?confirmed=true"
				}
			}
			state := func() string {
				t.Helper()
				var value string
				if err := pool.QueryRow(ctx, `SELECT jsonb_build_object('repairs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY r.revision) FROM product_delivery_repairs r WHERE r.order_id=$1),'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM audit_events a WHERE a.resource_id=$1 AND a.action LIKE 'marketplace.delivery_repair_%'))::text`, order).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			before := state()
			type result struct {
				status int
				body   string
				err    error
			}
			run := func() result {
				r, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(input))
				if err != nil {
					return result{err: err}
				}
				r.Header.Set("Content-Type", contentType)
				r.Header.Set("Idempotency-Key", "http-repair-command")
				resp, err := client.Do(r)
				if err != nil {
					return result{err: err}
				}
				defer resp.Body.Close()
				raw, err := io.ReadAll(resp.Body)
				return result{resp.StatusCode, string(raw), err}
			}
			done := make(chan result, 1)
			workers.Add(1)
			go func() { defer workers.Done(); done <- run() }()
			select {
			case <-entered:
			case r := <-done:
				t.Fatalf("did not reach HTTP gate: %+v", r)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			exec(`UPDATE users SET role='member' WHERE id=$1`, actor.ID)
			release()
			if r := <-done; r.err != nil || r.status != 403 || !strings.Contains(r.body, "forbidden") || strings.Contains(r.body, "Private repaired") || strings.Contains(r.body, "private-original") {
				t.Fatalf("revoked request: %+v", r)
			}
			if r := run(); r.err != nil || r.status != 403 {
				t.Fatalf("fresh revoked request: %+v", r)
			}
			if before != state() {
				t.Fatal("revoked HTTP request changed repair evidence")
			}
			if action == "upload" {
				if _, err := store.Stat(ctx, targetKey); !errors.Is(err, media.ErrNotFound) {
					t.Fatal("revoked upload wrote target", err)
				}
			}
		})
	}
}
