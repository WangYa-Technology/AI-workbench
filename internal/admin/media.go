package admin

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
)

type MediaItem struct {
	ID               uuid.UUID  `json:"id"`
	OwnerID          uuid.UUID  `json:"ownerId"`
	OwnerHandle      string     `json:"ownerHandle"`
	Title            string     `json:"title"`
	Kind             string     `json:"kind"`
	MimeType         string     `json:"mimeType"`
	MediaURL         string     `json:"mediaUrl"`
	UploadedFilename *string    `json:"uploadedFilename,omitempty"`
	SizeBytes        *int64     `json:"sizeBytes,omitempty"`
	ScanStatus       string     `json:"scanStatus"`
	ScanReason       *string    `json:"scanReason,omitempty"`
	ScannedAt        *time.Time `json:"scannedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
}

type MediaReview struct {
	Status string `json:"status"`
}

type MediaListInput struct {
	Query  string
	Kind   string
	Status string
	Cursor string
	Limit  int
}

type MediaPage struct {
	Items      []MediaItem `json:"items"`
	NextCursor *string     `json:"nextCursor,omitempty"`
}

type mediaCursor struct {
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

func (s *Service) ListMedia(ctx context.Context, input MediaListInput) (MediaPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if len(input.Query) > 120 || (input.Kind != "" && !oneOf(input.Kind, "image", "video", "audio", "document", "prompt", "workflow")) ||
		(input.Status != "" && !oneOf(input.Status, "pending", "clean", "review", "rejected")) {
		return MediaPage{}, ErrInvalidMediaFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return MediaPage{}, ErrInvalidMediaFilter
	}
	var cursorPriority *int
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeMediaCursor(input.Cursor)
		if err != nil {
			return MediaPage{}, ErrInvalidMediaFilter
		}
		cursorPriority, cursorTime, cursorID = &cursor.Priority, &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, mediaSelect+`
		AND ($1='' OR strpos(lower(a.title),$1)>0 OR strpos(lower(u.handle),$1)>0 OR strpos(lower(COALESCE(a.uploaded_filename,'')),$1)>0 OR strpos(lower(a.mime_type),$1)>0)
		AND ($2='' OR a.kind=$2)
		AND ($3='' OR a.scan_status=$3)
		AND ($4::integer IS NULL OR
			CASE a.scan_status WHEN 'review' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END > $4 OR
			(CASE a.scan_status WHEN 'review' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END = $4 AND (a.created_at,a.id) < ($5,$6::uuid)))
		ORDER BY CASE a.scan_status WHEN 'review' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END,a.created_at DESC,a.id DESC LIMIT $7`,
		input.Query, input.Kind, input.Status, cursorPriority, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return MediaPage{}, fmt.Errorf("list uploaded media: %w", err)
	}
	defer rows.Close()
	items := make([]MediaItem, 0)
	for rows.Next() {
		item, err := scanMedia(rows)
		if err != nil {
			return MediaPage{}, fmt.Errorf("scan uploaded media: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return MediaPage{}, err
	}
	page := MediaPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeMediaCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func mediaPriority(status string) int {
	switch status {
	case "review":
		return 0
	case "pending":
		return 1
	default:
		return 2
	}
}

func encodeMediaCursor(item MediaItem) string {
	body, _ := json.Marshal(mediaCursor{Priority: mediaPriority(item.ScanStatus), CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeMediaCursor(value string) (mediaCursor, error) {
	var cursor mediaCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.Priority < 0 || cursor.Priority > 2 || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
		return mediaCursor{}, ErrInvalidMediaFilter
	}
	return cursor, nil
}

func (s *Service) ReviewMedia(ctx context.Context, actorID, assetID uuid.UUID, input MediaReview, _ string) (MediaItem, error) {
	input.Status = strings.TrimSpace(strings.ToLower(input.Status))
	if !oneOf(input.Status, "clean", "review", "rejected") {
		return MediaItem{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MediaItem{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var ownerID uuid.UUID
	var previousStatus string
	if err := tx.QueryRow(ctx, `SELECT owner_id,scan_status FROM assets WHERE id=$1 AND source_type='upload' FOR UPDATE`, assetID).Scan(&ownerID, &previousStatus); errors.Is(err, pgx.ErrNoRows) {
		return MediaItem{}, ErrNotFound
	} else if err != nil {
		return MediaItem{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE assets SET scan_status=$2,scan_reason=NULL,scanned_at=now() WHERE id=$1`, assetID, input.Status); err != nil {
		return MediaItem{}, fmt.Errorf("review uploaded media: %w", err)
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: ownerID, Kind: "asset.scan_completed", Title: "Asset review completed",
		Body:       "An administrator reviewed your uploaded Asset. Its current scan status is " + input.Status + ".",
		TargetPath: "/workspace/assets/" + assetID.String(), ResourceType: "asset", ResourceID: &assetID,
		SourceKey: "asset-admin-review:" + assetID.String() + ":" + input.Status,
	}); err != nil {
		return MediaItem{}, err
	}
	if input.Status == "rejected" && previousStatus != "rejected" {
		if _, err := risk.RecordTx(ctx, tx, risk.SignalInput{
			SourceKey: "media_rejection:" + assetID.String(), ResourceType: "asset", ResourceID: assetID,
			SubjectUserID: ownerID, ActorUserID: &actorID, SignalType: "media_rejection",
			Summary:  "Rejected uploaded media requires risk review.",
			Evidence: map[string]any{"previousStatus": previousStatus},
		}); err != nil {
			return MediaItem{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return MediaItem{}, err
	}
	return s.media(ctx, assetID)
}

const mediaSelect = `
	SELECT a.id,a.owner_id,u.handle,a.title,a.kind,a.mime_type,a.media_url,a.uploaded_filename,a.size_bytes,
	       a.scan_status,a.scan_reason,a.scanned_at,a.created_at
	FROM assets a JOIN users u ON u.id=a.owner_id WHERE a.source_type='upload'`

func (s *Service) media(ctx context.Context, id uuid.UUID) (MediaItem, error) {
	item, err := scanMedia(s.pool.QueryRow(ctx, mediaSelect+` AND a.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return MediaItem{}, ErrNotFound
	}
	return item, err
}

func scanMedia(row scanner) (MediaItem, error) {
	var item MediaItem
	err := row.Scan(&item.ID, &item.OwnerID, &item.OwnerHandle, &item.Title, &item.Kind, &item.MimeType, &item.MediaURL,
		&item.UploadedFilename, &item.SizeBytes, &item.ScanStatus, &item.ScanReason, &item.ScannedAt, &item.CreatedAt)
	return item, err
}
