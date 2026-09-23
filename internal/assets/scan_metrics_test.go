package assets_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestScanRecoveryMetricsReflectBusinessState(t *testing.T) {
	for _, mode := range []string{"queued", "running", "expired_running", "missing_attempt", "failed", "backoff", "recovered", "succeeded_pending", "cancelled_pending", "manual_review", "missing_binding", "future_creation", "infinite_creation"} {
		t.Run(mode, func(t *testing.T) {
			pool, cleanup := assetTestPool(t)
			defer cleanup()
			ctx := t.Context()
			owner := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Scan owner','creator')`, owner, owner.String()+"@test.local", "scan_"+owner.String()[:8]); err != nil {
				t.Fatal(err)
			}
			service := assets.NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(t.TempDir())), &executionScanner{})
			a, err := service.Upload(ctx, owner, assets.UploadInput{Title: "Metrics original", Filename: "private.txt", Reader: strings.NewReader("Private original bytes never enter metrics."), RequestID: "metrics-scan"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `UPDATE assets SET created_at=now()-interval '20 minutes' WHERE id=$1`, a.ID); err != nil {
				t.Fatal(err)
			}
			repo := jobs.NewRepository(pool)
			expected := assets.ScanRecoveryMetrics{Pending: 1}
			switch mode {
			case "running", "expired_running", "missing_attempt":
				j := claimAssetJobKind(t, ctx, pool, "metrics-scan", assets.ScanJobKind)
				if mode == "expired_running" {
					_, err = pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=now()-interval '2 minutes' WHERE id=$1`, j.ID)
				}
				if mode == "missing_attempt" {
					_, err = pool.Exec(ctx, `UPDATE jobs SET lease_token=gen_random_uuid() WHERE id=$1`, j.ID)
					expected.Unresolved = 1
				}
			case "failed", "backoff", "recovered":
				j := claimAssetJobKind(t, ctx, pool, "metrics-scan", assets.ScanJobKind)
				if err = repo.Fail(ctx, j, "metrics-scan", permanentScanFailure{}); err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(ctx, `UPDATE asset_scan_executions SET recovery_after=now()-interval '6 minutes' WHERE asset_id=$1`, a.ID); err != nil {
					t.Fatal(err)
				}
				expected.Due = 1
				if mode == "backoff" {
					_, err = pool.Exec(ctx, `UPDATE asset_scan_executions SET recovery_after=now()+interval '1 minute',last_error_code='scan_recovery_failed' WHERE asset_id=$1`, a.ID)
					expected.Due = 0
					expected.Failed = 1
				}
				if mode == "recovered" {
					_, err = service.ReconcileScanExecutions(ctx, 100)
					expected = assets.ScanRecoveryMetrics{}
				}
			case "succeeded_pending", "cancelled_pending":
				state := "succeeded"
				if mode == "cancelled_pending" {
					state = "cancelled"
				}
				_, err = pool.Exec(ctx, `UPDATE jobs SET status=$2 WHERE kind='asset.scan' AND payload->>'assetId'=$1`, a.ID.String(), state)
				expected.Unresolved = 1
			case "manual_review":
				_, err = pool.Exec(ctx, `UPDATE assets SET scan_status='review' WHERE id=$1`, a.ID)
				expected = assets.ScanRecoveryMetrics{}
			case "missing_binding":
				// A historical unbound upload is created separately: do not delete immutable evidence.
				_, err = pool.Exec(ctx, `UPDATE assets SET scan_status='clean' WHERE id=$1`, a.ID)
				if err == nil {
					_, err = pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type,scan_status,license_code,storage_backend,storage_key,created_at)
 VALUES($1,$2,'document','Historical pending','/private','text/plain','upload','pending','hcai-commercial-standard-v1','local_file',$1::uuid::text,now()-interval '20 minutes')`, uuid.New(), owner)
				}
				expected.Unresolved = 1
			case "future_creation", "infinite_creation":
				sql := `UPDATE assets SET created_at=now()+interval '1 day' WHERE id=$1`
				if mode == "infinite_creation" {
					sql = `UPDATE assets SET created_at='infinity' WHERE id=$1`
				}
				_, err = pool.Exec(ctx, sql, a.ID)
				expected.Unresolved = 1
			}
			if err != nil {
				t.Fatal(err)
			}
			m, err := assets.ScanExecutionMetrics(ctx, pool)
			if err != nil {
				t.Fatal(err)
			}
			if m.Pending != expected.Pending || m.Failed != expected.Failed || m.Due != expected.Due || m.Unresolved != expected.Unresolved {
				t.Fatalf("metrics=%+v expected=%+v", m, expected)
			}
			if expected.Pending > 0 && mode != "future_creation" && mode != "infinite_creation" {
				if m.OldestPendingAge < 1200 || m.OldestPendingAge > 1260 {
					t.Fatalf("pending age reset or wrong: %v", m.OldestPendingAge)
				}
			} else if m.OldestPendingAge != 0 {
				t.Fatalf("no finite pending age: %v", m.OldestPendingAge)
			}
			if expected.Due > 0 {
				if m.OldestDueAge < 360 || m.OldestDueAge > 420 {
					t.Fatalf("wrong due age: %v", m.OldestDueAge)
				}
			} else if m.OldestDueAge != 0 {
				t.Fatalf("backoff or empty due has age: %v", m.OldestDueAge)
			}
		})
	}
}

func TestScanRecoveryMetricsQueryFailureIsNotZero(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := assets.ScanExecutionMetrics(ctx, pool); err == nil {
		t.Fatal("cancelled metrics query returned healthy counters")
	}
}
