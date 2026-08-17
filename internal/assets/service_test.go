package assets_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUploadedAssetScanningAndControlledReview(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, adminID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Upload Owner','creator','active'),($4,$5,$6,'Media Admin','admin','active')`,
		ownerID, ownerID.String()+"@test.local", "owner_"+ownerID.String()[:8], adminID, adminID.String()+"@test.local", "admin_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	mediaRoot := t.TempDir()
	service := assets.NewService(pool, mediaRoot)
	clean, err := service.Upload(ctx, ownerID, assets.UploadInput{
		Title: "Clean upload", Filename: "clean-note.txt", Reader: strings.NewReader("A deterministic local upload with normal content."), RequestID: "upload-clean",
	})
	if err != nil || clean.ScanStatus != "pending" {
		t.Fatalf("create clean upload: %#v %v", clean, err)
	}
	if _, _, err := service.Content(ctx, ownerID, clean.ID); !errors.Is(err, assets.ErrNotFound) {
		t.Fatalf("pending content was readable: %v", err)
	}
	repository := jobs.NewRepository(pool)
	job := claimAssetJobKind(t, ctx, pool, "asset-test-worker", assets.ScanJobKind)
	if err := service.HandleScanJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, job, "asset-test-worker"); err != nil {
		t.Fatal(err)
	}
	clean, err = service.GetOwned(ctx, ownerID, clean.ID)
	if err != nil || clean.ScanStatus != "clean" || clean.ScannedAt == nil || clean.ScanReason == nil {
		t.Fatalf("clean scan result: %#v %v", clean, err)
	}
	path, mimeType, err := service.Content(ctx, ownerID, clean.ID)
	if err != nil || mimeType != "text/plain; charset=utf-8" {
		t.Fatalf("clean content unavailable: path=%s mime=%s err=%v", path, mimeType, err)
	}
	if body, err := os.ReadFile(path); err != nil || !strings.Contains(string(body), "normal content") {
		t.Fatalf("unexpected stored upload: %q %v", body, err)
	}
	if clean.FamilyID != clean.ID || clean.VersionNumber != 1 || !clean.IsLatestVersion || len(clean.Versions) != 1 {
		t.Fatalf("root Asset version metadata is invalid: %#v", clean)
	}
	version, err := service.UploadVersion(ctx, ownerID, clean.ID, assets.VersionInput{
		UploadInput: assets.UploadInput{Title: "Clean upload revised", Filename: "clean-note-v2.txt", Reader: strings.NewReader("A revised deterministic local upload."), RequestID: "upload-version"},
		Note:        "Replace the draft copy with the reviewed revision.",
	})
	if err != nil || version.FamilyID != clean.ID || version.VersionNumber != 2 || version.SupersedesAssetID == nil || *version.SupersedesAssetID != clean.ID {
		t.Fatalf("create Asset version: %#v %v", version, err)
	}
	job = claimAssetJobKind(t, ctx, pool, "asset-test-worker", assets.ScanJobKind)
	if err := service.HandleScanJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, job, "asset-test-worker"); err != nil {
		t.Fatal(err)
	}
	version, err = service.GetOwned(ctx, ownerID, version.ID)
	if err != nil || version.ScanStatus != "clean" || !version.IsLatestVersion || len(version.Versions) != 2 || version.Versions[0].ID != version.ID {
		t.Fatalf("Asset version history is invalid: %#v %v", version, err)
	}
	clean, err = service.GetOwned(ctx, ownerID, clean.ID)
	if err != nil || clean.IsLatestVersion || len(clean.Versions) != 2 {
		t.Fatalf("previous Asset version did not retain lineage: %#v %v", clean, err)
	}
	listed, err := service.List(ctx, ownerID, assets.ListInput{})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != version.ID {
		t.Fatalf("Asset library did not collapse the family to its latest version: %#v %v", listed, err)
	}
	var versionEventID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM asset_version_events WHERE asset_id=$1 AND previous_asset_id=$2`, version.ID, clean.ID).Scan(&versionEventID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE asset_version_events SET reason='tampered' WHERE id=$1`, versionEventID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("Asset version evidence was mutable: %v", err)
	}

	review, err := service.Upload(ctx, ownerID, assets.UploadInput{
		Title: "Review upload", Filename: "review-note.txt", Reader: strings.NewReader("HCAI_LOCAL_TEST_REVIEW_UPLOAD"), RequestID: "upload-review",
	})
	if err != nil {
		t.Fatal(err)
	}
	job = claimAssetJobKind(t, ctx, pool, "asset-test-worker", assets.ScanJobKind)
	if err := service.HandleScanJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, job, "asset-test-worker"); err != nil {
		t.Fatal(err)
	}
	review, err = service.GetOwned(ctx, ownerID, review.ID)
	if err != nil || review.ScanStatus != "review" {
		t.Fatalf("review scan result: %#v %v", review, err)
	}
	if _, _, err := service.Content(ctx, ownerID, review.ID); !errors.Is(err, assets.ErrNotFound) {
		t.Fatalf("review content was readable: %v", err)
	}
	reviewed, err := admin.NewService(pool, true).ReviewMedia(ctx, adminID, review.ID, admin.MediaReview{
		Status: "clean", Reason: "Manual Local Test review verified this plain-text Asset.", Confirmed: true,
	}, "admin-media-review")
	if err != nil || reviewed.ScanStatus != "clean" {
		t.Fatalf("controlled media review: %#v %v", reviewed, err)
	}
	if _, _, err := service.Content(ctx, ownerID, review.ID); err != nil {
		t.Fatalf("reviewed content unavailable: %v", err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_id IN ($1,$2)`, clean.ID, review.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount < 5 {
		t.Fatalf("missing upload, scan, or review audit evidence: %d", auditCount)
	}
}

func claimAssetJobKind(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner, kind string) jobs.Job {
	t.Helper()
	jobRepository := jobs.NewRepository(pool)
	notificationRepository := notifications.NewRepository(pool)
	for attempt := 0; attempt < 20; attempt++ {
		job, err := jobRepository.Claim(ctx, owner, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if job.Kind == kind {
			return job
		}
		if job.Kind != notifications.JobKind {
			t.Fatalf("unexpected job %q while waiting for %q", job.Kind, kind)
		}
		if err := notificationRepository.HandleDeliveryJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if err := jobRepository.Complete(ctx, job, owner); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatalf("job %q unavailable after draining notifications", kind)
	return jobs.Job{}
}

func assetTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	root, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	if err := root.Ping(ctx); err != nil {
		root.Close()
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	schema := "test_assets_" + uuid.NewString()[:8]
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(baseURL)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = root.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	}
}
