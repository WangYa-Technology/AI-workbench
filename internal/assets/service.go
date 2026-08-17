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
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
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
	Cursor string
	Limit  int
}

type AssetPage struct {
	Items      []Asset `json:"items"`
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
	Title     string
	Filename  string
	Reader    io.Reader
	RequestID string
}

type VersionInput struct {
	UploadInput
	Note string
}

type Provenance struct {
	Purchase   *PurchaseProvenance   `json:"purchase,omitempty"`
	Generation *GenerationProvenance `json:"generation,omitempty"`
}

type PurchaseProvenance struct {
	OrderID      uuid.UUID `json:"orderId"`
	ProductID    uuid.UUID `json:"productId"`
	ProductTitle string    `json:"productTitle"`
	SellerID     uuid.UUID `json:"sellerId"`
	SellerName   string    `json:"sellerName"`
	SellerHandle string    `json:"sellerHandle"`
	LicenseCode  string    `json:"licenseCode"`
	LicenseName  string    `json:"licenseName"`
	OrderStatus  string    `json:"orderStatus"`
	GrantedAt    time.Time `json:"grantedAt"`
	PaymentMode  string    `json:"paymentMode"`
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
	pool      *pgxpool.Pool
	mediaRoot string
}

func NewService(pool *pgxpool.Pool, mediaRoot string) *Service {
	return &Service{pool: pool, mediaRoot: mediaRoot}
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
	if err := os.MkdirAll(s.mediaRoot, 0o750); err != nil {
		return Asset{}, fmt.Errorf("prepare upload storage: %w", err)
	}
	assetID := uuid.New()
	temporary, err := os.CreateTemp(s.mediaRoot, ".upload-*")
	if err != nil {
		return Asset{}, fmt.Errorf("create upload staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	limited := io.LimitReader(input.Reader, MaxUploadSize+1)
	first := make([]byte, 512)
	read, readErr := io.ReadFull(limited, first)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return Asset{}, ErrInvalid
	}
	first = first[:read]
	mimeType := normalizeMIME(http.DetectContentType(first))
	kind, extension, ok := uploadType(mimeType)
	if !ok {
		return Asset{}, ErrInvalid
	}
	written, err := io.Copy(temporary, io.MultiReader(bytes.NewReader(first), limited))
	if err != nil {
		return Asset{}, fmt.Errorf("store uploaded asset: %w", err)
	}
	if written == 0 {
		return Asset{}, ErrInvalid
	}
	if written > MaxUploadSize {
		return Asset{}, ErrTooLarge
	}
	if err := temporary.Sync(); err != nil {
		return Asset{}, fmt.Errorf("sync uploaded asset: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Asset{}, fmt.Errorf("close uploaded asset: %w", err)
	}
	finalPath := filepath.Join(s.mediaRoot, assetID.String()+extension)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return Asset{}, fmt.Errorf("commit uploaded asset: %w", err)
	}
	temporaryPath = finalPath

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Asset{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
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
		if actualOwner != ownerID || sourceType == "purchase" {
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
			 family_id,version_number,version_note,supersedes_asset_id)
			VALUES($1,$2,$3,$4,$5,$6,'pending','upload',$7,$8,$9,$10,$11,NULLIF($12,''),$13)
			RETURNING created_at`, item.ID, ownerID, item.Kind, item.Title, item.MediaURL, item.MimeType, item.LicenseCode,
		input.Filename, written, item.FamilyID, item.VersionNumber, versionNote, item.SupersedesAssetID).Scan(&item.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return Asset{}, ErrConflict
		}
		return Asset{}, fmt.Errorf("create uploaded asset: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"assetId": assetID})
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,3)`, ScanJobKind, payload); err != nil {
		return Asset{}, fmt.Errorf("queue asset scan: %w", err)
	}
	action, reason := "asset.uploaded", "User uploaded an Asset for deterministic local scanning"
	if baseAssetID != nil {
		action, reason = "asset.version_uploaded", versionNote
		if _, err := tx.Exec(ctx, `INSERT INTO asset_version_events(family_id,asset_id,actor_id,previous_asset_id,event_type,reason) VALUES($1,$2,$3,$4,'version_created',$5)`, familyID, assetID, ownerID, *baseAssetID, versionNote); err != nil {
			return Asset{}, fmt.Errorf("record asset version evidence: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,$2,'asset',$3,$4,$5,jsonb_build_object('mimeType',$6::text,'sizeBytes',$7::bigint,'familyId',$8::text,'versionNumber',$9::integer))`, ownerID, action, assetID, reason, requestID(input.RequestID), mimeType, written, familyID, versionNumber); err != nil {
		return Asset{}, fmt.Errorf("audit asset upload: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
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
	var mimeType, scanStatus string
	if err := s.pool.QueryRow(ctx, `SELECT owner_id,mime_type,scan_status FROM assets WHERE id=$1 AND source_type='upload'`, payload.AssetID).Scan(&ownerID, &mimeType, &scanStatus); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if scanStatus != "pending" {
		return nil
	}
	extension, ok := extensionForMIME(mimeType)
	if !ok {
		return s.finishScan(ctx, payload.AssetID, ownerID, "rejected", "Stored MIME type is not supported by the local scanner.")
	}
	data, err := os.ReadFile(filepath.Join(s.mediaRoot, payload.AssetID.String()+extension))
	if err != nil {
		if job.Attempts >= job.MaxAttempts {
			return s.finishScan(ctx, payload.AssetID, ownerID, "review", "Stored file could not be read after repeated local scan attempts.")
		}
		return fmt.Errorf("read uploaded asset for scan: %w", err)
	}
	status, reason := classifyUpload(mimeType, data)
	return s.finishScan(ctx, payload.AssetID, ownerID, status, reason)
}

func (s *Service) finishScan(ctx context.Context, assetID, ownerID uuid.UUID, status, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE assets SET scan_status=$2,scan_reason=$3,scanned_at=now() WHERE id=$1 AND scan_status='pending'`, assetID, status, reason)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	title := "Asset scan completed"
	body := "Your uploaded Asset passed deterministic local scanning and is ready to use."
	if status != "clean" {
		title = "Asset needs review"
		body = "Your uploaded Asset is not available because deterministic local scanning requires review."
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: ownerID, Kind: "asset.scan_completed", Title: title, Body: body, TargetPath: "/workspace/assets/" + assetID.String(),
		ResourceType: "asset", ResourceID: &assetID, SourceKey: "asset-scan:" + assetID.String() + ":" + status,
	}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata) VALUES('asset.scan_completed','asset',$1,$2,'asset-scanner',jsonb_build_object('status',$3::text))`, assetID, reason, status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) List(ctx context.Context, ownerID uuid.UUID, input ListInput) (AssetPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if ownerID == uuid.Nil || input.Limit < 1 || input.Limit > 50 {
		return AssetPage{}, ErrInvalidList
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeAssetCursor(input.Cursor)
		if err != nil {
			return AssetPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, assetSelect+`
		WHERE a.owner_id=$1
		  AND (a.source_type<>'purchase' OR EXISTS(SELECT 1 FROM entitlements e WHERE e.asset_id=a.id AND e.user_id=$1 AND e.status='active'))
		  AND NOT EXISTS(SELECT 1 FROM assets newer WHERE newer.family_id=a.family_id AND newer.version_number>a.version_number)
		  AND ($2::timestamptz IS NULL OR (a.created_at,a.id)<($2,$3::uuid))
		ORDER BY a.created_at DESC,a.id DESC LIMIT $4`, ownerID, cursorTime, cursorID, input.Limit+1)
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
	page := AssetPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		cursor := encodeCursor(assetCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = &cursor
	}
	return page, nil
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
		JOIN posts p ON p.id=pr.post_id
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
	if provenance.Purchase != nil || provenance.Generation != nil {
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
			       '/workspace/generations?generationId='||g.id::text AS target_path,g.created_at
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

func (s *Service) Content(ctx context.Context, viewerID, assetID uuid.UUID) (string, string, error) {
	var ownerID, storageID uuid.UUID
	var mimeType, storageSourceType string
	var publiclyVisible, purchaseActive bool
	err := s.pool.QueryRow(ctx, `
		SELECT a.owner_id,a.mime_type,COALESCE(a.origin_asset_id,a.id),COALESCE(origin.source_type,a.source_type),
		       EXISTS(SELECT 1 FROM works w WHERE w.asset_id IN (a.id,a.origin_asset_id) AND w.status='published'),
		       a.source_type<>'purchase' OR EXISTS(SELECT 1 FROM entitlements e WHERE e.asset_id=a.id AND e.user_id=a.owner_id AND e.status='active')
		FROM assets a
		LEFT JOIN assets origin ON origin.id=a.origin_asset_id
		WHERE a.id=$1 AND a.scan_status='clean'`, assetID).Scan(&ownerID, &mimeType, &storageID, &storageSourceType, &publiclyVisible, &purchaseActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("get asset content: %w", err)
	}
	if !purchaseActive || (ownerID != viewerID && !publiclyVisible) {
		return "", "", ErrForbidden
	}
	if storageSourceType != "generation" && storageSourceType != "upload" {
		return "", "", ErrNotFound
	}
	extension, ok := extensionForMIME(mimeType)
	if !ok {
		return "", "", ErrNotFound
	}
	return filepath.Join(s.mediaRoot, storageID.String()+extension), mimeType, nil
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
		SELECT e.order_id,p.id,o.product_title_snapshot,u.id,u.display_name,u.handle,e.license_code,o.license_name_snapshot,o.status,e.granted_at
		FROM entitlements e
		JOIN orders o ON o.id=e.order_id
		JOIN products p ON p.id=e.product_id
		JOIN users u ON u.id=p.seller_id
		WHERE e.asset_id=$1`, assetID).Scan(
		&result.OrderID, &result.ProductID, &result.ProductTitle, &result.SellerID, &result.SellerName,
		&result.SellerHandle, &result.LicenseCode, &result.LicenseName, &result.OrderStatus, &result.GrantedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get purchase provenance: %w", err)
	}
	result.PaymentMode = "test"
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
	case "audio/wav":
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

func classifyUpload(mimeType string, data []byte) (string, string) {
	detected := normalizeMIME(http.DetectContentType(data[:min(len(data), 512)]))
	if detected != mimeType {
		return "rejected", "File signature does not match the stored MIME type."
	}
	if bytes.Contains(data, []byte("HCAI_LOCAL_TEST_BLOCK_UPLOAD")) {
		return "rejected", "Deterministic local blocked-content marker detected."
	}
	if bytes.Contains(data, []byte("HCAI_LOCAL_TEST_REVIEW_UPLOAD")) {
		return "review", "Deterministic local review marker detected."
	}
	return "clean", "Deterministic local signature and policy checks passed."
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
