package creation

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ExecutionRecoveryMetrics struct {
	Failed, Due, Unresolved int64
	OldestDueAge            float64
}

// Unresolved includes missing bindings and terminal/malformed job evidence
// which automatic recovery cannot safely interpret. It is not a license to
// release a reservation or redispatch the external request.
func ExecutionMetrics(ctx context.Context, pool *pgxpool.Pool) (ExecutionRecoveryMetrics, error) {
	var m ExecutionRecoveryMetrics
	err := pool.QueryRow(ctx, `WITH pending AS (
 SELECT e.recovery_after,e.last_error_code,
 COALESCE(`+failedExecutionPredicate+`,false) AS recoverable,
 e.job_id IS NULL OR j.status IN ('failed','cancelled','succeeded') AS suspect
 FROM generations g LEFT JOIN generation_executions e ON e.generation_id=g.id
 LEFT JOIN jobs j ON j.id=e.job_id WHERE g.status IN ('queued','running'))
 SELECT count(*) FILTER(WHERE last_error_code IS NOT NULL),
 count(*) FILTER(WHERE recoverable AND recovery_after<=now()),
 count(*) FILTER(WHERE suspect AND NOT recoverable),
 COALESCE(max(extract(epoch FROM now()-recovery_after)) FILTER(WHERE recoverable AND recovery_after<=now()),0)::float8
 FROM pending`).Scan(&m.Failed, &m.Due, &m.Unresolved, &m.OldestDueAge)
	return m, err
}
