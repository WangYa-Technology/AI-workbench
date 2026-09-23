package media

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func inventoryS3(endpoint string) *S3Store {
	return NewS3Store(S3Config{Bucket: "inventory", Prefix: "media", Region: "us-east-1", Endpoint: endpoint,
		AccessKeyID: "fixture-key", SecretAccessKey: "fixture-secret", PathStyle: true})
}

func inventoryXML(contents, count, truncated, next string) string {
	return `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>inventory</Name><Prefix>media%2F</Prefix><EncodingType>url</EncodingType><KeyCount>` + count + `</KeyCount><IsTruncated>` + truncated + `</IsTruncated>` + contents + next + `</ListBucketResult>`
}

func inventoryObject(key string) string {
	return `<Contents><Key>` + key + `</Key><Size>9</Size></Contents>`
}

func TestS3InventoryPaginationAndEncodedKeys(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/inventory" || r.URL.Query().Get("list-type") != "2" ||
			r.URL.Query().Get("prefix") != "media/" || r.URL.Query().Get("encoding-type") != "url" ||
			!strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("invalid signed listing request")
		}
		switch call {
		case 1:
			fmt.Fprint(w, inventoryXML(inventoryObject("media%2Fa%2Bb"), "1", "true", "<NextContinuationToken>second</NextContinuationToken>"))
		case 2:
			if r.URL.Query().Get("continuation-token") != "second" {
				t.Error("pagination token lost")
			}
			fmt.Fprint(w, inventoryXML(inventoryObject("media%2Fz%20z"), "1", "false", ""))
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	var keys []string
	err := inventoryS3(server.URL).WalkInventory(t.Context(), 2, func(entries []InventoryEntry) error {
		for _, entry := range entries {
			keys = append(keys, entry.Key)
		}
		return nil
	})
	if err != nil || strings.Join(keys, ",") != "media/a+b,media/z z" || calls.Load() != 2 {
		t.Fatal(keys, calls.Load(), err)
	}
}

func TestS3InventoryRejectsIncompleteAndMalformedListing(t *testing.T) {
	cases := map[string]string{
		"out_of_prefix":    inventoryXML(inventoryObject("other%2Fx"), "1", "false", ""),
		"duplicate":        inventoryXML(inventoryObject("media%2Fx")+inventoryObject("media%2Fx"), "2", "false", ""),
		"count":            inventoryXML(inventoryObject("media%2Fx"), "0", "false", ""),
		"no_progress":      inventoryXML("", "0", "true", "<NextContinuationToken>next</NextContinuationToken>"),
		"no_token":         inventoryXML(inventoryObject("media%2Fx"), "1", "true", ""),
		"negative_size":    strings.Replace(inventoryXML(inventoryObject("media%2Fx"), "1", "false", ""), "<Size>9", "<Size>-1", 1),
		"bad_encoding":     inventoryXML(inventoryObject("media%2F%XX"), "1", "false", ""),
		"no_terminal_flag": strings.Replace(inventoryXML("", "0", "false", ""), "<IsTruncated>false</IsTruncated>", "", 1),
		"wrong_bucket":     strings.Replace(inventoryXML("", "0", "false", ""), "<Name>inventory", "<Name>other", 1),
		"bad_xml":          "<ListBucketResult>",
		"oversized":        strings.Repeat("x", (4<<20)+1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			if err := inventoryS3(server.URL).WalkInventory(t.Context(), 10, func([]InventoryEntry) error { return nil }); err == nil {
				t.Fatal("accepted incomplete listing")
			}
		})
	}
}

func TestS3InventoryLimitCancellationAndRedirect(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	for _, mode := range []string{"limit", "redirect", "cancel", "visitor"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "redirect" {
					http.Redirect(w, r, target.URL, 307)
					return
				}
				fmt.Fprint(w, inventoryXML(inventoryObject("media%2Fa")+inventoryObject("media%2Fb"), "2", "false", ""))
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			limit := 10
			if mode == "limit" {
				limit = 1
			}
			failure := errors.New("report unavailable")
			err := inventoryS3(server.URL).WalkInventory(ctx, limit, func([]InventoryEntry) error {
				if mode == "visitor" {
					return failure
				}
				return nil
			})
			if err == nil || redirected.Load() != 0 {
				t.Fatal("boundary escaped", err)
			}
			if mode == "limit" && !errors.Is(err, ErrInventoryLimit) {
				t.Fatal(err)
			}
			if mode == "visitor" && !errors.Is(err, failure) {
				t.Fatal(err)
			}
		})
	}
}
