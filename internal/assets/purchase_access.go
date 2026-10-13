package assets

import (
	"context"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/productpolicy"
	"github.com/jackc/pgx/v5"
)

// PurchaseAccess is a current capability projection, never a download grant.
// Downloads and creation independently recheck the same policy at execution.
type PurchaseAccess struct {
	OrderID     *uuid.UUID `json:"orderId,omitempty"`
	CanDownload bool       `json:"canDownload"`
	CanReuse    bool       `json:"canReuse"`
	Status      string     `json:"status"`
}

type purchaseQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// One query for the whole page; no per-card metadata or storage requests.
func populatePurchaseAccess(ctx context.Context, db purchaseQueryer, ownerID uuid.UUID, items []Asset) error {
	ids := make([]uuid.UUID, 0, len(items))
	positions := make(map[uuid.UUID]int)
	for i := range items {
		if items[i].SourceType == "purchase" {
			ids = append(ids, items[i].ID)
			positions[items[i].ID] = i
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := db.Query(ctx, `SELECT a.id,o.id,COALESCE(o.status,''),a.scan_status,
 EXISTS(SELECT 1 FROM entitlements e WHERE e.asset_id=a.id AND e.user_id=a.owner_id AND e.status='active'),
 `+productpolicy.PurchaseAssetAccessSQL+`, `+productpolicy.PurchaseAssetReuseSQL+`
 FROM assets a LEFT JOIN orders o ON o.id=a.source_id AND o.buyer_id=a.owner_id
 WHERE a.owner_id=$1 AND a.id=ANY($2) AND a.source_type='purchase'`, ownerID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var access PurchaseAccess
		var orderStatus, scan string
		var active bool
		if err := rows.Scan(&id, &access.OrderID, &orderStatus, &scan, &active, &access.CanDownload, &access.CanReuse); err != nil {
			return err
		}
		switch {
		case !active || orderStatus == "refunded" || orderStatus == "test_refunded":
			access.Status = "revoked"
		case scan != "clean":
			access.Status = "scan_blocked"
		case !access.CanDownload:
			access.Status = "unavailable"
		case orderStatus == "refund_requested":
			access.Status = "refund_pending"
		default:
			access.Status = "available"
		}
		items[positions[id]].PurchaseAccess = &access
	}
	return rows.Err()
}
