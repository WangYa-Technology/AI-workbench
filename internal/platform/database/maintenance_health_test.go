package database_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/observability"
)

func TestMaintenanceHealthPersistentConcurrentObservations(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := t.Context()
	repository := observability.NewRepository(pool)
	kind := observability.ProductRefundReconciliation
	readMetric := func(name string) float64 {
		t.Helper()
		body, err := observability.NewMetrics(time.Now()).Render(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(body, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == name+`{kind="`+kind+`"}` {
				value, err := strconv.ParseFloat(fields[1], 64)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
		}
		t.Fatal("missing series", name)
		return 0
	}
	if readMetric("hcai_maintenance_success_seen") != 0 {
		t.Fatal("fabricated initial success")
	}
	if err := repository.RecordMaintenance(ctx, kind, false); err != nil {
		t.Fatal(err)
	}
	if readMetric("hcai_maintenance_last_pass_failed") != 1 || readMetric("hcai_maintenance_success_seen") != 0 {
		t.Fatal("initial failure hidden")
	}
	if err := repository.RecordMaintenance(ctx, kind, true); err != nil {
		t.Fatal(err)
	}
	if readMetric("hcai_maintenance_last_pass_failed") != 0 || readMetric("hcai_maintenance_success_seen") != 1 {
		t.Fatal("success did not clear current failure")
	}
	if _, err := pool.Exec(ctx, `UPDATE maintenance_health SET completed_at=now()-interval '2 hours',last_success_at=now()-interval '2 hours',last_failure_at=now()-interval '3 hours' WHERE kind=$1`, kind); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordMaintenance(ctx, kind, false); err != nil {
		t.Fatal(err)
	}
	if readMetric("hcai_maintenance_last_success_age_seconds") < 7200 {
		t.Fatal("failure reset success clock")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- observability.NewRepository(pool).RecordMaintenance(ctx, kind, i%2 == 0)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if readMetric("hcai_maintenance_passes_total") != 23 || readMetric("hcai_maintenance_failures_total") != 12 {
		t.Fatal("concurrent observations lost")
	}
	if err := repository.RecordMaintenance(ctx, kind, true); err != nil {
		t.Fatal(err)
	}
	if readMetric("hcai_maintenance_last_success_age_seconds") > 10 {
		t.Fatal("successful resumed scan still stale")
	}
	if err := repository.RecordMaintenance(ctx, "PRIVATE-ARBITRARY-KIND", true); err == nil {
		t.Fatal("unbounded labels accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := repository.RecordMaintenance(cancelled, kind, true); err == nil {
		t.Fatal("cancelled record succeeded")
	}
	if readMetric("hcai_maintenance_passes_total") != 24 {
		t.Fatal("rejected write changed counters")
	}
	// Simulate a database clock correction. A later writer must not regress the
	// stored timestamps or turn the future observation into a healthy zero age.
	if _, err := pool.Exec(ctx, `UPDATE maintenance_health SET completed_at=now()+interval '1 hour',last_success_at=now()+interval '1 hour' WHERE kind=$1`, kind); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordMaintenance(ctx, kind, false); err != nil {
		t.Fatal(err)
	}
	if readMetric("hcai_maintenance_invalid_timestamps") != 1 {
		t.Fatal("future history silently normalized")
	}
}

func TestMaintenanceHealthMigrationRoundTrip(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := t.Context()
	repository := observability.NewRepository(pool)
	for _, kind := range []string{observability.LegalHoldExpiry, observability.LegalHoldCleanup, observability.ProductCleanupReconciliation, observability.AccountDeletionReconciliation, observability.OriginalMediaCleanupReconciliation, observability.ProductRefundReconciliation} {
		if err := repository.RecordMaintenance(ctx, kind, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO maintenance_health(kind,passes,failures,last_failed,completed_at,last_success_at) VALUES('unknown',1,0,false,now(),now())`); err == nil {
		t.Fatal("database accepted unknown kind")
	}
	for _, direction := range []string{"down", "up"} {
		sql, err := os.ReadFile("migrations/0109_maintenance_health." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM maintenance_health`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := repository.RecordMaintenance(ctx, observability.LegalHoldExpiry, true); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceHealthDelayedSuccessCannotHideNewFailure(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	repository := observability.NewRepository(pool)
	kind := observability.LegalHoldExpiry
	if err := repository.RecordMaintenance(ctx, kind, true); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `SELECT 1 FROM maintenance_health WHERE kind=$1 FOR UPDATE`, kind); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- repository.RecordMaintenance(ctx, kind, true) }()
	// Observe the actual blocked writer rather than assume a sleep establishes
	// ordering. The relation OID scopes the check to this isolated schema.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid
 WHERE a.wait_event_type='Lock' AND l.relation='maintenance_health'::regclass AND a.pid<>pg_backend_pid())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("writer never reached row lock")
		case <-ticker.C:
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE maintenance_health SET passes=passes+1,failures=failures+1,last_failed=true,completed_at=statement_timestamp(),last_failure_at=statement_timestamp() WHERE kind=$1`, kind); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var failed, ordered bool
	var passes, failures int
	if err := pool.QueryRow(ctx, `SELECT last_failed,passes,failures,last_success_at<last_failure_at AND last_failure_at=completed_at FROM maintenance_health WHERE kind=$1`, kind).Scan(&failed, &passes, &failures, &ordered); err != nil || !failed || passes != 3 || failures != 1 || !ordered {
		t.Fatal("stale success hid newer failure", failed, passes, failures, ordered, err)
	}
}
