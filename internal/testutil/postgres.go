package testutil

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Templates are process-owned, migrated from this binary's embedded sources,
// and closed to connections. Each test gets a physical copy, never reused data.
var errDatabaseConnection = errors.New("test database connection unavailable")

var testDatabases = struct {
	sync.Mutex
	templates map[string]*testTemplate
}{templates: make(map[string]*testTemplate)}

type testTemplate struct {
	admin *pgxpool.Pool
	base  string
	name  string
}

// DatabasePool requires CREATEDB on the test role. It never migrates or drops
// TEST_DATABASE_URL itself. The returned cleanup closes the main pool. Database
// removal follows later t.Cleanup callbacks, allowing auxiliary pools to close.
func DatabasePool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		base = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	template, err := databaseTemplate(ctx, base)
	if err != nil {
		if errors.Is(err, errDatabaseConnection) {
			DatabaseUnavailable(t, err)
		}
		t.Fatal(err)
	}
	name := "hcai_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := template.admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH TEMPLATE "+pgx.Identifier{template.name}.Sanitize()+" ALLOW_CONNECTIONS true"); err != nil {
		t.Fatal(err)
	}
	var pool *pgxpool.Pool
	var once sync.Once
	closePool := func() {
		once.Do(func() {
			if pool == nil {
				return
			}
			done := make(chan struct{})
			go func() { pool.Close(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Errorf("test database %s pool did not close: acquired=%d total=%d", name, pool.Stat().AcquiredConns(), pool.Stat().TotalConns())
			}
		})
	}
	// Registered before opening the database so setup failures also clean up.
	t.Cleanup(func() {
		closePool()
		// A closed client socket may remain visible while PostgreSQL is flushing
		// checkpoints during concurrent database churn. Use the cleanup budget for
		// this bounded grace period so transient server-side teardown latency does
		// not turn an otherwise closed pool into a test failure.
		disconnectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := waitDatabaseDisconnected(disconnectCtx, template.admin, name)
		cancel()
		if err != nil {
			t.Errorf("test database %s did not disconnect: %v", name, err)
		}
		// DROP DATABASE waits for a server-wide checkpoint. Give it a separate,
		// bounded budget during concurrent package teardown.
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if _, err := template.admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("remove owned test database %s: %v", name, err)
		}
	})
	connectionURL, err := isolatedDatabaseURL(template.base, name, "test:"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	pool, err = database.Open(ctx, connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	return pool, closePool
}

func databaseTemplate(ctx context.Context, base string) (*testTemplate, error) {
	testDatabases.Lock()
	defer testDatabases.Unlock()
	if result := testDatabases.templates[base]; result != nil {
		return result, nil
	}
	config, err := pgxpool.ParseConfig(base)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 2
	config.MinConns = 0
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		return nil, fmt.Errorf("%w: %v", errDatabaseConnection, err)
	}
	name := "hcai_template_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		admin.Close()
		return nil, fmt.Errorf("create isolated test template (test role needs CREATEDB): %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			admin.Close()
		}
	}()
	connectionURL, err := isolatedDatabaseURL(base, name, "test-template")
	if err != nil {
		return nil, err
	}
	pool, err := database.Open(ctx, connectionURL)
	if err != nil {
		return nil, err
	}
	var actual string
	if err = pool.QueryRow(ctx, "SELECT current_database()").Scan(&actual); err != nil || actual != name {
		pool.Close()
		return nil, fmt.Errorf("test database identity mismatch: %v", err)
	}
	if _, err = pool.Exec(ctx, "CREATE SCHEMA test_app"); err == nil {
		err = database.Migrate(ctx, pool)
	}
	pool.Close()
	if err != nil {
		return nil, err
	}
	if _, err = admin.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{name}.Sanitize()+" ALLOW_CONNECTIONS false"); err != nil {
		return nil, err
	}
	result := &testTemplate{admin: admin, base: base, name: name}
	testDatabases.templates[base] = result
	ok = true
	return result, nil
}

// RunWithDatabases removes only process-owned templates, including after failed
// assertions. A killed process still needs isolated environment teardown.
func RunWithDatabases(m *testing.M) int {
	code := m.Run()
	testDatabases.Lock()
	defer testDatabases.Unlock()
	for base, template := range testDatabases.templates {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := template.admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{template.name}.Sanitize()+" WITH (FORCE)")
		cancel()
		if err != nil {
			fmt.Fprintln(os.Stderr, "remove isolated test template:", err)
			code = 1
		}
		template.admin.Close()
		delete(testDatabases.templates, base)
	}
	return code
}

func isolatedDatabaseURL(base, name, app string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse test database URL: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", fmt.Errorf("TEST_DATABASE_URL must be a PostgreSQL URL")
	}
	u.Path = "/" + name
	u.RawPath = ""
	q := u.Query()
	q.Set("search_path", "test_app")
	q.Set("application_name", app)
	// Fresh, small fixtures do not amortize JIT compilation of retention queries.
	q.Set("jit", "off")
	q.Del("dbname")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func waitDatabaseDisconnected(ctx context.Context, pool *pgxpool.Pool, name string) error {
	for {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=$1", name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%d connections remain: %w", count, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
