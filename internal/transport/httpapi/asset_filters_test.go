package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAssetSearchHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	root := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "asset_search")
	_, err := assets.NewService(pool, root).Upload(t.Context(), owner.ID, assets.UploadInput{Title: "Garden 图 %_", Filename: "note.txt", Reader: strings.NewReader("isolated search evidence")})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"q=gArDeN&source=upload&kind=document&scanStatus=pending", "q=" + url.QueryEscape("图 %_")} {
		var page assets.AssetPage
		response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assets?"+query, nil, &page)
		if response.StatusCode != 200 || page.Total != 1 || len(page.Items) != 1 {
			t.Fatal(query, response.StatusCode, page)
		}
		if response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("private search cache header missing")
		}
	}
	for _, query := range []string{"q=a&q=b", "kind=image&kind=video", "scanStatus=bogus", "kind=bogus", "source=bogus", "q=" + url.QueryEscape(strings.Repeat("图", 121))} {
		response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assets?"+query, nil, nil)
		if response.StatusCode != 422 {
			t.Fatal(query, response.StatusCode)
		}
	}
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assets/saved-works?q=a&q=b", nil, nil)
	if response.StatusCode != 422 {
		t.Fatal("saved duplicate filters", response.StatusCode)
	}
}
