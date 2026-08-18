package reconciliation_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/reconciliation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type testCostReader struct{ summary reconciliation.CostSummary }

func (r testCostReader) Fetch(_ context.Context, _, _ time.Time) (reconciliation.CostSummary, error) {
	return r.summary, nil
}

func TestProviderCostReconciliationIsDurableThresholdedAndImmutable(t *testing.T) {
	pool, cleanup := reconciliationTestPool(t)
	defer cleanup()
	ctx := context.Background()
	actorID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Finance administrator','admin','active')`, actorID, actorID.String()+"@test.local", "admin_"+actorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	seedSuccessfulGeneration(t, ctx, pool, actorID, start.Add(6*time.Hour), 2)
	reader := testCostReader{summary: reconciliation.CostSummary{Provider: "openai", Currency: "USD", PeriodStart: start, PeriodEnd: start.Add(24 * time.Hour), CostMicros: 25_000}}
	service := reconciliation.NewService(pool, reader, 10_000)
	item, err := service.Request(ctx, actorID, reconciliation.RequestInput{
		Provider: "openai", PeriodStart: start, PeriodEnd: start.Add(24 * time.Hour), Reason: "Reconcile the approved daily OpenAI staging cost period.", Confirmed: true,
	}, "reconciliation-test")
	if err != nil || item.Status != "queued" || item.JobID == nil {
		t.Fatalf("queued reconciliation mismatch: %#v %v", item, err)
	}
	if _, err := service.Request(ctx, actorID, reconciliation.RequestInput{
		Provider: "openai", PeriodStart: start, PeriodEnd: start.Add(24 * time.Hour), Reason: "Attempt duplicate active reconciliation for the exact cost period.", Confirmed: true,
	}, "reconciliation-duplicate"); !errors.Is(err, reconciliation.ErrConflict) {
		t.Fatalf("duplicate active reconciliation accepted: %v", err)
	}
	job := claimReconciliationJob(t, ctx, pool)
	if err := service.HandleJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobs.NewRepository(pool).Complete(ctx, job, "reconciliation-test-worker"); err != nil {
		t.Fatal(err)
	}
	item, err = service.Get(ctx, item.ID)
	if err != nil || item.Status != "matched" || item.ProviderCostMicros == nil || *item.ProviderCostMicros != 25_000 || item.LocalEstimatedCostMicros == nil || *item.LocalEstimatedCostMicros != 20_000 || item.VarianceMicros == nil || *item.VarianceMicros != 5_000 || item.ReportedInputTokens == nil || *item.ReportedInputTokens != 17 || item.ReportedTotalTokens == nil || *item.ReportedTotalTokens != 31 {
		t.Fatalf("final reconciliation mismatch: %#v %v", item, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE provider_cost_reconciliations SET status='failed' WHERE id=$1`, item.ID); err == nil {
		t.Fatal("final reconciliation was mutable")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM provider_cost_reconciliations WHERE id=$1`, item.ID); err == nil {
		t.Fatal("reconciliation evidence was deletable")
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_type='provider_cost_reconciliation' AND resource_id=$1`, item.ID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("request audit evidence mismatch: count=%d err=%v", auditCount, err)
	}
}

func TestProviderCostReconciliationMarksOverage(t *testing.T) {
	pool, cleanup := reconciliationTestPool(t)
	defer cleanup()
	ctx := context.Background()
	actorID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Finance administrator','admin','active')`, actorID, actorID.String()+"@test.local", "admin_"+actorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.August, 2, 0, 0, 0, 0, time.UTC)
	reader := testCostReader{summary: reconciliation.CostSummary{Provider: "openai", Currency: "USD", PeriodStart: start, PeriodEnd: start.Add(24 * time.Hour), CostMicros: 10_001}}
	service := reconciliation.NewService(pool, reader, 10_000)
	item, err := service.Request(ctx, actorID, reconciliation.RequestInput{
		Provider: "openai", PeriodStart: start, PeriodEnd: start.Add(24 * time.Hour), Reason: "Reconcile an approved empty OpenAI daily staging period.", Confirmed: true,
	}, "reconciliation-overage")
	if err != nil {
		t.Fatal(err)
	}
	job := claimReconciliationJob(t, ctx, pool)
	if err := service.HandleJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	item, err = service.Get(ctx, item.ID)
	if err != nil || item.Status != "overage" || item.VarianceMicros == nil || *item.VarianceMicros != 10_001 {
		t.Fatalf("overage mismatch: %#v %v", item, err)
	}
}

func seedSuccessfulGeneration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ownerID uuid.UUID, completedAt time.Time, cents int) {
	t.Helper()
	generationID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,charged_cost_cents,updated_at)
		VALUES($1,$2,'chat','openai','gpt-test','A bounded staging prompt','succeeded',100,$3,$3,$4)`, generationID, ownerID, cents, completedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO generation_provider_usage(generation_id,provider,model_name,status,input_tokens,cached_input_tokens,output_tokens,reasoning_tokens,total_tokens)
		VALUES($1,'openai','gpt-test','reported',17,0,14,0,31)`, generationID); err != nil {
		t.Fatal(err)
	}
}

func claimReconciliationJob(t *testing.T, ctx context.Context, pool *pgxpool.Pool) jobs.Job {
	t.Helper()
	job, err := jobs.NewRepository(pool).Claim(ctx, "reconciliation-test-worker", time.Minute)
	if err != nil || job.Kind != reconciliation.JobKind {
		t.Fatalf("claim reconciliation job: %#v %v", job, err)
	}
	return job
}

func reconciliationTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	root, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	if err := root.Ping(ctx); err != nil {
		root.Close()
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	schema := "test_reconciliation_" + uuid.NewString()[:8]
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		root.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	}
}
