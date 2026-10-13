package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
	"github.com/hcai-chat/hcai-chat/internal/tasktypes"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestTaskDeliveryKindHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "task_kind_admin")
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='admin' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	base := server.URL + "/api/v1"
	body := map[string]any{"code": "http_brand_image", "nameZh": "品牌图片", "nameEn": "Brand image", "icon": "mixed", "sortOrder": 1, "deliveryKind": "image"}
	var category tasktypes.Type
	if response := requestJSON(t, client, http.MethodPost, base+"/admin/task-types?scope=task", body, &category); response.StatusCode != 201 || category.DeliveryKind != "image" {
		t.Fatal(response.StatusCode, category)
	}
	input := tasks.CreateInput{Title: "Custom image production brief", Summary: "An original image with source evidence.", Brief: "Create a coherent campaign image and disclose all sources and models used.", DeliverableType: category.Code, Deliverables: []string{"Original image"}, AcceptanceRules: []string{"Clean and documented"}, RightsTerms: "Campaign use for one year.", AIDisclosureRequirement: "Disclose every source and model.", BudgetCents: 500, Currency: "USD", Deadline: time.Now().Add(24 * time.Hour), ClientTimezone: "UTC"}
	// Use the public typed HTTP boundary rather than calling the task service.
	var created tasks.Detail
	response := requestJSON(t, client, http.MethodPost, base+"/tasks", input, &created)
	// Task commands require a key; prove the boundary rejects an unkeyed write.
	if response.StatusCode != 422 {
		t.Fatal("unkeyed task accepted", response.StatusCode)
	}
	// Existing request helper has no custom headers, so issue this one explicitly.
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/tasks", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "http-delivery-kind-create")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 201 {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	if err = json.NewDecoder(res.Body).Decode(&created); err != nil || created.DeliveryKind != "image" {
		t.Fatal(created.DeliveryKind, err)
	}
	body["deliveryKind"] = "video"
	if response = requestJSON(t, client, http.MethodPatch, base+"/admin/task-types/"+category.Code+"?scope=task", body, &category); response.StatusCode != 200 || category.DeliveryKind != "video" {
		t.Fatal(response.StatusCode, category)
	}
	var detail tasks.Detail
	if response = requestJSON(t, client, http.MethodGet, base+"/tasks/"+created.ID.String(), nil, &detail); response.StatusCode != 200 || detail.DeliveryKind != "image" {
		t.Fatal(response.StatusCode, detail.DeliveryKind)
	}
	if response = requestJSON(t, client, http.MethodGet, base+"/assets?purpose=task_delivery&limit=20", nil, nil); response.StatusCode != 200 {
		t.Fatal("task assets filter rejected", response.StatusCode)
	}
	body["deliveryKind"] = "unknown"
	if response = requestJSON(t, client, http.MethodPatch, base+"/admin/task-types/"+category.Code+"?scope=task", body, nil); response.StatusCode != 422 {
		t.Fatal("invalid delivery kind accepted", response.StatusCode)
	}
}
