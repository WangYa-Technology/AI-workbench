package payments

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSellerBankStatusOwnerProjectionAndNotifications(t *testing.T) {
	pool, service, runtime, request, actor, input := bankOperationFixture(t)
	ctx := t.Context()
	before, err := service.GetSellerPayoutRequest(ctx, request.SellerID, request.ID)
	if err != nil || before.BankPayout != nil {
		t.Fatal("source success became a bank result", before, err)
	}
	submitted, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, input, "seller-status-original", "private-status-trace")
	if err != nil {
		t.Fatal(err)
	}
	job := jobs.Job{ID: submitted.JobID, Kind: SellerBankPayoutJobKind}
	if err := pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, job.ID).Scan(&job.Payload); err != nil {
		t.Fatal(err)
	}
	current, err := service.GetSellerPayoutRequest(ctx, request.SellerID, request.ID)
	if err != nil || current.BankPayout == nil || current.BankPayout.Status != "unconfirmed" || current.BankPayout.ObservedAt != nil || current.BankPayout.CheckedAt != nil {
		t.Fatal("queued job implied bank arrival", current, err)
	}
	for _, step := range []struct {
		observed, expected, parent string
		notices                    int
		review                     bool
	}{
		{"pending", "pending", "processing", 0, false}, {"paid", "paid", "succeeded", 1, false},
		{"pending", "paid", "succeeded", 1, false}, {"failed", "failed", "reconciliation_required", 2, true},
		{"failed", "failed", "reconciliation_required", 2, true}, {"paid", "failed", "reconciliation_required", 2, true},
	} {
		runtime.status = step.observed
		_ = service.HandleSellerBankPayoutJob(ctx, job)
		calls := runtime.bankReads.Load()
		detail, err := service.GetSellerPayoutRequest(ctx, request.SellerID, request.ID)
		if err != nil || detail.Status != step.parent || detail.BankPayout == nil || detail.BankPayout.Status != step.expected || detail.BankPayout.RequiresReview != step.review || detail.BankPayout.ObservedAt == nil || detail.BankPayout.CheckedAt == nil {
			t.Fatalf("step %+v got %+v bank %+v: %v", step, detail, detail.BankPayout, err)
		}
		page, err := service.ListSellerPayoutRequests(ctx, request.SellerID, "", 1)
		if err != nil || len(page.Items) != 1 {
			t.Fatal("owner directory", page, err)
		}
		body, _ := json.Marshal(detail.BankPayout)
		listed, _ := json.Marshal(page.Items[0].BankPayout)
		if string(body) != string(listed) {
			t.Fatal("detail and directory differ")
		}
		var fields map[string]any
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 4 {
			t.Fatal("seller bank projection escaped whitelist", fields)
		}
		for _, field := range []string{"status", "requiresReview", "observedAt", "checkedAt"} {
			if _, ok := fields[field]; !ok {
				t.Fatal("missing seller bank field", field)
			}
		}
		for _, private := range []string{input.Reason, actor.String(), submitted.Command.ID.String(), "private-status-trace", "seller-status-original", "acct_", "po_"} {
			if strings.Contains(string(body), private) {
				t.Fatal("bank details leaked", private)
			}
		}
		if runtime.bankReads.Load() != calls {
			t.Fatal("seller GET called bank")
		}
		assertSellerBankNotices(t, pool, step.notices)
	}
	if runtime.payouts.Load() != 1 {
		t.Fatal("seller updates sent bank money twice")
	}
	if _, err := service.GetSellerPayoutRequest(ctx, actor, request.ID); !errors.Is(err, ErrSellerPayoutNotFound) {
		t.Fatal("foreign seller read", err)
	}
	repo := notifications.NewRepository(pool)
	ids := sellerBankNotificationIDs(t, pool)
	for _, id := range ids {
		payload, _ := json.Marshal(map[string]any{"notificationId": id})
		for range 2 {
			if err := repo.HandleDeliveryJob(ctx, jobs.Job{Kind: notifications.JobKind, Payload: payload}); err != nil {
				t.Fatal(err)
			}
		}
	}
	inbox, err := repo.List(ctx, request.SellerID, notifications.ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	bankNotices := 0
	for _, notice := range inbox.Items {
		if notice.Kind != "marketplace.payout_paid" && notice.Kind != "marketplace.payout_returned" {
			continue
		}
		bankNotices++
		if notice.TargetPath != "/workspace/payouts/"+request.ID.String() || notice.ResourceID == nil || *notice.ResourceID != request.ID {
			t.Fatal("wrong notification target", notice)
		}
		raw, _ := json.Marshal(notice)
		for _, private := range []string{input.Reason, actor.String(), submitted.Command.ID.String(), "private-status-trace", "acct_", "po_"} {
			if strings.Contains(string(raw), private) {
				t.Fatal("notification leaked", private)
			}
		}
		if _, err := repo.MarkRead(ctx, actor, notice.ID); !errors.Is(err, notifications.ErrNotFound) {
			t.Fatal("foreign bank notice readable", err)
		}
	}
	if bankNotices != 2 {
		t.Fatal("bank result notifications missing", inbox)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, request.SellerID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetSellerPayoutRequest(ctx, request.SellerID, request.ID); !errors.Is(err, ErrSellerPayoutNotFound) {
		t.Fatal("suspended owner read bank result", err)
	}
}

func assertSellerBankNotices(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	var notices, queued int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM notifications WHERE kind IN ('marketplace.payout_paid','marketplace.payout_failed','marketplace.payout_returned')),
 (SELECT count(*) FROM jobs j JOIN notifications n ON j.payload->>'notificationId'=n.id::text WHERE n.kind IN ('marketplace.payout_paid','marketplace.payout_failed','marketplace.payout_returned'))`).Scan(&notices, &queued); err != nil || notices != want || queued != want {
		t.Fatal("bank notifications/jobs", notices, queued, want, err)
	}
}
func sellerBankNotificationIDs(t *testing.T, pool *pgxpool.Pool) []uuid.UUID {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT id FROM notifications WHERE kind IN ('marketplace.payout_paid','marketplace.payout_failed','marketplace.payout_returned') ORDER BY created_at,id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestSellerBankStatusFailedBeforePaidAndDisabledNotifications(t *testing.T) {
	pool, service, runtime, _, request, _, job := bankExecutionFixture(t)
	ctx := t.Context()
	repo := notifications.NewRepository(pool)
	if _, err := repo.UpdatePreference(ctx, request.SellerID, "marketplace.payout_failed", false, 1); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"canceled", "failed", "failed"} {
		runtime.status = status
		if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	assertBankLedger(t, pool, request, 0, 0, "reconciliation_required")
	assertSellerBankNotices(t, pool, 1)
	ids := sellerBankNotificationIDs(t, pool)
	payload, _ := json.Marshal(map[string]any{"notificationId": ids[0]})
	if err := repo.HandleDeliveryJob(ctx, jobs.Job{Kind: notifications.JobKind, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var delivery, kind string
	if err := pool.QueryRow(ctx, `SELECT delivery_status,kind FROM notifications WHERE id=$1`, ids[0]).Scan(&delivery, &kind); err != nil || delivery != "suppressed" || kind != "marketplace.payout_failed" {
		t.Fatal(delivery, kind, err)
	}
	detail, err := service.GetSellerPayoutRequest(ctx, request.SellerID, request.ID)
	if err != nil || detail.BankPayout == nil || !detail.BankPayout.RequiresReview || detail.BankPayout.Status != "failed" {
		t.Fatal("disabled notification hid bank detail", detail, err)
	}
}

func TestSellerBankStatusNotificationFailureRollsBackFundsAndRecoversByRead(t *testing.T) {
	for _, returning := range []bool{false, true} {
		t.Run(map[bool]string{false: "paid", true: "returned"}[returning], func(t *testing.T) {
			pool, service, runtime, _, request, _, job := bankExecutionFixture(t)
			ctx := t.Context()
			before := 0
			parent := "processing"
			if returning {
				if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
					t.Fatal(err)
				}
				runtime.status = "failed"
				before = 1
				parent = "succeeded"
			}
			if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_bank_notification() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.kind IN ('marketplace.payout_paid','marketplace.payout_failed','marketplace.payout_returned') THEN RAISE EXCEPTION 'bank notification unavailable'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER fail_bank_notification BEFORE INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION fail_bank_notification()`); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
				t.Fatal("financial result committed without durable notification")
			}
			assertBankLedger(t, pool, request, before, 0, parent)
			assertSellerBankNotices(t, pool, before)
			if _, err := pool.Exec(ctx, `DROP TRIGGER fail_bank_notification ON notifications`); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
			returned := 0
			parent = "succeeded"
			if returning {
				returned = 1
				parent = "reconciliation_required"
			}
			assertBankLedger(t, pool, request, 1, returned, parent)
			assertSellerBankNotices(t, pool, before+1)
			if runtime.payouts.Load() != 1 {
				t.Fatal("notification retry resent money", runtime.payouts.Load())
			}
		})
	}
}
