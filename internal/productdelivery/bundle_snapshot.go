package productdelivery

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
)

// The accepted source projection is private. Rebuilding never consults today's
// listing files or asset storage locations, even if the seller changed them.
func frozenBundleSources(ctx context.Context, db Querier, order uuid.UUID) ([]BundleSource, error) {
	var body []byte
	if err := db.QueryRow(ctx, `SELECT contract FROM product_order_contracts WHERE order_id=$1`, order).Scan(&body); err != nil {
		return nil, err
	}
	var contract struct {
		Product struct {
			SellerID      uuid.UUID `json:"sellerId"`
			IncludedFiles []string  `json:"includedFiles"`
		} `json:"product"`
		Asset struct {
			ID, RootID                 uuid.UUID
			StorageBackend, StorageKey string
		} `json:"asset"`
		Delivery struct {
			Format string `json:"format"`
			Files  []struct {
				Position int       `json:"position"`
				AssetID  uuid.UUID `json:"assetId"`
				Name     string    `json:"name"`
				Source   struct {
					OwnerID                                                      uuid.UUID  `json:"ownerId"`
					OriginAssetID                                                *uuid.UUID `json:"originAssetId"`
					MIMEType, StorageBackend, StorageKey, SourceType, ScanStatus string
				} `json:"source"`
			} `json:"files"`
		} `json:"delivery"`
	}
	if json.Unmarshal(body, &contract) != nil || contract.Delivery.Format != FormatZIPV1 || contract.Product.SellerID == uuid.Nil {
		return nil, ErrBundleInvalid
	}
	files := contract.Delivery.Files
	if len(files) < 2 || len(files) > MaxBundleFiles || len(contract.Product.IncludedFiles) != len(files) || files[0].AssetID != contract.Asset.ID || contract.Asset.RootID != contract.Asset.ID {
		return nil, ErrBundleInvalid
	}
	seen := map[uuid.UUID]bool{}
	names := make([]string, len(files))
	sources := make([]BundleSource, len(files))
	for i, f := range files {
		if f.Position != i || f.AssetID == uuid.Nil || seen[f.AssetID] || f.Name != contract.Product.IncludedFiles[i] || f.Source.OwnerID != contract.Product.SellerID || f.Source.OriginAssetID != nil || f.Source.ScanStatus != "clean" || (f.Source.SourceType != "upload" && f.Source.SourceType != "generation") {
			return nil, ErrBundleInvalid
		}
		seen[f.AssetID] = true
		names[i] = f.Name
		sources[i] = BundleSource{Name: f.Name, MIMEType: f.Source.MIMEType, Backend: f.Source.StorageBackend, Key: f.Source.StorageKey}
	}
	if sources[0].Backend != contract.Asset.StorageBackend || sources[0].Key != contract.Asset.StorageKey {
		return nil, ErrBundleInvalid
	}
	if err := ValidateBundleNames(names); err != nil {
		return nil, err
	}
	return sources, nil
}

func reserveBundleTx(ctx context.Context, tx pgx.Tx, stores *media.Catalog, order uuid.UUID) error {
	sources, err := frozenBundleSources(ctx, tx, order)
	if err != nil {
		return err
	}
	stage, manifest, err := BuildBundle(ctx, stores, sources)
	if err != nil {
		return err
	}
	defer stage.Close()
	destination := stores.Primary()
	key, err := destination.ObjectKey("delivery-" + order.String() + "-" + uuid.NewString() + ".zip")
	if err != nil {
		return err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_delivery_snapshots(order_id,source_backend,source_key,storage_backend,storage_key,sha256,size_bytes,mime_type,format,file_manifest)
 VALUES($1,NULL,NULL,$2,$3,$4,$5,$6,'zip-v1',$7)`, order, destination.Backend(), key, stage.SHA256, stage.Size, BundleMIME, body)
	return err
}

func (s Snapshot) stageOriginal(ctx context.Context, db Querier, stores *media.Catalog) (*media.StagedObject, error) {
	if s.Format == FormatSingle {
		return repairSource(ctx, stores, s.SourceBackend, s.SourceKey, s)
	}
	if s.Format != FormatZIPV1 || s.Manifest == nil {
		return nil, ErrUnavailable
	}
	sources, err := frozenBundleSources(ctx, db, s.OrderID)
	if err != nil {
		return nil, err
	}
	stage, manifest, err := BuildBundle(ctx, stores, sources)
	if err != nil {
		return nil, err
	}
	if stage.Size != s.Size || stage.SHA256 != s.SHA256 || !reflect.DeepEqual(manifest, *s.Manifest) {
		stage.Close()
		return nil, media.ErrIntegrity
	}
	return stage, nil
}
