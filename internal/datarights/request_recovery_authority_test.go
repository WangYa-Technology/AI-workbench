package datarights_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRequestRecoveryRejectsRevokedOperatorAtEnqueue(t *testing.T) {
	for _, kind := range []string{"export", "deletion"} {
		for _, change := range []string{"role", "suspended", "permission"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				pool, cleanup := dataRightsTestPool(t)
				t.Cleanup(cleanup)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				t.Cleanup(cancel)
				actor := cleanupUser(t, pool, "admin", "active")
				var original, request uuid.UUID
				jobKind := datarights.ExportJobKind
				if kind == "deletion" {
					_, _, id, job := failedDeletionFixture(t, pool, "prepare")
					request, original, jobKind = id, job.ID, datarights.DeletionJobKind
				} else {
					owner := cleanupUser(t, pool, "member", "active")
					request, original = exportRecoveryFixture(t, pool, owner)
					if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=1 WHERE id=$1`, original); err != nil {
						t.Fatal(err)
					}
				}
				var before string
				if err := pool.QueryRow(ctx, `SELECT status FROM data_rights_requests WHERE id=$1`, request).Scan(&before); err != nil {
					t.Fatal(err)
				}
				// A server's stronger default isolation must not make the enqueue
				// authorization reuse the operator's old permission snapshot.
				config := pool.Config()
				config.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
				recoveryPool, err := pgxpool.NewWithConfig(ctx, config)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(recoveryPool.Close)
				traced, entered, release := testutil.GateQuery(t, recoveryPool, "INSERT INTO jobs(kind,payload,max_attempts)")
				service := datarights.NewService(traced, t.TempDir())
				retry := service.RetryExportJob
				if kind == "deletion" {
					retry = service.RetryDeletionJob
				}
				done := make(chan error, 1)
				go func() {
					_, err := retry(ctx, actor, original, exportRetryInput(), "revoked-request-recovery")
					done <- err
				}()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("request recovery did not reach enqueue", ctx.Err())
				}
				switch change {
				case "role":
					_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
				case "suspended":
					_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor)
				case "permission":
					_, err = pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:data-rights'`)
				}
				if err != nil {
					t.Fatal(err)
				}
				release()
				if err := <-done; !errors.Is(err, datarights.ErrCleanupForbidden) {
					t.Fatalf("revoked operator recovered request: %v", err)
				}
				var status, after string
				var attempts, queued, evidence, audits int
				if err := pool.QueryRow(ctx, `SELECT status,attempts,
 (SELECT count(*) FROM jobs WHERE kind=$2 AND status='queued'),
 (SELECT count(*) FROM data_export_recoveries)+(SELECT count(*) FROM account_deletion_recoveries),
 (SELECT count(*) FROM audit_events WHERE action IN ('data_rights.export_job_retried','data_rights.deletion_job_retried')),
 (SELECT status FROM data_rights_requests WHERE id=$3) FROM jobs WHERE id=$1`, original, jobKind, request).Scan(&status, &attempts, &queued, &evidence, &audits, &after); err != nil || status != "failed" || attempts != 1 || queued != 0 || evidence != 0 || audits != 0 || after != before {
					t.Fatalf("revoked recovery mutated request: %s %d %d %d %d %s/%s %v", status, attempts, queued, evidence, audits, before, after, err)
				}
			})
		}
	}
}
