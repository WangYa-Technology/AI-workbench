package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAssetPrivateJSONNoStore(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	root := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "cache_owner")
	asset, err := assets.NewService(pool, root).Upload(t.Context(), owner.ID, assets.UploadInput{Title: "Private JSON", Filename: "private.txt", Reader: strings.NewReader("private text")})
	if err != nil {
		t.Fatal(err)
	}
	outsider := testHTTPClient(t)
	registerGovernanceUser(t, outsider, server.URL, "cache_outsider")
	for _, path := range []string{"/assets", "/assets/saved-works", "/assets/" + asset.ID.String(), "/assets/" + asset.ID.String() + "/usages"} {
		for identity, c := range []*http.Client{client, outsider, testHTTPClient(t)} {
			response := requestJSON(t, c, http.MethodGet, server.URL+"/api/v1"+path, nil, nil)
			if response.Header.Get("Cache-Control") != "private, no-store" {
				t.Fatalf("%s status=%d cache=%q", path, response.StatusCode, response.Header.Get("Cache-Control"))
			}
			expected := http.StatusOK
			if identity == 2 {
				expected = http.StatusUnauthorized
			} else if identity == 1 && strings.Contains(path, asset.ID.String()) {
				expected = http.StatusForbidden
			}
			if response.StatusCode != expected {
				t.Fatalf("identity=%d path=%s status=%d expected=%d", identity, path, response.StatusCode, expected)
			}
		}
	}
	for _, path := range []string{"/assets/not-uuid", "/assets/not-uuid/usages", "/assets?limit=bad", "/assets/saved-works?limit=bad"} {
		response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1"+path, nil, nil)
		if (response.StatusCode != 400 && response.StatusCode != 422) || response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("invalid input %s: %d %v", path, response.StatusCode, response.Header)
		}
	}
}
