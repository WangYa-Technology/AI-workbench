package marketplace

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrInvalidPreview = errors.New("invalid product preview")
var ErrPreviewConflict = errors.New("product offer changed")

type PreviewUpdate struct {
	PreviewAssetID *uuid.UUID `json:"previewAssetId"`
	OfferVersion   string     `json:"offerVersion"`
}

// SetPreview makes one deliberately selected sample public, never the source.
func (s *Service) SetPreview(ctx context.Context, actor, product uuid.UUID, input PreviewUpdate, requestID string) (_ PreviewUpdate, err error) {
	defer func() {
		var conflict *pgconn.PgError
		if errors.As(err, &conflict) && (conflict.Code == "40001" || conflict.Code == "40P01") {
			err = ErrPreviewConflict
		}
	}()
	decoded, err := hex.DecodeString(input.OfferVersion)
	if actor == uuid.Nil || product == uuid.Nil || err != nil || len(decoded) != 32 || input.OfferVersion != strings.ToLower(input.OfferVersion) || (input.PreviewAssetID != nil && *input.PreviewAssetID == uuid.Nil) {
		return PreviewUpdate{}, ErrInvalidPreview
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PreviewUpdate{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Publishing); err != nil {
		return PreviewUpdate{}, err
	}
	var owner uuid.UUID
	var version string
	var previous *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT p.seller_id,p.preview_asset_id,o.offer_version FROM products p JOIN product_offers o ON o.product_id=p.id WHERE p.id=$1 FOR UPDATE OF p`, product).Scan(&owner, &previous, &version)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != actor) {
		return PreviewUpdate{}, ErrNotFound
	}
	if err != nil {
		return PreviewUpdate{}, err
	}
	// Managed listings use the versioned editor and review workflow. The legacy
	// sample endpoint must never bypass that publication gate.
	var managed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_publications WHERE product_id=$1)`, product).Scan(&managed); err != nil {
		return PreviewUpdate{}, err
	}
	if managed {
		return PreviewUpdate{}, ErrPreviewConflict
	}
	if version != input.OfferVersion {
		return PreviewUpdate{}, ErrPreviewConflict
	}
	if input.PreviewAssetID != nil {
		var allowed bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_preview_candidates candidate WHERE candidate.asset_id=a.id AND candidate.owner_id=$2)
		 FROM assets a WHERE a.id=$1 FOR SHARE OF a`, input.PreviewAssetID, actor).Scan(&allowed)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !allowed) {
			return PreviewUpdate{}, ErrInvalidPreview
		}
		if err != nil {
			return PreviewUpdate{}, err
		}
	}
	// Retrying an already applied selection has no additional publication/audit effect.
	if (previous == nil && input.PreviewAssetID != nil) || (previous != nil && (input.PreviewAssetID == nil || *previous != *input.PreviewAssetID)) {
		if _, err = tx.Exec(ctx, `UPDATE products SET preview_asset_id=$2 WHERE id=$1`, product, input.PreviewAssetID); err != nil {
			return PreviewUpdate{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
		 VALUES($1,'marketplace.preview_updated','product',$2,'Seller updated the public product sample',$3,jsonb_build_object('previousAssetId',$4::text,'previewAssetId',$5::text))`, actor, product, requestID, previous, input.PreviewAssetID); err != nil {
			return PreviewUpdate{}, err
		}
	}
	if err = tx.QueryRow(ctx, `SELECT offer_version FROM product_offers WHERE product_id=$1`, product).Scan(&input.OfferVersion); err != nil {
		return PreviewUpdate{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PreviewUpdate{}, err
	}
	return input, nil
}
