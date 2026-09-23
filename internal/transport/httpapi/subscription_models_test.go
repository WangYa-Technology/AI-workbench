package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestSubscriptionModelCatalogFinanceAuthority(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	t.Cleanup(cleanup)
	ctx := t.Context()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	client := testHTTPClient(t)
	actor := registerGovernanceUser(t, client, server.URL, "finance_models")
	path := server.URL + "/api/v1/admin/subscription-models"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("guest and member cannot read", func(t *testing.T) {
		if r := requestJSON(t, testHTTPClient(t), http.MethodGet, path, nil, nil); r.StatusCode != 401 {
			t.Fatal("guest catalog status", r.StatusCode)
		}
		if r := requestJSON(t, client, http.MethodGet, path, nil, nil); r.StatusCode != 403 {
			t.Fatal("member catalog status", r.StatusCode)
		}
	})
	exec(`UPDATE users SET role='creator' WHERE id=$1`, actor.ID)
	exec(`DELETE FROM role_permissions WHERE role='creator' AND permission_id LIKE 'admin:%'`)
	exec(`INSERT INTO role_permissions(role,permission_id) VALUES('creator','admin:providers')`)
	t.Run("provider permission alone cannot read finance catalog", func(t *testing.T) {
		if r := requestJSON(t, client, http.MethodGet, path, nil, nil); r.StatusCode != 403 {
			t.Fatal("provider-only catalog status", r.StatusCode)
		}
	})
	exec(`DELETE FROM role_permissions WHERE role='creator' AND permission_id LIKE 'admin:%'`)
	exec(`INSERT INTO role_permissions(role,permission_id) VALUES('creator','admin:finance')`)
	providerID, modelID, disabledID, archivedID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO provider_configs(id,name,protocol,endpoint,runtime_provider,admin_enabled,created_by,updated_by)
	 VALUES($1,'Finance model labels','openai_responses','https://private-provider.invalid/v1','openai',false,$2,$2)`, providerID, actor.ID)
	exec(`INSERT INTO provider_config_models(id,provider_id,mode,model_name,display_name,description,estimated_cost_cents,admin_enabled,created_by,updated_by) VALUES
	 ($3,$1,'chat','private-chat','Chat label','Private description',123,true,$2,$2),
	 ($4,$1,'image','private-image','Image label','Private description',456,false,$2,$2),
	 ($5,$1,'video','private-video','Archived label','Private description',789,false,$2,$2)`, providerID, actor.ID, modelID, disabledID, archivedID)
	exec(`UPDATE provider_config_models SET archived_at=now() WHERE id=$1`, archivedID)
	t.Run("finance sees only model labels and cannot read provider configuration", func(t *testing.T) {
		var result struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		if r := requestJSON(t, client, http.MethodGet, path, nil, &result); r.StatusCode != 200 {
			t.Fatal("finance catalog status", r.StatusCode)
		}
		seen := map[string]bool{}
		for _, item := range result.Items {
			if len(item) != 4 || item["id"] == nil || item["displayName"] == nil || item["mode"] == nil || item["providerName"] == nil {
				t.Fatalf("catalog leaked configuration or missed labels: %v", item)
			}
			var id string
			if err := json.Unmarshal(item["id"], &id); err != nil {
				t.Fatal(err)
			}
			seen[id] = true
		}
		if !seen[modelID.String()] || !seen[disabledID.String()] || seen[archivedID.String()] {
			t.Fatalf("incorrect eligible models: %v", seen)
		}
		if r := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/admin/provider-configs", nil, nil); r.StatusCode != 403 {
			t.Fatal("finance read provider configuration", r.StatusCode)
		}
	})
	t.Run("finance can create and update a plan using catalog models", func(t *testing.T) {
		input := billing.SubscriptionPlanInput{TierCode: "finance_catalog", Name: "Finance catalog", Description: "Finance only plan configuration", PriceCents: 1900, Currency: "USD", IncludedPoints: 10000, BillingPeriodDays: 30, Active: true, ModelIDs: []uuid.UUID{modelID}}
		var plan billing.SubscriptionPlan
		if r := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/admin/subscription-plans", input, &plan); r.StatusCode != 201 || len(plan.ModelIDs) != 1 || plan.ModelIDs[0] != modelID {
			t.Fatalf("create plan: status=%d plan=%+v", r.StatusCode, plan)
		}
		models := []uuid.UUID{disabledID}
		originalVersion := plan.Version
		updatePath := server.URL + "/api/v1/admin/subscription-plans/" + plan.ID.String()
		if r := requestJSON(t, client, http.MethodPatch, updatePath, billing.SubscriptionPlanUpdate{ModelIDs: &models}, nil); r.StatusCode != 422 {
			t.Fatal("missing version accepted", r.StatusCode)
		}
		if r := requestJSON(t, client, http.MethodPatch, updatePath, billing.SubscriptionPlanUpdate{ExpectedVersion: originalVersion, ModelIDs: &models}, &plan); r.StatusCode != 200 || len(plan.ModelIDs) != 1 || plan.ModelIDs[0] != disabledID || plan.Version != originalVersion+1 {
			t.Fatalf("update plan: status=%d plan=%+v", r.StatusCode, plan)
		}
		var conflict struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if r := requestJSON(t, client, http.MethodPatch, updatePath, billing.SubscriptionPlanUpdate{ExpectedVersion: originalVersion, ModelIDs: &models}, &conflict); r.StatusCode != 409 || conflict.Error.Code != "admin_state_conflict" {
			t.Fatalf("stale update: status=%d error=%+v", r.StatusCode, conflict)
		}
		var auditCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action IN ('billing.plan_created','billing.plan_updated')`, plan.ID).Scan(&auditCount); err != nil || auditCount != 2 {
			t.Fatalf("unexpected plan audit count: %d err=%v", auditCount, err)
		}
	})
	t.Run("archived provider and empty catalog", func(t *testing.T) {
		exec(`UPDATE provider_configs SET archived_at=now(),admin_enabled=false`)
		var result struct {
			Items []billing.SubscriptionModel `json:"items"`
		}
		if r := requestJSON(t, client, http.MethodGet, path, nil, &result); r.StatusCode != 200 || result.Items == nil || len(result.Items) != 0 {
			t.Fatalf("empty catalog: status=%d items=%+v", r.StatusCode, result.Items)
		}
	})
	t.Run("revocation applies to existing login", func(t *testing.T) {
		exec(`DELETE FROM role_permissions WHERE role='creator' AND permission_id='admin:finance'`)
		if r := requestJSON(t, client, http.MethodGet, path, nil, nil); r.StatusCode != 403 {
			t.Fatal("revoked catalog status", r.StatusCode)
		}
	})
}
