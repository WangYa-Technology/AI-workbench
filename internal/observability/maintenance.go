package observability

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	UploadWriteCleanup                 = "upload_write_cleanup"
	AssetScanExecutionRecovery         = "asset_scan_execution_recovery"
	GenerationExecutionRecovery        = "generation_execution_recovery"
	GenerationOutputCleanup            = "generation_output_cleanup"
	LegalHoldExpiry                    = "legal_hold_expiry"
	LegalHoldCleanup                   = "legal_hold_cleanup"
	ProductCleanupReconciliation       = "product_cleanup_reconciliation"
	AccountDeletionReconciliation      = "account_deletion_reconciliation"
	OriginalMediaCleanupReconciliation = "original_media_cleanup_reconciliation"
	ProductRefundReconciliation        = "product_refund_reconciliation"
	ProductCheckoutReconciliation      = "product_checkout_reconciliation"
	ProductSettlementReconciliation    = "product_settlement_reconciliation"
	SellerFundingReconciliation        = "seller_funding_reconciliation"
	SellerBankReconciliation           = "seller_bank_reconciliation"
	SellerReversalReconciliation       = "seller_reversal_reconciliation"
)

var maintenanceKinds = []string{
	LegalHoldExpiry, LegalHoldCleanup, ProductCleanupReconciliation,
	ProductCheckoutReconciliation,
	ProductSettlementReconciliation,
	SellerFundingReconciliation,
	SellerBankReconciliation,
	SellerReversalReconciliation,
	AccountDeletionReconciliation, OriginalMediaCleanupReconciliation, ProductRefundReconciliation, GenerationOutputCleanup, GenerationExecutionRecovery, AssetScanExecutionRecovery, UploadWriteCleanup,
}

// RecordMaintenance records an observed completion, not the completion of any
// financial or deletion obligation. One bounded row is shared by all workers.
// Atomic increments preserve concurrent results, and timestamps never regress
// when a writer waits on the row lock behind a newer observation.
func (r *Repository) RecordMaintenance(ctx context.Context, kind string, succeeded bool) error {
	if !slices.Contains(maintenanceKinds, kind) {
		return errors.New("invalid maintenance kind")
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO maintenance_health
 (kind,passes,failures,last_failed,completed_at,last_success_at,last_failure_at)
 VALUES($1,1,CASE WHEN $2 THEN 0 ELSE 1 END,NOT $2,statement_timestamp(),
 CASE WHEN $2 THEN statement_timestamp() END,CASE WHEN NOT $2 THEN statement_timestamp() END)
 ON CONFLICT(kind) DO UPDATE SET
 passes=maintenance_health.passes+1,
 failures=maintenance_health.failures+CASE WHEN $2 THEN 0 ELSE 1 END,
 last_failed=CASE WHEN statement_timestamp()>=maintenance_health.completed_at THEN NOT $2 ELSE maintenance_health.last_failed END,
 completed_at=GREATEST(maintenance_health.completed_at,statement_timestamp()),
 last_success_at=CASE WHEN $2 THEN GREATEST(maintenance_health.last_success_at,statement_timestamp()) ELSE maintenance_health.last_success_at END,
 last_failure_at=CASE WHEN NOT $2 THEN GREATEST(maintenance_health.last_failure_at,statement_timestamp()) ELSE maintenance_health.last_failure_at END`, kind, succeeded)
	return err
}

type MaintenanceMetric struct {
	Kind                                       string
	Passes, Failures                           int64
	SuccessSeen, LastFailed, InvalidTimestamps bool
	SuccessAgeSeconds                          float64
}

func maintenanceMetrics(ctx context.Context, pool *pgxpool.Pool) ([]MaintenanceMetric, error) {
	rows, err := pool.Query(ctx, `SELECT k.kind,COALESCE(h.passes,0),COALESCE(h.failures,0),
 h.last_success_at IS NOT NULL,COALESCE(h.last_failed,false),
 COALESCE(h.completed_at>now() OR h.last_success_at>now() OR h.last_failure_at>now(),false),
 COALESCE(GREATEST(0,extract(epoch FROM now()-h.last_success_at)),0)::float8
 FROM unnest($1::text[]) k(kind) LEFT JOIN maintenance_health h ON h.kind=k.kind ORDER BY k.kind`, maintenanceKinds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []MaintenanceMetric
	for rows.Next() {
		var item MaintenanceMetric
		if err := rows.Scan(&item.Kind, &item.Passes, &item.Failures, &item.SuccessSeen, &item.LastFailed, &item.InvalidTimestamps, &item.SuccessAgeSeconds); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
