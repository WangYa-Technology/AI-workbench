package admin_test

import (
	"context"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/observability"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func TestOperationalDiagnosticsReportDurableSignals(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	recorder := observability.NewRepository(pool)
	if _, err := pool.Exec(ctx, `INSERT INTO request_observations(request_id,method,route,status,duration_ms,response_bytes,occurred_at) VALUES('expired-observation','GET','/api/v1/expired',200,1,1,now()-interval '8 days')`); err != nil {
		t.Fatal(err)
	}
	for _, item := range []httputil.RequestObservation{
		{RequestID: "diagnostics-ok", Method: httpMethodGet, Route: "/api/v1/works", Status: 200, Duration: 10 * time.Millisecond, ResponseBytes: 120},
		{RequestID: "diagnostics-failed", Method: httpMethodPost, Route: "/api/v1/generations", Status: 503, Duration: 90 * time.Millisecond, ResponseBytes: 180},
	} {
		if err := recorder.RecordRequest(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(kind,payload,status,attempts,max_attempts,available_at) VALUES('diagnostics.test','{}','queued',1,5,now()-interval '2 minutes')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO job_attempts(job_id,attempt_number,job_kind,worker_ref_hash,lease_token,status,lease_renewals,error_code,lease_expires_at,started_at,finished_at)
		SELECT id,1,kind,repeat('a',64),gen_random_uuid(),'retry_scheduled',2,'handler_failed',now(),now(),now()
		FROM jobs WHERE kind='diagnostics.test'`); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	diagnostics, err := service.GetOperationalDiagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.Requests.Total != 2 || diagnostics.Requests.ServerErrors != 1 || diagnostics.Requests.ByStatus["2xx"] != 1 || diagnostics.Requests.ByStatus["5xx"] != 1 {
		t.Fatalf("request diagnostics mismatch: %#v", diagnostics.Requests)
	}
	if diagnostics.Jobs.ByStatus["queued"] != 1 || diagnostics.Jobs.Retrying != 1 || diagnostics.Jobs.OldestQueuedAt == nil {
		t.Fatalf("job diagnostics mismatch: %#v", diagnostics.Jobs)
	}
	if diagnostics.Jobs.AttemptsLast24Hours != 1 || diagnostics.Jobs.ByAttemptStatus["retry_scheduled"] != 1 || diagnostics.Jobs.LeaseRenewalsLast24Hours != 2 || diagnostics.Jobs.LeaseExpirationsLast24Hours != 0 || diagnostics.Jobs.TerminalFailuresLast24Hours != 0 {
		t.Fatalf("job attempt diagnostics mismatch: %#v", diagnostics.Jobs)
	}
	var expiredObservations int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM request_observations WHERE request_id='expired-observation'`).Scan(&expiredObservations); err != nil || expiredObservations != 0 {
		t.Fatalf("expired request observations were not purged: count=%d err=%v", expiredObservations, err)
	}
}

const (
	httpMethodGet  = "GET"
	httpMethodPost = "POST"
)
