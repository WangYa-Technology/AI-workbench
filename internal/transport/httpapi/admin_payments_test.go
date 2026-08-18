package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminPaymentOperationsHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient, creatorClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	member := registerGovernanceUser(t, memberClient, server.URL, "payment_http_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "payment_http_admin")
	creator := registerGovernanceUser(t, creatorClient, server.URL, "payment_http_creator")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}

	transferTaskID, transferPaymentID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary)
		VALUES($1,$2,'HTTP transfer recovery','A funded task requiring a controlled payout recovery.','image',12000,'USD',now()+interval '7 days','accepted',$3,'HTTP payment operations evidence.')`,
		transferTaskID, member.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id,provider_charge_id)
		VALUES($1,'stripe','task',$2,$3,$4,12000,'USD','transfer_pending',false,'http-transfer-recovery','pi_http_transfer','ch_http_transfer')`,
		transferPaymentID, member.ID, creator.ID, transferTaskID); err != nil {
		t.Fatal(err)
	}

	assetID, productID, orderID, productPaymentID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','HTTP payment product source','/media/http-payment.jpg','image/jpeg','clean','demo','hcai-commercial-standard-v1')`, assetID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
		VALUES($1,$2,$3,'HTTP refund recovery product','A product with failed Provider refund evidence.','workflow',1900,'USD','hcai-commercial-standard-v1','active')`, productID, creator.ID, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot)
		VALUES($1,$2,$3,1900,'USD','fulfilled',now(),'http-product-order','1.0','HTTP license snapshot','HTTP refund recovery product','Commercial Standard',14)`, orderID, member.ID, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id,provider_charge_id)
		VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','paid',false,'http-product-refund','pi_http_product','ch_http_product')`, productPaymentID, member.ID, creator.ID, productID, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
		VALUES($1,'refund.failed','refund_pending','paid','{"errorCode":"provider_unavailable"}')`, productPaymentID); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/v1/admin/payments", "/api/v1/admin/payment-destinations"} {
		response := requestJSON(t, memberClient, http.MethodGet, server.URL+path, nil, nil)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("member accessed %s: %d", path, response.StatusCode)
		}
	}
	for _, path := range []string{
		"/api/v1/admin/payments?purpose=subscription",
		"/api/v1/admin/payments?limit=51",
		"/api/v1/admin/payment-destinations?status=pending",
	} {
		response := requestJSON(t, adminClient, http.MethodGet, server.URL+path, nil, nil)
		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("invalid payment filter accepted for %s: %d", path, response.StatusCode)
		}
	}

	var first, second admin.PaymentOperationPage
	response := requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?mode=test&limit=1", nil, &first)
	if response.StatusCode != http.StatusOK || len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first payment page mismatch: status=%d page=%#v", response.StatusCode, first)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?mode=test&limit=1&cursor="+url.QueryEscape(*first.NextCursor), nil, &second)
	if response.StatusCode != http.StatusOK || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second payment page mismatch: status=%d page=%#v", response.StatusCode, second)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?limit=1&cursor="+url.QueryEscape(*first.NextCursor+"modified"), nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified payment cursor accepted: %d", response.StatusCode)
	}
	var attention admin.PaymentOperationPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?purpose=task&status=transfer_pending&attention=needs_attention", nil, &attention)
	if response.StatusCode != http.StatusOK || len(attention.Items) != 1 || attention.Items[0].ID != transferPaymentID || attention.Items[0].AttentionCode != "destination_missing" {
		t.Fatalf("payment attention filter mismatch: status=%d page=%#v", response.StatusCode, attention)
	}

	destinationInput := map[string]any{
		"destinationId": "acct_http_payment_creator", "enabled": true, "expectedVersion": 0,
		"reason": "Verify the Sandbox creator destination for HTTP recovery evidence.", "confirmed": false,
	}
	response = requestJSON(t, adminClient, http.MethodPut, server.URL+"/api/v1/admin/payment-destinations/"+creator.ID.String(), destinationInput, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed destination update accepted: %d", response.StatusCode)
	}
	destinationInput["confirmed"] = true
	var destination admin.PaymentDestination
	response = requestJSON(t, adminClient, http.MethodPut, server.URL+"/api/v1/admin/payment-destinations/"+creator.ID.String(), destinationInput, &destination)
	if response.StatusCode != http.StatusOK || destination.Status != "verified" || destination.Version != 1 {
		t.Fatalf("destination update mismatch: status=%d destination=%#v", response.StatusCode, destination)
	}
	response = requestJSON(t, adminClient, http.MethodPut, server.URL+"/api/v1/admin/payment-destinations/"+creator.ID.String(), destinationInput, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale destination version accepted: %d", response.StatusCode)
	}
	var destinations admin.PaymentDestinationPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payment-destinations?q="+url.QueryEscape(creator.Handle)+"&status=verified", nil, &destinations)
	if response.StatusCode != http.StatusOK || len(destinations.Items) != 1 || destinations.Items[0].UserID != creator.ID {
		t.Fatalf("destination list mismatch: status=%d page=%#v", response.StatusCode, destinations)
	}

	transferInput := map[string]any{
		"action": "retry_transfer", "expectedVersion": 1,
		"reason": "Retry the task transfer after verifying the creator destination.", "confirmed": false,
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/"+transferPaymentID.String()+"/recover", transferInput, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed transfer recovery accepted: %d", response.StatusCode)
	}
	transferInput["confirmed"] = true
	var transfer admin.PaymentOperation
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/"+transferPaymentID.String()+"/recover", transferInput, &transfer)
	if response.StatusCode != http.StatusOK || transfer.Version != 2 || transfer.Job == nil || transfer.Job.Kind != payments.TaskTransferJobKind || transfer.Job.Status != "queued" {
		t.Fatalf("transfer recovery mismatch: status=%d payment=%#v", response.StatusCode, transfer)
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/"+transferPaymentID.String()+"/recover", map[string]any{
		"action": "retry_transfer", "expectedVersion": 2, "reason": "A queued transfer must not be enqueued twice.", "confirmed": true,
	}, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate transfer recovery accepted: %d", response.StatusCode)
	}

	var productRefund admin.PaymentOperation
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/"+productPaymentID.String()+"/recover", map[string]any{
		"action": "retry_refund", "expectedVersion": 1,
		"reason": "Retry the failed product Provider refund through the controlled operations queue.", "confirmed": true,
	}, &productRefund)
	if response.StatusCode != http.StatusOK || productRefund.Status != "refund_pending" || productRefund.Version != 2 || productRefund.Job == nil || productRefund.Job.Kind != payments.ProductRefundJobKind {
		t.Fatalf("product refund recovery mismatch: status=%d payment=%#v", response.StatusCode, productRefund)
	}

	eventID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_provider_events(id,provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,payment_id,purpose)
		VALUES($1,'stripe','evt_http_payment_replay','payment_intent.succeeded','2026-02-25.clover',false,now(),repeat('a',64),'pi_http_replay','payment_intent',$2,'task')`, eventID, transferPaymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_provider_event_processing(event_id,status,attempt_count,error_code,processed_at)
		VALUES($1,'failed',8,'payment_response_invalid',now())`, eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO jobs(kind,payload,status,attempts,max_attempts,last_error,last_error_code)
		VALUES('payment.process_event',jsonb_build_object('eventId',$1::text),'failed',8,8,'payment_response_invalid','payment_response_invalid')`, eventID); err != nil {
		t.Fatal(err)
	}
	replayInput := map[string]any{
		"expectedVersion": 1, "reason": "Replay the failed signed event after correcting internal processing evidence.", "confirmed": false,
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/events/"+eventID.String()+"/replay", replayInput, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed payment event replay accepted: %d", response.StatusCode)
	}
	replayInput["confirmed"] = true
	var replayed admin.PaymentOperation
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/events/"+eventID.String()+"/replay", replayInput, &replayed)
	if response.StatusCode != http.StatusOK || replayed.ProviderEvent == nil || replayed.ProviderEvent.Version != 2 || replayed.ProviderEvent.ReplayCount != 1 || replayed.ProviderEvent.Job == nil || replayed.ProviderEvent.Job.Status != "queued" {
		t.Fatalf("payment event replay mismatch: status=%d payment=%#v", response.StatusCode, replayed)
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/events/"+eventID.String()+"/replay", map[string]any{
		"expectedVersion": 2, "reason": "An active replay job must not be duplicated.", "confirmed": true,
	}, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate payment event replay accepted: %d", response.StatusCode)
	}
}
