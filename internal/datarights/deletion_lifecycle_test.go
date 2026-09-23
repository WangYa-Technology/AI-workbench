package datarights_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func deletionOwner(t *testing.T, pool *pgxpool.Pool) (identity.User, string) {
	t.Helper()
	handle := "delete_" + uuid.NewString()[:8]
	user, token, err := identity.NewRepository(pool).Register(context.Background(), identity.RegisterInput{
		Email: handle + "@test.local", Password: "local-test-password", Handle: handle,
		DisplayName: "Deletion owner", Locale: "en-US", Timezone: "UTC",
	}, identity.ClientInfo{Label: "Deletion lifecycle test", RequestID: "register-deletion"})
	if err != nil {
		t.Fatal(err)
	}
	return user, token
}

func waitDeletionBlocker(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blocker uint32) uint32 {
	t.Helper()
	for {
		var pid uint32
		err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) LIMIT 1`, blocker).Scan(&pid)
		if err == nil {
			return pid
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("expected database lock wait", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestDeletionWaitsForLegalHoldBeforeTakingRequestLock(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner, token := deletionOwner(t, pool)
	actor := cleanupUser(t, pool, "admin", "active")
	service := datarights.NewService(pool, t.TempDir())
	request, err := service.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: owner.Handle}, "delete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 second' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, owner.ID); err != nil {
		t.Fatal(err)
	}
	holdDone := make(chan error, 1)
	go func() {
		_, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner.ID, AuthorityReference: "LEGAL-CONCURRENT-HOLD"}, "hold")
		holdDone <- err
	}()
	holdPID := waitDeletionBlocker(t, ctx, pool, gate.Conn().PgConn().PID())
	deletionDone := make(chan error, 1)
	payload, _ := json.Marshal(map[string]uuid.UUID{"requestId": request.ID})
	go func() { deletionDone <- service.HandleDeletionJob(ctx, jobs.Job{Payload: payload}) }()
	// The worker must wait for the hold operation, not take the request and
	// later wait for its user lock (the previous lock inversion).
	waitDeletionBlocker(t, ctx, pool, holdPID)
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-holdDone; err != nil {
		t.Fatal("hold creation failed", err)
	}
	if err := <-deletionDone; err != nil {
		t.Fatal("deletion failed instead of respecting hold", err)
	}
	var userStatus, requestStatus string
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT u.status,r.status,(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=r.id)
 FROM users u JOIN data_rights_requests r ON r.user_id=u.id WHERE r.id=$1`, request.ID).Scan(&userStatus, &requestStatus, &receipts); err != nil {
		t.Fatal(err)
	}
	if userStatus != "active" || requestStatus != "blocked" || receipts != 0 {
		t.Fatalf("hold bypassed: %s %s receipts=%d", userStatus, requestStatus, receipts)
	}
	if _, err := identity.NewRepository(pool).Authenticate(ctx, token); err != nil {
		t.Fatal("held account lost access", err)
	}
}

func TestHoldReleaseFindsCurrentDeletionAndPreservesGrace(t *testing.T) {
	for _, scenario := range []string{"hold_first", "legacy_unlinked", "cancel_then_request", "old_job_completed"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			owner, token := deletionOwner(t, pool)
			actor := cleanupUser(t, pool, "admin", "active")
			service := datarights.NewService(pool, t.TempDir())
			hold, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner.ID, AuthorityReference: "LEGAL-BEFORE-DELETION"}, "hold")
			if err != nil {
				t.Fatal(err)
			}
			input := datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: owner.Handle}
			request, err := service.Create(ctx, owner.ID, token, input, "request")
			if err != nil || request.Status != "blocked" {
				t.Fatal(request, err)
			}
			var linked uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT request_id FROM data_rights_legal_holds WHERE id=$1`, hold.ID).Scan(&linked); err != nil || linked != request.ID {
				t.Fatal("new request did not bind hold", linked, err)
			}
			var cancelled uuid.UUID
			switch scenario {
			case "legacy_unlinked":
				_, err = pool.Exec(ctx, `UPDATE data_rights_legal_holds SET request_id=NULL WHERE id=$1`, hold.ID)
			case "cancel_then_request":
				cancelled = request.ID
				if _, err = service.Cancel(ctx, owner.ID, request.ID, "cancel"); err != nil {
					t.Fatal(err)
				}
				request, err = service.Create(ctx, owner.ID, token, input, "new-request")
				if err == nil {
					// Reproduce a historical link to an earlier cancelled request.
					_, err = pool.Exec(ctx, `UPDATE data_rights_legal_holds SET request_id=$2 WHERE id=$1`, hold.ID, cancelled)
				}
			case "old_job_completed":
				_, err = pool.Exec(ctx, `UPDATE jobs SET status='succeeded',attempts=1 WHERE kind=$1 AND payload->>'requestId'=$2`, datarights.DeletionJobKind, request.ID.String())
			}
			if err != nil {
				t.Fatal(err)
			}
			released, err := service.ReleaseHold(ctx, actor, hold.ID)
			if err != nil || released.RequestID == nil || *released.RequestID != request.ID {
				t.Fatal("release did not follow current request", released, err)
			}
			current, err := service.Get(ctx, owner.ID, request.ID)
			if err != nil || current.Status != "scheduled" || !current.ExecuteAfter.Equal(request.ExecuteAfter) || current.CancelUntil == nil || !current.CancelUntil.Equal(*request.CancelUntil) {
				t.Fatal("release changed grace or left request blocked", current, err)
			}
			var count int
			var availableAt time.Time
			if err := pool.QueryRow(ctx, `SELECT count(*),min(available_at) FROM jobs WHERE kind=$1 AND payload->>'requestId'=$2 AND status='queued'`, datarights.DeletionJobKind, request.ID.String()).Scan(&count, &availableAt); err != nil || count != 1 || availableAt.Before(request.ExecuteAfter) {
				t.Fatal("missing/duplicate/premature deletion job", count, availableAt, err)
			}
			if cancelled != uuid.Nil {
				prior, err := service.Get(ctx, owner.ID, cancelled)
				if err != nil || prior.Status != "cancelled" {
					t.Fatal("cancelled request revived", prior, err)
				}
			}
			payload, _ := json.Marshal(map[string]uuid.UUID{"requestId": request.ID})
			if err := service.HandleDeletionJob(ctx, jobs.Job{Payload: payload}); err != nil {
				t.Fatal(err)
			}
			if _, err := identity.NewRepository(pool).Authenticate(ctx, token); err != nil {
				t.Fatal("deleted before grace deadline", err)
			}
		})
	}
}

func TestHoldCannotCrossDeletionPreparationCutoff(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner, token := deletionOwner(t, pool)
	actor := cleanupUser(t, pool, "admin", "active")
	service := datarights.NewService(pool, t.TempDir())
	request, err := service.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: owner.Handle}, "request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 second' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	lockID := time.Now().UnixNano()
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION pause_delete_cutoff_test() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN PERFORM pg_advisory_xact_lock(%d::bigint); RETURN NEW; END $$;
 CREATE TRIGGER pause_delete_cutoff_test BEFORE UPDATE OF status ON users FOR EACH ROW WHEN(NEW.status='deleted') EXECUTE FUNCTION pause_delete_cutoff_test();
 CREATE FUNCTION fail_delete_receipt_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END $$;
 CREATE TRIGGER fail_delete_receipt_test BEFORE INSERT ON data_rights_deletion_receipts FOR EACH ROW EXECUTE FUNCTION fail_delete_receipt_test()`, lockID)); err != nil {
		t.Fatal(err)
	}
	deletionDone, holdDone := make(chan error, 1), make(chan error, 1)
	payload, _ := json.Marshal(map[string]uuid.UUID{"requestId": request.ID})
	go func() { deletionDone <- service.HandleDeletionJob(ctx, jobs.Job{Payload: payload}) }()
	deletionPID := waitDeletionBlocker(t, ctx, pool, gate.Conn().PgConn().PID())
	go func() {
		_, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner.ID, AuthorityReference: "LEGAL-AFTER-CUTOFF"}, "hold")
		holdDone <- err
	}()
	waitDeletionBlocker(t, ctx, pool, deletionPID)
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-holdDone; !errors.Is(err, datarights.ErrHoldCutoff) {
		t.Fatal("hold crossed irreversible cutoff", err)
	}
	if err := <-deletionDone; err == nil {
		t.Fatal("expected receipt failure")
	}
	current, err := service.Get(ctx, owner.ID, request.ID)
	if err != nil || current.Status != "processing" || current.Receipt != nil {
		t.Fatal("failed cleanup lost irreversible stage", current, err)
	}
	if _, err := service.Cancel(ctx, owner.ID, request.ID, "too-late"); !errors.Is(err, datarights.ErrNotCancelable) {
		t.Fatal("processing request became cancelable", err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_delete_receipt_test ON data_rights_deletion_receipts`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleDeletionJob(ctx, jobs.Job{Payload: payload}); err != nil {
		t.Fatal("cleanup continuation failed", err)
	}
	current, err = service.Get(ctx, owner.ID, request.ID)
	if err != nil || current.Status != "completed" || current.Receipt == nil {
		t.Fatal("cleanup did not complete", current, err)
	}
}
