package payments

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestSellerBankRecoveryAndPostPaidReturn(t *testing.T) {
	pool, service, runtime, _, request, command, original := bankExecutionFixture(t)
	ctx := t.Context()
	runtime.create = func(context.Context, PayoutRequest) (Payout, error) { return Payout{}, context.DeadlineExceeded }
	if err := service.HandleSellerBankPayoutJob(ctx, original); err == nil {
		t.Fatal("lost bank response completed")
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',updated_at=clock_timestamp() WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	var due time.Time
	if err := pool.QueryRow(ctx, `SELECT due_at FROM seller_bank_payout_check_candidates WHERE command_id=$1`, command).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileSellerBankPayouts(ctx, 10, due.Add(-time.Microsecond)); err != nil || n != 0 {
		t.Fatal("early recovery", n, err)
	}
	// A busy original payment is skipped without holding up other scan work.
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `SELECT p.id FROM payment_intents p JOIN seller_bank_payout_commands c ON c.payment_id=p.id WHERE c.id=$1 FOR UPDATE OF p`, command); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileSellerBankPayouts(ctx, 10, due); err != nil || n != 0 {
		t.Fatal("busy payment scan", n, err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.scheduleSellerBankCheck(ctx, command, due)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_bank_payout_checks WHERE command_id=$1`, command).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate bank recovery jobs", count, err)
	}
	job := jobs.Job{Kind: SellerBankPayoutCheckJobKind}
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM seller_bank_payout_checks c JOIN jobs j ON j.id=c.job_id WHERE c.command_id=$1`, command).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	service.config.Enabled = false
	if err := service.HandleSellerBankPayoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertBankLedger(t, pool, request, 1, 0, "succeeded")
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='succeeded',updated_at=clock_timestamp() WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT due_at FROM seller_bank_payout_check_candidates WHERE command_id=$1`, command).Scan(&due); err != nil || time.Until(due) < 23*time.Hour {
		t.Fatal("paid bank payout lost its return monitor", due, err)
	}
	if n, err := service.reconcileSellerBankPayouts(ctx, 10, due); err != nil || n != 1 {
		t.Fatal("return check not scheduled", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM seller_bank_payout_checks c JOIN jobs j ON j.id=c.job_id
 WHERE c.command_id=$1 ORDER BY c.created_at DESC LIMIT 1`, command).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	runtime.status = "failed"
	if err := service.HandleSellerBankPayoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertBankLedger(t, pool, request, 1, 1, "reconciliation_required")
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_bank_payout_check_candidates WHERE command_id=$1`, command).Scan(&count); err != nil || count != 0 {
		t.Fatal("returned payout incorrectly scheduled another automated check", count, err)
	}
	if runtime.payouts.Load() != 1 || runtime.bankReads.Load() != 2 {
		t.Fatal("read recovery sent funds again")
	}
	for _, query := range []string{`DELETE FROM seller_bank_payout_checks`, `UPDATE seller_bank_payout_checks SET created_at=clock_timestamp()`} {
		_, err := pool.Exec(ctx, query)
		requirePayoutConstraint(t, err)
	}
	// Unregistered jobs and substituted commands cannot borrow a read binding.
	job.ID = uuid.New()
	if err := service.HandleSellerBankPayoutCheckJob(ctx, job); err == nil {
		t.Fatal("unbound read job accepted")
	}
	assertOperationalMetric(t, pool, "problem", "seller_bank_review", "test", 0, 0, 0)
	assertOperationalMetric(t, pool, "problem", "seller_bank_failed", "test", 1, 0, 0)
	var buyer uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT p.payer_id FROM seller_bank_payout_commands c JOIN payment_intents p ON p.id=c.payment_id WHERE c.id=$1`, command).Scan(&buyer); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []uuid.UUID{request.SellerID, buyer} {
		exporter, id, exportJob := sellerExportFixture(t, pool, owner)
		data, body := sellerExportData(t, exporter, owner, id, exportJob)
		if owner == request.SellerID {
			if len(data["sellerBankPayoutResults"]) != 2 || len(data["sellerBankPayoutReads"]) != 3 {
				t.Fatal("bank result export incomplete")
			}
			for _, row := range data["sellerBankPayoutResults"] {
				if len(row) != 4 || row["commandId"] != command.String() {
					t.Fatal("bank result export escaped allowlist", row)
				}
			}
			for _, row := range data["sellerBankPayoutReads"] {
				if len(row) != 8 || row["commandId"] != command.String() {
					t.Fatal("bank read export escaped allowlist", row)
				}
			}
			if strings.Contains(string(body), "seller-bank-payout-"+request.ID.String()) || strings.Contains(string(body), "po_"+strings.ReplaceAll(request.ID.String(), "-", "")) {
				t.Fatal("bank export leaked raw provider evidence")
			}
		} else if len(data["sellerBankPayoutResults"]) != 0 || len(data["sellerBankPayoutReads"]) != 0 {
			t.Fatal("buyer obtained seller's bank export")
		}
	}
}
