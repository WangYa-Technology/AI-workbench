package observability

import (
	"context"
	"fmt"
	"strings"

	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) RecordRequest(ctx context.Context, item httputil.RequestObservation) error {
	item.Route = strings.TrimSpace(item.Route)
	if item.Route == "" || len(item.Route) > 240 {
		item.Route = "unmatched"
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO request_observations(request_id,method,route,status,duration_ms,response_bytes)
		VALUES($1,$2,$3,$4,$5,$6)`, item.RequestID, item.Method, item.Route, item.Status, item.Duration.Milliseconds(), item.ResponseBytes)
	if err != nil {
		return fmt.Errorf("record request observation: %w", err)
	}
	return nil
}

// PurgeExpired removes a bounded batch so retention maintenance never runs in
// the request path or takes an unbounded delete lock.
func (r *Repository) PurgeExpired(ctx context.Context, batchSize int) error {
	if batchSize < 1 || batchSize > 10000 {
		batchSize = 1000
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM request_observations WHERE id IN (
		SELECT id FROM request_observations WHERE occurred_at < now()-interval '7 days' ORDER BY id LIMIT $1
	)`, batchSize)
	if err != nil {
		return fmt.Errorf("purge request observations: %w", err)
	}
	return nil
}
