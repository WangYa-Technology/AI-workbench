package payments

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OperationalMetric is an aggregate, never a payment identity or permission to
// move money. Kind and Mode are fixed SQL literals rather than provider data.
type OperationalMetric struct {
	Kind             string
	Mode             string
	Count            int64
	OldestAgeSeconds float64
}

type OperationalMetrics struct {
	Backlogs []OperationalMetric
	Problems []OperationalMetric
}

// ProductOperationalMetrics reads all aggregates in one database snapshot.
// Retry/update timestamps must not reset the age of an unresolved obligation.
func ProductOperationalMetrics(ctx context.Context, pool *pgxpool.Pool) (OperationalMetrics, error) {
	rows, err := pool.Query(ctx, `WITH products AS (
 SELECT pi.id,pi.order_id,pi.provider,pi.status,pi.compensation_reason,pi.created_at,pi.checkout_expires_at,pi.provider_checkout_id,pi.checkout_url,
 CASE WHEN pi.live_mode THEN 'live' ELSE 'test' END AS mode
 FROM payment_intents pi WHERE pi.purpose='product'
 ), settlements AS (
 SELECT ps.*,d.reserved_at,CASE WHEN ps.live_mode THEN 'live' ELSE 'test' END AS mode,
 pi.status AS payment_status,o.status AS order_status
 FROM product_settlements ps
 LEFT JOIN product_settlement_dispatches d ON d.settlement_id=ps.id
 JOIN payment_intents pi ON pi.id=ps.payment_id JOIN orders o ON o.id=ps.order_id
 ), unresolved_settlements AS (
 SELECT * FROM settlements WHERE status='recovery_required' OR recovery_amount_cents>0
 OR (provider_transfer_id IS NULL AND (reserved_at IS NOT NULL OR status='transfer_pending'))
 ), funding AS (
 SELECT t.*,CASE WHEN t.live_mode THEN 'live' ELSE 'test' END AS mode
 FROM seller_payout_transfers t
 ), unresolved_funding AS (
 SELECT * FROM funding WHERE status IN ('requested','processing','reconciliation_required')
 ), funding_activity_clocks AS (
 SELECT f.id,f.mode,d.created_at AS since FROM unresolved_funding f
 JOIN seller_payout_funding_dispatches d ON d.transfer_id=f.id
 UNION ALL
 SELECT f.id,f.mode,d.started_at FROM unresolved_funding f
 JOIN seller_payout_funding_dispatches d ON d.transfer_id=f.id WHERE d.started_at IS NOT NULL
 UNION ALL
 SELECT f.id,f.mode,j.updated_at FROM unresolved_funding f
 JOIN seller_payout_funding_dispatches d ON d.transfer_id=f.id JOIN jobs j ON j.id=d.job_id
 UNION ALL
 SELECT f.id,f.mode,c.created_at FROM unresolved_funding f
 JOIN seller_payout_funding_checks c ON c.transfer_id=f.id
 UNION ALL
 SELECT f.id,f.mode,j.updated_at FROM unresolved_funding f
 JOIN seller_payout_funding_checks c ON c.transfer_id=f.id JOIN jobs j ON j.id=c.job_id
 UNION ALL
 SELECT f.id,f.mode,r.started_at FROM unresolved_funding f
 JOIN seller_payout_funding_reads r ON r.transfer_id=f.id
 UNION ALL
 SELECT f.id,f.mode,r.finished_at FROM unresolved_funding f
 JOIN seller_payout_funding_reads r ON r.transfer_id=f.id WHERE r.finished_at IS NOT NULL
 ), banks AS (
 SELECT c.*,CASE WHEN c.provider_identity->>'liveMode'='true' THEN 'live' ELSE 'test' END AS mode
 FROM seller_bank_payout_commands c
 ), reversals AS (
 SELECT c.*,CASE WHEN c.live_mode THEN 'live' ELSE 'test' END AS mode
 FROM seller_source_reversal_commands c
 ), backlog AS (
 SELECT 'checkout_pending' AS kind,p.mode,p.created_at AS since
 FROM products p WHERE p.status='checkout_pending'
	 UNION ALL
 SELECT 'checkout_expired',p.mode,p.checkout_expires_at FROM products p
 WHERE p.status='checkout_open' AND p.checkout_expires_at<=now()
 UNION ALL
 SELECT 'refund_unresolved',p.mode,a.requested_at FROM products p
 JOIN product_refund_attempts a ON a.payment_id=p.id
 WHERE a.status IN ('requested','pending') OR a.reconciliation_required
 UNION ALL
 SELECT 'refund_check_due',p.mode,c.due_at FROM products p
 JOIN product_refund_reconciliation_candidates c ON c.payment_id=p.id WHERE c.due_at<=now()
 UNION ALL
 SELECT 'settlement_due',s.mode,s.available_at FROM settlements s
 WHERE s.net_amount_cents>0 AND s.provider_transfer_id IS NULL AND s.reserved_at IS NULL
 AND s.status IN ('pending_hold','available','provider_unsupported')
 AND s.payment_status='paid' AND s.order_status='fulfilled'
 AND (s.available_at<=now() OR NOT isfinite(s.available_at))
 UNION ALL
 SELECT 'settlement_unresolved',s.mode,COALESCE(s.reserved_at,s.created_at) FROM unresolved_settlements s
 UNION ALL
 SELECT 'seller_funding_unresolved',f.mode,f.reserved_at FROM unresolved_funding f
 UNION ALL
 SELECT 'seller_funding_check_due',f.mode,c.due_at FROM unresolved_funding f
 JOIN seller_payout_funding_check_candidates c ON c.transfer_id=f.id WHERE c.due_at<=now()
 UNION ALL
 SELECT 'seller_bank_unresolved',b.mode,b.created_at FROM banks b
 WHERE NOT EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=b.id AND status IN ('paid','failed','canceled'))
 UNION ALL
 SELECT 'seller_bank_check_due',b.mode,c.due_at FROM banks b JOIN seller_bank_payout_check_candidates c ON c.command_id=b.id WHERE c.due_at<=now()
 UNION ALL
 SELECT 'seller_reversal_unresolved',r.mode,r.created_at FROM reversals r
 WHERE NOT EXISTS(SELECT 1 FROM seller_source_reversal_closures x WHERE x.command_id=r.id)
 UNION ALL
 SELECT 'seller_reversal_closure_due',r.mode,result.created_at FROM reversals r
 JOIN seller_source_reversal_results result ON result.command_id=r.id
 WHERE NOT EXISTS(SELECT 1 FROM seller_source_reversal_closures x WHERE x.command_id=r.id)
 AND seller_source_reversal_return_proven(r.id)
 ), checkout_checks AS (
 SELECT DISTINCT ON (j.payload->>'paymentId') j.payload->>'paymentId' AS payment_id,j.status
 FROM jobs j WHERE j.kind='payment.check_product_checkout'
 ORDER BY j.payload->>'paymentId',j.created_at DESC,j.id DESC
 ), problems AS (
 SELECT 'event_failed' AS kind,p.mode FROM products p
 JOIN payment_provider_events e ON e.payment_id=p.id
 JOIN payment_provider_event_processing s ON s.event_id=e.id WHERE s.status='failed'
 UNION ALL
 SELECT 'checkout_check_missing',p.mode FROM products p
 JOIN product_checkout_check_candidates c ON c.payment_id=p.id WHERE c.due_at<=now()
 UNION ALL
 SELECT 'checkout_check_stopped',p.mode FROM products p
 JOIN checkout_checks last_check ON last_check.payment_id=p.id::text
 WHERE p.status='checkout_open' AND last_check.status IN ('failed','cancelled')
 AND NOT EXISTS(SELECT 1 FROM jobs active
   WHERE active.kind='payment.check_product_checkout' AND active.payload->>'paymentId'=p.id::text
   AND active.status IN ('queued','running'))
 UNION ALL
 SELECT 'refund_check_stopped',p.mode FROM products p
 JOIN LATERAL (SELECT c.status,j.status AS job_status FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id
   WHERE c.payment_id=p.id ORDER BY c.created_at DESC,c.id DESC LIMIT 1) last_check ON true
 WHERE last_check.status='failed' OR last_check.job_status IN ('failed','cancelled')
 UNION ALL
 SELECT 'closed_checkout_paid',p.mode FROM products p
 JOIN product_closed_checkout_recovery_review c ON c.payment_id=p.id
 UNION ALL
 SELECT 'checkout_evidence_conflict',p.mode FROM products p
 JOIN product_checkout_evidence_conflicts c ON c.payment_id=p.id
 UNION ALL
 SELECT 'refund_observation_unresolved',p.mode FROM products p
 JOIN product_refund_observation_review r ON r.payment_id=p.id
 UNION ALL
 SELECT 'refund_read_unrecorded',p.mode FROM products p
 WHERE EXISTS(SELECT 1 FROM product_refund_read_gaps g WHERE g.payment_id=p.id AND g.read_deadline+interval '5 seconds'<now())
 UNION ALL
 SELECT 'refund_evidence_missing',p.mode FROM products p
 WHERE p.status IN ('refund_pending','refund_failed') AND NOT EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=p.id)
 UNION ALL
 SELECT 'checkout_evidence_missing',p.mode FROM products p WHERE p.status='checkout_open'
 AND (p.checkout_expires_at IS NULL OR NOT isfinite(p.checkout_expires_at)
 OR p.provider_checkout_id IS NULL
 OR (p.checkout_url IS NULL AND NOT (
   p.provider='stripe' AND EXISTS(SELECT 1 FROM product_checkout_session_evidence e WHERE e.payment_id=p.id))))
 UNION ALL
 SELECT 'settlement_missing',p.mode FROM products p JOIN orders o ON o.id=p.order_id
 WHERE (o.status='fulfilled' OR (o.status='refund_requested' AND p.compensation_reason IS NULL)
   OR EXISTS(SELECT 1 FROM order_events e WHERE e.order_id=o.id AND e.to_status='fulfilled')
   OR EXISTS(SELECT 1 FROM entitlements e WHERE e.order_id=o.id))
 AND NOT EXISTS(SELECT 1 FROM product_settlements s WHERE s.payment_id=p.id AND s.order_id=o.id)
 UNION ALL
 SELECT 'settlement_check_stopped',s.mode FROM unresolved_settlements s
 JOIN LATERAL (SELECT j.status FROM product_settlement_checks c JOIN jobs j ON j.id=c.job_id
   WHERE c.settlement_id=s.id ORDER BY c.created_at DESC,c.id DESC LIMIT 1) last_check ON true
 WHERE s.provider_transfer_id IS NULL AND last_check.status IN ('failed','cancelled')
 AND NOT EXISTS(SELECT 1 FROM product_settlement_checks c JOIN jobs j ON j.id=c.job_id
   WHERE c.settlement_id=s.id AND j.status IN ('queued','running'))
 UNION ALL
 SELECT 'seller_funding_dispatch_missing',f.mode FROM unresolved_funding f
 WHERE NOT EXISTS(SELECT 1 FROM seller_payout_funding_dispatches d WHERE d.transfer_id=f.id)
 UNION ALL
 SELECT 'seller_funding_admission_missing',f.mode FROM funding f
 WHERE NOT EXISTS(SELECT 1 FROM seller_payout_funding_admissions a WHERE a.transfer_id=f.id)
 UNION ALL
 SELECT 'seller_funding_stopped',f.mode FROM unresolved_funding f
 JOIN seller_payout_funding_dispatches d ON d.transfer_id=f.id JOIN jobs original ON original.id=d.job_id
 WHERE original.status IN ('failed','cancelled','succeeded')
 AND NOT EXISTS(SELECT 1 FROM seller_payout_funding_checks c JOIN jobs active ON active.id=c.job_id
   WHERE c.transfer_id=f.id AND active.status IN ('queued','running'))
 UNION ALL
 SELECT 'seller_funding_review',f.mode FROM funding f
 WHERE EXISTS(SELECT 1 FROM seller_payout_funding_reads r WHERE r.transfer_id=f.id AND r.requires_review)
 UNION ALL
 SELECT 'seller_funding_read_unrecorded',f.mode FROM funding f
 WHERE EXISTS(SELECT 1 FROM seller_payout_funding_reads r WHERE r.transfer_id=f.id AND r.finished_at IS NULL
   AND (r.read_deadline IS NULL OR r.read_deadline+interval '5 seconds'<now()))
 UNION ALL
 SELECT 'seller_bank_failed',b.mode FROM banks b
 WHERE EXISTS(SELECT 1 FROM seller_bank_payout_results r WHERE r.command_id=b.id AND r.status IN ('failed','canceled'))
 UNION ALL
 SELECT 'seller_bank_review',b.mode FROM banks b
 WHERE EXISTS(SELECT 1 FROM seller_bank_payout_reads r WHERE r.command_id=b.id AND r.requires_review)
 UNION ALL
 SELECT 'seller_bank_read_unrecorded',b.mode FROM banks b
 WHERE EXISTS(SELECT 1 FROM seller_bank_payout_reads r WHERE r.command_id=b.id AND r.finished_at IS NULL AND r.deadline_at+interval '5 seconds'<now())
 UNION ALL
 SELECT 'seller_bank_dispatch_stopped',b.mode FROM banks b JOIN seller_bank_payout_active_dispatches d ON d.command_id=b.id
 JOIN jobs j ON j.id=d.execution_job_id WHERE d.started_at IS NULL AND j.status IN ('failed','cancelled','succeeded')
 UNION ALL
 SELECT 'seller_reversal_review',r.mode FROM reversals r
 WHERE EXISTS(SELECT 1 FROM seller_source_reversal_reads x WHERE x.command_id=r.id AND x.requires_review)
 UNION ALL
 SELECT 'seller_reversal_read_unrecorded',r.mode FROM reversals r
 WHERE EXISTS(SELECT 1 FROM seller_source_reversal_reads x WHERE x.command_id=r.id
   AND x.finished_at IS NULL AND x.deadline_at+interval '5 seconds'<now())
 UNION ALL
 SELECT 'seller_reversal_stopped',r.mode FROM reversals r JOIN jobs original ON original.id=r.job_id
 WHERE original.status IN ('failed','cancelled','succeeded')
 AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_results x WHERE x.command_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_closures x WHERE x.command_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_checks c JOIN jobs active ON active.id=c.job_id
   WHERE c.command_id=r.id AND active.status IN ('queued','running'))
 UNION ALL
 SELECT 'invalid_timestamp',bad.mode FROM (
   SELECT DISTINCT id,mode FROM funding_activity_clocks WHERE since IS NULL OR NOT isfinite(since) OR since>now()
 ) bad
 UNION ALL
 SELECT 'invalid_timestamp',mode FROM backlog WHERE since IS NULL OR NOT isfinite(since) OR since>now()
 ), modes(mode) AS (VALUES('live'),('test')),
 backlog_kinds(kind) AS (VALUES('checkout_pending'),('checkout_expired'),('refund_unresolved'),('refund_check_due'),('settlement_due'),('settlement_unresolved'),('seller_funding_unresolved'),('seller_funding_check_due'),('seller_bank_unresolved'),('seller_bank_check_due'),('seller_reversal_unresolved'),('seller_reversal_closure_due')),
 problem_kinds(kind) AS (VALUES('event_failed'),('checkout_check_missing'),('checkout_check_stopped'),('refund_check_stopped'),('refund_observation_unresolved'),('refund_read_unrecorded'),('refund_evidence_missing'),('checkout_evidence_missing'),('checkout_evidence_conflict'),('closed_checkout_paid'),('settlement_missing'),('settlement_check_stopped'),('seller_funding_dispatch_missing'),('seller_funding_admission_missing'),('seller_funding_stopped'),('seller_funding_review'),('seller_funding_read_unrecorded'),('seller_bank_failed'),('seller_bank_review'),('seller_bank_read_unrecorded'),('seller_bank_dispatch_stopped'),('seller_reversal_review'),('seller_reversal_read_unrecorded'),('seller_reversal_stopped'),('invalid_timestamp')),
 grouped_backlog AS (
 SELECT kind,mode,count(*) AS n,
 COALESCE(max(extract(epoch FROM now()-since)) FILTER(WHERE isfinite(since) AND since<=now()),0)::float8 AS age
 FROM backlog GROUP BY kind,mode
 ), grouped_problems AS (SELECT kind,mode,count(*) AS n FROM problems GROUP BY kind,mode)
 SELECT 'backlog',k.kind,m.mode,COALESCE(b.n,0),COALESCE(b.age,0)::float8
 FROM backlog_kinds k CROSS JOIN modes m LEFT JOIN grouped_backlog b ON b.kind=k.kind AND b.mode=m.mode
 UNION ALL
 SELECT 'problem',k.kind,m.mode,COALESCE(p.n,0),0::float8
 FROM problem_kinds k CROSS JOIN modes m LEFT JOIN grouped_problems p ON p.kind=k.kind AND p.mode=m.mode
 ORDER BY 1,2,3`)
	if err != nil {
		return OperationalMetrics{}, err
	}
	defer rows.Close()
	result := OperationalMetrics{}
	for rows.Next() {
		var category string
		var item OperationalMetric
		if err := rows.Scan(&category, &item.Kind, &item.Mode, &item.Count, &item.OldestAgeSeconds); err != nil {
			return OperationalMetrics{}, err
		}
		if category == "backlog" {
			result.Backlogs = append(result.Backlogs, item)
		} else {
			result.Problems = append(result.Problems, item)
		}
	}
	return result, rows.Err()
}
