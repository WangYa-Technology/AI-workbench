package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAssetUploadScanAndAdminMediaHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	mediaRoot := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: mediaRoot, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "upload_owner")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("title", "HTTP uploaded note"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "http-upload.txt")
	if err != nil {
		t.Fatal(err)
	}
	uploadedContent := []byte("A safe HTTP upload for deterministic local scan verification.")
	if _, err := part.Write(uploadedContent); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/assets/uploads", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Idempotency-Key", "upload-"+time.Now().Format("150405.000000000"))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var uploaded assets.Asset
	if err := json.NewDecoder(response.Body).Decode(&uploaded); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || uploaded.ScanStatus != "pending" || uploaded.SourceType != "upload" {
		t.Fatalf("upload contract failed: status=%d asset=%#v", response.StatusCode, uploaded)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+uploaded.MediaURL, nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("pending upload content was exposed: %d", response.StatusCode)
	}

	jobRepository := jobs.NewRepository(pool)
	job := claimHTTPJobKind(t, context.Background(), pool, "http-asset-worker", assets.ScanJobKind)
	assetService := assets.NewService(pool, mediaRoot)
	if err := assetService.HandleScanJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(context.Background(), job, "http-asset-worker"); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assets/"+uploaded.ID.String(), nil, &uploaded)
	if response.StatusCode != http.StatusOK || uploaded.ScanStatus != "clean" {
		t.Fatalf("scanned Asset contract failed: status=%d asset=%#v", response.StatusCode, uploaded)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+uploaded.MediaURL, nil, nil)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("clean upload content unavailable: status=%d type=%s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	rangeRequest, err := http.NewRequest(http.MethodGet, server.URL+uploaded.MediaURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	rangeRequest.Header.Set("Range", "bytes=2-8")
	rangeResponse, err := client.Do(rangeRequest)
	if err != nil {
		t.Fatal(err)
	}
	rangeBody, readErr := io.ReadAll(rangeResponse.Body)
	rangeResponse.Body.Close()
	if readErr != nil || rangeResponse.StatusCode != http.StatusPartialContent || rangeResponse.Header.Get("Content-Range") != "bytes 2-8/61" || string(rangeBody) != string(uploadedContent[2:9]) {
		t.Fatalf("asset range response mismatch: status=%d range=%q body=%q read=%v", rangeResponse.StatusCode, rangeResponse.Header.Get("Content-Range"), rangeBody, readErr)
	}
	invalidRangeRequest, err := http.NewRequest(http.MethodGet, server.URL+uploaded.MediaURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	invalidRangeRequest.Header.Set("Range", "bytes=0-1,3-4")
	invalidRangeResponse, err := client.Do(invalidRangeRequest)
	if err != nil {
		t.Fatal(err)
	}
	invalidRangeResponse.Body.Close()
	if invalidRangeResponse.StatusCode != http.StatusRequestedRangeNotSatisfiable || invalidRangeResponse.Header.Get("Content-Range") != "bytes */61" {
		t.Fatalf("invalid asset range was accepted: status=%d range=%q", invalidRangeResponse.StatusCode, invalidRangeResponse.Header.Get("Content-Range"))
	}
	var storedKey string
	if err := pool.QueryRow(t.Context(), `SELECT storage_key FROM assets WHERE id=$1`, uploaded.ID).Scan(&storedKey); err != nil {
		t.Fatal(err)
	}
	if err := media.NewLocalStore(mediaRoot).Delete(context.Background(), storedKey); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+uploaded.MediaURL, nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing stored object did not return 404: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/admin/media", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed Admin media queue: %d", response.StatusCode)
	}

	adminClient := testHTTPClient(t)
	administrator := registerGovernanceUser(t, adminClient, server.URL, "upload_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Items []admin.MediaItem `json:"items"`
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/media", nil, &inventory)
	if response.StatusCode != http.StatusOK || len(inventory.Items) != 1 || inventory.Items[0].OwnerID != owner.ID {
		t.Fatalf("Admin media inventory failed: status=%d items=%#v", response.StatusCode, inventory.Items)
	}
	var reviewed admin.MediaItem
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/media/"+uploaded.ID.String()+"/review", map[string]any{
		"status": "rejected"}, &reviewed)
	if response.StatusCode != http.StatusOK || reviewed.ScanStatus != "rejected" {
		t.Fatalf("Admin media review failed: status=%d item=%#v", response.StatusCode, reviewed)
	}
	var mediaSignalCount, mediaScore int
	if err := pool.QueryRow(context.Background(), `SELECT count(*),COALESCE(max(score),0) FROM risk_signals WHERE source_key=$1 AND signal_type='media_rejection' AND subject_user_id=$2`, "media_rejection:"+uploaded.ID.String(), owner.ID).Scan(&mediaSignalCount, &mediaScore); err != nil || mediaSignalCount != 1 || mediaScore != 75 {
		t.Fatalf("media rejection risk evidence mismatch: count=%d score=%d err=%v", mediaSignalCount, mediaScore, err)
	}
	var riskQueue struct {
		Items []admin.RiskSignal `json:"items"`
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/risk/signals", nil, &riskQueue)
	if response.StatusCode != http.StatusOK || len(riskQueue.Items) != 1 || riskQueue.Items[0].SignalType != "media_rejection" || riskQueue.Items[0].TargetPath != "/workspace/assets/"+uploaded.ID.String() {
		t.Fatalf("media rejection risk queue mismatch: status=%d items=%#v", response.StatusCode, riskQueue.Items)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+uploaded.MediaURL, nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("rejected upload content remained exposed: %d", response.StatusCode)
	}
}

func TestAssetUploadRejectsAccountRevokedAfterAuthentication(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := t.Context()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "upload_revocation")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = accountlifecycle.Lock(ctx, tx, owner.ID); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err = writer.WriteField("title", "Revoked upload"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "revoked.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte("Uploaded before account permission changes.")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v1/assets/uploads", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Idempotency-Key", "upload-"+time.Now().Format("150405.000000000"))
	type outcome struct {
		response *http.Response
		err      error
	}
	result := make(chan outcome, 1)
	go func() { r, e := client.Do(request); result <- outcome{r, e} }()
	deadline := time.Now().Add(5 * time.Second)
	waiting := false
	for !waiting && time.Now().Before(deadline) {
		select {
		case r := <-result:
			if r.response != nil {
				r.response.Body.Close()
			}
			t.Fatalf("upload did not wait for current authorization: %v", r.err)
		default:
		}
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if !waiting {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !waiting {
		t.Fatal("upload not waiting after authentication")
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	defer r.response.Body.Close()
	if r.response.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(r.response.Body)
		t.Fatalf("account revocation status=%d body=%s", r.response.StatusCode, b)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE owner_id=$1`, owner.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("revoked upload committed: %d %v", count, err)
	}
}
