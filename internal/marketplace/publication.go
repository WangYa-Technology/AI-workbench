package marketplace

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidListing   = errors.New("invalid product listing")
	ErrListingConflict  = errors.New("product listing changed")
	ErrListingForbidden = errors.New("product listing permission denied")
	ErrListingSource    = errors.New("product source or preview unavailable")
)

// ProductFile is safe seller/reviewer metadata. Storage evidence only enters
// internal versioning and the accepted order contract.
type ProductFile struct {
	AssetID uuid.UUID `json:"assetId"`
	Name    string    `json:"name"`
}

type ProductDraft struct {
	Title          string        `json:"title"`
	Description    string        `json:"description"`
	ProductType    string        `json:"productType"`
	Category       string        `json:"category"`
	AssetID        uuid.UUID     `json:"assetId"`
	PreviewAssetID *uuid.UUID    `json:"previewAssetId"`
	PriceCents     int           `json:"priceCents"`
	Currency       string        `json:"currency"`
	LicenseCode    string        `json:"licenseCode"`
	AIDisclosure   string        `json:"aiDisclosure"`
	IncludedFiles  []string      `json:"includedFiles"`
	Files          []ProductFile `json:"files,omitempty"`
	Compatibility  string        `json:"compatibility"`
}

type SellerProduct struct {
	ProductDraft
	ID             uuid.UUID `json:"id"`
	SellerID       uuid.UUID `json:"sellerId"`
	Status         string    `json:"status"`
	ReviewStatus   string    `json:"reviewStatus"`
	ReviewReason   string    `json:"reviewReason"`
	Version        string    `json:"version"`
	ContentVersion string    `json:"contentVersion"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type ListingMutation struct {
	ExpectedVersion string        `json:"expectedVersion"`
	Draft           *ProductDraft `json:"draft,omitempty"`
	RightsConfirmed bool          `json:"rightsConfirmed"`
	Confirmed       bool          `json:"confirmed"`
	Reason          string        `json:"reason"`
}

type ListingFilter struct {
	Status, Cursor string
	Limit          int
}
type ListingPage struct {
	Items      []SellerProduct `json:"items"`
	Total      int             `json:"total"`
	NextCursor *string         `json:"nextCursor,omitempty"`
}
type listingCursor struct {
	Actor     uuid.UUID `json:"actor"`
	Review    bool      `json:"review"`
	Status    string    `json:"status"`
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"at"`
}

const listingFrom = ` FROM products p JOIN product_listing_versions v ON v.product_id=p.id
 LEFT JOIN product_publications m ON m.product_id=p.id `
const listingJSON = `jsonb_build_object('id',p.id,'sellerId',p.seller_id,'title',p.title,'description',p.description,
 'productType',p.product_type,'category',p.category,'assetId',p.asset_id,'previewAssetId',p.preview_asset_id,
 'priceCents',p.price_cents,'currency',p.currency,'licenseCode',p.license_code,'aiDisclosure',p.ai_disclosure,
 'includedFiles',p.included_files,'files',COALESCE((SELECT jsonb_agg(jsonb_build_object('assetId',f.asset_id,'name',f.file_name) ORDER BY f.position) FROM product_listing_files f WHERE f.product_id=p.id),'[]'::jsonb),
 'compatibility',p.compatibility,'status',p.status,
 'reviewStatus',COALESCE(m.review_status,'legacy'),'reviewReason',COALESCE(m.review_reason,''),
 'version',v.version,'contentVersion',v.content_version,'createdAt',p.created_at,'updatedAt',p.updated_at)`

func scanListing(row scanner) (SellerProduct, error) {
	var raw []byte
	var item SellerProduct
	err := row.Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	err = json.Unmarshal(raw, &item)
	return item, err
}

func listingActor(ctx context.Context, tx pgx.Tx, actor uuid.UUID, review bool) error {
	var role, status string
	err := tx.QueryRow(ctx, `SELECT role,status FROM users WHERE id=$1 FOR SHARE`, actor).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "active") {
		return ErrListingForbidden
	}
	if err != nil {
		return err
	}
	if review {
		// The actor row alone cannot protect a role grant from concurrent
		// removal. Pin both through the read/decision and its audit commit.
		var permission string
		err = tx.QueryRow(ctx, `SELECT permission_id FROM role_permissions WHERE role=$1 AND permission_id='admin:content' FOR SHARE`, role).Scan(&permission)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrListingForbidden
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func listingCommandError(err error) error {
	var e *pgconn.PgError
	if errors.As(err, &e) && (e.Code == "40001" || e.Code == "40P01" || e.Code == "23505") {
		return ErrListingConflict
	}
	return err
}

func (s *Service) GetListing(ctx context.Context, actor, id uuid.UUID, review bool) (item SellerProduct, err error) {
	defer func() { err = listingCommandError(err) }()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SellerProduct{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = listingActor(ctx, tx, actor, review); err != nil {
		return SellerProduct{}, err
	}
	item, err = scanListing(tx.QueryRow(ctx, `SELECT `+listingJSON+listingFrom+` WHERE p.id=$1 AND ($3 OR p.seller_id=$2)`, id, actor, review))
	if err != nil {
		return item, err
	}
	return item, tx.Commit(ctx)
}

func (s *Service) ListListings(ctx context.Context, actor uuid.UUID, review bool, f ListingFilter) (page ListingPage, err error) {
	defer func() { err = listingCommandError(err) }()
	page = ListingPage{Items: []SellerProduct{}}
	if f.Limit == 0 {
		f.Limit = 20
	}
	if f.Limit < 1 || f.Limit > 50 || len(f.Cursor) > 1024 {
		return page, ErrInvalidListing
	}
	switch f.Status {
	case "", "draft", "active", "paused", "removed", "pending", "rejected", "blocked":
	default:
		return page, ErrInvalidListing
	}
	var c listingCursor
	if f.Cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(f.Cursor)
		if e != nil || json.Unmarshal(b, &c) != nil || c.Actor != actor || c.Review != review || c.Status != f.Status || c.ID == uuid.Nil || c.CreatedAt.IsZero() {
			return page, ErrInvalidListing
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = listingActor(ctx, tx, actor, review); err != nil {
		return page, err
	}
	condition := ` WHERE ($2 OR p.seller_id=$1) AND ($3='' OR CASE WHEN $3 IN ('pending','rejected','blocked') THEN m.review_status=$3 ELSE p.status=$3 END)`
	if err = tx.QueryRow(ctx, `SELECT count(*)`+listingFrom+condition, actor, review, f.Status).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.Query(ctx, `SELECT `+listingJSON+listingFrom+condition+` AND ($4::uuid IS NULL OR (p.created_at,p.id)<($5,$4)) ORDER BY p.created_at DESC,p.id DESC LIMIT $6`, actor, review, f.Status, nullableUUID(c.ID), c.CreatedAt, f.Limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		item, e := scanListing(rows)
		if e != nil {
			return page, e
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > f.Limit {
		page.Items = page.Items[:f.Limit]
		last := page.Items[len(page.Items)-1]
		raw, _ := json.Marshal(listingCursor{Actor: actor, Review: review, Status: f.Status, ID: last.ID, CreatedAt: last.CreatedAt})
		next := base64.RawURLEncoding.EncodeToString(raw)
		page.NextCursor = &next
	}
	return page, tx.Commit(ctx)
}
func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func (s *Service) ListingLicenses(ctx context.Context) ([]License, error) {
	rows, err := s.pool.Query(ctx, `SELECT code,name,summary,terms,version,allows_commercial,allows_derivatives,allows_redistribution,attribution_required,refund_window_days FROM licenses WHERE status='active' ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []License{}
	for rows.Next() {
		var l License
		if err = rows.Scan(&l.Code, &l.Name, &l.Summary, &l.Terms, &l.Version, &l.AllowsCommercial, &l.AllowsDerivatives, &l.AllowsRedistribution, &l.AttributionRequired, &l.RefundWindowDays); err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, rows.Err()
}

func validListingText(s string, min, max int) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && n >= min && n <= max
}
func normalizeDraft(d *ProductDraft) error {
	if d == nil {
		return ErrInvalidListing
	}
	d.Title = strings.TrimSpace(d.Title)
	d.Description = strings.TrimSpace(d.Description)
	d.AIDisclosure = strings.TrimSpace(d.AIDisclosure)
	d.Compatibility = strings.TrimSpace(d.Compatibility)
	if !validListingText(d.Title, 1, 160) || !validListingText(d.Description, 1, 10000) || !validListingText(d.AIDisclosure, 1, 2000) || !validListingText(d.Compatibility, 0, 2000) ||
		!categoryCodePattern.MatchString(d.Category) || !validListingText(d.LicenseCode, 1, 100) || d.AssetID == uuid.Nil || d.PriceCents < 50 || d.PriceCents > 99999999 || d.Currency != "USD" {
		return ErrInvalidListing
	}
	if len(d.Files) == 0 {
		if len(d.IncludedFiles) != 1 {
			return ErrInvalidListing
		}
		d.IncludedFiles[0] = strings.TrimSpace(d.IncludedFiles[0])
		if !validListingText(d.IncludedFiles[0], 1, 200) {
			return ErrInvalidListing
		}
	} else {
		if len(d.Files) != len(d.IncludedFiles) || len(d.Files) > productdelivery.MaxBundleFiles || d.Files[0].AssetID != d.AssetID {
			return ErrInvalidListing
		}
		names := make([]string, len(d.Files))
		seen := make(map[uuid.UUID]bool, len(d.Files))
		for i, file := range d.Files {
			if file.AssetID == uuid.Nil || seen[file.AssetID] || file.Name != d.IncludedFiles[i] || (d.PreviewAssetID != nil && file.AssetID == *d.PreviewAssetID) {
				return ErrInvalidListing
			}
			seen[file.AssetID], names[i] = true, file.Name
		}
		if productdelivery.ValidateBundleNames(names) != nil {
			return ErrInvalidListing
		}
	}
	switch d.ProductType {
	case "prompt", "workflow", "asset", "work":
	default:
		return ErrInvalidListing
	}
	if d.PreviewAssetID != nil && (*d.PreviewAssetID == uuid.Nil || *d.PreviewAssetID == d.AssetID) {
		return ErrInvalidListing
	}
	return nil
}
func listingVersionValid(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && v == strings.ToLower(v)
}

// Validate and lock the same candidates the owner can select. Locks shared with
// work publication and scan review serialize conversion of a private original.
func validateListingAssets(ctx context.Context, tx pgx.Tx, owner uuid.UUID, d ProductDraft) error {
	ids := listingSourceIDs(d)
	rows, err := tx.Query(ctx, `SELECT id FROM assets WHERE id=ANY($1::uuid[]) OR id=$2 ORDER BY id FOR UPDATE`, ids, d.PreviewAssetID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT count(*)=$3 FROM product_source_candidates WHERE asset_id=ANY($1::uuid[]) AND owner_id=$2`, ids, owner, len(ids)).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible {
		return ErrListingSource
	}
	if d.PreviewAssetID != nil {
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_preview_candidates WHERE asset_id=$1 AND owner_id=$2)`, d.PreviewAssetID, owner).Scan(&eligible); err != nil {
			return err
		}
		if !eligible {
			return ErrListingSource
		}
	}
	if err = tx.QueryRow(ctx, `SELECT code FROM licenses WHERE code=$1 AND status='active' FOR SHARE`, d.LicenseCode).Scan(&d.LicenseCode); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidListing
	} else if err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT code FROM task_types WHERE code=$1 AND scope='marketplace' FOR SHARE`, d.Category).Scan(&d.Category); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidListing
	} else {
		return err
	}
}

func (s *Service) MutateListing(ctx context.Context, actor, id uuid.UUID, action, key, requestID string, in ListingMutation) (result SellerProduct, err error) {
	defer func() { err = listingCommandError(err) }()
	review := action == "approve" || action == "reject" || action == "block" || action == "reopen"
	switch action {
	case "create", "edit", "submit", "pause", "approve", "reject", "block", "reopen":
	default:
		return result, ErrInvalidListing
	}
	if actor == uuid.Nil || !validListingText(key, 8, 128) || strings.TrimSpace(key) != key {
		return result, ErrInvalidListing
	}
	if action == "create" {
		if id != uuid.Nil || in.ExpectedVersion != "" {
			return result, ErrInvalidListing
		}
	} else if id == uuid.Nil || !listingVersionValid(in.ExpectedVersion) {
		return result, ErrInvalidListing
	}
	if action == "create" || action == "edit" {
		if err = normalizeDraft(in.Draft); err != nil {
			return result, err
		}
	} else if in.Draft != nil {
		return result, ErrInvalidListing
	}
	if action == "submit" && !in.RightsConfirmed {
		return result, ErrInvalidListing
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if !validListingText(in.Reason, 0, 2000) || (review && (!in.Confirmed || !validListingText(in.Reason, 10, 2000))) {
		return result, ErrInvalidListing
	}
	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	raw, _ := json.Marshal(struct {
		ID     uuid.UUID
		Action string
		Input  ListingMutation
	}{id, action, in})
	requestHash := fmt.Sprintf("%x", sha256.Sum256(raw))
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = listingActor(ctx, tx, actor, review); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,93))`, actor.String()+":"+keyHash); err != nil {
		return result, err
	}
	var savedID uuid.UUID
	var savedHash string
	err = tx.QueryRow(ctx, `SELECT product_id,request_sha256 FROM product_listing_commands WHERE actor_id=$1 AND key_sha256=$2`, actor, keyHash).Scan(&savedID, &savedHash)
	if err == nil {
		if savedHash != requestHash {
			return result, ErrIdempotencyConflict
		}
		result, err = scanListing(tx.QueryRow(ctx, `SELECT `+listingJSON+listingFrom+` WHERE p.id=$1 AND ($3 OR p.seller_id=$2)`, savedID, actor, review))
		if err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if action == "create" || action == "edit" || action == "submit" || action == "approve" {
		if err = systemsettings.RequireTx(ctx, tx, systemsettings.Publishing); err != nil {
			return result, err
		}
	}
	if action != "create" {
		var owner uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT seller_id FROM products WHERE id=$1 FOR UPDATE`, id).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
			return result, ErrNotFound
		} else if err != nil {
			return result, err
		}
		if !review && owner != actor {
			return result, ErrNotFound
		}
		if review && owner == actor {
			return result, ErrListingForbidden
		}
		result, err = scanListing(tx.QueryRow(ctx, `SELECT `+listingJSON+listingFrom+` WHERE p.id=$1`, id))
		if err != nil {
			return result, err
		}
		if result.Version != in.ExpectedVersion {
			return result, ErrListingConflict
		}
		if !review && (result.Status == "removed" || result.ReviewStatus == "blocked") {
			return result, ErrListingConflict
		}
	}
	switch action {
	case "create", "edit":
		if action == "edit" && (result.Status == "active" || result.ReviewStatus == "pending") {
			return result, ErrListingConflict
		}
		// Older clients must not silently discard a real file list. Switching
		// back to a single file requires an explicit empty files array.
		if action == "edit" && len(result.Files) != 0 && in.Draft.Files == nil {
			return result, ErrInvalidListing
		}
		if err = validateListingAssets(ctx, tx, actor, *in.Draft); err != nil {
			return result, err
		}
		// Registering a delivery root changes this asset's publication rights.
		// Advance its MVCC row even when file metadata is unchanged, so older
		// serializable preview writers cannot accept a stale candidate.
		if _, err = tx.Exec(ctx, `UPDATE assets SET title=title WHERE id=ANY($1::uuid[])`, listingSourceIDs(*in.Draft)); err != nil {
			return result, err
		}
		d := in.Draft
		files, _ := json.Marshal(d.IncludedFiles)
		if action == "edit" {
			if err = checkListingVersion(ctx, tx, id, in.ExpectedVersion); err != nil {
				return result, err
			}
		}
		if action == "create" {
			id = uuid.New()
			_, err = tx.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,category,price_cents,currency,license_code,ai_disclosure,included_files,compatibility,preview_asset_id,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'draft')`, id, actor, d.AssetID, d.Title, d.Description, d.ProductType, d.Category, d.PriceCents, d.Currency, d.LicenseCode, d.AIDisclosure, files, d.Compatibility, d.PreviewAssetID)
		} else {
			_, err = tx.Exec(ctx, `UPDATE products SET asset_id=$3,title=$4,description=$5,product_type=$6,category=$7,price_cents=$8,currency=$9,license_code=$10,ai_disclosure=$11,included_files=$12,compatibility=$13,preview_asset_id=$14,status='draft',updated_at=now() WHERE id=$1 AND seller_id=$2`, id, actor, d.AssetID, d.Title, d.Description, d.ProductType, d.Category, d.PriceCents, d.Currency, d.LicenseCode, d.AIDisclosure, files, d.Compatibility, d.PreviewAssetID)
		}
		if err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM product_listing_files WHERE product_id=$1`, id); err != nil {
			return result, err
		}
		for i, file := range d.Files {
			if _, err = tx.Exec(ctx, `INSERT INTO product_listing_files(product_id,position,asset_id,file_name) VALUES($1,$2,$3,$4)`, id, i, file.AssetID, file.Name); err != nil {
				return result, err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO product_publications(product_id) VALUES($1) ON CONFLICT(product_id) DO UPDATE SET review_status='draft',submitted_version=NULL,approved_version=NULL,review_reason='',updated_at=now()`, id)
	case "submit":
		if result.Status == "active" || result.ReviewStatus == "pending" {
			return result, ErrListingConflict
		}
		if err = normalizeDraft(&result.ProductDraft); err != nil {
			return result, err
		}
		if err = validateListingAssets(ctx, tx, actor, result.ProductDraft); err != nil {
			return result, err
		}
		if err = checkListingVersion(ctx, tx, id, in.ExpectedVersion); err != nil {
			return result, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO product_publications(product_id,review_status,submitted_version) VALUES($1,'pending',$2) ON CONFLICT(product_id) DO UPDATE SET review_status='pending',submitted_version=$2,approved_version=NULL,review_reason='',updated_at=now()`, id, result.ContentVersion)
	case "pause":
		// Also withdraw a pending submission. A seller cannot lift a governance block.
		if result.Status != "active" && result.ReviewStatus != "pending" {
			return result, ErrListingConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE products SET status='paused',updated_at=now() WHERE id=$1`, id); err != nil {
			return result, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO product_publications(product_id) VALUES($1) ON CONFLICT(product_id) DO UPDATE SET review_status='draft',submitted_version=NULL,approved_version=NULL,review_reason='',updated_at=now()`, id)
	case "approve", "reject":
		if result.ReviewStatus != "pending" || result.Status == "removed" {
			return result, ErrListingConflict
		}
		var submitted string
		if err = tx.QueryRow(ctx, `SELECT submitted_version FROM product_publications WHERE product_id=$1`, id).Scan(&submitted); err != nil {
			return result, err
		}
		if action == "approve" {
			if submitted != result.ContentVersion {
				return result, ErrListingConflict
			}
			if err = normalizeDraft(&result.ProductDraft); err != nil {
				return result, err
			}
			if err = validateListingAssets(ctx, tx, result.SellerID, result.ProductDraft); err != nil {
				return result, err
			}
			if err = checkListingVersion(ctx, tx, id, in.ExpectedVersion); err != nil {
				return result, err
			}
			_, err = tx.Exec(ctx, `UPDATE product_publications SET review_status='approved',approved_version=$2,review_reason=$3,updated_at=now() WHERE product_id=$1`, id, submitted, in.Reason)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE products SET status='active',updated_at=now() WHERE id=$1`, id)
			}
		} else {
			_, err = tx.Exec(ctx, `UPDATE product_publications SET review_status='rejected',approved_version=NULL,review_reason=$2,updated_at=now() WHERE product_id=$1`, id, in.Reason)
		}
	case "reopen":
		if result.ReviewStatus != "blocked" || result.Status == "removed" {
			return result, ErrListingConflict
		}
		_, err = tx.Exec(ctx, `UPDATE product_publications SET review_status='draft',submitted_version=NULL,approved_version=NULL,review_reason=$2,updated_at=now() WHERE product_id=$1`, id, in.Reason)
	case "block":
		if _, err = tx.Exec(ctx, `UPDATE products SET status='paused',updated_at=now() WHERE id=$1`, id); err != nil {
			return result, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO product_publications(product_id,review_status,review_reason) VALUES($1,'blocked',$2) ON CONFLICT(product_id) DO UPDATE SET review_status='blocked',approved_version=NULL,review_reason=$2,updated_at=now()`, id, in.Reason)
	}
	if err != nil {
		return result, err
	}
	result, err = scanListing(tx.QueryRow(ctx, `SELECT `+listingJSON+listingFrom+` WHERE p.id=$1`, id))
	if err != nil {
		return result, err
	}
	if review {
		err = notifications.CreateTx(ctx, tx, notifications.CreateInput{UserID: result.SellerID, Kind: "marketplace.listing_reviewed", Title: "Product review updated", Body: "A publication decision is available for your product.", TargetPath: "/workspace/products/" + id.String(), ResourceType: "product", ResourceID: &id, SourceKey: "product-review:" + actor.String() + ":" + keyHash})
		if err != nil {
			return result, err
		}
	}
	snapshot, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO product_listing_commands(actor_id,key_sha256,request_sha256,product_id,action,snapshot) VALUES($1,$2,$3,$4,$5,$6)`, actor, keyHash, requestHash, id, action, snapshot); err != nil {
		return result, err
	}
	evidence, _ := json.Marshal(map[string]any{"version": result.Version, "contentVersion": result.ContentVersion, "rightsConfirmed": in.RightsConfirmed, "commandSha256": keyHash})
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,$2,'product',$3,$4,$5,$6)`, actor, "marketplace.listing_"+action, id, in.Reason, requestID, evidence)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

// Review downloads are scoped to one observed listing and audited separately.
// The caller still uses the asset service's scan/storage authorization checks.
func (s *Service) ReviewListingFile(ctx context.Context, actor, id uuid.UUID, kind, version, requestID string) (uuid.UUID, uuid.UUID, error) {
	return s.ReviewListingFileAt(ctx, actor, id, kind, version, requestID, nil)
}

// ReviewListingFileAt requires an explicit member index for a real bundle. A
// legacy reviewer must never inspect only the first file as if it were all of it.
func (s *Service) ReviewListingFileAt(ctx context.Context, actor, id uuid.UUID, kind, version, requestID string, index *int) (owner, file uuid.UUID, err error) {
	defer func() { err = listingCommandError(err) }()
	if (kind != "source" && kind != "preview") || !listingVersionValid(version) || (index != nil && (kind != "source" || *index < 0 || *index >= productdelivery.MaxBundleFiles)) {
		return uuid.Nil, uuid.Nil, ErrInvalidListing
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = listingActor(ctx, tx, actor, true); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	var current string
	err = tx.QueryRow(ctx, `SELECT p.seller_id,CASE WHEN $2='source' THEN p.asset_id ELSE p.preview_asset_id END,v.version FROM products p JOIN product_listing_versions v ON v.product_id=p.id WHERE p.id=$1 AND ($2='source' OR p.preview_asset_id IS NOT NULL) FOR SHARE OF p`, id, kind).Scan(&owner, &file, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if current != version {
		return uuid.Nil, uuid.Nil, ErrListingConflict
	}
	if kind == "source" {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM product_listing_files WHERE product_id=$1`, id).Scan(&count); err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		if count > 0 {
			if index == nil || *index >= count {
				return uuid.Nil, uuid.Nil, ErrInvalidListing
			}
			if err = tx.QueryRow(ctx, `SELECT asset_id FROM product_listing_files WHERE product_id=$1 AND position=$2`, id, *index).Scan(&file); err != nil {
				return uuid.Nil, uuid.Nil, err
			}
		} else if index != nil && *index != 0 {
			return uuid.Nil, uuid.Nil, ErrInvalidListing
		}
	}
	// A stale or corrupted product reference must not grant a reviewer access
	// under an unrelated asset owner's identity.
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE id=$1 AND owner_id=$2 AND source_type IN ('upload','generation') AND origin_asset_id IS NULL)`, file, owner).Scan(&valid); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if !valid {
		return uuid.Nil, uuid.Nil, ErrListingSource
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,'marketplace.listing_file_reviewed','product',$2,'Authorized listing file review',$3,jsonb_build_object('kind',$4::text,'version',$5::text,'assetId',$6::text,'fileIndex',$7::integer))`, actor, id, requestID, kind, version, file.String(), index)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return owner, file, tx.Commit(ctx)
}

// RecheckListingFile binds the streaming handoff to the original reviewer,
// listing version and exact member. The earlier audited grant is not a durable
// capability to read as the seller after authority or listing content changes.
func (s *Service) RecheckListingFile(ctx context.Context, actor, id, owner, file uuid.UUID, kind, version string, index *int) error {
	if (kind != "source" && kind != "preview") || !listingVersionValid(version) || (index != nil && (kind != "source" || *index < 0 || *index >= productdelivery.MaxBundleFiles)) {
		return ErrListingForbidden
	}
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM users u JOIN role_permissions rp ON rp.role=u.role
 JOIN products p ON p.id=$2 JOIN product_listing_versions v ON v.product_id=p.id
 JOIN assets a ON a.id=$4 AND a.owner_id=p.seller_id
 WHERE u.id=$1 AND u.status='active' AND rp.permission_id='admin:content'
 AND p.seller_id=$3 AND v.version=$6
 AND a.source_type IN ('upload','generation') AND a.origin_asset_id IS NULL
 AND CASE WHEN $5='preview' THEN p.preview_asset_id=$4 AND $7::integer IS NULL
 WHEN EXISTS(SELECT 1 FROM product_listing_files f WHERE f.product_id=p.id)
 THEN EXISTS(SELECT 1 FROM product_listing_files f WHERE f.product_id=p.id AND f.position=$7 AND f.asset_id=$4)
 ELSE p.asset_id=$4 AND ($7::integer IS NULL OR $7=0) END)`, actor, id, owner, file, kind, version, index).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrListingForbidden
	}
	return nil
}

func checkListingVersion(ctx context.Context, tx pgx.Tx, id uuid.UUID, expected string) error {
	var current string
	if err := tx.QueryRow(ctx, `SELECT version FROM product_listing_versions WHERE product_id=$1`, id).Scan(&current); err != nil {
		return err
	}
	if current != expected {
		return ErrListingConflict
	}
	return nil
}

func listingSourceIDs(d ProductDraft) []uuid.UUID {
	if len(d.Files) == 0 {
		return []uuid.UUID{d.AssetID}
	}
	ids := make([]uuid.UUID, len(d.Files))
	for i, f := range d.Files {
		ids[i] = f.AssetID
	}
	return ids
}
