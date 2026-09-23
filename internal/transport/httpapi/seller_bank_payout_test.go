package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sellerFundsHTTPFixture struct {
	pool                                            *pgxpool.Pool
	server                                          *httptest.Server
	sellerClient, buyerClient, financeClient, guest *http.Client
	seller, finance                                 identity.User
	payout                                          payments.SellerPayoutRequest
	reviewed                                        payments.SellerPayoutReviewResult
	funding                                         payments.SellerFundingAdmissionResult
	runtime                                         *bankHTTPSourceRuntime
	providerCalls                                   *atomic.Int32
	exec                                            func(string, ...any)
	base                                            string
}

func newSellerFundsHTTPFixture(t *testing.T) sellerFundsHTTPFixture {
	t.Helper()
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

	fundingInput := payments.SellerFundingAdmissionInput{ReviewID: reviewed.Review.ID, ExpectedRevision: 1, SettlementID: settlement, AmountCents: 1852, BankDestinationID: "ba_frozen_original", Reason: "Authorize the source before its bank payout.", Confirmed: true}
	var funding payments.SellerFundingAdmissionResult
	if r := requestPaymentJSON(t, financeClient, http.MethodPost, base+"/funding", "http-bank-source", fundingInput, &funding); r.StatusCode != 200 {
		t.Fatal("source admission", r.StatusCode, funding)
	}
	job := jobs.Job{ID: funding.JobID, Kind: payments.SellerPayoutFundingJobKind}
	if err := pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, job.ID).Scan(&job.Payload); err != nil {
		t.Fatal(err)
	}
	runtime := &bankHTTPSourceRuntime{identity: payments.ProductCheckoutIdentity{Provider: "stripe", MerchantID: "acct_original", LiveMode: false, Endpoint: cfg.StripeBaseURL, APIVersion: cfg.StripeAPIVersion, RequestVersion: "stripe-product-checkout-v1"}}
	service := payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{Enabled: true}, payments.NewRuntimeCatalog(runtime))
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal("source execution", err)
	}
	return sellerFundsHTTPFixture{pool: pool, server: server, sellerClient: sellerClient, buyerClient: buyerClient,
		financeClient: financeClient, guest: guest, seller: seller, finance: finance, payout: payout,
		reviewed: reviewed, funding: funding, runtime: runtime, providerCalls: &providerCalls, exec: exec, base: base}
}

func TestSellerBankHTTPConfirmationAndBoundary(t *testing.T) {
	f := newSellerFundsHTTPFixture(t)
	pool, server, ctx := f.pool, f.server, t.Context()
	sellerClient, buyerClient, financeClient, guest := f.sellerClient, f.buyerClient, f.financeClient, f.guest
	seller, finance, exec := f.seller, f.finance, f.exec
	payout, reviewed, funding, runtime, providerCalls := f.payout, f.reviewed, f.funding, f.runtime, f.providerCalls
	path := f.base + "/bank-payout"
	var view payments.SellerBankPayoutOperation
	if r := requestJSON(t, financeClient, http.MethodGet, path, nil, &view); r.StatusCode != 200 || !view.CanSubmit || view.Bank != nil {
		t.Fatal("bank operation read", r.StatusCode, view)
	}
	for _, entry := range []struct {
		client *http.Client
		status int
	}{{guest, 401}, {buyerClient, 403}} {
		if r := requestJSON(t, entry.client, http.MethodGet, path, nil, nil); r.StatusCode != entry.status {
			t.Fatal("private bank view", r.StatusCode)
		}
	}
	input := payments.SellerBankPayoutInput{SourceTransferID: funding.Admission.TransferID, ReviewID: reviewed.Review.ID, ExpectedRevision: 1, AmountCents: 1852, BankDestinationID: "ba_frozen_original", Reason: "Confirm the original source and frozen bank payout.", Confirmed: true}
	raw, _ := json.Marshal(input)
	call := func(client *http.Client, target, body string, keys []string, want int) []byte {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
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
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want || res.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("bank status=%d want=%d data=%s", res.StatusCode, want, data)
		}
		return data
	}
	keys := []string{"http-bank-confirmation"}
	call(guest, path, string(raw), keys, 401)
	call(buyerClient, path, string(raw), keys, 403)
	exec(`UPDATE users SET role='admin' WHERE id=$1`, seller.ID)
	call(sellerClient, path, string(raw), keys, 403)
	for _, body := range []string{`{}`, `null`, string(raw) + ` {}`, strings.Replace(string(raw), `"confirmed":true`, `"confirmed":false`, 1), strings.TrimSuffix(string(raw), "}") + `,"merchantId":"spoofed"}`, strings.Repeat(" ", 16385) + string(raw)} {
		call(financeClient, path, body, keys, 422)
	}
	for _, keySet := range [][]string{nil, {"short"}, {"duplicate-key", "duplicate-key"}} {
		call(financeClient, path, string(raw), keySet, 422)
	}
	call(financeClient, path+"?force=true", string(raw), keys, 422)
	changed := input
	changed.ExpectedRevision++
	wrong, _ := json.Marshal(changed)
	call(financeClient, path, string(wrong), keys, 409)
	var first payments.SellerBankPayoutSubmission
	if err := json.Unmarshal(call(financeClient, path, string(raw), keys, 200), &first); err != nil {
		t.Fatal(err)
	}
	if first.JobID == uuid.Nil || first.Replayed || first.Operation.Bank == nil || first.Operation.CanSubmit || first.Operation.Bank.JobStatus == nil || *first.Operation.Bank.JobStatus != "queued" {
		t.Fatal("bank submission", first)
	}
	var again payments.SellerBankPayoutSubmission
	if err := json.Unmarshal(call(financeClient, path, string(raw), keys, 200), &again); err != nil {
		t.Fatal(err)
	}
	if !again.Replayed || again.Command.ID != first.Command.ID || again.JobID != first.JobID {
		t.Fatal("bank replay", again)
	}
	call(financeClient, path, string(wrong), keys, 409)
	call(financeClient, path, string(raw), []string{"http-bank-another-key"}, 409)
	if r := requestJSON(t, financeClient, http.MethodGet, path, nil, &view); r.StatusCode != 200 || view.CanSubmit || view.Bank == nil || view.Bank.CommandID != first.Command.ID {
		t.Fatal("bank view after queue", r.StatusCode, view)
	}
	var commands, dispatches, bankJobs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_bank_payout_commands),(SELECT count(*) FROM seller_bank_payout_dispatches),(SELECT count(*) FROM jobs WHERE kind='payment.execute_seller_bank_payout')`).Scan(&commands, &dispatches, &bankJobs); err != nil || commands != 1 || dispatches != 1 || bankJobs != 1 {
		t.Fatal("atomic command", commands, dispatches, bankJobs, err)
	}
	// A stopped, unstarted execution requires a new explicit finance command.
	exec(`UPDATE jobs SET status='cancelled' WHERE id=$1`, first.JobID)
	if r := requestJSON(t, financeClient, http.MethodGet, path, nil, &view); r.StatusCode != 200 || !view.CanResume || view.CanSubmit {
		t.Fatal("stopped bank operation", r.StatusCode, view)
	}
	resumeInput := payments.SellerBankPayoutResumeInput{CommandID: first.Command.ID, ExpectedJobID: first.JobID, Confirmed: true, Reason: "Continue the verified stopped operation with its original bank."}
	resumeRaw, _ := json.Marshal(resumeInput)
	resumePath, resumeKeys := path+"/resume", []string{"http-bank-resume"}
	call(guest, resumePath, string(resumeRaw), resumeKeys, 401)
	call(buyerClient, resumePath, string(resumeRaw), resumeKeys, 403)
	call(sellerClient, resumePath, string(resumeRaw), resumeKeys, 403)
	for _, body := range []string{`{}`, `null`, string(resumeRaw) + ` {}`, strings.Replace(string(resumeRaw), `"confirmed":true`, `"confirmed":false`, 1), strings.TrimSuffix(string(resumeRaw), "}") + `,"amountCents":1}`, strings.Repeat(" ", 16385) + string(resumeRaw)} {
		call(financeClient, resumePath, body, resumeKeys, 422)
	}
	for _, keySet := range [][]string{nil, {"short"}, {"duplicate-key", "duplicate-key"}} {
		call(financeClient, resumePath, string(resumeRaw), keySet, 422)
	}
	call(financeClient, resumePath+"?force=true", string(resumeRaw), resumeKeys, 422)
	var resumed, resumeReplay payments.SellerBankPayoutResumeResult
	if err := json.Unmarshal(call(financeClient, resumePath, string(resumeRaw), resumeKeys, 200), &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Replayed || resumed.Resume.PredecessorJobID != first.JobID || resumed.Operation.CanResume || resumed.Operation.Bank.Resume == nil || resumed.Operation.Bank.Resume.JobStatus != "queued" {
		t.Fatal("resume did not retain original task", resumed)
	}
	if err := json.Unmarshal(call(financeClient, resumePath, string(resumeRaw), resumeKeys, 200), &resumeReplay); err != nil {
		t.Fatal(err)
	}
	if !resumeReplay.Replayed || resumeReplay.Resume != resumed.Resume {
		t.Fatal("resume replay", resumeReplay)
	}
	call(financeClient, resumePath, string(resumeRaw), []string{"http-bank-resume-new-key"}, 409)
	call(financeClient, resumePath, strings.Replace(string(resumeRaw), resumeInput.Reason, "A different recovery reason for the same key.", 1), resumeKeys, 409)
	// Advance the durable job through the real service, then read the seller
	// HTTP projection with its actual authenticated session. Only the provider
	// is simulated; the route, authorization, SQL projection and ledger are real.
	bankRuntime := &bankHTTPPayoutRuntime{bankHTTPSourceRuntime: runtime, status: "paid"}
	bankService := payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{Enabled: true}, payments.NewRuntimeCatalog(bankRuntime))
	bankJob := jobs.Job{ID: resumed.Resume.JobID, Kind: payments.SellerBankPayoutJobKind}
	if err := pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, bankJob.ID).Scan(&bankJob.Payload); err != nil {
		t.Fatal(err)
	}
	sellerPath := server.URL + "/api/v1/seller/payout-requests/" + payout.ID.String()
	for _, status := range []string{"paid", "failed"} {
		bankRuntime.status = status
		if err := bankService.HandleSellerBankPayoutJob(ctx, bankJob); err != nil {
			t.Fatal(err)
		}
		var detail payments.SellerPayoutItem
		res := requestJSON(t, sellerClient, http.MethodGet, sellerPath, nil, &detail)
		if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "private, no-store" || detail.BankPayout == nil || detail.BankPayout.Status != status || detail.BankPayout.RequiresReview != (status == "failed") {
			t.Fatal("seller bank result", res.StatusCode, detail)
		}
		var history payments.SellerPayoutPage
		if res := requestJSON(t, sellerClient, http.MethodGet, server.URL+"/api/v1/seller/payout-requests", nil, &history); res.StatusCode != 200 || len(history.Items) != 1 || history.Items[0].BankPayout == nil || history.Items[0].BankPayout.Status != status {
			t.Fatal("seller bank history", res.StatusCode, history)
		}
		for _, outsider := range []*http.Client{buyerClient, financeClient} {
			if res := requestJSON(t, outsider, http.MethodGet, sellerPath, nil, nil); res.StatusCode != 404 {
				t.Fatal("foreign bank result", res.StatusCode)
			}
		}
	}
	if bankRuntime.creates != 1 {
		t.Fatal("bank return caused another payout", bankRuntime.creates)
	}
	exec(`UPDATE users SET role='member' WHERE id=$1`, finance.ID)
	call(financeClient, path, string(raw), keys, 403)
	call(financeClient, resumePath, string(resumeRaw), resumeKeys, 403)
	if providerCalls.Load() != 0 {
		t.Fatal("bank HTTP command contacted external provider", providerCalls.Load())
	}
}

type bankHTTPSourceRuntime struct {
	payments.ProviderRuntime
	identity payments.ProductCheckoutIdentity
}

func (r *bankHTTPSourceRuntime) Provider() string { return "stripe" }
func (r *bankHTTPSourceRuntime) ProductCheckoutIdentity(context.Context) (payments.ProductCheckoutIdentity, error) {
	return r.identity, nil
}
func (r *bankHTTPSourceRuntime) CreateTransfer(_ context.Context, input payments.TransferRequest) (payments.Transfer, error) {
	return payments.Transfer{ProviderID: "tr_original_http", DestinationID: input.DestinationID, AmountCents: input.AmountCents, Currency: input.Currency, TransferGroup: "hcai_" + input.PaymentID.String()}, nil
}
func (r *bankHTTPSourceRuntime) LookupProductTransfer(context.Context, payments.TransferLookupRequest) (payments.TransferLookupResult, error) {
	return payments.TransferLookupResult{}, fmt.Errorf("unexpected source lookup")
}

// This runtime never contacts the network; HTTP handlers still use their normal
// configured runtime, whose unexpected network calls are counted above.
type bankHTTPPayoutRuntime struct {
	*bankHTTPSourceRuntime
	status  string
	creates int
}

func (r *bankHTTPPayoutRuntime) payout(input payments.PayoutRequest) payments.Payout {
	return payments.Payout{ProviderID: "po_http_bank_original", Destination: input.DestinationID, BankDestinationID: input.BankDestinationID, AmountCents: input.AmountCents, Currency: input.Currency, Status: r.status, CreatedAt: input.ReservedAt}
}
func (r *bankHTTPPayoutRuntime) CreatePayout(_ context.Context, input payments.PayoutRequest) (payments.Payout, error) {
	r.creates++
	return r.payout(input), nil
}
func (r *bankHTTPPayoutRuntime) ReadPayout(_ context.Context, input payments.PayoutRequest, _ string) (payments.Payout, error) {
	return r.payout(input), nil
}
func (r *bankHTTPPayoutRuntime) LookupPayout(_ context.Context, input payments.PayoutRequest) (payments.PayoutLookupResult, error) {
	return payments.PayoutLookupResult{Outcome: "found", Pages: 1, Observations: []payments.Payout{r.payout(input)}}, nil
}
