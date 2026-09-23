package payments

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Older tests intentionally have exactly one merchant/environment. Assert
// that precondition rather than silently summing the new account response.
func singleSellerFunds(t *testing.T, service *Service, ctx context.Context, seller uuid.UUID) (SellerFundsAccount, error) {
	t.Helper()
	out, err := service.GetSellerFunds(ctx, seller)
	if err != nil {
		return SellerFundsAccount{}, err
	}
	if out.UnresolvedRecords != 0 || len(out.Accounts) != 1 {
		t.Fatalf("expected one classified financial account: %+v", out)
	}
	return out.Accounts[0], nil
}

// Local provider only: even live-mode fixtures never contact a real gateway.
type scopeCatalogRuntime struct {
	sellerCatalogRuntime
	identity ProductCheckoutIdentity
}

func (r *scopeCatalogRuntime) ProductCheckoutIdentity(context.Context) (ProductCheckoutIdentity, error) {
	return r.identity, nil
}
func (r *scopeCatalogRuntime) CreateCheckout(ctx context.Context, in CheckoutRequest) (CheckoutSession, error) {
	out, err := r.sellerCatalogRuntime.CreateCheckout(ctx, in)
	out.LiveMode = r.identity.LiveMode
	return out, err
}
func (r *scopeCatalogRuntime) LookupProductTransfer(_ context.Context, in TransferLookupRequest) (TransferLookupResult, error) {
	return observedSettlementTransfer(in), nil
}
func useScopeRuntime(t *testing.T, pool *pgxpool.Pool, s *Service, seller uuid.UUID, identity ProductCheckoutIdentity) {
	t.Helper()
	env := "test"
	if identity.LiveMode {
		env = "prod"
	}
	_, err := pool.Exec(t.Context(), `INSERT INTO payment_provider_configs(provider,enabled,environment,merchant_id,store_id)
 VALUES('stripe',true,$1,$2,$3) ON CONFLICT(provider) DO UPDATE SET enabled=true,environment=EXCLUDED.environment,merchant_id=EXCLUDED.merchant_id,store_id=EXCLUDED.store_id`, env, identity.MerchantID, identity.StoreID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `UPDATE payment_destinations SET original_merchant_id=$2,original_store_id=$3,original_live_mode=$4,original_endpoint=$5,original_api_version=$6,original_request_version=$7 WHERE user_id=$1 AND provider='stripe'`, seller, identity.MerchantID, identity.StoreID, identity.LiveMode, identity.Endpoint, identity.APIVersion, identity.RequestVersion)
	if err != nil {
		t.Fatal(err)
	}
	s.config.LiveMode = identity.LiveMode
	s.runtimes = NewRuntimeCatalog(&scopeCatalogRuntime{identity: identity})
}

func TestSellerFundsScopesSeparateBalancesAndRecovery(t *testing.T) {
	for _, scenario := range []string{"same", "merchant", "mode", "store", "endpoint"} {
		t.Run(scenario, func(t *testing.T) {
			pool, s, _, first := sellerBankFixture(t)
			ctx := t.Context()
			original, err := s.GetSellerFunds(ctx, first.SellerID)
			if err != nil || len(original.Accounts) != 1 {
				t.Fatalf("first scope %+v %v", original, err)
			}
			firstAccount := original.Accounts[0].AccountID
			var firstSettlement uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT settlement_id FROM seller_payout_request_allocations WHERE payout_request_id=$1`, first.ID).Scan(&firstSettlement); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CancelSellerPayoutRequest(ctx, first.SellerID, first.ID); err != nil {
				t.Fatal(err)
			}
			identity, _ := (&productCheckoutRuntime{}).ProductCheckoutIdentity(ctx)
			switch scenario {
			case "merchant":
				identity.MerchantID = "acct_othermerchant"
			case "mode":
				identity.LiveMode = true
			case "store":
				identity.StoreID = "store_other"
			case "endpoint":
				identity.Endpoint = "https://another.example.test/v1"
			}
			useScopeRuntime(t, pool, s, first.SellerID, identity)
			second := addEqualSellerSettlement(t, pool, s, first.SellerID)
			if _, err := pool.Exec(ctx, `INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status) VALUES($1,$2,1,1,'USD','open')`, first.SellerID, firstSettlement); err != nil {
				t.Fatal(err)
			}
			balance, err := s.GetSellerFunds(ctx, first.SellerID)
			want := 2
			if scenario == "same" {
				want = 1
			}
			if err != nil || balance.UnresolvedRecords != 0 || len(balance.Accounts) != want {
				t.Fatalf("scopes %+v %v", balance, err)
			}
			for _, account := range balance.Accounts {
				if len(account.AccountID) != 64 {
					t.Fatal("missing opaque scope ID")
				}
				if account.AccountID == firstAccount {
					if account.RecoveryDueCents != 1 || account.WithdrawableCents != 0 {
						t.Fatalf("original debt disappeared: %+v", account)
					}
				} else if account.RecoveryDueCents != 0 || account.AvailableCents != first.AmountCents || account.WithdrawableCents != first.AmountCents {
					t.Fatalf("cross-scope balance/debt leak: %+v", account)
				}
			}
			body, _ := json.Marshal(balance)
			if strings.Contains(string(body), identity.MerchantID) || strings.Contains(string(body), identity.Endpoint) {
				t.Fatal("internal merchant identity leaked")
			}
			choices, err := s.SellerPayoutOptions(ctx, first.SellerID, "", 20)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			automaticErr := validateAutomaticSellerFundsTx(ctx, tx, second, first.SellerID)
			_ = tx.Rollback(ctx)
			if (scenario == "same" && !errors.Is(automaticErr, ErrCheckoutReconciliation)) || (scenario != "same" && automaticErr != nil) {
				t.Fatalf("automatic transfer scope guard: %v", automaticErr)
			}
			next, err := s.CreateSellerPayoutRequestForSettlement(ctx, first.SellerID, second, first.AmountCents, "scoped-reservation")
			if scenario == "same" {
				if !errors.Is(err, ErrSellerFundsRecoveryDue) || len(choices.Items) != 0 {
					t.Fatalf("same-scope debt bypassed: %v %+v", err, choices)
				}
				return
			}
			if err != nil || len(choices.Items) != 1 || choices.Items[0].SettlementID != second {
				t.Fatalf("unrelated scope blocked: %v %+v", err, choices)
			}
			// Exercise allocation, bank, review, source and admission SQL guards too.
			actor, input := approveSellerFundingFixture(t, pool, s, next)
			admitted, err := s.AdmitSellerPayoutFunding(ctx, actor, next.ID, input, "scoped-funding-admission", "test")
			if err != nil {
				t.Fatalf("other scope debt blocked source admission: %v", err)
			}
			job := jobs.Job{ID: admitted.JobID, Kind: SellerPayoutFundingJobKind}
			if err := pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, job.ID).Scan(&job.Payload); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := s.HandleSellerPayoutFundingJob(ctx, job); err != nil {
					t.Fatalf("unrelated debt blocked source worker: %v", err)
				}
			}
			assertSellerFunding(t, pool, next, "succeeded", "processing")
			current, err := s.GetSellerFunds(ctx, first.SellerID)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range current.Accounts {
				if a.AccountID != firstAccount && (a.ReservedCents != first.AmountCents || a.WithdrawableCents != 0) {
					t.Fatalf("reservation leaked to wrong scope: %+v", a)
				}
			}
			other, err := s.GetSellerFunds(ctx, uuid.New())
			if err != nil || len(other.Accounts) != 0 || other.UnresolvedRecords != 0 {
				t.Fatal("other seller can read scope balances", err)
			}
		})
	}
}

func TestSellerFundsScopesUnknownEvidenceAndDowngrade(t *testing.T) {
	pool, s, _, request := sellerBankFixture(t)
	ctx := t.Context()
	if _, err := s.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID); err != nil {
		t.Fatal(err)
	}
	// Historical unclassified adjustment cannot silently increase available money.
	if _, err := pool.Exec(ctx, `INSERT INTO seller_ledger_entries(seller_id,entry_type,amount_cents,currency,idempotency_key) VALUES($1,'adjustment',999,'USD','legacy-unscoped-adjustment')`, request.SellerID); err != nil {
		t.Fatal(err)
	}
	funds, err := s.GetSellerFunds(ctx, request.SellerID)
	if err != nil || funds.UnresolvedRecords != 1 || len(funds.Accounts) != 1 || funds.Accounts[0].WithdrawableCents != 0 || funds.Accounts[0].AvailableCents != request.AmountCents {
		t.Fatalf("unclassified evidence not isolated: %+v %v", funds, err)
	}
	var settlement uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT settlement_id FROM seller_payout_request_allocations WHERE payout_request_id=$1`, request.ID).Scan(&settlement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSellerPayoutRequestForSettlement(ctx, request.SellerID, settlement, request.AmountCents, "unknown-scope-request"); !errors.Is(err, ErrSellerFundsReconciliation) {
		t.Fatalf("unknown money funded payout: %v", err)
	}
	body, err := os.ReadFile("../platform/database/migrations/0164_seller_funds_scopes.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, string(body))
	requirePayoutConstraint(t, err)
	_ = tx.Rollback(ctx)
}

// Corrupt/legacy evidence is installed only in this test's disposable schema.
// No immutable production receipt is edited and no remote operation is sent.
func TestSellerFundsScopesRejectUnboundEvidence(t *testing.T) {
	for _, scenario := range []string{"missing", "wrong_buyer", "wrong_currency", "wrong_charge", "invalid_original_no_fallback"} {
		t.Run(scenario, func(t *testing.T) {
			pool, s, _, request := sellerBankFixture(t)
			ctx := t.Context()
			var payment uuid.UUID
			var originalIdentity, originalRequest []byte
			if err := pool.QueryRow(ctx, `SELECT ps.payment_id,r.identity,r.request FROM seller_payout_request_allocations a JOIN product_settlements ps ON ps.id=a.settlement_id JOIN product_checkout_requests r ON r.payment_id=ps.payment_id WHERE a.payout_request_id=$1`, request.ID).Scan(&payment, &originalIdentity, &originalRequest); err != nil {
				t.Fatal(err)
			}
			binding, err := readProductPaymentBinding(ctx, pool, payment, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `TRUNCATE product_checkout_dispatches,product_checkout_requests`); err != nil {
				t.Fatal(err)
			}
			if scenario != "missing" {
				switch scenario {
				case "wrong_buyer":
					binding.BuyerID = uuid.New()
				case "wrong_currency":
					binding.Currency = "EUR"
				case "wrong_charge":
					binding.ProviderChargeID = "ch_wrongscope"
				}
				body, _ := json.Marshal(binding)
				job := identityRecoveryJob(t, pool, payment, request.SellerID)
				if _, err := pool.Exec(ctx, `INSERT INTO product_payment_identity_recoveries(payment_id,job_id,requested_by,identity,binding,observation) VALUES($1,$2,$3,$4,$5,'{}')`, payment, job.ID, request.SellerID, originalIdentity, body); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "invalid_original_no_fallback" {
				if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_requests(payment_id,identity,request) VALUES($1,$2,jsonb_set($3::jsonb,'{BuyerIdentity}',to_jsonb($4::text)))`, payment, originalIdentity, originalRequest, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			}
			funds, err := s.GetSellerFunds(ctx, request.SellerID)
			if err != nil || funds.UnresolvedRecords != 4 || len(funds.Accounts) != 0 {
				t.Fatalf("unbound funds classified or counted twice: %+v %v", funds, err)
			}
			// Preserve cancellation as a safe exit even while classification is unknown.
			if _, err := s.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSellerFundsScopesWaffoReceipt(t *testing.T) {
	f := newWaffoRefundFixture(t, "await_payment")
	f.event(t, "order.completed", uuid.Nil)
	var seller uuid.UUID
	var amount int
	if err := f.pool.QueryRow(t.Context(), `SELECT seller_id,net_amount_cents FROM product_settlements WHERE payment_id=$1`, f.checkout.PaymentID).Scan(&seller, &amount); err != nil {
		t.Fatal(err)
	}
	funds, err := f.service.GetSellerFunds(t.Context(), seller)
	if err != nil || funds.UnresolvedRecords != 0 || len(funds.Accounts) != 1 {
		t.Fatalf("waffo funds %+v %v", funds, err)
	}
	a := funds.Accounts[0]
	if a.Provider != "waffo_pancake" || a.Environment != "test" || a.Currency != "USD" || a.PendingCents != amount || a.AvailableCents != 0 || a.WithdrawableCents != 0 {
		t.Fatalf("wrong waffo account: %+v", a)
	}
}

func TestSellerFundsScopesProviderSeparation(t *testing.T) {
	f := newWaffoRefundFixture(t, "await_payment")
	f.event(t, "order.completed", uuid.Nil)
	ctx := t.Context()
	var seller, waffoSettlement uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id,seller_id FROM product_settlements WHERE payment_id=$1`, f.checkout.PaymentID).Scan(&waffoSettlement, &seller); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE product_settlement_settings SET hold_days=0,payout_mode='seller_payout'; UPDATE licenses SET refund_window_days=0; UPDATE payment_provider_configs SET enabled=false WHERE provider='waffo_pancake'`); err != nil {
		t.Fatal(err)
	}
	identity, _ := (&productCheckoutRuntime{}).ProductCheckoutIdentity(ctx)
	if _, err := f.pool.Exec(ctx, `INSERT INTO payment_destinations(provider,user_id,destination_id,status,charges_enabled,payouts_enabled,details_submitted,original_merchant_id,original_store_id,original_live_mode,original_endpoint,original_api_version,original_request_version,verified_at) VALUES('stripe',$1,'acct_scopes','verified',true,true,true,$2,$3,$4,$5,$6,$7,now())`, seller, identity.MerchantID, identity.StoreID, identity.LiveMode, identity.Endpoint, identity.APIVersion, identity.RequestVersion); err != nil {
		t.Fatal(err)
	}
	s := newPaymentTestService(t, f.pool, ServiceConfig{Enabled: true, Provider: "stripe", APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog())
	useScopeRuntime(t, f.pool, s, seller, identity)
	stripeSettlement := addEqualSellerSettlement(t, f.pool, s, seller)
	if _, err := f.pool.Exec(ctx, `INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status) VALUES($1,$2,1,1,'USD','open')`, seller, waffoSettlement); err != nil {
		t.Fatal(err)
	}
	funds, err := s.GetSellerFunds(ctx, seller)
	if err != nil || len(funds.Accounts) != 2 || funds.UnresolvedRecords != 0 {
		t.Fatalf("provider accounts: %+v %v", funds, err)
	}
	for _, a := range funds.Accounts {
		if a.Provider == "waffo_pancake" && (a.RecoveryDueCents != 1 || a.WithdrawableCents != 0) {
			t.Fatalf("waffo debt missing: %+v", a)
		}
		if a.Provider == "stripe" {
			if a.RecoveryDueCents != 0 || a.AvailableCents <= 0 || a.WithdrawableCents != a.AvailableCents {
				t.Fatalf("waffo debt leaked to Stripe: %+v", a)
			}
			if _, err := s.CreateSellerPayoutRequestForSettlement(ctx, seller, stripeSettlement, a.WithdrawableCents, "other-provider-reservation"); err != nil {
				t.Fatalf("other provider blocked Stripe: %v", err)
			}
		}
	}
}
