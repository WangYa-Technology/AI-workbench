package media_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestS3MissingObjectRequiresAccessibleBucket(t *testing.T) {
	for _, tc := range []struct{ bucket, delete int }{{404, 204}, {404, 404}, {403, 204}, {403, 404}} {
		t.Run(fmt.Sprintf("bucket_%d_delete_%d", tc.bucket, tc.delete), func(t *testing.T) {
			var available atomic.Bool
			var bucketChecks atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "" {
					t.Error("unsigned storage request")
				}
				if r.Method == http.MethodHead && r.URL.Path == "/private-media" {
					bucketChecks.Add(1)
					if available.Load() {
						w.WriteHeader(http.StatusOK)
					} else {
						w.WriteHeader(tc.bucket)
					}
					return
				}
				if r.URL.Path != "/private-media/object.txt" {
					t.Errorf("wrong storage boundary: %s", r.URL.Path)
				}
				if r.Method == http.MethodDelete {
					w.WriteHeader(tc.delete)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			store := boundaryS3Store(server.URL)
			ctx := context.Background()
			if _, err := store.Stat(ctx, "object.txt"); !errors.Is(err, media.ErrStorageUnavailable) || errors.Is(err, media.ErrNotFound) {
				t.Error("inaccessible bucket classified as absent object", err)
			}
			if object, err := store.Open(ctx, "object.txt", nil); !errors.Is(err, media.ErrStorageUnavailable) || errors.Is(err, media.ErrNotFound) {
				if object.Body != nil {
					_ = object.Body.Close()
				}
				t.Error("inaccessible bucket classified as absent content", err)
			}
			if err := media.DeleteVerified(ctx, store, "object.txt"); !errors.Is(err, media.ErrStorageUnavailable) || errors.Is(err, media.ErrNotFound) {
				t.Error("inaccessible bucket produced verified deletion")
			}
			if bucketChecks.Load() != 3 {
				t.Error("missing bucket verification", bucketChecks.Load())
			}
			available.Store(true)
			if err := media.DeleteVerified(ctx, store, "object.txt"); err != nil {
				t.Fatal("could not verify absence after bucket recovery", err)
			}
			available.Store(false)
			if err := media.DeleteVerified(ctx, store, "object.txt"); !errors.Is(err, media.ErrStorageUnavailable) {
				t.Fatal("previous bucket availability was reused as current evidence", err)
			}
		})
	}
}

func TestS3DeletionRequiresConfirmedAbsence(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		deleteStatus, headCode int
		success                bool
	}{
		{"absent", 204, 404, true},
		{"still_present", 204, 200, false},
		{"inspection_forbidden", 204, 403, false},
		{"delete_forbidden", 403, 404, false},
		{"already_missing", 404, 404, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var deletes, inspections atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead && r.URL.Path == "/private-media" {
					if r.Header.Get("Authorization") == "" {
						t.Error("missing signed bucket check")
					}
					w.WriteHeader(http.StatusOK)
					return
				}
				if r.URL.Path != "/private-media/object.txt" || r.Header.Get("Authorization") == "" {
					t.Error("wrong deletion location or missing signature")
				}
				switch r.Method {
				case http.MethodDelete:
					deletes.Add(1)
					w.WriteHeader(tc.deleteStatus)
				case http.MethodHead:
					inspections.Add(1)
					if deletes.Load() == 0 {
						t.Error("absence checked before deletion")
					}
					if tc.headCode == http.StatusOK {
						w.Header().Set("Content-Length", "42")
					}
					w.WriteHeader(tc.headCode)
				default:
					t.Error("unexpected storage operation")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			store := boundaryS3Store(server.URL)
			err := media.DeleteVerified(context.Background(), store, "object.txt")
			if (err == nil) != tc.success || deletes.Load() != 1 {
				t.Fatal("incorrect deletion result", err, deletes.Load())
			}
			if tc.deleteStatus == http.StatusForbidden {
				if inspections.Load() != 0 {
					t.Fatal("delete failure was ignored")
				}
			} else if inspections.Load() != 1 || !tc.success && !errors.Is(err, media.ErrDeletionUnverified) {
				t.Fatal("unverified outcome lost", inspections.Load(), err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := media.DeleteVerified(ctx, store, "object.txt"); !errors.Is(err, context.Canceled) || deletes.Load() != 1 {
				t.Fatal("cancelled cleanup reached storage", err)
			}
		})
	}
}
