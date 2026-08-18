package observability

import (
	"context"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func TestMetricsRecordsStatusClassesWithoutHighCardinalityLabels(t *testing.T) {
	metrics := NewMetrics(time.Now().Add(-2 * time.Second))
	if err := metrics.RecordRequest(context.Background(), httputil.RequestObservation{Status: 201, Duration: 20 * time.Millisecond, ResponseBytes: 12}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.RecordRequest(context.Background(), httputil.RequestObservation{Status: 503, Duration: 40 * time.Millisecond, ResponseBytes: 8}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.RecordRequest(context.Background(), httputil.RequestObservation{Status: 700}); err != nil {
		t.Fatal(err)
	}
	if got := metrics.totalRequests(); got != 3 {
		t.Fatalf("unexpected request count: %d", got)
	}
	if metrics.requests[2].Load() != 1 || metrics.requests[5].Load() != 1 || metrics.requests[0].Load() != 1 {
		t.Fatalf("unexpected status buckets: 2xx=%d 5xx=%d unknown=%d", metrics.requests[2].Load(), metrics.requests[5].Load(), metrics.requests[0].Load())
	}
	if metrics.responseBytes.Load() != 20 || metrics.durationNanos.Load() != uint64(60*time.Millisecond) {
		t.Fatalf("unexpected process counters: bytes=%d duration=%d", metrics.responseBytes.Load(), metrics.durationNanos.Load())
	}
}
