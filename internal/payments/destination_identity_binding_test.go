package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPayoutOnboardingRejectsBoundDestinationAfterMerchantSwitch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	user, destination := payoutOrderingOwner(t, pool)
	runtime := newUncertainAccountRuntime()
	identity := runtime.identity
	if _, err := pool.Exec(ctx, `UPDATE payment_destinations SET original_merchant_id=$2,original_store_id=$3,original_live_mode=$4,
		original_endpoint=$5,original_api_version=$6,original_request_version=$7 WHERE user_id=$1 AND destination_id=$8`,
		user, identity.MerchantID, identity.StoreID, identity.LiveMode, identity.Endpoint, identity.APIVersion, identity.RequestVersion, destination); err != nil {
		t.Fatal(err)
	}
	runtime.identity.MerchantID = "acct_switched"
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	if _, err := callPayoutOnboarding(service, user); !errors.Is(err, ErrPayoutReconciliation) {
		t.Fatalf("bound destination was reused after merchant switch: %v", err)
	}
	if runtime.linkCalls != 0 {
		t.Fatalf("account link was sent for mismatched merchant: %d", runtime.linkCalls)
	}
}

func TestProductSettlementRejectsUnknownDestinationBinding(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	runtime := &productSettlementRuntime{}
	service, _, job, settlement := productSettlementFixture(t, pool, runtime)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE payment_destinations SET original_merchant_id=NULL,original_store_id=NULL,original_live_mode=NULL,
		original_endpoint=NULL,original_api_version=NULL,original_request_version=NULL WHERE destination_id='acct_settlement'`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductSettlementJob(ctx, job); err == nil {
		t.Fatal("unknown destination binding was accepted")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM product_settlements WHERE id=$1`, settlement).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending_hold" {
		t.Fatalf("unknown destination changed settlement state: %s", status)
	}
}

func TestDestinationIdentityBindingColumnsRemainUserScoped(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	user := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Binding user','creator')`, user, user.String()+"@example.test", "binding_"+user.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_destinations(provider,user_id,destination_id,status,charges_enabled,payouts_enabled,details_submitted,
		original_merchant_id,original_live_mode,original_endpoint,original_api_version,original_request_version,verified_at)
		VALUES('stripe',$1,'acct_binding','verified',true,true,true,'acct_bound',false,'https://api.stripe.com/v1','2026-02-25.clover',$2,now())`, user, stripeProductCheckoutVersion); err != nil {
		t.Fatal(err)
	}
}
