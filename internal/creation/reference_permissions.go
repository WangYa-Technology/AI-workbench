package creation

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/productpolicy"
	"github.com/jackc/pgx/v5"
)

type referenceQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Keep submission, retries and worker reads on the same permission policy.
// Parameters are owner, asset IDs and supported media kinds, in that order.
const referenceAssetPermissionSQL = `
	a.owner_id=$1 AND a.id=ANY($2) AND a.scan_status='clean' AND a.kind=ANY($3)
	AND EXISTS(SELECT 1 FROM users reference_owner WHERE reference_owner.id=a.owner_id AND reference_owner.status='active')
	AND (a.origin_asset_id IS NULL OR EXISTS(SELECT 1 FROM assets origin WHERE origin.id=a.origin_asset_id AND origin.scan_status='clean'))
	AND (a.license_code<>'task-contract' OR EXISTS(SELECT 1 FROM task_delivery_grants tg WHERE tg.asset_id=a.id AND tg.client_id=$1 AND tg.allow_derivative_reuse))
	AND (a.source_type<>'purchase' OR ` + productpolicy.PurchaseAssetReuseSQL + `)`

// This must also run immediately before dispatch: rights may be revoked while
// a job is queued, retried by the worker, or reading its reference files.
func validateReferenceAssets(ctx context.Context, db referenceQuerier, ownerID uuid.UUID, input SubmitInput) error {
	check := func(ids []uuid.UUID, kinds []string) error {
		if len(ids) == 0 {
			return nil
		}
		var count int
		err := db.QueryRow(ctx, `SELECT count(*) FROM assets a WHERE `+referenceAssetPermissionSQL,
			ownerID, ids, kinds).Scan(&count)
		if err != nil {
			return fmt.Errorf("check reference permissions: %w", err)
		}
		if count != len(ids) {
			return ErrInvalid
		}
		return nil
	}
	if err := check(input.SourceAssetIDs, referenceKindsForMode(input.Mode)); err != nil {
		return err
	}
	if input.MaskAssetID != nil {
		if input.Mode != "image" || len(input.SourceAssetIDs) == 0 || *input.MaskAssetID == uuid.Nil {
			return ErrInvalid
		}
		return check([]uuid.UUID{*input.MaskAssetID}, []string{"image"})
	}
	return nil
}
