package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestExportDownloadBudgetResponsesAndOwnership(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	root := t.TempDir()
	cfg := config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}
	server := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "export_budget")
	var request datarights.Request
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/account/data-rights", map[string]any{"requestType": "data_export", "identityConfirmation": owner.Handle}, &request)
	if response.StatusCode != http.StatusCreated {
		t.Fatal(response.StatusCode)
	}
	ctx := context.Background()
	job := claimHTTPJobKind(t, ctx, pool, "budget-http", datarights.ExportJobKind)
	if err := datarights.NewService(pool, root).HandleExportJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		limits config.DataExportConfig
		code   string
		retry  string
	}{
		{"package", config.DataExportConfig{MaxBytes: 1024}, "data_export_too_large", ""},
		{"storage", config.DataExportConfig{TempDir: root + "/missing"}, "data_export_storage_unavailable", "30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limited := cfg
			limited.DataExport = tc.limits
			small := httptest.NewServer(httpapi.New(limited, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
			defer small.Close()
			res, err := client.Get(small.URL + "/api/v1/account/data-rights/" + request.ID.String() + "/export")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusServiceUnavailable || !json.Valid(body) || !strings.Contains(string(body), tc.code) || res.Header.Get("Retry-After") != tc.retry || res.Header.Get("Cache-Control") != "private, no-store" {
				t.Fatal(res.StatusCode, string(body), res.Header)
			}
			if res.Header.Get("Digest") != "" || res.Header.Get("Content-Disposition") != "" || strings.Contains(string(body), owner.Email) || strings.Contains(string(body), root) {
				t.Fatal("private or partial export leaked")
			}
			if tc.name == "package" {
				res, err = client.Get(small.URL + "/api/v1/account/data-rights/" + uuid.NewString() + "/export")
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != http.StatusNotFound {
					t.Fatal("size check precedes ownership", res.StatusCode)
				}
			}
		})
	}
	res, err := client.Get(server.URL + "/api/v1/account/data-rights/" + request.ID.String() + "/export")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK || !json.Valid(body) || res.Header.Get("Digest") == "" {
		t.Fatal("budget errors damaged ready export", err, res.StatusCode)
	}
}
