package media_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestS3StoreSignedImmutableLifecycleAndRange(t *testing.T) {
	var mu sync.Mutex
	var stored []byte
	deleted := false
	modified := time.Now().UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && r.URL.Path == "/hcai-contract" {
			if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
				t.Error("unsigned bucket check")
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/hcai-contract/media/object.txt" || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Errorf("S3 request identity mismatch: method=%s path=%s authorization=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			if r.Header.Get("If-None-Match") != "*" || r.Header.Get("X-Amz-Server-Side-Encryption") != "AES256" || r.Header.Get("X-Amz-Checksum-Sha256") == "" {
				t.Errorf("S3 immutable/checksum/encryption headers missing: %v", r.Header)
			}
			if stored != nil && !deleted {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code><Message>exists</Message></Error>`)
				return
			}
			stored, _ = io.ReadAll(r.Body)
			deleted = false
			w.Header().Set("ETag", `"contract-etag"`)
		case http.MethodHead:
			if stored == nil || deleted {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(stored)))
			w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
			w.Header().Set("ETag", `"contract-etag"`)
		case http.MethodGet:
			if stored == nil || deleted {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			start, end := 0, len(stored)-1
			status := http.StatusOK
			if value := r.Header.Get("Range"); value != "" {
				if _, err := fmt.Sscanf(value, "bytes=%d-%d", &start, &end); err != nil {
					t.Errorf("invalid Range from client: %q", value)
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(stored)))
				status = http.StatusPartialContent
			}
			w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
			w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
			w.Header().Set("ETag", `"contract-etag"`)
			w.WriteHeader(status)
			_, _ = w.Write(stored[start : end+1])
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected S3 method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store := media.NewS3Store(media.S3Config{
		Bucket: "hcai-contract", Region: "us-east-1", Endpoint: server.URL,
		AccessKeyID: "contract-access", SecretAccessKey: "contract-secret", PathStyle: true, Prefix: "media",
	})
	key, err := store.ObjectKey("object.txt")
	if err != nil || key != "media/object.txt" {
		t.Fatalf("S3 key mismatch: key=%q err=%v", key, err)
	}
	ctx := context.Background()
	staged, err := media.Stage(ctx, bytes.NewReader([]byte("0123456789")), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	if err = staged.Put(ctx, store, key, "text/plain"); err != nil {
		t.Fatal(err)
	}
	verified, err := media.OpenVerified(ctx, store, key, staged.SHA256, staged.Size, &media.ByteRange{Start: 3, End: 6})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(verified.Body)
	if err != nil || string(actual) != "3456" {
		t.Fatalf("verified S3 range: %q %v", actual, err)
	}
	if err = verified.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, key, []byte("replacement"), "text/plain"); !errors.Is(err, media.ErrConflict) {
		t.Fatalf("S3 overwrite was accepted: %v", err)
	}
	info, err := store.Stat(ctx, key)
	if err != nil || info.Size != 10 || info.ETag != `"contract-etag"` {
		t.Fatalf("S3 stat mismatch: info=%+v err=%v", info, err)
	}
	object, err := store.Open(ctx, key, &media.ByteRange{Start: 3, End: 6})
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil || string(body) != "3456" {
		t.Fatalf("S3 range mismatch: body=%q read=%v close=%v", body, readErr, closeErr)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("deleted S3 object remained available: %v", err)
	}
}
