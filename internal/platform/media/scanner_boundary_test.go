package media_test

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func scannerCleanResponse(digest string) []byte {
	body, _ := json.Marshal(map[string]string{"status": "clean", "reasonCode": "no_threat_detected", "contentSha256": digest, "engine": "boundary-scanner", "version": "1"})
	return body
}

func TestHTTPScannerNeverFollowsRedirects(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, sameOrigin := range []bool{true, false} {
			t.Run(fmt.Sprintf("%d/same_origin_%t", code, sameOrigin), func(t *testing.T) {
				var destinationCalls atomic.Int32
				destination := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					destinationCalls.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					_, _ = w.Write(scannerCleanResponse(r.Header.Get("X-HCAI-Content-SHA256")))
				})
				target := httptest.NewServer(destination)
				defer target.Close()
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/redirected" {
						destination.ServeHTTP(w, r)
						return
					}
					_, _ = io.Copy(io.Discard, r.Body)
					location := target.URL + "/redirected"
					if sameOrigin {
						location = "/redirected"
					}
					w.Header().Set("Location", location)
					w.WriteHeader(code)
				}))
				defer source.Close()
				result, err := media.NewHTTPScanner(source.URL, "private-fixture-token", time.Second).Scan(t.Context(), "private/source.txt", "text/plain", []byte("private licensed original"))
				if err == nil || result.Status != "" || destinationCalls.Load() != 0 || jobs.ShouldRetry(err) {
					t.Fatalf("redirect accepted/forwarded: status=%s error=%v requests=%d", result.Status, err, destinationCalls.Load())
				}
			})
		}
	}
}

func TestHTTPScannerRequiresCompleteBoundedResponse(t *testing.T) {
	content := []byte("private licensed original")
	digest := sha256.Sum256(content)
	valid := string(scannerCleanResponse(hex.EncodeToString(digest[:])))
	for _, testCase := range []struct {
		name, body        string
		compressed, valid bool
	}{
		{"ordinary", valid, false, true},
		{"exact_limit", valid + strings.Repeat(" ", (16<<10)-len(valid)), false, true},
		{"over_limit_whitespace", valid + strings.Repeat(" ", (16<<10)-len(valid)+1), false, false},
		{"hidden_tail", valid + strings.Repeat(" ", (16<<10)-len(valid)) + `{"status":"rejected"}`, false, false},
		{"compressed_over_limit", valid + strings.Repeat(" ", (16<<10)-len(valid)) + "not-json", true, false},
		{"two_results", valid + valid, false, false},
		{"truncated_json", valid[:len(valid)-1], false, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if testCase.compressed {
					w.Header().Set("Content-Encoding", "gzip")
					writer := gzip.NewWriter(w)
					_, _ = io.WriteString(writer, testCase.body)
					_ = writer.Close()
					return
				}
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer source.Close()
			result, err := media.NewHTTPScanner(source.URL, "private-fixture-token", time.Second).Scan(t.Context(), "private/source.txt", "text/plain", content)
			if testCase.valid {
				if err != nil || result.Status != "clean" {
					t.Fatalf("valid response rejected: %+v %v", result, err)
				}
				return
			}
			if err == nil || result.Status != "" || jobs.ShouldRetry(err) {
				t.Fatalf("invalid response trusted: %+v %v", result, err)
			}
		})
	}
}

func TestHTTPScannerResponseDeadlineAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			waiting := make(chan struct{})
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(200)
				_, _ = w.Write(scannerCleanResponse(r.Header.Get("X-HCAI-Content-SHA256")))
				w.(http.Flusher).Flush()
				close(waiting)
				<-r.Context().Done()
			}))
			defer source.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			out := make(chan error, 1)
			timeout := 150 * time.Millisecond
			if cancelled {
				timeout = 5 * time.Second
			}
			go func() {
				_, err := media.NewHTTPScanner(source.URL, "private-fixture-token", timeout).Scan(ctx, "private/source.txt", "text/plain", []byte("original"))
				out <- err
			}()
			select {
			case <-waiting:
			case <-time.After(3 * time.Second):
				t.Fatal("scanner never received request")
			}
			if cancelled {
				cancel()
			}
			select {
			case err := <-out:
				if err == nil || !jobs.ShouldRetry(err) || err.Error() != "scanner_timeout" {
					t.Fatalf("unfinished response mishandled: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("response deadline not enforced")
			}
		})
	}
}
