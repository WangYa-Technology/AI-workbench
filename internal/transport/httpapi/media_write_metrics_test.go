package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/generationoutput"
	"github.com/hcai-chat/hcai-chat/internal/observability"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
	"github.com/hcai-chat/hcai-chat/internal/uploadwrite"
)

func TestMediaWriteAgeSurvivesBusyOwnerAndHonorsHolds(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := t.Context()
	root := t.TempDir()
	catalog := media.NewCatalog(media.NewLocalStore(root))
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	for _, kind := range []string{observability.LegalHoldExpiry, observability.LegalHoldCleanup, observability.ProductCleanupReconciliation, observability.AccountDeletionReconciliation, observability.OriginalMediaCleanupReconciliation, observability.ProductRefundReconciliation, observability.ProductCheckoutReconciliation, observability.GenerationOutputCleanup, observability.GenerationExecutionRecovery, observability.AssetScanExecutionRecovery, observability.UploadWriteCleanup} {
		if err := observability.NewRepository(pool).RecordMaintenance(ctx, kind, true); err != nil {
			t.Fatal(err)
		}
	}
	owner := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Private intent owner','admin')`, owner, owner.String()+"@test.local", "intent_"+owner.String()[:8]); err != nil {
		t.Fatal(err)
	}
	// Use the submission service so the queued generation has its real job,
	// execution binding and credit reservation. Missing execution evidence must
	// continue to alert independently of media cleanup.
	queued, err := creation.NewService(pool, root, "", true).SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "image", Prompt: "private content"}, "media-write-metrics", "test")
	if err != nil {
		t.Fatal(err)
	}
	generation := queued.ID
	insert := func(kind, stamp string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		var err error
		if kind == "upload" {
			_, err = pool.Exec(ctx, `INSERT INTO upload_writes(id,owner_id,asset_id,storage_backend,storage_key,checksum_sha256,size_bytes,created_at,next_check_at) VALUES($1,$2,$3,'local_file',$4,repeat('a',64),32,$5::timestamptz,now()-interval '1 minute')`, id, owner, uuid.New(), "upload-"+id.String()+".txt", stamp)
		} else {
			_, err = pool.Exec(ctx, `INSERT INTO generation_output_writes(id,generation_id,owner_id,asset_id,storage_backend,storage_key,checksum_sha256,size_bytes,created_at,next_check_at) VALUES($1,$2,$3,$4,'local_file',$5,repeat('a',64),32,$6::timestamptz,now()-interval '1 minute')`, id, generation, owner, uuid.New(), "generation-"+id.String()+".jpg", stamp)
		}
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	var old string
	if err := pool.QueryRow(ctx, `SELECT (now()-interval '2 hours')::text`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	upload := insert("upload", old)
	output := insert("generation", old)
	check := func(want string, success bool) {
		t.Helper()
		cmd := exec.Command("bash", "../../../scripts/metrics-alert-check.sh")
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "ALERT_") && !strings.HasPrefix(value, "METRICS_URL=") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "METRICS_URL="+server.URL+"/metrics")
		body, err := cmd.CombinedOutput()
		if (err == nil) != success || !strings.Contains(string(body), want) {
			t.Fatalf("err=%v output=%s want=%s", err, body, want)
		}
	}
	locked, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(ctx)
	if err = accountlifecycle.Lock(ctx, locked, owner); err != nil {
		t.Fatal(err)
	}
	if n, err := generationoutput.NewService(pool, catalog).Reconcile(ctx, 1); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if n, err := uploadwrite.NewService(pool, catalog).Reconcile(ctx, 1); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	// Both deadlines are now in the future and both scanners can look healthy,
	// but the original two-hour pending obligations still need attention.
	check("ALERT generation_output_pending_overdue upload_write_pending_overdue", false)
	response, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"hcai_generation_output_cleanup_due 0\n", "hcai_upload_write_cleanup_due 0\n", "hcai_generation_output_cleanup_failed 0\n", "hcai_upload_write_cleanup_failed 0\n"} {
		if !strings.Contains(string(body), s) {
			t.Fatal("deferral did not isolate business age", s)
		}
	}
	for _, s := range []string{owner.String(), generation.String(), upload.String(), output.String(), "private content", strings.Repeat("a", 64)} {
		if strings.Contains(string(body), s) {
			t.Fatal("private write evidence leaked")
		}
	}
	if err = locked.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	rights := datarights.NewServiceWithMedia(pool, root, catalog)
	hold, err := rights.CreateHold(ctx, owner, datarights.HoldInput{UserID: owner, AuthorityReference: "MEDIA-WRITE-RETENTION"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	// Let both cleaners persist a retained result. Releasing/expiring the hold
	// must restore the pending-age alarm even while that cached code remains.
	for _, table := range []string{"generation_output_writes", "upload_writes"} {
		if _, err = pool.Exec(ctx, "UPDATE "+table+" SET next_check_at=now()"); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := generationoutput.NewService(pool, catalog).Reconcile(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n, err := uploadwrite.NewService(pool, catalog).Reconcile(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	check("ok ", true)
	if _, err = rights.ReleaseHold(ctx, owner, hold.ID); err != nil {
		t.Fatal(err)
	}
	check("ALERT generation_output_pending_overdue upload_write_pending_overdue", false)
	hold, err = rights.CreateHold(ctx, owner, datarights.HoldInput{UserID: owner, AuthorityReference: "MEDIA-WRITE-EXPIRY"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	check("ok ", true)
	// A wall-clock expiry is effective before the periodic expiry task runs.
	if _, err = pool.Exec(ctx, `UPDATE data_rights_legal_holds SET review_at=now()-interval '2 days',expires_at=now()-interval '1 day' WHERE id=$1`, hold.ID); err != nil {
		t.Fatal(err)
	}
	check("ALERT generation_output_pending_overdue upload_write_pending_overdue", false)
	if _, err = pool.Exec(ctx, `UPDATE upload_writes SET next_check_at=now() WHERE id=$1`, upload); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE generation_output_writes SET next_check_at=now() WHERE id=$1`, output); err != nil {
		t.Fatal(err)
	}
	if _, err = generationoutput.NewService(pool, catalog).Reconcile(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err = uploadwrite.NewService(pool, catalog).Reconcile(ctx, 100); err != nil {
		t.Fatal(err)
	}
	check("ok ", true)
	// Future and infinite registration times cannot silently look like age zero.
	var future string
	if err = pool.QueryRow(ctx, `SELECT (now()+interval '1 hour')::text`).Scan(&future); err != nil {
		t.Fatal(err)
	}
	insert("upload", future)
	insert("generation", "infinity")
	check("ALERT generation_output_pending_clock_invalid upload_write_pending_clock_invalid", false)
}
