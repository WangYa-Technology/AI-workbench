package discovery

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("work not found")
	ErrInvalidQuery = errors.New("invalid discovery query")
)

type Author struct {
	ID          uuid.UUID `json:"id"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"displayName"`
}

type WorkReference struct {
	ID     uuid.UUID `json:"id"`
	Title  string    `json:"title"`
	Author Author    `json:"author"`
}

type Work struct {
	PostID           *uuid.UUID      `json:"postId,omitempty"`
	ViewerBookmarked bool            `json:"viewerBookmarked"`
	Sources          []WorkReference `json:"sources,omitempty"`
	ID               uuid.UUID       `json:"id"`
	Title            string          `json:"title"`
	Summary          string          `json:"summary"`
	Prompt           *string         `json:"prompt,omitempty"`
	PromptVisibility string          `json:"promptVisibility"`
	ModelName        string          `json:"modelName"`
	AIDisclosure     string          `json:"aiDisclosure"`
	PublishedAt      time.Time       `json:"publishedAt"`
	AssetID          uuid.UUID       `json:"assetId"`
	MediaURL         string          `json:"mediaUrl"`
	MediaKind        string          `json:"mediaKind"`
	Width            *int            `json:"width,omitempty"`
	Height           *int            `json:"height,omitempty"`
	LicenseCode      string          `json:"licenseCode"`
	Author           Author          `json:"author"`
}

type Page struct {
	Total          int            `json:"total"`
	Items          []Work         `json:"items"`
	NextCursor     *string        `json:"nextCursor"`
	CategoryCounts map[string]int `json:"categoryCounts"`
}

type SearchFilter struct {
	Query string
	Types []string
	Page  int
	Limit int
}

type SearchResult struct {
	Type          string     `json:"type"`
	ID            uuid.UUID  `json:"id"`
	Title         string     `json:"title"`
	Summary       string     `json:"summary"`
	Path          string     `json:"path"`
	MediaURL      *string    `json:"mediaUrl,omitempty"`
	MediaKind     *string    `json:"mediaKind,omitempty"`
	CreatorHandle *string    `json:"creatorHandle,omitempty"`
	PriceCents    *int       `json:"priceCents,omitempty"`
	Currency      *string    `json:"currency,omitempty"`
	PublishedAt   *time.Time `json:"publishedAt,omitempty"`
	Rank          int        `json:"rank"`
	RankSignals   []string   `json:"rankSignals"`
}

type SearchPage struct {
	Query         string         `json:"query"`
	Items         []SearchResult `json:"items"`
	Page          int            `json:"page"`
	Limit         int            `json:"limit"`
	Total         int            `json:"total"`
	HasMore       bool           `json:"hasMore"`
	PolicyVersion int            `json:"policyVersion"`
	PolicyName    string         `json:"policyName"`
	PolicyVariant string         `json:"policyVariant"`
}

type RankingPolicy struct {
	Version               int
	Name                  string
	TitleExactWeight      int
	TitlePrefixWeight     int
	TitleContainsWeight   int
	CreatorExactWeight    int
	CreatorMatchWeight    int
	BodyMatchWeight       int
	SecondaryMatchWeight  int
	RecencyWeight         int
	CreatorActivityWeight int
	WorkTypeBoost         int
	CreatorTypeBoost      int
	ProductTypeBoost      int
	DemandTypeBoost       int
}

type CreatorProduct struct {
	ID           uuid.UUID `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	ProductType  string    `json:"productType"`
	PriceCents   int       `json:"priceCents"`
	Currency     string    `json:"currency"`
	LicenseCode  string    `json:"licenseCode"`
	MediaURL     string    `json:"mediaUrl"`
	MediaKind    string    `json:"mediaKind"`
	AIDisclosure string    `json:"aiDisclosure"`
}

type CreatorFilter struct{ WorksPage, ProductsPage, Limit int }
type CreatorProfile struct {
	WorksTotal      int              `json:"worksTotal"`
	ProductsTotal   int              `json:"productsTotal"`
	WorksPage       int              `json:"worksPage"`
	ProductsPage    int              `json:"productsPage"`
	Limit           int              `json:"limit"`
	ID              uuid.UUID        `json:"id"`
	Handle          string           `json:"handle"`
	DisplayName     string           `json:"displayName"`
	Role            string           `json:"role"`
	MemberSince     time.Time        `json:"memberSince"`
	FollowerCount   int              `json:"followerCount"`
	FollowingCount  int              `json:"followingCount"`
	ViewerFollowing bool             `json:"viewerFollowing"`
	Works           []Work           `json:"works"`
	Products        []CreatorProduct `json:"products"`
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

type WorkFilter struct {
	Query, Kind, PromptVisibility string
	BeforeID                      *uuid.UUID
}

func (r *Repository) List(ctx context.Context, limit int, before *time.Time, filters ...WorkFilter) (Page, error) {
	var filter WorkFilter
	if len(filters) > 0 {
		filter = filters[0]
	}
	if limit == 0 {
		limit = 12
	}
	if limit < 1 || limit > 24 {
		return Page{}, ErrInvalidQuery
	}
	var err error
	filter, err = NormalizeWorkFilter(filter)
	if err != nil {
		return Page{}, err
	}
	if before != nil && filter.BeforeID == nil {
		return Page{}, ErrInvalidQuery
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT w.id,w.title,w.summary,w.prompt_visibility,w.model_name,w.ai_disclosure,w.published_at,
		       a.id,a.media_url,a.kind,a.width,a.height,a.license_code,
		       u.id,u.handle,u.display_name
		FROM public_works w
		JOIN assets a ON a.id=w.asset_id
		JOIN users u ON u.id=w.author_id
		WHERE w.status='published' AND a.scan_status='clean'
		AND ($1::timestamptz IS NULL OR w.published_at < $1 OR ($6::uuid IS NOT NULL AND w.published_at=$1 AND w.id<$6))
		AND ($3='' OR concat_ws(' ',w.title,w.summary,u.display_name,u.handle,w.model_name,a.license_code) ILIKE '%'||$3||'%')
		AND ($4='' OR a.kind=$4)
		AND ($5='' OR w.prompt_visibility=$5)
		ORDER BY w.published_at DESC,w.id DESC
		LIMIT $2`, before, limit+1, literalLike(filter.Query), filter.Kind, filter.PromptVisibility, filter.BeforeID)
	if err != nil {
		return Page{}, fmt.Errorf("list works: %w", err)
	}
	defer rows.Close()

	items := make([]Work, 0, limit)
	for rows.Next() {
		work, err := scanWork(rows, false)
		if err != nil {
			return Page{}, err
		}
		items = append(items, work)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate works: %w", err)
	}

	var next *string
	if len(items) > limit {
		items = items[:limit]
		cursor := encodeWorkCursor(items[len(items)-1], filter)
		next = &cursor
	}
	rows.Close()
	counts := map[string]int{}
	countRows, err := tx.Query(ctx, `
		SELECT a.kind, count(*) FROM public_works w
		JOIN assets a ON a.id=w.asset_id JOIN users u ON u.id=w.author_id
		WHERE w.status='published' AND a.scan_status='clean'
		AND ($1='' OR concat_ws(' ',w.title,w.summary,u.display_name,u.handle,w.model_name,a.license_code) ILIKE '%'||$1||'%')
		AND ($2='' OR w.prompt_visibility=$2)
		GROUP BY a.kind`, literalLike(filter.Query), filter.PromptVisibility)
	if err != nil {
		return Page{}, fmt.Errorf("count work categories: %w", err)
	}
	defer countRows.Close()
	for countRows.Next() {
		var kind string
		var count int
		if err := countRows.Scan(&kind, &count); err != nil {
			return Page{}, err
		}
		counts[kind] = count
	}
	if err := countRows.Err(); err != nil {
		return Page{}, err
	}
	countRows.Close()
	total := 0
	for kind, count := range counts {
		if filter.Kind == "" || filter.Kind == kind {
			total += count
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Page{}, err
	}
	return Page{Items: items, NextCursor: next, CategoryCounts: counts, Total: total}, nil
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID, viewers ...uuid.UUID) (Work, error) {
	viewer := uuid.Nil
	if len(viewers) > 0 {
		viewer = viewers[0]
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback(ctx)
	row := tx.QueryRow(ctx, `
		SELECT w.id,w.title,w.summary,
		       CASE WHEN w.prompt_visibility='public' THEN w.prompt ELSE NULL END,
		       w.prompt_visibility,w.model_name,w.ai_disclosure,w.published_at,
		       a.id,a.media_url,a.kind,a.width,a.height,a.license_code,
		       u.id,u.handle,u.display_name
		FROM public_works w
		JOIN assets a ON a.id=w.asset_id
		JOIN users u ON u.id=w.author_id
		WHERE w.id=$1 AND w.status='published' AND a.scan_status='clean'`, id)
	work, err := scanWork(row, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return Work{}, ErrNotFound
	}
	if err != nil {
		return Work{}, fmt.Errorf("get work: %w", err)
	}
	err = tx.QueryRow(ctx, `SELECT p.id,EXISTS(SELECT 1 FROM post_reactions pr WHERE pr.post_id=p.id AND pr.user_id=$2 AND pr.kind='bookmark')
 FROM community_visible_posts p WHERE p.work_id=$1 ORDER BY p.created_at,p.id LIMIT 1`, id, viewer).Scan(&work.PostID, &work.ViewerBookmarked)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Work{}, err
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE lineage AS (
 SELECT source.id,source.title,source.author_id,source.asset_id,ARRAY[$1::uuid,source.id] path,1 depth
 FROM public_works current JOIN generations g ON g.output_asset_id=current.asset_id AND g.status='succeeded'
 JOIN public_works source ON source.id=g.source_work_id WHERE current.id=$1 AND source.id<>current.id
 UNION ALL
 SELECT source.id,source.title,source.author_id,source.asset_id,l.path||source.id,l.depth+1
 FROM lineage l JOIN generations g ON g.output_asset_id=l.asset_id AND g.status='succeeded'
 JOIN public_works source ON source.id=g.source_work_id WHERE NOT source.id=ANY(l.path)
 ) SELECT l.id,l.title,u.id,u.handle,u.display_name FROM lineage l JOIN users u ON u.id=l.author_id ORDER BY l.depth`, id)
	if err != nil {
		return Work{}, err
	}
	for rows.Next() {
		var item WorkReference
		if err := rows.Scan(&item.ID, &item.Title, &item.Author.ID, &item.Author.Handle, &item.Author.DisplayName); err != nil {
			rows.Close()
			return Work{}, err
		}
		work.Sources = append(work.Sources, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Work{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Work{}, err
	}
	return work, nil
}

func (r *Repository) Search(ctx context.Context, filter SearchFilter) (SearchPage, error) {
	query, types, page, limit, err := normalizeSearchFilter(filter)
	if err != nil {
		return SearchPage{}, err
	}
	policy, variant, err := r.activeRankingPolicy(ctx, query+"|"+strings.Join(types, ","))
	if err != nil {
		return SearchPage{}, err
	}
	return r.searchWithPolicy(ctx, query, types, page, limit, policy, variant)
}

func (r *Repository) SearchWithPolicy(ctx context.Context, filter SearchFilter, policy RankingPolicy) (SearchPage, error) {
	query, types, page, limit, err := normalizeSearchFilter(filter)
	if err != nil {
		return SearchPage{}, err
	}
	return r.searchWithPolicy(ctx, query, types, page, limit, policy, "evaluation")
}

func normalizeSearchFilter(filter SearchFilter) (string, []string, int, int, error) {
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) < 2 || utf8.RuneCountInString(query) > 120 {
		return "", nil, 0, 0, ErrInvalidQuery
	}
	allowed := map[string]bool{"work": true, "creator": true, "product": true, "demand": true}
	types := make([]string, 0, len(filter.Types))
	seen := map[string]bool{}
	for _, kind := range filter.Types {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if !allowed[kind] {
			return "", nil, 0, 0, ErrInvalidQuery
		}
		if !seen[kind] {
			types = append(types, kind)
			seen[kind] = true
		}
	}
	if filter.Page == 0 {
		filter.Page = 1
	}
	if filter.Page < 1 || filter.Page > 100 {
		return "", nil, 0, 0, ErrInvalidQuery
	}
	if filter.Limit == 0 {
		filter.Limit = 12
	}
	if filter.Limit < 1 || filter.Limit > 24 {
		return "", nil, 0, 0, ErrInvalidQuery
	}
	return query, types, filter.Page, filter.Limit, nil
}

func (r *Repository) searchWithPolicy(ctx context.Context, query string, types []string, page, limit int, policy RankingPolicy, variant string) (SearchPage, error) {
	offset := (page - 1) * limit
	rows, err := r.pool.Query(ctx, searchQuery, query, types, limit, offset,
		policy.TitleExactWeight, policy.TitlePrefixWeight, policy.TitleContainsWeight,
		policy.CreatorExactWeight, policy.CreatorMatchWeight, policy.BodyMatchWeight, policy.SecondaryMatchWeight,
		policy.RecencyWeight, policy.CreatorActivityWeight, policy.WorkTypeBoost, policy.CreatorTypeBoost,
		policy.ProductTypeBoost, policy.DemandTypeBoost, literalLike(query))
	if err != nil {
		return SearchPage{}, fmt.Errorf("search discovery: %w", err)
	}
	defer rows.Close()
	items := make([]SearchResult, 0, limit)
	total := 0
	for rows.Next() {
		var item SearchResult
		if err := rows.Scan(&item.Type, &item.ID, &item.Title, &item.Summary, &item.Path, &item.MediaURL,
			&item.MediaKind, &item.CreatorHandle, &item.PriceCents, &item.Currency, &item.PublishedAt,
			&item.Rank, &item.RankSignals, &total); err != nil {
			return SearchPage{}, fmt.Errorf("scan discovery search: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return SearchPage{}, fmt.Errorf("iterate discovery search: %w", err)
	}
	if len(items) == 0 && page > 1 {
		firstPage, err := r.searchWithPolicy(ctx, query, types, 1, limit, policy, variant)
		if err != nil {
			return SearchPage{}, err
		}
		total = firstPage.Total
	}
	return SearchPage{
		Query: query, Items: items, Page: page, Limit: limit, Total: total,
		HasMore: offset+len(items) < total, PolicyVersion: policy.Version, PolicyName: policy.Name, PolicyVariant: variant,
	}, nil
}

func (r *Repository) activeRankingPolicy(ctx context.Context, rolloutKey string) (RankingPolicy, string, error) {
	var activeID uuid.UUID
	var candidateID *uuid.UUID
	var rolloutPercent int
	if err := r.pool.QueryRow(ctx, `SELECT active_revision_id,candidate_revision_id,rollout_percent FROM discovery_ranking_state WHERE singleton=true`).Scan(&activeID, &candidateID, &rolloutPercent); err != nil {
		return RankingPolicy{}, "", fmt.Errorf("load discovery ranking state: %w", err)
	}
	selectedID := activeID
	variant := "baseline"
	if candidateID != nil && rolloutPercent > 0 {
		bucket := rolloutBucket(rolloutKey)
		if bucket < rolloutPercent {
			selectedID = *candidateID
			variant = "candidate"
		}
	}
	var policy RankingPolicy
	err := r.pool.QueryRow(ctx, `
		SELECT r.version,r.name,r.title_exact_weight,r.title_prefix_weight,r.title_contains_weight,
		       r.creator_exact_weight,r.creator_match_weight,r.body_match_weight,r.secondary_match_weight,
		       r.recency_weight,r.creator_activity_weight,r.work_type_boost,r.creator_type_boost,
		       r.product_type_boost,r.demand_type_boost
		FROM discovery_ranking_revisions r WHERE r.id=$1`, selectedID).Scan(
		&policy.Version, &policy.Name, &policy.TitleExactWeight, &policy.TitlePrefixWeight, &policy.TitleContainsWeight,
		&policy.CreatorExactWeight, &policy.CreatorMatchWeight, &policy.BodyMatchWeight, &policy.SecondaryMatchWeight,
		&policy.RecencyWeight, &policy.CreatorActivityWeight, &policy.WorkTypeBoost, &policy.CreatorTypeBoost,
		&policy.ProductTypeBoost, &policy.DemandTypeBoost,
	)
	if err != nil {
		return RankingPolicy{}, "", fmt.Errorf("load discovery ranking policy: %w", err)
	}
	return policy, variant, nil
}

func rolloutBucket(key string) int {
	digest := sha256.Sum256([]byte(key))
	return int(digest[0]) * 100 / 256
}

func (r *Repository) Creator(ctx context.Context, handle string, viewerID uuid.UUID, filters ...CreatorFilter) (CreatorProfile, error) {
	f := CreatorFilter{1, 1, 12}
	if len(filters) > 0 {
		f = filters[0]
	}
	if f.WorksPage < 1 || f.ProductsPage < 1 || f.WorksPage > 1000000 || f.ProductsPage > 1000000 || f.Limit < 1 || f.Limit > 24 {
		return CreatorProfile{}, ErrInvalidQuery
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return CreatorProfile{}, err
	}
	defer tx.Rollback(ctx)
	handle = strings.TrimSpace(handle)
	if len(handle) < 2 || len(handle) > 40 {
		return CreatorProfile{}, ErrNotFound
	}
	var profile CreatorProfile
	err = tx.QueryRow(ctx, `
		SELECT u.id,u.handle,u.display_name,u.role,u.created_at,
		       (SELECT count(*) FROM user_follows f JOIN users x ON x.id=f.follower_id AND x.status='active' WHERE f.following_id=u.id),
		       (SELECT count(*) FROM user_follows f JOIN users x ON x.id=f.following_id AND x.status='active' WHERE f.follower_id=u.id),
		       EXISTS(SELECT 1 FROM user_follows f WHERE f.follower_id=$2 AND f.following_id=u.id)
		FROM users u
		WHERE lower(u.handle)=lower($1) AND u.status='active'`, handle, viewerID).
		Scan(&profile.ID, &profile.Handle, &profile.DisplayName, &profile.Role, &profile.MemberSince,
			&profile.FollowerCount, &profile.FollowingCount, &profile.ViewerFollowing)
	if errors.Is(err, pgx.ErrNoRows) {
		return CreatorProfile{}, ErrNotFound
	}
	if err != nil {
		return CreatorProfile{}, fmt.Errorf("get creator: %w", err)
	}
	works, err := r.creatorWorks(ctx, tx, profile.ID, f)
	if err != nil {
		return CreatorProfile{}, err
	}
	products, err := r.creatorProducts(ctx, tx, profile.ID, f)
	if err != nil {
		return CreatorProfile{}, err
	}
	profile.Works = works
	profile.Products = products
	profile.WorksPage = f.WorksPage
	profile.ProductsPage = f.ProductsPage
	profile.Limit = f.Limit
	err = tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM public_works WHERE author_id=$1),
 (SELECT count(*) FROM public_products p WHERE p.seller_id=$1)`, profile.ID).Scan(&profile.WorksTotal, &profile.ProductsTotal)
	if err != nil {
		return CreatorProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreatorProfile{}, err
	}
	return profile, nil
}

func (r *Repository) creatorWorks(ctx context.Context, tx pgx.Tx, creatorID uuid.UUID, f CreatorFilter) ([]Work, error) {
	rows, err := tx.Query(ctx, `
		SELECT w.id,w.title,w.summary,w.prompt_visibility,w.model_name,w.ai_disclosure,w.published_at,
		       a.id,a.media_url,a.kind,a.width,a.height,a.license_code,u.id,u.handle,u.display_name
		FROM public_works w JOIN assets a ON a.id=w.asset_id AND a.scan_status='clean' JOIN users u ON u.id=w.author_id
		WHERE w.author_id=$1 AND w.status='published' ORDER BY w.published_at DESC,w.id DESC LIMIT $2 OFFSET $3`, creatorID, f.Limit, (f.WorksPage-1)*f.Limit)
	if err != nil {
		return nil, fmt.Errorf("list creator works: %w", err)
	}
	defer rows.Close()
	items := make([]Work, 0)
	for rows.Next() {
		item, err := scanWork(rows, false)
		if err != nil {
			return nil, fmt.Errorf("scan creator work: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) creatorProducts(ctx context.Context, tx pgx.Tx, creatorID uuid.UUID, f CreatorFilter) ([]CreatorProduct, error) {
	rows, err := tx.Query(ctx, `
		SELECT p.id,p.title,p.description,p.product_type,p.price_cents,p.currency,p.license_code,COALESCE(preview.media_url,''),COALESCE(preview.kind,a.kind),p.ai_disclosure
		FROM public_products p JOIN assets a ON a.id=p.asset_id
		LEFT JOIN public_product_previews preview ON preview.product_id=p.id
		WHERE p.seller_id=$1 ORDER BY p.created_at DESC,p.id DESC LIMIT $2 OFFSET $3`, creatorID, f.Limit, (f.ProductsPage-1)*f.Limit)
	if err != nil {
		return nil, fmt.Errorf("list creator products: %w", err)
	}
	defer rows.Close()
	items := make([]CreatorProduct, 0)
	for rows.Next() {
		var item CreatorProduct
		if err := rows.Scan(&item.ID, &item.Title, &item.Description, &item.ProductType, &item.PriceCents,
			&item.Currency, &item.LicenseCode, &item.MediaURL, &item.MediaKind, &item.AIDisclosure); err != nil {
			return nil, fmt.Errorf("scan creator product: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const searchQuery = `
WITH candidates AS (
  SELECT 'work'::text kind,w.id,w.title,w.summary,'/works/'||w.id::text path,a.media_url,a.kind media_kind,u.handle,
         NULL::integer price_cents,NULL::text currency,w.published_at,
         (CASE WHEN lower(w.title)=$1 THEN $5 WHEN lower(w.title) LIKE $18||'%' THEN $6 WHEN lower(w.title) LIKE '%'||$18||'%' THEN $7 ELSE 0 END
          + CASE WHEN lower(u.handle)=$1 OR lower(u.display_name)=$1 THEN $8 WHEN lower(u.handle||' '||u.display_name) LIKE '%'||$18||'%' THEN $9 ELSE 0 END
          + CASE WHEN lower(w.summary) LIKE '%'||$18||'%' THEN $10 ELSE 0 END
          + CASE WHEN w.prompt_visibility='public' AND lower(COALESCE(w.prompt,'')) LIKE '%'||$18||'%' THEN $11 ELSE 0 END
          + GREATEST(0,$12::integer-LEAST($12::integer,floor(EXTRACT(EPOCH FROM (now()-w.published_at))/2592000)::int)) + $14) rank,
         array_remove(ARRAY[
           CASE WHEN lower(w.title) LIKE '%'||$18||'%' THEN 'title_match' END,
           CASE WHEN lower(u.handle||' '||u.display_name) LIKE '%'||$18||'%' THEN 'creator_match' END,
           CASE WHEN lower(w.summary) LIKE '%'||$18||'%' THEN 'summary_match' END,
           CASE WHEN w.prompt_visibility='public' AND lower(COALESCE(w.prompt,'')) LIKE '%'||$18||'%' THEN 'public_prompt_match' END,
           CASE WHEN w.published_at > now()-interval '30 days' THEN 'recently_published' END],NULL) rank_signals
  FROM public_works w JOIN assets a ON a.id=w.asset_id AND a.scan_status='clean' JOIN users u ON u.id=w.author_id AND u.status='active'
  WHERE w.status='published' AND lower(w.title||' '||w.summary||' '||u.handle||' '||u.display_name||' '||CASE WHEN w.prompt_visibility='public' THEN COALESCE(w.prompt,'') ELSE '' END) LIKE '%'||$18||'%'
  UNION ALL
  SELECT 'creator',u.id,u.display_name,'@'||u.handle||' · '||count(DISTINCT w.id)::text||' published works','/creators/'||u.handle,
         (array_agg(a.media_url ORDER BY w.published_at DESC) FILTER (WHERE a.media_url IS NOT NULL))[1],
         (array_agg(a.kind ORDER BY w.published_at DESC) FILTER (WHERE a.kind IS NOT NULL))[1],u.handle,NULL,NULL,max(w.published_at),
         (CASE WHEN lower(u.handle)=$1 THEN $5::integer+10 WHEN lower(u.display_name)=$1 THEN $5 WHEN lower(u.handle) LIKE $18||'%' THEN $6::integer+5 ELSE GREATEST(0,$7::integer-5) END
          + LEAST($13::integer,count(DISTINCT w.id)::int*3+count(DISTINCT p.id)::int*2) + $15),
         array_remove(ARRAY[CASE WHEN lower(u.handle) LIKE '%'||$18||'%' THEN 'handle_match' END,CASE WHEN lower(u.display_name) LIKE '%'||$18||'%' THEN 'display_name_match' END,CASE WHEN count(DISTINCT w.id)>0 THEN 'published_creator' END],NULL)
  FROM users u
  LEFT JOIN public_works w ON w.author_id=u.id AND w.status='published'
  LEFT JOIN assets a ON a.id=w.asset_id AND a.scan_status='clean'
  LEFT JOIN public_products p ON p.seller_id=u.id
  WHERE u.status='active' AND lower(u.handle||' '||u.display_name) LIKE '%'||$18||'%'
  GROUP BY u.id HAVING count(a.id)>0 OR count(p.id)>0
  UNION ALL
  SELECT 'product',p.id,p.title,p.description,'/market/assets/'||p.id::text,preview.media_url,COALESCE(preview.kind,a.kind),u.handle,p.price_cents,p.currency,p.created_at,
         (CASE WHEN lower(p.title)=$1 THEN $5 WHEN lower(p.title) LIKE $18||'%' THEN $6 WHEN lower(p.title) LIKE '%'||$18||'%' THEN $7 ELSE 0 END
          + CASE WHEN lower(u.handle||' '||u.display_name) LIKE '%'||$18||'%' THEN $9 ELSE 0 END
          + CASE WHEN lower(p.description) LIKE '%'||$18||'%' THEN $10 ELSE 0 END + $16),
         array_remove(ARRAY[CASE WHEN lower(p.title) LIKE '%'||$18||'%' THEN 'title_match' END,CASE WHEN lower(u.handle||' '||u.display_name) LIKE '%'||$18||'%' THEN 'creator_match' END,CASE WHEN lower(p.description) LIKE '%'||$18||'%' THEN 'description_match' END],NULL)
  FROM public_products p JOIN assets a ON a.id=p.asset_id JOIN users u ON u.id=p.seller_id
  LEFT JOIN public_product_previews preview ON preview.product_id=p.id
  WHERE lower(p.title||' '||p.description||' '||u.handle||' '||u.display_name) LIKE '%'||$18||'%'
  UNION ALL
  SELECT 'demand',d.id,d.title,d.summary,'/market/demands/'||d.id::text,NULL,NULL,u.handle,d.budget_cents,d.currency,d.created_at,
         (CASE WHEN lower(d.title)=$1 THEN $5 WHEN lower(d.title) LIKE $18||'%' THEN $6 WHEN lower(d.title) LIKE '%'||$18||'%' THEN $7 ELSE 0 END
          + CASE WHEN lower(d.summary) LIKE '%'||$18||'%' THEN $10 ELSE 0 END
          + CASE WHEN lower(d.brief) LIKE '%'||$18||'%' THEN $11::integer+3 ELSE 0 END + $17),
         array_remove(ARRAY[CASE WHEN lower(d.title) LIKE '%'||$18||'%' THEN 'title_match' END,CASE WHEN lower(d.summary) LIKE '%'||$18||'%' THEN 'summary_match' END,CASE WHEN lower(d.brief) LIKE '%'||$18||'%' THEN 'brief_match' END],NULL)
  FROM demands d JOIN users u ON u.id=d.client_id AND u.status='active'
  WHERE d.status='open' AND lower(d.title||' '||d.summary||' '||d.brief||' '||u.handle||' '||u.display_name) LIKE '%'||$18||'%'
), filtered AS (
  SELECT * FROM candidates WHERE cardinality($2::text[])=0 OR kind=ANY($2::text[])
), counted AS (
  SELECT *,count(*) OVER() total FROM filtered
)
SELECT kind,id,title,summary,path,media_url,media_kind,handle,price_cents,currency,published_at,rank,rank_signals,total
FROM counted ORDER BY rank DESC,published_at DESC NULLS LAST,id DESC LIMIT $3 OFFSET $4`

type scanner interface {
	Scan(...any) error
}

func scanWork(row scanner, includePrompt bool) (Work, error) {
	var work Work
	var err error
	if includePrompt {
		err = row.Scan(&work.ID, &work.Title, &work.Summary, &work.Prompt, &work.PromptVisibility, &work.ModelName,
			&work.AIDisclosure, &work.PublishedAt, &work.AssetID, &work.MediaURL, &work.MediaKind, &work.Width, &work.Height,
			&work.LicenseCode, &work.Author.ID, &work.Author.Handle, &work.Author.DisplayName)
	} else {
		err = row.Scan(&work.ID, &work.Title, &work.Summary, &work.PromptVisibility, &work.ModelName,
			&work.AIDisclosure, &work.PublishedAt, &work.AssetID, &work.MediaURL, &work.MediaKind, &work.Width, &work.Height,
			&work.LicenseCode, &work.Author.ID, &work.Author.Handle, &work.Author.DisplayName)
	}
	return work, err
}
