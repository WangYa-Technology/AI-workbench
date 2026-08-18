package media_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestHTTPScannerBindsAuthenticatedContentEvidence(t *testing.T) {
	content := []byte("scanned object bytes")
	digest := sha256.Sum256(content)
	digestText := hex.EncodeToString(digest[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer local-scanner-token" ||
			r.Header.Get("Content-Type") != "text/plain" || r.Header.Get("X-HCAI-Scanner-Contract") != "1" ||
			r.Header.Get("X-HCAI-Object-Key") != "media/object.txt" || r.Header.Get("X-HCAI-Content-SHA256") != digestText || string(body) != string(content) {
			t.Errorf("scanner request mismatch: method=%s headers=%v body=%q", r.Method, r.Header, body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "clean", "reasonCode": "no_threat_detected", "contentSha256": digestText, "engine": "contract-scanner", "version": "1.0.0",
		})
	}))
	defer server.Close()
	scanner := media.NewHTTPScanner(server.URL, "local-scanner-token", time.Second)
	result, err := scanner.Scan(context.Background(), "media/object.txt", "text/plain", content)
	if err != nil || result.Status != "clean" || result.ReasonCode != "no_threat_detected" || result.Engine != "contract-scanner" {
		t.Fatalf("scanner result mismatch: result=%+v err=%v", result, err)
	}
}

func TestHTTPScannerRejectsUnboundResponseAndClassifiesOutage(t *testing.T) {
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "clean", "reasonCode": "no_threat_detected", "contentSha256": "wrong", "engine": "contract-scanner", "version": "1.0.0",
		})
	}))
	defer invalid.Close()
	if _, err := media.NewHTTPScanner(invalid.URL, "local-scanner-token", time.Second).Scan(context.Background(), "object.txt", "text/plain", []byte("body")); err == nil || jobs.ShouldRetry(err) {
		t.Fatalf("unbound scanner response did not fail permanently: %v", err)
	}

	outage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer outage.Close()
	if _, err := media.NewHTTPScanner(outage.URL, "local-scanner-token", time.Second).Scan(context.Background(), "object.txt", "text/plain", []byte("body")); err == nil || !jobs.ShouldRetry(err) {
		t.Fatalf("scanner outage was not retryable: %v", err)
	}
}
