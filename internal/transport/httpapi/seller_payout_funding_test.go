package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestSellerFundingHTTPAdmissionAndBoundary(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	t.Cleanup(cleanup)
	ctx := t.Context()
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		http.Error(w, "Admission must only enqueue local work", 500)
	}))
	t.Cleanup(provider.Close)
	cfg := config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
		StripeEnabled: true, StripeSecretKey: "sk_test_funding_http_fixture", StripeWebhookSecret: "whsec_funding_fixture",
		StripeBaseURL: provider.URL + "/v1", StripeAPIVersion: "2026-02-25.clover", StripeWebhookToleranceSeconds: 300}
	server := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	sellerClient, buyerClient, financeClient, guest := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	seller := registerGovernanceUser(t, sellerClient, server.URL, "funding_seller")
	buyer := registerGovernanceUser(t, buyerClient, server.URL, "funding_buyer")
	finance := registerGovernanceUser(t, financeClient, server.URL, "funding_finance")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE users SET role='admin' WHERE id=$1`, finance.ID)
	source, product, order, payment, settlement := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	// Precondition: an already-paid sale with original financial identity. No
	// trigger is disabled and no external payment or bank verification is faked
	// through the API under test; the frozen verified bank is fixture evidence.
	exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','Original','/media/original.txt','text/plain','clean','upload','hcai-commercial-standard-v1','local_file','funding-original.txt')`, source, seller.ID)
	exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Funding source','Funding HTTP fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller.ID, source)
	exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot)
 VALUES($1,$2,$3,1900,'USD','fulfilled',now(),$1::uuid::text,'1.0','Accepted terms','Accepted title','Accepted license',0)`, order, buyer.ID, product)
	exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,paid_at,provider_charge_id)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','paid',false,$1::uuid::text,now(),'ch_funding_original')`, payment, buyer.ID, seller.ID, product, order)
	exec(`INSERT INTO product_checkout_requests(payment_id,identity,request)
 SELECT id,jsonb_build_object('provider','stripe','merchantId','acct_original','liveMode',false,'endpoint',$2::text,'apiVersion','2026-02-25.clover','requestVersion','stripe-product-checkout-v1'),
 jsonb_build_object('PaymentID',id,'Purpose','product','ResourceID',resource_id,'OrderExternalID',order_id,'BuyerIdentity',payer_id,'AmountCents',amount_cents,'Currency',currency)
 FROM payment_intents WHERE id=$1`, payment, cfg.StripeBaseURL)
	exec(`INSERT INTO product_settlements(id,order_id,payment_id,seller_id,provider,live_mode,gross_amount_cents,fee_bps,fee_cents,net_amount_cents,currency,status,available_at,transfer_idempotency_key)
 VALUES($1,$2,$3,$4,'stripe',false,1900,250,48,1852,'USD','available',now()-interval '1 minute',$1::uuid::text)`, settlement, order, payment, seller.ID)
	exec(`INSERT INTO payment_destinations(provider,user_id,destination_id,status,charges_enabled,payouts_enabled,details_submitted,original_merchant_id,original_live_mode,original_endpoint,original_api_version,original_request_version,verified_at)
 VALUES('stripe',$1,'acct_seller_original','verified',true,true,true,'acct_original',false,$2,'2026-02-25.clover','stripe-product-checkout-v1',now())`, seller.ID, cfg.StripeBaseURL)
	exec(`UPDATE product_settlement_settings SET payout_mode='seller_payout' WHERE singleton=true`)
	if err := payments.NewService(pool, payments.ServiceConfig{Enabled: true}).ProjectSellerSettlementCredit(ctx, settlement); err != nil {
		t.Fatal(err)
	}
	var payout payments.SellerPayoutRequest
	if r := requestPaymentJSON(t, sellerClient, http.MethodPost, server.URL+"/api/v1/seller/payout-requests", "http-seller-reserve", map[string]any{"settlementId": settlement, "amountCents": 1852}, &payout); r.StatusCode != 201 {
		t.Fatalf("request status=%d %+v", r.StatusCode, payout)
	}
	exec(`INSERT INTO seller_payout_bank_targets(payout_request_id,seller_id,settlement_id,payment_id,provider_identity,destination_id,bank_destination_id,amount_cents,currency,observed_at,bank_name,last4)
 SELECT $1,$2,$3,$4,identity,'acct_seller_original','ba_frozen_original',1852,'USD',clock_timestamp(),'Frozen HTTP Bank','6789' FROM product_checkout_requests WHERE payment_id=$4`, payout.ID, seller.ID, settlement, payment)
	base := server.URL + "/api/v1/admin/seller-payout-requests/" + payout.ID.String()
	var detail payments.SellerPayoutReviewItem
	if r := requestJSON(t, financeClient, http.MethodGet, base, nil, &detail); r.StatusCode != 200 || detail.CanAdmitFunding || detail.Funding != nil {
		t.Fatalf("unapproved eligibility %+v status=%d", detail, r.StatusCode)
	}
	reviewInput := payments.SellerPayoutReviewInput{SettlementID: settlement, AmountCents: 1852, BankDestinationID: "ba_frozen_original", Decision: "approved", Reason: "Verified the original payment and bank.", SellerMessage: "Your request was approved; no bank payout was sent."}
	var reviewed payments.SellerPayoutReviewResult
	if r := requestPaymentJSON(t, financeClient, http.MethodPost, base+"/review", "http-review-original", reviewInput, &reviewed); r.StatusCode != 200 || !reviewed.Request.CanAdmitFunding {
		t.Fatalf("review status=%d %+v", r.StatusCode, reviewed)
	}
	count := func(want int) {
		t.Helper()
		var sources, admissions, jobs, audits int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_payout_transfers),(SELECT count(*) FROM seller_payout_funding_admissions),(SELECT count(*) FROM jobs WHERE kind='payment.fund_seller_payout'),(SELECT count(*) FROM audit_events WHERE action='seller_payout.funding_admitted')`).Scan(&sources, &admissions, &jobs, &audits); err != nil || sources != want || admissions != want || jobs != want || audits != want {
			t.Fatalf("atomic admission: %d %d %d %d want %d: %v", sources, admissions, jobs, audits, want, err)
		}
	}
	count(0) // Approval alone must never create funding work.
	input := payments.SellerFundingAdmissionInput{ReviewID: reviewed.Review.ID, ExpectedRevision: 1, SettlementID: settlement, AmountCents: 1852, BankDestinationID: "ba_frozen_original", Reason: "Explicitly authorize this original source transfer.", Confirmed: true}
	raw, _ := json.Marshal(input)
	call := func(client *http.Client, path, body string, keys []string, want int) []byte {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		for _, key := range keys {
			req.Header.Add("Idempotency-Key", key)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		bodyBytes, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want || res.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("funding status=%d want %d body=%s", res.StatusCode, want, bodyBytes)
		}
		return bodyBytes
	}
	path := base + "/funding"
	key := []string{"http-funding-original"}
	call(guest, path, string(raw), key, 401)
	call(buyerClient, path, string(raw), key, 403)
	exec(`UPDATE users SET role='admin' WHERE id=$1`, seller.ID)
	call(sellerClient, path, string(raw), key, 403) // Finance role cannot self-fund.
	for _, body := range []string{`{}`, `null`, string(raw) + ` {}`, strings.Replace(string(raw), `"confirmed":true`, `"confirmed":false`, 1), strings.Replace(string(raw), `"expectedRevision":1`, `"expectedRevision":0`, 1), strings.Replace(string(raw), `"amountCents":1852`, `"amountCents":0`, 1), strings.TrimSuffix(string(raw), "}") + `,"bankName":"spoofed"}`, strings.Repeat(" ", 16385) + string(raw)} {
		call(financeClient, path, body, key, 422)
	}
	for _, keys := range [][]string{nil, {"short"}, {"duplicate-key", "duplicate-key"}} {
		call(financeClient, path, string(raw), keys, 422)
	}
	call(financeClient, path+"?dispatch=true", string(raw), key, 422)
	changed := input
	changed.ExpectedRevision = 2
	wrong, _ := json.Marshal(changed)
	call(financeClient, path, string(wrong), key, 409)
	count(0)
	exec(`UPDATE payment_intents SET provider_charge_id=NULL WHERE id=$1`, payment)
	call(financeClient, path, string(raw), key, 409)
	count(0)
	exec(`UPDATE payment_intents SET provider_charge_id='ch_funding_original' WHERE id=$1`, payment)
	var first payments.SellerFundingAdmissionResult
	if err := json.Unmarshal(call(financeClient, path, string(raw), key, 200), &first); err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.JobID == uuid.Nil || first.Request.CanAdmitFunding || first.Request.Funding == nil || first.Request.Funding.TransferID != first.Admission.TransferID || first.Request.Funding.JobStatus == nil || *first.Request.Funding.JobStatus != "queued" {
		t.Fatalf("admission result %+v", first)
	}
	count(1)
	var replay payments.SellerFundingAdmissionResult
	if err := json.Unmarshal(call(financeClient, path, string(raw), key, 200), &replay); err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Admission.ID != first.Admission.ID || replay.JobID != first.JobID {
		t.Fatalf("replay %+v", replay)
	}
	call(financeClient, path, string(wrong), key, 409)
	call(financeClient, path, string(raw), []string{"http-funding-another"}, 409)
	if r := requestJSON(t, financeClient, http.MethodGet, base, nil, &detail); r.StatusCode != 200 || detail.CanAdmitFunding || detail.Funding == nil || detail.Funding.AdmissionID == nil || *detail.Funding.AdmissionID != first.Admission.ID || detail.Status != "under_review" {
		t.Fatalf("current detail %+v status=%d", detail, r.StatusCode)
	}
	exec(`UPDATE users SET role='member' WHERE id=$1`, finance.ID)
	call(financeClient, path, string(raw), key, 403)
	count(1)
	if providerCalls.Load() != 0 {
		t.Fatalf("HTTP admission sent %d provider requests", providerCalls.Load())
	}
}
