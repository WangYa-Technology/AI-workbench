package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestMetricsEndpointExposesSafeOperationalSeries(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO jobs(kind,status,created_at,available_at)
		VALUES('identity.email_action.expire','queued',now()-interval '2 days',now()+interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	response, err := http.Get(server.URL + "/api/v1")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response, err = http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics response mismatch: status=%d contentType=%q body=%s", response.StatusCode, response.Header.Get("Content-Type"), text)
	}
	for _, required := range []string{
		"# TYPE hcai_database_ready gauge",
		"hcai_database_ready 1",
		"# TYPE hcai_http_requests_total counter",
		"hcai_http_requests_total{status_class=\"2xx\"}",
		"# TYPE hcai_jobs_total gauge",
		"hcai_jobs_total{status=\"queued\"} 1",
		"# HELP hcai_jobs_oldest_queued_age_seconds Age of the oldest runnable queued job.",
		"hcai_jobs_oldest_queued_age_seconds 0.000000",
		"# TYPE hcai_job_attempts_total gauge",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("metrics response omitted %q: %s", required, text)
		}
	}
	for _, forbidden := range []string{"request_id", "prompt", "email", "user_id", "provider_api_key"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("metrics response exposed forbidden field %q: %s", forbidden, text)
		}
	}
}
