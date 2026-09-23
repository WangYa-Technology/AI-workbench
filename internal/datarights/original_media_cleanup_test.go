package datarights_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5/pgxpool"
)

type originalFixture struct {
	service               *datarights.Service
	owner, request, asset uuid.UUID
	root, key             string
}

func completedOriginalFixture(t *testing.T, pool *pgxpool.Pool) originalFixture {
	t.Helper()
	ctx := context.Background()
	owner, token := deletionOwner(t, pool)
	f := originalFixture{owner: owner.ID, root: t.TempDir(), asset: uuid.New()}
	f.key = f.asset.String() + ".txt"
	f.service = datarights.NewService(pool, f.root)
	r, err := f.service.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: owner.Handle}, "original-media-fixture")
	if err != nil {
		t.Fatal(err)
	}
	f.request = r.ID
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 day',cancel_until=now()-interval '1 day' WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"requestId": r.ID})
	if err := f.service.HandleDeletionJob(ctx, jobs.Job{Payload: raw}); err != nil {
		t.Fatal(err)
	}
	// Import a historical managed location after constructing real completion
	// evidence. Never erase a receipt or job attempt to manufacture missing work.
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,storage_backend,storage_key)
 VALUES($1,$2,'document','Historical retained original','/private-original','text/plain','rejected','upload','local_file',$3)`, f.asset, f.owner, f.key); err != nil {
		t.Fatal(err)
	}
	if err := media.NewLocalStore(f.root).Put(ctx, f.key, []byte("historical retained bytes"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now()+interval '1 day' WHERE status='queued'`); err != nil {
		t.Fatal(err)
	}
	return f
}

func originalJob(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID) jobs.Job {
	t.Helper()
	var job jobs.Job
	if err := pool.QueryRow(context.Background(), `SELECT j.id,j.payload FROM jobs j JOIN original_media_cleanup_reconciliations r ON r.job_id=j.id WHERE r.user_id=$1 ORDER BY r.created_at DESC,r.job_id DESC LIMIT 1`, owner).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestOriginalMediaReconciliationAtomicDispatchAndVerifiedReceipt(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := completedOriginalFixture(t, pool)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_original_cleanup_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.action='data_rights.original_media_cleanup_reconciled' THEN RAISE EXCEPTION 'private audit outage'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_original_cleanup_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_original_cleanup_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReconcileOriginalMediaCleanups(ctx, 100); err == nil {
		t.Fatal("expected audit rollback")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM original_media_cleanup_reconciliations)+(SELECT count(*) FROM jobs WHERE kind=$1)`, datarights.MediaCleanupJobKind).Scan(&n); err != nil || n != 0 {
		t.Fatal("non-atomic dispatch", n, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_original_cleanup_audit ON audit_events; DROP FUNCTION fail_original_cleanup_audit()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := datarights.NewService(pool, f.root).ReconcileOriginalMediaCleanups(ctx, 100)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM original_media_cleanup_reconciliations`).Scan(&n); err != nil || n != 1 {
		t.Fatal("duplicate dispatch", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM original_media_cleanup_receipts`).Scan(&n); err != nil || n != 0 {
		t.Fatal("dispatch forged physical completion", n, err)
	}
	expected := originalJob(t, pool, f.owner)
	repo := jobs.NewRepository(pool)
	job, err := repo.Claim(ctx, "original-cleanup", time.Minute)
	if err != nil || job.ID != expected.ID {
		t.Fatal("wrong job", job.ID, err)
	}
	if err := f.service.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(ctx, job, "original-cleanup"); err != nil {
		t.Fatal(err)
	}
	if _, err := media.NewLocalStore(f.root).Stat(ctx, f.key); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("file remains", err)
	}
	var outcome, keyHash string
	var size int64
	var receiptJob uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT outcome,storage_key_sha256,size_bytes,job_id FROM original_media_cleanup_receipts WHERE owner_id=$1`, f.owner).Scan(&outcome, &keyHash, &size, &receiptJob); err != nil || outcome != "removed" || size != int64(len("historical retained bytes")) || receiptJob != job.ID || len(keyHash) != 64 {
		t.Fatal("incorrect receipt", outcome, size, err)
	}
	if err := f.service.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM original_media_cleanup_receipts`).Scan(&n); err != nil || n != 1 {
		t.Fatal("duplicate receipt", n, err)
	}
	if n, err := datarights.NewService(pool, f.root).ReconcileOriginalMediaCleanups(ctx, 100); err != nil || n != 0 {
		t.Fatal("already verified location requeued", n, err)
	}
	for _, sql := range []string{`UPDATE original_media_cleanup_receipts SET outcome='already_absent'`, `DELETE FROM original_media_cleanup_receipts`, `DELETE FROM original_media_cleanup_reconciliations`, `UPDATE original_media_cleanup_reconciliations SET created_at=now()`} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Fatal("mutable cleanup evidence", sql)
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0105_original_media_cleanup.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard original media cleanup evidence") {
		t.Fatal("unsafe rollback", err)
	}
}

func TestOriginalMediaReconciliationEligibility(t *testing.T) {
	cases := map[string]string{
		"completed_before_execution":             `UPDATE data_rights_requests SET execute_after=completed_at+interval '1 microsecond' WHERE user_id=$1`,
		"completed_before_cancellation_deadline": `UPDATE data_rights_requests SET cancel_until=completed_at+interval '1 microsecond' WHERE user_id=$1`,
		"mismatched_completion_time":             `UPDATE data_rights_requests SET completed_at=completed_at+interval '1 minute' WHERE user_id=$1`,
		"mismatched_subject":                     `UPDATE data_rights_requests SET subject_ref='subject_000000000000000000000000' WHERE user_id=$1`,
		"active_account":                         `UPDATE users SET status='active' WHERE id=$1`,
		"missing_completion":                     `UPDATE data_rights_requests SET completed_at=NULL WHERE user_id=$1`,
		"request_cancelled":                      `UPDATE data_rights_requests SET status='cancelled' WHERE user_id=$1`,
		"missing_deadline":                       `UPDATE data_rights_requests SET cancel_until=NULL WHERE user_id=$1`,
		"future_deadline":                        `UPDATE data_rights_requests SET execute_after=now()+interval '1 day' WHERE user_id=$1`,
		"held":                                   `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at) VALUES($1,$1,'Preserve original evidence',repeat('e',64),now()+interval '1 day',now()+interval '2 days')`,
		"queued":                                 `INSERT INTO jobs(kind,payload,status,max_attempts) VALUES('data_rights.media_cleanup',jsonb_build_object('userId',$1::text),'queued',20)`,
		"running":                                `INSERT INTO jobs(kind,payload,status,max_attempts) VALUES('data_rights.media_cleanup',jsonb_build_object('userId',$1::text),'running',20)`,
		"failed":                                 `INSERT INTO jobs(kind,payload,status,max_attempts) VALUES('data_rights.media_cleanup',jsonb_build_object('userId',$1::text),'failed',20)`,
		"cancelled":                              `INSERT INTO jobs(kind,payload,status,max_attempts) VALUES('data_rights.media_cleanup',jsonb_build_object('userId',$1::text),'cancelled',20)`,
	}
	for name, sql := range cases {
		t.Run(name, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			f := completedOriginalFixture(t, pool)
			ctx := context.Background()
			if _, err := pool.Exec(ctx, sql, f.owner); err != nil {
				t.Fatal(err)
			}
			if n, err := f.service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil || n != 0 {
				t.Fatal("ineligible cleanup dispatched", n, err)
			}
			if _, err := media.NewLocalStore(f.root).Stat(ctx, f.key); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type originalCleanupStore struct {
	media.Store
	mode string
}

func (s *originalCleanupStore) Delete(ctx context.Context, key string) error {
	if s.mode == "failure" {
		return errors.New("private storage failure")
	}
	if s.mode == "noop" {
		return nil
	}
	return s.Store.Delete(ctx, key)
}

func TestOriginalMediaReceiptRequiresVerifiedAbsenceAndCommittedAudit(t *testing.T) {
	for _, mode := range []string{"failure", "noop", "audit_failure", "already_absent", "root_missing", "root_replaced"} {
		t.Run(mode, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			f := completedOriginalFixture(t, pool)
			if _, err := f.service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil {
				t.Fatal(err)
			}
			job := originalJob(t, pool, f.owner)
			store := &originalCleanupStore{Store: media.NewLocalStore(f.root), mode: mode}
			service := datarights.NewServiceWithMedia(pool, f.root, media.NewCatalog(store))
			var moved string
			if mode == "root_missing" || mode == "root_replaced" {
				moved = filepath.Join(t.TempDir(), "offline-media")
				if err := os.Rename(f.root, moved); err != nil {
					t.Fatal(err)
				}
				if mode == "root_replaced" {
					if err := os.Mkdir(f.root, 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "audit_failure" {
				if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_original_receipt_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='data_rights.original_media_cleanup_verified' THEN RAISE EXCEPTION 'receipt audit outage'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_original_receipt_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_original_receipt_audit()`); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "already_absent" {
				if err := store.Store.Delete(ctx, f.key); err != nil {
					t.Fatal(err)
				}
			}
			err := service.HandleMediaCleanupJob(ctx, job)
			if mode != "already_absent" && err == nil {
				t.Fatal("unverified or unaudited cleanup succeeded")
			}
			if mode == "root_missing" && !errors.Is(err, media.ErrStorageUnavailable) {
				t.Fatal("storage outage was not preserved", err)
			}
			if mode == "root_replaced" && !errors.Is(err, media.ErrIntegrity) {
				t.Fatal("replacement root was not rejected", err)
			}
			var n int
			want := 0
			if mode == "already_absent" {
				want = 1
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM original_media_cleanup_receipts`).Scan(&n); err != nil || n != want {
				t.Fatal("false completion", n, err)
			}
			if mode == "audit_failure" {
				if _, err := pool.Exec(ctx, `DROP TRIGGER fail_original_receipt_audit ON audit_events; DROP FUNCTION fail_original_receipt_audit()`); err != nil {
					t.Fatal(err)
				}
			}
			store.mode = ""
			if moved != "" {
				if body, err := os.ReadFile(filepath.Join(moved, f.key)); err != nil || string(body) != "historical retained bytes" {
					t.Fatal("original changed while root unavailable", err)
				}
				if mode == "root_replaced" {
					if err := os.Remove(f.root); err != nil {
						t.Fatal("unexpected writes in replacement root", err)
					}
				}
				if err := os.Rename(moved, f.root); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.HandleMediaCleanupJob(ctx, job); err != nil {
				t.Fatal("idempotent physical recovery failed", err)
			}
			var outcome string
			if err := pool.QueryRow(ctx, `SELECT outcome FROM original_media_cleanup_receipts WHERE owner_id=$1`, f.owner).Scan(&outcome); err != nil {
				t.Fatal(err)
			}
			if (mode == "audit_failure" || mode == "already_absent") && outcome != "already_absent" {
				t.Fatal("retry invented deletion evidence", outcome)
			}
		})
	}
}

func TestOriginalMediaReconciliationEmptyMigrationRoundTrip(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	for _, suffix := range []string{"down", "up"} {
		body, err := os.ReadFile("../platform/database/migrations/0105_original_media_cleanup." + suffix + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), string(body)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOriginalMediaReconciliationRecoveryInheritsEvidence(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := completedOriginalFixture(t, pool)
	actor := cleanupUser(t, pool, "admin", "active")
	if _, err := f.service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	expected := originalJob(t, pool, f.owner)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET max_attempts=1 WHERE id=$1`, expected.ID); err != nil {
		t.Fatal(err)
	}
	repo := jobs.NewRepository(pool)
	job, err := repo.Claim(ctx, "original-recovery", time.Minute)
	if err != nil || job.ID != expected.ID {
		t.Fatal(err)
	}
	store := &originalCleanupStore{Store: media.NewLocalStore(f.root), mode: "failure"}
	service := datarights.NewServiceWithMedia(pool, f.root, media.NewCatalog(store))
	failure := service.HandleMediaCleanupJob(ctx, job)
	if failure == nil {
		t.Fatal("expected storage failure")
	}
	if err := repo.Fail(ctx, job, "original-recovery", failure); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil || n != 0 {
		t.Fatal("failed task automatically reset", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET status='cancelled' WHERE id=$1`, f.request); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{})
	if err != nil || len(page.Items) != 1 || page.Items[0].CanRetry || page.Items[0].UnavailableReason != "inconsistent_stage" {
		t.Fatal("recovery omitted completion evidence", page, err)
	}
	attempts := 1
	input := datarights.MediaCleanupRetryInput{ExpectedAttempts: &attempts, Reason: "Verify original deletion and retry failed media cleanup.", Confirmed: true}
	if _, err := service.RetryMediaCleanup(ctx, actor, job.ID, input, "test"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatal("invalid evidence recovery allowed", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET status='completed' WHERE id=$1`, f.request); err != nil {
		t.Fatal(err)
	}
	for generation := 0; generation < 2; generation++ {
		retry, err := service.RetryMediaCleanup(ctx, actor, job.ID, input, "test")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE jobs SET max_attempts=1 WHERE id=$1`, retry.ID); err != nil {
			t.Fatal(err)
		}
		job, err = repo.Claim(ctx, "original-recovery", time.Minute)
		if err != nil || job.ID != retry.ID {
			t.Fatal(err)
		}
		// Successors deliberately have no extra payload flag. The durable account
		// evidence must still prevent deletion after an inconsistent request change.
		if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()+interval '1 day' WHERE id=$1`, f.request); err != nil {
			t.Fatal(err)
		}
		store.mode = ""
		failure = service.HandleMediaCleanupJob(ctx, job)
		if !errors.Is(failure, datarights.ErrInvalid) {
			t.Fatal("successor bypassed proof", failure)
		}
		if _, err := store.Stat(ctx, f.key); err != nil {
			t.Fatal("blocked recovery deleted file", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 day' WHERE id=$1`, f.request); err != nil {
			t.Fatal(err)
		}
		if generation == 0 {
			if err := repo.Fail(ctx, job, "original-recovery", failure); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := service.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(ctx, job, "original-recovery"); err != nil {
		t.Fatal(err)
	}
	var attemptsCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_attempts WHERE job_id IN(SELECT job_id FROM original_media_cleanup_reconciliations UNION SELECT retry_job_id FROM media_cleanup_recoveries)`).Scan(&attemptsCount); err != nil || attemptsCount != 3 {
		t.Fatal("attempt evidence lost", attemptsCount, err)
	}
}

func TestOriginalMediaReconciliationFairnessAndSuccessfulPredecessor(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	first := completedOriginalFixture(t, pool)
	second := completedOriginalFixture(t, pool)
	var previous uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status,max_attempts) VALUES($1,jsonb_build_object('userId',$2::text),'succeeded',20) RETURNING id`, datarights.MediaCleanupJobKind, first.owner).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err := gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, first.owner); err != nil {
		t.Fatal(err)
	}
	if n, err := first.service.ReconcileOriginalMediaCleanups(ctx, 1); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if n, err := first.service.ReconcileOriginalMediaCleanups(ctx, 1); err != nil || n != 1 {
		t.Fatal("busy account starved later candidate", n, err)
	}
	if originalJob(t, pool, second.owner).ID == uuid.Nil {
		t.Fatal("missing later job")
	}
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := datarights.NewService(pool, first.root).ReconcileOriginalMediaCleanups(ctx, 1); err != nil || n != 1 {
		t.Fatal("restart lost skipped account", n, err)
	}
	var recorded uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT previous_job_id FROM original_media_cleanup_reconciliations WHERE user_id=$1`, first.owner).Scan(&recorded); err != nil || recorded != previous {
		t.Fatal("predecessor lost", err)
	}
}

func TestOriginalMediaCleanupBatchesAndLateHold(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := completedOriginalFixture(t, pool)
	store := media.NewLocalStore(f.root)
	for i := 0; i < 204; i++ {
		key := fmt.Sprintf("original-%03d.txt", i)
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,storage_backend,storage_key)
 VALUES($1,$2,'document','Historical original','/private-original','text/plain','rejected','upload','local_file',$3)`, uuid.New(), f.owner, key); err != nil {
			t.Fatal(err)
		}
		if err := store.Put(ctx, key, []byte("retained bytes"), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	job := originalJob(t, pool, f.owner)
	actor := cleanupUser(t, pool, "admin", "active")
	hold, err := f.service.CreateHold(ctx, actor, datarights.HoldInput{UserID: f.owner, AuthorityReference: "ORIGINAL-CLEANUP-LATE-HOLD"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.HandleMediaCleanupJob(ctx, job); !errors.Is(err, datarights.ErrHoldCutoff) {
		t.Fatal("late hold lost", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM original_media_cleanup_receipts`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if _, err := f.service.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM original_media_cleanup_receipts WHERE owner_id=$1`, f.owner).Scan(&n); err != nil || n != 205 {
		t.Fatal("batch skipped files", n, err)
	}
	for _, key := range []string{f.key, "original-000.txt", "original-099.txt", "original-100.txt", "original-203.txt"} {
		if _, err := store.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
			t.Fatal(key, err)
		}
	}
}

func TestOriginalMediaReconciliationRequiresOriginalCompletionEvidence(t *testing.T) {
	for _, missing := range []string{"requested", "deletion_prepared", "deletion_completed", "receipt"} {
		t.Run(missing, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			// Model old incomplete evidence at insertion; never remove protected
			// production history to create an eligible or ineligible fixture.
			sql := `CREATE FUNCTION omit_original_evidence() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='` + missing + `' THEN RETURN NULL; END IF; RETURN NEW; END $$;
 CREATE TRIGGER omit_original_evidence BEFORE INSERT ON data_rights_events FOR EACH ROW EXECUTE FUNCTION omit_original_evidence()`
			if missing == "receipt" {
				sql = `CREATE FUNCTION omit_original_evidence() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$;
 CREATE TRIGGER omit_original_evidence BEFORE INSERT ON data_rights_deletion_receipts FOR EACH ROW EXECUTE FUNCTION omit_original_evidence()`
			}
			if _, err := pool.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
			f := completedOriginalFixture(t, pool)
			if n, err := f.service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil || n != 0 {
				t.Fatal("missing original evidence bypassed", missing, n, err)
			}
			if _, err := media.NewLocalStore(f.root).Stat(ctx, f.key); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOriginalMediaCleanupExportPrivacy(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := completedOriginalFixture(t, pool)
	if _, err := f.service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if err := f.service.HandleMediaCleanupJob(ctx, originalJob(t, pool, f.owner)); err != nil {
		t.Fatal(err)
	}
	foreign := cleanupUser(t, pool, "member", "active")
	for _, owner := range []uuid.UUID{f.owner, foreign} {
		request, jobID := exportRecoveryFixture(t, pool, owner)
		raw, _ := json.Marshal(map[string]any{"requestId": request})
		err := f.service.HandleExportJob(ctx, jobs.Job{ID: jobID, Payload: raw})
		if owner == f.owner {
			if !errors.Is(err, datarights.ErrNotReady) {
				t.Fatal("deleted owner gained export access", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		file, _, err := f.service.OpenExport(ctx, owner, request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		var pkg struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &pkg); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"originalMediaCleanupReceipts", "originalMediaCleanupReconciliations"} {
			if strings.TrimSpace(string(pkg.Data[field])) != "[]" {
				t.Fatal("foreign cleanup evidence leaked", field, string(pkg.Data[field]))
			}
		}
		if strings.Contains(string(body), f.key) || strings.Contains(string(body), f.owner.String()) {
			t.Fatal("foreign original identifier leaked")
		}
	}
}

func TestOriginalMediaCleanupLocksJobBeforeSubjects(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := completedOriginalFixture(t, pool)
	if _, err := f.service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	job := originalJob(t, pool, f.owner)
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT id FROM jobs WHERE id=$1 FOR UPDATE`, job.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.service.HandleMediaCleanupJob(ctx, job) }()
	waitDeletionBlocker(t, ctx, pool, gate.Conn().PgConn().PID())
	_, lockErr := gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE NOWAIT`, f.owner)
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if lockErr != nil {
		t.Fatal("cleanup holds subject while waiting for receipt job FK", lockErr)
	}
}

func TestOriginalMediaSuccessfulPredecessorRetryDoesNotWaitForSubject(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	actor := cleanupUser(t, pool, "admin", "active")
	owner := cleanupUser(t, pool, "member", "deleted")
	job := cleanupFailedJob(t, pool, owner)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='succeeded' WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, owner); err != nil {
		t.Fatal(err)
	}
	_, err = datarights.NewService(pool, t.TempDir()).RetryMediaCleanup(ctx, actor, job, cleanupInput(), "test")
	if !errors.Is(err, datarights.ErrConflict) {
		t.Fatal("successful predecessor waited on subject before rejection", err)
	}
	if _, err := gate.Exec(ctx, `SELECT id FROM jobs WHERE id=$1 FOR KEY SHARE NOWAIT`, job); err != nil {
		t.Fatal("predecessor FK remains blocked", err)
	}
}
