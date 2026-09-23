package assets_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestScannerBoundaryControlsMarketAdmission(t *testing.T) {
	for _, mode := range []string{"redirect", "oversized", "malformed", "timeout_then_clean", "unavailable_exhausted"} {
		t.Run(mode, func(t *testing.T) {
			pool, cleanup := assetTestPool(t)
			defer cleanup()
			ctx := t.Context()
			owner := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Scanner owner','creator')`, owner, owner.String()+"@test.local", "scan_"+owner.String()[:8]); err != nil {
				t.Fatal(err)
			}
			var recovered atomic.Bool
			var targetCalls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1); _, _ = io.Copy(io.Discard, r.Body) }))
			defer target.Close()
			scannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch mode {
				case "redirect":
					w.Header().Set("Location", target.URL)
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				case "unavailable_exhausted":
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				case "timeout_then_clean":
					if !recovered.Load() {
						w.WriteHeader(200)
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
				case "malformed":
					_, _ = io.WriteString(w, `{"status":"clean"}`)
					return
				}
				raw, _ := json.Marshal(map[string]string{"status": "clean", "reasonCode": "no_threat_detected", "contentSha256": r.Header.Get("X-HCAI-Content-SHA256"), "engine": "fixture", "version": "1"})
				_, _ = w.Write(raw)
				if mode == "oversized" {
					_, _ = io.WriteString(w, strings.Repeat(" ", 16<<10)+"ignored-tail")
				}
			}))
			defer scannerServer.Close()
			store := media.NewLocalStore(t.TempDir())
			service := assets.NewServiceWithMedia(pool, media.NewCatalog(store), media.NewHTTPScanner(scannerServer.URL, "private-scanner-fixture-token", 150*time.Millisecond))
			asset, err := service.Upload(ctx, owner, assets.UploadInput{Title: "Private original", Filename: "original.txt", Reader: strings.NewReader("Original content for a licensed market resource."), RequestID: "scanner-boundary"})
			if err != nil {
				t.Fatal(err)
			}
			job := claimAssetJobKind(t, ctx, pool, "scan-boundary-worker", assets.ScanJobKind)
			repo := jobs.NewRepository(pool)
			for attempt := 1; ; attempt++ {
				err = service.HandleScanJob(ctx, job)
				if mode == "timeout_then_clean" && attempt == 1 || mode == "unavailable_exhausted" && attempt < job.MaxAttempts {
					if err == nil || !jobs.ShouldRetry(err) {
						t.Fatalf("temporary error did not retain retry: %v", err)
					}
					pending, getErr := service.GetOwned(ctx, owner, asset.ID)
					if getErr != nil || pending.ScanStatus != "pending" {
						t.Fatalf("temporary error completed asset: %+v %v", pending, getErr)
					}
					if err = repo.Fail(ctx, job, "scan-boundary-worker", err); err != nil {
						t.Fatal(err)
					}
					if _, err = pool.Exec(ctx, `UPDATE jobs SET available_at=now() WHERE id=$1`, job.ID); err != nil {
						t.Fatal(err)
					}
					recovered.Store(true)
					job = claimAssetJobKind(t, ctx, pool, "scan-boundary-worker", assets.ScanJobKind)
					continue
				}
				if err != nil {
					t.Fatalf("terminal scanner error left asset pending instead of review: %v", err)
				}
				if err = repo.Complete(ctx, job, "scan-boundary-worker"); err != nil {
					t.Fatal(err)
				}
				break
			}
			asset, err = service.GetOwned(ctx, owner, asset.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := "review"
			if mode == "timeout_then_clean" {
				expected = "clean"
			}
			if asset.ScanStatus != expected || asset.ScannedAt == nil || asset.ScanReason == nil {
				t.Fatalf("scan state %+v", asset)
			}
			if targetCalls.Load() != 0 {
				t.Fatal("scan redirect disclosed original")
			}
			if err = service.HandleScanJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			var auditCount, notices int
			if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE action='asset.scan_completed' AND resource_id=$1),(SELECT count(*) FROM notifications WHERE kind='asset.scan_completed' AND resource_id=$1)`, asset.ID).Scan(&auditCount, &notices); err != nil || auditCount != 1 || notices != 1 {
				t.Fatalf("scan completion duplicated: %d %d %v", auditCount, notices, err)
			}
			content, readErr := service.Content(ctx, owner, asset.ID)
			if expected == "review" && !errors.Is(readErr, assets.ErrNotFound) {
				t.Fatalf("untrusted original readable: %v", readErr)
			}
			if expected == "clean" && (readErr != nil || content.MimeType == "") {
				t.Fatalf("verified original unavailable: %v", readErr)
			}
			draft := marketplace.ProductDraft{Title: "Scanner market resource", Description: "Licensed text file.", ProductType: "asset", Category: "market_asset", AssetID: asset.ID, PriceCents: 1900, Currency: "USD", LicenseCode: "hcai-commercial-standard-v1", AIDisclosure: "Independently uploaded original.", IncludedFiles: []string{"original.txt"}}
			_, err = marketplace.NewService(pool).MutateListing(ctx, owner, uuid.Nil, "create", "scan-market-admission", "test", marketplace.ListingMutation{Draft: &draft})
			if expected == "review" && !errors.Is(err, marketplace.ErrListingSource) {
				t.Fatalf("untrusted original entered market: %v", err)
			}
			if expected == "clean" && err != nil {
				t.Fatalf("verified original blocked from market: %v", err)
			}
		})
	}
}
