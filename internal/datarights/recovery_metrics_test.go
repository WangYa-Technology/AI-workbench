package datarights_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
)

func TestRecoveryMetricsTrackFailedLeavesBeyondDailyWindow(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := t.Context()
	assertCount := func(kind string, want int64) {
		t.Helper()
		counts, err := datarights.RecoveryFailureCounts(ctx, pool)
		if err != nil || counts[kind] != want || len(counts) != 5 {
			t.Fatalf("counts=%v kind=%s want=%d err=%v", counts, kind, want, err)
		}
	}
	assertCount("media_account", 0)
	owner := cleanupUser(t, pool, "member", "deleted")
	actor := cleanupUser(t, pool, "admin", "active")
	failed := cleanupFailedJob(t, pool, owner)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET created_at=now()-interval '3 days',updated_at=now()-interval '2 days' WHERE id=$1`, failed); err != nil {
		t.Fatal(err)
	}
	assertCount("media_account", 1)
	service := datarights.NewService(pool, t.TempDir())
	retry, err := service.RetryMediaCleanup(ctx, actor, failed, cleanupInput(), "metrics-recovery")
	if err != nil {
		t.Fatal(err)
	}
	assertCount("media_account", 0)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=max_attempts WHERE id=$1`, retry.ID); err != nil {
		t.Fatal(err)
	}
	assertCount("media_account", 1)
	second, err := service.RetryMediaCleanup(ctx, actor, retry.ID, cleanupInput(), "metrics-second-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='succeeded' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	assertCount("media_account", 0)
	var history int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE status='failed'`).Scan(&history); err != nil || history != 2 {
		t.Fatal("monitoring must retain failed evidence", history, err)
	}

	// Missing subjects still need investigation, even though retry is denied.
	for kind, label := range map[string]string{
		"product.delivery_cleanup":  "media_product",
		"data_rights.export":        "export_export",
		"data_rights.export_expire": "export_expiry",
		"data_rights.delete":        "account_deletion",
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO jobs(kind,status) VALUES($1,'failed')`, kind); err != nil {
			t.Fatal(err)
		}
		assertCount(label, 1)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(kind,status) VALUES('unknown-private-job','failed'),('payment.refund_product','failed')`); err != nil {
		t.Fatal(err)
	}
	assertCount("media_product", 1)
}

func TestRecoveryMetricsExcludeClosedExportAndDeletion(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := cleanupUser(t, pool, "member", "active")
	actor := cleanupUser(t, pool, "admin", "active")
	request, original := exportRecoveryFixture(t, pool, owner)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=1 WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	assertExport := func(want int64) {
		t.Helper()
		counts, err := datarights.RecoveryFailureCounts(ctx, pool)
		if err != nil || counts["export_export"] != want || counts["account_deletion"] != 0 {
			t.Fatal(counts, want, err)
		}
	}
	assertExport(1)
	service := datarights.NewService(pool, t.TempDir())
	retry, err := service.RetryExportJob(ctx, actor, original, exportRetryInput(), "metrics-export-recovery")
	if err != nil {
		t.Fatal(err)
	}
	assertExport(0)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=1 WHERE id=$1`, retry.ID); err != nil {
		t.Fatal(err)
	}
	assertExport(1)
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET status='cancelled' WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	deletion := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO data_rights_requests(id,user_id,request_type,status,subject_ref,execute_after)
 VALUES($1,$2,'account_deletion','cancelled',$3,now())`, deletion, owner, "subject_"+strings.Repeat("b", 24)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(kind,status,payload) VALUES('data_rights.delete','failed',jsonb_build_object('requestId',$1::text))`, deletion); err != nil {
		t.Fatal(err)
	}
	assertExport(0)
}
