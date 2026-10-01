package billing_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
)

func TestPointEntriesPaginationAndSubscriptionExpiry(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := t.Context()
	owner, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{owner, other} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Point Audit')`, id, id.String()+"@test.local", "pa_"+id.String()[:8]); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO point_entries(user_id,operation_id,entry_type,direction,amount_points,balance_after_points,description,created_at) SELECT $1,gen_random_uuid(),'admin_adjustment','credit',1,1,'Audit entry',now() FROM generate_series(1,5)`, id); err != nil {
			t.Fatal(err)
		}
	}
	svc := billing.NewService(pool)
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for {
		page, err := svc.PointOverviewPage(ctx, owner, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Entries {
			if seen[entry.ID] {
				t.Fatal("duplicate entry across pages")
			}
			seen[entry.ID] = true
			var user uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT user_id FROM point_entries WHERE id=$1`, entry.ID).Scan(&user); err != nil || user != owner {
				t.Fatalf("owner isolation: %v", err)
			}
		}
		if page.NextEntryCursor == nil {
			break
		}
		cursor = *page.NextEntryCursor
	}
	var expected int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM point_entries WHERE user_id=$1`, owner).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	if len(seen) != expected {
		t.Fatalf("expected all %d entries, got %d", expected, len(seen))
	}
	for _, input := range []struct {
		cursor string
		limit  int
	}{{"bad", 2}, {"", -1}, {"", 51}} {
		if _, err := svc.PointOverviewPage(ctx, owner, input.cursor, input.limit); !errors.Is(err, billing.ErrInvalidPointEntries) {
			t.Fatalf("invalid pagination: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE user_subscriptions SET current_period_end=now()-interval '1 hour' WHERE user_id=$1 AND status='active'`, owner); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.ExpireSubscriptions(ctx, 1); err != nil || n != 1 {
		t.Fatalf("expiry: n=%d err=%v", n, err)
	}
	if n, err := svc.ExpireSubscriptions(ctx, 1); err != nil || n != 0 {
		t.Fatalf("expiry replay: n=%d err=%v", n, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM user_subscriptions WHERE user_id=$1`, owner).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "expired" {
		t.Fatalf("subscription %s: got %s want expired", owner, status)
	}
}
