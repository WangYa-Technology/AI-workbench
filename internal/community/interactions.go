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
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/risk"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound             = errors.New("community resource not found")
	ErrInvalidCommentFilter = errors.New("invalid comment filter")
	ErrInvalidReportFilter  = errors.New("invalid community report filter")
)

type Comment struct {
	ID           uuid.UUID `json:"id"`
	PostID       uuid.UUID `json:"postId"`
	Body         string    `json:"body"`
	Status       string    `json:"status"`
	AuthorID     uuid.UUID `json:"authorId"`
	AuthorHandle string    `json:"authorHandle"`
	AuthorName   string    `json:"authorName"`
	CreatedAt    time.Time `json:"createdAt"`
}

type CommentListInput struct {
	Cursor string
	Limit  int
}

type CommentPage struct {
	Items      []Comment `json:"items"`
	NextCursor *string   `json:"nextCursor,omitempty"`
}

type commentCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type InteractionState struct {
	PostID           uuid.UUID `json:"postId"`
	LikeCount        int       `json:"likeCount"`
	BookmarkCount    int       `json:"bookmarkCount"`
	ViewerLiked      bool      `json:"viewerLiked"`
	ViewerBookmarked bool      `json:"viewerBookmarked"`
}

type FollowState struct {
	AuthorID  uuid.UUID `json:"authorId"`
	Following bool      `json:"following"`
}

type ReportInput struct {
	Category string `json:"category"`
	Details  string `json:"details"`
}

type Report struct {
	ID               uuid.UUID  `json:"id"`
	ReporterID       uuid.UUID  `json:"reporterId"`
	ResourceType     string     `json:"resourceType"`
	ResourceID       uuid.UUID  `json:"resourceId"`
	ResourceTitle    string     `json:"resourceTitle"`
	SubjectAuthorID  uuid.UUID  `json:"subjectAuthorId"`
	SubjectHandle    string     `json:"subjectHandle"`
	Category         string     `json:"category"`
	Details          string     `json:"details"`
	Status           string     `json:"status"`
	Outcome          *string    `json:"outcome,omitempty"`
	ResolutionReason *string    `json:"resolutionReason,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	ResolvedAt       *time.Time `json:"resolvedAt,omitempty"`
	ViewerCanAppeal  bool       `json:"viewerCanAppeal"`
	Appeal           *Appeal    `json:"appeal,omitempty"`
}

type ReportListInput struct {
	Cursor string
	Limit  int
}

type ReportPage struct {
	Items      []Report `json:"items"`
	NextCursor *string  `json:"nextCursor,omitempty"`
}

type reportCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type Appeal struct {
	ID               uuid.UUID  `json:"id"`
	ReportID         uuid.UUID  `json:"reportId"`
	AppellantID      uuid.UUID  `json:"appellantId"`
	AppellantHandle  string     `json:"appellantHandle"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	ResolutionReason *string    `json:"resolutionReason,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	ResolvedAt       *time.Time `json:"resolvedAt,omitempty"`
}

func (r *Repository) ListComments(ctx context.Context, postID uuid.UUID, input CommentListInput) (CommentPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return CommentPage{}, ErrInvalidCommentFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeCommentCursor(input.Cursor)
		if err != nil {
			return CommentPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := r.pool.Query(ctx, `
		SELECT c.id,c.post_id,c.body,c.status,u.id,u.handle,u.display_name,c.created_at
		FROM comments c JOIN users u ON u.id=c.author_id
		JOIN posts p ON p.id=c.post_id AND p.status='published'
		WHERE c.post_id=$1 AND c.status='published'
		  AND ($2::timestamptz IS NULL OR (c.created_at,c.id)>($2,$3::uuid))
		ORDER BY c.created_at ASC,c.id ASC LIMIT $4`, postID, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return CommentPage{}, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()
	items := make([]Comment, 0)
	for rows.Next() {
		var item Comment
		if err := rows.Scan(&item.ID, &item.PostID, &item.Body, &item.Status, &item.AuthorID, &item.AuthorHandle, &item.AuthorName, &item.CreatedAt); err != nil {
			return CommentPage{}, fmt.Errorf("scan comment: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return CommentPage{}, err
	}
	page := CommentPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeCommentCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func (r *Repository) CreateComment(ctx context.Context, actorID, postID uuid.UUID, body string) (Comment, error) {
	body = strings.TrimSpace(body)
	if len(body) < 2 || len(body) > 1000 {
		return Comment{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Comment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var postAuthorID uuid.UUID
	var workTitle string
	if err := tx.QueryRow(ctx, `SELECT p.author_id,w.title FROM posts p JOIN works w ON w.id=p.work_id WHERE p.id=$1 AND p.status='published' AND w.status='published'`, postID).Scan(&postAuthorID, &workTitle); errors.Is(err, pgx.ErrNoRows) {
		return Comment{}, ErrNotFound
	} else if err != nil {
		return Comment{}, err
	}
	var item Comment
	item.ID = uuid.New()
	if err := tx.QueryRow(ctx, `
		INSERT INTO comments(id,post_id,author_id,body,status) VALUES($1,$2,$3,$4,'published')
		RETURNING post_id,body,status,created_at`, item.ID, postID, actorID, body).Scan(&item.PostID, &item.Body, &item.Status, &item.CreatedAt); err != nil {
		return Comment{}, fmt.Errorf("create comment: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT id,handle,display_name FROM users WHERE id=$1`, actorID).Scan(&item.AuthorID, &item.AuthorHandle, &item.AuthorName); err != nil {
		return Comment{}, err
	}
	if postAuthorID != actorID {
		if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
			UserID: postAuthorID, Kind: "community.comment", Title: "New comment", Body: item.AuthorName + " commented on " + workTitle + ".",
			TargetPath: "/community", ResourceType: "comment", ResourceID: &item.ID, SourceKey: "comment:" + item.ID.String(),
		}); err != nil {
			return Comment{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Comment{}, err
	}
	return item, nil
}

func (r *Repository) SetReaction(ctx context.Context, actorID, postID uuid.UUID, kind string, active bool) (InteractionState, error) {
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind != "like" && kind != "bookmark" {
		return InteractionState{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return InteractionState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM posts p JOIN works w ON w.id=p.work_id WHERE p.id=$1 AND p.status='published' AND w.status='published')`, postID).Scan(&exists); err != nil {
		return InteractionState{}, err
	}
	if !exists {
		return InteractionState{}, ErrNotFound
	}
	if active {
		_, err = tx.Exec(ctx, `INSERT INTO post_reactions(post_id,user_id,kind) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, postID, actorID, kind)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM post_reactions WHERE post_id=$1 AND user_id=$2 AND kind=$3`, postID, actorID, kind)
	}
	if err != nil {
		return InteractionState{}, err
	}
	state, err := interactionState(ctx, tx, actorID, postID)
	if err != nil {
		return InteractionState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InteractionState{}, err
	}
	return state, nil
}

func (r *Repository) SetFollow(ctx context.Context, actorID, authorID uuid.UUID, active bool) (FollowState, error) {
	if actorID == authorID {
		return FollowState{}, ErrConflict
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return FollowState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var authorName string
	if err := tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id=$1 AND status='active'`, authorID).Scan(&authorName); errors.Is(err, pgx.ErrNoRows) {
		return FollowState{}, ErrNotFound
	} else if err != nil {
		return FollowState{}, err
	}
	if active {
		result, err := tx.Exec(ctx, `INSERT INTO user_follows(follower_id,following_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, actorID, authorID)
		if err != nil {
			return FollowState{}, err
		}
		if result.RowsAffected() == 1 {
			var followerName string
			if err := tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id=$1`, actorID).Scan(&followerName); err != nil {
				return FollowState{}, err
			}
			if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
				UserID: authorID, Kind: "community.follow", Title: "New follower", Body: followerName + " followed your Community work.",
				TargetPath: "/community", ResourceType: "user", ResourceID: &actorID, SourceKey: "follow:" + actorID.String() + ":" + authorID.String(),
			}); err != nil {
				return FollowState{}, err
			}
		}
	} else if _, err := tx.Exec(ctx, `DELETE FROM user_follows WHERE follower_id=$1 AND following_id=$2`, actorID, authorID); err != nil {
		return FollowState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FollowState{}, err
	}
	return FollowState{AuthorID: authorID, Following: active}, nil
}

func (r *Repository) ReportPost(ctx context.Context, actorID, postID uuid.UUID, input ReportInput) (Report, error) {
	input.Category = strings.TrimSpace(strings.ToLower(input.Category))
	input.Details = strings.TrimSpace(input.Details)
	if !oneOf(input.Category, "spam", "harassment", "copyright", "sexual", "violence", "misleading", "other") || len(input.Details) < 10 || len(input.Details) > 1000 {
		return Report{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var authorID uuid.UUID
	var title, handle string
	if err := tx.QueryRow(ctx, `SELECT p.author_id,w.title,u.handle FROM posts p JOIN works w ON w.id=p.work_id JOIN users u ON u.id=p.author_id WHERE p.id=$1 AND p.status='published'`, postID).Scan(&authorID, &title, &handle); errors.Is(err, pgx.ErrNoRows) {
		return Report{}, ErrNotFound
	} else if err != nil {
		return Report{}, err
	}
	if authorID == actorID {
		return Report{}, ErrConflict
	}
	item := Report{ID: uuid.New(), ReporterID: actorID, ResourceType: "post", ResourceID: postID, ResourceTitle: title, SubjectAuthorID: authorID, SubjectHandle: handle, Category: input.Category, Details: input.Details, Status: "open"}
	if err := tx.QueryRow(ctx, `
		INSERT INTO content_reports(id,reporter_id,resource_type,resource_id,subject_author_id,category,details)
		VALUES($1,$2,'post',$3,$4,$5,$6)
		RETURNING created_at,updated_at`, item.ID, actorID, postID, authorID, item.Category, item.Details).Scan(&item.CreatedAt, &item.UpdatedAt); err != nil {
		if isUniqueViolation(err, "content_reports_one_open_idx") {
			return Report{}, ErrConflict
		}
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO governance_events(report_id,actor_id,kind,to_status,reason) VALUES($1,$2,'reported','open',$3)`, item.ID, actorID, item.Details); err != nil {
		return Report{}, err
	}
	if _, err := risk.RecordTx(ctx, tx, risk.SignalInput{
		SourceKey: "community_report:" + item.ID.String(), ResourceType: "post", ResourceID: postID,
		SubjectUserID: authorID, ActorUserID: &actorID, SignalType: "community_report",
		Summary:  "Community report requires risk review.",
		Evidence: map[string]any{"reportId": item.ID, "category": item.Category},
	}); err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return item, nil
}

func (r *Repository) ListMyReports(ctx context.Context, actorID uuid.UUID, input ReportListInput) (ReportPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return ReportPage{}, ErrInvalidReportFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeReportCursor(input.Cursor)
		if err != nil {
			return ReportPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := r.pool.Query(ctx, reportSelect+`
		WHERE (r.reporter_id=$1 OR r.subject_author_id=$1)
		  AND ($2::timestamptz IS NULL OR (r.created_at,r.id)<($2,$3::uuid))
		ORDER BY r.created_at DESC,r.id DESC LIMIT $4`, actorID, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return ReportPage{}, fmt.Errorf("list visible Community reports: %w", err)
	}
	defer rows.Close()
	items := make([]Report, 0)
	for rows.Next() {
		item, err := scanReport(rows, actorID)
		if err != nil {
			return ReportPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ReportPage{}, err
	}
	page := ReportPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeReportCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func (r *Repository) CreateAppeal(ctx context.Context, actorID, reportID uuid.UUID, reason string) (Appeal, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 10 || len(reason) > 1000 {
		return Appeal{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Appeal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var reporterID, subjectID uuid.UUID
	var reportStatus string
	if err := tx.QueryRow(ctx, `SELECT reporter_id,subject_author_id,status FROM content_reports WHERE id=$1 FOR UPDATE`, reportID).Scan(&reporterID, &subjectID, &reportStatus); errors.Is(err, pgx.ErrNoRows) {
		return Appeal{}, ErrNotFound
	} else if err != nil {
		return Appeal{}, err
	}
	if actorID != reporterID && actorID != subjectID {
		return Appeal{}, ErrForbidden
	}
	if reportStatus != "resolved" && reportStatus != "dismissed" {
		return Appeal{}, ErrConflict
	}
	item := Appeal{ID: uuid.New(), ReportID: reportID, AppellantID: actorID, Reason: reason, Status: "pending"}
	if err := tx.QueryRow(ctx, `
		INSERT INTO moderation_appeals(id,report_id,appellant_id,reason) VALUES($1,$2,$3,$4)
		RETURNING created_at`, item.ID, reportID, actorID, reason).Scan(&item.CreatedAt); err != nil {
		if isUniqueViolation(err, "moderation_appeals_report_id_appellant_id_key") {
			return Appeal{}, ErrConflict
		}
		return Appeal{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT handle FROM users WHERE id=$1`, actorID).Scan(&item.AppellantHandle); err != nil {
		return Appeal{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO governance_events(report_id,appeal_id,actor_id,kind,from_status,to_status,reason) VALUES($1,$2,$3,'appealed',$4,'pending',$5)`, reportID, item.ID, actorID, reportStatus, reason); err != nil {
		return Appeal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Appeal{}, err
	}
	return item, nil
}

const reportSelect = `
		SELECT r.id,r.reporter_id,r.resource_type,r.resource_id,
		       CASE r.resource_type WHEN 'post' THEN COALESCE(w.title,'Community post') WHEN 'work' THEN COALESCE(ww.title,'Work') ELSE 'Comment' END,
		       r.subject_author_id,u.handle,r.category,r.details,r.status,r.outcome,r.resolution_reason,r.created_at,r.updated_at,r.resolved_at,
		       a.id,a.appellant_id,COALESCE(au.handle,''),a.reason,a.status,a.resolution_reason,a.created_at,a.resolved_at
		FROM content_reports r
		JOIN users u ON u.id=r.subject_author_id
		LEFT JOIN posts p ON r.resource_type='post' AND p.id=r.resource_id
		LEFT JOIN works w ON w.id=p.work_id
		LEFT JOIN works ww ON r.resource_type='work' AND ww.id=r.resource_id
		LEFT JOIN moderation_appeals a ON a.report_id=r.id
		LEFT JOIN users au ON au.id=a.appellant_id `

func scanReport(row pgx.Row, actorID uuid.UUID) (Report, error) {
	var item Report
	var appealID, appellantID *uuid.UUID
	var appealHandle, appealReason, appealStatus *string
	var appealResolution *string
	var appealCreated *time.Time
	var appealResolved *time.Time
	if err := row.Scan(&item.ID, &item.ReporterID, &item.ResourceType, &item.ResourceID, &item.ResourceTitle,
		&item.SubjectAuthorID, &item.SubjectHandle, &item.Category, &item.Details, &item.Status, &item.Outcome,
		&item.ResolutionReason, &item.CreatedAt, &item.UpdatedAt, &item.ResolvedAt,
		&appealID, &appellantID, &appealHandle, &appealReason, &appealStatus, &appealResolution, &appealCreated, &appealResolved); err != nil {
		return Report{}, fmt.Errorf("scan visible Community report: %w", err)
	}
	if appealID != nil && appellantID != nil && appealReason != nil && appealStatus != nil && appealCreated != nil {
		item.Appeal = &Appeal{ID: *appealID, ReportID: item.ID, AppellantID: *appellantID, AppellantHandle: value(appealHandle), Reason: *appealReason, Status: *appealStatus, ResolutionReason: appealResolution, CreatedAt: *appealCreated, ResolvedAt: appealResolved}
	}
	item.ViewerCanAppeal = (actorID == item.ReporterID || actorID == item.SubjectAuthorID) && (item.Status == "resolved" || item.Status == "dismissed") && item.Appeal == nil
	return item, nil
}

func encodeReportCursor(item Report) string {
	body, _ := json.Marshal(reportCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func encodeCommentCursor(item Comment) string {
	body, _ := json.Marshal(commentCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeCommentCursor(value string) (commentCursor, error) {
	var cursor commentCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return commentCursor{}, ErrInvalidCommentFilter
	}
	return cursor, nil
}

func decodeReportCursor(value string) (reportCursor, error) {
	var cursor reportCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return reportCursor{}, ErrInvalidReportFilter
	}
	return cursor, nil
}

func interactionState(ctx context.Context, tx pgx.Tx, actorID, postID uuid.UUID) (InteractionState, error) {
	var item InteractionState
	item.PostID = postID
	err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE kind='like'),count(*) FILTER (WHERE kind='bookmark'),
		       COALESCE(bool_or(user_id=$2 AND kind='like'),false),COALESCE(bool_or(user_id=$2 AND kind='bookmark'),false)
		FROM post_reactions WHERE post_id=$1`, postID, actorID).Scan(&item.LikeCount, &item.BookmarkCount, &item.ViewerLiked, &item.ViewerBookmarked)
	return item, err
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func value(input *string) string {
	if input == nil {
		return ""
	}
	return *input
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
