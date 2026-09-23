package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSellerPayoutCatalogOwnershipSelectionAndPagination(t *testing.T) {
	pool, service, runtime, original := sellerBankFixture(t)
	ctx := t.Context()
	var settlement uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT settlement_id FROM seller_payout_request_allocations WHERE payout_request_id=$1`, original.ID).Scan(&settlement); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListSellerPayoutRequests(ctx, original.SellerID, "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != original.ID || !page.Items[0].CanSelectBank || !page.Items[0].CanCancel || page.Items[0].Environment != "test" {
		t.Fatalf("history %+v %v", page, err)
	}
	foreign, err := service.ListSellerPayoutRequests(ctx, uuid.New(), "", 20)
	if err != nil || len(foreign.Items) != 0 {
		t.Fatalf("foreign history %+v %v", foreign, err)
	}
	if _, err := service.ListSellerPayoutRequests(ctx, uuid.New(), original.ID.String(), 20); !errors.Is(err, ErrSellerPayoutFilter) {
		t.Fatalf("foreign cursor: %v", err)
	}
	options, err := service.SellerPayoutOptions(ctx, original.SellerID, "", 20)
	if err != nil || len(options.Items) != 0 || options.Availability != "available" {
		t.Fatalf("allocated options %+v %v", options, err)
	}
	if _, err := service.CancelSellerPayoutRequest(ctx, original.SellerID, original.ID); err != nil {
		t.Fatal(err)
	}
	options, err = service.SellerPayoutOptions(ctx, original.SellerID, "", 20)
	if err != nil || len(options.Items) != 1 || options.Items[0].SettlementID != settlement || options.Items[0].AmountCents != original.AmountCents {
		t.Fatalf("released options %+v %v", options, err)
	}
	if _, err := service.SellerPayoutOptions(ctx, uuid.New(), settlement.String(), 20); !errors.Is(err, ErrSellerPayoutFilter) {
		t.Fatalf("foreign option cursor: %v", err)
	}
	if _, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, uuid.New(), original.AmountCents, "wrong-settlement-key"); !errors.Is(err, ErrSellerPayoutAllocation) {
		t.Fatalf("unrelated settlement reserved: %v", err)
	}
	if _, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, uuid.Nil, original.AmountCents, "nil-settlement-key"); !errors.Is(err, ErrInvalidSellerPayout) {
		t.Fatalf("nil settlement: %v", err)
	}
	created, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, settlement, original.AmountCents, "chosen-settlement-key")
	if err != nil {
		t.Fatal(err)
	}
	service.config.Enabled = false
	replay, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, settlement, original.AmountCents, "chosen-settlement-key")
	if err != nil || replay.ID != created.ID {
		t.Fatalf("replay %+v %v", replay, err)
	}
	if _, err := service.CreateSellerPayoutRequestForSettlement(ctx, original.SellerID, uuid.New(), original.AmountCents, "chosen-settlement-key"); !errors.Is(err, ErrSellerPayoutConflict) {
		t.Fatalf("changed settlement replay: %v", err)
	}
	page, err = service.ListSellerPayoutRequests(ctx, original.SellerID, "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != created.ID || page.NextCursor == "" || page.Items[0].CanSelectBank {
		t.Fatalf("first page %+v %v", page, err)
	}
	page, err = service.ListSellerPayoutRequests(ctx, original.SellerID, page.NextCursor, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != original.ID || page.NextCursor != "" || page.Items[0].CanCancel || page.Items[0].CanSelectBank {
		t.Fatalf("cancelled page %+v %v", page, err)
	}
	options, err = service.SellerPayoutOptions(ctx, original.SellerID, "", 20)
	if err != nil || options.Availability != "unavailable" {
		t.Fatalf("disabled options %+v %v", options, err)
	}
	service.config.Enabled = true
	if _, err := service.BindSellerPayoutBankTarget(ctx, original.SellerID, created.ID, "ba_original"); err != nil {
		t.Fatal(err)
	}
	page, err = service.ListSellerPayoutRequests(ctx, original.SellerID, "", 20)
	if err != nil || len(page.Items) != 2 || page.Items[0].BankTarget == nil || page.Items[0].CanSelectBank {
		t.Fatalf("bank projection %+v %v", page, err)
	}
	if runtime.transfers.Load() != 0 {
		t.Fatal("catalog dispatched money")
	}
}

type sellerBankDirectoryRuntime struct {
	sellerBankRuntime
	onList func(context.Context, PayoutBankTargetRequest) (PayoutBankDirectory, error)
}

func (r *sellerBankDirectoryRuntime) ListPayoutBanks(ctx context.Context, in PayoutBankTargetRequest) (PayoutBankDirectory, error) {
	r.reads.Add(1)
	return r.onList(ctx, in)
}

func TestSellerPayoutBankDirectoryRechecksState(t *testing.T) {
	for _, scenario := range []string{"valid", "cancel", "bind", "refund", "suspended", "duplicate", "stale", "future", "nil_items", "unsafe", "error"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, _, request := sellerBankFixture(t)
			runtime := &sellerBankDirectoryRuntime{}
			service.runtimes = NewRuntimeCatalog(runtime)
			runtime.onList = func(ctx context.Context, input PayoutBankTargetRequest) (PayoutBankDirectory, error) {
				if input.DestinationID != "acct_settlement" || input.BankDestinationID != "" {
					t.Fatalf("wrong directory scope %+v", input)
				}
				out := PayoutBankDirectory{Items: []PayoutBankOption{{BankDestinationID: "ba_original", BankName: "Example", Last4: "1234", Currency: "USD"}}, ObservedAt: time.Now()}
				var err error
				switch scenario {
				case "cancel":
					_, err = service.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID)
				case "bind":
					_, err = service.BindSellerPayoutBankTarget(ctx, request.SellerID, request.ID, "ba_original")
				case "refund":
					_, err = pool.Exec(ctx, `UPDATE product_settlements SET status='refund_hold' WHERE seller_id=$1`, request.SellerID)
				case "suspended":
					_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, request.SellerID)
				case "duplicate":
					out.Items = append(out.Items, out.Items[0])
				case "stale":
					out.ObservedAt = time.Now().Add(-time.Minute)
				case "future":
					out.ObservedAt = time.Now().Add(time.Minute)
				case "nil_items":
					out.Items = nil
				case "unsafe":
					out.Items[0].Last4 = "12345678"
				case "error":
					return PayoutBankDirectory{}, errors.New("provider unavailable")
				}
				if err != nil {
					t.Fatal(err)
				}
				return out, nil
			}
			if _, err := service.ListSellerPayoutBanks(t.Context(), uuid.New(), request.ID); !errors.Is(err, ErrSellerPayoutNotFound) || runtime.reads.Load() != 0 {
				t.Fatalf("foreign directory: %v", err)
			}
			out, err := service.ListSellerPayoutBanks(t.Context(), request.SellerID, request.ID)
			if scenario == "valid" {
				if err != nil || len(out.Items) != 1 {
					t.Fatalf("valid directory %+v %v", out, err)
				}
			} else if err == nil || len(out.Items) != 0 {
				t.Fatalf("invalid directory %+v %v", out, err)
			}
			var transfers int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_payout_transfers`).Scan(&transfers); err != nil || transfers != 0 || runtime.transfers.Load() != 0 {
				t.Fatalf("directory dispatched funds %d %v", transfers, err)
			}
		})
	}
}
