package testutil

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GateQuery pauses one SQL statement before execution on a separate pool for
// the same isolated schema. Tests can establish an exact interleaving without
// sleep-based timing or hooks in production code. The caller must release the
// gate and join its goroutine before returning from the test.
func GateQuery(t *testing.T, pool *pgxpool.Pool, query string) (*pgxpool.Pool, <-chan struct{}, func()) {
	return GateNthQuery(t, pool, query, 1)
}

// GateNthQuery can pause a transactional recheck after an earlier, identical
// preflight query without adding synchronization hooks to production code.
func GateNthQuery(t *testing.T, pool *pgxpool.Pool, query string, occurrence int32) (*pgxpool.Pool, <-chan struct{}, func()) {
	t.Helper()
	if occurrence < 1 {
		t.Fatal("query gate occurrence must be positive")
	}
	gate := &queryGate{query: query, occurrence: occurrence, entered: make(chan struct{}), released: make(chan struct{})}
	config := pool.Config()
	config.ConnConfig.Tracer = gate
	traced, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { close(gate.released) }) }
	t.Cleanup(traced.Close)
	t.Cleanup(release)
	return traced, gate.entered, release
}

type queryGate struct {
	query             string
	entered, released chan struct{}
	matches           atomic.Int32
	occurrence        int32
}

func (g *queryGate) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, g.query) && g.matches.Add(1) == g.occurrence {
		close(g.entered)
		select {
		case <-g.released:
		case <-ctx.Done():
		}
	}
	return ctx
}

func (*queryGate) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
