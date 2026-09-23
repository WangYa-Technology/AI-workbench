package creation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

type lifecycleRuntime struct {
	*creation.LocalRuntime
	calls   atomic.Int64
	change  func(context.Context) error
	failure error
}

func (r *lifecycleRuntime) Generate(ctx context.Context, in creation.ProviderRequest) (creation.ProviderOutput, error) {
	r.calls.Add(1)
	if r.change != nil {
		if err := r.change(ctx); err != nil {
			return creation.ProviderOutput{}, err
		}
	}
	if r.failure != nil {
		return creation.ProviderOutput{}, r.failure
	}
	return r.LocalRuntime.Generate(ctx, in)
}

func newLifecycleRuntime(t *testing.T) *lifecycleRuntime {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"source.jpg", "local-video-test.mp4"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("isolated lifecycle fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return &lifecycleRuntime{LocalRuntime: creation.NewLocalRuntime(filepath.Join(dir, "source.jpg"))}
}

type lifecycleStore struct {
	media.Store
	puts      atomic.Int64
	beforePut func(context.Context) error
}

func (s *lifecycleStore) Put(ctx context.Context, key string, content []byte, mime string) error {
	s.puts.Add(1)
	if s.beforePut != nil {
		if err := s.beforePut(ctx); err != nil {
			return err
		}
	}
	return s.Store.Put(ctx, key, content, mime)
}

func lifecycleJob(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) jobs.Job {
	return testutil.GenerationJob(t, pool, id)
}

func lifecycleOwner(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Lifecycle creator')`, id, id.String()+"@test.local", "cycle_"+id.String()[:8]); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertInactiveGeneration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner, generation uuid.UUID) {
	t.Helper()
	var state, code, reservation string
	var balance, reserved, charged int64
	var output, asset bool
	if err := pool.QueryRow(ctx, `SELECT g.status,COALESCE(g.error_code,''),g.charged_points,g.output_text IS NOT NULL,g.output_asset_id IS NOT NULL,
  p.balance_points,p.reserved_points,r.status FROM generations g JOIN point_accounts p ON p.user_id=g.owner_id JOIN point_reservations r ON r.generation_id=g.id WHERE g.id=$1`, generation).Scan(&state, &code, &charged, &output, &asset, &balance, &reserved, &reservation); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "generation_account_unavailable" || charged != 0 || output || asset || balance != 10000 || reserved != 0 || reservation != "released" {
		t.Fatalf("inactive generation persisted/charged: %s %s %d output=%v asset=%v balance=%d reserved=%d reservation=%s", state, code, charged, output, asset, balance, reserved, reservation)
	}
	var notices, assets, charges, failures, evidenceJobs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications WHERE user_id=$1),(SELECT count(*) FROM assets WHERE owner_id=$1),
  (SELECT count(*) FROM point_entries WHERE operation_id=$2 AND entry_type='generation_charge'),
  (SELECT count(*) FROM audit_events WHERE resource_id=$2 AND action='generation.failed'),
  (SELECT count(*) FROM jobs WHERE kind=$3 AND payload->>'generationId'=$2::text)`, owner, generation, creation.FailureEvidenceJobKind).Scan(&notices, &assets, &charges, &failures, &evidenceJobs); err != nil {
		t.Fatal(err)
	}
	if notices != 0 || assets != 0 || charges != 0 || failures != 1 || evidenceJobs != 1 {
		t.Fatalf("inactive side effects: notices=%d assets=%d charges=%d failures=%d jobs=%d", notices, assets, charges, failures, evidenceJobs)
	}
}

func TestGenerationWithoutReferencesRespectsAccountLifecycle(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, status := range []string{"suspended", "deleted"} {
		for _, mode := range []string{"image", "video", "chat", "music"} {
			for _, phase := range []string{"queued", "provider_success", "provider_retry", "automatic_retry"} {
				t.Run(status+"/"+mode+"/"+phase, func(t *testing.T) {
					owner := lifecycleOwner(t, pool)
					runtime := newLifecycleRuntime(t)
					store := &lifecycleStore{Store: media.NewLocalStore(t.TempDir())}
					svc := creation.NewServiceWithMedia(pool, media.NewCatalog(store), creation.NewRuntimeCatalog(runtime))
					input := creation.SubmitInput{Mode: mode, Prompt: "Private lifecycle prompt"}
					g, err := svc.SubmitCommand(ctx, owner, input, "before-account-change", "test")
					if err != nil {
						t.Fatal(err)
					}
					change := func(ctx context.Context) error {
						_, err := pool.Exec(ctx, `UPDATE users SET status=$2 WHERE id=$1`, owner, status)
						return err
					}
					job := lifecycleJob(t, pool, g.ID)
					wantCalls := int64(0)
					switch phase {
					case "queued":
						if err := change(ctx); err != nil {
							t.Fatal(err)
						}
					case "provider_success", "provider_retry":
						runtime.change = change
						wantCalls = 1
						if phase == "provider_retry" {
							runtime.failure = creation.NewProviderFailure("provider_rate_limited", 0)
						}
					case "automatic_retry":
						runtime.failure = creation.NewProviderFailure("provider_rate_limited", 0)
						if err := svc.HandleJob(ctx, job); err == nil || !jobs.ShouldRetry(err) {
							t.Fatalf("missing initial retry: %v", err)
						}
						if err := jobs.NewRepository(pool).Fail(ctx, job, "generation-test", creation.NewProviderFailure("provider_rate_limited", 0)); err != nil {
							t.Fatal(err)
						}
						job = lifecycleJob(t, pool, g.ID)
						wantCalls = 1
						if err := change(ctx); err != nil {
							t.Fatal(err)
						}
					}
					if err := svc.HandleJob(ctx, job); !errors.Is(err, creation.ErrAccountUnavailable) || jobs.ShouldRetry(err) {
						t.Fatalf("inactive worker not terminated: %v", err)
					}
					for _, key := range []string{"before-account-change", "after-account-change"} {
						if _, err := svc.SubmitCommand(ctx, owner, input, key, "test"); !errors.Is(err, creation.ErrForbidden) {
							t.Fatalf("inactive submit/replay: %v", err)
						}
					}
					if _, err := svc.Retry(ctx, owner, g.ID, "inactive-manual-retry", "test"); !errors.Is(err, creation.ErrForbidden) {
						t.Fatalf("inactive retry: %v", err)
					}
					if _, err := svc.Cancel(ctx, owner, g.ID, "inactive-cancel-key", "test", "Cancel private work"); !errors.Is(err, creation.ErrForbidden) {
						t.Fatalf("inactive cancel: %v", err)
					}
					// Stale/concurrent handlers must neither dispatch nor recreate evidence.
					results := make(chan error, 3)
					for i := 0; i < 3; i++ {
						go func() { results <- svc.HandleJob(ctx, job) }()
					}
					for i := 0; i < 3; i++ {
						if err := <-results; err != nil {
							t.Fatal(err)
						}
					}
					evidence := job
					evidence.Kind = creation.FailureEvidenceJobKind
					if err := svc.HandleJob(ctx, evidence); err != nil {
						t.Fatal(err)
					}
					if runtime.calls.Load() != wantCalls || store.puts.Load() != 0 {
						t.Fatalf("unexpected I/O: provider=%d puts=%d", runtime.calls.Load(), store.puts.Load())
					}
					assertInactiveGeneration(t, ctx, pool, owner, g.ID)
					var count int
					if err := pool.QueryRow(ctx, `SELECT count(*) FROM generations WHERE owner_id=$1`, owner).Scan(&count); err != nil || count != 1 {
						t.Fatalf("inactive commands created work: %d %v", count, err)
					}
				})
			}
		}
	}
}

func lifecycleDeletion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, svc *datarights.Service) (uuid.UUID, jobs.Job) {
	t.Helper()
	handle := "delete_" + uuid.NewString()[:8]
	owner, token, err := identity.NewRepository(pool).Register(ctx, identity.RegisterInput{Email: handle + "@test.local", Password: "local-test-password", Handle: handle, DisplayName: "Lifecycle deletion", Locale: "en-US", Timezone: "UTC"}, identity.ClientInfo{Label: "Isolated lifecycle test", RequestID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := svc.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: owner.Handle}, "delete-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 second' WHERE id=$1`, req.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]uuid.UUID{"requestId": req.ID})
	return owner.ID, jobs.Job{Kind: datarights.DeletionJobKind, Payload: raw}
}

func assertDeletedGeneration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner, generation uuid.UUID, wantState string) {
	t.Helper()
	var state, prompt, params, userState string
	var text, reason bool
	var jobsCount, notices int
	if err := pool.QueryRow(ctx, `SELECT g.status,g.prompt,g.parameters::text,g.output_text IS NOT NULL,g.cancel_reason IS NOT NULL,u.status,
  (SELECT count(*) FROM jobs j WHERE j.kind IN ('generation.generate','creation.failure_evidence') AND j.status IN ('queued','running') AND j.payload->>'generationId'=g.id::text),
  (SELECT count(*) FROM notifications n WHERE n.user_id=g.owner_id)
  FROM generations g JOIN users u ON u.id=g.owner_id WHERE g.id=$1 AND u.id=$2`, generation, owner).Scan(&state, &prompt, &params, &text, &reason, &userState, &jobsCount, &notices); err != nil {
		t.Fatal(err)
	}
	if state != wantState || prompt != "[Deleted by account owner]" || params != "{}" || text || reason || userState != "deleted" || jobsCount != 0 || notices != 0 {
		t.Fatalf("deletion incomplete: state=%s prompt=%s params=%s text=%v reason=%v user=%s jobs=%d notices=%d", state, prompt, params, text, reason, userState, jobsCount, notices)
	}
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM data_rights_deletion_receipts r JOIN data_rights_requests q ON q.id=r.request_id WHERE q.user_id=$1`, owner).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("missing receipt: %d %v", receipts, err)
	}
}

func TestAccountDeletionDuringProviderCallPreventsLateResults(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, mode := range []string{"image", "video", "chat", "music"} {
		for _, failure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retryable=%v", mode, failure), func(t *testing.T) {
				root := t.TempDir()
				store := &lifecycleStore{Store: media.NewLocalStore(root)}
				catalog := media.NewCatalog(store)
				deletion := datarights.NewServiceWithMedia(pool, root, catalog)
				owner, deleteJob := lifecycleDeletion(t, ctx, pool, deletion)
				runtime := newLifecycleRuntime(t)
				runtime.change = func(ctx context.Context) error { return deletion.HandleDeletionJob(ctx, deleteJob) }
				if failure {
					runtime.failure = creation.NewProviderFailure("provider_rate_limited", 0)
				}
				svc := creation.NewServiceWithMedia(pool, catalog, creation.NewRuntimeCatalog(runtime))
				g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: mode, Prompt: "Private late result"}, "delete-during-provider", "test")
				if err != nil {
					t.Fatal(err)
				}
				job := claimCreationJobKind(t, ctx, pool, "obsolete-worker", creation.JobKind)
				if err = svc.HandleJob(ctx, job); err != nil {
					t.Fatal(err)
				}
				if err = svc.HandleJob(ctx, job); err != nil {
					t.Fatal(err)
				}
				assertDeletedGeneration(t, ctx, pool, owner, g.ID, "cancelled")
				if runtime.calls.Load() != 1 || store.puts.Load() != 0 {
					t.Fatalf("late provider wrote result: calls=%d puts=%d", runtime.calls.Load(), store.puts.Load())
				}
				var reserved, charges, assets, leases int
				if err = pool.QueryRow(ctx, `SELECT p.reserved_points,(SELECT count(*) FROM point_entries WHERE operation_id=$2 AND entry_type='generation_charge'),
     (SELECT count(*) FROM assets WHERE owner_id=$1),(SELECT count(*) FROM jobs WHERE kind=$3 AND payload->>'generationId'=$2::text AND (lease_owner IS NOT NULL OR lease_expires_at IS NOT NULL)) FROM point_accounts p WHERE p.user_id=$1`, owner, g.ID, creation.JobKind).Scan(&reserved, &charges, &assets, &leases); err != nil {
					t.Fatal(err)
				}
				if reserved != 0 || charges != 0 || assets != 0 || leases != 0 {
					t.Fatalf("late effects: reserved=%d charges=%d assets=%d leases=%d", reserved, charges, assets, leases)
				}
			})
		}
	}
}

func TestAccountDeletionWaitsForGenerationStorageAndCleansCommittedResult(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := t.TempDir()
	base := media.NewLocalStore(root)
	entered := make(chan struct{})
	release := make(chan struct{})
	store := &lifecycleStore{Store: base, beforePut: func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	catalog := media.NewCatalog(store)
	deletion := datarights.NewServiceWithMedia(pool, root, catalog)
	owner, deleteJob := lifecycleDeletion(t, ctx, pool, deletion)
	runtime := newLifecycleRuntime(t)
	svc := creation.NewServiceWithMedia(pool, catalog, creation.NewRuntimeCatalog(runtime))
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "image", Prompt: "Result committing during deletion"}, "write-before-deletion", "test")
	if err != nil {
		t.Fatal(err)
	}
	resultDone := make(chan error, 1)
	go func() { resultDone <- svc.HandleJob(ctx, lifecycleJob(t, pool, g.ID)) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Put is inside the lifecycle critical section; a competing operation cannot acquire it.
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	var acquired bool
	if err = gate.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, accountlifecycle.Key(owner)).Scan(&acquired); err != nil || acquired {
		t.Fatalf("result write has no lifecycle lock: %v %v", acquired, err)
	}
	if err = gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var resultPID uint32
	if err = pool.QueryRow(ctx, `SELECT pid FROM pg_locks WHERE locktype='advisory' AND granted AND objsubid=1
 AND classid::bigint=((hashtextextended($1,0)>>32)&4294967295) AND objid::bigint=(hashtextextended($1,0)&4294967295)
 AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`, accountlifecycle.Key(owner)).Scan(&resultPID); err != nil {
		t.Fatal(err)
	}
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- deletion.HandleDeletionJob(ctx, deleteJob) }()
	// Prove the real deletion handler is waiting on the generation transaction.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, resultPID).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	if err = <-resultDone; err != nil {
		t.Fatal("result", err)
	}
	if err = <-deleteDone; err != nil {
		t.Fatal("deletion", err)
	}
	assertDeletedGeneration(t, ctx, pool, owner, g.ID, "succeeded")
	if store.puts.Load() != 1 {
		t.Fatalf("unexpected writes %d", store.puts.Load())
	}
	var key string
	if err := pool.QueryRow(ctx, `SELECT storage_key FROM generation_output_writes WHERE generation_id=$1 AND status='attached'`, g.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("committed file survived deletion: %v", err)
	}
	var reserved, charges int
	if err = pool.QueryRow(ctx, `SELECT reserved_points,(SELECT count(*) FROM point_entries WHERE operation_id=$2 AND entry_type='generation_charge') FROM point_accounts WHERE user_id=$1`, owner, g.ID).Scan(&reserved, &charges); err != nil || reserved != 0 || charges != 1 {
		t.Fatalf("result billing changed: %d %d %v", reserved, charges, err)
	}
}

func TestAccountDeletionStopsBatchedJobsAndStaleFailureEvidence(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	deletion := datarights.NewService(pool, root)
	owner, deleteJob := lifecycleDeletion(t, ctx, pool, deletion)
	// More than one cleanup batch, plus failed historical work with delayed evidence.
	if _, err := pool.Exec(ctx, `INSERT INTO generations(owner_id,mode,provider,model_name,prompt,status,output_text,parameters)
 SELECT $1,'chat','local_test','hcai-local-chat-v1','Private prompt',CASE WHEN n=205 THEN 'failed' WHEN n%2=0 THEN 'running' ELSE 'queued' END,'Private response','{"responseLength":"detailed"}'::jsonb FROM generate_series(1,205) n`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(kind,payload)
 SELECT CASE WHEN status='failed' THEN 'creation.failure_evidence' ELSE 'generation.generate' END,jsonb_build_object('generationId',id) FROM generations WHERE owner_id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now()-interval '1 hour' WHERE kind='generation.generate' AND payload->>'generationId' IN (SELECT id::text FROM generations WHERE owner_id=$1)`, owner); err != nil {
		t.Fatal(err)
	}
	claimed, err := jobs.NewRepository(pool).Claim(ctx, "stale-worker", time.Minute)
	if err != nil || claimed.Kind != creation.JobKind {
		t.Fatalf("claim real generation: %+v %v", claimed, err)
	}
	var failed uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM generations WHERE owner_id=$1 AND status='failed'`, owner).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if err := deletion.HandleDeletionJob(ctx, deleteJob); err != nil {
		t.Fatal(err)
	}
	if err := jobs.NewRepository(pool).Complete(ctx, claimed, "stale-worker"); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("stale worker retained lease: %v", err)
	}
	svc := creation.NewService(pool, root, "", false)
	job := jobs.Job{Kind: creation.FailureEvidenceJobKind, Payload: []byte(fmt.Sprintf(`{"generationId":%q}`, failed))}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- svc.HandleJob(ctx, job) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	assertDeletedGeneration(t, ctx, pool, owner, failed, "failed")
	var cancelled, leases, private, failures int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM generations WHERE owner_id=$1 AND status='cancelled'),
 (SELECT count(*) FROM jobs WHERE payload->>'generationId' IN (SELECT id::text FROM generations WHERE owner_id=$1) AND (lease_owner IS NOT NULL OR lease_expires_at IS NOT NULL OR status IN ('queued','running'))),
 (SELECT count(*) FROM generations WHERE owner_id=$1 AND (output_text IS NOT NULL OR prompt<>'[Deleted by account owner]' OR parameters<>'{}'::jsonb)),
 (SELECT count(*) FROM audit_events WHERE resource_id=$2 AND action='generation.failed')`, owner, failed).Scan(&cancelled, &leases, &private, &failures); err != nil {
		t.Fatal(err)
	}
	if cancelled != 204 || leases != 0 || private != 0 || failures != 1 {
		t.Fatalf("batch/evidence mismatch: %d %d %d %d", cancelled, leases, private, failures)
	}
}

func TestGenerationCancellationDuringReferenceReadStopsDispatch(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := lifecycleOwner(t, pool)
	asset := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','Private reference',$3,'text/plain','clean','upload','creator-owned','local_file',$1::uuid::text||'.txt')`, asset, owner, "/api/v1/assets/"+asset.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	base := media.NewLocalStore(t.TempDir())
	if err := base.Put(ctx, asset.String()+".txt", []byte("Private reference"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	store := &ownerChangingStore{Store: base}
	runtime := newLifecycleRuntime(t)
	svc := creation.NewServiceWithMedia(pool, media.NewCatalog(store), creation.NewRuntimeCatalog(runtime))
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "chat", Prompt: "Use private reference", SourceAssetIDs: []uuid.UUID{asset}}, "cancel-during-reference", "test")
	if err != nil {
		t.Fatal(err)
	}
	store.change = func() error {
		_, err := svc.Cancel(ctx, owner, g.ID, "cancel-in-reference-read", "test", "Stop private generation")
		return err
	}
	if err = svc.HandleJob(ctx, lifecycleJob(t, pool, g.ID)); err != nil {
		t.Fatal(err)
	}
	after, err := svc.Get(ctx, owner, g.ID)
	if err != nil || after.Status != "cancelled" || after.OutputText != nil || after.ChargedPoints != 0 || runtime.calls.Load() != 0 {
		t.Fatalf("cancelled read dispatched: %+v calls=%d %v", after, runtime.calls.Load(), err)
	}
	var reserved int
	if err = pool.QueryRow(ctx, `SELECT reserved_points FROM point_accounts WHERE user_id=$1`, owner).Scan(&reserved); err != nil || reserved != 0 {
		t.Fatalf("cancelled points %d %v", reserved, err)
	}
}

func TestAccountDeletionGenerationCancellationIsAtomicAndHonorsHold(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, scenario := range []string{"rollback", "legal_hold"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			deletion := datarights.NewService(pool, root)
			owner, deleteJob := lifecycleDeletion(t, ctx, pool, deletion)
			svc := creation.NewServiceWithRuntimes(pool, root, creation.NewRuntimeCatalog(newLifecycleRuntime(t)))
			g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "chat", Prompt: "Preserve pending work until deletion commits"}, "atomic-deletion-test", "test")
			if err != nil {
				t.Fatal(err)
			}
			// Also retain a legacy currency reservation on separate historical work.
			legacy := uuid.New()
			if _, err = pool.Exec(ctx, `INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status) VALUES($1,$2,'image','local','historical','Private legacy prompt','running')`, legacy, owner); err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `INSERT INTO billing_reservations(user_id,operation_type,operation_id,amount_cents,currency) VALUES($1,'generation',$2,25,'USD')`, owner, legacy); err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `UPDATE billing_accounts SET reserved_cents=reserved_cents+25 WHERE user_id=$1 AND currency='USD'`, owner); err != nil {
				t.Fatal(err)
			}
			var held int
			if err = pool.QueryRow(ctx, `SELECT reserved_points FROM point_accounts WHERE user_id=$1`, owner).Scan(&held); err != nil || held <= 0 {
				t.Fatalf("no reservation %d %v", held, err)
			}
			if scenario == "legal_hold" {
				admin := lifecycleOwner(t, pool)
				if _, err = pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, admin); err != nil {
					t.Fatal(err)
				}
				if _, err = deletion.CreateHold(ctx, admin, datarights.HoldInput{UserID: owner, AuthorityReference: "LIFECYCLE-HOLD"}, "hold"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_generation_deletion_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected deletion failure'; END $$;
    CREATE TRIGGER reject_generation_deletion_test BEFORE UPDATE OF status ON users FOR EACH ROW WHEN(NEW.status='deleted') EXECUTE FUNCTION reject_generation_deletion_test()`); err != nil {
					t.Fatal(err)
				}
			}
			err = deletion.HandleDeletionJob(ctx, deleteJob)
			if scenario == "rollback" && err == nil {
				t.Fatal("deletion fault did not fail")
			}
			if scenario == "legal_hold" && err != nil {
				t.Fatal(err)
			}
			var state, account, reservation string
			var points, cents int
			if err = pool.QueryRow(ctx, `SELECT g.status,u.status,r.status,p.reserved_points,b.reserved_cents FROM generations g JOIN users u ON u.id=g.owner_id JOIN point_reservations r ON r.generation_id=g.id JOIN point_accounts p ON p.user_id=u.id JOIN billing_accounts b ON b.user_id=u.id AND b.currency='USD' WHERE g.id=$1`, g.ID).Scan(&state, &account, &reservation, &points, &cents); err != nil {
				t.Fatal(err)
			}
			if state != "queued" || account != "active" || reservation != "held" || points != held || cents != 25 {
				t.Fatalf("failed/held deletion altered work: %s %s %s %d %d", state, account, reservation, points, cents)
			}
			if scenario == "rollback" {
				if _, err = pool.Exec(ctx, `DROP TRIGGER reject_generation_deletion_test ON users`); err != nil {
					t.Fatal(err)
				}
				if err = deletion.HandleDeletionJob(ctx, deleteJob); err != nil {
					t.Fatal(err)
				}
				if err = deletion.HandleDeletionJob(ctx, deleteJob); err != nil {
					t.Fatal(err)
				}
				assertDeletedGeneration(t, ctx, pool, owner, g.ID, "cancelled")
				if err = pool.QueryRow(ctx, `SELECT status FROM billing_reservations WHERE operation_id=$1`, legacy).Scan(&reservation); err != nil || reservation != "released" {
					t.Fatalf("legacy release: %s %v", reservation, err)
				}
				if err = pool.QueryRow(ctx, `SELECT reserved_cents FROM billing_accounts WHERE user_id=$1 AND currency='USD'`, owner).Scan(&cents); err != nil || cents != 0 {
					t.Fatalf("legacy reserved: %d %v", cents, err)
				}
			} else {
				// A hold blocks deletion, not the active owner's normal generation.
				if err = svc.HandleJob(ctx, lifecycleJob(t, pool, g.ID)); err != nil {
					t.Fatal(err)
				}
				after, err := svc.Get(ctx, owner, g.ID)
				if err != nil || after.Status != "succeeded" || after.OutputText == nil {
					t.Fatalf("held active account could not generate: %+v %v", after, err)
				}
			}
		})
	}
}
