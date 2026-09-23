package datarights_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/support"
)

func TestExportResourceFailuresAreAtomicAndRecoverable(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := cleanupUser(t, pool, "admin", "active")
	for _, kind := range []string{"package", "record", "storage"} {
		t.Run(kind, func(t *testing.T) {
			owner := cleanupUser(t, pool, "member", "active")
			request, original := exportRecoveryFixture(t, pool, owner)
			// Limit failures should stop immediately even with attempts still available.
			if _, err := pool.Exec(ctx, `UPDATE jobs SET max_attempts=5 WHERE id=$1`, original); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id)
  SELECT $1,'export.budget','user',$1,repeat('完整记录',1000),'budget-'||n FROM generate_series(1,10) n`, owner); err != nil {
				t.Fatal(err)
			}
			limits := config.DataExportConfig{}
			expected := error(datarights.ErrExportTooLarge)
			switch kind {
			case "package":
				limits.MaxBytes = 32 << 10
			case "record":
				limits.MaxRowBytes = 4096
				expected = datarights.ErrExportRowTooLarge
			case "storage":
				limits.TempDir = t.TempDir() + "/missing"
				expected = datarights.ErrExportStorage
				if _, err := pool.Exec(ctx, `UPDATE jobs SET max_attempts=1 WHERE id=$1`, original); err != nil {
					t.Fatal(err)
				}
			}
			restricted := datarights.NewServiceWithMedia(pool, t.TempDir(), nil, limits)
			repository := jobs.NewRepository(pool)
			job := claimDataRightsJobKind(t, ctx, pool, "budget-worker", datarights.ExportJobKind)
			if job.ID != original {
				t.Fatal(job.ID, original)
			}
			failure := restricted.HandleExportJob(ctx, job)
			if !errors.Is(failure, expected) {
				t.Fatal("unexpected resource failure", failure)
			}
			if err := repository.Fail(ctx, job, "budget-worker", failure); err != nil {
				t.Fatal(err)
			}
			var counts int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM data_rights_export_artifacts WHERE request_id=$1)+
  (SELECT count(*) FROM data_rights_export_parts WHERE request_id=$1)+
  (SELECT count(*) FROM notifications WHERE source_key=$2)`, request, "data-export-ready:"+request.String()).Scan(&counts); err != nil || counts != 0 {
				t.Fatal("partial result published", counts, err)
			}
			current, err := restricted.Get(ctx, owner, request)
			var coded interface{ ErrorCode() string }
			errors.As(expected, &coded)
			if err != nil || current.Status != "failed" || current.FailureCode == nil || *current.FailureCode != coded.ErrorCode() {
				t.Fatal(current, err)
			}
			healthy := datarights.NewService(pool, t.TempDir())
			retry, err := healthy.RetryExportJob(ctx, actor, original, exportRetryInput(), "capacity-restored")
			if err != nil {
				t.Fatal(err)
			}
			retried := claimDataRightsJobKind(t, ctx, pool, "budget-worker", datarights.ExportJobKind)
			if retried.ID != retry.ID {
				t.Fatal(retried.ID, retry.ID)
			}
			if err = healthy.HandleExportJob(ctx, retried); err != nil {
				t.Fatal(err)
			}
			if err = repository.Complete(ctx, retried, "budget-worker"); err != nil {
				t.Fatal(err)
			}
			if err := restricted.HandleExportJob(ctx, retried); err != nil {
				t.Fatal("ready replay depends on temporary capacity", err)
			}
			body, sum, err := healthy.Download(ctx, owner, request)
			if err != nil || !json.Valid(body) || !strings.Contains(string(body), "budget-10") {
				t.Fatal("incomplete recovered export", err)
			}
			if kind == "package" {
				if _, _, err = restricted.OpenExport(ctx, owner, request); !errors.Is(err, datarights.ErrExportTooLarge) {
					t.Fatal("download ignored budget", err)
				}
				if _, _, err = restricted.OpenExport(ctx, uuid.New(), request); !errors.Is(err, datarights.ErrNotReady) {
					t.Fatal("foreign export size leaked", err)
				}
			}
			after, afterSum, err := healthy.Download(ctx, owner, request)
			if err != nil || sum != afterSum || string(after) != string(body) {
				t.Fatal("budget failure rewrote ready artifact", err)
			}
			var state, code string
			if err = pool.QueryRow(ctx, `SELECT status,last_error_code FROM jobs WHERE id=$1`, original).Scan(&state, &code); err != nil || state != "failed" || code != coded.ErrorCode() {
				t.Fatal("original evidence changed", state, code, err)
			}
		})
	}
}

func TestExportStreamsLargeNestedHistoryWithoutChangingSchema(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := cleanupUser(t, pool, "member", "active")
	other := cleanupUser(t, pool, "member", "active")
	supportService := support.NewService(pool)
	input := support.CreateInput{Category: "general_support", Subject: "Complete nested export", Details: strings.Repeat("Evidence ", 10), Locale: "en-US"}
	own, err := supportService.Create(ctx, owner, input, "nested")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := supportService.Create(ctx, other, input, "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO support_messages(case_id,author_id,author_role,body,created_at)
 SELECT $1,$2,'requester',n::text||':'||repeat('完整记录',100),now()+make_interval(secs=>n) FROM generate_series(1,200) n`, own.ID, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO support_messages(case_id,author_id,author_role,body) VALUES($1,$2,'requester','FOREIGN_SECRET_NOT_EXPORTED')`, foreign.ID, other); err != nil {
		t.Fatal(err)
	}
	request, jobID := exportRecoveryFixture(t, pool, owner)
	service := datarights.NewServiceWithMedia(pool, t.TempDir(), nil, config.DataExportConfig{MaxRowBytes: 4096})
	payload, _ := json.Marshal(map[string]any{"requestId": request})
	if err = service.HandleExportJob(ctx, jobs.Job{ID: jobID, Payload: payload}); err != nil {
		t.Fatal("nested aggregate still exceeds row budget", err)
	}
	body, _, err := service.Download(ctx, owner, request)
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Data struct {
			SupportCases []struct {
				ID       uuid.UUID
				Messages []struct{ Body string }
				Events   []json.RawMessage
			} `json:"supportCases"`
		}
	}
	if err = json.Unmarshal(body, &pkg); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Data.SupportCases) != 1 {
		t.Fatal("wrong owner scope")
	}
	item := pkg.Data.SupportCases[0]
	if item.ID != own.ID || len(item.Messages) != 201 || !strings.HasPrefix(item.Messages[200].Body, "200:") || len(item.Events) != 1 || strings.Contains(string(body), "FOREIGN_SECRET") {
		t.Fatal("schema/order/privacy regression")
	}
	// A cancelled context must not read an already prepared export or leak slots.
	cancelled, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	if _, _, err = service.OpenExport(cancelled, owner, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestCancelledExportReplayDoesNotRequireTemporaryStorage(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := cleanupUser(t, pool, "member", "active")
	request, id := exportRecoveryFixture(t, pool, owner)
	service := datarights.NewServiceWithMedia(pool, t.TempDir(), nil, config.DataExportConfig{TempDir: t.TempDir() + "/missing"})
	if _, err := service.Cancel(ctx, owner, request, "cancel-before-processing"); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"requestId": request})
	if err := service.HandleExportJob(ctx, jobs.Job{ID: id, Payload: payload}); err != nil {
		t.Fatal("cancelled job replay touched unavailable storage", err)
	}
	current, err := service.Get(ctx, owner, request)
	if err != nil || current.Status != "cancelled" || current.Export != nil {
		t.Fatal(current, err)
	}
}
