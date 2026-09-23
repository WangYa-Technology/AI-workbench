package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminFinanceRecoveryHTTPRevocation(t *testing.T) {
	for _, action := range []string{"recover", "replay"} {
		t.Run(action, func(t *testing.T) {
			pool, cleanup := httpTestPool(t)
			t.Cleanup(cleanup)
			traced, entered, release := testutil.GateQuery(t, pool, "SELECT EXISTS(SELECT 1 FROM jobs WHERE kind=$1")
			server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, traced, slog.New(slog.NewTextHandler(io.Discard, nil))))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			var workers sync.WaitGroup
			t.Cleanup(func() { release(); cancel(); workers.Wait() })
			client := testHTTPClient(t)
			actor := registerGovernanceUser(t, client, server.URL, "finance_revoke")
			buyer, seller, task, payment, event := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`UPDATE users SET role='admin' WHERE id=$1`, actor.ID)
			for _, id := range []uuid.UUID{buyer, seller} {
				exec(`INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Finance HTTP')`, id, id.String()+"@test.local", "finance_"+id.String()[:8])
			}
			exec(`INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary)
 VALUES($1,$2,'Finance HTTP task','Recovery fixture','image',1900,'USD',now()+interval '7 days','accepted',$3,'Recovery fixture')`, task, buyer, seller)
			exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id)
 VALUES($1,'stripe','task',$2,$3,$4,1900,'USD','transfer_pending',false,'finance-authority-http','pi_finance_authority')`, payment, buyer, seller, task)
			path := "/api/v1/admin/payments/" + payment.String() + "/recover"
			body := `{"action":"retry_transfer","expectedVersion":1}`
			if action == "replay" {
				exec(`INSERT INTO payment_provider_events(id,provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,payment_id,purpose)
 VALUES($1,'stripe','evt_finance_http','payment_intent.succeeded','2026-02-25.clover',false,now(),repeat('a',64),'pi_finance_authority','payment_intent',$2,'task')`, event, payment)
				exec(`INSERT INTO payment_provider_event_processing(event_id,status,error_code,processed_at) VALUES($1,'failed','provider_unavailable',now())`, event)
				path = "/api/v1/admin/payments/events/" + event.String() + "/replay"
				body = `{"expectedVersion":1}`
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			type result struct {
				status int
				body   string
				err    error
			}
			done := make(chan result, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				response, err := client.Do(request)
				if err != nil {
					done <- result{err: err}
					return
				}
				defer response.Body.Close()
				raw, err := io.ReadAll(response.Body)
				done <- result{response.StatusCode, string(raw), err}
			}()
			select {
			case <-entered:
			case r := <-done:
				t.Fatalf("HTTP operation did not reach mutation gate: %#v", r)
			case <-ctx.Done():
				t.Fatal("HTTP operation did not reach mutation gate", ctx.Err())
			}
			exec(`UPDATE users SET role='member' WHERE id=$1`, actor.ID)
			release()
			r := <-done
			if r.err != nil || r.status != 409 || !strings.Contains(r.body, "admin_state_conflict") || strings.Contains(r.body, "pi_finance_authority") {
				t.Fatalf("revoked HTTP mutation was not safely rejected: %#v", r)
			}
			if r := requestJSON(t, client, http.MethodPost, server.URL+path, map[string]any{"action": "retry_transfer", "expectedVersion": 1}, nil); r.StatusCode != 403 {
				t.Fatal("fresh revoked HTTP command", r.StatusCode)
			}
			var version, jobs, replays int
			if err := pool.QueryRow(ctx, `SELECT (SELECT version FROM payment_intents WHERE id=$1),(SELECT count(*) FROM jobs WHERE kind LIKE 'payment.%'),COALESCE((SELECT replay_count FROM payment_provider_event_processing WHERE event_id=$2),0)`, payment, event).Scan(&version, &jobs, &replays); err != nil || version != 1 || jobs != 0 || replays != 0 {
				t.Fatalf("revoked command changed evidence: %d %d %d %v", version, jobs, replays, err)
			}
		})
	}
}

func TestAdminPaymentOperationsHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
		StripeEnabled: true, StripeSecretKey: "test-disabled-network", StripeBaseURL: "http://127.0.0.1:1", StripeAPIVersion: "2026-02-25.clover",
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
		VALUES($1,$2,'image','HTTP payment product source','/media/http-payment.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1')`, assetID, creator.ID); err != nil {
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
	// This case models an order whose original merchant evidence is present.
	if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_requests(payment_id,identity,request)
 SELECT pi.id,jsonb_build_object('provider',pi.provider,'merchantId','acct_fixture','liveMode',pi.live_mode,
 'endpoint','https://api.stripe.com/v1','apiVersion','2026-02-25.clover','requestVersion','stripe-product-checkout-v1'),
 jsonb_build_object('PaymentID',pi.id,'Purpose','product','ResourceID',pi.resource_id,'OrderExternalID',pi.order_id,
 'BuyerIdentity',pi.payer_id,'BuyerEmail',u.email,'AmountCents',pi.amount_cents,'Currency',pi.currency)
 FROM payment_intents pi JOIN users u ON u.id=pi.payer_id WHERE pi.id=$1`, productPaymentID); err != nil {
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
		"/api/v1/admin/payments?purpose=unknown",
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
	}
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
	}
	var transfer admin.PaymentOperation
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/"+transferPaymentID.String()+"/recover", transferInput, &transfer)
	if response.StatusCode != http.StatusOK || transfer.Version != 2 || transfer.Job == nil || transfer.Job.Kind != payments.TaskTransferJobKind || transfer.Job.Status != "queued" {
		t.Fatalf("transfer recovery mismatch: status=%d payment=%#v", response.StatusCode, transfer)
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/"+transferPaymentID.String()+"/recover", map[string]any{
		"action": "retry_transfer", "expectedVersion": 2}, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate transfer recovery accepted: %d", response.StatusCode)
	}

	var productRefund admin.PaymentOperation
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/"+productPaymentID.String()+"/recover", map[string]any{
		"action": "retry_refund", "expectedVersion": 1,
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
		"expectedVersion": 1}
	var replayed admin.PaymentOperation
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/events/"+eventID.String()+"/replay", replayInput, &replayed)
	if response.StatusCode != http.StatusOK || replayed.ProviderEvent == nil || replayed.ProviderEvent.Version != 2 || replayed.ProviderEvent.ReplayCount != 1 || replayed.ProviderEvent.Job == nil || replayed.ProviderEvent.Job.Status != "queued" {
		t.Fatalf("payment event replay mismatch: status=%d payment=%#v", response.StatusCode, replayed)
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/payments/events/"+eventID.String()+"/replay", map[string]any{
		"expectedVersion": 2}, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate payment event replay accepted: %d", response.StatusCode)
	}
	historyPath := server.URL + "/api/v1/admin/payments/" + productPaymentID.String() + "/refund-history"
	checkPath := server.URL + "/api/v1/admin/payments/" + productPaymentID.String() + "/refund-checks"
	if response := requestJSON(t, memberClient, http.MethodGet, historyPath, nil, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("member read refund evidence: %d", response.StatusCode)
	}
	if response := requestJSON(t, memberClient, http.MethodPost, checkPath, map[string]any{"expectedVersion": 2}, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("member queried provider: %d", response.StatusCode)
	}
	var history payments.RefundHistory
	response = requestJSON(t, adminClient, http.MethodGet, historyPath, nil, &history)
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private, no-store" || len(history.Items) != 1 || !history.CanCheck {
		t.Fatalf("refund history contract: %d %#v", response.StatusCode, history)
	}
	if response := requestJSON(t, adminClient, http.MethodGet, historyPath+"?limit=51", nil, nil); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unbounded history allowed: %d", response.StatusCode)
	}
	if response := requestJSON(t, adminClient, http.MethodGet, historyPath+"?cursor="+uuid.NewString(), nil, nil); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("foreign cursor accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodPost, checkPath, map[string]any{"expectedVersion": history.PaymentVersion}, &history)
	if response.StatusCode != http.StatusOK || history.LatestCheck == nil || history.LatestCheck.Status != "requested" || history.LatestCheck.Origin != "operator" || history.CanCheck {
		t.Fatalf("refund check not queued: %d %#v", response.StatusCode, history)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, checkPath, map[string]any{"expectedVersion": history.PaymentVersion}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate check accepted: %d", response.StatusCode)
	}
	var checkingPayments admin.PaymentOperationPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?q="+productPaymentID.String()+"&attention=needs_attention", nil, &checkingPayments)
	if response.StatusCode != http.StatusOK || len(checkingPayments.Items) != 1 || checkingPayments.Items[0].AttentionCode != "refund_reconciliation_required" {
		t.Fatalf("active query still offers refund recovery: %#v", checkingPayments)
	}
	var checks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1`, payments.ProductRefundCheckJobKind).Scan(&checks); err != nil || checks != 1 {
		t.Fatalf("unauthorized or duplicate work queued: %d %v", checks, err)
	}

	oldCheck, oldJob := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(id,kind,status,payload) VALUES($1,$2,'succeeded',jsonb_build_object('checkId',$3::text))`, oldJob, payments.ProductRefundCheckJobKind, oldCheck); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_refund_checks(id,payment_id,job_id,origin,status,observations,observed_at,completed_at,created_at,unresolved_count)
 VALUES($1,$2,$3,'automatic','completed','[{"providerId":"re_old_unbound","providerPaymentId":"pi_http_product","amountCents":100,"currency":"USD","status":"succeeded"}]',now()-interval '1 day',now()-interval '1 day',now()-interval '1 day',1)`, oldCheck, productPaymentID, oldJob); err != nil {
		t.Fatal(err)
	}
	detailPath := checkPath + "/" + oldCheck.String()
	for _, path := range []string{checkPath, detailPath} {
		if res := requestJSON(t, memberClient, http.MethodGet, path, nil, nil); res.StatusCode != http.StatusForbidden {
			t.Fatalf("buyer accessed finance-only query history: %d", res.StatusCode)
		}
		if res := requestJSON(t, testHTTPClient(t), http.MethodGet, path, nil, nil); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous query history access: %d", res.StatusCode)
		}
	}
	var pageChecks payments.RefundCheckPage
	response = requestJSON(t, adminClient, http.MethodGet, checkPath+"?limit=1", nil, &pageChecks)
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private, no-store" || len(pageChecks.Items) != 1 || pageChecks.NextCursor == nil {
		t.Fatalf("query history contract: %d %+v", response.StatusCode, pageChecks)
	}
	response = requestJSON(t, adminClient, http.MethodGet, checkPath+"?limit=1&cursor="+url.QueryEscape(*pageChecks.NextCursor), nil, &pageChecks)
	if response.StatusCode != http.StatusOK || len(pageChecks.Items) != 1 || pageChecks.Items[0].ID != oldCheck || !pageChecks.Items[0].RequiresReview {
		t.Fatalf("historical query missing: %d %+v", response.StatusCode, pageChecks)
	}
	response = requestJSON(t, adminClient, http.MethodGet, checkPath+"?review=unresolved", nil, &pageChecks)
	if response.StatusCode != http.StatusOK || len(pageChecks.Items) != 1 || pageChecks.Items[0].ID != oldCheck {
		t.Fatal("unresolved history filter", response.StatusCode, pageChecks)
	}
	var checkDetail payments.RefundCheckDetail
	response = requestJSON(t, adminClient, http.MethodGet, detailPath, nil, &checkDetail)
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private, no-store" || len(checkDetail.Observations) != 1 || len(checkDetail.UnresolvedProviderRefundIDs) != 1 {
		t.Fatal("historical evidence unavailable", response.StatusCode, checkDetail)
	}
	for _, query := range []string{"?review=invalid", "?limit=51", "?cursor=invalid"} {
		if res := requestJSON(t, adminClient, http.MethodGet, checkPath+query, nil, nil); res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatal("invalid history query accepted", query, res.StatusCode)
		}
	}
	foreignPath := server.URL + "/api/v1/admin/payments/" + transferPaymentID.String() + "/refund-checks/" + oldCheck.String()
	if res := requestJSON(t, adminClient, http.MethodGet, foreignPath, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatal("query from another payment exposed", res.StatusCode)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1`, payments.ProductRefundCheckJobKind).Scan(&checks); err != nil || checks != 2 {
		t.Fatal("history reads created remote work", checks, err)
	}

	receiptID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO product_refund_read_receipts(id,check_id,attempt_number,complete,observations,evidence_sha256)
 VALUES($1,$2,0,true,'[{"providerId":"re_late_http","providerPaymentId":"pi_http_product","amountCents":100,"currency":"USD","status":"succeeded"}]',repeat('c',64))`, receiptID, oldCheck); err != nil {
		t.Fatal(err)
	}
	receiptPath := detailPath + "/receipts"
	for _, caller := range []*http.Client{memberClient, testHTTPClient(t)} {
		res := requestJSON(t, caller, http.MethodGet, receiptPath, nil, nil)
		if res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusForbidden {
			t.Fatal("late receipts lack finance permission", res.StatusCode)
		}
	}
	var receiptPage payments.RefundReadReceiptPage
	response = requestJSON(t, adminClient, http.MethodGet, receiptPath, nil, &receiptPage)
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private, no-store" || len(receiptPage.Items) != 1 || receiptPage.Items[0].ID != receiptID || len(receiptPage.Items[0].UnresolvedProviderRefundIDs) != 1 {
		t.Fatal("late receipt HTTP contract", response.StatusCode, receiptPage)
	}
	for _, suffix := range []string{"?cursor=invalid", "?cursor=" + uuid.NewString(), "?cursor=a&cursor=b", "?limit=50"} {
		if res := requestJSON(t, adminClient, http.MethodGet, receiptPath+suffix, nil, nil); res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatal("invalid receipt cursor accepted", suffix, res.StatusCode)
		}
	}
	if res := requestJSON(t, adminClient, http.MethodGet, foreignPath+"/receipts", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatal("foreign receipt exposed", res.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodGet, detailPath, nil, &checkDetail)
	if response.StatusCode != http.StatusOK || checkDetail.LateReceiptCount != 1 || len(checkDetail.Observations) != 1 || len(checkDetail.UnresolvedProviderRefundIDs) != 1 {
		t.Fatal("late receipt changed original response", checkDetail)
	}
	readID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO product_refund_read_executions(id,check_id,attempt_number) VALUES($1,$2,0)`, readID, oldCheck); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, adminClient, http.MethodGet, detailPath, nil, &checkDetail)
	if response.StatusCode != http.StatusOK || checkDetail.UnrecordedReadCount != 1 || checkDetail.RecoveredReadCount != 0 || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("unrecorded read missing from private detail", checkDetail)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1`, payments.ProductRefundCheckJobKind).Scan(&checks); err != nil || checks != 2 {
		t.Fatal("receipt history started remote work", checks, err)
	}

	// Expired checkout recovery queues a read, never a new charge or a local
	// cancellation. The deliberately unreachable provider must not be contacted.
	checkoutOrder, checkoutPayment := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot)
 SELECT $1,$2,product_id,amount_cents,currency,'payment_pending','http-expired-order',product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot FROM orders WHERE id=$3`, checkoutOrder, administrator.ID, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_checkout_id,checkout_url,checkout_expires_at)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','checkout_open',false,'http-expired-checkout','cs_http_expired','https://checkout.stripe.com/c/pay/expired',now()-interval '1 hour')`, checkoutPayment, administrator.ID, creator.ID, productID, checkoutOrder); err != nil {
		t.Fatal(err)
	}
	// This case models an order whose original merchant evidence is present.
	if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_requests(payment_id,identity,request)
 SELECT pi.id,jsonb_build_object('provider',pi.provider,'merchantId','acct_fixture','liveMode',pi.live_mode,
 'endpoint','https://api.stripe.com/v1','apiVersion','2026-02-25.clover','requestVersion','stripe-product-checkout-v1'),
 jsonb_build_object('PaymentID',pi.id,'Purpose','product','ResourceID',pi.resource_id,'OrderExternalID',pi.order_id,
 'BuyerIdentity',pi.payer_id,'BuyerEmail',u.email,'AmountCents',pi.amount_cents,'Currency',pi.currency)
 FROM payment_intents pi JOIN users u ON u.id=pi.payer_id WHERE pi.id=$1`, checkoutPayment); err != nil {
		t.Fatal(err)
	}

	checkoutPath := server.URL + "/api/v1/admin/payments/" + checkoutPayment.String() + "/recover"
	input := map[string]any{"action": "check_checkout", "expectedVersion": 1}
	if response := requestJSON(t, memberClient, http.MethodPost, checkoutPath, input, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("member checked checkout: %d", response.StatusCode)
	}
	var directory admin.PaymentOperationPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?q="+checkoutPayment.String(), nil, &directory)
	if response.StatusCode != http.StatusOK || len(directory.Items) != 1 || !directory.Items[0].CanCheckCheckout {
		t.Fatalf("missing checkout recovery: %#v", directory)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "check_checkout", "expectedVersion": 2}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("stale checkout check: %d", response.StatusCode)
	}
	var checked admin.PaymentOperation
	response = requestJSON(t, adminClient, http.MethodPost, checkoutPath, input, &checked)
	if response.StatusCode != http.StatusOK || checked.Status != "checkout_open" || checked.Version != 2 || checked.CanCheckCheckout || checked.Job == nil || checked.Job.Kind != payments.ProductCheckoutCheckJobKind {
		t.Fatalf("checkout recovery mutated money/state: %d %#v", response.StatusCode, checked)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "check_checkout", "expectedVersion": 2}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate checkout job: %d", response.StatusCode)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',last_error_code='payment_timeout' WHERE id=$1`, checked.Job.ID); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "check_checkout", "expectedVersion": 2}, &checked)
	if response.StatusCode != http.StatusOK || checked.Version != 3 || checked.Job == nil {
		t.Fatalf("exhausted checkout cannot recover: %d %#v", response.StatusCode, checked)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, checked.Job.ID); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"waffo_pancake", "stripe"} {
		if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider=$2,checkout_expires_at=CASE WHEN $2='stripe' THEN now()+interval '1 hour' ELSE now()-interval '1 hour' END WHERE id=$1`, checkoutPayment, provider); err != nil {
			t.Fatal(err)
		}
		if response := requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "check_checkout", "expectedVersion": 3}, nil); response.StatusCode != http.StatusConflict {
			t.Fatalf("unsupported or unexpired checkout recovery: %s %d", provider, response.StatusCode)
		}
	}

	// Legacy evidence must be recovered before the checkout or refund reader is
	// advertised. The API only queues a read, so the unreachable provider is safe.
	if _, err := pool.Exec(ctx, `CREATE TABLE original_checkout_fixture AS SELECT * FROM product_checkout_requests; TRUNCATE product_checkout_dispatches, product_checkout_requests`); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?q="+checkoutPayment.String(), nil, &directory)
	if response.StatusCode != http.StatusOK || len(directory.Items) != 1 || !directory.Items[0].CanVerifyIdentity || directory.Items[0].CanCheckCheckout || directory.Items[0].AttentionCode != "identity_verification_required" {
		t.Fatalf("legacy identity projection: %#v", directory)
	}
	identityInput := map[string]any{"action": "verify_identity", "expectedVersion": 3}
	if response := requestJSON(t, memberClient, http.MethodPost, checkoutPath, identityInput, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("member identity recovery: %d", response.StatusCode)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "verify_identity", "expectedVersion": 4}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("stale identity recovery: %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodPost, checkoutPath, identityInput, &checked)
	if response.StatusCode != http.StatusOK || checked.Status != "checkout_open" || checked.Version != 4 || checked.CanVerifyIdentity || checked.Job == nil || checked.Job.Kind != payments.ProductIdentityRecoveryJobKind {
		t.Fatalf("identity recovery changed funds or failed: %d %#v", response.StatusCode, checked)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "verify_identity", "expectedVersion": 4}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate identity recovery: %d", response.StatusCode)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',last_error_code='payment_authentication' WHERE id=$1`, checked.Job.ID); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "verify_identity", "expectedVersion": 4}, &checked)
	if response.StatusCode != http.StatusOK || checked.Version != 5 {
		t.Fatalf("identity query cannot recover after credential correction: %d", response.StatusCode)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='payment.identity_verification_requested'`, checkoutPayment).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("identity audit missing %d %v", auditCount, err)
	}

	// A pending command outside Stripe's safe replay period remains actionable
	// even though it already has an original merchant/request snapshot.
	if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_requests(payment_id,identity,request,created_at)
 SELECT payment_id,identity,request,now()-interval '24 hours' FROM original_checkout_fixture WHERE payment_id=$1`, checkoutPayment); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='checkout_pending' WHERE id=$1`, checkoutPayment); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?q="+checkoutPayment.String()+"&attention=needs_attention", nil, &directory)
	if response.StatusCode != http.StatusOK || len(directory.Items) != 1 || directory.Items[0].AttentionCode != "checkout_reconciliation_required" || directory.Items[0].CanVerifyIdentity {
		t.Fatalf("expired replay hidden from operations: %#v", directory)
	}

	// Missing-session recovery is a finance-only durable lookup. This API must
	// remain usable with the deliberately unreachable provider above.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE kind=$1 AND payload->>'paymentId'=$2`, payments.ProductIdentityRecoveryJobKind, checkoutPayment.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_checkout_id=NULL,checkout_url=NULL,checkout_expires_at=NULL WHERE id=$1`, checkoutPayment); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?q="+checkoutPayment.String(), nil, &directory)
	if response.StatusCode != http.StatusOK || len(directory.Items) != 1 || !directory.Items[0].CanLocateCheckout {
		t.Fatalf("missing locate capability: %#v", directory)
	}
	locateInput := map[string]any{"action": "locate_checkout", "expectedVersion": 5}
	if response := requestJSON(t, memberClient, http.MethodPost, checkoutPath, locateInput, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("non-finance lookup %d", response.StatusCode)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "locate_checkout", "expectedVersion": 4}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("stale locate version %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodPost, checkoutPath, locateInput, &checked)
	if response.StatusCode != http.StatusOK || checked.Status != "checkout_pending" || checked.Version != 6 || checked.CanLocateCheckout || checked.Job == nil || checked.Job.Kind != payments.ProductCheckoutLookupJobKind {
		t.Fatalf("locate changed funds or did not queue: %d %#v", response.StatusCode, checked)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "locate_checkout", "expectedVersion": 6}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate locate %d", response.StatusCode)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',last_error_code='payment_timeout' WHERE id=$1`, checked.Job.ID); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "locate_checkout", "expectedVersion": 6}, &checked)
	if response.StatusCode != http.StatusOK || checked.Version != 7 || checked.Job == nil {
		t.Fatalf("failed lookup cannot retry: %d %#v", response.StatusCode, checked)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='payment.checkout_lookup_requested'`, checkoutPayment).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("missing lookup audit %d %v", auditCount, err)
	}
	var actorID string
	if err := pool.QueryRow(ctx, `SELECT payload->>'actorId' FROM jobs WHERE id=$1`, checked.Job.ID).Scan(&actorID); err != nil || actorID != administrator.ID.String() {
		t.Fatalf("lookup actor binding %s %v", actorID, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_lookups(job_id,payment_id,requested_by,outcome,searched_after,searched_before,result)
     VALUES($1,$2,$3,'ambiguous',now()-interval '1 day',now(),'{"outcome":"ambiguous","pages":1,"scanned":2,"matches":["cs_http_first","cs_http_second"]}')`, checked.Job.ID, checkoutPayment, administrator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',last_error_code='payment_reconciliation_required' WHERE id=$1`, checked.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='paid',provider_payment_id='pi_http_lookup_paid' WHERE id=$1`, checkoutPayment); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?q="+checkoutPayment.String()+"&attention=needs_attention", nil, &directory)
	if response.StatusCode != http.StatusOK || len(directory.Items) != 1 {
		t.Fatalf("paid ambiguous lookup hidden: %d %#v", response.StatusCode, directory)
	}
	item := directory.Items[0]
	if item.AttentionCode != "checkout_reconciliation_required" || item.CanLocateCheckout || item.CheckoutLookupOutcome == nil || *item.CheckoutLookupOutcome != "ambiguous" || len(item.CheckoutLookupMatches) != 2 || item.CheckoutLookupAt == nil {
		t.Fatalf("missing persistent lookup evidence %#v", item)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "locate_checkout", "expectedVersion": 7}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("paid lookup requeued %d", response.StatusCode)
	}
	// Finding one expired candidate later must not erase the older ambiguity.
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='checkout_open',provider_checkout_id='cs_http_first',provider_payment_id=NULL,checkout_url=NULL,checkout_expires_at=now()+interval '1 hour' WHERE id=$1`, checkoutPayment); err != nil {
		t.Fatal(err)
	}
	var foundJob uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status,max_attempts) VALUES($1,jsonb_build_object('paymentId',$2::text,'actorId',$3::text),'succeeded',20) RETURNING id`, payments.ProductCheckoutLookupJobKind, checkoutPayment, administrator.ID).Scan(&foundJob); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_lookups(job_id,payment_id,requested_by,outcome,searched_after,searched_before,result)
      VALUES($1,$2,$3,'found',now()-interval '1 day',now(),'{"outcome":"found","pages":1,"scanned":1,"matches":["cs_http_first"],"observation":{"providerCheckoutId":"cs_http_first","status":"expired","paymentStatus":"unpaid"}}')`, foundJob, checkoutPayment, administrator.ID); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?q="+checkoutPayment.String(), nil, &directory)
	if response.StatusCode != http.StatusOK || len(directory.Items) != 1 || !directory.Items[0].CanCheckCheckout || directory.Items[0].AttentionCode != "checkout_reconciliation_required" {
		t.Fatalf("terminal recovered checkout cannot be rechecked: %#v", directory)
	}
	response = requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "check_checkout", "expectedVersion": 7}, &checked)
	if response.StatusCode != http.StatusOK || checked.Version != 8 || checked.Job == nil || checked.Job.Kind != payments.ProductCheckoutCheckJobKind {
		t.Fatalf("terminal query recovery failed: %d %#v", response.StatusCode, checked)
	}

	for _, scenario := range []string{"unsupported_provider", "missing_snapshot", "future_snapshot"} {
		t.Run("locate_"+scenario, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='checkout_pending',provider_checkout_id=NULL,checkout_url=NULL,checkout_expires_at=NULL,provider='stripe' WHERE id=$1`, checkoutPayment); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "unsupported_provider":
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider='waffo_pancake' WHERE id=$1`, checkoutPayment); err != nil {
					t.Fatal(err)
				}
			case "missing_snapshot":
				if _, err := pool.Exec(ctx, `TRUNCATE product_checkout_dispatches, product_checkout_requests`); err != nil {
					t.Fatal(err)
				}
			case "future_snapshot":
				if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_requests(payment_id,identity,request,created_at) SELECT payment_id,identity,request,now()+interval '1 hour' FROM original_checkout_fixture WHERE payment_id=$1`, checkoutPayment); err != nil {
					t.Fatal(err)
				}
			}
			response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/payments?q="+checkoutPayment.String(), nil, &directory)
			if response.StatusCode != http.StatusOK || len(directory.Items) != 1 || directory.Items[0].CanLocateCheckout {
				t.Fatalf("invalid locate offered: %#v", directory)
			}
			if response := requestJSON(t, adminClient, http.MethodPost, checkoutPath, map[string]any{"action": "locate_checkout", "expectedVersion": 8}, nil); response.StatusCode != http.StatusConflict {
				t.Fatalf("invalid locate accepted %d", response.StatusCode)
			}
		})
	}

}
