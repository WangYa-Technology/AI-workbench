package community

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalid            = errors.New("invalid publication")
	ErrInvalidDraftFilter = errors.New("invalid content draft filter")
	ErrInvalidPostFilter  = errors.New("invalid post filter")
	ErrForbidden          = errors.New("publication forbidden")
	ErrConflict           = errors.New("asset is already published")
)

type PublishInput struct {
	Category         string    `json:"category"`
	AssetID          uuid.UUID `json:"assetId"`
	Title            string    `json:"title"`
	Summary          string    `json:"summary"`
	Prompt           string    `json:"prompt"`
	PromptVisibility string    `json:"promptVisibility"`
	AIDisclosure     string    `json:"aiDisclosure"`
	Body             string    `json:"body"`
}

type Publication struct {
	WorkID uuid.UUID `json:"workId"`
	PostID uuid.UUID `json:"postId"`
}

type PostCreateInput struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Category string `json:"category"`
}

type DraftInput struct {
	PublishInput
	ExpectedVersion int `json:"expectedVersion"`
}

type Draft struct {
	Category         string    `json:"category"`
	ID               uuid.UUID `json:"id"`
	PostID           uuid.UUID `json:"postId"`
	AssetID          uuid.UUID `json:"assetId"`
	AssetTitle       string    `json:"assetTitle"`
	AssetMediaURL    string    `json:"assetMediaUrl"`
	AssetKind        string    `json:"assetKind"`
	AssetScanStatus  string    `json:"assetScanStatus"`
	Title            string    `json:"title"`
	Summary          string    `json:"summary"`
	Prompt           string    `json:"prompt"`
	PromptVisibility string    `json:"promptVisibility"`
	AIDisclosure     string    `json:"aiDisclosure"`
	Body             string    `json:"body"`
	Version          int       `json:"version"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type DraftListInput struct {
	Cursor string
	Limit  int
}

type DraftPage struct {
	Items      []Draft `json:"items"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

type draftCursor struct {
	UpdatedAt time.Time `json:"updatedAt"`
	ID        uuid.UUID `json:"id"`
}

type Post struct {
	Category         string     `json:"category"`
	ID               uuid.UUID  `json:"id"`
	Title            string     `json:"title"`
	Body             string     `json:"body"`
	PublishedAt      time.Time  `json:"publishedAt"`
	WorkID           *uuid.UUID `json:"workId,omitempty"`
	WorkTitle        *string    `json:"workTitle,omitempty"`
	MediaURL         *string    `json:"mediaUrl,omitempty"`
	MediaKind        *string    `json:"mediaKind,omitempty"`
	AIDisclosure     *string    `json:"aiDisclosure,omitempty"`
	AuthorID         uuid.UUID  `json:"authorId"`
	AuthorHandle     string     `json:"authorHandle"`
	AuthorName       string     `json:"authorName"`
	CommentCount     int        `json:"commentCount"`
	LikeCount        int        `json:"likeCount"`
	BookmarkCount    int        `json:"bookmarkCount"`
	ViewerLiked      bool       `json:"viewerLiked"`
	ViewerBookmarked bool       `json:"viewerBookmarked"`
	ViewerFollowing  bool       `json:"viewerFollowing"`
}

type PostListInput struct {
	Category string
	Cursor   string
	Limit    int
	Mine     bool
}

type PostPage struct {
	Items      []Post  `json:"items"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

type postCursor struct {
	PublishedAt time.Time `json:"publishedAt"`
	ID          uuid.UUID `json:"id"`
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Publish(ctx context.Context, authorID uuid.UUID, input PublishInput) (Publication, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.AIDisclosure = strings.TrimSpace(input.AIDisclosure)
	input.Body = strings.TrimSpace(input.Body)
	if input.AssetID == uuid.Nil || len(input.Title) < 3 || len(input.Title) > 120 || len(input.Summary) > 500 ||
		len(input.Prompt) > 2000 || len(input.AIDisclosure) < 10 || len(input.AIDisclosure) > 500 || len(input.Body) > 2000 {
		return Publication{}, ErrInvalid
	}
	if input.PromptVisibility == "" {
		input.PromptVisibility = "public"
	}
	if input.PromptVisibility != "public" && input.PromptVisibility != "partial" && input.PromptVisibility != "private" {
		return Publication{}, ErrInvalid
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Publication{}, fmt.Errorf("begin publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Publishing); err != nil {
		return Publication{}, err
	}

	var ownerID uuid.UUID
	var scanStatus, sourceType string
	var modelName *string
	err = tx.QueryRow(ctx, `
		SELECT a.owner_id,a.scan_status,a.source_type,
		       (SELECT g.model_name FROM generations g WHERE g.output_asset_id=a.id AND g.status='succeeded' LIMIT 1)
		FROM assets a WHERE a.id=$1 FOR UPDATE`, input.AssetID).Scan(&ownerID, &scanStatus, &sourceType, &modelName)
	if errors.Is(err, pgx.ErrNoRows) || ownerID != authorID || scanStatus != "clean" || sourceType == "purchase" {
		return Publication{}, ErrForbidden
	}
	if err != nil {
		return Publication{}, fmt.Errorf("load publication asset: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM works WHERE asset_id=$1 AND status='published')`, input.AssetID).Scan(&exists); err != nil {
		return Publication{}, fmt.Errorf("check publication: %w", err)
	}
	if exists {
		return Publication{}, ErrConflict
	}
	if modelName == nil {
		value := "Imported asset"
		modelName = &value
	}

	publication := Publication{WorkID: uuid.New(), PostID: uuid.New()}
	_, err = tx.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,prompt,prompt_visibility,model_name,status,ai_disclosure,published_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'published',$9,now())`,
		publication.WorkID, authorID, input.AssetID, input.Title, input.Summary, nullable(input.Prompt), input.PromptVisibility, *modelName, input.AIDisclosure)
	if err != nil {
		return Publication{}, fmt.Errorf("insert work: %w", err)
	}
	body := input.Body
	if body == "" {
		body = input.Summary
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO posts(id,author_id,work_id,title,body,status,published_at,category)
		VALUES($1,$2,$3,$4,$5,'published',now(),$6)`, publication.PostID, authorID, publication.WorkID, input.Title, body, input.Category)
	if err != nil {
		return Publication{}, fmt.Errorf("insert post: %w", err)
	}
	if err := webhooks.EnqueueTx(ctx, tx, webhooks.EventInput{OwnerID: authorID, EventType: "work.published", ResourceType: "work", ResourceID: &publication.WorkID, SourceKey: "work:" + publication.WorkID.String() + ":published"}); err != nil {
		return Publication{}, fmt.Errorf("enqueue publication webhook: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Publication{}, fmt.Errorf("commit publication: %w", err)
	}
	return publication, nil
}

func (r *Repository) CreatePost(ctx context.Context, authorID uuid.UUID, input PostCreateInput) (Post, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	if authorID == uuid.Nil || len(input.Title) < 3 || len(input.Title) > 120 || len(input.Body) < 2 || len(input.Body) > 2000 {
		return Post{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Post{}, fmt.Errorf("begin Community post: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Publishing); err != nil {
		return Post{}, err
	}
	postID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO posts(id,author_id,title,body,status,published_at,category)
		VALUES($1,$2,$3,$4,'published',now(),$5)`, postID, authorID, input.Title, input.Body, input.Category); err != nil {
		return Post{}, fmt.Errorf("insert Community post: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Post{}, fmt.Errorf("commit Community post: %w", err)
	}
	return r.GetPostForViewer(ctx, authorID, postID)
}

func (r *Repository) ListDrafts(ctx context.Context, authorID uuid.UUID, input DraftListInput) (DraftPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return DraftPage{}, ErrInvalidDraftFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeDraftCursor(input.Cursor)
		if err != nil {
			return DraftPage{}, err
		}
		cursorTime, cursorID = &cursor.UpdatedAt, &cursor.ID
	}
	rows, err := r.pool.Query(ctx, draftSelect+`
		WHERE w.author_id=$1 AND w.status='draft' AND p.status='draft'
		  AND ($2::timestamptz IS NULL OR (w.updated_at,w.id)<($2,$3::uuid))
		ORDER BY w.updated_at DESC,w.id DESC LIMIT $4`, authorID, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return DraftPage{}, fmt.Errorf("list content drafts: %w", err)
	}
	defer rows.Close()
	items := make([]Draft, 0)
	for rows.Next() {
		item, err := scanDraft(rows)
		if err != nil {
			return DraftPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return DraftPage{}, err
	}
	page := DraftPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeDraftCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func (r *Repository) GetDraft(ctx context.Context, authorID, draftID uuid.UUID) (Draft, error) {
	item, err := scanDraft(r.pool.QueryRow(ctx, draftSelect+` WHERE w.id=$1 AND w.author_id=$2 AND w.status='draft' AND p.status='draft'`, draftID, authorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) SaveDraft(ctx context.Context, authorID uuid.UUID, draftID *uuid.UUID, input DraftInput, requestID string) (Draft, error) {
	normalizeDraftInput(&input)
	if !validDraftInput(input) || draftID != nil && input.ExpectedVersion < 1 {
		return Draft{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Draft{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	modelName, err := draftAsset(ctx, tx, authorID, input.AssetID, false)
	if err != nil {
		return Draft{}, err
	}
	workID, postID := uuid.New(), uuid.New()
	action := "content.draft_created"
	if draftID == nil {
		_, err = tx.Exec(ctx, `
			INSERT INTO works(id,author_id,asset_id,title,summary,prompt,prompt_visibility,model_name,status,ai_disclosure)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,'draft',$9)`, workID, authorID, input.AssetID, input.Title, input.Summary,
			nullable(input.Prompt), input.PromptVisibility, modelName, input.AIDisclosure)
		if isUniqueViolation(err, "works_owner_asset_active_draft_idx") {
			return Draft{}, ErrConflict
		}
		if err != nil {
			return Draft{}, fmt.Errorf("create content draft work: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO posts(id,author_id,work_id,title,body,status,category) VALUES($1,$2,$3,$4,$5,'draft',$6)`, postID, authorID, workID, input.Title, input.Body, input.Category); err != nil {
			return Draft{}, fmt.Errorf("create content draft post: %w", err)
		}
	} else {
		workID = *draftID
		var version int
		if err := tx.QueryRow(ctx, `SELECT w.version,p.id FROM works w JOIN posts p ON p.work_id=w.id WHERE w.id=$1 AND w.author_id=$2 AND w.status='draft' AND p.status='draft' FOR UPDATE OF w,p`, workID, authorID).Scan(&version, &postID); errors.Is(err, pgx.ErrNoRows) {
			return Draft{}, ErrNotFound
		} else if err != nil {
			return Draft{}, err
		}
		if version != input.ExpectedVersion {
			return Draft{}, ErrConflict
		}
		result, err := tx.Exec(ctx, `UPDATE works SET asset_id=$2,title=$3,summary=$4,prompt=$5,prompt_visibility=$6,model_name=$7,ai_disclosure=$8,version=version+1,updated_at=now() WHERE id=$1 AND version=$9`, workID, input.AssetID, input.Title, input.Summary, nullable(input.Prompt), input.PromptVisibility, modelName, input.AIDisclosure, input.ExpectedVersion)
		if err != nil {
			return Draft{}, err
		}
		if result.RowsAffected() != 1 {
			return Draft{}, ErrConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE posts SET title=$2,body=$3,category=CASE WHEN $4='' THEN category ELSE $4 END,version=version+1,updated_at=now() WHERE id=$1`, postID, input.Title, input.Body, input.Category); err != nil {
			return Draft{}, err
		}
		action = "content.draft_updated"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,$2,'work',$3,'Owner saved a private content draft',$4,jsonb_build_object('assetId',$5::text))`, authorID, action, workID, safeRequestID(requestID), input.AssetID); err != nil {
		return Draft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Draft{}, err
	}
	return r.GetDraft(ctx, authorID, workID)
}

func (r *Repository) PublishDraft(ctx context.Context, authorID, draftID uuid.UUID, expectedVersion int, requestID string) (Publication, error) {
	if expectedVersion < 1 {
		return Publication{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Publication{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Publishing); err != nil {
		return Publication{}, err
	}
	var input PublishInput
	var postID uuid.UUID
	var version int
	err = tx.QueryRow(ctx, `SELECT w.asset_id,w.title,w.summary,COALESCE(w.prompt,''),w.prompt_visibility,w.ai_disclosure,p.body,w.version,p.id FROM works w JOIN posts p ON p.work_id=w.id WHERE w.id=$1 AND w.author_id=$2 AND w.status='draft' AND p.status='draft' FOR UPDATE OF w,p`, draftID, authorID).Scan(
		&input.AssetID, &input.Title, &input.Summary, &input.Prompt, &input.PromptVisibility, &input.AIDisclosure, &input.Body, &version, &postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Publication{}, ErrNotFound
	}
	if err != nil {
		return Publication{}, err
	}
	if version != expectedVersion {
		return Publication{}, ErrConflict
	}
	if !validPublicationInput(input) {
		return Publication{}, ErrInvalid
	}
	if _, err := draftAsset(ctx, tx, authorID, input.AssetID, true); err != nil {
		return Publication{}, err
	}
	var published bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM works WHERE asset_id=$1 AND status='published')`, input.AssetID).Scan(&published); err != nil {
		return Publication{}, err
	}
	if published {
		return Publication{}, ErrConflict
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE works SET status='published',published_at=$2,version=version+1,updated_at=$2 WHERE id=$1`, draftID, now); err != nil {
		return Publication{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE posts SET status='published',published_at=$2,version=version+1,updated_at=$2 WHERE id=$1`, postID, now); err != nil {
		return Publication{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,'content.draft_published','work',$2,'Owner published a persisted content draft',$3,jsonb_build_object('postId',$4::text,'assetId',$5::text,'expectedVersion',$6::integer))`, authorID, draftID, safeRequestID(requestID), postID, input.AssetID, expectedVersion); err != nil {
		return Publication{}, err
	}
	if err := webhooks.EnqueueTx(ctx, tx, webhooks.EventInput{OwnerID: authorID, EventType: "work.published", ResourceType: "work", ResourceID: &draftID, SourceKey: "work:" + draftID.String() + ":published"}); err != nil {
		return Publication{}, fmt.Errorf("enqueue draft publication webhook: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Publication{}, err
	}
	return Publication{WorkID: draftID, PostID: postID}, nil
}

func (r *Repository) DiscardDraft(ctx context.Context, authorID, draftID uuid.UUID, expectedVersion int, requestID string) error {
	if expectedVersion < 1 {
		return ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var postID uuid.UUID
	var version int
	if err := tx.QueryRow(ctx, `SELECT w.version,p.id FROM works w JOIN posts p ON p.work_id=w.id WHERE w.id=$1 AND w.author_id=$2 AND w.status='draft' AND p.status='draft' FOR UPDATE OF w,p`, draftID, authorID).Scan(&version, &postID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if version != expectedVersion {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE works SET status='removed',version=version+1,updated_at=now() WHERE id=$1`, draftID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE posts SET status='removed',version=version+1,updated_at=now() WHERE id=$1`, postID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id) VALUES($1,'content.draft_discarded','work',$2,'Owner discarded a private content draft',$3)`, authorID, draftID, safeRequestID(requestID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const draftSelect = `
	SELECT w.id,p.id,w.asset_id,a.title,a.media_url,a.kind,a.scan_status,w.title,w.summary,COALESCE(w.prompt,''),w.prompt_visibility,
	       w.ai_disclosure,p.body,p.category,w.version,w.created_at,w.updated_at
	FROM works w JOIN posts p ON p.work_id=w.id JOIN assets a ON a.id=w.asset_id`

func scanDraft(row interface{ Scan(...any) error }) (Draft, error) {
	var item Draft
	err := row.Scan(&item.ID, &item.PostID, &item.AssetID, &item.AssetTitle, &item.AssetMediaURL, &item.AssetKind, &item.AssetScanStatus,
		&item.Title, &item.Summary, &item.Prompt, &item.PromptVisibility, &item.AIDisclosure, &item.Body, &item.Category, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func normalizeDraftInput(input *DraftInput) {
	input.Title = strings.TrimSpace(input.Title)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.AIDisclosure = strings.TrimSpace(input.AIDisclosure)
	input.Body = strings.TrimSpace(input.Body)
	if input.PromptVisibility == "" {
		input.PromptVisibility = "public"
	}
}

func validDraftInput(input DraftInput) bool {
	return input.AssetID != uuid.Nil && len(input.Title) <= 120 && len(input.Summary) <= 500 && len(input.Prompt) <= 2000 &&
		len(input.AIDisclosure) <= 500 && len(input.Body) <= 2000 && oneOf(input.PromptVisibility, "public", "partial", "private")
}

func validPublicationInput(input PublishInput) bool {
	return input.AssetID != uuid.Nil && len(strings.TrimSpace(input.Title)) >= 3 && len(input.Title) <= 120 && len(input.Summary) <= 500 &&
		len(input.Prompt) <= 2000 && len(strings.TrimSpace(input.AIDisclosure)) >= 10 && len(input.AIDisclosure) <= 500 &&
		len(input.Body) <= 2000 && oneOf(input.PromptVisibility, "public", "partial", "private")
}

func draftAsset(ctx context.Context, tx pgx.Tx, authorID, assetID uuid.UUID, requireClean bool) (string, error) {
	var ownerID uuid.UUID
	var scanStatus, sourceType, modelName string
	err := tx.QueryRow(ctx, `SELECT a.owner_id,a.scan_status,a.source_type,COALESCE((SELECT g.model_name FROM generations g WHERE g.output_asset_id=a.id AND g.status='succeeded' LIMIT 1),'Imported asset') FROM assets a WHERE a.id=$1 FOR UPDATE`, assetID).Scan(&ownerID, &scanStatus, &sourceType, &modelName)
	if errors.Is(err, pgx.ErrNoRows) || ownerID != authorID || sourceType == "purchase" || requireClean && scanStatus != "clean" {
		return "", ErrForbidden
	}
	if err != nil {
		return "", err
	}
	return modelName, nil
}

func safeRequestID(value string) string {
	if strings.TrimSpace(value) == "" {
		return "content-draft"
	}
	return value
}

func (r *Repository) List(ctx context.Context) ([]Post, error) {
	return r.ListForViewer(ctx, uuid.Nil)
}

func (r *Repository) ListForViewer(ctx context.Context, viewerID uuid.UUID) ([]Post, error) {
	page, err := r.ListPageForViewer(ctx, viewerID, PostListInput{Limit: 50})
	return page.Items, err
}

func (r *Repository) GetPostForViewer(ctx context.Context, viewerID, postID uuid.UUID) (Post, error) {
	var item Post
	err := r.pool.QueryRow(ctx, `
		SELECT p.id,p.category,COALESCE(p.title,w.title,'Community post'),p.body,p.published_at,w.id,w.title,a.media_url,a.kind,w.ai_disclosure,u.id,u.handle,u.display_name,
		       (SELECT count(*) FROM comments c WHERE c.post_id=p.id AND c.status='published'),
		       (SELECT count(*) FROM post_reactions pr WHERE pr.post_id=p.id AND pr.kind='like'),
		       (SELECT count(*) FROM post_reactions pr WHERE pr.post_id=p.id AND pr.kind='bookmark'),
		       EXISTS(SELECT 1 FROM post_reactions pr WHERE pr.post_id=p.id AND pr.user_id=$1 AND pr.kind='like'),
		       EXISTS(SELECT 1 FROM post_reactions pr WHERE pr.post_id=p.id AND pr.user_id=$1 AND pr.kind='bookmark'),
		       EXISTS(SELECT 1 FROM user_follows f WHERE f.follower_id=$1 AND f.following_id=u.id)
		FROM posts p
		LEFT JOIN works w ON w.id=p.work_id
		LEFT JOIN assets a ON a.id=w.asset_id
		JOIN users u ON u.id=p.author_id
		WHERE p.id=$2 AND p.status='published'
		  AND (p.work_id IS NULL OR (w.status='published' AND a.scan_status='clean'))`, viewerID, postID).Scan(
		&item.ID, &item.Category, &item.Title, &item.Body, &item.PublishedAt, &item.WorkID, &item.WorkTitle, &item.MediaURL,
		&item.MediaKind, &item.AIDisclosure, &item.AuthorID, &item.AuthorHandle, &item.AuthorName,
		&item.CommentCount, &item.LikeCount, &item.BookmarkCount, &item.ViewerLiked, &item.ViewerBookmarked, &item.ViewerFollowing)
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, ErrNotFound
	}
	if err != nil {
		return Post{}, fmt.Errorf("get post: %w", err)
	}
	return item, nil
}

func (r *Repository) ListPageForViewer(ctx context.Context, viewerID uuid.UUID, input PostListInput) (PostPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return PostPage{}, ErrInvalidPostFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodePostCursor(input.Cursor)
		if err != nil {
			return PostPage{}, err
		}
		cursorTime, cursorID = &cursor.PublishedAt, &cursor.ID
	}
	rows, err := r.pool.Query(ctx, `
		SELECT p.id,p.category,COALESCE(p.title,w.title,'Community post'),p.body,p.published_at,w.id,w.title,a.media_url,a.kind,w.ai_disclosure,u.id,u.handle,u.display_name,
		       (SELECT count(*) FROM comments c WHERE c.post_id=p.id AND c.status='published'),
		       (SELECT count(*) FROM post_reactions pr WHERE pr.post_id=p.id AND pr.kind='like'),
		       (SELECT count(*) FROM post_reactions pr WHERE pr.post_id=p.id AND pr.kind='bookmark'),
		       EXISTS(SELECT 1 FROM post_reactions pr WHERE pr.post_id=p.id AND pr.user_id=$1 AND pr.kind='like'),
		       EXISTS(SELECT 1 FROM post_reactions pr WHERE pr.post_id=p.id AND pr.user_id=$1 AND pr.kind='bookmark'),
		       EXISTS(SELECT 1 FROM user_follows f WHERE f.follower_id=$1 AND f.following_id=u.id)
		FROM posts p
		LEFT JOIN works w ON w.id=p.work_id
		LEFT JOIN assets a ON a.id=w.asset_id
		JOIN users u ON u.id=p.author_id
			WHERE p.status='published'
			  AND (p.work_id IS NULL OR (w.status='published' AND a.scan_status='clean'))
			  AND ($5::boolean = false OR p.author_id=$1)
			  AND ($6='' OR p.category=$6)
			  AND ($2::timestamptz IS NULL OR (p.published_at,p.id)<($2,$3::uuid))
			ORDER BY p.published_at DESC,p.id DESC LIMIT $4`, viewerID, cursorTime, cursorID, input.Limit+1, input.Mine, input.Category)
	if err != nil {
		return PostPage{}, fmt.Errorf("list posts: %w", err)
	}
	defer rows.Close()
	items := make([]Post, 0)
	for rows.Next() {
		var item Post
		if err := rows.Scan(&item.ID, &item.Category, &item.Title, &item.Body, &item.PublishedAt, &item.WorkID, &item.WorkTitle, &item.MediaURL,
			&item.MediaKind, &item.AIDisclosure, &item.AuthorID, &item.AuthorHandle, &item.AuthorName,
			&item.CommentCount, &item.LikeCount, &item.BookmarkCount, &item.ViewerLiked, &item.ViewerBookmarked, &item.ViewerFollowing); err != nil {
			return PostPage{}, fmt.Errorf("scan post: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return PostPage{}, err
	}
	page := PostPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodePostCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func encodeDraftCursor(item Draft) string {
	body, _ := json.Marshal(draftCursor{UpdatedAt: item.UpdatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeDraftCursor(value string) (draftCursor, error) {
	var cursor draftCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.UpdatedAt.IsZero() || cursor.ID == uuid.Nil {
		return draftCursor{}, ErrInvalidDraftFilter
	}
	return cursor, nil
}

func encodePostCursor(item Post) string {
	body, _ := json.Marshal(postCursor{PublishedAt: item.PublishedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodePostCursor(value string) (postCursor, error) {
	var cursor postCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.PublishedAt.IsZero() || cursor.ID == uuid.Nil {
		return postCursor{}, ErrInvalidPostFilter
	}
	return cursor, nil
}
