package creation_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestFailureEvidenceCommitFailureAndConcurrentReplay(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	owner, generation := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Evidence fixture')`, owner, owner.String()+"@test.local", "evidence_"+owner.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status) VALUES($1,$2,'image','local','fixture','fixture','failed')`, generation, owner); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]uuid.UUID{"generationId": generation})
	if err != nil {
		t.Fatal(err)
	}
	job := jobs.Job{Kind: creation.FailureEvidenceJobKind, Payload: payload}
	service := creation.NewService(pool, t.TempDir(), "", true)
	// A deferred trigger fails at commit, after both evidence writes succeeded.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_evidence_commit() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected commit failure'; END $$ LANGUAGE plpgsql;
	CREATE CONSTRAINT TRIGGER reject_evidence_commit AFTER INSERT ON notifications DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_evidence_commit()`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleJob(ctx, job); err == nil {
		t.Fatal("commit fault was not returned")
	} else if !strings.Contains(err.Error(), "injected commit failure") {
		t.Fatalf("unexpected failure before deferred commit: %v", err)
	}
	assertCounts := func(want int) {
		t.Helper()
		var audits, notices int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE action='generation.failed' AND resource_id=$1), (SELECT count(*) FROM notifications WHERE resource_id=$1)`, generation).Scan(&audits, &notices); err != nil {
			t.Fatal(err)
		}
		if audits != want || notices != want {
			t.Fatalf("audits=%d notices=%d want=%d", audits, notices, want)
		}
	}
	assertCounts(0)
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_evidence_commit ON notifications`); err != nil {
		t.Fatal(err)
	}
	// Hold the shared chain lock so competing handlers overlap before insertion.
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(ctx, `SELECT * FROM audit_chain_state FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- service.HandleJob(ctx, job) }()
	}
	// Both handlers must reach their database lock before releasing the blocker.
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	assertCounts(1)
}
