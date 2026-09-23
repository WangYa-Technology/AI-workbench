package media_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func boundaryS3Store(endpoint string) *media.S3Store {
	return media.NewS3Store(media.S3Config{Bucket: "private-media", Region: "us-east-1", Endpoint: endpoint,
		AccessKeyID: "boundary-fixture-access", SecretAccessKey: "boundary-fixture-secret", PathStyle: true})
}

func TestS3RequestsNeverFollowRedirects(t *testing.T) {
	operations := []struct {
		name, method string
		call         func(*media.S3Store) error
	}{
		{"upload", http.MethodPut, func(s *media.S3Store) error {
			return s.Put(context.Background(), "object.txt", []byte("private purchased content"), "text/plain")
		}},
		{"stat", http.MethodHead, func(s *media.S3Store) error {
			_, err := s.Stat(context.Background(), "object.txt")
			return err
		}},
		{"download", http.MethodGet, func(s *media.S3Store) error {
			o, err := s.Open(context.Background(), "object.txt", nil)
			if err == nil {
				_ = o.Body.Close()
			}
			return err
		}},
		{"delete", http.MethodDelete, func(s *media.S3Store) error {
			return s.Delete(context.Background(), "object.txt")
		}},
	}
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, sameOrigin := range []bool{false, true} {
			for _, operation := range operations {
				t.Run(fmt.Sprintf("%d/same_origin_%t/%s", status, sameOrigin, operation.name), func(t *testing.T) {
					var originCalls, targetCalls atomic.Int32
					targetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						targetCalls.Add(1)
						_, _ = io.Copy(io.Discard, r.Body)
						w.Header().Set("Content-Length", "0")
						w.WriteHeader(http.StatusOK)
					})
					target := httptest.NewServer(targetHandler)
					defer target.Close()
					origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/other-object" {
							targetHandler.ServeHTTP(w, r)
							return
						}
						originCalls.Add(1)
						if r.Method != operation.method || r.Header.Get("Authorization") == "" {
							t.Error("original storage request lost its method or signature")
						}
						_, _ = io.Copy(io.Discard, r.Body)
						location := target.URL + "/other-object"
						if sameOrigin {
							location = "/other-object"
						}
						w.Header().Set("Location", location)
						w.WriteHeader(status)
					}))
					defer origin.Close()
					err := operation.call(boundaryS3Store(origin.URL))
					if err == nil || targetCalls.Load() != 0 || originCalls.Load() != 1 {
						t.Fatalf("storage redirect escaped: err=%v original=%d target=%d", err, originCalls.Load(), targetCalls.Load())
					}
				})
			}
		}
	}
}

func TestS3ReadResponseMustMatchRequestedRange(t *testing.T) {
	cases := []struct {
		name         string
		requested    *media.ByteRange
		status       int
		contentRange string
		body         string
		valid        bool
	}{
		{"full", nil, 200, "", "0123456789", true},
		{"partial", &media.ByteRange{Start: 3, End: 6}, 206, "bytes 3-6/10", "3456", true},
		{"ignored_range", &media.ByteRange{Start: 3, End: 6}, 200, "", "0123456789", false},
		{"wrong_start", &media.ByteRange{Start: 3, End: 6}, 206, "bytes 0-3/10", "0123", false},
		{"wrong_end", &media.ByteRange{Start: 3, End: 6}, 206, "bytes 3-5/10", "345", false},
		{"missing_range", &media.ByteRange{Start: 3, End: 6}, 206, "", "3456", false},
		{"unknown_total", &media.ByteRange{Start: 3, End: 6}, 206, "bytes 3-6/*", "3456", false},
		{"out_of_bounds", &media.ByteRange{Start: 3, End: 6}, 206, "bytes 3-6/6", "3456", false},
		{"wrong_length", &media.ByteRange{Start: 3, End: 6}, 206, "bytes 3-6/10", "345", false},
		{"wrong_status", &media.ByteRange{Start: 3, End: 6}, 200, "bytes 3-6/10", "3456", false},
		{"unexpected_partial", nil, 206, "bytes 3-6/10", "3456", false},
		{"unexpected_range", nil, 200, "bytes 3-6/10", "3456", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expected := ""
				if tc.requested != nil {
					expected = fmt.Sprintf("bytes=%d-%d", tc.requested.Start, tc.requested.End)
				}
				if r.Header.Get("Range") != expected {
					t.Error("wrong request range")
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
				if tc.contentRange != "" {
					w.Header().Set("Content-Range", tc.contentRange)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			object, err := boundaryS3Store(server.URL).Open(context.Background(), "object.txt", tc.requested)
			if !tc.valid {
				if err == nil {
					_ = object.Body.Close()
				}
				if !errors.Is(err, media.ErrIntegrity) || object.Body != nil {
					t.Fatalf("mismatched storage bytes were exposed: err=%v bodyReturned=%t", err, object.Body != nil)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer object.Body.Close()
			body, err := io.ReadAll(object.Body)
			if err != nil || string(body) != tc.body || object.Info.Size != 10 {
				t.Fatalf("valid object mismatch: body=%q size=%d err=%v", body, object.Info.Size, err)
			}
		})
	}
}
