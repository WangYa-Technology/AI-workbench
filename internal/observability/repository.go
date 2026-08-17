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
	_, err := r.pool.Exec(ctx, `WITH inserted AS (
		INSERT INTO request_observations(request_id,method,route,status,duration_ms,response_bytes) VALUES($1,$2,$3,$4,$5,$6) RETURNING id
	), purged AS (
		DELETE FROM request_observations WHERE occurred_at < now()-interval '7 days' RETURNING id
	) SELECT id FROM inserted`, item.RequestID, item.Method, item.Route, item.Status, item.Duration.Milliseconds(), item.ResponseBytes)
	if err != nil {
		return fmt.Errorf("record request observation: %w", err)
	}
	return nil
}
