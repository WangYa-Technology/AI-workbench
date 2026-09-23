package authchallenges

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConcurrentChallengeRequestsRespectResendWindow(t *testing.T) {
	pool, cleanup := challengeTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := []byte("01234567890123456789012345678901")
	root := t.TempDir()
	for _, purpose := range []string{RegistrationCode, LoginCode} {
		t.Run(purpose, func(t *testing.T) {
			// Use separately constructed services and distinct pool connections;
			// an in-process mutex cannot supply the cross-instance guarantee.
			cfg := pool.Config()
			appName := "challenge-race-" + uuid.NewString()
			cfg.ConnConfig.RuntimeParams["application_name"] = appName
			workers, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer workers.Close()
			gate, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			const gateID = int64(577035702091)
			if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, gateID); err != nil {
				t.Fatal(err)
			}
			// Pause at the actual insert, after the old implementation has
			// already observed an empty resend window. No production hooks.
			_, err = pool.Exec(ctx, fmt.Sprintf(`
CREATE OR REPLACE FUNCTION gate_challenge_insert() RETURNS trigger AS $$
BEGIN
 IF NEW.email_snapshot='concurrent@example.test' AND NEW.purpose='%s' THEN
  PERFORM pg_advisory_xact_lock(%d::bigint);
 END IF;
 RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER gate_challenge_insert BEFORE INSERT ON identity_auth_challenges
FOR EACH ROW EXECUTE FUNCTION gate_challenge_insert();`, purpose, gateID))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = gate.Rollback(context.Background())
				_, _ = pool.Exec(context.Background(), `DROP TRIGGER gate_challenge_insert ON identity_auth_challenges`)
			}()
			results := make(chan error, 2)
			for _, email := range []string{"concurrent@example.test", " CONCURRENT@example.test "} {
				go func(email string) {
					_, err := NewService(workers, key, "local_file", root).Start(ctx, email, purpose, "en-US", "concurrent-request")
					results <- err
				}(email)
			}
			var early []error
			for {
				var blocked int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND cardinality(pg_blocking_pids(pid))>0`, appName).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked+len(early) == 2 {
					break
				}
				select {
				case err := <-results:
					if !errors.Is(err, ErrRateLimited) {
						t.Fatalf("request finished before insert gate was released: %v", err)
					}
					early = append(early, err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			otherCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			_, otherErr := NewService(workers, key, "local_file", root).Start(otherCtx, "other@example.test", purpose, "en-US", "unrelated-request")
			stop()
			if otherErr != nil {
				t.Fatalf("one email blocked another recipient: %v", otherErr)
			}
			if err := gate.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			accepted, limited := 0, len(early)
			for range 2 - len(early) {
				select {
				case err := <-results:
					switch {
					case err == nil:
						accepted++
					case errors.Is(err, ErrRateLimited):
						limited++
					default:
						t.Errorf("concurrent resend returned an internal error: %v", err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if accepted != 1 || limited != 1 {
				t.Fatalf("resend window did not serialize requests: accepted=%d limited=%d", accepted, limited)
			}
			var challenges, deliveries int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_auth_challenges WHERE email_snapshot='concurrent@example.test' AND purpose=$1`, purpose).Scan(&challenges); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs j JOIN identity_auth_challenges c ON j.payload->>'challengeId'=c.id::text WHERE j.kind=$1 AND c.email_snapshot='concurrent@example.test' AND c.purpose=$2`, DeliveryJobKind, purpose).Scan(&deliveries); err != nil {
				t.Fatal(err)
			}
			if challenges != 1 || deliveries != 1 {
				t.Fatalf("concurrent requests created duplicate challenges/jobs: %d/%d", challenges, deliveries)
			}
			// A completed cooldown must permit a replacement and retire the
			// prior code/job, rather than leaving the lock or window stuck.
			if _, err := pool.Exec(ctx, `UPDATE identity_auth_challenges SET created_at=now()-interval '31 seconds' WHERE email_snapshot='concurrent@example.test' AND purpose=$1`, purpose); err != nil {
				t.Fatal(err)
			}
			if _, err := NewService(workers, key, "local_file", root).Start(ctx, "concurrent@example.test", purpose, "en-US", "after-cooldown"); err != nil {
				t.Fatalf("resend after cooldown failed: %v", err)
			}
			var active, retired int
			if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='queued'),count(*) FILTER (WHERE status='cancelled' AND code_hash IS NULL AND code_nonce IS NULL AND code_ciphertext IS NULL) FROM identity_auth_challenges WHERE email_snapshot='concurrent@example.test' AND purpose=$1`, purpose).Scan(&active, &retired); err != nil {
				t.Fatal(err)
			}
			if active != 1 || retired != 1 {
				t.Fatalf("replacement did not invalidate the old challenge: active=%d retired=%d", active, retired)
			}
		})
	}
}
