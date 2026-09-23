package datarights

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RecoveryFailureCounts counts failed recovery-chain leaves, including failures
// that need manual investigation rather than retry permission. Media failures
// need only the retry linkage and actual removal state, not the operations
// queue's full financial, retention and retry-eligibility projection. Historical
// predecessors and explicitly completed/closed subjects are excluded. Counts
// are not permission to retry or proof that an operation has completed.
func RecoveryFailureCounts(ctx context.Context, pool *pgxpool.Pool) (map[string]int64, error) {
	rows, err := pool.Query(ctx, `WITH failures AS (
 SELECT CASE WHEN j.kind='data_rights.media_cleanup' THEN 'media_account' ELSE 'media_product' END AS kind
 FROM jobs j
 LEFT JOIN orders o ON j.kind='product.delivery_cleanup' AND o.id::text=j.payload->>'orderId'
 LEFT JOIN product_delivery_snapshots d ON d.order_id=o.id
 WHERE j.kind IN ('data_rights.media_cleanup','product.delivery_cleanup') AND j.status='failed'
 AND NOT EXISTS(SELECT 1 FROM media_cleanup_recoveries r WHERE r.original_job_id=j.id)
 AND (d.order_id IS NULL OR d.state<>'removed')
 UNION ALL
 SELECT 'export_'||kind FROM data_export_job_policy
 WHERE status='failed' AND retry_job_id IS NULL AND unavailable_reason NOT IN ('request_closed','already_purged')
 UNION ALL
 SELECT 'account_deletion' FROM account_deletion_job_policy
 WHERE status='failed' AND retry_job_id IS NULL AND unavailable_reason<>'request_closed'
 ) SELECT kind,count(*) FROM failures GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int64{
		"media_account": 0, "media_product": 0, "export_export": 0,
		"export_expiry": 0, "account_deletion": 0,
	}
	for rows.Next() {
		var kind string
		var count int64
		if err := rows.Scan(&kind, &count); err != nil {
			return nil, err
		}
		counts[kind] = count
	}
	return counts, rows.Err()
}
