package database_test

import (
	"context"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrateEmptySchema(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()

	var tables int
	err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_name IN ('users','works','generations','assets','jobs')`).Scan(&tables)
	if err != nil {
		t.Fatal(err)
	}
	if tables != 5 {
		t.Fatalf("expected five core tables, got %d", tables)
	}
	var currentSettings, legacySettingsTables int
	err = pool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM system_settings WHERE singleton=true),
		  (SELECT count(*) FROM information_schema.tables
		   WHERE table_schema=current_schema() AND table_name IN ('system_setting_state','system_setting_revisions'))`).Scan(&currentSettings, &legacySettingsTables)
	if err != nil {
		t.Fatal(err)
	}
	if currentSettings != 1 || legacySettingsTables != 0 {
		t.Fatalf("system settings migration mismatch: current=%d legacy_tables=%d", currentSettings, legacySettingsTables)
	}
	var auditLookupIndex int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname='audit_events_action_resource_idx'`).Scan(&auditLookupIndex); err != nil {
		t.Fatal(err)
	}
	if auditLookupIndex != 1 {
		t.Fatalf("audit evidence lookup index missing: %d", auditLookupIndex)
	}
	var extensionSchema string
	if err := pool.QueryRow(context.Background(), `SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pgcrypto'`).Scan(&extensionSchema); err != nil {
		t.Fatal(err)
	}
	if extensionSchema != "public" {
		t.Fatalf("pgcrypto must be installed in public schema, got %q", extensionSchema)
	}

	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("second migration must be idempotent: %v", err)
	}
}

func testPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		testutil.DatabaseUnavailable(t, err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		testutil.DatabaseUnavailable(t, err)
	}
	schema := "test_migrate_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
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
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
