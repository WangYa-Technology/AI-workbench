package billing_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStatementFiltersStableCursorAndOwnerIsolation(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, outsiderID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Statement Owner','creator','active'),
		($4,$5,$6,'Statement Outsider','creator','active')`,
		ownerID, ownerID.String()+"@test.local", "statement_"+ownerID.String()[:8],
		outsiderID, outsiderID.String()+"@test.local", "outside_"+outsiderID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE billing_accounts SET balance_cents=CASE WHEN user_id=$1 THEN 10000 ELSE 5000 END WHERE user_id IN ($1,$2)`, ownerID, outsiderID); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-4000-8000-000000000206"),
		uuid.MustParse("00000000-0000-4000-8000-000000000205"),
		uuid.MustParse("00000000-0000-4000-8000-000000000204"),
		uuid.MustParse("00000000-0000-4000-8000-000000000203"),
	}
	rows := []struct {
		entryType, direction string
		createdAt            time.Time
	}{
		{"generation_charge", "debit", base.Add(3 * time.Minute)},
		{"product_sale", "credit", base.Add(3 * time.Minute)},
		{"generation_charge", "debit", base.Add(2 * time.Minute)},
		{"task_earning", "credit", base.Add(time.Minute)},
	}
	for index, row := range rows {
		if _, err := pool.Exec(ctx, `
			INSERT INTO billing_entries(id,user_id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description,created_at)
			VALUES($1,$2,$3,$4,$5,25,'USD',9975,'Statement test entry',$6)`, ids[index], ownerID, uuid.New(), row.entryType, row.direction, row.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing_entries(user_id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description,created_at)
		VALUES($1,$2,'admin_adjustment','credit',50,'USD',5050,'Foreign statement entry',$3)`, outsiderID, uuid.New(), base.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	service := billing.NewService(pool)
	first, err := service.Statement(ctx, ownerID, billing.StatementInput{Limit: 2})
	if err != nil || len(first.Entries) != 2 || first.NextCursor == nil {
		t.Fatalf("first statement page: %#v err=%v", first, err)
	}
	if first.Entries[0].ID != ids[0] || first.Entries[1].ID != ids[1] {
		t.Fatalf("same-time statement ordering is unstable: %#v", first.Entries)
	}
	second, err := service.Statement(ctx, ownerID, billing.StatementInput{Limit: 2, Cursor: *first.NextCursor})
	if err != nil || len(second.Entries) != 2 || second.NextCursor != nil {
		t.Fatalf("second statement page: %#v err=%v", second, err)
	}
	filtered, err := service.Statement(ctx, ownerID, billing.StatementInput{
		Direction: "debit", EntryType: "generation_charge", DateFrom: timePointer(base.Add(90 * time.Second)), DateTo: timePointer(base.Add(4 * time.Minute)),
	})
	if err != nil || len(filtered.Entries) != 2 || filtered.Account.UserID != ownerID {
		t.Fatalf("combined statement filters: %#v err=%v", filtered, err)
	}
	outsider, err := service.Statement(ctx, outsiderID, billing.StatementInput{Limit: 50})
	if err != nil || len(outsider.Entries) != 1 || outsider.Entries[0].Description != "Foreign statement entry" {
		t.Fatalf("owner isolation failed: %#v err=%v", outsider, err)
	}

	invalid := []billing.StatementInput{
		{Direction: "sideways"}, {EntryType: "unknown"}, {Limit: 51}, {Cursor: "modified"},
		{DateFrom: timePointer(base.Add(time.Hour)), DateTo: timePointer(base)},
	}
	for _, input := range invalid {
		if _, err := service.Statement(ctx, ownerID, input); !errors.Is(err, billing.ErrInvalidStatement) {
			t.Fatalf("invalid statement input accepted: %#v err=%v", input, err)
		}
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func testPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	schema := "test_billing_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(baseURL)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
