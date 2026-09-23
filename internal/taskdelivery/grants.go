// Package taskdelivery owns the immutable handover shared by acceptance and arbitration.
package taskdelivery

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrUnavailable = errors.New("delivery source unavailable")

// GrantTx runs in the acceptance transaction. Copies point at immutable source bytes;
// the grant snapshots the contract and defaults to no derivative reuse or resale.
func GrantTx(ctx context.Context, tx pgx.Tx, deliveryID uuid.UUID) error {
	// Recheck and lock all source rows at acceptance, including every attachment.
	rows, err := tx.Query(ctx, `SELECT a.scan_status,a.owner_id=dl.creator_id FROM deliveries dl JOIN assets a
 ON a.id=dl.asset_id OR EXISTS(SELECT 1 FROM delivery_assets da WHERE da.delivery_id=dl.id AND da.asset_id=a.id)
 WHERE dl.id=$1 FOR SHARE OF a`, deliveryID)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var status string
		var owner bool
		if err = rows.Scan(&status, &owner); err != nil {
			rows.Close()
			return err
		}
		if status != "clean" || !owner {
			rows.Close()
			return ErrUnavailable
		}
		count++
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if count == 0 {
		return ErrUnavailable
	}
	_, err = tx.Exec(ctx, `
 WITH source_ids AS (
   SELECT da.asset_id,da.position FROM delivery_assets da WHERE da.delivery_id=$1
   UNION ALL
   SELECT dl.asset_id,0 FROM deliveries dl WHERE dl.id=$1
     AND NOT EXISTS(SELECT 1 FROM delivery_assets WHERE delivery_id=$1)
 ), sources AS MATERIALIZED (SELECT asset_id,gen_random_uuid() AS copy_id FROM source_ids), granted AS (
   INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,source_id,license_code,origin_asset_id)
   SELECT src.copy_id,d.client_id,a.kind,a.title,a.media_url,a.mime_type,a.width,a.height,a.scan_status,
          'delivery',dl.id,'task-contract',COALESCE(a.origin_asset_id,a.id)
   FROM deliveries dl JOIN demands d ON d.id=dl.demand_id
   JOIN sources src ON true JOIN assets a ON a.id=src.asset_id
   WHERE dl.id=$1 AND dl.status='accepted'
     AND NOT EXISTS(SELECT 1 FROM task_delivery_grants g WHERE g.delivery_id=dl.id AND g.source_asset_id=a.id)
   RETURNING id,source_id,owner_id,origin_asset_id
 )
 INSERT INTO task_delivery_grants(demand_id,delivery_id,source_asset_id,asset_id,client_id,creator_id,rights_terms,rights_evidence,ai_disclosure,allow_derivative_reuse)
 SELECT d.id,dl.id,a.id,g.id,d.client_id,dl.creator_id,d.rights_terms,dl.rights_evidence,dl.ai_disclosure,d.allow_derivative_reuse
 FROM granted g JOIN deliveries dl ON dl.id=g.source_id JOIN demands d ON d.id=dl.demand_id
 JOIN sources src ON src.copy_id=g.id JOIN assets a ON a.id=src.asset_id`, deliveryID)
	if err != nil {
		return err
	}
	// Each copy needs its own authenticated content URL, never the creator's private URL.
	_, err = tx.Exec(ctx, `UPDATE assets SET media_url='/api/v1/assets/'||id::text||'/content'
 WHERE id IN (SELECT asset_id FROM task_delivery_grants WHERE delivery_id=$1)`, deliveryID)
	return err
}

type Grant struct {
	TaskID               uuid.UUID `json:"taskId"`
	DeliveryID           uuid.UUID `json:"deliveryId"`
	RightsTerms          string    `json:"rightsTerms"`
	RightsEvidence       string    `json:"rightsEvidence"`
	AIDisclosure         string    `json:"aiDisclosure"`
	AllowDerivativeReuse bool      `json:"allowDerivativeReuse"`
}
