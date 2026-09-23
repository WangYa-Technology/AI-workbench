package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestWalletTopupSettingsHTTPAuthorityAndRevision(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	t.Cleanup(cleanup)
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	client := testHTTPClient(t)
	actor := registerGovernanceUser(t, client, server.URL, "topup_finance")
	path := server.URL + "/api/v1/admin/wallet-topup-settings"
	public := server.URL + "/api/v1/billing/topup-settings"
	input := admin.WalletTopupSettingsUpdate{ExpectedVersion: 1, MinimumAmountCents: 1000, PresetAmountsCents: []int{2000, 1000}}
	for _, endpoint := range []string{path, public} {
		if r := requestJSON(t, testHTTPClient(t), http.MethodGet, endpoint, nil, nil); r.StatusCode != 401 {
			t.Fatal("guest", endpoint, r.StatusCode)
		}
	}
	var settings billing.WalletTopupSettings
	if r := requestJSON(t, client, http.MethodGet, public, nil, &settings); r.StatusCode != 200 || settings.Version != 1 || settings.MinimumAmountCents != 50 || settings.Currency != "USD" {
		t.Fatalf("default=%+v status=%d", settings, r.StatusCode)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		if r := requestJSON(t, client, method, path, input, nil); r.StatusCode != 403 {
			t.Fatal("member", method, r.StatusCode)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE users SET role='creator' WHERE id=$1`, actor.ID)
	exec(`DELETE FROM role_permissions WHERE role='creator' AND permission_id LIKE 'admin:%'`)
	exec(`INSERT INTO role_permissions(role,permission_id) VALUES('creator','admin:providers')`)
	if r := requestJSON(t, client, http.MethodPut, path, input, nil); r.StatusCode != 403 {
		t.Fatal("provider-only", r.StatusCode)
	}
	exec(`DELETE FROM role_permissions WHERE role='creator' AND permission_id LIKE 'admin:%'`)
	exec(`INSERT INTO role_permissions(role,permission_id) VALUES('creator','admin:finance')`)
	if r := requestJSON(t, client, http.MethodPut, path, input, &settings); r.StatusCode != 200 || settings.Version != 2 || settings.MinimumAmountCents != 1000 || len(settings.PresetAmountsCents) != 2 || settings.PresetAmountsCents[0] != 1000 {
		t.Fatalf("saved=%+v status=%d", settings, r.StatusCode)
	}
	if r := requestJSON(t, client, http.MethodPut, path, input, nil); r.StatusCode != 409 {
		t.Fatal("stale version", r.StatusCode)
	}
	input.ExpectedVersion = 2
	input.PresetAmountsCents = nil
	if r := requestJSON(t, client, http.MethodPut, path, input, nil); r.StatusCode != 422 {
		t.Fatal("null presets", r.StatusCode)
	}
	if r := requestJSON(t, client, http.MethodGet, public, nil, &settings); r.StatusCode != 200 || settings.Version != 2 || settings.MinimumAmountCents != 1000 {
		t.Fatalf("readback=%+v status=%d", settings, r.StatusCode)
	}
	exec(`DELETE FROM role_permissions WHERE role='creator' AND permission_id='admin:finance'`)
	input.PresetAmountsCents = []int{}
	if r := requestJSON(t, client, http.MethodPut, path, input, nil); r.StatusCode != 403 {
		t.Fatal("revoked", r.StatusCode)
	}
	var audits int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='admin.wallet_topup_settings_updated' AND actor_id=$1`, actor.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
}
