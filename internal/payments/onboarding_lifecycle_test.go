package payments

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPayoutOnboardingRejectsInactiveExistingAccounts(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, status := range []string{"suspended", "deleted"} {
		t.Run(status, func(t *testing.T) {
			user, _ := payoutOrderingOwner(t, pool)
			if _, err := pool.Exec(ctx, `UPDATE users SET status=$2 WHERE id=$1`, user, status); err != nil {
				t.Fatal(err)
			}
			runtime := &onboardingRuntime{}
			service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
			link, err := service.BeginPayoutOnboarding(ctx, user, "https://app.example.test/settings", "https://app.example.test/settings", "inactive")
			if !errors.Is(err, ErrPayoutNotFound) || link.URL != "" || runtime.accountCalls != 0 || runtime.linkCalls != 0 {
				t.Fatalf("inactive user obtained onboarding: accountCalls=%d linkCalls=%d url=%q err=%v", runtime.accountCalls, runtime.linkCalls, link.URL, err)
			}
			var audits int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_id=$1`, user).Scan(&audits); err != nil || audits != 0 {
				t.Fatalf("audits=%d err=%v", audits, err)
			}
		})
	}
}

func TestPayoutOnboardingRechecksAfterLifecycleWait(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_%t", existing), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			user, _ := payoutOrderingOwner(t, pool)
			if !existing {
				if _, err := pool.Exec(ctx, `DELETE FROM payment_destinations WHERE user_id=$1`, user); err != nil {
					t.Fatal(err)
				}
			}
			gate, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if err := accountlifecycle.Lock(ctx, gate, user); err != nil {
				t.Fatal(err)
			}
			runtime := &onboardingRuntime{}
			service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
			done := make(chan error, 1)
			go func() {
				_, err := service.BeginPayoutOnboarding(ctx, user, "https://app.example.test/settings", "https://app.example.test/settings", "lifecycle-wait")
				done <- err
			}()
			waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
			if _, err := gate.Exec(ctx, `UPDATE users SET status='deleted' WHERE id=$1`, user); err != nil {
				t.Fatal(err)
			}
			if err := gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, ErrPayoutNotFound) {
				t.Fatalf("post-deletion result=%v", err)
			}
			if runtime.accountCalls != 0 || runtime.linkCalls != 0 {
				t.Fatalf("dispatch after deletion: accounts=%d links=%d", runtime.accountCalls, runtime.linkCalls)
			}
		})
	}
}

type pausedOnboardingRuntime struct {
	onboardingRuntime
	entered chan struct{}
	release chan struct{}
}

func (r *pausedOnboardingRuntime) CreateAccountLink(ctx context.Context, input AccountLinkRequest) (AccountLink, error) {
	close(r.entered)
	select {
	case <-r.release:
	case <-ctx.Done():
		return AccountLink{}, ctx.Err()
	}
	return r.onboardingRuntime.CreateAccountLink(ctx, input)
}

func TestPayoutOnboardingSerializesSuspensionWithLink(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	user, _ := payoutOrderingOwner(t, pool)
	runtime := &pausedOnboardingRuntime{entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(runtime.release) }) }
	defer release()
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	done := make(chan error, 1)
	go func() {
		_, err := service.BeginPayoutOnboarding(ctx, user, "https://app.example.test/settings", "https://app.example.test/settings", "suspension")
		done <- err
	}()
	select {
	case <-runtime.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var ownerPID int32
	if err := pool.QueryRow(ctx, `SELECT a.pid FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid
	 WHERE a.datname=current_database() AND l.locktype='advisory' AND l.granted
	 AND l.classid::bigint=((hashtextextended($1,0)>>32)&4294967295)
	 AND l.objid::bigint=(hashtextextended($1,0)&4294967295) AND l.objsubid=1
	 AND a.state='idle in transaction' LIMIT 1`, accountlifecycle.Key(user)).Scan(&ownerPID); err != nil {
		t.Fatal(err)
	}
	suspended := make(chan error, 1)
	go func() {
		_, err := pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, user)
		suspended <- err
	}()
	waitForProductBlockingTx(t, ctx, pool, ownerPID)
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-suspended; err != nil {
		t.Fatal(err)
	}
	if runtime.linkCalls != 1 {
		t.Fatalf("links=%d", runtime.linkCalls)
	}
	if _, err := service.BeginPayoutOnboarding(ctx, user, "https://app.example.test/settings", "https://app.example.test/settings", "after-suspension"); !errors.Is(err, ErrPayoutNotFound) {
		t.Fatalf("suspended account admitted: %v", err)
	}
}

type failingOnboardingLinkRuntime struct {
	onboardingRuntime
}

func (r *failingOnboardingLinkRuntime) CreateAccountLink(ctx context.Context, input AccountLinkRequest) (AccountLink, error) {
	link, err := r.onboardingRuntime.CreateAccountLink(ctx, input)
	if r.linkCalls == 1 {
		return AccountLink{}, errors.New("simulated link request failure")
	}
	return link, err
}

func TestPayoutOnboardingKeepsAccountAfterLinkFailureWithOneConnection(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	user, _ := payoutOrderingOwner(t, pool)
	if _, err := pool.Exec(ctx, `DELETE FROM payment_destinations WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	config := pool.Config().Copy()
	config.MaxConns, config.MinConns = 1, 0
	single, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	runtime := &failingOnboardingLinkRuntime{}
	service := NewServiceWithRuntimes(single, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	if _, err := service.BeginPayoutOnboarding(ctx, user, "https://app.example.test/settings", "https://app.example.test/settings", "failed-link"); err == nil {
		t.Fatal("link failure was ignored")
	}
	var destinations, audits int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_destinations WHERE user_id=$1),
	 (SELECT count(*) FROM audit_events WHERE actor_id=$1 AND action='payment.payout_destination_created')`, user).Scan(&destinations, &audits); err != nil || destinations != 1 || audits != 1 {
		t.Fatalf("creation evidence lost: destinations=%d audits=%d err=%v", destinations, audits, err)
	}
	link, err := service.BeginPayoutOnboarding(ctx, user, "https://app.example.test/settings", "https://app.example.test/settings", "retry-link")
	if err != nil || link.URL == "" || runtime.accountCalls != 1 || runtime.linkCalls != 2 {
		t.Fatalf("retry recreated account or stalled: accounts=%d links=%d err=%v", runtime.accountCalls, runtime.linkCalls, err)
	}
}
