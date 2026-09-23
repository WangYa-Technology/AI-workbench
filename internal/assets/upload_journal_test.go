package assets_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/observability"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/uploadwrite"
	"github.com/jackc/pgx/v5/pgxpool"
)

const journalBytes = "Private original upload with durable ownership"

type journalFaultStore struct {
	media.Store
	afterPut                     func() error
	deleteHook                   func(context.Context) error
	deleteFails, acknowledgeOnly bool
	deletes                      atomic.Int64
}

func (s *journalFaultStore) Put(ctx context.Context, key string, body []byte, mime string) error {
	if err := s.Store.Put(ctx, key, body, mime); err != nil {
		return err
	}
	if s.afterPut != nil {
		return s.afterPut()
	}
	return nil
}
func (s *journalFaultStore) Delete(ctx context.Context, key string) error {
	s.deletes.Add(1)
	if s.deleteHook != nil {
		return s.deleteHook(ctx)
	}
	if s.deleteFails {
		return errors.New("isolated private storage failure")
	}
	if s.acknowledgeOnly {
		return nil
	}
	return s.Store.Delete(ctx, key)
}
func journalOwner(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Upload journal owner','creator')`, id, id.String()+"@test.local", "upload_"+id.String()[:8]); err != nil {
		t.Fatal(err)
	}
	return id
}
func journalUpload(t *testing.T, pool *pgxpool.Pool, store media.Store, owner uuid.UUID) (assets.Asset, error) {
	t.Helper()
	return assets.NewServiceWithMedia(pool, media.NewCatalog(store), &executionScanner{}).Upload(t.Context(), owner, assets.UploadInput{Title: "Journal original", Filename: "original.txt", Reader: strings.NewReader(journalBytes), RequestID: "journal-test"})
}
func journalIntent(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID, store media.Store) uploadwrite.Intent {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = accountlifecycle.Lock(ctx, tx, owner); err != nil {
		t.Fatal(err)
	}
	intent, err := uploadwrite.RegisterTx(ctx, tx, owner, uuid.New(), store, []byte(journalBytes), ".txt")
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return intent
}
func journalDue(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE upload_writes SET next_check_at=now()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}
func journalForOwner(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID) (uuid.UUID, string, string) {
	t.Helper()
	var id uuid.UUID
	var key, state string
	if err := pool.QueryRow(t.Context(), `SELECT id,storage_key,status FROM upload_writes WHERE owner_id=$1 ORDER BY created_at LIMIT 1`, owner).Scan(&id, &key, &state); err != nil {
		t.Fatal(err)
	}
	return id, key, state
}
func journalAbsent(t *testing.T, store media.Store, key string) {
	t.Helper()
	if _, err := store.Stat(t.Context(), key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("file remains: %v", err)
	}
}

// The same test executable exits after the actual Put, bypassing all defers.
func TestUploadJournalCrashChild(t *testing.T) {
	if os.Getenv("HCAI_UPLOAD_CRASH_CHILD") != "1" {
		return
	}
	pool, err := database.Open(t.Context(), os.Getenv("HCAI_UPLOAD_CRASH_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := uuid.Parse(os.Getenv("HCAI_UPLOAD_CRASH_OWNER"))
	if err != nil {
		t.Fatal(err)
	}
	store := &journalFaultStore{Store: media.NewLocalStore(os.Getenv("HCAI_UPLOAD_CRASH_ROOT")), afterPut: func() error { os.Exit(88); return nil }}
	_, err = journalUpload(t, pool, store, owner)
	t.Fatalf("child did not exit at write: %v", err)
}
func TestUploadJournalProcessCrashAndLateWrite(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := journalOwner(t, pool)
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	childCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	child := exec.CommandContext(childCtx, executable, "-test.run=^TestUploadJournalCrashChild$")
	child.Env = append(os.Environ(), "HCAI_UPLOAD_CRASH_CHILD=1", "HCAI_UPLOAD_CRASH_DSN="+pool.Config().ConnString(), "HCAI_UPLOAD_CRASH_OWNER="+owner.String(), "HCAI_UPLOAD_CRASH_ROOT="+root)
	output, err := child.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 88 {
		t.Fatalf("crash failed: %v %s", err, output)
	}
	id, key, state := journalForOwner(t, pool, owner)
	if state != "pending" {
		t.Fatal(state)
	}
	store := media.NewLocalStore(root)
	if _, err = store.Stat(ctx, key); err != nil {
		t.Fatal("crash lost file evidence", err)
	}
	var records, scans int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM assets WHERE owner_id=$1),(SELECT count(*) FROM asset_scan_executions)`, owner).Scan(&records, &scans); err != nil || records != 0 || scans != 0 {
		t.Fatal(records, scans, err)
	}
	// Retrying the request gets an independent location; the janitor must preserve it.
	a, err := journalUpload(t, pool, store, owner)
	if err != nil {
		t.Fatal(err)
	}
	journalDue(t, pool, id)
	sweeper := uploadwrite.NewService(pool, media.NewCatalog(store))
	if n, err := sweeper.Reconcile(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	journalAbsent(t, store, key)
	var attachedKey string
	if err = pool.QueryRow(ctx, `SELECT storage_key FROM upload_writes WHERE asset_id=$1 AND status='attached'`, a.ID).Scan(&attachedKey); err != nil || attachedKey == key {
		t.Fatal(attachedKey, err)
	}
	if _, err = store.Stat(ctx, attachedKey); err != nil {
		t.Fatal("attached original deleted", err)
	}
	// A delayed remote write is tracked even after an earlier absent result.
	if err = store.Put(ctx, key, []byte(journalBytes), "text/plain"); err != nil {
		t.Fatal(err)
	}
	journalDue(t, pool, id)
	if _, err = sweeper.Reconcile(ctx, 100); err != nil {
		t.Fatal(err)
	}
	journalAbsent(t, store, key)
	var checks int
	var verified bool
	if err = pool.QueryRow(ctx, `SELECT status,cleanup_checks,verified_absent_at IS NOT NULL FROM upload_writes WHERE id=$1`, id).Scan(&state, &checks, &verified); err != nil || state != "cleaned" || checks != 2 || !verified {
		t.Fatal(state, checks, verified, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE upload_writes SET status='pending',verified_absent_at=NULL WHERE id=$1`, id); err == nil {
		t.Fatal("cleaned intent revived")
	}
}
func TestUploadJournalUncertainPutAndVerifiedCleanup(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := journalOwner(t, pool)
	root := t.TempDir()
	store := &journalFaultStore{Store: media.NewLocalStore(root), afterPut: func() error { return errors.New("lost Put response") }, deleteFails: true}
	if _, err := journalUpload(t, pool, store, owner); err == nil {
		t.Fatal("lost Put reply hidden")
	}
	id, key, state := journalForOwner(t, pool, owner)
	if state != "pending" {
		t.Fatal(state)
	}
	var code string
	var backedOff bool
	if err := pool.QueryRow(ctx, `SELECT last_error_code,next_check_at>now() FROM upload_writes WHERE id=$1`, id).Scan(&code, &backedOff); err != nil || code != "upload_write_cleanup_failed" || !backedOff {
		t.Fatal(code, backedOff, err)
	}
	sweeper := uploadwrite.NewService(pool, media.NewCatalog(store))
	store.deleteFails = false
	moved := filepath.Join(t.TempDir(), "offline-media")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	journalDue(t, pool, id)
	if _, err := sweeper.Reconcile(ctx, 100); !errors.Is(err, media.ErrStorageUnavailable) {
		t.Fatal("unavailable upload storage accepted as absence", err)
	}
	var verifiedAbsent bool
	if err := pool.QueryRow(ctx, `SELECT status,verified_absent_at IS NOT NULL FROM upload_writes WHERE id=$1`, id).Scan(&state, &verifiedAbsent); err != nil || state != "pending" || verifiedAbsent {
		t.Fatal("upload marked cleaned while bytes remain", state, verifiedAbsent, err)
	}
	if _, err := os.Stat(filepath.Join(moved, key)); err != nil {
		t.Fatal("pending upload disappeared during storage outage", err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	journalDue(t, pool, id)
	if _, err := sweeper.Reconcile(ctx, 100); !errors.Is(err, uploadwrite.ErrReferenced) {
		t.Fatal("replacement upload root accepted as deletion evidence", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status,verified_absent_at IS NOT NULL FROM upload_writes WHERE id=$1`, id).Scan(&state, &verifiedAbsent); err != nil || state != "pending" || verifiedAbsent {
		t.Fatal("replacement root completed upload cleanup", state, verifiedAbsent, err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal("unexpected data written in replacement root", err)
	}
	if err := os.Rename(moved, root); err != nil {
		t.Fatal(err)
	}
	store.acknowledgeOnly = true
	journalDue(t, pool, id)
	if _, err := sweeper.Reconcile(ctx, 100); !errors.Is(err, media.ErrDeletionUnverified) {
		t.Fatal("delete ack accepted as proof", err)
	}
	metrics, err := observability.NewMetrics(time.Now()).Render(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metrics, "hcai_upload_write_cleanup_failed 1\n") || strings.Contains(metrics, key) || strings.Contains(metrics, owner.String()) {
		t.Fatal("unsafe or missing metrics")
	}
	store.acknowledgeOnly = false
	journalDue(t, pool, id)
	if _, err = sweeper.Reconcile(ctx, 100); err != nil {
		t.Fatal(err)
	}
	journalAbsent(t, store, key)
	metrics, err = observability.NewMetrics(time.Now()).Render(ctx, pool)
	if err != nil || !strings.Contains(metrics, "hcai_upload_write_cleanup_failed 0\n") {
		t.Fatal("failure did not clear", err)
	}
}
func TestUploadJournalIntentCommitReplyLostStopsBeforePut(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := journalOwner(t, pool)
	var armed, dropped atomic.Bool
	cfg := pool.Config()
	cfg.MinConns = 0
	cfg.MaxConns = 1
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &uploadCommitDropConn{Conn: c, armed: &armed, dropped: &dropped}, nil
	}
	faulty, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer faulty.Close()
	store := &uploadTrackingStore{Store: media.NewLocalStore(t.TempDir())}
	armed.Store(true)
	if _, err = journalUpload(t, faulty, store, owner); err == nil || !dropped.Load() || store.puts.Load() != 0 {
		t.Fatal("uncertain registration performed Put", err, dropped.Load(), store.puts.Load())
	}
	id, key, state := journalForOwner(t, pool, owner)
	if state != "pending" {
		t.Fatal(state)
	}
	journalAbsent(t, store, key)
	journalDue(t, pool, id)
	if _, err = uploadwrite.NewService(pool, media.NewCatalog(store)).Reconcile(ctx, 100); err != nil {
		t.Fatal(err)
	}
}
func TestUploadJournalHoldAndUnknownBytes(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := journalOwner(t, pool)
	store := &journalFaultStore{Store: media.NewLocalStore(t.TempDir())}
	intent := journalIntent(t, pool, owner, store)
	if err := store.Put(ctx, intent.Key, []byte(journalBytes), "text/plain"); err != nil {
		t.Fatal(err)
	}
	actor := journalOwner(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	rights := datarights.NewServiceWithMedia(pool, t.TempDir(), media.NewCatalog(store))
	hold, err := rights.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner, AuthorityReference: "UPLOAD-LEGAL-HOLD"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	sweeper := uploadwrite.NewService(pool, media.NewCatalog(store))
	journalDue(t, pool, intent.ID)
	if n, err := sweeper.Reconcile(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if store.deletes.Load() != 0 {
		t.Fatal("held file deleted")
	}
	if _, err = rights.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.Store.Delete(ctx, intent.Key); err != nil {
		t.Fatal(err)
	}
	if err = store.Store.Put(ctx, intent.Key, []byte("unrecognized replacement"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	journalDue(t, pool, intent.ID)
	if _, err = sweeper.Reconcile(ctx, 100); !errors.Is(err, uploadwrite.ErrReferenced) {
		t.Fatal("unknown bytes deleted", err)
	}
	if store.deletes.Load() != 0 {
		t.Fatal("unrecognized file removed")
	}
	if err = store.Store.Delete(ctx, intent.Key); err != nil {
		t.Fatal(err)
	}
	if err = store.Store.Put(ctx, intent.Key, []byte(journalBytes), "text/plain"); err != nil {
		t.Fatal(err)
	}
	journalDue(t, pool, intent.ID)
	results := make(chan error, 2)
	for range 2 {
		go func() { _, e := sweeper.Reconcile(ctx, 100); results <- e }()
	}
	for range 2 {
		if err = <-results; err != nil {
			t.Fatal(err)
		}
	}
	journalAbsent(t, store, intent.Key)
	if store.deletes.Load() != 1 {
		t.Fatal("concurrent duplicate cleanup", store.deletes.Load())
	}
}
func TestUploadJournalBusyCandidatesAndInterruptedChecks(t *testing.T) {
	for _, lock := range []string{"owner", "intent", "interrupted"} {
		t.Run(lock, func(t *testing.T) {
			pool, cleanup := assetTestPool(t)
			defer cleanup()
			ctx := t.Context()
			store := &journalFaultStore{Store: media.NewLocalStore(t.TempDir())}
			owner := journalOwner(t, pool)
			first := journalIntent(t, pool, owner, store)
			second := journalIntent(t, pool, journalOwner(t, pool), store)
			for _, i := range []uploadwrite.Intent{first, second} {
				if err := store.Put(ctx, i.Key, []byte(journalBytes), "text/plain"); err != nil {
					t.Fatal(err)
				}
				journalDue(t, pool, i.ID)
			}
			if _, err := pool.Exec(ctx, `UPDATE upload_writes SET next_check_at=now()-interval '1 day' WHERE id=$1`, first.ID); err != nil {
				t.Fatal(err)
			}
			sweeper := uploadwrite.NewService(pool, media.NewCatalog(store))
			if lock == "interrupted" {
				pass, cancel := context.WithCancel(ctx)
				defer cancel()
				store.deleteHook = func(ctx context.Context) error { cancel(); return ctx.Err() }
				if _, err := sweeper.Reconcile(pass, 1); !errors.Is(err, context.Canceled) {
					t.Fatal("interruption ignored", err)
				}
				store.deleteHook = nil
				var code string
				var future bool
				if err := pool.QueryRow(ctx, `SELECT last_error_code,next_check_at>now() FROM upload_writes WHERE id=$1`, first.ID).Scan(&code, &future); err != nil || code != "upload_write_cleanup_failed" || !future {
					t.Fatal(code, future, err)
				}
			} else {
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if lock == "owner" {
					err = accountlifecycle.Lock(ctx, tx, owner)
				} else {
					_, err = tx.Exec(ctx, `SELECT id FROM upload_writes WHERE id=$1 FOR UPDATE`, first.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					pass, cancel := context.WithTimeout(ctx, time.Second)
					_, err = sweeper.Reconcile(pass, 1)
					cancel()
					if err != nil {
						t.Fatal("busy candidate blocked pass", err)
					}
				}
				if _, err = store.Stat(ctx, first.Key); err != nil {
					t.Fatal("busy owner file deleted", err)
				}
				journalAbsent(t, store, second.Key)
				if err = tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := sweeper.Reconcile(ctx, 1); err != nil {
				t.Fatal(err)
			}
			journalAbsent(t, store, second.Key)
			journalDue(t, pool, first.ID)
			if _, err := sweeper.Reconcile(ctx, 100); err != nil {
				t.Fatal(err)
			}
			journalAbsent(t, store, first.Key)
		})
	}
}
func TestUploadJournalDeletionAndOwnerExport(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	root := t.TempDir()
	store := &journalFaultStore{Store: media.NewLocalStore(root)}
	catalog := media.NewCatalog(store)
	handle := "upload_" + uuid.NewString()[:8]
	owner, token, err := identity.NewRepository(pool).Register(ctx, identity.RegisterInput{Email: handle + "@test.local", Password: "local-test-password", Handle: handle, DisplayName: "Journal export owner", Locale: "en-US", Timezone: "UTC"}, identity.ClientInfo{Label: "Journal test", RequestID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	own := journalIntent(t, pool, owner.ID, store)
	other := journalIntent(t, pool, journalOwner(t, pool), store)
	if err = store.Put(ctx, own.Key, []byte(journalBytes), "text/plain"); err != nil {
		t.Fatal(err)
	}
	rights := datarights.NewServiceWithMedia(pool, root, catalog)
	request, err := rights.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "data_export", IdentityConfirmation: handle}, "journal-export")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]uuid.UUID{"requestId": request.ID})
	if err = rights.HandleExportJob(ctx, jobs.Job{Kind: datarights.ExportJobKind, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	body, _, err := rights.Download(ctx, owner.ID, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(body, &exported); err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	if err = json.Unmarshal(exported.Data["uploadWrites"], &records); err != nil || len(records) != 1 || records[0]["id"] != own.ID.String() {
		t.Fatal(records, err)
	}
	for _, secret := range []string{other.ID.String(), own.Key, other.Key, own.Digest} {
		if strings.Contains(string(body), secret) {
			t.Fatal("private journal data leaked")
		}
	}
	for _, field := range []string{"storageKey", "storageBackend", "checksumSha256"} {
		if _, ok := records[0][field]; ok {
			t.Fatal("locator exposed", field)
		}
	}
	deletion, err := rights.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: handle}, "journal-delete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 second' WHERE id=$1`, deletion.ID); err != nil {
		t.Fatal(err)
	}
	payload, _ = json.Marshal(map[string]uuid.UUID{"requestId": deletion.ID})
	job := jobs.Job{Kind: datarights.DeletionJobKind, Payload: payload}
	store.deleteFails = true
	if err = rights.HandleDeletionJob(ctx, job); err == nil {
		t.Fatal("deletion ignored orphan cleanup failure")
	}
	var receipts int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=$1`, deletion.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal(receipts, err)
	}
	store.deleteFails = false
	if err = rights.HandleDeletionJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	journalAbsent(t, store, own.Key)
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=$1`, deletion.ID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal(receipts, err)
	}
}

func TestUploadJournalMigrationAndBindingGuards(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	up, err := os.ReadFile("../platform/database/migrations/0122_upload_write_journal.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0122_upload_write_journal.down.sql")
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
	commandDown, err := os.ReadFile("../platform/database/migrations/0123_asset_upload_commands.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	commandUp, err := os.ReadFile("../platform/database/migrations/0123_asset_upload_commands.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = run(string(commandDown)); err != nil {
		t.Fatal(err)
	}
	repo := jobs.NewRepository(pool)
	if _, err = repo.Enqueue(ctx, "test.upload-drain", nil); err != nil {
		t.Fatal(err)
	}
	job, err := repo.Claim(ctx, "upload-migration-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(string(down)); err == nil || !strings.Contains(err.Error(), "drain running jobs") {
		t.Fatal("downgrade ignored running worker", err)
	}
	if err = repo.Complete(ctx, job, "upload-migration-worker"); err != nil {
		t.Fatal(err)
	}
	if err = run(string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(ctx, "test.upload-drain", nil); err != nil {
		t.Fatal(err)
	}
	job, err = repo.Claim(ctx, "upload-migration-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(string(up)); err == nil || !strings.Contains(err.Error(), "drain running jobs") {
		t.Fatal("upgrade ignored running worker", err)
	}
	if err = repo.Complete(ctx, job, "upload-migration-worker"); err != nil {
		t.Fatal(err)
	}
	if err = run(string(up)); err != nil {
		t.Fatal(err)
	}
	owner := journalOwner(t, pool)
	store := media.NewLocalStore(t.TempDir())
	intent := journalIntent(t, pool, owner, store)
	var intendedAsset uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT asset_id FROM upload_writes WHERE id=$1`, intent.ID).Scan(&intendedAsset); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,size_bytes,storage_backend,storage_key)
 VALUES($1,$2,'document','Journal guarded original','/private','text/plain','clean','upload','personal',$3,'local_file',$4)`
	// An existing journal cannot be bypassed by adopting another asset ID.
	if _, err = pool.Exec(ctx, insert, uuid.New(), owner, intent.Size, intent.Key); err == nil {
		t.Fatal("journal location aliased")
	}
	// Reserved namespace is not usable without durable registration, including S3 prefixes.
	for _, key := range []string{"upload-" + uuid.NewString() + ".txt", "nested/upload-" + uuid.NewString() + ".txt"} {
		if _, err = pool.Exec(ctx, insert, uuid.New(), owner, intent.Size, key); err == nil {
			t.Fatal("journal-less reserved location adopted")
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, insert, intendedAsset, owner, intent.Size, intent.Key); err != nil {
		t.Fatal(err)
	}
	err = tx.Commit(ctx)
	if err == nil || !strings.Contains(err.Error(), "attached write evidence") {
		t.Fatal("asset committed without attachment", err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = uploadwrite.RegisterTx(ctx, tx, owner, intendedAsset, store, []byte(journalBytes), ".txt")
	_ = tx.Rollback(ctx)
	if err == nil {
		t.Fatal("second invocation shares asset identity")
	}
	for _, query := range []string{
		`UPDATE upload_writes SET checksum_sha256=repeat('0',64)`,
		`UPDATE upload_writes SET storage_key='different.txt'`,
		`UPDATE upload_writes SET asset_id=gen_random_uuid()`,
		`UPDATE upload_writes SET owner_id=gen_random_uuid()`,
		`UPDATE upload_writes SET status='attached',attached_at=now()`,
		`DELETE FROM upload_writes`,
	} {
		if err = run(query); err == nil {
			t.Fatal("unsafe evidence mutation accepted", query)
		}
	}
	if err = run(string(down)); err == nil || !strings.Contains(err.Error(), "durable evidence") {
		t.Fatal("rollback erased obligations", err)
	}
	if err = run(string(commandUp)); err != nil {
		t.Fatal(err)
	}
	a, err := journalUpload(t, pool, store, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE assets SET size_bytes=size_bytes+1 WHERE id=$1`,
		`UPDATE assets SET source_type='generation' WHERE id=$1`,
		`UPDATE upload_writes SET next_check_at=now() WHERE asset_id=$1`,
	} {
		if _, err = pool.Exec(ctx, query, a.ID); err == nil {
			t.Fatal("attached binding mutation accepted", query)
		}
	}
	for _, query := range []string{
		`UPDATE assets SET title='Old writer' WHERE source_type='upload'`,
		`UPDATE jobs SET status='running' WHERE kind='asset.scan'`,
		`UPDATE upload_writes SET next_check_at=now() WHERE status='pending'`,
	} {
		if err = run(`SELECT set_config('app.upload_write_protocol','',true);` + query); err == nil || !strings.Contains(err.Error(), "upload-write-aware") {
			t.Fatal("old writer permitted", query, err)
		}
	}
	// A retired location cannot later become an asset even with the original ID.
	journalDue(t, pool, intent.ID)
	if _, err = uploadwrite.NewService(pool, media.NewCatalog(store)).Reconcile(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, insert, intendedAsset, owner, intent.Size, intent.Key); err == nil {
		t.Fatal("cleaned intent adopted")
	}
}

func TestUploadJournalWriteValidatesConflictAndRetiredIntent(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := journalOwner(t, pool)
	for _, scenario := range []string{"exact_conflict", "changed_stored_bytes", "changed_request_bytes", "changed_evidence", "retired"} {
		t.Run(scenario, func(t *testing.T) {
			store := &uploadTrackingStore{Store: media.NewLocalStore(t.TempDir())}
			catalog := media.NewCatalog(store)
			intent := journalIntent(t, pool, owner, store)
			data := []byte(journalBytes)
			if scenario == "exact_conflict" || scenario == "changed_stored_bytes" {
				content := data
				if scenario == "changed_stored_bytes" {
					content = []byte("Another owner's unrelated object")
				}
				if err := store.Store.Put(ctx, intent.Key, content, "text/plain"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "changed_request_bytes" {
				data = []byte("a different request body")
			}
			if scenario == "retired" {
				journalDue(t, pool, intent.ID)
				if _, err := uploadwrite.NewService(pool, catalog).Reconcile(ctx, 1); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "changed_evidence" {
				intent.Digest = strings.Repeat("0", 64)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if err = accountlifecycle.Lock(ctx, tx, owner); err != nil {
				t.Fatal(err)
			}
			err = uploadwrite.WriteTx(ctx, tx, catalog, intent, data, "text/plain")
			switch scenario {
			case "exact_conflict":
				if err != nil {
					t.Fatal("exact existing write rejected", err)
				}
			case "changed_stored_bytes":
				if !errors.Is(err, media.ErrIntegrity) {
					t.Fatal("foreign bytes adopted", err)
				}
			default:
				if err == nil || store.puts.Load() != 0 {
					t.Fatal("invalid or retired write reached storage", err, store.puts.Load())
				}
			}
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if scenario == "changed_stored_bytes" {
				obj, err := store.Open(ctx, intent.Key, nil)
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(obj.Body)
				obj.Body.Close()
				if readErr != nil || string(body) != "Another owner's unrelated object" {
					t.Fatal("conflicting content replaced", readErr)
				}
				if store.deletes.Load() != 0 {
					t.Fatal("conflicting object deleted")
				}
			}
		})
	}
}
