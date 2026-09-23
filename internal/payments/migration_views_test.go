package payments

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Historical fixtures may retain financial evidence while rebuilding an older
// identity table. Temporarily remove only derived views in their isolated test
// schema, preserving all tables, guards and evidence. This is not a production
// downgrade: 0164 correctly refuses to downgrade a funded database.
func suspendSellerFundsViewsForMigrationTest(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	ctx := context.Background()
	names := []string{"seller_funds_settlement_scopes", "seller_funds_request_scopes", "seller_funds_ledger_scopes", "seller_funds_recovery_scopes", "seller_funds_unscoped"}
	definitions := make([]string, len(names))
	for i, name := range names {
		if err := pool.QueryRow(ctx, `SELECT pg_get_viewdef($1::regclass, true)`, name).Scan(&definitions[i]); err != nil {
			t.Fatalf("read derived view %s: %v", name, err)
		}
	}
	for i := len(names) - 1; i >= 0; i-- {
		if _, err := pool.Exec(ctx, "DROP VIEW "+pgx.Identifier{names[i]}.Sanitize()); err != nil {
			t.Fatalf("detach derived view %s: %v", names[i], err)
		}
	}
	return func() {
		t.Helper()
		for i, name := range names {
			if _, err := pool.Exec(ctx, "CREATE VIEW "+pgx.Identifier{name}.Sanitize()+" AS "+definitions[i]); err != nil {
				t.Fatalf("restore derived view %s: %v", name, err)
			}
		}
	}
}
