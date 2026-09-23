package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

type settlementResponseRuntime struct {
	*productSettlementRuntime
	stripe *StripeRuntime
}

func (r *settlementResponseRuntime) CreateTransfer(ctx context.Context, input TransferRequest) (Transfer, error) {
	return r.stripe.CreateTransfer(ctx, input)
}

func TestProductSettlementStripeResponseEvidence(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong_charge", "wrong_mode", "missing_metadata", "partial_reversal", "full_reversal"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			runtime := &productSettlementRuntime{}
			service, checkout, job, settlement := productSettlementFixture(t, pool, runtime)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/v1/transfers" {
					t.Errorf("unexpected transfer request: %s %s", r.Method, r.URL.Path)
				}
				assertStripeHeaders(t, r, "transfer-"+checkout.PaymentID.String())
				if err := r.ParseForm(); err != nil {
					t.Error(err)
					http.Error(w, "bad form", http.StatusBadRequest)
					return
				}
				amount, err := strconv.Atoi(r.PostForm.Get("amount"))
				if err != nil {
					t.Error(err)
				}
				input := TransferRequest{
					PaymentID:        uuid.MustParse(r.PostForm.Get("metadata[hcai_payment_id]")),
					ProviderChargeID: r.PostForm.Get("source_transaction"), DestinationID: r.PostForm.Get("destination"),
					AmountCents: amount, Currency: r.PostForm.Get("currency"),
				}
				response := stripeTransferResponse(input, false)
				switch scenario {
				case "wrong_charge":
					response["source_transaction"] = "ch_unrelated"
				case "wrong_mode":
					response["livemode"] = true
				case "missing_metadata":
					delete(response, "metadata")
				case "partial_reversal":
					response["amount_reversed"] = 1
				case "full_reversal":
					response["amount_reversed"], response["reversed"] = amount, true
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			service.runtimes = NewRuntimeCatalog(&settlementResponseRuntime{productSettlementRuntime: runtime, stripe: testStripeRuntime(server)})
			for range 2 {
				if err := service.HandleProductSettlementJob(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
			var status string
			var notifications, batches int
			var hasTransfer bool
			if err := pool.QueryRow(ctx, `SELECT status,provider_transfer_id IS NOT NULL,
			 (SELECT count(*) FROM notifications WHERE resource_id=$1 AND kind='marketplace.product_settlement_transferred'),
			 (SELECT count(*) FROM product_payout_batch_items WHERE settlement_id=$1)
			 FROM product_settlements WHERE id=$1`, settlement).Scan(&status, &hasTransfer, &notifications, &batches); err != nil {
				t.Fatal(err)
			}
			wantStatus, wantNotifications := "recovery_required", 0
			if scenario == "valid" {
				wantStatus, wantNotifications = "transferred", 1
			}
			if status != wantStatus || notifications != wantNotifications || hasTransfer != (scenario == "valid") || batches != 1 || requests.Load() != 1 {
				t.Fatalf("status=%s transfer=%v notifications=%d batches=%d requests=%d", status, hasTransfer, notifications, batches, requests.Load())
			}
			// An invalid response cannot erase the obligation created by a dispatch.
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if err := markProductSettlementRefundTx(ctx, tx, checkout.PaymentID); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			var recovery, net int
			if err := pool.QueryRow(ctx, `SELECT status,recovery_amount_cents,net_amount_cents FROM product_settlements WHERE id=$1`, settlement).Scan(&status, &recovery, &net); err != nil {
				t.Fatal(err)
			}
			if status != "recovery_required" || recovery != net || net <= 0 {
				t.Fatalf("refund lost obligation: status=%s recovery=%d net=%d", status, recovery, net)
			}
		})
	}
}
