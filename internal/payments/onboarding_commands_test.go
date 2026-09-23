package payments

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type uncertainAccountRuntime struct {
	onboardingRuntime
	identity ProductCheckoutIdentity
	requests []ConnectAccountRequest
	fail     bool
}

func newUncertainAccountRuntime() *uncertainAccountRuntime {
	identity, _ := (&productCheckoutRuntime{}).ProductCheckoutIdentity(context.Background())
	return &uncertainAccountRuntime{identity: identity}
}

func (r *uncertainAccountRuntime) ProductCheckoutIdentity(context.Context) (ProductCheckoutIdentity, error) {
	return r.identity, nil
}

func (r *uncertainAccountRuntime) CreateConnectAccount(ctx context.Context, input ConnectAccountRequest) (ConnectAccount, error) {
	r.requests = append(r.requests, input)
	account, err := r.onboardingRuntime.CreateConnectAccount(ctx, input)
	if r.fail {
		return ConnectAccount{}, errors.New("simulated lost account response")
	}
	return account, err
}

func payoutCommandOwner(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	user, _ := payoutOrderingOwner(t, pool)
	if _, err := pool.Exec(context.Background(), `DELETE FROM payment_destinations WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	return user
}

func callPayoutOnboarding(service *Service, user uuid.UUID) (PayoutOnboardingLink, error) {
	return service.BeginPayoutOnboarding(context.Background(), user,
		"https://app.example.test/settings", "https://app.example.test/settings", "command-test")
}

func TestPayoutOnboardingRecoversOriginalCommandAfterLostResponse(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	user := payoutCommandOwner(t, pool)
	runtime := newUncertainAccountRuntime()
	runtime.fail = true
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	if _, err := callPayoutOnboarding(service, user); err == nil {
		t.Fatal("lost account response was ignored")
	}
	var commands, destinations int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payout_account_commands WHERE user_id=$1 AND completed_at IS NULL),
	 (SELECT count(*) FROM payment_destinations WHERE user_id=$1)`, user).Scan(&commands, &destinations); err != nil || commands != 1 || destinations != 0 {
		t.Fatalf("lost durable reservation: commands=%d destinations=%d err=%v", commands, destinations, err)
	}
	status, err := service.GetPayoutStatus(ctx, user)
	if err != nil || status.Status != "creation_pending" || !status.CanStartOnboarding {
		t.Fatalf("uncertain account misrepresented: status=%+v err=%v", status, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email=$2 WHERE id=$1`, user, user.String()+"-changed@example.test"); err != nil {
		t.Fatal(err)
	}
	runtime.fail = false
	// Recreate the service to demonstrate recovery from persisted evidence.
	service = NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	link, err := callPayoutOnboarding(service, user)
	if err != nil || link.URL == "" || len(runtime.requests) != 2 {
		t.Fatalf("original account not recovered: requests=%d err=%v", len(runtime.requests), err)
	}
	first, second := runtime.requests[0], runtime.requests[1]
	if first.Email != second.Email || first.UserID != second.UserID || first.Identity == nil || second.Identity == nil || *first.Identity != *second.Identity ||
		first.RetryBefore.IsZero() || !first.RetryBefore.Before(time.Now().Add(23*time.Hour)) {
		t.Fatal("recovery changed original parameters or discarded the retry deadline")
	}
	if _, err := callPayoutOnboarding(service, user); err != nil || runtime.accountCalls != 2 || runtime.linkCalls != 2 {
		t.Fatalf("completed account was recreated: accounts=%d links=%d err=%v", runtime.accountCalls, runtime.linkCalls, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payout_account_commands c JOIN payment_destinations d
	 ON d.user_id=c.user_id AND d.destination_id=c.destination_id WHERE c.user_id=$1 AND c.completed_at IS NOT NULL`, user).Scan(&commands); err != nil || commands != 1 {
		t.Fatalf("completion not atomically bound to destination: count=%d err=%v", commands, err)
	}
}

func TestPayoutOnboardingRejectsChangedCreationIdentity(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, field := range []string{"merchant", "mode", "endpoint", "api_version", "request_version"} {
		t.Run(field, func(t *testing.T) {
			user := payoutCommandOwner(t, pool)
			runtime := newUncertainAccountRuntime()
			runtime.fail = true
			service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
			if _, err := callPayoutOnboarding(service, user); err == nil {
				t.Fatal("expected simulated response loss")
			}
			switch field {
			case "merchant":
				runtime.identity.MerchantID = "acct_other"
			case "mode":
				runtime.identity.LiveMode = true
			case "endpoint":
				runtime.identity.Endpoint = "https://other.example.test/v1"
			case "api_version":
				runtime.identity.APIVersion = "other-version"
			case "request_version":
				runtime.identity.RequestVersion = "other-serialization"
			}
			if _, err := callPayoutOnboarding(service, user); !errors.Is(err, ErrPayoutReconciliation) || runtime.accountCalls != 1 || runtime.linkCalls != 0 {
				t.Fatalf("changed identity sent an account command: calls=%d err=%v", runtime.accountCalls, err)
			}
		})
	}
}

func insertPayoutCommand(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, identity ProductCheckoutIdentity, timestamp time.Time) {
	t.Helper()
	body, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), `INSERT INTO payout_account_commands(user_id,identity,email,command_version,idempotency_key,reserved_at)
	 VALUES($1,$2,$3,$4,$5,$6)`, user, body, user.String()+"@example.test", connectAccountCommandVersion, "connect-account-"+user.String(), timestamp)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPayoutOnboardingRejectsUnsafeRetryWindow(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, offset := range []time.Duration{-24 * time.Hour, time.Hour} {
		t.Run(offset.String(), func(t *testing.T) {
			user := payoutCommandOwner(t, pool)
			runtime := newUncertainAccountRuntime()
			insertPayoutCommand(t, pool, user, runtime.identity, time.Now().Add(offset))
			service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
			status, err := service.GetPayoutStatus(context.Background(), user)
			if err != nil || status.Status != "recovery_required" || status.CanStartOnboarding {
				t.Fatalf("unsafe command advertised retry: %+v err=%v", status, err)
			}
			if _, err := callPayoutOnboarding(service, user); !errors.Is(err, ErrPayoutReconciliation) || runtime.accountCalls != 0 {
				t.Fatalf("unsafe command sent: calls=%d err=%v", runtime.accountCalls, err)
			}
		})
	}
}

func TestPayoutOnboardingKeepsCommandAfterLocalSaveFailure(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	user := payoutCommandOwner(t, pool)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_payout_save() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'simulated local save failure'; END; $$ LANGUAGE plpgsql;
	 CREATE TRIGGER fail_payout_save BEFORE INSERT ON payment_destinations FOR EACH ROW EXECUTE FUNCTION fail_payout_save()`); err != nil {
		t.Fatal(err)
	}
	runtime := newUncertainAccountRuntime()
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	if _, err := callPayoutOnboarding(service, user); err == nil || runtime.accountCalls != 1 || runtime.linkCalls != 0 {
		t.Fatalf("expected local failure after remote response: err=%v", err)
	}
	var pending bool
	if err := pool.QueryRow(ctx, `SELECT completed_at IS NULL AND destination_id IS NULL FROM payout_account_commands WHERE user_id=$1`, user).Scan(&pending); err != nil || !pending {
		t.Fatalf("local failure lost pending evidence: err=%v", err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_payout_save ON payment_destinations`); err != nil {
		t.Fatal(err)
	}
	if _, err := callPayoutOnboarding(service, user); err != nil || runtime.accountCalls != 2 || runtime.linkCalls != 1 {
		t.Fatalf("local failure not recoverable with original command: err=%v", err)
	}
}

func TestPayoutAccountCommandEvidenceGuards(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	user := payoutCommandOwner(t, pool)
	runtime := newUncertainAccountRuntime()
	insertPayoutCommand(t, pool, user, runtime.identity, time.Now().Add(-time.Minute))
	for _, statement := range []string{
		`UPDATE payout_account_commands SET email='other@example.test' WHERE user_id=$1`,
		`UPDATE payout_account_commands SET reserved_at=clock_timestamp() WHERE user_id=$1`,
		`UPDATE payout_account_commands SET identity=jsonb_set(identity,'{merchantId}','"acct_other"') WHERE user_id=$1`,
		`DELETE FROM payout_account_commands WHERE user_id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, user); err == nil {
			t.Fatalf("immutable evidence changed: %s", statement)
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0147_payout_account_commands.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("down migration discarded uncertain external write")
	}
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	if _, err := callPayoutOnboarding(service, user); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payout_account_commands SET destination_id='acct_other' WHERE user_id=$1`, user); err == nil {
		t.Fatal("completed account evidence was replaced")
	}
}
