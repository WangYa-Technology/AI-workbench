package productdelivery

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SourcesClean checks the accepted sources, never today's mutable product file
// list. Removing originals after independent delivery must preserve their scan
// metadata; a missing member cannot be treated as a clean one.
func SourcesClean(ctx context.Context, db Querier, order uuid.UUID) (bool, error) {
	var clean bool
	err := db.QueryRow(ctx, `SELECT count(*)>0 AND COALESCE(bool_and(
 a.id IS NOT NULL AND root.id IS NOT NULL AND a.scan_status='clean' AND root.scan_status='clean'),false)
 FROM product_order_media_sources s
 LEFT JOIN assets a ON a.id=s.source_asset_id
 LEFT JOIN assets root ON root.id=s.root_asset_id
 WHERE s.order_id=$1`, order).Scan(&clean)
	return clean, err
}

// LockSources serializes new fulfillment with scans and original cleanup.
// The caller already holds the payment/order and buyer locks. Do not lock a
// mutable listing here: an accepted order may outlive the seller's listing.
func LockSources(ctx context.Context, tx pgx.Tx, order uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT a.id FROM assets a WHERE a.id IN (
 SELECT source_asset_id FROM product_order_media_sources WHERE order_id=$1
 UNION SELECT root_asset_id FROM product_order_media_sources WHERE order_id=$1
 ) ORDER BY a.id FOR UPDATE OF a`, order)
	return err
}
