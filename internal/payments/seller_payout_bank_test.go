package payments

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sellerBankRuntime struct {
	productSettlementRuntime
	reads  atomic.Int32
	onRead func(context.Context, PayoutBankTargetRequest) (PayoutBankTarget, error)
}

func (r *sellerBankRuntime) ReadPayoutBankTarget(ctx context.Context, input PayoutBankTargetRequest) (PayoutBankTarget, error) {
	r.reads.Add(1)
	if r.onRead != nil {
		return r.onRead(ctx, input)
	}
	return PayoutBankTarget{BankName: "Example Bank", Last4: "6789", DestinationID: input.DestinationID, BankDestinationID: input.BankDestinationID,
		Currency: input.Currency, ObservedAt: time.Now().UTC()}, nil
}

func sellerBankFixture(t *testing.T) (*pgxpool.Pool, *Service, *sellerBankRuntime, SellerPayoutRequest) {
	t.Helper()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	service, _, _, request := sellerTransferFixture(t, pool)
	runtime := &sellerBankRuntime{}
	service.runtimes = NewRuntimeCatalog(runtime)
	return pool, service, runtime, request
}

func TestSellerPayoutBankSelectionIsImmutableAndOwnerScoped(t *testing.T) {
	pool, service, runtime, request := sellerBankFixture(t)
	ctx := t.Context()
	if _, err := service.BindSellerPayoutBankTarget(ctx, uuid.New(), request.ID, "ba_original"); !errors.Is(err, ErrSellerPayoutNotFound) {
		t.Fatalf("other seller: %v", err)
	}
	if runtime.reads.Load() != 0 {
		t.Fatal("foreign owner reached provider")
	}
	first, err := service.BindSellerPayoutBankTarget(ctx, request.SellerID, request.ID, "ba_original")
	if err != nil {
		t.Fatal(err)
	}
	if first.BankName != "Example Bank" || first.Last4 != "6789" {
		t.Fatalf("summary missing: %+v", first)
	}
	runtime.onRead = func(context.Context, PayoutBankTargetRequest) (PayoutBankTarget, error) {
		t.Fatal("replay must not refetch current provider metadata")
		return PayoutBankTarget{}, nil
	}
	service.config.Enabled = false
	again, err := service.BindSellerPayoutBankTarget(ctx, request.SellerID, request.ID, "ba_original")
	if err != nil || first != again || runtime.reads.Load() != 1 {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if _, err := service.BindSellerPayoutBankTarget(ctx, request.SellerID, request.ID, "ba_changed"); !errors.Is(err, ErrSellerPayoutBankConflict) {
		t.Fatalf("changed bank: %v", err)
	}
	if _, err := service.GetSellerPayoutBankTarget(ctx, uuid.New(), request.ID); !errors.Is(err, ErrSellerPayoutNotFound) {
		t.Fatalf("private read: %v", err)
	}
	for _, sql := range []string{
		`UPDATE seller_payout_bank_targets SET bank_destination_id='ba_other'`,
		`UPDATE seller_payout_bank_targets SET observed_at=clock_timestamp()`,
		`UPDATE seller_payout_bank_targets SET bank_name='Changed Bank'`,
		`UPDATE seller_payout_bank_targets SET last4='1111'`,
		`DELETE FROM seller_payout_bank_targets`,
	} {
		_, err := pool.Exec(ctx, sql)
		requirePayoutConstraint(t, err)
	}
	body, err := os.ReadFile("../platform/database/migrations/0159_seller_payout_bank_targets.down.sql")
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
	if _, err := service.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetSellerPayoutBankTarget(ctx, request.SellerID, request.ID)
	if err != nil || stored != first {
		t.Fatalf("cancel lost bank evidence: %+v %v", stored, err)
	}
	var bindings, events, jobs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_payout_bank_targets),
 (SELECT count(*) FROM seller_payout_request_events WHERE event_type='payout.bank_bound'),
 (SELECT count(*) FROM jobs WHERE kind='payment.fund_seller_payout')`).Scan(&bindings, &events, &jobs); err != nil {
		t.Fatal(err)
	}
	if bindings != 1 || events != 1 || jobs != 0 || runtime.transfers.Load() != 0 {
		t.Fatal("bank selection dispatched funds or duplicated evidence")
	}
}

func TestSellerPayoutBankRechecksAfterRemoteRead(t *testing.T) {
	for _, scenario := range []string{"cancel", "suspended", "destination", "identity", "refund", "mode", "debt", "wrong_bank", "stale", "future", "missing_last4", "invalid_last4", "invalid_name", "provider_error"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, request := sellerBankFixture(t)
			runtime.onRead = func(ctx context.Context, input PayoutBankTargetRequest) (PayoutBankTarget, error) {
				out := PayoutBankTarget{BankName: "Example Bank", Last4: "6789", DestinationID: input.DestinationID, BankDestinationID: input.BankDestinationID, Currency: input.Currency, ObservedAt: time.Now()}
				var err error
				switch scenario {
				case "cancel":
					_, err = service.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID)
				case "suspended":
					_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, request.SellerID)
				case "destination":
					_, err = pool.Exec(ctx, `UPDATE payment_destinations SET destination_id='acct_changed' WHERE user_id=$1`, request.SellerID)
				case "identity":
					_, err = pool.Exec(ctx, `UPDATE payment_destinations SET original_merchant_id='acct_changed' WHERE user_id=$1`, request.SellerID)
				case "refund":
					_, err = pool.Exec(ctx, `UPDATE product_settlements SET status='refund_hold' WHERE seller_id=$1`, request.SellerID)
				case "mode":
					_, err = pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='automatic'`)
				case "debt":
					_, err = pool.Exec(ctx, `INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status)
 SELECT seller_id,id,1,1,'USD','open' FROM product_settlements WHERE seller_id=$1`, request.SellerID)
				case "wrong_bank":
					out.BankDestinationID = "ba_wrong"
				case "stale":
					out.ObservedAt = time.Now().Add(-time.Minute)
				case "future":
					out.ObservedAt = time.Now().Add(time.Minute)
				case "missing_last4":
					out.Last4 = ""
				case "invalid_last4":
					out.Last4 = "12345678"
				case "invalid_name":
					out.BankName = "Bank\u202Espoof"
				case "provider_error":
					return out, errors.New("provider unavailable")
				}
				if err != nil {
					t.Fatal(err)
				}
				return out, nil
			}
			if _, err := service.BindSellerPayoutBankTarget(t.Context(), request.SellerID, request.ID, "ba_original"); err == nil {
				t.Fatal("changed state accepted")
			}
			var count int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_payout_bank_targets`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("invalid evidence persisted: %d %v", count, err)
			}
		})
	}
}

func TestSellerPayoutBankConcurrentSelections(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(map[bool]string{true: "same", false: "different"}[same], func(t *testing.T) {
			pool, service, runtime, request := sellerBankFixture(t)
			entered := make(chan struct{}, 2)
			release := make(chan struct{})
			runtime.onRead = func(ctx context.Context, input PayoutBankTargetRequest) (PayoutBankTarget, error) {
				entered <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return PayoutBankTarget{}, ctx.Err()
				}
				return PayoutBankTarget{BankName: "Example Bank", Last4: "6789", DestinationID: input.DestinationID, BankDestinationID: input.BankDestinationID, Currency: input.Currency, ObservedAt: time.Now()}, nil
			}
			var wg sync.WaitGroup
			errs := make([]error, 2)
			for i := range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					bank := "ba_first123"
					if i == 1 && !same {
						bank = "ba_second123"
					}
					_, errs[i] = service.BindSellerPayoutBankTarget(t.Context(), request.SellerID, request.ID, bank)
				}()
			}
			for range 2 {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					close(release)
					wg.Wait()
					t.Fatalf("both reads did not reach provider: %v", errs)
				}
			}
			close(release)
			wg.Wait()
			successes := 0
			for _, err := range errs {
				if err == nil {
					successes++
				} else if !errors.Is(err, ErrSellerPayoutBankConflict) {
					t.Fatal(err)
				}
			}
			want := 1
			if same {
				want = 2
			}
			if successes != want {
				t.Fatalf("successes=%d want=%d errors=%v", successes, want, errs)
			}
			var count int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_payout_bank_targets`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("duplicate selections: %d %v", count, err)
			}
		})
	}
}

func TestSellerPayoutBankCannotBindAfterFundingStarts(t *testing.T) {
	t.Run("unbound source reservation", func(t *testing.T) {
		pool, service, runtime, request := sellerBankFixture(t)
		var settlement uuid.UUID
		if err := pool.QueryRow(t.Context(), `SELECT settlement_id FROM seller_payout_request_allocations WHERE payout_request_id=$1`, request.ID).Scan(&settlement); err != nil {
			t.Fatal(err)
		}
		// An unbound historical/reserved source cannot gain a bank after the fact.
		if _, err := pool.Exec(t.Context(), reserveSellerTransferSQL, request.ID, settlement); err != nil {
			t.Fatal(err)
		}
		if _, err := service.BindSellerPayoutBankTarget(t.Context(), request.SellerID, request.ID, "ba_original"); !errors.Is(err, ErrSellerPayoutBankConflict) {
			t.Fatalf("source reservation allowed later selection: %v", err)
		}
		var count int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_payout_bank_targets`).Scan(&count); err != nil || count != 0 || runtime.reads.Load() != 0 {
			t.Fatalf("late selection: %d %v", count, err)
		}
	})
	t.Run("started approved source retains original binding", func(t *testing.T) {
		pool, service, funding, _, request, job := sellerFundingFixture(t)
		original, err := service.GetSellerPayoutBankTarget(t.Context(), request.SellerID, request.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err != nil {
			t.Fatal(err)
		}
		if funding.transfers.Load() != 1 {
			t.Fatal("source did not actually dispatch")
		}
		runtime := &sellerBankRuntime{}
		service.runtimes = NewRuntimeCatalog(runtime)
		replay, err := service.BindSellerPayoutBankTarget(t.Context(), request.SellerID, request.ID, original.BankDestinationID)
		if err != nil || replay != original {
			t.Fatalf("original evidence not replayable: %+v %v", replay, err)
		}
		if _, err := service.BindSellerPayoutBankTarget(t.Context(), request.SellerID, request.ID, "ba_changed"); !errors.Is(err, ErrSellerPayoutBankConflict) {
			t.Fatalf("source allowed bank replacement: %v", err)
		}
		var count int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_payout_bank_targets`).Scan(&count); err != nil || count != 1 || runtime.reads.Load() != 0 {
			t.Fatalf("bank evidence changed: %d %v", count, err)
		}
	})
}

func TestSellerPayoutBankPinsLaterSourceAdmission(t *testing.T) {
	pool, service, _, request := sellerBankFixture(t)
	ctx := t.Context()
	if _, err := service.BindSellerPayoutBankTarget(ctx, request.SellerID, request.ID, "ba_original"); err != nil {
		t.Fatal(err)
	}
	var settlement uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT settlement_id FROM seller_payout_bank_targets WHERE payout_request_id=$1`, request.ID).Scan(&settlement); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_destinations SET destination_id='acct_changed123' WHERE user_id=$1`, request.SellerID); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, strings.ReplaceAll(reserveSellerTransferSQL, "acct_settlement", "acct_changed123"), request.ID, settlement)
	requirePayoutConstraint(t, err)
	if _, err := pool.Exec(ctx, `UPDATE payment_destinations SET destination_id='acct_settlement' WHERE user_id=$1`, request.SellerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, reserveSellerTransferSQL, request.ID, settlement); err != nil {
		t.Fatal(err)
	}
}

func TestSellerPayoutBankAuditFailureRollsBackSelection(t *testing.T) {
	pool, service, _, request := sellerBankFixture(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_bank_audit_test() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.event_type='payout.bank_bound' THEN RAISE EXCEPTION 'test audit unavailable'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER reject_bank_audit_test BEFORE INSERT ON seller_payout_request_events FOR EACH ROW EXECUTE FUNCTION reject_bank_audit_test()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BindSellerPayoutBankTarget(ctx, request.SellerID, request.ID, "ba_original"); err == nil {
		t.Fatal("audit failure accepted")
	}
	if _, err := service.GetSellerPayoutBankTarget(ctx, request.SellerID, request.ID); !errors.Is(err, ErrSellerPayoutNotFound) {
		t.Fatalf("binding survived audit rollback: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_bank_audit_test ON seller_payout_request_events; DROP FUNCTION reject_bank_audit_test()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BindSellerPayoutBankTarget(ctx, request.SellerID, request.ID, "ba_original"); err != nil {
		t.Fatal(err)
	}
}
