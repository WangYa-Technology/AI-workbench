package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestSellerSalesHTTPPrivateReadBoundary(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	guest, sellerClient, buyerClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	seller := registerGovernanceUser(t, sellerClient, server.URL, "sales_seller")
	buyer := registerGovernanceUser(t, buyerClient, server.URL, "sales_buyer")
	source, product, order := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES($1,$2,'document','Original','/media/original.txt','text/plain','clean','upload','hcai-commercial-standard-v1','local_file','seller-original.txt')`, source, seller.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status) VALUES($1,$2,$3,'Current title','Sales HTTP fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller.ID, source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot) VALUES($1,$2,$3,1900,'USD','fulfilled',now(),$1::uuid::text,'1.0','Private accepted terms','Accepted title','Accepted license',7)`, order, buyer.ID, product); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,paid_at) VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','paid',true,$1::uuid::text,now())`, uuid.New(), buyer.ID, seller.ID, product, order); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_events(order_id,actor_id,sequence,to_status,reason) VALUES($1,$2,1,'fulfilled','Buyer private note with email')`, order, buyer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_settlements(order_id,payment_id,seller_id,provider,live_mode,gross_amount_cents,fee_bps,fee_cents,net_amount_cents,
 currency,status,available_at,transfer_idempotency_key,destination_id,provider_transfer_id,transferred_at)
 SELECT order_id,id,payee_id,provider,live_mode,1900,250,48,1852,'USD','transferred',now(),'private-transfer-key','acct_private_destination','tr_private_transfer',now()
 FROM payment_intents WHERE order_id=$1`, order); err != nil {
		t.Fatal(err)
	}
	base := server.URL + "/api/v1/seller/sales"
	for _, path := range []string{base, base + "/" + order.String(), base + "/" + order.String() + "/events"} {
		if r := requestJSON(t, guest, "GET", path, nil, nil); r.StatusCode != 401 {
			t.Fatal("guest read", r.StatusCode)
		}
	}
	for _, suffix := range []string{"?status=bad", "?environment=production", "?limit=0", "?limit=", "?limit=51", "?limit=1&limit=2", "?status=fulfilled&status=cancelled", "?environment=live&environment=test", "?cursor=bad", "?productId=", "?productId=00000000-0000-0000-0000-000000000000", "?buyerId=" + buyer.ID.String()} {
		if r := requestJSON(t, sellerClient, "GET", base+suffix, nil, nil); r.StatusCode != 422 {
			t.Fatal("invalid filter", suffix, r.StatusCode)
		}
	}
	var page marketplace.SellerSalesPage
	r := requestJSON(t, sellerClient, "GET", base+"?status=fulfilled&environment=live&productId="+product.String(), nil, &page)
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "private, no-store" || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal("page", r.StatusCode, page)
	}
	if settlement := page.Items[0].Settlement; settlement == nil || settlement.NetAmountCents != 1852 || settlement.TransferredAt == nil || settlement.Status != "transferred" {
		t.Fatal("missing HTTP settlement", settlement)
	}
	var foreign marketplace.SellerSalesPage
	if r = requestJSON(t, buyerClient, "GET", base, nil, &foreign); r.StatusCode != 200 || foreign.Total != 0 {
		t.Fatal("buyer sales", r.StatusCode, foreign)
	}
	for _, suffix := range []string{"/" + order.String(), "/" + order.String() + "/events"} {
		if r = requestJSON(t, buyerClient, "GET", base+suffix, nil, nil); r.StatusCode != 404 {
			t.Fatal("foreign detail", r.StatusCode)
		}
		var data map[string]any
		if r = requestJSON(t, sellerClient, "GET", base+suffix, nil, &data); r.StatusCode != 200 || r.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("private detail", r.StatusCode)
		}
		raw, _ := json.Marshal(data)
		for _, value := range []string{buyer.ID.String(), "sales_buyer", "buyerId", "actorId", "paymentId", "providerPaymentId", "reason", "checkoutUrl", "storageKey", "Buyer private note", "acct_private_destination", "tr_private_transfer", "private-transfer-key"} {
			if strings.Contains(string(raw), value) {
				t.Fatal("private field", value, string(raw))
			}
		}
	}
	if r = requestJSON(t, sellerClient, "GET", base+"/"+order.String()+"/events?status=fulfilled", nil, nil); r.StatusCode != 422 {
		t.Fatal("event filter", r.StatusCode)
	}
	if r = requestJSON(t, sellerClient, "GET", base+"/"+order.String()+"?cursor=anything", nil, nil); r.StatusCode != 422 {
		t.Fatal("detail ignored filter", r.StatusCode)
	}
	// Read permission never becomes buyer refund permission or an asset grant.
	if r = requestPaymentJSON(t, sellerClient, "POST", server.URL+"/api/v1/orders/"+order.String()+"/refund", "seller-refund-forbidden", map[string]string{"reason": "Seller cannot act as the buyer"}, nil); r.StatusCode != 404 {
		t.Fatal("seller refund", r.StatusCode)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlements WHERE user_id=$1`, seller.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("seller read granted access", count, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, seller.ID); err != nil {
		t.Fatal(err)
	}
	if r = requestJSON(t, sellerClient, http.MethodGet, base, nil, nil); r.StatusCode != 401 && r.StatusCode != 403 {
		t.Fatal("inactive session", r.StatusCode)
	}
}
