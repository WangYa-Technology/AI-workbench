package marketplace_test

import (
	"context"
	"errors"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPurchaseProvenanceReuseAndRefund(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	sellerID, buyerID, sourceAssetID, productID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedMarketplaceProduct(t, pool, sellerID, buyerID, sourceAssetID, productID)
	service := marketplace.NewService(pool)

	items, err := service.ListProducts(ctx, buyerID, marketplace.ListFilter{Query: "workflow", ProductType: "workflow"})
	if err != nil || len(items) != 1 || items[0].ID != productID {
		t.Fatalf("filtered marketplace product missing: %#v, %v", items, err)
	}
	if _, _, err := service.Purchase(ctx, sellerID, productID, "seller-buy-001", "test", true); !errors.Is(err, marketplace.ErrSellerPurchase) {
		t.Fatalf("seller purchase should be forbidden: %v", err)
	}
	purchase, created, err := service.Purchase(ctx, buyerID, productID, "buyer-purchase-001", "request-purchase", true)
	if err != nil || !created || purchase.RealCharge || purchase.PaymentMode != "test" {
		t.Fatalf("unexpected purchase: %#v, created=%v, err=%v", purchase, created, err)
	}
	replay, created, err := service.Purchase(ctx, buyerID, productID, "buyer-purchase-001", "request-replay", true)
	if err != nil || created || replay.OrderID != purchase.OrderID || !replay.AlreadyOwned {
		t.Fatalf("purchase replay was not idempotent: %#v, created=%v, err=%v", replay, created, err)
	}
	var fulfilledNotificationCount int
	var fulfilledTarget string
	if err := pool.QueryRow(ctx, `
		SELECT count(*),COALESCE(max(target_path),'') FROM notifications
		WHERE user_id=$1 AND kind='marketplace.order_fulfilled' AND resource_id=$2`, buyerID, purchase.OrderID).Scan(&fulfilledNotificationCount, &fulfilledTarget); err != nil {
		t.Fatal(err)
	}
	if fulfilledNotificationCount != 1 || fulfilledTarget != "/workspace/assets/"+purchase.AssetID.String() {
		t.Fatalf("purchase notification is not idempotent or deep-linked: count=%d target=%q", fulfilledNotificationCount, fulfilledTarget)
	}

	assetService := assets.NewService(pool, t.TempDir())
	purchasedAsset, err := assetService.GetOwned(ctx, buyerID, purchase.AssetID)
	if err != nil || purchasedAsset.Provenance == nil || purchasedAsset.Provenance.Purchase == nil {
		t.Fatalf("purchase provenance missing: %#v, %v", purchasedAsset, err)
	}
	if purchasedAsset.Provenance.Purchase.ProductID != productID || purchasedAsset.OriginAssetID == nil || *purchasedAsset.OriginAssetID != sourceAssetID {
		t.Fatalf("purchase source relationship changed: %#v", purchasedAsset)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET title='Changed catalog title' WHERE id=$1`, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE licenses SET name='Changed catalog license',refund_window_days=90 WHERE code='hcai-commercial-standard-v1'`); err != nil {
		t.Fatal(err)
	}
	orderEvidence, err := service.GetOrder(ctx, buyerID, purchase.OrderID)
	if err != nil || orderEvidence.ProductTitle != "Editorial image workflow" || orderEvidence.LicenseName != "HCAI Commercial Standard" || orderEvidence.RefundWindowDays != 7 {
		t.Fatalf("order evidence changed with mutable catalog data: %#v, %v", orderEvidence, err)
	}
	purchasedAsset, err = assetService.GetOwned(ctx, buyerID, purchase.AssetID)
	if err != nil || purchasedAsset.Provenance == nil || purchasedAsset.Provenance.Purchase == nil || purchasedAsset.Provenance.Purchase.LicenseName != "HCAI Commercial Standard" {
		t.Fatalf("asset provenance did not preserve license snapshot: %#v, %v", purchasedAsset, err)
	}

	publication := community.NewRepository(pool)
	_, err = publication.Publish(ctx, buyerID, community.PublishInput{
		AssetID: purchase.AssetID, Title: "Forbidden standalone resale", AIDisclosure: "Purchased source asset, unchanged.", PromptVisibility: "private",
	})
	if !errors.Is(err, community.ErrForbidden) {
		t.Fatalf("purchased source asset must not publish standalone: %v", err)
	}
	creationService := creation.NewService(pool, t.TempDir(), "", true)
	if _, err := creationService.Submit(ctx, buyerID, creation.SubmitInput{Mode: "image", Prompt: "Create an original derivative study", SourceAssetID: &purchase.AssetID}); err != nil {
		t.Fatalf("active purchased asset should be reusable: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE orders SET created_at=now()-interval '8 days' WHERE id=$1`, purchase.OrderID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RequestRefund(ctx, buyerID, purchase.OrderID, "refund-expired-001", "refund-expired", "The asset is no longer needed for this local test project."); !errors.Is(err, marketplace.ErrRefundWindowExpired) {
		t.Fatalf("expired refund should fail: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET created_at=now() WHERE id=$1`, purchase.OrderID); err != nil {
		t.Fatal(err)
	}
	refunded, err := service.RequestRefund(ctx, buyerID, purchase.OrderID, "refund-valid-001", "refund-valid", "The included workflow does not fit the intended local test project.")
	if err != nil || refunded.Status != "test_refunded" || refunded.RefundedAt == nil {
		t.Fatalf("refund failed: %#v, %v", refunded, err)
	}
	replayedRefund, err := service.RequestRefund(ctx, buyerID, purchase.OrderID, "refund-valid-001", "refund-replay", "The included workflow does not fit the intended local test project.")
	if err != nil || replayedRefund.Status != "test_refunded" {
		t.Fatalf("refund replay failed: %#v, %v", replayedRefund, err)
	}
	var refundNotificationCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM notifications
		WHERE user_id=$1 AND kind='marketplace.order_refunded' AND resource_id=$2 AND target_path='/workspace/orders'`, buyerID, purchase.OrderID).Scan(&refundNotificationCount); err != nil {
		t.Fatal(err)
	}
	if refundNotificationCount != 1 {
		t.Fatalf("refund notification is not idempotent: count=%d", refundNotificationCount)
	}
	var refundRiskSignals, refundRiskEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM risk_signals WHERE source_key=$1`, "order_refund:"+purchase.OrderID.String()).Scan(&refundRiskSignals); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM risk_events e JOIN risk_signals s ON s.id=e.signal_id WHERE s.source_key=$1`, "order_refund:"+purchase.OrderID.String()).Scan(&refundRiskEvents); err != nil {
		t.Fatal(err)
	}
	if refundRiskSignals != 1 || refundRiskEvents != 1 {
		t.Fatalf("refund risk signal was not idempotent: signals=%d events=%d", refundRiskSignals, refundRiskEvents)
	}

	var entitlementStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM entitlements WHERE order_id=$1`, purchase.OrderID).Scan(&entitlementStatus); err != nil || entitlementStatus != "refunded" {
		t.Fatalf("entitlement was not revoked: %q, %v", entitlementStatus, err)
	}
	if _, err := creationService.Submit(ctx, buyerID, creation.SubmitInput{Mode: "image", Prompt: "This source right has been refunded", SourceAssetID: &purchase.AssetID}); !errors.Is(err, creation.ErrInvalid) {
		t.Fatalf("refunded asset remained reusable: %v", err)
	}

	var unbalanced, operationCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE debit<>credit),count(*) FROM (
		  SELECT operation_id,
		         COALESCE(sum(amount_cents) FILTER (WHERE direction='debit'),0) debit,
		         COALESCE(sum(amount_cents) FILTER (WHERE direction='credit'),0) credit
		  FROM ledger_entries WHERE reason LIKE 'test_purchase%%' OR reason LIKE 'test_refund%%' GROUP BY operation_id
		) balanced`).Scan(&unbalanced, &operationCount); err != nil {
		t.Fatal(err)
	}
	if unbalanced != 0 || operationCount != 2 {
		t.Fatalf("purchase/refund ledger is not balanced: unbalanced=%d operations=%d", unbalanced, operationCount)
	}
}

func seedMarketplaceProduct(t *testing.T, pool *pgxpool.Pool, sellerID, buyerID, sourceAssetID, productID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	for _, user := range []struct {
		id           uuid.UUID
		handle, name string
	}{{sellerID, "market_seller", "Market Seller"}, {buyerID, "market_buyer", "Market Buyer"}} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,$4,'creator','active')`, user.id, user.handle+"@test.local", user.handle, user.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,license_code) VALUES($1,$2,'image','Source workflow preview','/media/test.jpg','image/jpeg',1600,1200,'clean','demo','hcai-commercial-standard-v1')`, sourceAssetID, sellerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
		VALUES($1,$2,$3,'Editorial image workflow','A reusable editorial workflow for controlled campaign studies.','workflow',2500,'USD','hcai-commercial-standard-v1','active','Local test disclosure.','["Workflow","Preview"]','HCAI Image')`, productID, sellerID, sourceAssetID); err != nil {
		t.Fatal(err)
	}
}

func marketplaceTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		testutil.DatabaseUnavailable(t, err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		testutil.DatabaseUnavailable(t, err)
	}
	schema := "test_marketplace_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(baseURL)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
