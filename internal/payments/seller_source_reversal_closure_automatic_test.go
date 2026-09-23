package payments

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestSellerSourceReversalClosureAutomaticSettlement(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost_%t", lost), func(t *testing.T) {
			pool, service, runtime, request, command, reversalJob := sourceReversalExecutionFixture(t)
			ctx := t.Context()
			if err := service.HandleSellerSourceReversalJob(ctx, reversalJob); err != nil {
				t.Fatal(err)
			}
			actor, input := sourceClosureInput(t, pool, command.ID)
			if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "close-before-mode-switch", "trace"); err != nil {
				t.Fatal(err)
			}
			settlement, original := sourceClosureBinding(t, pool, command.ID)
			if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='automatic' WHERE singleton`); err != nil {
				t.Fatal(err)
			}
			job := jobs.Job{Kind: ProductSettlementJobKind}
			if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM product_settlement_dispatches d JOIN jobs j ON j.id=d.job_id WHERE d.settlement_id=$1`, settlement).Scan(&job.ID, &job.Payload); err != nil {
				t.Fatal(err)
			}
			runtime.onTransfer = func(_ context.Context, in TransferRequest) {
				if in.SourceRequestID != uuid.Nil || in.SettlementBatchID == uuid.Nil || transferDispatchKey(in) != "settlement-batch-"+in.SettlementBatchID.String() {
					t.Error("automatic mode reused old source key", in)
				}
				var batch uuid.UUID
				if err := pool.QueryRow(ctx, `SELECT payout_batch_id FROM product_settlements WHERE id=$1`, settlement).Scan(&batch); err != nil || batch != in.SettlementBatchID {
					t.Error("key not bound to durable batch", batch, err)
				}
			}
			runtime.transform = func(in Transfer) Transfer { in.ProviderID = "tr_automatic_after_return"; return in }
			runtime.lost = lost
			if err := service.HandleProductSettlementJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if lost {
				runtime.lookup = func(_ context.Context, in TransferLookupRequest) (TransferLookupResult, error) {
					if len(in.ReturnedSources) != 1 || in.ReturnedSources[0].ProviderID != original {
						t.Error("lost historical exclusion", in.ReturnedSources)
					}
					out := observedSettlementTransfer(in)
					out.Observations[0].ProviderID = "tr_automatic_after_return"
					return out, nil
				}
				if err := service.HandleProductSettlementCheckJob(ctx, scheduleSettlementCheck(t, service, settlement)); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.HandleProductSettlementJob(ctx, job); err != nil {
				t.Fatal("completed job replay", err)
			}
			var status string
			if err := pool.QueryRow(ctx, `SELECT status FROM product_settlements WHERE id=$1`, settlement).Scan(&status); err != nil || status != "transferred" {
				t.Fatal("automatic transfer incomplete", status, err)
			}
			if runtime.transfers.Load() != 2 {
				t.Fatal("automatic source sent more than once", runtime.transfers.Load())
			}
			balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
			if err != nil || balance.ReservedCents != 0 || balance.WithdrawableCents != 0 {
				t.Fatal("automatic transfer left spendable balance", balance, err)
			}
			assertSourceClosure(t, pool, request, 1)
			assertSellerFunding(t, pool, request, "succeeded", "cancelled")
		})
	}
}
