package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GenerationJob exercises real claims/attempt evidence in isolated tests. A
// caller replaying an existing attempt receives its original token; this helper
// must not mint a replacement lease for an expired or terminal execution.
func GenerationJob(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) jobs.Job {
	t.Helper()
	ctx := context.Background()
	var job jobs.Job
	var status string
	if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload,j.attempts,j.max_attempts,COALESCE(j.lease_token,'00000000-0000-0000-0000-000000000000'::uuid),j.status
 FROM generation_executions e JOIN jobs j ON j.id=e.job_id WHERE e.generation_id=$1`, id).Scan(&job.ID, &job.Kind, &job.Payload, &job.Attempts, &job.MaxAttempts, &job.LeaseToken, &status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		return job
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now()-interval '100 years' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := jobs.NewRepository(pool).Claim(ctx, "generation-test", 10*time.Minute)
	if err != nil || claimed.ID != job.ID {
		t.Fatalf("claim generation job: %v (got %s want %s)", err, claimed.ID, job.ID)
	}
	return claimed
}

func FinalGenerationAttempt(t *testing.T, pool *pgxpool.Pool, job jobs.Job) jobs.Job {
	t.Helper()
	job.MaxAttempts = job.Attempts
	if _, err := pool.Exec(context.Background(), `UPDATE jobs SET max_attempts=attempts WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	return job
}
