package datarights_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestExportPartsIntegrityExpiryAndLegacyCompatibility(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, direction := range []string{"down", "up"} {
		migration, err := os.ReadFile("../platform/database/migrations/0098_data_export_parts." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, string(migration)); err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	name := "parts_" + uuid.NewString()[:8]
	owner, token, err := identity.NewRepository(pool).Register(ctx, identity.RegisterInput{
		Email: name + "@example.test", Password: "Strong-parts-test-42", Handle: name, DisplayName: "Parts Owner", Locale: "en-US", Timezone: "UTC",
	}, identity.ClientInfo{Label: "Export parts test", RequestID: "export-parts"})
	if err != nil {
		t.Fatal(err)
	}
	service := datarights.NewService(pool, t.TempDir())
	request, err := service.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "data_export", IdentityConfirmation: name}, "parts-request")
	if err != nil {
		t.Fatal(err)
	}
	// Many separate records must all appear, and in stable order, across parts.
	if _, err = pool.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,created_at)
 SELECT $1,'export.parts_test','user',$1,repeat('完整记录',1000),'part-row-'||n,now()+make_interval(secs=>n) FROM generate_series(1,1100) n`, owner.ID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"requestId": request.ID})
	job := jobs.Job{Kind: datarights.ExportJobKind, Payload: payload}
	if err = service.HandleExportJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	file, checksum, err := service.OpenExport(ctx, owner.ID, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	file.Close()
	if err != nil || size <= 10<<20 || checksum != hex.EncodeToString(hash.Sum(nil)) {
		t.Fatalf("stream bytes/digest: %d %v", size, err)
	}
	body, _, err := service.Download(ctx, owner.ID, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Data struct {
			Audit []struct {
				Action    string
				RequestID string `json:"requestId"`
			}
		}
	}
	if err = json.Unmarshal(body, &pkg); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, entry := range pkg.Data.Audit {
		if entry.Action == "export.parts_test" {
			total++
		}
	}
	if total != 1100 {
		t.Fatalf("lost records: %d", total)
	}
	if _, _, err = service.OpenExport(ctx, uuid.New(), request.ID); !errors.Is(err, datarights.ErrNotReady) {
		t.Fatal("foreign export accessible", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_rights_export_parts SET body=NULL,purged_at=now() WHERE request_id=$1`, request.ID); err == nil {
		t.Fatal("parts mutable without maintenance")
	}
	down, err := os.ReadFile("../platform/database/migrations/0098_data_export_parts.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(down)); err == nil {
		t.Fatal("migration discarded existing export evidence")
	}
	tx.Rollback(ctx)
	// Even a maintenance write that replaces a part and its matching part digest
	// must not pass the immutable complete-package checksum.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.data_rights_maintenance','on',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE data_rights_export_parts SET body=set_byte(body,0,32),checksum_sha256=encode(public.digest(set_byte(body,0,32),'sha256'),'hex') WHERE request_id=$1 AND part_number=2`, request.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.OpenExport(ctx, owner.ID, request.ID); err == nil || !strings.Contains(err.Error(), "integrity mismatch") {
		t.Fatal("corrupt export downloadable", err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.data_rights_maintenance','on',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE data_rights_export_artifacts SET expires_at=now()-interval '1 second' WHERE request_id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.OpenExport(ctx, owner.ID, request.ID); !errors.Is(err, datarights.ErrExpired) {
		t.Fatal("expiry not enforced before purge", err)
	}
	if err = service.HandleExportExpiryJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err = service.HandleExportExpiryJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var count, unpurged int
	if err = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE body IS NOT NULL OR purged_at IS NULL) FROM data_rights_export_parts WHERE request_id=$1`, request.ID).Scan(&count, &unpurged); err != nil || count < 3 || unpurged != 0 {
		t.Fatalf("retention: %d %d %v", count, unpurged, err)
	}
	// An old single-body artifact is still downloadable and verified.
	legacy, err := service.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "data_export", IdentityConfirmation: name}, "legacy-request")
	if err != nil {
		t.Fatal(err)
	}
	oldBody := []byte(`{"schemaVersion":1,"data":{"legacy":true}}`)
	sum := sha256.Sum256(oldBody)
	if _, err = pool.Exec(ctx, `INSERT INTO data_rights_export_artifacts(request_id,body,checksum_sha256,size_bytes,expires_at) VALUES($1,$2,$3,$4,now()+interval '7 days')`, legacy.ID, oldBody, hex.EncodeToString(sum[:]), len(oldBody)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_rights_requests SET status='ready' WHERE id=$1`, legacy.ID); err != nil {
		t.Fatal(err)
	}
	downloaded, _, err := service.Download(ctx, owner.ID, legacy.ID)
	if err != nil || string(downloaded) != string(oldBody) {
		t.Fatal("legacy compatibility", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.OpenExport(ctx, owner.ID, legacy.ID); !errors.Is(err, datarights.ErrNotReady) {
		t.Fatal("inactive owner download", err)
	}
}
