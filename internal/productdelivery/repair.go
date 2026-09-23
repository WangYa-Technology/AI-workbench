package productdelivery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrRepairForbidden      = errors.New("delivery repair forbidden")
	ErrRepairInvalid        = errors.New("invalid delivery repair")
	ErrRepairConflict       = errors.New("delivery repair changed or is ineligible")
	ErrRepairNotFound       = errors.New("delivery snapshot not found")
	ErrRepairUploadRequired = errors.New("delivery repair requires upload bytes")
	ErrRepairBusy           = errors.New("delivery repair upload capacity reached")
)

type RepairService struct {
	pool        *pgxpool.Pool
	stores      *media.Catalog
	uploadSlots chan struct{}
}

func NewRepairService(pool *pgxpool.Pool, stores *media.Catalog) *RepairService {
	return &RepairService{pool: pool, stores: stores, uploadSlots: make(chan struct{}, 2)}
}

type RepairInput struct {
	ExpectedRevision *int       `json:"expectedRevision"`
	SourceAssetID    *uuid.UUID `json:"sourceAssetId,omitempty"`
	Reason           string     `json:"reason"`
	Confirmed        bool       `json:"confirmed"`
}
type RepairStatus struct {
	OrderID       uuid.UUID  `json:"orderId"`
	Title         string     `json:"title"`
	SHA256        string     `json:"sha256"`
	SizeBytes     int64      `json:"sizeBytes"`
	State         string     `json:"state"`
	Revision      int        `json:"revision"`
	Needed        bool       `json:"needed"`
	Health        string     `json:"health"`
	CanRepair     bool       `json:"canRepair"`
	PendingID     *uuid.UUID `json:"pendingId,omitempty"`
	PendingSource string     `json:"pendingSource,omitempty"`
	RepairedAt    *time.Time `json:"repairedAt,omitempty"`
}

func repairPermission(ctx context.Context, db Querier, actor uuid.UUID) error {
	var ok bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN role_permissions rp ON rp.role=u.role WHERE u.id=$1 AND u.status='active' AND rp.permission_id='admin:media')`, actor).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrRepairForbidden
	}
	return nil
}

// Source/target verification can be slow. Recheck after it and pin the active
// operator plus role permission before reserving evidence or writing bytes.
// Later revocations wait until this already-authorized transaction finishes.
func pinRepairPermission(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT u.id FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=$1 AND u.status='active' AND rp.permission_id='admin:media' FOR SHARE OF u,rp`, actor).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRepairForbidden
	}
	return err
}

func repairCommandError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
		return ErrRepairConflict
	}
	return err
}
func repairState(ctx context.Context, db Querier, order uuid.UUID) (RepairStatus, error) {
	var item RepairStatus
	err := db.QueryRow(ctx, `SELECT o.id,o.product_title_snapshot,d.sha256,d.size_bytes,d.state,p.needed,
 COALESCE(r.revision,0),CASE WHEN r.state='prepared' THEN r.id ELSE NULL END,
 CASE WHEN r.state='prepared' THEN r.source_kind ELSE '' END,
 (SELECT ready_at FROM product_delivery_repairs WHERE order_id=o.id AND state='ready' ORDER BY revision DESC LIMIT 1)
 FROM product_delivery_snapshots d JOIN orders o ON o.id=d.order_id JOIN product_delivery_cleanup_policy p ON p.order_id=o.id
 LEFT JOIN LATERAL(SELECT id,revision,state,ready_at,source_kind FROM product_delivery_repairs WHERE order_id=o.id ORDER BY revision DESC LIMIT 1) r ON true
 WHERE o.id=$1`, order).Scan(&item.OrderID, &item.Title, &item.SHA256, &item.SizeBytes, &item.State, &item.Needed, &item.Revision, &item.PendingID, &item.PendingSource, &item.RepairedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrRepairNotFound
	}
	return item, err
}
func checkSnapshot(ctx context.Context, snapshot Snapshot, stores *media.Catalog) error {
	obj, err := snapshot.Open(ctx, stores, nil)
	if err != nil {
		return err
	}
	return obj.Body.Close()
}
func (s *RepairService) Inspect(ctx context.Context, actor, order uuid.UUID) (RepairStatus, error) {
	var item RepairStatus
	if err := repairPermission(ctx, s.pool, actor); err != nil {
		return item, err
	}
	item, err := repairState(ctx, s.pool, order)
	if err != nil {
		return item, err
	}
	snapshot, err := Load(ctx, s.pool, order)
	if err != nil {
		return item, err
	}
	err = checkSnapshot(ctx, snapshot, s.stores)
	if errors.Is(err, media.ErrStageBusy) || errors.Is(err, media.ErrStageStorage) {
		return item, err
	}
	switch {
	case err == nil:
		item.Health = "healthy"
	case errors.Is(err, media.ErrNotFound):
		item.Health = "missing"
	case errors.Is(err, media.ErrIntegrity):
		item.Health = "corrupt"
	default:
		item.Health = "unavailable"
	}
	item.CanRepair = item.State == "ready" && item.Needed && (item.Health == "missing" || item.Health == "corrupt")
	if err := repairPermission(ctx, s.pool, actor); err != nil {
		return RepairStatus{}, err
	}
	return item, nil
}

// Repair reserves evidence before writing, then completes that exact reservation.
// The default source is the frozen original location. An optional backup must
// be a clean, independent upload owned by the operator, and match every byte.
func (s *RepairService) Repair(ctx context.Context, actor, order uuid.UUID, key, requestID string, in RepairInput) (RepairStatus, error) {
	item, err := s.prepare(ctx, actor, order, key, requestID, in, false)
	return item, repairCommandError(err)
}

func (s *RepairService) PrepareUpload(ctx context.Context, actor, order uuid.UUID, key, requestID string, in RepairInput) (RepairStatus, error) {
	if in.SourceAssetID != nil {
		return RepairStatus{}, ErrRepairInvalid
	}
	item, err := s.prepare(ctx, actor, order, key, requestID, in, true)
	return item, repairCommandError(err)
}

func (s *RepairService) prepare(ctx context.Context, actor, order uuid.UUID, key, requestID string, in RepairInput, upload bool) (RepairStatus, error) {
	var empty RepairStatus
	in.Reason = strings.TrimSpace(in.Reason)
	if actor == uuid.Nil || order == uuid.Nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 0 || *in.ExpectedRevision >= math.MaxInt32 || !in.Confirmed || len(key) < 8 || len(key) > 128 || strings.TrimSpace(key) != key || !utf8.ValidString(in.Reason) || strings.ContainsRune(in.Reason, 0) || utf8.RuneCountInString(in.Reason) < 10 || utf8.RuneCountInString(in.Reason) > 2000 || (in.SourceAssetID != nil && *in.SourceAssetID == uuid.Nil) {
		return empty, ErrRepairInvalid
	}
	raw, _ := json.Marshal(struct {
		Order uuid.UUID
		Input RepairInput
	}{order, in})
	// Preserve existing stored-source command hashes across this migration.
	if upload {
		raw = append([]byte("delivery-upload-v1:"), raw...)
	}
	requestHash := fmt.Sprintf("%x", sha256.Sum256(raw))
	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	if err = repairPermission(ctx, tx, actor); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,95))`, actor.String()+":"+keyHash); err != nil {
		return empty, err
	}
	var repairID uuid.UUID
	var savedHash string
	err = tx.QueryRow(ctx, `SELECT id,request_sha256 FROM product_delivery_repairs WHERE actor_id=$1 AND key_sha256=$2`, actor, keyHash).Scan(&repairID, &savedHash)
	if err == nil {
		if savedHash != requestHash {
			return empty, ErrRepairConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return empty, err
		}
		if upload {
			return s.inspectUploadReservation(ctx, actor, order, repairID, *in.ExpectedRevision+1)
		}
		return s.Resume(ctx, actor, order, repairID, requestID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, err
	}
	if err = LockCleanupTx(ctx, tx, order); err != nil {
		return empty, err
	}
	if err = repairPermission(ctx, tx, actor); err != nil {
		return empty, err
	}
	current, err := repairState(ctx, tx, order)
	if err != nil {
		return empty, err
	}
	if current.Revision != *in.ExpectedRevision || current.State != "ready" || !current.Needed {
		return empty, ErrRepairConflict
	}
	snapshot, err := Load(ctx, tx, order)
	if err != nil {
		return empty, err
	}
	damaged := checkSnapshot(ctx, snapshot, s.stores)
	if !errors.Is(damaged, media.ErrNotFound) && !errors.Is(damaged, media.ErrIntegrity) {
		return empty, ErrRepairConflict
	}
	sourceBackend, sourceKey := snapshot.SourceBackend, snapshot.SourceKey
	bundle := !upload && in.SourceAssetID == nil && snapshot.Format == FormatZIPV1
	if in.SourceAssetID != nil {
		sourceBackend, sourceKey, err = lockRepairBackup(ctx, tx, *in.SourceAssetID, actor)
		if err != nil {
			return empty, err
		}
	}
	if !upload {
		var stage *media.StagedObject
		var e error
		if bundle {
			stage, e = snapshot.stageOriginal(ctx, tx, s.stores)
		} else {
			stage, e = repairSource(ctx, s.stores, sourceBackend, sourceKey, snapshot)
		}
		if e != nil {
			return empty, e
		}
		defer stage.Close()
	}
	if s.stores == nil {
		return empty, ErrUnavailable
	}
	repairID = uuid.New()
	destination := s.stores.Primary()
	targetKey, err := destination.ObjectKey("delivery-repair-" + repairID.String() + ".bin")
	if err != nil {
		return empty, err
	}
	kind := "stored"
	var fromBackend, fromKey any = sourceBackend, sourceKey
	if upload {
		kind, fromBackend, fromKey = "upload", nil, nil
	} else if bundle {
		kind, fromBackend, fromKey = "bundle", nil, nil
	}
	if err = pinRepairPermission(ctx, tx, actor); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO product_delivery_repairs(id,order_id,revision,actor_id,key_sha256,request_sha256,source_asset_id,source_backend,source_key,storage_backend,storage_key,reason,source_kind)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, repairID, order, current.Revision+1, actor, keyHash, requestHash, in.SourceAssetID, fromBackend, fromKey, destination.Backend(), targetKey, in.Reason, kind); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,'marketplace.delivery_repair_requested','order',$2,$3,$4,jsonb_build_object('repairId',$5::text,'revision',$6::integer))`, actor, order, in.Reason, requestID, repairID, current.Revision+1); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	if upload {
		return s.inspectUploadReservation(ctx, actor, order, repairID, current.Revision+1)
	}
	return s.Resume(ctx, actor, order, repairID, requestID)
}

// Never return a later command's pending ID after a committed reservation.
// The upload endpoint independently checks the revision again before writing.
func (s *RepairService) inspectUploadReservation(ctx context.Context, actor, order, repair uuid.UUID, revision int) (RepairStatus, error) {
	status, err := s.Inspect(ctx, actor, order)
	if err != nil {
		return RepairStatus{}, err
	}
	if status.Revision != revision || (status.PendingID != nil && *status.PendingID != repair) {
		return RepairStatus{}, ErrRepairConflict
	}
	return status, nil
}

func repairSource(ctx context.Context, stores *media.Catalog, backend, key string, snapshot Snapshot) (*media.StagedObject, error) {
	if stores == nil {
		return nil, ErrUnavailable
	}
	store, err := stores.Get(backend)
	if err != nil {
		return nil, err
	}
	object, err := store.Open(ctx, key, nil)
	if err != nil {
		return nil, err
	}
	stage, readErr := stores.Stage(ctx, object.Body, snapshot.Size)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil {
		if stage != nil {
			stage.Close()
		}
		return nil, errors.Join(readErr, closeErr)
	}
	if stage.Size != snapshot.Size || stage.SHA256 != snapshot.SHA256 {
		stage.Close()
		return nil, media.ErrIntegrity
	}
	return stage, nil
}
func (s *RepairService) Resume(ctx context.Context, actor, order, repairID uuid.UUID, requestID string) (RepairStatus, error) {
	item, err := s.resume(ctx, actor, order, repairID, requestID)
	return item, repairCommandError(err)
}

func (s *RepairService) resume(ctx context.Context, actor, order, repairID uuid.UUID, requestID string) (RepairStatus, error) {
	var empty RepairStatus
	if s.stores == nil {
		return empty, ErrUnavailable
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	if err = repairPermission(ctx, tx, actor); err != nil {
		return empty, err
	}
	if err = LockCleanupTx(ctx, tx, order); err != nil {
		return empty, err
	}
	if err = repairPermission(ctx, tx, actor); err != nil {
		return empty, err
	}
	current, err := repairState(ctx, tx, order)
	if err != nil {
		return empty, err
	}
	var revision int
	var sourceBackend, sourceKey, backend, key, state, kind string
	var sourceAsset *uuid.UUID
	var owner uuid.UUID
	err = tx.QueryRow(ctx, `SELECT revision,COALESCE(source_backend,''),COALESCE(source_key,''),storage_backend,storage_key,state,source_asset_id,actor_id,source_kind FROM product_delivery_repairs WHERE id=$1 AND order_id=$2`, repairID, order).Scan(&revision, &sourceBackend, &sourceKey, &backend, &key, &state, &sourceAsset, &owner, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, ErrRepairNotFound
	}
	if err != nil {
		return empty, err
	}
	if state == "ready" {
		if err = tx.Commit(ctx); err != nil {
			return empty, err
		}
		return s.Inspect(ctx, actor, order)
	}
	if revision != current.Revision || state != "prepared" || current.State != "ready" || !current.Needed {
		return empty, ErrRepairConflict
	}
	snapshot, err := Load(ctx, tx, order)
	if err != nil {
		return empty, err
	}
	if kind != "stored" && kind != "upload" && kind != "bundle" {
		return empty, ErrRepairConflict
	}
	if kind == "bundle" && snapshot.Format != FormatZIPV1 {
		return empty, ErrRepairConflict
	}
	destination, err := s.stores.Get(backend)
	if err != nil {
		return empty, err
	}
	check := func() error {
		object, e := s.stores.OpenVerified(ctx, destination, key, snapshot.SHA256, snapshot.Size, nil)
		if e != nil {
			return e
		}
		return object.Body.Close()
	}
	err = check()
	if errors.Is(err, media.ErrNotFound) {
		if kind == "upload" {
			return empty, ErrRepairUploadRequired
		}
		// A verified target may outlive the backup or its owner's account. Only
		// require fresh source eligibility when we actually need to read it.
		if sourceAsset != nil {
			gotBackend, gotKey, sourceErr := lockRepairBackup(ctx, tx, *sourceAsset, owner)
			if errors.Is(sourceErr, ErrRepairInvalid) {
				return empty, ErrRepairConflict
			}
			if sourceErr != nil {
				return empty, sourceErr
			}
			if gotBackend != sourceBackend || gotKey != sourceKey {
				return empty, ErrRepairConflict
			}
		}
		var stage *media.StagedObject
		var e error
		if kind == "bundle" {
			stage, e = snapshot.stageOriginal(ctx, tx, s.stores)
		} else {
			stage, e = repairSource(ctx, s.stores, sourceBackend, sourceKey, snapshot)
		}
		if e != nil {
			return empty, e
		}
		defer stage.Close()
		if err = pinRepairPermission(ctx, tx, actor); err != nil {
			return empty, err
		}
		putErr := stage.Put(ctx, destination, key, snapshot.MIMEType)
		if e = check(); e != nil {
			return empty, errors.Join(putErr, e)
		}
	} else if err != nil {
		return empty, err
	}
	if err = completeRepair(ctx, tx, actor, order, repairID, revision, requestID); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return s.Inspect(ctx, actor, order)
}

func completeRepair(ctx context.Context, tx pgx.Tx, actor, order, repair uuid.UUID, revision int, requestID string) error {
	// Includes the case where a prior interrupted attempt already wrote the
	// verified target and this request only makes it the active delivery.
	if err := pinRepairPermission(ctx, tx, actor); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE product_delivery_repairs SET state='ready',ready_at=now() WHERE id=$1`, repair); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'marketplace.delivery_repair_completed','order',$2,$3,jsonb_build_object('repairId',$4::text,'revision',$5::integer))`, actor, order, requestID, repair, revision)
	return err
}

func lockRepairBackup(ctx context.Context, tx pgx.Tx, asset, owner uuid.UUID) (string, string, error) {
	var backend, key string
	err := tx.QueryRow(ctx, `SELECT storage_backend,storage_key FROM assets WHERE id=$1 AND owner_id=$2 FOR UPDATE`, asset, owner).Scan(&backend, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrRepairInvalid
	}
	if err != nil {
		return "", "", err
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_repair_backup_candidates WHERE asset_id=$1 AND owner_id=$2)`, asset, owner).Scan(&eligible)
	if err != nil {
		return "", "", err
	}
	if !eligible {
		return "", "", ErrRepairInvalid
	}
	return backend, key, nil
}
