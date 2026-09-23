package assets_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

type permanentScanFailure struct{}

func (permanentScanFailure) Error() string   { return "fixture permanent failure" }
func (permanentScanFailure) Retryable() bool { return false }

type executionScanner struct {
	scan  func() (media.ScanResult, error)
	calls int
}

func (s *executionScanner) Adapter() string { return "execution-fixture" }
func (s *executionScanner) Scan(context.Context, string, string, []byte) (media.ScanResult, error) {
	s.calls++
	if s.scan != nil {
		return s.scan()
	}
	return media.ScanResult{Status: "clean", ReasonCode: "no_threat_detected", Engine: "fixture", Version: "1"}, nil
}

func TestScanExecutionLeaseAndIdentity(t *testing.T) {
	for _, mode := range []string{"expired_before_start", "expired_after_scan", "reassigned_after_scan", "foreign_payload", "wrong_kind", "forged_counters", "manual_during_scan", "renew_during_scan"} {
		t.Run(mode, func(t *testing.T) {
			pool, cleanup := assetTestPool(t)
			defer cleanup()
			ctx := t.Context()
			owner := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Scan owner','creator')`, owner, owner.String()+"@test.local", "scan_"+owner.String()[:8]); err != nil {
				t.Fatal(err)
			}
			scanner := &executionScanner{}
			service := assets.NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(t.TempDir())), scanner)
			upload := func() assets.Asset {
				a, err := service.Upload(ctx, owner, assets.UploadInput{Title: "Original", Filename: "original.txt", Reader: strings.NewReader("Licensed original content."), RequestID: "scan-execution"})
				if err != nil {
					t.Fatal(err)
				}
				return a
			}
			a := upload()
			job := claimAssetJobKind(t, ctx, pool, "scan-old", assets.ScanJobKind)
			expire := func() {
				if _, err := pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			var next jobs.Job
			switch mode {
			case "expired_before_start":
				expire()
			case "expired_after_scan", "reassigned_after_scan":
				scanner.scan = func() (media.ScanResult, error) {
					expire()
					if mode == "reassigned_after_scan" {
						if err := jobs.NewRepository(pool).RecoverExpired(ctx); err != nil {
							t.Fatal(err)
						}
						next = claimAssetJobKind(t, ctx, pool, "scan-new", assets.ScanJobKind)
					}
					return media.ScanResult{Status: "clean", ReasonCode: "no_threat_detected", Engine: "fixture", Version: "1"}, nil
				}
			case "manual_during_scan", "renew_during_scan":
				scanner.scan = func() (media.ScanResult, error) {
					if mode == "manual_during_scan" {
						_, err := admin.NewService(pool, true).ReviewMedia(ctx, owner, a.ID, admin.MediaReview{Status: "rejected"}, "scan-review")
						if err != nil {
							return media.ScanResult{}, err
						}
					} else {
						renewCtx, cancel := context.WithTimeout(ctx, time.Second)
						defer cancel()
						if err := jobs.NewRepository(pool).Renew(renewCtx, job, "scan-old", time.Minute); err != nil {
							return media.ScanResult{}, err
						}
					}
					return media.ScanResult{Status: "clean", ReasonCode: "no_threat_detected", Engine: "fixture", Version: "1"}, nil
				}
			case "foreign_payload":
				a = upload()
				job.Payload, _ = json.Marshal(map[string]any{"assetId": a.ID})
			case "wrong_kind":
				job.Kind = "generation.generate"
			case "forged_counters":
				job.Attempts = 100
				job.MaxAttempts = 1
				scanner.scan = func() (media.ScanResult, error) { return media.ScanResult{}, errors.New("temporary fixture outage") }
			}
			err := service.HandleScanJob(ctx, job)
			if mode == "forged_counters" {
				if err == nil || !jobs.ShouldRetry(err) {
					t.Fatalf("forged counters ended scan: %v", err)
				}
			} else if mode == "manual_during_scan" || mode == "renew_during_scan" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, jobs.ErrLeaseLost) {
				t.Errorf("invalid execution accepted: %v", err)
			}
			var status string
			var audits int
			if err := pool.QueryRow(ctx, `SELECT scan_status,(SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='asset.scan_completed') FROM assets WHERE id=$1`, a.ID).Scan(&status, &audits); err != nil {
				t.Fatal(err)
			}
			expectedStatus, expectedAudits := "pending", 0
			if mode == "manual_during_scan" {
				expectedStatus = "rejected"
			}
			if mode == "renew_during_scan" {
				expectedStatus = "clean"
				expectedAudits = 1
			}
			if status != expectedStatus || audits != expectedAudits {
				t.Errorf("invalid execution changed asset: status=%s audits=%d", status, audits)
			}
			if (mode == "expired_before_start" || mode == "foreign_payload" || mode == "wrong_kind") && scanner.calls != 0 {
				t.Errorf("invalid execution sent private bytes to scanner: %d", scanner.calls)
			}
			if mode == "reassigned_after_scan" {
				scanner.scan = nil
				if err := service.HandleScanJob(ctx, next); err != nil {
					t.Fatal(err)
				}
				if err := jobs.NewRepository(pool).Complete(ctx, next, "scan-new"); err != nil {
					t.Fatal(err)
				}
				if err := pool.QueryRow(ctx, `SELECT scan_status FROM assets WHERE id=$1`, a.ID).Scan(&status); err != nil || status != "clean" {
					t.Fatalf("current execution failed: %s %v", status, err)
				}
			}
		})
	}
}

func TestScanExecutionCrashRecovery(t *testing.T) {
	for _, mode := range []string{"last_crash", "retries_left", "permanent_failure", "missing_attempt", "unrelated_failed_job", "manual_review", "transaction_failure", "concurrent_recovery", "deleted_owner"} {
		t.Run(mode, func(t *testing.T) {
			pool, cleanup := assetTestPool(t)
			defer cleanup()
			ctx := t.Context()
			owner := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Scan owner','creator')`, owner, owner.String()+"@test.local", "scan_"+owner.String()[:8]); err != nil {
				t.Fatal(err)
			}
			scanner := &executionScanner{}
			service := assets.NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(t.TempDir())), scanner)
			a, err := service.Upload(ctx, owner, assets.UploadInput{Title: "Original", Filename: "original.txt", Reader: strings.NewReader("Licensed original content."), RequestID: "scan-recovery"})
			if err != nil {
				t.Fatal(err)
			}
			if mode != "retries_left" && mode != "permanent_failure" {
				if _, err = pool.Exec(ctx, `UPDATE jobs SET max_attempts=1 WHERE kind='asset.scan'`); err != nil {
					t.Fatal(err)
				}
			}
			job := claimAssetJobKind(t, ctx, pool, "scan-crashed", assets.ScanJobKind)
			repo := jobs.NewRepository(pool)
			switch mode {
			case "missing_attempt":
				// Failed state with no finished attempt must not be promoted into evidence.
				_, err = pool.Exec(ctx, `UPDATE jobs SET status='failed',lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,last_error_code='handler_failed' WHERE id=$1`, job.ID)
			case "permanent_failure":
				err = repo.Fail(ctx, job, "scan-crashed", permanentScanFailure{})
			case "unrelated_failed_job":
				_, err = pool.Exec(ctx, `INSERT INTO jobs(kind,payload,status,max_attempts) VALUES('asset.scan',$1,'failed',1)`, job.Payload)
			default:
				_, err = pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID)
				if err == nil {
					err = repo.RecoverExpired(ctx)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "manual_review" {
				if _, err = admin.NewService(pool, true).ReviewMedia(ctx, owner, a.ID, admin.MediaReview{Status: "rejected"}, "scan-review"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "transaction_failure" {
				if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_scan_recovery_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='asset.scan_recovered' THEN RAISE EXCEPTION 'fixture audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_scan_recovery_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_scan_recovery_audit()`); err != nil {
					t.Fatal(err)
				}
				n, recoverErr := service.ReconcileScanExecutions(ctx, 100)
				if recoverErr == nil || n != 0 {
					t.Fatalf("failed audit committed recovery: %d %v", n, recoverErr)
				}
				var status string
				var count int
				if err = pool.QueryRow(ctx, `SELECT scan_status,(SELECT count(*) FROM notifications WHERE resource_id=$1) FROM assets WHERE id=$1`, a.ID).Scan(&status, &count); err != nil || status != "pending" || count != 0 {
					t.Fatalf("partial recovery: %s %d %v", status, count, err)
				}
				var checks int
				var code string
				var backedOff bool
				if err = pool.QueryRow(ctx, `SELECT recovery_checks,last_error_code,recovery_after>clock_timestamp() FROM asset_scan_executions WHERE asset_id=$1`, a.ID).Scan(&checks, &code, &backedOff); err != nil || checks != 1 || code != "scan_recovery_failed" || !backedOff {
					t.Fatalf("failed recovery not backed off: %d %s %t %v", checks, code, backedOff, err)
				}
				if _, err = pool.Exec(ctx, `DROP TRIGGER reject_scan_recovery_audit ON audit_events; UPDATE asset_scan_executions SET recovery_after=now()`); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "deleted_owner" {
				if _, err = pool.Exec(ctx, `UPDATE users SET status='deleted' WHERE id=$1`, owner); err != nil {
					t.Fatal(err)
				}
			}
			var n int
			if mode == "concurrent_recovery" {
				type outcome struct {
					n   int
					err error
				}
				results := make(chan outcome, 2)
				for range 2 {
					go func() { count, e := service.ReconcileScanExecutions(ctx, 100); results <- outcome{count, e} }()
				}
				for range 2 {
					r := <-results
					n += r.n
					if r.err != nil {
						err = r.err
					}
				}
			} else {
				n, err = service.ReconcileScanExecutions(ctx, 100)
			}
			expected := 1
			statusExpected := "review"
			if mode == "retries_left" || mode == "missing_attempt" || mode == "unrelated_failed_job" {
				expected = 0
				statusExpected = "pending"
			}
			if mode == "manual_review" {
				expected = 0
				statusExpected = "rejected"
			}
			if err != nil || n != expected {
				t.Fatalf("reconcile: %d expected %d err=%v", n, expected, err)
			}
			var status string
			var audits, notices int
			if err = pool.QueryRow(ctx, `SELECT scan_status,(SELECT count(*) FROM audit_events WHERE action='asset.scan_recovered' AND resource_id=$1),(SELECT count(*) FROM notifications WHERE resource_id=$1 AND source_key LIKE 'asset-scan:%') FROM assets WHERE id=$1`, a.ID).Scan(&status, &audits, &notices); err != nil {
				t.Fatal(err)
			}
			noticeExpected := expected
			if mode == "deleted_owner" {
				noticeExpected = 0
			}
			if status != statusExpected || audits != expected || notices != noticeExpected {
				t.Fatalf("wrong recovery: %s audits=%d notices=%d", status, audits, notices)
			}
			if n, err = service.ReconcileScanExecutions(ctx, 100); err != nil || n != 0 {
				t.Fatalf("recovery duplicated: %d %v", n, err)
			}
			if scanner.calls != 0 {
				t.Fatal("recovery called external scanner")
			}
			if _, err = service.Content(ctx, owner, a.ID); !errors.Is(err, assets.ErrNotFound) {
				t.Fatalf("unverified recovered asset readable: %v", err)
			}
		})
	}
}

func TestScanExecutionRechecksLeaseAfterLockWait(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Scan owner','creator')`, owner, owner.String()+"@test.local", "scan_"+owner.String()[:8]); err != nil {
		t.Fatal(err)
	}
	scanner := &executionScanner{}
	service := assets.NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(t.TempDir())), scanner)
	a, err := service.Upload(ctx, owner, assets.UploadInput{Title: "Original", Filename: "original.txt", Reader: strings.NewReader("Licensed original content."), RequestID: "scan-wait"})
	if err != nil {
		t.Fatal(err)
	}
	job := claimAssetJobKind(t, ctx, pool, "scan-wait", assets.ScanJobKind)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var pid int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM jobs WHERE id=$1 FOR UPDATE`, job.ID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- service.HandleScanJob(ctx, job) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan did not wait for execution lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("wait used stale lease time: %v", err)
	}
	var status string
	if err = pool.QueryRow(ctx, `SELECT scan_status FROM assets WHERE id=$1`, a.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("expired waiter changed asset: %s %v", status, err)
	}
}

func TestScanExecutionMigrationAndProtocol(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	up, err := os.ReadFile("../platform/database/migrations/0121_asset_scan_execution_recovery.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0121_asset_scan_execution_recovery.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	run := func(sql string) error {
		tx, e := pool.Begin(ctx)
		if e != nil {
			return e
		}
		defer tx.Rollback(ctx)
		if _, e = tx.Exec(ctx, sql); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if err = run(string(down)); err != nil {
		t.Fatal(err)
	}
	repo := jobs.NewRepository(pool)
	if _, err = repo.Enqueue(ctx, "fixture.drain", nil); err != nil {
		t.Fatal(err)
	}
	running, err := repo.Claim(ctx, "migration", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(string(up)); err == nil || !strings.Contains(err.Error(), "drain running jobs") {
		t.Fatalf("migration accepted active worker: %v", err)
	}
	if err = repo.Complete(ctx, running, "migration"); err != nil {
		t.Fatal(err)
	}
	owner := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Scan owner','creator')`, owner, owner.String()+"@test.local", "scan_"+owner.String()[:8]); err != nil {
		t.Fatal(err)
	}
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for index, id := range ids {
		if _, err = pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES($1,$2,'document','Historical original','/private','text/plain','pending','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text)`, id, owner); err != nil {
			t.Fatal(err)
		}
		for range index {
			if _, err = repo.Enqueue(ctx, assets.ScanJobKind, map[string]any{"assetId": id}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = repo.Enqueue(ctx, assets.ScanJobKind, map[string]string{"assetId": "malformed"}); err != nil {
		t.Fatal(err)
	}
	if err = run(string(up)); err != nil {
		t.Fatal(err)
	}
	var id, jobID uuid.UUID
	var count int
	if err = pool.QueryRow(ctx, `SELECT asset_id,job_id,(SELECT count(*) FROM asset_scan_executions) FROM asset_scan_executions`).Scan(&id, &jobID, &count); err != nil || id != ids[1] || count != 1 {
		t.Fatalf("ambiguous backfill: %s %d %v", id, count, err)
	}
	for _, query := range []string{
		`UPDATE asset_scan_executions SET job_id=gen_random_uuid()`,
		`DELETE FROM asset_scan_executions`,
		`UPDATE jobs SET payload='{}' WHERE id IN (SELECT job_id FROM asset_scan_executions)`,
		`SELECT set_config('app.asset_scan_execution_protocol','',true); UPDATE assets SET scan_status='clean' WHERE source_type='upload'`,
		`SELECT set_config('app.asset_scan_execution_protocol','',true); UPDATE jobs SET status='running' WHERE kind='asset.scan'`,
	} {
		if err = run(query); err == nil {
			t.Fatalf("invalid mutation allowed: %s", query)
		}
	}
	if err = run(string(down)); err == nil || !strings.Contains(err.Error(), "rollback refused") {
		t.Fatalf("migration discarded evidence: %v", err)
	}
}
