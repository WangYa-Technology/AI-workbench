package httputil_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func TestMiddlewareEmitsStructuredRouteStatusAndObservation(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuffer, nil))
	var observed httputil.RequestObservation
	router := chi.NewRouter()
	router.Use(httputil.Middleware(logger, "http://localhost:5173", func(_ context.Context, item httputil.RequestObservation) error {
		observed = item
		return nil
	}))
	router.Post("/api/v1/things/{thingID}", func(w http.ResponseWriter, _ *http.Request) {
		httputil.JSON(w, http.StatusCreated, map[string]string{"status": "created"})
	})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/things/"+uuid.NewString(), nil)
	request.Header.Set("X-Request-ID", "operations-request-01")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("X-Request-ID") != "operations-request-01" {
		t.Fatalf("response evidence mismatch: status=%d request=%q", response.Code, response.Header().Get("X-Request-ID"))
	}
	if observed.RequestID != "operations-request-01" || observed.Route != "/api/v1/things/{thingID}" || observed.Status != http.StatusCreated || observed.ResponseBytes == 0 {
		t.Fatalf("observation mismatch: %#v", observed)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logBuffer.Bytes()), &record); err != nil {
		t.Fatal(err)
	}
	if record["request_id"] != "operations-request-01" || record["route"] != "/api/v1/things/{thingID}" || record["status"] != float64(http.StatusCreated) || record["response_bytes"] == nil || record["duration_ms"] == nil {
		t.Fatalf("structured log evidence mismatch: %#v", record)
	}
}

func TestMiddlewareReplacesUnsafeRequestID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	router := chi.NewRouter()
	router.Use(httputil.Middleware(logger, "http://localhost:5173"))
	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("X-Request-ID", string(bytes.Repeat([]byte("a"), 129)))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if _, err := uuid.Parse(response.Header().Get("X-Request-ID")); err != nil {
		t.Fatalf("unsafe request ID was not replaced: %q", response.Header().Get("X-Request-ID"))
	}
}
