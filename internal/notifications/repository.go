package notifications

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const JobKind = "notification.deliver"

var (
	ErrInvalid               = errors.New("invalid notification input")
	ErrInvalidDeliveryFilter = errors.New("invalid notification delivery filter")
	ErrNotFound              = errors.New("notification not found")
	ErrConflict              = errors.New("notification preference version conflict")
)

var KnownKinds = []string{
	"account.data_rights",
	"asset.scan_completed",
	"community.comment",
	"community.follow",
	"community.moderation",
	"task.proposal_submitted",
	"task.proposal_accepted",
	"task.directly_accepted",
	"task.delivery_submitted",
	"task.revision_requested",
	"task.delivery_accepted",
	"task.funding_confirmed",
	"task.payout_transferred",
	"task.refund_requested",
	"task.refunded",
	"task.refund_failed",
	"task.dispute_opened",
	"task.dispute_resolved",
	"task.cancelled",
	"marketplace.order_fulfilled",
	"marketplace.order_refunded",
	"marketplace.refund_failed",
	"generation.completed",
	"billing.wallet_topup_completed",
	"billing.subscription_completed",
	"security.webhook_replayed",
	"security.email_delivery_retried",
	"support.case_updated",
}

var detailTargetPattern = regexp.MustCompile(`^/(market/demands|workspace/assets|support|community/posts)/[0-9a-fA-F-]{36}$`)

type Notification struct {
	ID           uuid.UUID  `json:"id"`
	Kind         string     `json:"kind"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	TargetPath   string     `json:"targetPath"`
	ResourceType string     `json:"resourceType,omitempty"`
	ResourceID   *uuid.UUID `json:"resourceId,omitempty"`
	ReadAt       *time.Time `json:"readAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
}

type Page struct {
	Items       []Notification `json:"items"`
	UnreadCount int            `json:"unreadCount"`
	NextCursor  string         `json:"nextCursor,omitempty"`
}

type ListInput struct {
	ReadState string
	Kind      string
	Before    *time.Time
	Limit     int
}

type Preference struct {
	Kind         string    `json:"kind"`
	InAppEnabled bool      `json:"inAppEnabled"`
	Version      int       `json:"version"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type DeliveryEvidence struct {
	ID          uuid.UUID  `json:"id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	ErrorCode   *string    `json:"errorCode,omitempty"`
	Attempts    int        `json:"attempts"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

type DeliveryEvidenceInput struct {
	Cursor string
	Limit  int
}

type DeliveryEvidencePage struct {
	Items      []DeliveryEvidence `json:"items"`
	NextCursor *string            `json:"nextCursor,omitempty"`
}

type deliveryEvidenceCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type CreateInput struct {
	UserID       uuid.UUID
	Kind         string
	Title        string
	Body         string
	TargetPath   string
	ResourceType string
	ResourceID   *uuid.UUID
	SourceKey    string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) List(ctx context.Context, userID uuid.UUID, input ListInput) (Page, error) {
	if input.ReadState == "" {
		input.ReadState = "all"
	}
	if input.ReadState != "all" && input.ReadState != "unread" && input.ReadState != "read" {
		return Page{}, ErrInvalid
	}
	if input.Kind != "" && !isKnownKind(input.Kind) {
		return Page{}, ErrInvalid
	}
	if input.Limit <= 0 {
		input.Limit = 20
	}
	if input.Limit > 50 {
		return Page{}, ErrInvalid
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id,kind,title,body,target_path,COALESCE(resource_type,''),resource_id,read_at,created_at
		FROM notifications
		WHERE user_id=$1 AND delivery_status='delivered'
		  AND ($2='all' OR ($2='unread' AND read_at IS NULL) OR ($2='read' AND read_at IS NOT NULL))
		  AND ($3='' OR kind=$3)
		  AND ($4::timestamptz IS NULL OR created_at < $4)
		ORDER BY created_at DESC,id DESC LIMIT $5`, userID, input.ReadState, input.Kind, input.Before, input.Limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	items := make([]Notification, 0, input.Limit+1)
	for rows.Next() {
		var item Notification
		if err := rows.Scan(&item.ID, &item.Kind, &item.Title, &item.Body, &item.TargetPath, &item.ResourceType, &item.ResourceID, &item.ReadAt, &item.CreatedAt); err != nil {
			return Page{}, fmt.Errorf("scan notification: %w", err)
		}
		if !ValidTargetPath(item.TargetPath) {
			item.TargetPath = "/notifications"
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate notifications: %w", err)
	}
	page := Page{Items: items}
	if len(page.Items) > input.Limit {
		page.NextCursor = page.Items[input.Limit-1].CreatedAt.Format(time.RFC3339Nano)
		page.Items = page.Items[:input.Limit]
	}
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND delivery_status='delivered' AND read_at IS NULL`, userID).Scan(&page.UnreadCount); err != nil {
		return Page{}, fmt.Errorf("count unread notifications: %w", err)
	}
	return page, nil
}

func (r *Repository) MarkRead(ctx context.Context, userID, notificationID uuid.UUID) (Notification, error) {
	var item Notification
	err := r.pool.QueryRow(ctx, `
		UPDATE notifications SET read_at=COALESCE(read_at,now())
		WHERE id=$1 AND user_id=$2 AND delivery_status='delivered'
		RETURNING id,kind,title,body,target_path,COALESCE(resource_type,''),resource_id,read_at,created_at`, notificationID, userID).Scan(
		&item.ID, &item.Kind, &item.Title, &item.Body, &item.TargetPath, &item.ResourceType, &item.ResourceID, &item.ReadAt, &item.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Notification{}, ErrNotFound
	}
	if err != nil {
		return Notification{}, fmt.Errorf("mark notification read: %w", err)
	}
	if !ValidTargetPath(item.TargetPath) {
		item.TargetPath = "/notifications"
	}
	return item, nil
}

func (r *Repository) MarkAllRead(ctx context.Context, userID uuid.UUID) (int64, error) {
	result, err := r.pool.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE user_id=$1 AND delivery_status='delivered' AND read_at IS NULL`, userID)
	if err != nil {
		return 0, fmt.Errorf("mark all notifications read: %w", err)
	}
	return result.RowsAffected(), nil
}

func (r *Repository) ListPreferences(ctx context.Context, userID uuid.UUID) ([]Preference, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT kinds.kind,COALESCE(p.in_app_enabled,true),COALESCE(p.version,1),COALESCE(p.updated_at,now())
		FROM unnest($2::text[]) WITH ORDINALITY AS kinds(kind,position)
		LEFT JOIN notification_preferences p ON p.user_id=$1 AND p.notification_kind=kinds.kind
		ORDER BY kinds.position`, userID, KnownKinds)
	if err != nil {
		return nil, fmt.Errorf("list notification preferences: %w", err)
	}
	defer rows.Close()
	items := make([]Preference, 0, len(KnownKinds))
	for rows.Next() {
		var item Preference
		if err := rows.Scan(&item.Kind, &item.InAppEnabled, &item.Version, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan notification preference: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) UpdatePreference(ctx context.Context, userID uuid.UUID, kind string, enabled bool, expectedVersion int) (Preference, error) {
	if !isKnownKind(kind) || expectedVersion < 1 {
		return Preference{}, ErrInvalid
	}
	var item Preference
	var err error
	if expectedVersion == 1 {
		err = r.pool.QueryRow(ctx, `
			INSERT INTO notification_preferences(user_id,notification_kind,in_app_enabled,version)
			VALUES($1,$2,$3,2)
			ON CONFLICT (user_id,notification_kind) DO NOTHING
			RETURNING notification_kind,in_app_enabled,version,updated_at`, userID, kind, enabled).Scan(
			&item.Kind, &item.InAppEnabled, &item.Version, &item.UpdatedAt,
		)
	} else {
		err = r.pool.QueryRow(ctx, `
			UPDATE notification_preferences
			SET in_app_enabled=$3,version=version+1,updated_at=now()
			WHERE user_id=$1 AND notification_kind=$2 AND version=$4
			RETURNING notification_kind,in_app_enabled,version,updated_at`, userID, kind, enabled, expectedVersion).Scan(
			&item.Kind, &item.InAppEnabled, &item.Version, &item.UpdatedAt,
		)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Preference{}, ErrConflict
	}
	if err != nil {
		return Preference{}, fmt.Errorf("update notification preference: %w", err)
	}
	return item, nil
}

func (r *Repository) ListDeliveryEvidence(ctx context.Context, userID uuid.UUID) ([]DeliveryEvidence, error) {
	page, err := r.ListDeliveryEvidencePage(ctx, userID, DeliveryEvidenceInput{})
	return page.Items, err
}

func (r *Repository) ListDeliveryEvidencePage(ctx context.Context, userID uuid.UUID, input DeliveryEvidenceInput) (DeliveryEvidencePage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return DeliveryEvidencePage{}, ErrInvalidDeliveryFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeDeliveryEvidenceCursor(input.Cursor)
		if err != nil {
			return DeliveryEvidencePage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := r.pool.Query(ctx, `
			SELECT n.id,n.kind,n.delivery_status,n.delivery_error_code,
			       COALESCE((SELECT j.attempts FROM jobs j WHERE j.kind=$2 AND j.payload->>'notificationId'=n.id::text ORDER BY j.created_at DESC LIMIT 1),0),
			       n.created_at,COALESCE(n.delivered_at,n.suppressed_at)
			FROM notifications n WHERE n.user_id=$1
			  AND ($3::timestamptz IS NULL OR (n.created_at,n.id)<($3,$4::uuid))
			ORDER BY n.created_at DESC,n.id DESC LIMIT $5`, userID, JobKind, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return DeliveryEvidencePage{}, fmt.Errorf("list notification delivery evidence: %w", err)
	}
	defer rows.Close()
	items := make([]DeliveryEvidence, 0)
	for rows.Next() {
		var item DeliveryEvidence
		if err := rows.Scan(&item.ID, &item.Kind, &item.Status, &item.ErrorCode, &item.Attempts, &item.CreatedAt, &item.CompletedAt); err != nil {
			return DeliveryEvidencePage{}, fmt.Errorf("scan notification delivery evidence: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return DeliveryEvidencePage{}, err
	}
	page := DeliveryEvidencePage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeDeliveryEvidenceCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func encodeDeliveryEvidenceCursor(item DeliveryEvidence) string {
	body, _ := json.Marshal(deliveryEvidenceCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeDeliveryEvidenceCursor(value string) (deliveryEvidenceCursor, error) {
	var cursor deliveryEvidenceCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return deliveryEvidenceCursor{}, ErrInvalidDeliveryFilter
	}
	return cursor, nil
}

func CreateTx(ctx context.Context, tx pgx.Tx, input CreateInput) error {
	input.Kind = strings.TrimSpace(input.Kind)
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	input.ResourceType = strings.TrimSpace(input.ResourceType)
	input.SourceKey = strings.TrimSpace(input.SourceKey)
	if input.UserID == uuid.Nil || !isKnownKind(input.Kind) || len(input.Title) < 2 || len(input.Title) > 160 || len(input.Body) < 2 || len(input.Body) > 500 || !ValidTargetPath(input.TargetPath) || len(input.ResourceType) > 60 || len(input.SourceKey) < 3 || len(input.SourceKey) > 160 {
		return ErrInvalid
	}
	var notificationID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO notifications(user_id,kind,title,body,target_path,resource_type,resource_id,source_key,delivery_status)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'queued')
		ON CONFLICT (user_id,source_key) WHERE source_key IS NOT NULL DO NOTHING
		RETURNING id`,
		input.UserID, input.Kind, input.Title, input.Body, input.TargetPath, nullable(input.ResourceType), input.ResourceID, input.SourceKey).Scan(&notificationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("queue notification: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('notificationId',$2::text),5)`, JobKind, notificationID); err != nil {
		return fmt.Errorf("queue notification delivery: %w", err)
	}
	return nil
}

func (r *Repository) HandleDeliveryJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		NotificationID uuid.UUID `json:"notificationId"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.NotificationID == uuid.Nil {
		return deliveryError("notification_payload_invalid")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID uuid.UUID
	var kind, status string
	err = tx.QueryRow(ctx, `SELECT user_id,kind,delivery_status FROM notifications WHERE id=$1 FOR UPDATE`, payload.NotificationID).Scan(&userID, &kind, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if status != "queued" {
		return tx.Commit(ctx)
	}
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT in_app_enabled FROM notification_preferences WHERE user_id=$1 AND notification_kind=$2),true)`, userID, kind).Scan(&enabled); err != nil {
		return err
	}
	if enabled {
		_, err = tx.Exec(ctx, `UPDATE notifications SET delivery_status='delivered',delivered_at=now() WHERE id=$1 AND delivery_status='queued'`, payload.NotificationID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE notifications SET delivery_status='suppressed',delivery_error_code='preference_disabled',suppressed_at=now() WHERE id=$1 AND delivery_status='queued'`, payload.NotificationID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type deliveryError string

func (e deliveryError) Error() string     { return string(e) }
func (e deliveryError) ErrorCode() string { return string(e) }

func ValidTargetPath(value string) bool {
	if len(value) < 2 || len(value) > 240 || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\r\n") {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.RawQuery != "" {
		if parsed.Path != "/create/image" {
			return false
		}
		query, queryErr := url.ParseQuery(parsed.RawQuery)
		if queryErr != nil || len(query) < 1 || len(query) > 2 {
			return false
		}
		for key, values := range query {
			if (key != "conversationId" && key != "generationId") || len(values) != 1 {
				return false
			}
			parsedID, parseErr := uuid.Parse(values[0])
			if parseErr != nil || parsedID == uuid.Nil {
				return false
			}
		}
	}
	switch parsed.Path {
	case "/notifications", "/settings", "/support", "/create/image", "/workspace/assets", "/workspace/generations", "/workspace/orders", "/workspace/tasks", "/workspace/billing", "/market", "/market/demands", "/community":
		return true
	default:
		return detailTargetPattern.MatchString(parsed.Path)
	}
}

func isKnownKind(kind string) bool {
	for _, candidate := range KnownKinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
