package observability

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type mediaWriteBacklog struct {
	kind             string
	pending, invalid int64
	oldestAge        float64
}

// Original intent age cannot be reset by lock deferral or retry backoff. Active
// legal holds are deliberately retained, not overdue cleanup obligations. Test
// current holds instead of a cached error code so release/expiry is immediate.
func mediaWriteBacklogs(ctx context.Context, pool *pgxpool.Pool) ([]mediaWriteBacklog, error) {
	rows, err := pool.Query(ctx, `WITH writes AS (
 SELECT 'generation_output' AS kind,owner_id,created_at FROM generation_output_writes WHERE status='pending'
 UNION ALL
 SELECT 'upload_write',owner_id,created_at FROM upload_writes WHERE status='pending'
 ), eligible AS (
 SELECT w.* FROM writes w WHERE NOT EXISTS(SELECT 1 FROM data_rights_legal_holds h
 WHERE h.user_id=w.owner_id AND h.status='active' AND h.expires_at>now())
 ), grouped AS (
 SELECT kind,count(*) AS pending,
 count(*) FILTER(WHERE NOT isfinite(created_at) OR created_at>now()) AS invalid,
 COALESCE(max(extract(epoch FROM now()-created_at)) FILTER(WHERE isfinite(created_at) AND created_at<=now()),0)::float8 AS age
 FROM eligible GROUP BY kind
 )
 SELECT k.kind,COALESCE(g.pending,0),COALESCE(g.invalid,0),COALESCE(g.age,0)::float8
 FROM (VALUES('generation_output'),('upload_write')) k(kind) LEFT JOIN grouped g ON g.kind=k.kind ORDER BY k.kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []mediaWriteBacklog
	for rows.Next() {
		var m mediaWriteBacklog
		if err := rows.Scan(&m.kind, &m.pending, &m.invalid, &m.oldestAge); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
