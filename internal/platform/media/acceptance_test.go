package media_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestMediaAcceptanceLifecycleAndCleanup(t *testing.T) {
	root := t.TempDir()
	token := "media-acceptance-test-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		digest := sha256.Sum256(body)
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("X-HCAI-Scanner-Contract") != "1" ||
			r.Header.Get("X-HCAI-Content-SHA256") != hex.EncodeToString(digest[:]) {
			t.Errorf("unexpected scanner acceptance request: method=%s headers=%v", r.Method, r.Header)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "clean", "reasonCode": "no_threat_detected", "contentSha256": hex.EncodeToString(digest[:]),
			"engine": "acceptance-fixture", "version": "1.0.0",
		})
	}))
	defer server.Close()

	result, err := media.RunAcceptance(context.Background(), media.NewLocalStore(root), media.NewHTTPScanner(server.URL, token, time.Second), "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "passed" || !result.ImmutableWrite || !result.Deleted || result.ScanStatus != "clean" || result.RangeBytes != 16 || result.ObjectKeySHA256 == "" {
		t.Fatalf("unexpected acceptance result: %+v", result)
	}
	assertEmptyDirectory(t, root)
}

func TestMediaAcceptanceRemovesObjectAfterScannerFailure(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	if _, err := media.RunAcceptance(context.Background(), media.NewLocalStore(root), media.NewHTTPScanner(server.URL, "media-acceptance-test-token", time.Second), "fedcba9876543210"); err == nil {
		t.Fatal("scanner outage did not fail media acceptance")
	}
	assertEmptyDirectory(t, root)
}

func TestMediaAcceptanceRejectsUnsafeSuffixBeforeWriting(t *testing.T) {
	root := t.TempDir()
	if _, err := media.RunAcceptance(context.Background(), media.NewLocalStore(root), media.NewHTTPScanner("http://127.0.0.1:1", "media-acceptance-test-token", time.Second), "../unsafe"); err == nil {
		t.Fatal("unsafe acceptance suffix was accepted")
	}
	assertEmptyDirectory(t, root)
}

func assertEmptyDirectory(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("acceptance object was not cleaned: %v", entries)
	}
}
