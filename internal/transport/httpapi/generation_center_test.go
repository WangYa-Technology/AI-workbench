package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestGenerationCenterHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	ownerClient := testHTTPClient(t)
	owner := registerGovernanceUser(t, ownerClient, server.URL, "genctr_owner")
	otherClient := testHTTPClient(t)
	registerGovernanceUser(t, otherClient, server.URL, "genctr_other")
	ctx := context.Background()
	assetID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,family_id,version_number)
		VALUES($1,$2,'image','HTTP reusable output',$3,'image/jpeg','clean','demo','creator-owned',$1,1)`,
		assetID, owner.ID, "/api/v1/assets/"+assetID.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	newestID, olderID := uuid.New(), uuid.New()
	base := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,charged_cost_cents,output_asset_id,created_at,updated_at) VALUES
		($1,$2,'image','local_test','HTTP Generation Center','HTTP completed image','succeeded',100,5,5,$3,$4,$4),
		($5,$2,'image','local_test','HTTP Generation Center','HTTP failed image','failed',0,5,0,NULL,$6,$6)`,
		newestID, owner.ID, assetID, base.Add(time.Minute), olderID, base); err != nil {
		t.Fatal(err)
	}

	var first creation.GenerationPage
	response := requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/generations?mode=image&limit=1", nil, &first)
	if response.StatusCode != http.StatusOK || len(first.Items) != 1 || first.Items[0].ID != newestID || first.NextCursor == nil {
		t.Fatalf("first Generation Center page: status=%d page=%#v", response.StatusCode, first)
	}
	if !first.Items[0].Actions.CanDownload || !first.Items[0].Actions.CanReuse || first.Items[0].Actions.CanRetry {
		t.Fatalf("HTTP action projection mismatch: %#v", first.Items[0].Actions)
	}
	var second creation.GenerationPage
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/generations?mode=image&limit=1&cursor="+url.QueryEscape(*first.NextCursor), nil, &second)
	if response.StatusCode != http.StatusOK || len(second.Items) != 1 || second.Items[0].ID != olderID || second.NextCursor != nil || !second.Items[0].Actions.CanRetry {
		t.Fatalf("second Generation Center page: status=%d page=%#v", response.StatusCode, second)
	}

	var otherPage creation.GenerationPage
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/generations", nil, &otherPage)
	if response.StatusCode != http.StatusOK || len(otherPage.Items) != 0 {
		t.Fatalf("cross-account list leaked Generations: status=%d page=%#v", response.StatusCode, otherPage)
	}
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/generations/"+newestID.String(), nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-account detail did not fail closed: %d", response.StatusCode)
	}
	response = requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/generations", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous Generation Center access: %d", response.StatusCode)
	}

	invalidQueries := []string{
		"status=unknown", "limit=51", "limit=invalid", "cursor=modified",
		"dateFrom=2026-08-12T00%3A00%3A00Z&dateTo=2026-08-11T00%3A00%3A00Z", "dateFrom=not-a-date",
	}
	for _, query := range invalidQueries {
		response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/generations?"+query, nil, nil)
		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("invalid Generation Center query accepted: %s status=%d", query, response.StatusCode)
		}
	}
}
