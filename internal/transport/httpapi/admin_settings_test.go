package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminSystemSettingsHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	_ = registerGovernanceUser(t, memberClient, server.URL, "settings_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "settings_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	if response := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/settings", nil, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed settings: %d", response.StatusCode)
	}
	siteConfiguration := admin.SiteConfiguration{
		SiteName: "Configured HCAI", ServerURL: "https://api.example.test", SiteIconURL: "https://cdn.example.test/icon.png",
		FooterText: admin.LocalizedSiteText{EnUS: "Configured footer", ZhCN: "已配置页脚"},
		Policies:   admin.SitePolicyContent{Terms: admin.LocalizedSiteText{EnUS: "Configured terms", ZhCN: "已配置条款"}},
	}
	response := requestJSON(t, adminClient, http.MethodPut, server.URL+"/api/v1/admin/site-config", siteConfiguration, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("site configuration update status: %d", response.StatusCode)
	}
	input := map[string]any{"registrationsEnabled": false, "generationsEnabled": true, "publishingEnabled": true, "marketplaceCheckoutEnabled": true, "taskCreationEnabled": true, "publicNotice": "New registration is paused for a bounded Local Test maintenance window."}
	var updated admin.SystemSettings
	response = requestJSON(t, adminClient, http.MethodPut, server.URL+"/api/v1/admin/settings", input, &updated)
	if response.StatusCode != http.StatusOK || updated.RegistrationsEnabled || updated.SiteConfiguration.SiteName != "Configured HCAI" {
		t.Fatalf("settings contract mismatch: %d %#v", response.StatusCode, updated)
	}
	var publicConfiguration admin.SiteConfiguration
	response = requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/site-config", nil, &publicConfiguration)
	if response.StatusCode != http.StatusOK || publicConfiguration.SiteName != "Configured HCAI" || publicConfiguration.Policies.Terms.ZhCN != "已配置条款" {
		t.Fatalf("public site configuration mismatch: %d %#v", response.StatusCode, publicConfiguration)
	}
	newClient := testHTTPClient(t)
	response = requestJSON(t, newClient, http.MethodPost, server.URL+"/api/v1/auth/register", map[string]any{"email": "paused@example.test", "password": "strong-password-123", "handle": "paused_user", "displayName": "Paused User", "locale": "en-US", "timezone": "UTC"}, nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled registration status: %d", response.StatusCode)
	}
}
