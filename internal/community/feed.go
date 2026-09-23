package community

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type feedCursor struct {
	Snapshot uuid.UUID `json:"snapshot"`
	Position int64     `json:"position"`
}

// Capture ordering once, including mutable discussion scores. Subsequent pages
// keep the rank but recheck visibility, so withdrawn content is never exposed.
func (r *Repository) ListPageForViewer(ctx context.Context, viewer uuid.UUID, input PostListInput) (PostPage, error) {
	input.Query = strings.TrimSpace(input.Query)
	if input.Sort == "" {
		input.Sort = "latest"
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Mine {
		input.View = "mine"
	}
	if input.View == "" {
		input.View = "all"
	}
	if !oneOf(input.Sort, "latest", "discussed") || !oneOf(input.View, "all", "mine", "saved", "following", "drafts") || input.Limit < 1 || input.Limit > 50 || textLength(input.Query) > 120 {
		return PostPage{}, ErrInvalidPostFilter
	}
	if input.View != "all" && viewer == uuid.Nil {
		return PostPage{}, ErrForbidden
	}
	raw, _ := json.Marshal([]any{viewer, input.Sort, input.Query, input.Category, input.View})
	digest := sha256.Sum256(raw)
	scope := hex.EncodeToString(digest[:])
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return PostPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cursor := feedCursor{Snapshot: uuid.New()}
	if input.Cursor != "" {
		decoded, e := base64.RawURLEncoding.DecodeString(input.Cursor)
		if e != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Snapshot == uuid.Nil || cursor.Position < 0 {
			return PostPage{}, ErrInvalidPostFilter
		}
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM community_feed_snapshots WHERE id=$1 AND scope_hash=$2 AND viewer_id IS NOT DISTINCT FROM $3::uuid AND expires_at>now())`, cursor.Snapshot, scope, nullableViewer(viewer)).Scan(&valid); err != nil {
			return PostPage{}, err
		}
		if !valid {
			return PostPage{}, ErrInvalidPostFilter
		}
	} else {
		if _, err = tx.Exec(ctx, `DELETE FROM community_feed_snapshots WHERE expires_at<now()`); err != nil {
			return PostPage{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO community_feed_snapshots(id,viewer_id,scope_hash) VALUES($1,$2,$3)`, cursor.Snapshot, nullableViewer(viewer), scope); err != nil {
			return PostPage{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO community_feed_snapshot_items(snapshot_id,position,post_id)
   SELECT $1,row_number() OVER(ORDER BY
    CASE WHEN $4='discussed' THEN (SELECT count(*) FROM comments c JOIN users cu ON cu.id=c.author_id AND cu.status='active' WHERE c.post_id=p.id AND c.status='published') END DESC,
    CASE WHEN $4='discussed' THEN (SELECT count(*) FROM post_reactions pr WHERE pr.post_id=p.id AND pr.kind='like') END DESC,
    COALESCE(p.published_at,p.created_at) DESC,p.id DESC),p.id
   FROM posts p JOIN users u ON u.id=p.author_id LEFT JOIN works w ON w.id=p.work_id
   WHERE `+feedVisibility+`
   AND ($5='' OR strpos(lower(concat_ws(' ',p.title,w.title,p.body,u.display_name,u.handle)),lower($5))>0)
   AND ($3<>'mine' OR p.author_id=$2)
   AND ($3<>'saved' OR EXISTS(SELECT 1 FROM post_reactions pr WHERE pr.post_id=p.id AND pr.user_id=$2 AND pr.kind='bookmark'))
   AND ($3<>'following' OR EXISTS(SELECT 1 FROM user_follows f WHERE f.follower_id=$2 AND f.following_id=p.author_id))`, cursor.Snapshot, viewer, input.View, input.Sort, input.Query)
		if err != nil {
			return PostPage{}, err
		}
	}
	page := PostPage{Items: []Post{}, CategoryCounts: map[string]int{}}
	counts, err := tx.Query(ctx, `SELECT p.category,count(*) FROM community_feed_snapshot_items i JOIN posts p ON p.id=i.post_id WHERE i.snapshot_id=$1 AND `+feedVisibility+` GROUP BY p.category`, cursor.Snapshot, viewer, input.View)
	if err != nil {
		return PostPage{}, err
	}
	for counts.Next() {
		var category string
		var count int
		if err = counts.Scan(&category, &count); err != nil {
			counts.Close()
			return PostPage{}, err
		}
		page.CategoryCounts[category] = count
		if input.Category == "" || category == input.Category {
			page.Total += count
		}
	}
	err = counts.Err()
	counts.Close()
	if err != nil {
		return PostPage{}, err
	}
	rows, err := tx.Query(ctx, postProjection+` FROM community_feed_snapshot_items i JOIN posts p ON p.id=i.post_id
 LEFT JOIN works w ON w.id=p.work_id LEFT JOIN assets a ON a.id=w.asset_id JOIN users u ON u.id=p.author_id
 WHERE i.snapshot_id=$1 AND `+feedVisibility+` AND i.position>$4 AND ($5='' OR p.category=$5)
 ORDER BY i.position LIMIT $6`, cursor.Snapshot, viewer, input.View, cursor.Position, input.Category, input.Limit+1)
	if err != nil {
		return PostPage{}, err
	}
	var positions []int64
	for rows.Next() {
		var item Post
		var position int64
		if err = rows.Scan(&item.ID, &item.Category, &item.Title, &item.Body, &item.PublishedAt, &item.WorkID, &item.WorkTitle, &item.MediaURL, &item.MediaKind, &item.AIDisclosure, &item.AuthorID, &item.AuthorHandle, &item.AuthorName, &item.CommentCount, &item.LikeCount, &item.BookmarkCount, &item.ViewerLiked, &item.ViewerBookmarked, &item.ViewerFollowing, &item.Status, &item.Version, &position); err != nil {
			rows.Close()
			return PostPage{}, err
		}
		page.Items = append(page.Items, item)
		positions = append(positions, position)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return PostPage{}, err
	}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		value, _ := json.Marshal(feedCursor{cursor.Snapshot, positions[input.Limit-1]})
		next := base64.RawURLEncoding.EncodeToString(value)
		page.NextCursor = &next
	}
	if err = tx.Commit(ctx); err != nil {
		return PostPage{}, err
	}
	return page, nil
}

func nullableViewer(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// $2 is viewer, $3 is view. Only the explicit private draft view bypasses the
// public projection, and it requires both ownership and a standalone post.
const feedVisibility = `(($3='drafts' AND p.author_id=$2 AND p.work_id IS NULL AND p.status='draft' AND NOT p.owner_removed)
 OR ($3<>'drafts' AND EXISTS(SELECT 1 FROM community_visible_posts visible WHERE visible.id=p.id)))`

const postProjection = `SELECT p.id,p.category,COALESCE(p.title,w.title,''),p.body,COALESCE(p.published_at,p.created_at),w.id,w.title,a.media_url,a.kind,w.ai_disclosure,u.id,u.handle,u.display_name,
 (SELECT count(*) FROM comments c JOIN users cu ON cu.id=c.author_id AND cu.status='active' WHERE c.post_id=p.id AND c.status='published'),
 (SELECT count(*) FROM post_reactions pr WHERE pr.post_id=p.id AND pr.kind='like'),
 (SELECT count(*) FROM post_reactions pr WHERE pr.post_id=p.id AND pr.kind='bookmark'),
 EXISTS(SELECT 1 FROM post_reactions pr WHERE pr.post_id=p.id AND pr.user_id=$2 AND pr.kind='like'),
 EXISTS(SELECT 1 FROM post_reactions pr WHERE pr.post_id=p.id AND pr.user_id=$2 AND pr.kind='bookmark'),
 EXISTS(SELECT 1 FROM user_follows f WHERE f.follower_id=$2 AND f.following_id=p.author_id),p.status,p.version,i.position`
