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
	"github.com/hcai-chat/hcai-chat/internal/developer"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestDeveloperAccessCredentialLifecycleAndAPIIsolation(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	member := registerGovernanceUser(t, memberClient, server.URL, "developer_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "developer_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}

	var initial developer.Access
	response := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/account/developer-access", nil, &initial)
	if response.StatusCode != http.StatusOK || initial.Control.Enabled || initial.Control.Version != 1 {
		t.Fatalf("default-off access mismatch: status=%d item=%#v", response.StatusCode, initial)
	}
	response = requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/account/developer-service-accounts", map[string]any{"name": "Studio automation"}, nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled control accepted account creation: %d", response.StatusCode)
	}
	response = requestJSON(t, memberClient, http.MethodPut, server.URL+"/api/v1/admin/developer/control", map[string]any{"enabled": true, "maxServiceAccounts": 5, "maxActiveKeys": 3, "defaultTtlDays": 90, "expectedVersion": 1}, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member changed Developer Access control: %d", response.StatusCode)
	}
	var control developer.Control
	response = requestJSON(t, adminClient, http.MethodPut, server.URL+"/api/v1/admin/developer/control", map[string]any{"enabled": true, "maxServiceAccounts": 5, "maxActiveKeys": 3, "defaultTtlDays": 90, "expectedVersion": 1}, &control)
	if response.StatusCode != http.StatusOK || !control.Enabled || control.Version != 2 {
		t.Fatalf("Admin control update failed: status=%d item=%#v", response.StatusCode, control)
	}

	var account developer.ServiceAccount
	response = requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/account/developer-service-accounts", map[string]any{"name": "Studio automation"}, &account)
	if response.StatusCode != http.StatusCreated || account.OwnerID != member.ID || account.Version != 1 {
		t.Fatalf("Service Account creation failed: status=%d item=%#v", response.StatusCode, account)
	}
	var issued developer.Credential
	response = requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/account/developer-service-accounts/"+account.ID.String()+"/keys", map[string]any{"scopes": []string{"developer:identity:read"}, "ttlDays": 30}, &issued)
	if response.StatusCode != http.StatusCreated || !strings.HasPrefix(issued.PlaintextKey, "hcai_sk_") || issued.PublicPrefix == "" || issued.DisplayHint == "" {
		t.Fatalf("API key issuance failed: status=%d item=%#v", response.StatusCode, issued)
	}
	var persistedHash, persistedPrefix string
	if err := pool.QueryRow(context.Background(), `SELECT secret_hash,public_prefix FROM developer_api_keys WHERE id=$1`, issued.ID).Scan(&persistedHash, &persistedPrefix); err != nil {
		t.Fatal(err)
	}
	if persistedPrefix != issued.PublicPrefix || len(persistedHash) != 64 || strings.Contains(persistedHash, issued.PlaintextKey) {
		t.Fatalf("credential persistence leaked or omitted evidence: prefix=%q hash=%q", persistedPrefix, persistedHash)
	}

	principal := callDeveloperAPI(t, server.URL+"/api/v1/principal", issued.PlaintextKey)
	if principal.StatusCode != http.StatusOK || principal.Version != "v1" || principal.OwnerID != member.ID || principal.RequestID == "" {
		t.Fatalf("Developer API principal mismatch: %#v", principal)
	}
	internal := callDeveloperAPI(t, server.URL+"/api/v1/account/sessions", issued.PlaintextKey)
	if internal.StatusCode != http.StatusUnauthorized {
		t.Fatalf("API key crossed into Cookie product API: %#v", internal)
	}

	var rotated developer.Credential
	response = requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/account/developer-service-accounts/"+account.ID.String()+"/keys/"+issued.ID.String()+"/rotate", map[string]any{"scopes": []string{"developer:identity:read"}, "ttlDays": 60, "expectedVersion": issued.Version, "reason": "Rotate after bounded credential verification", "confirmed": true}, &rotated)
	if response.StatusCode != http.StatusCreated || rotated.ID == issued.ID || rotated.PlaintextKey == issued.PlaintextKey {
		t.Fatalf("API key rotation failed: status=%d item=%#v", response.StatusCode, rotated)
	}
	if old := callDeveloperAPI(t, server.URL+"/api/v1/principal", issued.PlaintextKey); old.StatusCode != http.StatusUnauthorized {
		t.Fatalf("rotated key remained active: %#v", old)
	}
	if current := callDeveloperAPI(t, server.URL+"/api/v1/principal", rotated.PlaintextKey); current.StatusCode != http.StatusOK {
		t.Fatalf("replacement key is unusable: %#v", current)
	}
	response = requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/account/developer-service-accounts/"+account.ID.String()+"/keys/"+rotated.ID.String()+"/revoke", map[string]any{"expectedVersion": rotated.Version, "reason": "Revoke completed contract-test credential", "confirmed": true}, nil)
	if response.StatusCode != http.StatusOK || callDeveloperAPI(t, server.URL+"/api/v1/principal", rotated.PlaintextKey).StatusCode != http.StatusUnauthorized {
		t.Fatalf("revocation did not invalidate replacement: %d", response.StatusCode)
	}
	var audits int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE actor_id=$1 AND action IN ('developer.service_account_created','developer.api_key_issued','developer.api_key_rotated','developer.api_key_revoked')`, member.ID).Scan(&audits); err != nil || audits != 4 {
		t.Fatalf("developer audit evidence mismatch: count=%d err=%v", audits, err)
	}

	var emergencyKey developer.Credential
	response = requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/account/developer-service-accounts/"+account.ID.String()+"/keys", map[string]any{"scopes": []string{"developer:identity:read"}, "ttlDays": 30}, &emergencyKey)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("emergency key setup failed: status=%d item=%#v", response.StatusCode, emergencyKey)
	}
	transition := map[string]any{"expectedVersion": emergencyKey.Version}
	response = requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/admin/developer/keys/"+emergencyKey.ID.String()+"/revoke", transition, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member invoked Admin key revocation: %d", response.StatusCode)
	}
	var revokedKey developer.APIKey
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/developer/keys/"+emergencyKey.ID.String()+"/revoke", transition, &revokedKey)
	if response.StatusCode != http.StatusOK || revokedKey.Status != "revoked" || callDeveloperAPI(t, server.URL+"/api/v1/principal", emergencyKey.PlaintextKey).StatusCode != http.StatusUnauthorized {
		t.Fatalf("Admin key revocation failed: status=%d item=%#v", response.StatusCode, revokedKey)
	}

	var emergencyAccount developer.ServiceAccount
	response = requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/account/developer-service-accounts", map[string]any{"name": "Emergency render agent"}, &emergencyAccount)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("emergency account setup failed: status=%d item=%#v", response.StatusCode, emergencyAccount)
	}
	var emergencyAccountKey developer.Credential
	response = requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/account/developer-service-accounts/"+emergencyAccount.ID.String()+"/keys", map[string]any{"scopes": []string{"developer:identity:read"}, "ttlDays": 30}, &emergencyAccountKey)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("emergency account key setup failed: status=%d item=%#v", response.StatusCode, emergencyAccountKey)
	}
	var inventory developer.Access
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/developer/access", nil, &inventory)
	encodedInventory, err := json.Marshal(inventory)
	if response.StatusCode != http.StatusOK || err != nil || strings.Contains(string(encodedInventory), emergencyAccountKey.PlaintextKey) || strings.Contains(string(encodedInventory), "secretHash") || strings.Contains(string(encodedInventory), "secret_hash") {
		t.Fatalf("Admin inventory exposed credential material: status=%d err=%v body=%s", response.StatusCode, err, encodedInventory)
	}
	var revokedAccount developer.ServiceAccount
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/developer/service-accounts/"+emergencyAccount.ID.String()+"/revoke", map[string]any{"expectedVersion": emergencyAccount.Version}, &revokedAccount)
	if response.StatusCode != http.StatusOK || revokedAccount.Status != "revoked" || callDeveloperAPI(t, server.URL+"/api/v1/principal", emergencyAccountKey.PlaintextKey).StatusCode != http.StatusUnauthorized {
		t.Fatalf("Admin account revocation failed: status=%d item=%#v", response.StatusCode, revokedAccount)
	}
}

type developerAPIResult struct {
	StatusCode int
	Version    string
	RequestID  string
	OwnerID    uuid.UUID
}

func callDeveloperAPI(t *testing.T, target, key string) developerAPIResult {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("X-Request-ID", "developer-contract-request")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result := developerAPIResult{StatusCode: response.StatusCode, Version: response.Header.Get("X-API-Version")}
	var body struct {
		Data struct {
			OwnerID uuid.UUID `json:"ownerId"`
		} `json:"data"`
		Meta struct {
			RequestID string `json:"requestId"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	result.RequestID, result.OwnerID = body.Meta.RequestID, body.Data.OwnerID
	return result
}
