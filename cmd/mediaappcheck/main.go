package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
	"github.com/jackc/pgx/v5/pgxpool"
)

const applicationAcceptanceConfirmation = "I_APPROVE_MEDIA_APPLICATION_ACCEPTANCE_CALLS"

var acceptanceSchemaPattern = regexp.MustCompile(`^hcai_media_acceptance_[a-z0-9_]{8,80}$`)

type acceptanceResult struct {
	Status               string `json:"status"`
	StorageAdapter       string `json:"storageAdapter"`
	ScannerAdapter       string `json:"scannerAdapter"`
	PayloadBytes         int    `json:"payloadBytes"`
	RangeBytes           int    `json:"rangeBytes"`
	ScanStatus           string `json:"scanStatus"`
	ScannerEvidence      bool   `json:"scannerEvidence"`
	PendingPrivate       bool   `json:"pendingPrivate"`
	CrossAccountPrivate  bool   `json:"crossAccountPrivate"`
	FullReadVerified     bool   `json:"fullReadVerified"`
	RangeReadVerified    bool   `json:"rangeReadVerified"`
	DurableJobVerified   bool   `json:"durableJobVerified"`
	AuditVerified        bool   `json:"auditVerified"`
	NotificationVerified bool   `json:"notificationVerified"`
	Deleted              bool   `json:"deleted"`
}

func main() {
	if os.Getenv("MEDIA_APPLICATION_ACCEPTANCE_CONFIRM") != applicationAcceptanceConfirmation {
		fmt.Fprintln(os.Stderr, "media application acceptance disabled: exact confirmation is required")
		os.Exit(1)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "media application acceptance configuration invalid:", err)
		os.Exit(1)
	}
	schema := os.Getenv("MEDIA_APPLICATION_ACCEPTANCE_SCHEMA")
	if cfg.Environment != "staging" || cfg.MediaStorageAdapter != "s3" || cfg.MediaScannerAdapter != "http" || cfg.LocalProviderEnabled || !isolatedSchema(cfg.DatabaseURL, schema) {
		fmt.Fprintln(os.Stderr, "media application acceptance requires staging, S3/HTTP media, disabled local Provider mode, and an isolated acceptance schema")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "media application acceptance database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "media application acceptance migration failed")
		os.Exit(1)
	}
	result, err := runAcceptance(ctx, cfg, pool)
	if err != nil {
		fmt.Fprintln(os.Stderr, "media application acceptance failed:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "encode media application acceptance summary:", err)
		os.Exit(1)
	}
}

func isolatedSchema(databaseURL, expected string) bool {
	if !acceptanceSchemaPattern.MatchString(expected) {
		return false
	}
	parsed, err := url.Parse(databaseURL)
	return err == nil && parsed.Query().Get("search_path") == expected
}

func runAcceptance(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) (result acceptanceResult, err error) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	server := httptest.NewTLSServer(httpapi.New(cfg, pool, logger))
	defer server.Close()

	payload := []byte("HCAI application media staging acceptance. No user data.")
	owner, err := newClient(server)
	if err != nil {
		return result, err
	}
	suffix, err := randomSuffix()
	if err != nil {
		return result, err
	}
	if err := register(ctx, owner, server.URL, "media_owner_"+suffix, "owner_"+suffix+"@example.test"); err != nil {
		return result, err
	}
	uploaded, err := upload(ctx, owner, server.URL, payload)
	if err != nil {
		return result, err
	}

	store := media.NewCatalogFromConfig(cfg).Primary()
	var storageBackend, storageKey string
	if err := pool.QueryRow(ctx, `SELECT storage_backend,storage_key FROM assets WHERE id=$1`, uploaded.ID).Scan(&storageBackend, &storageKey); err != nil {
		return result, fmt.Errorf("read persisted media evidence")
	}
	stored := true
	defer func() {
		if !stored {
			return
		}
		cleanupErr := store.Delete(context.WithoutCancel(ctx), storageKey)
		if cleanupErr != nil && !errors.Is(cleanupErr, media.ErrNotFound) && err == nil {
			err = fmt.Errorf("clean application acceptance object")
		}
	}()
	if storageBackend != cfg.MediaStorageAdapter {
		return result, fmt.Errorf("uploaded Asset did not retain the configured backend")
	}

	pendingStatus, _, _, err := get(ctx, owner, server.URL+uploaded.MediaURL, "")
	if err != nil || pendingStatus != http.StatusNotFound {
		return result, fmt.Errorf("pending Asset content was not private")
	}

	repository := jobs.NewRepository(pool)
	workerName := "media-application-acceptance"
	job, err := repository.Claim(ctx, workerName, 30*time.Second)
	if err != nil || job.Kind != assets.ScanJobKind {
		return result, fmt.Errorf("durable Asset scan job was unavailable")
	}
	assetService := assets.NewServiceWithMedia(pool, media.NewCatalogFromConfig(cfg), assets.NewScannerFromConfig(cfg))
	if err := assetService.HandleScanJob(ctx, job); err != nil {
		_ = repository.Fail(context.WithoutCancel(ctx), job, workerName, err)
		return result, fmt.Errorf("execute durable Asset scan job")
	}
	if err := repository.Complete(ctx, job, workerName); err != nil {
		return result, fmt.Errorf("complete durable Asset scan job")
	}

	assetStatus, scanReason, err := readAsset(ctx, owner, server.URL, uploaded.ID.String())
	if err != nil || assetStatus != "clean" || strings.TrimSpace(scanReason) == "" {
		return result, fmt.Errorf("scanned Asset did not become clean")
	}
	fullStatus, _, fullBody, err := get(ctx, owner, server.URL+uploaded.MediaURL, "")
	if err != nil || fullStatus != http.StatusOK || !bytes.Equal(fullBody, payload) {
		return result, fmt.Errorf("full Asset delivery did not match")
	}
	rangeStart, rangeEnd := 5, 20
	rangeStatus, rangeHeader, rangeBody, err := get(ctx, owner, server.URL+uploaded.MediaURL, fmt.Sprintf("bytes=%d-%d", rangeStart, rangeEnd))
	expectedContentRange := fmt.Sprintf("bytes %d-%d/%d", rangeStart, rangeEnd, len(payload))
	if err != nil || rangeStatus != http.StatusPartialContent || rangeHeader.Get("Content-Range") != expectedContentRange || !bytes.Equal(rangeBody, payload[rangeStart:rangeEnd+1]) {
		return result, fmt.Errorf("Range Asset delivery did not match")
	}

	other, err := newClient(server)
	if err != nil {
		return result, err
	}
	if err := register(ctx, other, server.URL, "media_other_"+suffix, "other_"+suffix+"@example.test"); err != nil {
		return result, err
	}
	otherStatus, _, _, err := get(ctx, other, server.URL+uploaded.MediaURL, "")
	if err != nil || otherStatus != http.StatusForbidden {
		return result, fmt.Errorf("cross-account Asset content was not private")
	}

	var jobCount, attemptCount, auditCount, scannerEvidenceCount, notificationCount int
	if err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM jobs WHERE id=$1 AND kind=$2 AND status='succeeded'),
		  (SELECT count(*) FROM job_attempts WHERE job_id=$1 AND status='succeeded'),
		  (SELECT count(*) FROM audit_events WHERE resource_id=$3 AND action IN ('asset.uploaded','asset.scan_completed')),
		  (SELECT count(*) FROM audit_events WHERE resource_id=$3 AND action='asset.scan_completed'
		    AND metadata->>'scannerAdapter'=$4 AND length(metadata->>'engine')>0 AND length(metadata->>'version')>0),
		  (SELECT count(*) FROM notifications WHERE resource_id=$3 AND kind='asset.scan_completed')`,
		job.ID, assets.ScanJobKind, uploaded.ID, assets.NewScannerFromConfig(cfg).Adapter()).Scan(&jobCount, &attemptCount, &auditCount, &scannerEvidenceCount, &notificationCount); err != nil {
		return result, fmt.Errorf("read application media evidence")
	}
	if jobCount != 1 || attemptCount != 1 || auditCount != 2 || scannerEvidenceCount != 1 || notificationCount != 1 {
		return result, fmt.Errorf("application media evidence was incomplete")
	}
	if err := store.Delete(ctx, storageKey); err != nil {
		return result, fmt.Errorf("delete application acceptance object")
	}
	if _, statErr := store.Stat(ctx, storageKey); !errors.Is(statErr, media.ErrNotFound) {
		return result, fmt.Errorf("deleted application acceptance object remained visible")
	}
	stored = false
	result = acceptanceResult{
		Status: "passed", StorageAdapter: storageBackend, ScannerAdapter: assets.NewScannerFromConfig(cfg).Adapter(),
		PayloadBytes: len(payload), RangeBytes: rangeEnd - rangeStart + 1, ScanStatus: assetStatus,
		ScannerEvidence: true, PendingPrivate: true, CrossAccountPrivate: true, FullReadVerified: true,
		RangeReadVerified: true, DurableJobVerified: true, AuditVerified: true, NotificationVerified: true, Deleted: true,
	}
	return result, nil
}

func newClient(server *httptest.Server) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := server.Client()
	client.Jar = jar
	client.Timeout = 30 * time.Second
	return client, nil
}

func randomSuffix() (string, error) {
	value := make([]byte, 6)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create application acceptance identifier")
	}
	return hex.EncodeToString(value), nil
}

func register(ctx context.Context, client *http.Client, baseURL, handle, email string) error {
	body, _ := json.Marshal(map[string]string{
		"email": email, "password": "Media-acceptance-password-2026", "handle": handle,
		"displayName": "Media Acceptance", "locale": "en-US", "timezone": "UTC",
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/auth/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("register application acceptance account")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("register application acceptance account: status %d", response.StatusCode)
	}
	return nil
}

func upload(ctx context.Context, client *http.Client, baseURL string, payload []byte) (assets.Asset, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("title", "Media staging acceptance"); err != nil {
		return assets.Asset{}, err
	}
	part, err := writer.CreateFormFile("file", "media-acceptance.txt")
	if err != nil {
		return assets.Asset{}, err
	}
	if _, err := part.Write(payload); err != nil {
		return assets.Asset{}, err
	}
	if err := writer.Close(); err != nil {
		return assets.Asset{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/assets/uploads", &body)
	if err != nil {
		return assets.Asset{}, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Idempotency-Key", "media-acceptance-"+rand.Text())
	response, err := client.Do(request)
	if err != nil {
		return assets.Asset{}, fmt.Errorf("upload application acceptance Asset")
	}
	defer response.Body.Close()
	var item assets.Asset
	if response.StatusCode != http.StatusCreated || json.NewDecoder(response.Body).Decode(&item) != nil || item.ScanStatus != "pending" {
		return assets.Asset{}, fmt.Errorf("upload application acceptance Asset: status %d", response.StatusCode)
	}
	return item, nil
}

func readAsset(ctx context.Context, client *http.Client, baseURL, assetID string) (string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/assets/"+assetID, nil)
	if err != nil {
		return "", "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("read application acceptance Asset")
	}
	defer response.Body.Close()
	var item struct {
		ScanStatus string  `json:"scanStatus"`
		ScanReason *string `json:"scanReason"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&item) != nil || item.ScanReason == nil {
		return "", "", fmt.Errorf("read application acceptance Asset: status %d", response.StatusCode)
	}
	return item.ScanStatus, *item.ScanReason, nil
}

func get(ctx context.Context, client *http.Client, target, byteRange string) (int, http.Header, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(assets.MaxUploadSize+1)))
	return response.StatusCode, response.Header.Clone(), body, err
}
