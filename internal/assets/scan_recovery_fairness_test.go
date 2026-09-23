package assets_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
)

func TestScanRecoveryCannotDeferAnInFlightRecovery(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	owner := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Scan owner','creator')`, owner, owner.String()+"@test.local", "scan_"+owner.String()[:8]); err != nil {
		t.Fatal(err)
	}
	scanner := &executionScanner{}
	stores := media.NewCatalog(media.NewLocalStore(t.TempDir()))
	service := assets.NewServiceWithMedia(pool, stores, scanner)
	a, err := service.Upload(ctx, owner, assets.UploadInput{Title: "Original", Filename: "original.txt", Reader: strings.NewReader("Original awaiting recovery."), RequestID: "scan-concurrent-deferral"})
	if err != nil {
		t.Fatal(err)
	}
	job := claimAssetJobKind(t, ctx, pool, "scan-interrupted", assets.ScanJobKind)
	if err = jobs.NewRepository(pool).Fail(ctx, job, "scan-interrupted", permanentScanFailure{}); err != nil {
		t.Fatal(err)
	}
	traced, entered, release := testutil.GateQuery(t, pool, "SELECT owner_id,scan_status FROM assets")
	first := assets.NewServiceWithMedia(traced, stores, scanner)
	type outcome struct {
		count int
		err   error
	}
	done := make(chan outcome, 1)
	go func() { n, err := first.ReconcileScanExecutions(ctx, 1); done <- outcome{n, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		release()
		<-done
		t.Fatal("first recovery did not reach the account-locked boundary")
	}
	// The first process already owns the account lock. A second process must
	// neither block behind it nor defer its still-due execution binding.
	n, secondErr := service.ReconcileScanExecutions(ctx, 1)
	var due bool
	readErr := pool.QueryRow(ctx, `SELECT recovery_after<=clock_timestamp() FROM asset_scan_executions WHERE asset_id=$1`, a.ID).Scan(&due)
	release()
	result := <-done
	if secondErr != nil || n != 0 || readErr != nil || !due || result.err != nil || result.count != 1 {
		t.Fatalf("in-flight recovery was deferred: second=%d/%v due=%t/%v first=%d/%v", n, secondErr, due, readErr, result.count, result.err)
	}
	var status string
	var audits, checks int
	if err = pool.QueryRow(ctx, `SELECT a.scan_status,e.recovery_checks,(SELECT count(*) FROM audit_events WHERE action='asset.scan_recovered' AND resource_id=a.id) FROM assets a JOIN asset_scan_executions e ON e.asset_id=a.id WHERE a.id=$1`, a.ID).Scan(&status, &checks, &audits); err != nil || status != "review" || checks != 1 || audits != 1 {
		t.Fatal(status, checks, audits, err)
	}
	if scanner.calls != 0 {
		t.Fatal("recovery called scanner")
	}
}

func TestScanRecoveryLockedHeadDoesNotStarveLaterAssets(t *testing.T) {
	for _, lockKind := range []string{"account", "asset", "job", "binding"} {
		t.Run(lockKind, func(t *testing.T) {
			pool, cleanup := assetTestPool(t)
			defer cleanup()
			ctx := t.Context()
			scanner := &executionScanner{}
			service := assets.NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(t.TempDir())), scanner)
			repo := jobs.NewRepository(pool)
			var owners, ids [2]uuid.UUID
			var scanJobs [2]jobs.Job
			for i := range ids {
				owners[i] = uuid.New()
				if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Scan owner','creator')`, owners[i], owners[i].String()+"@test.local", "scan_"+owners[i].String()[:8]); err != nil {
					t.Fatal(err)
				}
				a, err := service.Upload(ctx, owners[i], assets.UploadInput{Title: "Original", Filename: "original.txt", Reader: strings.NewReader("Private original for scan recovery."), RequestID: "scan-fairness"})
				if err != nil {
					t.Fatal(err)
				}
				ids[i] = a.ID
				scanJobs[i] = claimAssetJobKind(t, ctx, pool, "scan-fairness", assets.ScanJobKind)
				if err = repo.Fail(ctx, scanJobs[i], "scan-fairness", permanentScanFailure{}); err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(ctx, `UPDATE asset_scan_executions SET recovery_after=now()-interval '10 minutes'+$2::integer*interval '1 minute' WHERE asset_id=$1`, ids[i], i); err != nil {
					t.Fatal(err)
				}
			}
			// Deferring a busy item must not erase an existing failure or invent another check.
			if _, err := pool.Exec(ctx, `UPDATE asset_scan_executions SET recovery_checks=7,last_error_code='scan_recovery_failed' WHERE asset_id=$1`, ids[0]); err != nil {
				t.Fatal(err)
			}
			locked, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer locked.Rollback(ctx)
			switch lockKind {
			case "account":
				err = accountlifecycle.Lock(ctx, locked, owners[0])
			case "asset":
				_, err = locked.Exec(ctx, `SELECT id FROM assets WHERE id=$1 FOR UPDATE`, ids[0])
			case "job":
				_, err = locked.Exec(ctx, `SELECT id FROM jobs WHERE id=$1 FOR UPDATE`, scanJobs[0].ID)
			case "binding":
				_, err = locked.Exec(ctx, `SELECT asset_id FROM asset_scan_executions WHERE asset_id=$1 FOR UPDATE`, ids[0])
			}
			if err != nil {
				t.Fatal(err)
			}
			total := 0
			for range 2 {
				pass, cancel := context.WithTimeout(ctx, time.Second)
				count, recoverErr := service.ReconcileScanExecutions(pass, 1)
				cancel()
				if recoverErr != nil {
					t.Fatalf("busy %s consumed pass: %v", lockKind, recoverErr)
				}
				total += count
			}
			if total != 1 {
				t.Errorf("locked head starved next candidate: completed=%d", total)
			}
			var first, second string
			var notices int
			if err = pool.QueryRow(ctx, `SELECT (SELECT scan_status FROM assets WHERE id=$1),(SELECT scan_status FROM assets WHERE id=$2),(SELECT count(*) FROM notifications WHERE resource_id=$1)`, ids[0], ids[1]).Scan(&first, &second, &notices); err != nil {
				t.Fatal(err)
			}
			if first != "pending" || second != "review" || notices != 0 {
				t.Errorf("busy head or later asset wrong: %s %s notifications=%d", first, second, notices)
			}
			if err = locked.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			var checks int
			var code, jobState string
			var deferred bool
			if err = pool.QueryRow(ctx, `SELECT e.recovery_checks,e.last_error_code,e.recovery_after>now(),j.status FROM asset_scan_executions e JOIN jobs j ON j.id=e.job_id WHERE e.asset_id=$1`, ids[0]).Scan(&checks, &code, &deferred, &jobState); err != nil {
				t.Fatal(err)
			}
			if checks != 7 || code != "scan_recovery_failed" || jobState != "failed" {
				t.Fatalf("busy skip rewrote failure evidence: %d %s %s", checks, code, jobState)
			}
			if lockKind != "binding" && !deferred {
				t.Error("busy head was not deferred")
			}
			if _, err = pool.Exec(ctx, `UPDATE asset_scan_executions SET recovery_after=now()-interval '1 second' WHERE asset_id=$1`, ids[0]); err != nil {
				t.Fatal(err)
			}
			if n, err := service.ReconcileScanExecutions(ctx, 1); err != nil || n != 1 {
				t.Fatalf("unlocked asset cannot recover: %d %v", n, err)
			}
			if scanner.calls != 0 {
				t.Fatal("lock recovery called external scanner")
			}
		})
	}
}
