package assets_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/uploadwrite"
	"github.com/jackc/pgx/v5/pgxpool"
)

func commandInput(key string) assets.UploadInput {
	return assets.UploadInput{Title: "Idempotent original", Filename: "original.txt", Reader: strings.NewReader(journalBytes), IdempotencyKey: key, RequestID: "command-test"}
}
func commandService(pool *pgxpool.Pool, store media.Store) *assets.Service {
	return assets.NewServiceWithMedia(pool, media.NewCatalog(store), &executionScanner{})
}
func assertSingleUploadCommand(t *testing.T, pool *pgxpool.Pool, owner, asset uuid.UUID, key string) {
	t.Helper()
	var commands, scans, audits, resultCount int
	err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM asset_upload_commands WHERE owner_id=$1 AND idempotency_key=$3),
 (SELECT count(*) FROM asset_scan_executions WHERE asset_id=$2),(SELECT count(*) FROM audit_events WHERE resource_id=$2 AND action IN ('asset.uploaded','asset.version_uploaded')),
 (SELECT count(*) FROM asset_upload_commands WHERE owner_id=$1 AND idempotency_key=$3 AND result_asset_id=$2)`, owner, asset, key).Scan(&commands, &scans, &audits, &resultCount)
	if err != nil || commands != 1 || scans != 1 || audits != 1 || resultCount != 1 {
		t.Fatal("duplicate or unbound result", commands, scans, audits, resultCount, err)
	}
}
func TestUploadCommandConcurrentReplaysAndConflicts(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := journalOwner(t, pool)
	store := &uploadTrackingStore{Store: media.NewLocalStore(t.TempDir())}
	svc := commandService(pool, store)
	key := uuid.NewString()
	type outcome struct {
		a assets.Asset
		e error
	}
	results := make(chan outcome, 8)
	for range 8 {
		go func() { a, e := svc.Upload(ctx, owner, commandInput(key)); results <- outcome{a, e} }()
	}
	var id uuid.UUID
	created := 0
	for range 8 {
		r := <-results
		if r.e != nil {
			t.Fatal(r.e)
		}
		if id == uuid.Nil {
			id = r.a.ID
		}
		if r.a.ID != id {
			t.Fatal("duplicate concurrent asset")
		}
		if !r.a.UploadReplayed {
			created++
		}
	}
	if created != 1 || store.puts.Load() != 1 {
		t.Fatal("duplicate storage or successful result", created, store.puts.Load())
	}
	assertSingleUploadCommand(t, pool, owner, id, key)
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	replay, err := svc.Upload(ctx, owner, commandInput(key))
	if err != nil || replay.ID != id || replay.ScanStatus != "rejected" || !replay.UploadReplayed {
		t.Fatal("stale replay projection", replay, err)
	}
	for _, field := range []string{"title", "filename", "bytes", "target"} {
		t.Run(field, func(t *testing.T) {
			input := commandInput(key)
			var err error
			switch field {
			case "title":
				input.Title = "Different title"
			case "filename":
				input.Filename = "other.txt"
			case "bytes":
				input.Reader = strings.NewReader(strings.Repeat("x", len(journalBytes)))
			}
			if field == "target" {
				_, err = svc.UploadVersion(ctx, owner, id, assets.VersionInput{UploadInput: input, Note: "Version note"})
			} else {
				_, err = svc.Upload(ctx, owner, input)
			}
			if !errors.Is(err, assets.ErrUploadConflict) {
				t.Fatal("changed input reused key", err)
			}
		})
	}
	if store.puts.Load() != 1 {
		t.Fatal("conflict performed storage write")
	}
	other := journalOwner(t, pool)
	second, err := svc.Upload(ctx, other, commandInput(key))
	if err != nil || second.ID == id {
		t.Fatal("cross-owner key collision", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Upload(ctx, owner, commandInput(key)); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("inactive owner replay exposed asset", err)
	}
}
func TestUploadCommandVersionAndFailedAttemptRecovery(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := journalOwner(t, pool)
	store := &journalFaultStore{Store: media.NewLocalStore(t.TempDir())}
	svc := commandService(pool, store)
	base, err := svc.Upload(ctx, owner, commandInput(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	version := func(note string) (assets.Asset, error) {
		return svc.UploadVersion(ctx, owner, base.ID, assets.VersionInput{UploadInput: commandInput(key), Note: note})
	}
	store.afterPut = func() error { return errors.New("stored but response lost") }
	if _, err = version("Updated contents"); err == nil {
		t.Fatal("lost response hidden")
	}
	var oldID uuid.UUID
	var state string
	if err = pool.QueryRow(ctx, `SELECT w.id,w.status FROM upload_writes w JOIN asset_upload_attempts a ON a.write_id=w.id JOIN asset_upload_commands c ON c.id=a.command_id WHERE c.owner_id=$1 AND c.idempotency_key=$2`, owner, key).Scan(&oldID, &state); err != nil || state != "cleaned" {
		t.Fatal(state, err)
	}
	store.afterPut = nil
	item, err := version("Updated contents")
	if err != nil || item.VersionNumber != 2 || item.FamilyID != base.FamilyID {
		t.Fatal(item, err)
	}
	replay, err := version(" Updated contents ")
	if err != nil || replay.ID != item.ID || !replay.UploadReplayed {
		t.Fatal(replay, err)
	}
	if _, err = version("A different note"); !errors.Is(err, assets.ErrUploadConflict) {
		t.Fatal("note not bound", err)
	}
	if _, err = svc.UploadVersion(ctx, owner, item.ID, assets.VersionInput{UploadInput: commandInput(key), Note: "Updated contents"}); !errors.Is(err, assets.ErrUploadConflict) {
		t.Fatal("base not bound", err)
	}
	assertSingleUploadCommand(t, pool, owner, item.ID, key)
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM asset_version_events WHERE asset_id=$1`, item.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("version audit duplicated", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM asset_upload_attempts a JOIN asset_upload_commands c ON c.id=a.command_id WHERE c.owner_id=$1 AND c.idempotency_key=$2`, owner, key).Scan(&count); err != nil || count != 2 {
		t.Fatal("failed attempt history lost", count, err)
	}
}
func TestUploadCommandLostCommitRepliesRecoverSameOperation(t *testing.T) {
	for _, stage := range []string{"registration", "result"} {
		t.Run(stage, func(t *testing.T) {
			pool, cleanup := assetTestPool(t)
			defer cleanup()
			ctx := t.Context()
			owner := journalOwner(t, pool)
			key := uuid.NewString()
			var armed, dropped atomic.Bool
			cfg := pool.Config()
			cfg.MinConns = 0
			cfg.MaxConns = 1
			cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
				if dropped.Load() {
					return nil, errors.New("database verification unavailable")
				}
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
			if stage == "registration" {
				armed.Store(true)
			} else {
				store.afterPut = func() { armed.Store(true) }
			}
			if _, err = commandService(faulty, store).Upload(ctx, owner, commandInput(key)); err == nil || !dropped.Load() {
				t.Fatal("commit reply not lost", err)
			}
			before := store.puts.Load()
			store.afterPut = nil
			recovered, err := commandService(pool, store).Upload(ctx, owner, commandInput(key))
			if err != nil {
				t.Fatal(err)
			}
			if stage == "registration" && (before != 0 || store.puts.Load() != 1) {
				t.Fatal("registration ambiguity wrote bytes")
			}
			if stage == "result" && (before != 1 || store.puts.Load() != 1 || !recovered.UploadReplayed) {
				t.Fatal("result ambiguity wrote twice")
			}
			replay, err := commandService(pool, store).Upload(ctx, owner, commandInput(key))
			if err != nil || replay.ID != recovered.ID {
				t.Fatal(replay, err)
			}
			assertSingleUploadCommand(t, pool, owner, recovered.ID, key)
		})
	}
}
func TestUploadCommandCrashChild(t *testing.T) {
	if os.Getenv("HCAI_UPLOAD_COMMAND_CHILD") != "1" {
		return
	}
	pool, err := database.Open(t.Context(), os.Getenv("HCAI_UPLOAD_COMMAND_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := uuid.Parse(os.Getenv("HCAI_UPLOAD_COMMAND_OWNER"))
	if err != nil {
		t.Fatal(err)
	}
	store := &journalFaultStore{Store: media.NewLocalStore(os.Getenv("HCAI_UPLOAD_COMMAND_ROOT")), afterPut: func() error { os.Exit(88); return nil }}
	_, err = commandService(pool, store).Upload(t.Context(), owner, commandInput(os.Getenv("HCAI_UPLOAD_COMMAND_KEY")))
	t.Fatal("crash not reached", err)
}
func TestUploadCommandCrashCleanupThenSameKeyRetry(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := journalOwner(t, pool)
	root := t.TempDir()
	key := uuid.NewString()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	child := exec.CommandContext(deadline, executable, "-test.run=^TestUploadCommandCrashChild$")
	child.Env = append(os.Environ(), "HCAI_UPLOAD_COMMAND_CHILD=1", "HCAI_UPLOAD_COMMAND_DSN="+pool.Config().ConnString(), "HCAI_UPLOAD_COMMAND_OWNER="+owner.String(), "HCAI_UPLOAD_COMMAND_ROOT="+root, "HCAI_UPLOAD_COMMAND_KEY="+key)
	output, err := child.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 88 {
		t.Fatalf("child %v %s", err, output)
	}
	id, location, state := journalForOwner(t, pool, owner)
	if state != "pending" {
		t.Fatal(state)
	}
	store := media.NewLocalStore(root)
	journalDue(t, pool, id)
	if _, err = uploadwrite.NewService(pool, media.NewCatalog(store)).Reconcile(ctx, 100); err != nil {
		t.Fatal(err)
	}
	journalAbsent(t, store, location)
	result, err := commandService(pool, store).Upload(ctx, owner, commandInput(key))
	if err != nil {
		t.Fatal(err)
	}
	assertSingleUploadCommand(t, pool, owner, result.ID, key)
	var attemptCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM asset_upload_attempts a JOIN asset_upload_commands c ON c.id=a.command_id WHERE c.owner_id=$1`, owner).Scan(&attemptCount); err != nil || attemptCount != 2 {
		t.Fatal(attemptCount, err)
	}
	if err = store.Put(ctx, location, []byte(journalBytes), "text/plain"); err != nil {
		t.Fatal(err)
	}
	journalDue(t, pool, id)
	if _, err = uploadwrite.NewService(pool, media.NewCatalog(store)).Reconcile(ctx, 100); err != nil {
		t.Fatal(err)
	}
	journalAbsent(t, store, location)
	replay, err := commandService(pool, store).Upload(ctx, owner, commandInput(key))
	if err != nil || replay.ID != result.ID {
		t.Fatal("late cleanup lost result", err)
	}
}
func TestUploadCommandExportOmitsKeysAndOtherOwners(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	root := t.TempDir()
	store := media.NewLocalStore(root)
	handle := "command_" + uuid.NewString()[:8]
	owner, token, err := identity.NewRepository(pool).Register(ctx, identity.RegisterInput{Email: handle + "@test.local", Password: "local-test-password", Handle: handle, DisplayName: "Command owner", Locale: "en-US", Timezone: "UTC"}, identity.ClientInfo{Label: "Command export", RequestID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	asset, err := commandService(pool, store).Upload(ctx, owner.ID, commandInput(key))
	if err != nil {
		t.Fatal(err)
	}
	other := journalOwner(t, pool)
	if _, err = commandService(pool, store).Upload(ctx, other, commandInput(key)); err != nil {
		t.Fatal(err)
	}
	rights := datarights.NewServiceWithMedia(pool, root, media.NewCatalog(store))
	req, err := rights.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "data_export", IdentityConfirmation: handle}, "test")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]uuid.UUID{"requestId": req.ID})
	if err = rights.HandleExportJob(ctx, jobs.Job{Kind: datarights.ExportJobKind, Payload: raw}); err != nil {
		t.Fatal(err)
	}
	body, _, err := rights.Download(ctx, owner.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(body, &exported); err != nil {
		t.Fatal(err)
	}
	var commands, attempts []map[string]any
	if err = json.Unmarshal(exported.Data["uploadCommands"], &commands); err != nil || len(commands) != 1 || commands[0]["resultAssetId"] != asset.ID.String() {
		t.Fatal(commands, err)
	}
	if err = json.Unmarshal(exported.Data["uploadAttempts"], &attempts); err != nil || len(attempts) != 1 || attempts[0]["commandId"] != commands[0]["id"] {
		t.Fatal(attempts, err)
	}
	var hash, otherCommand, location string
	if err = pool.QueryRow(ctx, `SELECT c.request_hash,(SELECT id::text FROM asset_upload_commands WHERE owner_id=$2),w.storage_key FROM asset_upload_commands c JOIN asset_upload_attempts a ON a.command_id=c.id JOIN upload_writes w ON w.id=a.write_id WHERE c.owner_id=$1`, owner.ID, other).Scan(&hash, &otherCommand, &location); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{key, hash, otherCommand, location} {
		if strings.Contains(string(body), secret) {
			t.Fatal("private command evidence exposed")
		}
	}
}

func TestUploadCommandMigrationAndResultGuards(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	down, err := os.ReadFile("../platform/database/migrations/0123_asset_upload_commands.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0123_asset_upload_commands.up.sql")
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
	repo := jobs.NewRepository(pool)
	if _, err = repo.Enqueue(ctx, "upload.command-migration", nil); err != nil {
		t.Fatal(err)
	}
	job, err := repo.Claim(ctx, "upload-command-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(string(down)); err == nil || !strings.Contains(err.Error(), "drain running jobs") {
		t.Fatal("rollback ignored running jobs", err)
	}
	if err = repo.Complete(ctx, job, "upload-command-worker"); err != nil {
		t.Fatal(err)
	}
	if err = run(string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(ctx, "upload.command-migration", nil); err != nil {
		t.Fatal(err)
	}
	job, err = repo.Claim(ctx, "upload-command-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(string(up)); err == nil || !strings.Contains(err.Error(), "drain running jobs") {
		t.Fatal("upgrade ignored running jobs", err)
	}
	if err = repo.Complete(ctx, job, "upload-command-worker"); err != nil {
		t.Fatal(err)
	}
	if err = run(string(up)); err != nil {
		t.Fatal(err)
	}
	owner := journalOwner(t, pool)
	store := media.NewLocalStore(t.TempDir())
	key := uuid.NewString()
	a, err := commandService(pool, store).Upload(ctx, owner, commandInput(key))
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE asset_upload_commands SET request_hash=repeat('0',64)`,
		`UPDATE asset_upload_commands SET owner_id=gen_random_uuid()`,
		`UPDATE asset_upload_commands SET idempotency_key='new-command-key'`,
		`UPDATE asset_upload_commands SET result_asset_id=NULL,completed_at=NULL`,
		`DELETE FROM asset_upload_commands`,
		`UPDATE asset_upload_attempts SET created_at=now()`,
		`DELETE FROM asset_upload_attempts`,
	} {
		if err = run(sql); err == nil {
			t.Fatal("command evidence mutated", sql)
		}
	}
	if err = run(string(down)); err == nil || !strings.Contains(err.Error(), "evidence exists") {
		t.Fatal("rollback erased commands", err)
	}
	for _, sql := range []string{`UPDATE assets SET title='Old upload writer' WHERE source_type='upload'`, `UPDATE jobs SET status='running' WHERE kind='asset.scan'`, `UPDATE asset_upload_commands SET created_at=now()`} {
		if err = run(`SELECT set_config('app.upload_command_protocol','',true);` + sql); err == nil || !strings.Contains(err.Error(), "upload-command-aware") {
			t.Fatal("old protocol accepted", sql, err)
		}
	}
	// The upload journal alone no longer suffices to commit a new upload result.
	intent := journalIntent(t, pool, owner, store)
	var assetID uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT asset_id FROM upload_writes WHERE id=$1`, intent.ID).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,size_bytes,storage_backend,storage_key)
 VALUES($1,$2,'document','Journal without command','/private','text/plain','clean','upload','personal',$3,'local_file',$4)`, assetID, owner, intent.Size, intent.Key); err != nil {
		t.Fatal(err)
	}
	if err = uploadwrite.AttachTx(ctx, tx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "completed command") {
		t.Fatal("command result guard bypassed", err)
	}
	commandID := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO asset_upload_commands(id,owner_id,idempotency_key,request_hash) VALUES($1,$2,$3,repeat('a',64))`, commandID, owner, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE asset_upload_commands SET result_asset_id=$2,completed_at=now() WHERE id=$1`, commandID, a.ID); err == nil {
		t.Fatal("unrelated result adopted")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO asset_upload_attempts(command_id,write_id) VALUES($1,$2)`, commandID, intent.ID); err != nil {
		t.Fatal(err)
	}
	other := journalOwner(t, pool)
	foreign := journalIntent(t, pool, other, store)
	if _, err = pool.Exec(ctx, `INSERT INTO asset_upload_attempts(command_id,write_id) VALUES($1,$2)`, commandID, foreign.ID); err == nil {
		t.Fatal("foreign write adopted")
	}
	assertSingleUploadCommand(t, pool, owner, a.ID, key)
}
