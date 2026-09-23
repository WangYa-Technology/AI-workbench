package assets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/productpolicy"
	"github.com/hcai-chat/hcai-chat/internal/taskdelivery"
	"github.com/hcai-chat/hcai-chat/internal/uploadwrite"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound    = errors.New("asset not found")
	ErrForbidden   = errors.New("asset access forbidden")
	ErrInvalid     = errors.New("invalid asset upload")
	ErrTooLarge    = errors.New("asset upload is too large")
	ErrConflict    = errors.New("asset version conflict")
	ErrInvalidList = errors.New("invalid asset list filter")
)

const (
	ScanJobKind   = "asset.scan"
	MaxUploadSize = 10 << 20
)

type Asset struct {
	UploadReplayed    bool           `json:"-"`
	ID                uuid.UUID      `json:"id"`
	Kind              string         `json:"kind"`
	Title             string         `json:"title"`
	MediaURL          string         `json:"mediaUrl"`
	MimeType          string         `json:"mimeType"`
	Width             *int           `json:"width,omitempty"`
	Height            *int           `json:"height,omitempty"`
	ScanStatus        string         `json:"scanStatus"`
	SourceType        string         `json:"sourceType"`
	SourceID          *uuid.UUID     `json:"sourceId,omitempty"`
	OriginAssetID     *uuid.UUID     `json:"originAssetId,omitempty"`
	LicenseCode       string         `json:"licenseCode"`
	UploadedFilename  *string        `json:"uploadedFilename,omitempty"`
	SizeBytes         *int64         `json:"sizeBytes,omitempty"`
	ScanReason        *string        `json:"scanReason,omitempty"`
	ScannedAt         *time.Time     `json:"scannedAt,omitempty"`
	CreatedAt         time.Time      `json:"createdAt"`
	FamilyID          uuid.UUID      `json:"familyId"`
	VersionNumber     int            `json:"versionNumber"`
	VersionNote       *string        `json:"versionNote,omitempty"`
	SupersedesAssetID *uuid.UUID     `json:"supersedesAssetId,omitempty"`
	IsLatestVersion   bool           `json:"isLatestVersion"`
	Versions          []AssetVersion `json:"versions,omitempty"`
	Provenance        *Provenance    `json:"provenance,omitempty"`
	Usages            []AssetUsage   `json:"usages,omitempty"`
	UsageNextCursor   *string        `json:"usageNextCursor,omitempty"`
}

// SavedWork is a reference-only projection of a Community bookmark. It does
// not represent Asset ownership or grant download, commercial, or derivative rights.
type SavedWork struct {
	PostID           uuid.UUID `json:"postId"`
	WorkID           uuid.UUID `json:"workId"`
	AssetID          uuid.UUID `json:"assetId"`
	Title            string    `json:"title"`
	Summary          string    `json:"summary"`
	MediaURL         string    `json:"mediaUrl"`
	MediaKind        string    `json:"mediaKind"`
	Width            *int      `json:"width,omitempty"`
	Height           *int      `json:"height,omitempty"`
	LicenseCode      string    `json:"licenseCode"`
	PromptVisibility string    `json:"promptVisibility"`
	AuthorID         uuid.UUID `json:"authorId"`
	AuthorHandle     string    `json:"authorHandle"`
	AuthorName       string    `json:"authorName"`
	SavedAt          time.Time `json:"savedAt"`
}

type AssetVersion struct {
	ID                uuid.UUID  `json:"id"`
	VersionNumber     int        `json:"versionNumber"`
	VersionNote       *string    `json:"versionNote,omitempty"`
	SupersedesAssetID *uuid.UUID `json:"supersedesAssetId,omitempty"`
	Title             string     `json:"title"`
	ScanStatus        string     `json:"scanStatus"`
	CreatedAt         time.Time  `json:"createdAt"`
}

type AssetUsage struct {
	Kind         string    `json:"kind"`
	ResourceID   uuid.UUID `json:"resourceId"`
	AssetID      uuid.UUID `json:"assetId"`
	AssetVersion int       `json:"assetVersion"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	TargetPath   *string   `json:"targetPath,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type ListInput struct {
	Cursor  string
	Limit   int
	Source  string
	Purpose string
}

type AssetPage struct {
	Items      []Asset `json:"items"`
	Total      int     `json:"total"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

type SavedWorkPage struct {
	Items      []SavedWork `json:"items"`
	NextCursor *string     `json:"nextCursor,omitempty"`
}

type UsagePage struct {
	Items      []AssetUsage `json:"items"`
	NextCursor *string      `json:"nextCursor,omitempty"`
}

type assetCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
	OwnerID   uuid.UUID `json:"ownerId"`
	Source    string    `json:"source,omitempty"`
	Purpose   string    `json:"purpose,omitempty"`
}

type savedWorkCursor struct {
	SavedAt time.Time `json:"savedAt"`
	PostID  uuid.UUID `json:"postId"`
}

type usageCursor struct {
	CreatedAt  time.Time `json:"createdAt"`
	Kind       string    `json:"kind"`
	ResourceID uuid.UUID `json:"resourceId"`
}

type UploadInput struct {
	IdempotencyKey string
	Title          string
	Filename       string
	Reader         io.Reader
	RequestID      string
}

type VersionInput struct {
	UploadInput
	Note string
}

type Provenance struct {
	TaskGrant  *taskdelivery.Grant   `json:"taskGrant,omitempty"`
	Purchase   *PurchaseProvenance   `json:"purchase,omitempty"`
	Generation *GenerationProvenance `json:"generation,omitempty"`
}

type PurchaseProvenance struct {
	Delivery     *PurchaseDelivery `json:"delivery,omitempty"`
	CanDownload  bool              `json:"canDownload"`
	CanReuse     bool              `json:"canReuse"`
	OrderID      uuid.UUID         `json:"orderId"`
	ProductID    uuid.UUID         `json:"productId"`
	ProductTitle string            `json:"productTitle"`
	SellerID     *uuid.UUID        `json:"sellerId,omitempty"`
	SellerName   string            `json:"sellerName,omitempty"`
	SellerHandle string            `json:"sellerHandle,omitempty"`
	LicenseCode  string            `json:"licenseCode"`
	LicenseName  string            `json:"licenseName"`
	OrderStatus  string            `json:"orderStatus"`
	GrantedAt    time.Time         `json:"grantedAt"`
	PaymentMode  string            `json:"paymentMode"`
	RealCharge   bool              `json:"realCharge"`
}

type PurchaseDelivery struct {
	Format    string                       `json:"format"`
	SizeBytes int64                        `json:"sizeBytes"`
	SHA256    string                       `json:"sha256"`
	Files     []productdelivery.BundleFile `json:"files"`
}

type GenerationProvenance struct {
	GenerationID uuid.UUID       `json:"generationId"`
	Provider     string          `json:"provider"`
	ModelName    string          `json:"modelName"`
	Prompt       string          `json:"prompt"`
	SourceWorkID *uuid.UUID      `json:"sourceWorkId,omitempty"`
	SourceAsset  *SourceAssetRef `json:"sourceAsset,omitempty"`
}

type SourceAssetRef struct {
	ID          uuid.UUID           `json:"id"`
	Title       string              `json:"title"`
	SourceType  string              `json:"sourceType"`
	LicenseCode string              `json:"licenseCode"`
	Purchase    *PurchaseProvenance `json:"purchase,omitempty"`
}

type Service struct {
	pool    *pgxpool.Pool
	stores  *media.Catalog
	scanner media.Scanner
}

func NewService(pool *pgxpool.Pool, mediaRoot string) *Service {
	return NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(mediaRoot)), deterministicScanner{})
}

func NewServiceWithMedia(pool *pgxpool.Pool, stores *media.Catalog, scanner media.Scanner) *Service {
	return &Service{pool: pool, stores: stores, scanner: scanner}
}

func NewScannerFromConfig(cfg config.Config) media.Scanner {
	if cfg.MediaScannerAdapter == "http" {
		return media.NewHTTPScanner(cfg.MediaScannerURL, cfg.MediaScannerToken, time.Duration(cfg.MediaScannerTimeoutSeconds)*time.Second)
	}
	return deterministicScanner{}
}

func (s *Service) Upload(ctx context.Context, ownerID uuid.UUID, input UploadInput) (Asset, error) {
	return s.storeUpload(ctx, ownerID, input, nil, "")
}

func (s *Service) UploadVersion(ctx context.Context, ownerID, baseAssetID uuid.UUID, input VersionInput) (Asset, error) {
	input.Note = strings.TrimSpace(input.Note)
	if baseAssetID == uuid.Nil || len(input.Note) < 3 || len(input.Note) > 500 {
		return Asset{}, ErrInvalid
	}
	return s.storeUpload(ctx, ownerID, input.UploadInput, &baseAssetID, input.Note)
}

func (s *Service) storeUpload(ctx context.Context, ownerID uuid.UUID, input UploadInput, baseAssetID *uuid.UUID, versionNote string) (Asset, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Filename = path.Base(strings.TrimSpace(input.Filename))
	if ownerID == uuid.Nil || len(input.Title) < 3 || len(input.Title) > 120 || input.Filename == "." || len(input.Filename) > 180 || input.Reader == nil {
		return Asset{}, ErrInvalid
	}
	// Direct in-process callers may request a fresh operation. HTTP callers must
	// supply a key, so response loss can be recovered across client retries.
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = uuid.NewString()
	}
	if !ValidUploadKey(input.IdempotencyKey) {
		return Asset{}, ErrUploadKey
	}
	assetID := uuid.New()
	limited := io.LimitReader(input.Reader, MaxUploadSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Asset{}, fmt.Errorf("read uploaded asset: %w", err)
	}
	if len(data) == 0 {
		return Asset{}, ErrInvalid
	}
	if len(data) > MaxUploadSize {
		return Asset{}, ErrTooLarge
	}
	first := data[:min(len(data), 512)]
	mimeType := detectUploadMIME(first)
	kind, extension, ok := uploadType(mimeType)
	if !ok {
		return Asset{}, ErrInvalid
	}
	store := s.stores.Primary()
	reservation, err := s.reserveUpload(ctx, ownerID, assetID, input.IdempotencyKey, uploadRequestHash(input, baseAssetID, versionNote, mimeType, data), store, data, extension)
	if err != nil {
		return Asset{}, err
	}
	if reservation.Replay != nil {
		return *reservation.Replay, nil
	}
	intent := reservation.Intent
	storageKey := intent.Key
	// Always leave cleanup responsibility in durable storage before Put. Cleanup
	// after any failure checks attachment/holds/bytes rather than deleting blindly.
	committed := false
	defer func() {
		if committed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = uploadwrite.NewService(s.pool, s.stores).Cleanup(cleanupCtx, intent.ID)
	}()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Asset{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = accountlifecycle.Lock(ctx, tx, ownerID); err != nil {
		return Asset{}, err
	}
	var accountStatus string
	if err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR SHARE`, ownerID).Scan(&accountStatus); errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrForbidden
	} else if err != nil {
		return Asset{}, err
	}
	if accountStatus != "active" {
		return Asset{}, ErrForbidden
	}
	// A concurrent request can finish the same command after our reservation.
	// Recheck under lifecycle/command locks before any external write.
	if replay, err := uploadCommandResult(ctx, tx, reservation.CommandID, ownerID); err != nil {
		return Asset{}, err
	} else if replay != nil {
		return *replay, nil
	}
	written := int64(len(data))
	familyID, versionNumber, licenseCode := assetID, 1, "personal"
	var supersedesAssetID *uuid.UUID
	if baseAssetID != nil {
		var actualOwner uuid.UUID
		var baseKind, sourceType string
		if err := tx.QueryRow(ctx, `SELECT owner_id,family_id,kind,source_type,license_code FROM assets WHERE id=$1 FOR UPDATE`, *baseAssetID).Scan(&actualOwner, &familyID, &baseKind, &sourceType, &licenseCode); errors.Is(err, pgx.ErrNoRows) {
			return Asset{}, ErrNotFound
		} else if err != nil {
			return Asset{}, err
		}
		if actualOwner != ownerID || (sourceType == "purchase" || licenseCode == "task-contract") {
			return Asset{}, ErrForbidden
		}
		if baseKind != kind {
			return Asset{}, ErrInvalid
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, familyID.String()); err != nil {
			return Asset{}, err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version_number),0)+1 FROM assets WHERE family_id=$1`, familyID).Scan(&versionNumber); err != nil {
			return Asset{}, err
		}
		supersedesAssetID = baseAssetID
	}
	// Validate account and version ownership before writing private bytes. The
	// lifecycle lock remains held through Put and commit, so deletion cannot finish
	// between authorization and asset insertion.
	if err = uploadwrite.WriteTx(ctx, tx, s.stores, intent, data, mimeType); errors.Is(err, media.ErrConflict) {
		return Asset{}, ErrConflict
	} else if err != nil {
		return Asset{}, fmt.Errorf("store uploaded asset: %w", err)
	}
	item := Asset{
		ID: assetID, Kind: kind, Title: input.Title, MediaURL: "/api/v1/assets/" + assetID.String() + "/content",
		MimeType: mimeType, ScanStatus: "pending", SourceType: "upload", LicenseCode: licenseCode, UploadedFilename: &input.Filename, SizeBytes: &written,
		FamilyID: familyID, VersionNumber: versionNumber, SupersedesAssetID: supersedesAssetID,
	}
	if versionNote != "" {
		item.VersionNote = &versionNote
	}
	if err := tx.QueryRow(ctx, `
			INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,uploaded_filename,size_bytes,
			 family_id,version_number,version_note,supersedes_asset_id,storage_backend,storage_key)
			VALUES($1,$2,$3,$4,$5,$6,'pending','upload',$7,$8,$9,$10,$11,NULLIF($12,''),$13,$14,$15)
			RETURNING created_at`, item.ID, ownerID, item.Kind, item.Title, item.MediaURL, item.MimeType, item.LicenseCode,
		input.Filename, written, item.FamilyID, item.VersionNumber, versionNote, item.SupersedesAssetID, store.Backend(), storageKey).Scan(&item.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return Asset{}, ErrConflict
		}
		return Asset{}, fmt.Errorf("create uploaded asset: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"assetId": assetID})
	var scanJobID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,3) RETURNING id`, ScanJobKind, payload).Scan(&scanJobID); err != nil {
		return Asset{}, fmt.Errorf("queue asset scan: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO asset_scan_executions(asset_id,job_id) VALUES($1,$2)`, assetID, scanJobID); err != nil {
		return Asset{}, fmt.Errorf("bind asset scan: %w", err)
	}

	action, reason := "asset.uploaded", "User uploaded an Asset for asynchronous scanning"
	if baseAssetID != nil {
		action, reason = "asset.version_uploaded", versionNote
		if _, err := tx.Exec(ctx, `INSERT INTO asset_version_events(family_id,asset_id,actor_id,previous_asset_id,event_type,reason) VALUES($1,$2,$3,$4,'version_created',$5)`, familyID, assetID, ownerID, *baseAssetID, versionNote); err != nil {
			return Asset{}, fmt.Errorf("record asset version evidence: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,$2,'asset',$3,$4,$5,jsonb_build_object('mimeType',$6::text,'sizeBytes',$7::bigint,'familyId',$8::text,'versionNumber',$9::integer))`, ownerID, action, assetID, reason, requestID(input.RequestID), mimeType, written, familyID, versionNumber); err != nil {
		return Asset{}, fmt.Errorf("audit asset upload: %w", err)
	}
	if err = uploadwrite.AttachTx(ctx, tx, intent.ID); err != nil {
		return Asset{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE asset_upload_commands SET result_asset_id=$2,completed_at=now() WHERE id=$1 AND result_asset_id IS NULL`, reservation.CommandID, item.ID); err != nil {
		return Asset{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		// A failed acknowledgement is not proof of rollback. Never delete the
		// original until a fresh, serialized read proves that it was not committed.
		committedAsset, found, verifyErr := s.resolveUploadCommit(ctx, ownerID, item, store.Backend(), storageKey)
		if verifyErr != nil {
			return Asset{}, errors.Join(err, verifyErr)
		}
		if found {
			committed = true
			return committedAsset, nil
		}
		return Asset{}, err
	}

	committed = true
	return item, nil
}

func (s *Service) HandleScanJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		AssetID uuid.UUID `json:"assetId"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.AssetID == uuid.Nil {
		return ErrInvalid
	}
	var ownerID uuid.UUID
	var mimeType, scanStatus, storageBackend, storageKey string
	if err := s.pool.QueryRow(ctx, `SELECT owner_id,mime_type,scan_status,storage_backend,storage_key FROM assets WHERE id=$1 AND source_type='upload'`, payload.AssetID).Scan(&ownerID, &mimeType, &scanStatus, &storageBackend, &storageKey); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if scanStatus != "pending" {
		return nil
	}
	job, err := checkScanExecution(ctx, s.pool, payload.AssetID, job)
	if err != nil {
		return err
	}
	store, err := s.stores.Get(storageBackend)
	if err != nil {
		if job.Attempts >= job.MaxAttempts {
			return s.finishScan(ctx, payload.AssetID, ownerID, job, media.ScanResult{Status: "review", ReasonCode: "storage_backend_unavailable", Engine: s.scanner.Adapter(), Version: "1"})
		}
		return err
	}
	object, err := store.Open(ctx, storageKey, nil)
	if err != nil {
		if job.Attempts >= job.MaxAttempts {
			return s.finishScan(ctx, payload.AssetID, ownerID, job, media.ScanResult{Status: "review", ReasonCode: "storage_read_failed", Engine: s.scanner.Adapter(), Version: "1"})
		}
		return fmt.Errorf("read uploaded asset for scan: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(object.Body, MaxUploadSize+1))
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil || len(data) > MaxUploadSize {
		if job.Attempts >= job.MaxAttempts {
			return s.finishScan(ctx, payload.AssetID, ownerID, job, media.ScanResult{Status: "review", ReasonCode: "storage_read_failed", Engine: s.scanner.Adapter(), Version: "1"})
		}
		return fmt.Errorf("read uploaded asset for scan: read=%v close=%v", readErr, closeErr)
	}
	// Storage I/O may outlive the claim; recheck before sending private bytes.
	if job, err = checkScanExecution(ctx, s.pool, payload.AssetID, job); err != nil {
		return err
	}
	result, err := s.scanner.Scan(ctx, storageKey, mimeType, data)
	if err != nil {
		// The worker will not schedule another attempt for a permanent scanner
		// failure. Complete its business state too, without accepting any verdict.
		if !jobs.ShouldRetry(err) {
			return s.finishScan(ctx, payload.AssetID, ownerID, job, media.ScanResult{Status: "review", ReasonCode: "scanner_failed", Engine: s.scanner.Adapter(), Version: "1"})
		}
		if job.Attempts >= job.MaxAttempts {
			return s.finishScan(ctx, payload.AssetID, ownerID, job, media.ScanResult{Status: "review", ReasonCode: "scanner_failed_after_retries", Engine: s.scanner.Adapter(), Version: "1"})
		}
		return err
	}
	return s.finishScan(ctx, payload.AssetID, ownerID, job, result)
}

func (s *Service) finishScan(ctx context.Context, assetID, ownerID uuid.UUID, job jobs.Job, result media.ScanResult) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = accountlifecycle.Lock(ctx, tx, ownerID); err != nil {
		return err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT scan_status FROM assets WHERE id=$1 FOR UPDATE`, assetID).Scan(&status); err != nil {
		return err
	}
	if status != "pending" {
		return tx.Commit(ctx)
	}
	if _, err = lockScanExecution(ctx, tx, assetID, job); err != nil {
		return err
	}
	if err = finishScanTx(ctx, tx, assetID, ownerID, result, s.scanner.Adapter()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func finishScanTx(ctx context.Context, tx pgx.Tx, assetID, ownerID uuid.UUID, result media.ScanResult, adapter string) error {
	// Callers hold the account lifecycle lock before the asset row lock.
	var accountStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, ownerID).Scan(&accountStatus); err != nil {
		return err
	}
	if accountStatus == "deleted" {
		result = media.ScanResult{Status: "review", ReasonCode: "scanner_execution_failed", Engine: result.Engine, Version: result.Version}
	}
	reason := scanReason(result)
	updated, err := tx.Exec(ctx, `UPDATE assets SET scan_status=$2,scan_reason=$3,scanned_at=now() WHERE id=$1 AND scan_status='pending'`, assetID, result.Status, reason)
	if err != nil {
		return err
	}
	if updated.RowsAffected() == 0 {
		return nil
	}
	title := "Asset scan completed"
	body := "Your uploaded Asset passed scanning and is ready to use."
	if result.Status != "clean" {
		title = "Asset needs review"
		body = "Your uploaded Asset needs review and is not available for use yet."
	}
	if accountStatus != "deleted" {
		if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
			UserID: ownerID, Kind: "asset.scan_completed", Title: title, Body: body, TargetPath: "/workspace/assets/" + assetID.String(),
			ResourceType: "asset", ResourceID: &assetID, SourceKey: "asset-scan:" + assetID.String() + ":" + result.Status,
		}); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata) VALUES('asset.scan_completed','asset',$1,$2,'asset-scanner',jsonb_build_object('status',$3::text,'reasonCode',$4::text,'scannerAdapter',$5::text,'engine',$6::text,'version',$7::text))`, assetID, reason, result.Status, result.ReasonCode, adapter, result.Engine, result.Version); err != nil {
		return err
	}
	return nil
}

const assetListConditions = ` WHERE a.owner_id=$1
	AND ($2='' OR a.source_type=$2)
	AND ($3='' OR ($3='product_preview' AND EXISTS(SELECT 1 FROM product_preview_candidates candidate WHERE candidate.asset_id=a.id))
 OR ($3='product_source' AND EXISTS(SELECT 1 FROM product_source_candidates candidate WHERE candidate.asset_id=a.id)))
	AND (a.source_type<>'purchase' OR EXISTS(SELECT 1 FROM entitlements e WHERE e.asset_id=a.id AND e.user_id=$1 AND e.status='active'))
	AND NOT (a.source_type='generation' AND EXISTS(SELECT 1 FROM generations chat_generation WHERE chat_generation.id=a.source_id AND chat_generation.mode='chat'))
	AND NOT EXISTS(SELECT 1 FROM assets newer WHERE newer.family_id=a.family_id AND newer.version_number>a.version_number)`

func (s *Service) List(ctx context.Context, ownerID uuid.UUID, input ListInput) (AssetPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if ownerID == uuid.Nil || input.Limit < 1 || input.Limit > 50 || len(input.Cursor) > 1024 ||
		(input.Source != "" && input.Source != "purchase") ||
		(input.Purpose != "" && input.Purpose != "product_preview" && input.Purpose != "product_source") || (input.Purpose != "" && input.Source != "") {
		return AssetPage{}, ErrInvalidList
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeAssetCursor(input.Cursor)
		if err != nil {
			return AssetPage{}, err
		}
		if cursor.OwnerID != ownerID || cursor.Source != input.Source || cursor.Purpose != input.Purpose {
			return AssetPage{}, ErrInvalidList
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return AssetPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM assets a`+assetListConditions, ownerID, input.Source, input.Purpose).Scan(&total); err != nil {
		return AssetPage{}, err
	}
	rows, err := tx.Query(ctx, assetSelect+assetListConditions+`
		  AND ($4::timestamptz IS NULL OR (a.created_at,a.id)<($4,$5::uuid))
		ORDER BY a.created_at DESC,a.id DESC LIMIT $6`, ownerID, input.Source, input.Purpose, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return AssetPage{}, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()
	items := make([]Asset, 0)
	for rows.Next() {
		item, _, err := scanAsset(rows)
		if err != nil {
			return AssetPage{}, fmt.Errorf("scan asset: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return AssetPage{}, err
	}
	page := AssetPage{Items: items, Total: total}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		cursor := encodeCursor(assetCursor{CreatedAt: last.CreatedAt, ID: last.ID, OwnerID: ownerID, Source: input.Source, Purpose: input.Purpose})
		page.NextCursor = &cursor
	}
	return page, tx.Commit(ctx)
}

func (s *Service) ListSavedWorks(ctx context.Context, userID uuid.UUID, input ListInput) (SavedWorkPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if userID == uuid.Nil || input.Limit < 1 || input.Limit > 50 {
		return SavedWorkPage{}, ErrInvalidList
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeSavedWorkCursor(input.Cursor)
		if err != nil {
			return SavedWorkPage{}, err
		}
		cursorTime, cursorID = &cursor.SavedAt, &cursor.PostID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.id,w.id,a.id,w.title,w.summary,a.media_url,a.kind,a.width,a.height,
		       a.license_code,w.prompt_visibility,u.id,u.handle,u.display_name,pr.created_at
		FROM post_reactions pr
		JOIN community_visible_posts p ON p.id=pr.post_id
		JOIN works w ON w.id=p.work_id
		JOIN assets a ON a.id=w.asset_id
		JOIN users u ON u.id=w.author_id
		WHERE pr.user_id=$1 AND pr.kind='bookmark'
		  AND p.status='published' AND w.status='published' AND a.scan_status='clean'
		  AND ($2::timestamptz IS NULL OR (pr.created_at,p.id)<($2,$3::uuid))
		ORDER BY pr.created_at DESC,p.id DESC
		LIMIT $4`, userID, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return SavedWorkPage{}, fmt.Errorf("list saved works: %w", err)
	}
	defer rows.Close()
	items := make([]SavedWork, 0)
	for rows.Next() {
		var item SavedWork
		if err := rows.Scan(&item.PostID, &item.WorkID, &item.AssetID, &item.Title, &item.Summary,
			&item.MediaURL, &item.MediaKind, &item.Width, &item.Height, &item.LicenseCode,
			&item.PromptVisibility, &item.AuthorID, &item.AuthorHandle, &item.AuthorName, &item.SavedAt); err != nil {
			return SavedWorkPage{}, fmt.Errorf("scan saved work: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return SavedWorkPage{}, err
	}
	page := SavedWorkPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		cursor := encodeCursor(savedWorkCursor{SavedAt: last.SavedAt, PostID: last.PostID})
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) GetOwned(ctx context.Context, ownerID, assetID uuid.UUID) (Asset, error) {
	item, actualOwner, err := scanAsset(s.pool.QueryRow(ctx, assetSelect+` WHERE a.id=$1`, assetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("get asset: %w", err)
	}
	if actualOwner != ownerID {
		return Asset{}, ErrForbidden
	}
	provenance := &Provenance{}
	switch item.SourceType {
	case "delivery":
		var grant taskdelivery.Grant
		err := s.pool.QueryRow(ctx, `SELECT demand_id,delivery_id,rights_terms,rights_evidence,ai_disclosure,allow_derivative_reuse FROM task_delivery_grants WHERE asset_id=$1 AND client_id=$2`, item.ID, ownerID).Scan(&grant.TaskID, &grant.DeliveryID, &grant.RightsTerms, &grant.RightsEvidence, &grant.AIDisclosure, &grant.AllowDerivativeReuse)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Asset{}, err
		}
		if err == nil {
			provenance.TaskGrant = &grant
		}
	case "purchase":
		purchase, err := s.purchaseProvenance(ctx, item.ID)
		if err != nil {
			return Asset{}, err
		}
		provenance.Purchase = purchase
	case "generation":
		generation, err := s.generationProvenance(ctx, item.ID)
		if err != nil {
			return Asset{}, err
		}
		provenance.Generation = generation
	}
	if provenance.Purchase != nil || provenance.Generation != nil || provenance.TaskGrant != nil {
		item.Provenance = provenance
	}
	versions, err := s.versions(ctx, item.FamilyID)
	if err != nil {
		return Asset{}, err
	}
	item.Versions = versions
	usagePage, err := s.usages(ctx, ownerID, item.FamilyID, ListInput{Limit: 20})
	if err != nil {
		return Asset{}, err
	}
	item.Usages = usagePage.Items
	item.UsageNextCursor = usagePage.NextCursor
	return item, nil
}

func (s *Service) ListUsages(ctx context.Context, ownerID, assetID uuid.UUID, input ListInput) (UsagePage, error) {
	if ownerID == uuid.Nil || assetID == uuid.Nil {
		return UsagePage{}, ErrInvalidList
	}
	var actualOwner, familyID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT owner_id,family_id FROM assets WHERE id=$1`, assetID).Scan(&actualOwner, &familyID); errors.Is(err, pgx.ErrNoRows) {
		return UsagePage{}, ErrNotFound
	} else if err != nil {
		return UsagePage{}, err
	}
	if actualOwner != ownerID {
		return UsagePage{}, ErrForbidden
	}
	return s.usages(ctx, ownerID, familyID, input)
}

func (s *Service) usages(ctx context.Context, ownerID, familyID uuid.UUID, input ListInput) (UsagePage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return UsagePage{}, ErrInvalidList
	}
	var cursorTime *time.Time
	var cursorKind *string
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeUsageCursor(input.Cursor)
		if err != nil {
			return UsagePage{}, err
		}
		cursorTime, cursorKind, cursorID = &cursor.CreatedAt, &cursor.Kind, &cursor.ResourceID
	}
	rows, err := s.pool.Query(ctx, `
		WITH family_assets AS (
			SELECT id,version_number FROM assets WHERE family_id=$1 AND owner_id=$2
		), usage_rows AS (
			SELECT 'generation'::text AS kind,g.id AS resource_id,fa.id AS asset_id,fa.version_number,
			   left(g.prompt,120) AS title,g.status,
			   CASE WHEN g.conversation_id IS NULL THEN '/create/image?generationId='||g.id::text
			            ELSE '/create/image?conversationId='||g.conversation_id::text||'&generationId='||g.id::text END AS target_path,g.created_at
			FROM generations g JOIN family_assets fa ON fa.id=g.source_asset_id
			WHERE g.owner_id=$2
			UNION ALL
			SELECT 'work',w.id,fa.id,fa.version_number,w.title,w.status,
			       CASE WHEN w.status='published' THEN '/works/'||w.id::text
			            WHEN w.status='draft' THEN '/publish?draftId='||w.id::text END,w.created_at
			FROM works w JOIN family_assets fa ON fa.id=w.asset_id
			WHERE w.author_id=$2
			UNION ALL
			SELECT 'product',p.id,fa.id,fa.version_number,p.title,p.status,
			       CASE WHEN p.status='active' THEN '/market/assets/'||p.id::text END,p.created_at
			FROM products p JOIN family_assets fa ON fa.id=p.asset_id
			WHERE p.seller_id=$2
			UNION ALL
			SELECT 'delivery',d.id,fa.id,fa.version_number,dem.title,d.status,
			       '/market/demands/'||d.demand_id::text,d.created_at
			FROM deliveries d
			JOIN family_assets fa ON fa.id=d.asset_id
			JOIN demands dem ON dem.id=d.demand_id
			WHERE d.creator_id=$2
		)
		SELECT kind,resource_id,asset_id,version_number,title,status,target_path,created_at
		FROM usage_rows
		WHERE $3::timestamptz IS NULL OR created_at<$3
		   OR (created_at=$3 AND (kind>$4 OR (kind=$4 AND resource_id>$5::uuid)))
		ORDER BY created_at DESC,kind,resource_id LIMIT $6`, familyID, ownerID, cursorTime, cursorKind, cursorID, input.Limit+1)
	if err != nil {
		return UsagePage{}, fmt.Errorf("list asset usages: %w", err)
	}
	defer rows.Close()
	items := make([]AssetUsage, 0)
	for rows.Next() {
		var item AssetUsage
		if err := rows.Scan(&item.Kind, &item.ResourceID, &item.AssetID, &item.AssetVersion, &item.Title,
			&item.Status, &item.TargetPath, &item.CreatedAt); err != nil {
			return UsagePage{}, fmt.Errorf("scan asset usage: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return UsagePage{}, err
	}
	page := UsagePage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		cursor := encodeCursor(usageCursor{CreatedAt: last.CreatedAt, Kind: last.Kind, ResourceID: last.ResourceID})
		page.NextCursor = &cursor
	}
	return page, nil
}

func encodeCursor(value any) string {
	body, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeAssetCursor(value string) (assetCursor, error) {
	var cursor assetCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return assetCursor{}, ErrInvalidList
	}
	return cursor, nil
}

func decodeSavedWorkCursor(value string) (savedWorkCursor, error) {
	var cursor savedWorkCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.SavedAt.IsZero() || cursor.PostID == uuid.Nil {
		return savedWorkCursor{}, ErrInvalidList
	}
	return cursor, nil
}

func decodeUsageCursor(value string) (usageCursor, error) {
	var cursor usageCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ResourceID == uuid.Nil || !validUsageKind(cursor.Kind) {
		return usageCursor{}, ErrInvalidList
	}
	return cursor, nil
}

func validUsageKind(value string) bool {
	return value == "generation" || value == "work" || value == "product" || value == "delivery"
}

func (s *Service) versions(ctx context.Context, familyID uuid.UUID) ([]AssetVersion, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,version_number,version_note,supersedes_asset_id,title,scan_status,created_at FROM assets WHERE family_id=$1 ORDER BY version_number DESC`, familyID)
	if err != nil {
		return nil, fmt.Errorf("list asset versions: %w", err)
	}
	defer rows.Close()
	items := make([]AssetVersion, 0)
	for rows.Next() {
		var item AssetVersion
		if err := rows.Scan(&item.ID, &item.VersionNumber, &item.VersionNote, &item.SupersedesAssetID, &item.Title, &item.ScanStatus, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type Content struct {
	snapshot   *productdelivery.Snapshot
	fileIndex  *int
	authorize  func(context.Context) error
	stores     *media.Catalog
	MimeType   string
	Name       string
	Attachment bool
	store      media.Store
	key        string
}

// WithAuthorization adds an access condition without replacing ownership,
// scan, storage-locator or purchased-content checks on the original handle.
func (c Content) WithAuthorization(check func(context.Context) error) Content {
	if check == nil {
		return c
	}
	original := c.authorize
	c.authorize = func(ctx context.Context) error {
		if err := check(ctx); err != nil {
			return err
		}
		if original != nil {
			return original(ctx)
		}
		return nil
	}
	return c
}

func (c Content) Stat(ctx context.Context) (media.ObjectInfo, error) {
	if c.authorize != nil {
		if err := c.authorize(ctx); err != nil {
			return media.ObjectInfo{}, err
		}
	}
	if c.snapshot != nil {
		if c.fileIndex != nil {
			file := c.snapshot.Manifest.Files[*c.fileIndex]
			return media.ObjectInfo{Size: file.SizeBytes, ETag: `"sha256-` + file.SHA256 + `"`}, nil
		}
		return media.ObjectInfo{Size: c.snapshot.Size, ETag: `"sha256-` + c.snapshot.SHA256 + `"`}, nil
	}
	info, err := c.store.Stat(ctx, c.key)
	if err != nil {
		return media.ObjectInfo{}, err
	}
	// A remote HEAD may outlive authority. Do not expose even length/range
	// metadata after it completes with an obsolete grant.
	if c.authorize != nil {
		if err := c.authorize(ctx); err != nil {
			return media.ObjectInfo{}, err
		}
	}
	return info, nil
}
func (c Content) Open(ctx context.Context, requested *media.ByteRange) (media.Object, error) {
	if c.authorize != nil {
		if err := c.authorize(ctx); err != nil {
			return media.Object{}, err
		}
	}
	var object media.Object
	var err error
	if c.snapshot != nil {
		if c.fileIndex != nil {
			object, err = c.snapshot.OpenFile(ctx, c.stores, *c.fileIndex, requested)
		} else {
			object, err = c.snapshot.Open(ctx, c.stores, requested)
		}
	} else {
		object, err = c.store.Open(ctx, c.key, requested)
	}
	if err != nil {
		return media.Object{}, err
	}
	// Both snapshot staging and legacy storage reads may take time. Close the
	// stream if rights or scanning changed before the bytes can be returned.
	if c.authorize != nil {
		if err = c.authorize(ctx); err != nil {
			_ = object.Body.Close()
			return media.Object{}, err
		}
	}
	return object, nil
}

// contentAccess binds a non-snapshot handle to the exact authorized locator.
// Re-resolving it prevents a stale handle from inheriting changed visibility or
// using an old key after the asset has moved. This is not a historical checksum.
type contentAccess struct {
	ownerID, orderID                                        uuid.UUID
	mimeType, storageSourceType, storageBackend, storageKey string
	snapshotRequired                                        bool
}

func (s *Service) resolveContentAccess(ctx context.Context, viewerID, assetID uuid.UUID) (contentAccess, error) {
	var access contentAccess
	var publiclyVisible, purchaseActive, taskParticipant, viewerActive bool
	err := s.pool.QueryRow(ctx, `
			SELECT a.owner_id,COALESCE(c.contract->'asset'->>'mimeType',a.mime_type),COALESCE(c.contract->'asset'->>'sourceType',origin.source_type,a.source_type),
			       COALESCE(c.contract->'asset'->>'storageBackend',origin.storage_backend,a.storage_backend),
			       COALESCE(c.contract->'asset'->>'storageKey',origin.storage_key,a.storage_key),
			       (a.source_type<>'purchase' AND NOT EXISTS(SELECT 1 FROM product_delivery_roots r WHERE r.asset_id=COALESCE(a.origin_asset_id,a.id)) AND
			       (EXISTS(SELECT 1 FROM public_product_previews preview WHERE preview.asset_id=a.id) OR EXISTS(SELECT 1 FROM public_works w WHERE w.asset_id IN (a.id,a.origin_asset_id)) OR
			        EXISTS(SELECT 1 FROM system_settings ss WHERE ss.singleton=true AND ss.site_configuration->>'siteIconUrl'=a.media_url))),
			       a.source_type<>'purchase' OR `+productpolicy.PurchaseAssetAccessSQL+`,
                   EXISTS(SELECT 1 FROM deliveries dl JOIN demands d ON d.id=dl.demand_id
                          WHERE (dl.asset_id=a.id OR EXISTS(SELECT 1 FROM delivery_assets da WHERE da.delivery_id=dl.id AND da.asset_id=a.id)) AND (d.client_id=$2 OR (d.status='disputed' AND EXISTS(SELECT 1 FROM users u JOIN role_permissions rp ON rp.role=u.role WHERE u.id=$2 AND u.status='active' AND rp.permission_id='admin:tasks'))) AND d.status IN ('submitted','revision','disputed','accepted')),
                   COALESCE(o.delivery_snapshot_required,false),COALESCE(o.id,'00000000-0000-0000-0000-000000000000'::uuid),
                   EXISTS(SELECT 1 FROM users viewer WHERE viewer.id=$2 AND viewer.status='active')
			FROM assets a
			LEFT JOIN assets origin ON origin.id=a.origin_asset_id
			LEFT JOIN product_order_contracts c ON a.source_type='purchase' AND c.order_id=a.source_id
            LEFT JOIN orders o ON a.source_type='purchase' AND o.id=a.source_id
			WHERE a.id=$1 AND a.scan_status='clean' AND (origin.id IS NULL OR origin.scan_status='clean')`, assetID, viewerID).Scan(&access.ownerID, &access.mimeType, &access.storageSourceType, &access.storageBackend, &access.storageKey, &publiclyVisible, &purchaseActive, &taskParticipant, &access.snapshotRequired, &access.orderID, &viewerActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return contentAccess{}, ErrNotFound
	}
	if err != nil {
		return contentAccess{}, fmt.Errorf("get asset content: %w", err)
	}
	if !purchaseActive || (!publiclyVisible && !(viewerActive && (access.ownerID == viewerID || taskParticipant))) {
		return contentAccess{}, ErrForbidden
	}
	return access, nil
}

func (s *Service) Content(ctx context.Context, viewerID, assetID uuid.UUID) (Content, error) {
	access, err := s.resolveContentAccess(ctx, viewerID, assetID)
	if err != nil {
		return Content{}, err
	}

	if access.snapshotRequired {
		if access.orderID == uuid.Nil {
			return Content{}, ErrNotFound
		}
		if err := s.authorizePurchaseContent(ctx, viewerID, assetID, access.orderID); err != nil {
			return Content{}, err
		}
		snapshot, err := productdelivery.Load(ctx, s.pool, access.orderID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && snapshot.State != "ready") {
			return Content{}, ErrNotFound
		}
		if err != nil {
			return Content{}, err
		}
		content := Content{MimeType: snapshot.MIMEType, Name: assetID.String(), snapshot: &snapshot, stores: s.stores,
			authorize: func(ctx context.Context) error {
				return s.authorizePurchaseContent(ctx, viewerID, assetID, access.orderID)
			}}
		if snapshot.Format == productdelivery.FormatZIPV1 {
			content.Name += ".zip"
			content.Attachment = true
		}
		return content, nil
	}
	if access.storageSourceType != "generation" && access.storageSourceType != "upload" {
		return Content{}, ErrNotFound
	}
	store, err := s.stores.Get(access.storageBackend)
	if err != nil {
		return Content{}, fmt.Errorf("get asset storage backend: %w", err)
	}
	content := Content{MimeType: access.mimeType, Name: assetID.String(), store: store, key: access.storageKey}
	content.authorize = func(ctx context.Context) error {
		current, err := s.resolveContentAccess(ctx, viewerID, assetID)
		if errors.Is(err, ErrNotFound) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if current != access {
			return ErrForbidden
		}
		return nil
	}
	return content, nil
}

// ContentFile accepts only an index into this buyer's immutable package. It
// cannot address originals, another order, or arbitrary storage keys.
func (s *Service) ContentFile(ctx context.Context, viewerID, assetID uuid.UUID, index int) (Content, error) {
	content, err := s.Content(ctx, viewerID, assetID)
	if err != nil {
		return Content{}, err
	}
	if content.snapshot == nil || content.snapshot.Format != productdelivery.FormatZIPV1 || content.snapshot.Manifest == nil || index < 0 || index >= len(content.snapshot.Manifest.Files) {
		return Content{}, ErrInvalid
	}
	file := content.snapshot.Manifest.Files[index]
	content.fileIndex, content.Name, content.MimeType, content.Attachment = &index, file.Name, file.MIMEType, true
	return content, nil
}

func (s *Service) authorizePurchaseContent(ctx context.Context, viewerID, assetID, orderID uuid.UUID) error {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets a
 WHERE a.id=$1 AND a.source_id=$2 AND a.owner_id=$3 AND `+productpolicy.PurchaseAssetAccessSQL+`)`,
		assetID, orderID, viewerID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func (s *Service) generationProvenance(ctx context.Context, assetID uuid.UUID) (*GenerationProvenance, error) {
	var result GenerationProvenance
	var sourceAssetID *uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id,provider,model_name,prompt,source_work_id,source_asset_id
		FROM generations WHERE output_asset_id=$1 AND status='succeeded'`, assetID).Scan(
		&result.GenerationID, &result.Provider, &result.ModelName, &result.Prompt, &result.SourceWorkID, &sourceAssetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get generation provenance: %w", err)
	}
	if sourceAssetID != nil {
		var source SourceAssetRef
		err := s.pool.QueryRow(ctx, `SELECT id,title,source_type,license_code FROM assets WHERE id=$1`, sourceAssetID).Scan(
			&source.ID, &source.Title, &source.SourceType, &source.LicenseCode)
		if err != nil {
			return nil, fmt.Errorf("get source asset provenance: %w", err)
		}
		if source.SourceType == "purchase" {
			purchase, err := s.purchaseProvenance(ctx, source.ID)
			if err != nil {
				return nil, err
			}
			source.Purchase = purchase
		}
		result.SourceAsset = &source
	}
	return &result, nil
}

func (s *Service) purchaseProvenance(ctx context.Context, assetID uuid.UUID) (*PurchaseProvenance, error) {
	var result PurchaseProvenance
	err := s.pool.QueryRow(ctx, `
		SELECT e.order_id,p.id,o.product_title_snapshot,u.id,COALESCE(u.display_name,''),COALESCE(u.handle,''),e.license_code,o.license_name_snapshot,o.status,e.granted_at,
	       CASE WHEN pi.provider IS NOT NULL THEN pi.provider WHEN legacy.order_id IS NOT NULL THEN 'test' ELSE 'unverified' END,COALESCE(pi.live_mode,false),
	       `+productpolicy.PurchaseAssetAccessSQL+`, `+productpolicy.PurchaseAssetReuseSQL+`
		FROM entitlements e
		JOIN assets a ON a.id=e.asset_id
		LEFT JOIN product_order_contracts c ON c.order_id=e.order_id
		JOIN orders o ON o.id=e.order_id
		JOIN products p ON p.id=e.product_id
		LEFT JOIN payment_intents pi ON pi.order_id=o.id
  LEFT JOIN legacy_product_refund_evidence legacy ON legacy.order_id=o.id AND pi.id IS NULL
  LEFT JOIN users u ON u.id::text=CASE WHEN c.order_id IS NOT NULL THEN c.contract->'product'->>'sellerId'
    WHEN pi.id IS NOT NULL THEN pi.payee_id::text ELSE legacy.seller_id::text END
		WHERE e.asset_id=$1`, assetID).Scan(
		&result.OrderID, &result.ProductID, &result.ProductTitle, &result.SellerID, &result.SellerName,
		&result.SellerHandle, &result.LicenseCode, &result.LicenseName, &result.OrderStatus, &result.GrantedAt, &result.PaymentMode, &result.RealCharge,
		&result.CanDownload, &result.CanReuse)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get purchase provenance: %w", err)
	}
	snapshot, err := productdelivery.Load(ctx, s.pool, result.OrderID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		if snapshot.Format == productdelivery.FormatZIPV1 {
			result.CanReuse = false
			result.Delivery = &PurchaseDelivery{Format: snapshot.Format, SizeBytes: snapshot.Size, SHA256: snapshot.SHA256, Files: snapshot.Manifest.Files}
		}
	}
	return &result, nil
}

const assetSelect = `
	SELECT a.id,a.kind,a.title,a.media_url,a.mime_type,a.width,a.height,a.scan_status,a.source_type,a.source_id,
	       a.origin_asset_id,a.license_code,a.uploaded_filename,a.size_bytes,a.scan_reason,a.scanned_at,a.created_at,
	       a.family_id,a.version_number,a.version_note,a.supersedes_asset_id,
	       NOT EXISTS(SELECT 1 FROM assets newer WHERE newer.family_id=a.family_id AND newer.version_number>a.version_number),a.owner_id
	FROM assets a`

type scanner interface {
	Scan(...any) error
}

func scanAsset(row scanner) (Asset, uuid.UUID, error) {
	var item Asset
	var ownerID uuid.UUID
	err := row.Scan(&item.ID, &item.Kind, &item.Title, &item.MediaURL, &item.MimeType, &item.Width, &item.Height,
		&item.ScanStatus, &item.SourceType, &item.SourceID, &item.OriginAssetID, &item.LicenseCode, &item.UploadedFilename,
		&item.SizeBytes, &item.ScanReason, &item.ScannedAt, &item.CreatedAt, &item.FamilyID, &item.VersionNumber,
		&item.VersionNote, &item.SupersedesAssetID, &item.IsLatestVersion, &ownerID)
	return item, ownerID, err
}

func extensionForMIME(mimeType string) (string, bool) {
	switch mimeType {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "video/mp4":
		return ".mp4", true
	case "audio/wav":
		return ".wav", true
	case "audio/mpeg":
		return ".mp3", true
	case "text/plain; charset=utf-8", "text/plain":
		return ".txt", true
	default:
		return "", false
	}
}

func uploadType(mimeType string) (string, string, bool) {
	extension, ok := extensionForMIME(mimeType)
	if !ok {
		return "", "", false
	}
	switch mimeType {
	case "image/jpeg", "image/png":
		return "image", extension, true
	case "video/mp4":
		return "video", extension, true
	case "audio/wav", "audio/mpeg":
		return "audio", extension, true
	case "text/plain; charset=utf-8", "text/plain":
		return "document", extension, true
	default:
		return "", "", false
	}
}

func normalizeMIME(value string) string {
	if strings.HasPrefix(value, "text/plain") {
		return "text/plain; charset=utf-8"
	}
	if value == "audio/wave" || value == "audio/x-wav" {
		return "audio/wav"
	}
	return value
}

func detectUploadMIME(data []byte) string {
	if looksLikeMP3(data) {
		return "audio/mpeg"
	}
	return normalizeMIME(http.DetectContentType(data))
}

func looksLikeMP3(data []byte) bool {
	if len(data) >= 3 && string(data[:3]) == "ID3" {
		return true
	}
	return len(data) >= 2 && data[0] == 0xff && (data[1]&0xe0) == 0xe0
}

type deterministicScanner struct{}

func (deterministicScanner) Adapter() string { return "local_deterministic" }

func (deterministicScanner) Scan(_ context.Context, _ string, mimeType string, data []byte) (media.ScanResult, error) {
	detected := detectUploadMIME(data[:min(len(data), 512)])
	if detected != mimeType {
		return media.ScanResult{Status: "rejected", ReasonCode: "mime_signature_mismatch", Engine: "hcai-local", Version: "1"}, nil
	}
	if bytes.Contains(data, []byte("HCAI_LOCAL_TEST_BLOCK_UPLOAD")) {
		return media.ScanResult{Status: "rejected", ReasonCode: "local_block_marker", Engine: "hcai-local", Version: "1"}, nil
	}
	if bytes.Contains(data, []byte("HCAI_LOCAL_TEST_REVIEW_UPLOAD")) {
		return media.ScanResult{Status: "review", ReasonCode: "local_review_marker", Engine: "hcai-local", Version: "1"}, nil
	}
	return media.ScanResult{Status: "clean", ReasonCode: "local_checks_passed", Engine: "hcai-local", Version: "1"}, nil
}

func scanReason(result media.ScanResult) string {
	switch result.ReasonCode {
	case "mime_signature_mismatch":
		return "File signature does not match the stored MIME type."
	case "local_block_marker":
		return "Deterministic local blocked-content marker detected."
	case "local_review_marker":
		return "Deterministic local review marker detected."
	case "local_checks_passed":
		return "Deterministic local signature and policy checks passed."
	case "storage_backend_unavailable":
		return "Stored media backend was unavailable after repeated scan attempts."
	case "storage_read_failed":
		return "Stored media could not be read after repeated scan attempts."
	case "scanner_failed_after_retries":
		return "The configured media scanner failed after repeated attempts."
	case "scanner_execution_failed":
		return "The scan worker stopped without a verified result. Review is required."
	case "scanner_failed":
		return "The configured media scanner could not verify this file. Review is required."
	default:
		return "Media scanner returned " + result.Status + " with reason code " + result.ReasonCode + "."
	}
}

func requestID(value string) string {
	if strings.TrimSpace(value) == "" {
		return "asset-upload"
	}
	return value
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
