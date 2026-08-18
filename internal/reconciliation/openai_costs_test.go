package reconciliation_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/reconciliation"
)

func TestOpenAICostsRuntimeFetchesAggregateCostWithoutLeakingConfiguration(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodGet || request.URL.Path != "/v1/organization/costs" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer admin-secret" || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing organization-cost authorization contract")
		}
		query := request.URL.Query()
		if query.Get("start_time") != "1735689600" || query.Get("end_time") != "1735862400" || query.Get("bucket_width") != "1d" || query.Get("limit") != "180" || query.Get("project_ids") != "proj_private" {
			t.Errorf("unexpected cost query: %s", query.Encode())
		}
		groups := query["group_by"]
		if len(groups) != 2 || groups[0] != "project_id" || groups[1] != "line_item" {
			t.Errorf("unexpected group_by evidence: %#v", groups)
		}
		if requests == 1 {
			if query.Get("page") != "" {
				t.Errorf("first page unexpectedly supplied cursor")
			}
			_, _ = writer.Write([]byte(`{"data":[{"results":[{"amount":{"value":0.010001,"currency":"usd"}}]}],"has_more":true,"next_page":"page-private"}`))
			return
		}
		if query.Get("page") != "page-private" {
			t.Errorf("second page cursor missing: %q", query.Get("page"))
		}
		_, _ = writer.Write([]byte(`{"data":[{"results":[{"amount":{"value":0.025,"currency":"USD"}}]}],"has_more":false,"next_page":""}`))
	}))
	defer server.Close()

	runtime, err := reconciliation.NewOpenAICostsRuntime(reconciliation.OpenAICostsConfig{
		AdminAPIKey: "admin-secret", BaseURL: server.URL + "/v1", ProjectID: "proj_private",
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(48 * time.Hour)
	summary, err := runtime.Fetch(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || summary.Provider != "openai" || summary.Currency != "USD" || summary.CostMicros != 35_001 || !summary.PeriodStart.Equal(start) || !summary.PeriodEnd.Equal(end) {
		t.Fatalf("unexpected aggregate cost summary: %#v requests=%d", summary, requests)
	}
	if strings.Contains(summary.String(), "proj_private") || strings.Contains(summary.String(), "admin-secret") {
		t.Fatalf("summary leaked private configuration: %q", summary.String())
	}
}

func TestOpenAICostsRuntimeFailsClosedForInvalidBoundsAndResponses(t *testing.T) {
	runtime, err := reconciliation.NewOpenAICostsRuntime(reconciliation.OpenAICostsConfig{
		AdminAPIKey: "admin-secret", BaseURL: "https://api.openai.com/v1", ProjectID: "proj_private",
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := runtime.Fetch(context.Background(), start.Add(time.Hour), start.Add(24*time.Hour)); err == nil || err.Error() != "provider_invalid_request" {
		t.Fatalf("partial-day cost query did not fail closed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"data":[{"results":[{"amount":{"value":0.1,"currency":"usd"}}]}],"has_more":true,"next_page":""}`))
	}))
	defer server.Close()
	runtime, err = reconciliation.NewOpenAICostsRuntime(reconciliation.OpenAICostsConfig{
		AdminAPIKey: "admin-secret", BaseURL: server.URL, ProjectID: "proj_private",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Fetch(context.Background(), start, start.Add(24*time.Hour)); err == nil || err.Error() != "provider_response_invalid" {
		t.Fatalf("malformed pagination did not fail closed: %v", err)
	}
}

func TestOpenAICostsRuntimeSanitizesProviderFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"error":{"message":"private organization error"}}`))
	}))
	defer server.Close()
	runtime, err := reconciliation.NewOpenAICostsRuntime(reconciliation.OpenAICostsConfig{
		AdminAPIKey: "admin-secret", BaseURL: server.URL, ProjectID: "proj_private",
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	_, err = runtime.Fetch(context.Background(), start, start.Add(24*time.Hour))
	if err == nil || err.Error() != "provider_authentication" || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe Provider failure: %v", err)
	}
	var coded interface{ ErrorCode() string }
	if !errors.As(err, &coded) || coded.ErrorCode() != "provider_authentication" {
		t.Fatalf("missing stable Provider failure classification: %v", err)
	}
}

func TestOpenAICostsRuntimeRejectsInvalidConfiguration(t *testing.T) {
	for _, config := range []reconciliation.OpenAICostsConfig{
		{AdminAPIKey: "", BaseURL: "https://api.openai.com/v1", ProjectID: "proj_private"},
		{AdminAPIKey: "admin", BaseURL: "https://api.openai.com/v1?private=true", ProjectID: "proj_private"},
		{AdminAPIKey: "admin", BaseURL: "http://provider.example/v1", ProjectID: "proj_private"},
		{AdminAPIKey: "admin", BaseURL: "ftp://provider.example/v1", ProjectID: "proj_private"},
		{AdminAPIKey: "admin", BaseURL: "https://api.openai.com/v1", ProjectID: ""},
		{AdminAPIKey: "admin\nsecret", BaseURL: "https://api.openai.com/v1", ProjectID: "proj_private"},
	} {
		if _, err := reconciliation.NewOpenAICostsRuntime(config); err == nil {
			t.Fatalf("invalid cost configuration was accepted: %#v", config)
		}
	}
}

func TestCostQueryUsesOnlySafeProjectParameter(t *testing.T) {
	query := url.Values{"project_ids": {"proj_private"}}
	if strings.Contains(query.Encode(), "admin") {
		t.Fatal("query must never contain an administrative credential")
	}
}
