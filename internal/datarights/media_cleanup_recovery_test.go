package datarights_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func cleanupUser(t *testing.T, pool *pgxpool.Pool, role, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	handle := "cleanup_" + strings.ReplaceAll(id.String(), "-", "")[:15]
	if _, err := pool.Exec(context.Background(), `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Cleanup fixture',$4,$5)`, id, handle+"@test.local", handle, role, status); err != nil {
		t.Fatal(err)
	}
	return id
}
func cleanupFailedJob(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `INSERT INTO jobs(kind,payload,status,attempts,max_attempts,last_error,last_error_code)
 VALUES($1,jsonb_build_object('userId',$2::text),'failed',20,20,'SECRET: object-store/path/must-not-leak','handler_failed') RETURNING id`, datarights.MediaCleanupJobKind, user).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func cleanupInput() datarights.MediaCleanupRetryInput {
	attempts := 20
	return datarights.MediaCleanupRetryInput{ExpectedAttempts: &attempts, Reason: "Storage credentials have been repaired and verified.", Confirmed: true}
}

func TestMediaCleanupPolicyPreservesConnectionSettingsAndResponse(t *testing.T) {
	base, cleanup := dataRightsTestPool(t)
	t.Cleanup(cleanup)
	config := base.Config()
	config.MaxConns = 1
	config.MinConns = 0
	config.ConnConfig.RuntimeParams["jit"] = "on"
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	actor := cleanupUser(t, pool, "admin", "active")
	owner := cleanupUser(t, pool, "member", "deleted")
	original := cleanupFailedJob(t, pool, owner)
	service := datarights.NewService(pool, t.TempDir())
	assertSettings := func() {
		t.Helper()
		var jit string
		if err := pool.QueryRow(ctx, `SHOW jit`).Scan(&jit); err != nil || jit != "on" {
			t.Fatalf("cleanup changed the pooled connection setting: %q %v", jit, err)
		}
	}
	assertSettings()
	if page, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{}); err != nil || len(page.Items) != 1 || !page.Items[0].CanRetry {
		t.Fatalf("failed job eligibility: %+v %v", page, err)
	}
	assertSettings()
	stale := cleanupInput()
	*stale.ExpectedAttempts = 19
	if _, err := service.RetryMediaCleanup(ctx, actor, original, stale, "stale-policy"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatalf("stale recovery: %v", err)
	}
	assertSettings() // The full policy was read, then its transaction rolled back.
	next, err := service.RetryMediaCleanup(ctx, actor, original, cleanupInput(), "valid-policy")
	if err != nil {
		t.Fatal(err)
	}
	assertSettings()
	page, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Status: "all"})
	if err != nil {
		t.Fatal(err)
	}
	assertSettings()
	found := false
	for _, item := range page.Items {
		if item.ID != next.ID {
			continue
		}
		found = true
		response, err := json.Marshal(next)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := json.Marshal(item)
		if err != nil || string(response) != string(stored) {
			t.Fatalf("recovery response differs from queue projection: %s / %s (%v)", response, stored, err)
		}
	}
	if !found {
		t.Fatal("replacement missing from queue")
	}
}

func TestMediaCleanupRecoveryRejectsRevokedOperatorBeforeEnqueue(t *testing.T) {
	for _, phase := range []string{"job_lock", "subject_lock", "enqueue"} {
		for _, change := range []string{"role", "suspended", "permission"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				pool, cleanup := dataRightsTestPool(t)
				t.Cleanup(cleanup)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				t.Cleanup(cancel)
				actor := cleanupUser(t, pool, "admin", "active")
				owner := cleanupUser(t, pool, "member", "deleted")
				original := cleanupFailedJob(t, pool, owner)
				service := datarights.NewService(pool, t.TempDir())
				var wait, release func()
				if phase == "enqueue" {
					traced, entered, resume := testutil.GateQuery(t, pool, "INSERT INTO jobs(kind,payload,max_attempts)")
					service = datarights.NewService(traced, t.TempDir())
					wait = func() {
						select {
						case <-entered:
						case <-ctx.Done():
							t.Fatal("recovery did not reach enqueue", ctx.Err())
						}
					}
					release = resume
				} else {
					gate, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = gate.Rollback(context.Background()) })
					query, id := `SELECT id FROM jobs WHERE id=$1 FOR UPDATE`, original
					if phase == "subject_lock" {
						query, id = `SELECT id FROM users WHERE id=$1 FOR UPDATE`, owner
					}
					if _, err := gate.Exec(ctx, query, id); err != nil {
						t.Fatal(err)
					}
					wait = func() { waitDeletionBlocker(t, ctx, pool, gate.Conn().PgConn().PID()) }
					release = func() {
						if err := gate.Commit(ctx); err != nil {
							t.Fatal(err)
						}
					}
				}
				done := make(chan error, 1)
				go func() {
					_, err := service.RetryMediaCleanup(ctx, actor, original, cleanupInput(), "revoked-cleanup")
					done <- err
				}()
				wait()
				var err error
				switch change {
				case "role":
					_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
				case "suspended":
					_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor)
				case "permission":
					_, err = pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:data-rights'`)
				}
				if err != nil {
					t.Fatal(err)
				}
				release()
				if err = <-done; !errors.Is(err, datarights.ErrCleanupForbidden) {
					t.Fatalf("revoked operator enqueued cleanup: %v", err)
				}
				var status string
				var attempts, queued, recoveries, audits int
				if err := pool.QueryRow(ctx, `SELECT status,attempts,
 (SELECT count(*) FROM jobs WHERE kind=$2 AND status='queued'),
 (SELECT count(*) FROM media_cleanup_recoveries),
 (SELECT count(*) FROM audit_events WHERE action='data_rights.media_cleanup_retried')
 FROM jobs WHERE id=$1`, original, datarights.MediaCleanupJobKind).Scan(&status, &attempts, &queued, &recoveries, &audits); err != nil || status != "failed" || attempts != 20 || queued != 0 || recoveries != 0 || audits != 0 {
					t.Fatalf("revoked recovery changed state: %s %d %d %d %d %v", status, attempts, queued, recoveries, audits, err)
				}
			})
		}
	}
}

func TestMediaCleanupRecoveryEvidenceAndConcurrency(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := cleanupUser(t, pool, "admin", "active")
	owner := cleanupUser(t, pool, "member", "deleted")
	original := cleanupFailedJob(t, pool, owner)
	other := cleanupFailedJob(t, pool, owner)
	service := datarights.NewService(pool, t.TempDir())
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for _, id := range []uuid.UUID{original, original, other, other} {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			_, err := service.RetryMediaCleanup(ctx, actor, id, cleanupInput(), "cleanup-concurrent")
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, datarights.ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("duplicate subject recovery: %d", success)
	}
	page, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Status: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("lost original evidence: %#v", page)
	}
	var replacement datarights.MediaCleanup
	for _, item := range page.Items {
		if item.Status == "queued" {
			replacement = item
		}
		if item.CanRetry {
			t.Fatal("retry enabled while a job is active")
		}
	}
	if replacement.RetryOf == nil || replacement.Attempts != 0 || replacement.MaxAttempts != 20 {
		t.Fatal("replacement missing lineage", replacement)
	}
	var evidence, audit int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_cleanup_recoveries),(SELECT count(*) FROM audit_events WHERE action='data_rights.media_cleanup_retried')`).Scan(&evidence, &audit); err != nil || evidence != 1 || audit != 1 {
		t.Fatal("missing recovery evidence", err, evidence, audit)
	}
	body, _ := json.Marshal(page)
	if strings.Contains(string(body), "SECRET") || strings.Contains(string(body), "object-store") {
		t.Fatal("raw storage error leaked")
	}
	for _, sql := range []string{`UPDATE media_cleanup_recoveries SET reason='overwritten'`, `DELETE FROM media_cleanup_recoveries`} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Fatal("recovery evidence mutable")
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0088_media_cleanup_recovery.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard media cleanup recovery evidence") {
		t.Fatal("rollback evidence lost", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=20 WHERE id=$1`, replacement.ID); err != nil {
		t.Fatal(err)
	}
	next, err := service.RetryMediaCleanup(ctx, actor, replacement.ID, cleanupInput(), "cleanup-next")
	if err != nil || next.RetryOf == nil || *next.RetryOf != replacement.ID {
		t.Fatal("new failure cannot recover", err)
	}
	if _, err := service.RetryMediaCleanup(ctx, actor, *replacement.RetryOf, cleanupInput(), "cleanup-old"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatal("original job recovered twice", err)
	}
}

func TestMediaCleanupRecoveryGuardsAndPagination(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := cleanupUser(t, pool, "admin", "active")
	member := cleanupUser(t, pool, "member", "active")
	owner := cleanupUser(t, pool, "member", "deleted")
	jobID := cleanupFailedJob(t, pool, owner)
	service := datarights.NewService(pool, t.TempDir())
	if _, err := service.RetryMediaCleanup(ctx, member, jobID, cleanupInput(), "member"); !errors.Is(err, datarights.ErrCleanupForbidden) {
		t.Fatal("member retry allowed", err)
	}
	for _, scenario := range []string{"unconfirmed", "no_attempts", "stale", "short_reason", "invalid_utf8", "nul"} {
		input := cleanupInput()
		switch scenario {
		case "unconfirmed":
			input.Confirmed = false
		case "no_attempts":
			input.ExpectedAttempts = nil
		case "stale":
			attempts := 19
			input.ExpectedAttempts = &attempts
		case "short_reason":
			input.Reason = "short"
		case "invalid_utf8":
			input.Reason = string([]byte{0xff}) + strings.Repeat("x", 20)
		case "nul":
			input.Reason = "reason has \x00 null"
		}
		if _, err := service.RetryMediaCleanup(ctx, actor, jobID, input, "invalid"); err == nil {
			t.Fatal("invalid recovery", scenario)
		}
	}
	for _, status := range []string{"queued", "running", "succeeded", "cancelled"} {
		if _, err := pool.Exec(ctx, `UPDATE jobs SET status=$2 WHERE id=$1`, jobID, status); err != nil {
			t.Fatal(err)
		}
		if _, err := service.RetryMediaCleanup(ctx, actor, jobID, cleanupInput(), "not-failed"); !errors.Is(err, datarights.ErrConflict) {
			t.Fatal("nonfailed retried", status, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	activeJob := cleanupFailedJob(t, pool, member)
	if _, err := service.RetryMediaCleanup(ctx, actor, activeJob, cleanupInput(), "active-owner"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatal("active owner cleanup allowed", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at) VALUES($1,$2,'Preserve the required records.',repeat('a',64),now()+interval '1 day',now()+interval '2 days')`, owner, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetryMediaCleanup(ctx, actor, jobID, cleanupInput(), "held-owner"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatal("held owner cleanup allowed", err)
	}
	for i := 0; i < 3; i++ {
		cleanupFailedJob(t, pool, owner)
	}
	var got []uuid.UUID
	cursor := ""
	for {
		page, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Status: "failed", ListInput: datarights.ListInput{Limit: 2, Cursor: cursor}})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		if _, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Status: "all", ListInput: datarights.ListInput{Cursor: cursor}}); !errors.Is(err, datarights.ErrInvalidList) {
			t.Fatal("cross-filter cursor accepted", err)
		}
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatal("duplicate page item")
		}
		seen[id] = true
	}
	if len(got) != 5 {
		t.Fatal("missing jobs", len(got))
	}
	cross, _ := json.Marshal(map[string]any{"at": time.Now(), "id": uuid.New(), "scope": "other", "status": "failed"})
	for _, input := range []datarights.MediaCleanupListInput{{Status: "bad"}, {ListInput: datarights.ListInput{Limit: 51}}, {ListInput: datarights.ListInput{Cursor: base64.RawURLEncoding.EncodeToString(cross)}}} {
		if _, err := service.ListMediaCleanups(ctx, input); !errors.Is(err, datarights.ErrInvalidList) {
			t.Fatal("invalid list accepted", err)
		}
	}
	// The worker cannot be invoked with an invented job id or another subject.
	payload, _ := json.Marshal(map[string]string{"userId": owner.String()})
	if err := service.HandleMediaCleanupJob(ctx, jobs.Job{ID: uuid.New(), Payload: payload}); !errors.Is(err, datarights.ErrInvalid) {
		t.Fatal("forged job accepted", err)
	}
	if err := service.HandleMediaCleanupJob(ctx, jobs.Job{ID: jobID, Payload: payload}); !errors.Is(err, datarights.ErrHoldCutoff) {
		t.Fatal("worker bypassed active hold", err)
	}
}

func TestMediaCleanupWaitsForConcurrentLegalHold(t *testing.T) {
	for _, operation := range []string{"recover", "worker"} {
		t.Run(operation, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			actor := cleanupUser(t, pool, "admin", "active")
			owner := cleanupUser(t, pool, "member", "deleted")
			jobID := cleanupFailedJob(t, pool, owner)
			service := datarights.NewService(pool, t.TempDir())
			// Hold creation locks the subject before adding the hold. Pause that
			// transaction at its linearization point while cleanup starts.
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			var pid int
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM users WHERE id=$1 FOR UPDATE`, owner).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				if operation == "recover" {
					_, err := service.RetryMediaCleanup(ctx, actor, jobID, cleanupInput(), "concurrent-hold")
					finished <- err
					return
				}
				payload, _ := json.Marshal(map[string]string{"userId": owner.String()})
				finished <- service.HandleMediaCleanupJob(ctx, jobs.Job{ID: jobID, Payload: payload})
			}()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-finished:
					t.Fatal("cleanup did not wait for hold creation", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at) VALUES($1,$2,'Preserve these files.',repeat('b',64),now()+interval '1 day',now()+interval '2 days')`, owner, actor); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			want := datarights.ErrConflict
			if operation == "worker" {
				want = datarights.ErrHoldCutoff
			}
			select {
			case err := <-finished:
				if !errors.Is(err, want) {
					t.Fatal("cleanup missed committed hold", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var queued int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_cleanup_recoveries`).Scan(&queued); err != nil || queued != 0 {
				t.Fatal("held subject was recovered", queued, err)
			}
		})
	}
}

func TestMediaCleanupKindsAndOrphanedJobs(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := cleanupUser(t, pool, "admin", "active")
	owner := cleanupUser(t, pool, "member", "deleted")
	account := cleanupFailedJob(t, pool, owner)
	var orphan uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status,attempts,max_attempts) VALUES('product.delivery_cleanup',jsonb_build_object('orderId',$1::text),'failed',20,20) RETURNING id`, uuid.New()).Scan(&orphan); err != nil {
		t.Fatal(err)
	}
	service := datarights.NewService(pool, t.TempDir())
	all, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{ListInput: datarights.ListInput{Limit: 1}})
	if err != nil || len(all.Items) != 1 || all.NextCursor == nil {
		t.Fatalf("mixed queue pagination: %+v %v", all, err)
	}
	for _, kind := range []string{"product", "account"} {
		if _, err = service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Kind: kind, ListInput: datarights.ListInput{Cursor: *all.NextCursor}}); !errors.Is(err, datarights.ErrInvalidList) {
			t.Fatalf("cross-kind cursor accepted: %v", err)
		}
		page, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Kind: kind})
		if err != nil || len(page.Items) != 1 || page.Items[0].Kind != kind {
			t.Fatalf("kind filter: %+v %v", page, err)
		}
		if kind == "product" && (page.Items[0].ID != orphan || page.Items[0].CanRetry || page.Items[0].UnavailableReason != "missing_subject" || page.Items[0].OrderID != nil || page.Items[0].UserID != nil) {
			t.Fatalf("orphan made actionable: %+v", page)
		}
		if kind == "account" && (page.Items[0].ID != account || !page.Items[0].CanRetry) {
			t.Fatalf("account lost: %+v", page)
		}
	}
	if _, err = service.RetryMediaCleanup(ctx, actor, orphan, cleanupInput(), "orphan"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatalf("orphan recovery accepted: %v", err)
	}
	if _, err = service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Kind: "unknown"}); !errors.Is(err, datarights.ErrInvalidList) {
		t.Fatal("unknown kind accepted")
	}
}
