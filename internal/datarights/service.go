package datarights

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ExportJobKind       = "data_rights.export"
	DeletionJobKind     = "data_rights.delete"
	ExportExpiryJobKind = "data_rights.export_expire"
	maximumExportBytes  = 5 << 20
	deletionGrace       = 30 * 24 * time.Hour
	exportLifetime      = 7 * 24 * time.Hour
	recentSessionAge    = 15 * time.Minute
)

var (
	ErrInvalid       = errors.New("invalid data rights request")
	ErrNotFound      = errors.New("data rights request not found")
	ErrConflict      = errors.New("active data rights request already exists")
	ErrReauth        = errors.New("recent authentication required")
	ErrIdentity      = errors.New("identity confirmation failed")
	ErrNotCancelable = errors.New("data rights request cannot be cancelled")
	ErrNotReady      = errors.New("data export is not ready")
	ErrExpired       = errors.New("data export expired")
	ErrTooLarge      = errors.New("data export exceeds maximum size")
	ErrDemoAccount   = errors.New("demo accounts cannot create data rights requests")
	ErrHoldCutoff    = errors.New("legal hold cutoff passed")
	ErrInvalidList   = errors.New("invalid data rights list filter")
	ErrInvalidHolds  = errors.New("invalid data rights hold filter")
)

type Request struct {
	ID           uuid.UUID       `json:"id"`
	OwnerID      uuid.UUID       `json:"ownerId,omitempty"`
	RequestType  string          `json:"requestType"`
	Status       string          `json:"status"`
	SubjectRef   string          `json:"subjectRef"`
	ExecuteAfter time.Time       `json:"executeAfter"`
	CancelUntil  *time.Time      `json:"cancelUntil,omitempty"`
	CompletedAt  *time.Time      `json:"completedAt,omitempty"`
	FailureCode  *string         `json:"failureCode,omitempty"`
	Version      int             `json:"version"`
	Export       *ExportEvidence `json:"export,omitempty"`
	Receipt      *Receipt        `json:"receipt,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	OwnerHandle  string          `json:"ownerHandle,omitempty"`
	OwnerEmail   string          `json:"ownerEmail,omitempty"`
}

type ExportEvidence struct {
	ChecksumSHA256 string     `json:"checksumSha256"`
	SizeBytes      int64      `json:"sizeBytes"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	PurgedAt       *time.Time `json:"purgedAt,omitempty"`
}

type Receipt struct {
	ID             uuid.UUID       `json:"id"`
	ChecksumSHA256 string          `json:"checksumSha256"`
	CompletedAt    time.Time       `json:"completedAt"`
	Domains        json.RawMessage `json:"domains"`
}

type CreateInput struct {
	RequestType          string `json:"requestType"`
	IdentityConfirmation string `json:"identityConfirmation"`
}

type Hold struct {
	ID                     uuid.UUID  `json:"id"`
	UserID                 uuid.UUID  `json:"userId"`
	RequestID              *uuid.UUID `json:"requestId,omitempty"`
	OwnerHandle            string     `json:"ownerHandle"`
	Reason                 string     `json:"reason"`
	AuthorityReferenceHash string     `json:"authorityReferenceHash"`
	Status                 string     `json:"status"`
	ReviewAt               time.Time  `json:"reviewAt"`
	ExpiresAt              time.Time  `json:"expiresAt"`
	CreatedAt              time.Time  `json:"createdAt"`
}

type HoldInput struct {
	UserID             uuid.UUID `json:"userId"`
	Reason             string    `json:"reason"`
	AuthorityReference string    `json:"authorityReference"`
	Confirmed          bool      `json:"confirmed"`
}

type ListInput struct {
	Cursor string
	Limit  int
}

type RequestPage struct {
	Items      []Request `json:"items"`
	NextCursor *string   `json:"nextCursor,omitempty"`
}

type HoldPage struct {
	Items      []Hold  `json:"items"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

type requestCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type holdCursor struct {
	AsOf      time.Time `json:"asOf"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type Service struct {
	pool      *pgxpool.Pool
	mediaRoot string
	stores    *media.Catalog
}

func NewService(pool *pgxpool.Pool, mediaRoot string) *Service {
	return NewServiceWithMedia(pool, mediaRoot, media.NewCatalog(media.NewLocalStore(mediaRoot)))
}

func NewServiceWithMedia(pool *pgxpool.Pool, mediaRoot string, stores *media.Catalog) *Service {
	return &Service{pool: pool, mediaRoot: mediaRoot, stores: stores}
}

func (s *Service) Create(ctx context.Context, userID uuid.UUID, sessionToken string, input CreateInput, requestID string) (Request, error) {
	input.RequestType = strings.TrimSpace(input.RequestType)
	input.IdentityConfirmation = strings.ToLower(strings.TrimSpace(input.IdentityConfirmation))
	if userID == uuid.Nil || !oneOf(input.RequestType, "data_export", "account_deletion") || input.IdentityConfirmation == "" {
		return Request{}, ErrInvalid
	}
	if isDemoUser(userID) {
		return Request{}, ErrDemoAccount
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var handle string
	if err := tx.QueryRow(ctx, `SELECT handle FROM users WHERE id=$1 AND status='active' FOR UPDATE`, userID).Scan(&handle); errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	} else if err != nil {
		return Request{}, err
	}
	if strings.ToLower(handle) != input.IdentityConfirmation {
		return Request{}, ErrIdentity
	}
	var sessionCreated time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM sessions WHERE user_id=$1 AND token_hash=$2 AND revoked_at IS NULL AND expires_at>now()`, userID, identity.HashToken(sessionToken)).Scan(&sessionCreated); errors.Is(err, pgx.ErrNoRows) || time.Since(sessionCreated) > recentSessionAge {
		return Request{}, ErrReauth
	} else if err != nil {
		return Request{}, err
	}
	var recentCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM data_rights_requests WHERE user_id=$1 AND created_at>now()-interval '30 days'`, userID).Scan(&recentCount); err != nil {
		return Request{}, err
	}
	if recentCount >= 3 {
		return Request{}, ErrConflict
	}
	now := time.Now().UTC()
	status, executeAfter := "queued", now
	var cancelUntil *time.Time
	jobKind := ExportJobKind
	if input.RequestType == "account_deletion" {
		status, executeAfter, jobKind = "scheduled", now.Add(deletionGrace), DeletionJobKind
		cancelUntil = &executeAfter
		var held bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_rights_legal_holds WHERE user_id=$1 AND status='active' AND expires_at>now())`, userID).Scan(&held); err != nil {
			return Request{}, err
		}
		if held {
			status = "blocked"
		}
	}
	item := Request{ID: uuid.New(), RequestType: input.RequestType, Status: status, SubjectRef: subjectRef(userID), ExecuteAfter: executeAfter, CancelUntil: cancelUntil, Version: 1, CreatedAt: now, UpdatedAt: now}
	_, err = tx.Exec(ctx, `INSERT INTO data_rights_requests(id,user_id,request_type,status,subject_ref,execute_after,cancel_until,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)`, item.ID, userID, item.RequestType, item.Status, item.SubjectRef, item.ExecuteAfter, item.CancelUntil, now)
	if isUniqueViolation(err) {
		return Request{}, ErrConflict
	}
	if err != nil {
		return Request{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts,available_at) VALUES($1,jsonb_build_object('requestId',$2::text),5,$3)`, jobKind, item.ID, executeAfter); err != nil {
		return Request{}, err
	}
	if err := appendEvent(ctx, tx, item.ID, &userID, "requested", "", status, "Owner identity and recent session verified", map[string]any{"requestId": requestID}); err != nil {
		return Request{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,'data_rights.requested','data_rights_request',$2,$3,$4,jsonb_build_object('requestType',$5::text,'subjectRef',$6::text))`, userID, item.ID, "Owner requested "+input.RequestType, safeRequestID(requestID), item.RequestType, item.SubjectRef); err != nil {
		return Request{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, err
	}
	return item, nil
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, input ListInput) (RequestPage, error) {
	if userID == uuid.Nil {
		return RequestPage{}, ErrInvalidList
	}
	return s.list(ctx, &userID, input)
}

func (s *Service) ListAdmin(ctx context.Context, input ListInput) (RequestPage, error) {
	return s.list(ctx, nil, input)
}

func (s *Service) ListHolds(ctx context.Context, input ListInput) (HoldPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return HoldPage{}, ErrInvalidHolds
	}
	asOf := time.Now().UTC()
	var cursorPriority *int
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeHoldCursor(input.Cursor)
		if err != nil {
			return HoldPage{}, err
		}
		asOf = cursor.AsOf
		cursorPriority, cursorTime, cursorID = &cursor.Priority, &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT h.id,h.user_id,h.request_id,u.handle,h.reason,h.authority_reference_hash,
		       CASE WHEN h.status='active' AND h.expires_at<=$1 THEN 'expired' ELSE h.status END,
		       h.review_at,h.expires_at,h.created_at
		FROM data_rights_legal_holds h JOIN users u ON u.id=h.user_id
		WHERE $2::int IS NULL
		   OR CASE WHEN h.status='active' AND h.expires_at>$1 THEN 0 ELSE 1 END>$2
		   OR (CASE WHEN h.status='active' AND h.expires_at>$1 THEN 0 ELSE 1 END=$2 AND (h.created_at,h.id)<($3,$4::uuid))
		ORDER BY CASE WHEN h.status='active' AND h.expires_at>$1 THEN 0 ELSE 1 END,h.created_at DESC,h.id DESC LIMIT $5`, asOf, cursorPriority, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return HoldPage{}, err
	}
	defer rows.Close()
	items := make([]Hold, 0)
	for rows.Next() {
		var item Hold
		if err := rows.Scan(&item.ID, &item.UserID, &item.RequestID, &item.OwnerHandle, &item.Reason, &item.AuthorityReferenceHash, &item.Status, &item.ReviewAt, &item.ExpiresAt, &item.CreatedAt); err != nil {
			return HoldPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return HoldPage{}, err
	}
	page := HoldPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		priority := 1
		if last.Status == "active" {
			priority = 0
		}
		cursor := encodeHoldCursor(holdCursor{AsOf: asOf, Priority: priority, CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) CreateHold(ctx context.Context, actorID uuid.UUID, input HoldInput, requestID string) (Hold, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	input.AuthorityReference = strings.TrimSpace(input.AuthorityReference)
	if actorID == uuid.Nil || input.UserID == uuid.Nil || !input.Confirmed || len(input.Reason) < 10 || len(input.Reason) > 1000 || len(input.AuthorityReference) < 6 || len(input.AuthorityReference) > 200 {
		return Hold{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Hold{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var handle string
	if err := tx.QueryRow(ctx, `SELECT handle FROM users WHERE id=$1 FOR UPDATE`, input.UserID).Scan(&handle); errors.Is(err, pgx.ErrNoRows) {
		return Hold{}, ErrNotFound
	} else if err != nil {
		return Hold{}, err
	}
	var linkedRequestID *uuid.UUID
	var linkedStatus *string
	err = tx.QueryRow(ctx, `SELECT id,status FROM data_rights_requests WHERE user_id=$1 AND request_type='account_deletion' AND status IN ('scheduled','blocked','processing') ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, input.UserID).Scan(&linkedRequestID, &linkedStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Hold{}, err
	}
	if linkedStatus != nil && *linkedStatus == "processing" {
		return Hold{}, ErrHoldCutoff
	}
	now := time.Now().UTC()
	referenceHash := sha256.Sum256([]byte(input.AuthorityReference))
	item := Hold{ID: uuid.New(), UserID: input.UserID, RequestID: linkedRequestID, OwnerHandle: handle, Reason: input.Reason, AuthorityReferenceHash: hex.EncodeToString(referenceHash[:]), Status: "active", ReviewAt: now.Add(90 * 24 * time.Hour), ExpiresAt: now.Add(365 * 24 * time.Hour), CreatedAt: now}
	_, err = tx.Exec(ctx, `INSERT INTO data_rights_legal_holds(id,user_id,request_id,reason,authority_reference_hash,review_at,expires_at,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, item.ID, item.UserID, item.RequestID, item.Reason, item.AuthorityReferenceHash, item.ReviewAt, item.ExpiresAt, actorID, item.CreatedAt)
	if isUniqueViolation(err) {
		return Hold{}, ErrConflict
	}
	if err != nil {
		return Hold{}, err
	}
	if linkedRequestID != nil && linkedStatus != nil && *linkedStatus == "scheduled" {
		if _, err := tx.Exec(ctx, `UPDATE data_rights_requests SET status='blocked',version=version+1,updated_at=now() WHERE id=$1`, *linkedRequestID); err != nil {
			return Hold{}, err
		}
		if err := appendEvent(ctx, tx, *linkedRequestID, &actorID, "legal_hold_created", *linkedStatus, "blocked", input.Reason, map[string]any{"holdId": item.ID}); err != nil {
			return Hold{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,'admin.data_rights_hold_created','data_rights_legal_hold',$2,$3,$4,jsonb_build_object('subjectRef',$5::text,'authorityReferenceHash',$6::text,'expiresAt',$7::timestamptz))`, actorID, item.ID, input.Reason, safeRequestID(requestID), subjectRef(input.UserID), item.AuthorityReferenceHash, item.ExpiresAt); err != nil {
		return Hold{}, err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{UserID: input.UserID, Kind: "account.data_rights", Title: "Account deletion is on legal hold", Body: "A controlled legal hold is preserving the scoped account record. Export remains available.", TargetPath: "/settings", ResourceType: "data_rights_legal_hold", ResourceID: &item.ID, SourceKey: "data-rights-hold:" + item.ID.String()}); err != nil {
		return Hold{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Hold{}, err
	}
	return item, nil
}

func (s *Service) ReleaseHold(ctx context.Context, actorID, holdID uuid.UUID, reason, requestID string, confirmed bool) (Hold, error) {
	reason = strings.TrimSpace(reason)
	if actorID == uuid.Nil || holdID == uuid.Nil || !confirmed || len(reason) < 10 || len(reason) > 1000 {
		return Hold{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Hold{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item Hold
	if err := tx.QueryRow(ctx, `SELECT h.id,h.user_id,h.request_id,u.handle,h.reason,h.authority_reference_hash,h.status,h.review_at,h.expires_at,h.created_at FROM data_rights_legal_holds h JOIN users u ON u.id=h.user_id WHERE h.id=$1 FOR UPDATE`, holdID).Scan(&item.ID, &item.UserID, &item.RequestID, &item.OwnerHandle, &item.Reason, &item.AuthorityReferenceHash, &item.Status, &item.ReviewAt, &item.ExpiresAt, &item.CreatedAt); errors.Is(err, pgx.ErrNoRows) {
		return Hold{}, ErrNotFound
	} else if err != nil {
		return Hold{}, err
	}
	if item.Status != "active" {
		return Hold{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE data_rights_legal_holds SET status='released',released_by=$2,released_at=now() WHERE id=$1`, holdID, actorID); err != nil {
		return Hold{}, err
	}
	item.Status = "released"
	if item.RequestID != nil {
		var executeAfter time.Time
		var status string
		if err := tx.QueryRow(ctx, `SELECT status,execute_after FROM data_rights_requests WHERE id=$1 FOR UPDATE`, *item.RequestID).Scan(&status, &executeAfter); err != nil {
			return Hold{}, err
		}
		if status == "blocked" {
			if _, err := tx.Exec(ctx, `UPDATE data_rights_requests SET status='scheduled',version=version+1,updated_at=now() WHERE id=$1`, *item.RequestID); err != nil {
				return Hold{}, err
			}
			availableAt := executeAfter
			if availableAt.Before(time.Now()) {
				availableAt = time.Now()
			}
			if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts,available_at) VALUES($1,jsonb_build_object('requestId',$2::text),5,$3)`, DeletionJobKind, *item.RequestID, availableAt); err != nil {
				return Hold{}, err
			}
			if err := appendEvent(ctx, tx, *item.RequestID, &actorID, "legal_hold_released", "blocked", "scheduled", reason, map[string]any{"holdId": holdID}); err != nil {
				return Hold{}, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,'admin.data_rights_hold_released','data_rights_legal_hold',$2,$3,$4,jsonb_build_object('subjectRef',$5::text))`, actorID, holdID, reason, safeRequestID(requestID), subjectRef(item.UserID)); err != nil {
		return Hold{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Hold{}, err
	}
	return item, nil
}

func (s *Service) list(ctx context.Context, userID *uuid.UUID, input ListInput) (RequestPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return RequestPage{}, ErrInvalidList
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeRequestCursor(input.Cursor)
		if err != nil {
			return RequestPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	var rows pgx.Rows
	var err error
	if userID == nil {
		rows, err = s.pool.Query(ctx, requestSelect+`
			WHERE $1::timestamptz IS NULL OR (r.created_at,r.id)<($1,$2::uuid)
			ORDER BY r.created_at DESC,r.id DESC LIMIT $3`, cursorTime, cursorID, input.Limit+1)
	} else {
		rows, err = s.pool.Query(ctx, requestSelect+`
			WHERE r.user_id=$1 AND ($2::timestamptz IS NULL OR (r.created_at,r.id)<($2,$3::uuid))
			ORDER BY r.created_at DESC,r.id DESC LIMIT $4`, *userID, cursorTime, cursorID, input.Limit+1)
	}
	if err != nil {
		return RequestPage{}, err
	}
	defer rows.Close()
	items := make([]Request, 0)
	for rows.Next() {
		item, err := scanRequest(rows)
		if err != nil {
			return RequestPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return RequestPage{}, err
	}
	page := RequestPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		cursor := encodeRequestCursor(requestCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = &cursor
	}
	return page, nil
}

func encodeRequestCursor(cursor requestCursor) string {
	body, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeRequestCursor(value string) (requestCursor, error) {
	var cursor requestCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return requestCursor{}, ErrInvalidList
	}
	return cursor, nil
}

func encodeHoldCursor(cursor holdCursor) string {
	body, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeHoldCursor(value string) (holdCursor, error) {
	var cursor holdCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.AsOf.IsZero() || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil || cursor.Priority < 0 || cursor.Priority > 1 {
		return holdCursor{}, ErrInvalidHolds
	}
	return cursor, nil
}

func (s *Service) Get(ctx context.Context, userID, requestID uuid.UUID) (Request, error) {
	item, err := scanRequest(s.pool.QueryRow(ctx, requestSelect+` WHERE r.id=$1 AND r.user_id=$2`, requestID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	return item, err
}

func (s *Service) Cancel(ctx context.Context, userID, requestID uuid.UUID, requestIDHeader string) (Request, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var cancelUntil *time.Time
	if err := tx.QueryRow(ctx, `SELECT status,cancel_until FROM data_rights_requests WHERE id=$1 AND user_id=$2 FOR UPDATE`, requestID, userID).Scan(&status, &cancelUntil); errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	} else if err != nil {
		return Request{}, err
	}
	if !oneOf(status, "queued", "scheduled", "blocked") || (cancelUntil != nil && time.Now().After(*cancelUntil)) {
		return Request{}, ErrNotCancelable
	}
	if _, err := tx.Exec(ctx, `UPDATE data_rights_requests SET status='cancelled',version=version+1,updated_at=now() WHERE id=$1`, requestID); err != nil {
		return Request{}, err
	}
	_, _ = tx.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE status='queued' AND payload->>'requestId'=$1`, requestID.String())
	if err := appendEvent(ctx, tx, requestID, &userID, "cancelled", status, "cancelled", "Owner cancelled within the permitted window", nil); err != nil {
		return Request{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id) VALUES($1,'data_rights.cancelled','data_rights_request',$2,'Owner cancelled the request',$3)`, userID, requestID, safeRequestID(requestIDHeader)); err != nil {
		return Request{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, err
	}
	return s.Get(ctx, userID, requestID)
}

func (s *Service) Download(ctx context.Context, userID, requestID uuid.UUID) ([]byte, string, error) {
	var body []byte
	var checksum, status string
	var expiresAt time.Time
	var purgedAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT a.body,a.checksum_sha256,a.expires_at,a.purged_at,r.status FROM data_rights_requests r JOIN data_rights_export_artifacts a ON a.request_id=r.id WHERE r.id=$1 AND r.user_id=$2 AND r.request_type='data_export'`, requestID, userID).Scan(&body, &checksum, &expiresAt, &purgedAt, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotReady
	}
	if err != nil {
		return nil, "", err
	}
	if purgedAt != nil || time.Now().After(expiresAt) || len(body) == 0 {
		return nil, "", ErrExpired
	}
	if status != "ready" {
		return nil, "", ErrNotReady
	}
	return body, checksum, nil
}

func (s *Service) HandleExportJob(ctx context.Context, job jobs.Job) error {
	requestID, err := payloadRequestID(job.Payload)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID uuid.UUID
	var status, subject string
	if err := tx.QueryRow(ctx, `SELECT user_id,status,subject_ref FROM data_rights_requests WHERE id=$1 AND request_type='data_export' FOR UPDATE`, requestID).Scan(&userID, &status, &subject); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if status != "queued" {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE data_rights_requests SET status='processing',version=version+1,updated_at=now() WHERE id=$1`, requestID); err != nil {
		return err
	}
	generatedAt := time.Now().UTC()
	data, err := s.exportSnapshot(ctx, tx, requestID, userID, subject, generatedAt)
	if err != nil {
		return err
	}
	if len(data) > maximumExportBytes {
		return ErrTooLarge
	}
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])
	expiresAt := generatedAt.Add(exportLifetime)
	if _, err := tx.Exec(ctx, `INSERT INTO data_rights_export_artifacts(request_id,body,checksum_sha256,size_bytes,expires_at) VALUES($1,$2,$3,$4,$5)`, requestID, data, checksum, len(data), expiresAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE data_rights_requests SET status='ready',version=version+1,updated_at=now() WHERE id=$1`, requestID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts,available_at) VALUES($1,jsonb_build_object('requestId',$2::text),5,$3)`, ExportExpiryJobKind, requestID, expiresAt); err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, requestID, nil, "export_ready", "processing", "ready", "Bounded private export package generated", map[string]any{"checksumSha256": checksum, "sizeBytes": len(data), "expiresAt": expiresAt}); err != nil {
		return err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{UserID: userID, Kind: "account.data_rights", Title: "Your data export is ready", Body: "The private export is available for seven days in Account settings.", TargetPath: "/settings", ResourceType: "data_rights_request", ResourceID: &requestID, SourceKey: "data-export-ready:" + requestID.String()}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) HandleExportExpiryJob(ctx context.Context, job jobs.Job) error {
	requestID, err := payloadRequestID(job.Payload)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.data_rights_maintenance','on',true)`); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE data_rights_export_artifacts SET body=NULL,purged_at=now() WHERE request_id=$1 AND purged_at IS NULL AND expires_at<=now()`, requestID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE data_rights_requests SET status='completed',completed_at=now(),version=version+1,updated_at=now() WHERE id=$1 AND status='ready'`, requestID); err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, requestID, nil, "export_purged", "ready", "completed", "Expired export body purged; checksum evidence retained", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) HandleDeletionJob(ctx context.Context, job jobs.Job) error {
	requestID, err := payloadRequestID(job.Payload)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID uuid.UUID
	var status, subject string
	var executeAfter time.Time
	if err := tx.QueryRow(ctx, `SELECT user_id,status,subject_ref,execute_after FROM data_rights_requests WHERE id=$1 AND request_type='account_deletion' FOR UPDATE`, requestID).Scan(&userID, &status, &subject, &executeAfter); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if !oneOf(status, "scheduled", "blocked") || time.Now().Before(executeAfter) {
		return tx.Commit(ctx)
	}
	var holdID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM data_rights_legal_holds WHERE user_id=$1 AND status='active' AND expires_at>now() LIMIT 1`, userID).Scan(&holdID)
	if err == nil {
		if status != "blocked" {
			_, err = tx.Exec(ctx, `UPDATE data_rights_requests SET status='blocked',version=version+1,updated_at=now() WHERE id=$1`, requestID)
			if err != nil {
				return err
			}
			if err := appendEvent(ctx, tx, requestID, nil, "legal_hold_blocked", status, "blocked", "Active scoped legal hold prevented deletion", map[string]any{"holdId": holdID}); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE data_rights_requests SET status='processing',version=version+1,updated_at=now() WHERE id=$1`, requestID); err != nil {
		return err
	}
	if err := s.removeOwnedMedia(ctx, tx, userID); err != nil {
		return err
	}
	deletedLabel := strings.ReplaceAll(userID.String(), "-", "")[:16]
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()),network_hash=NULL WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE jobs SET status='cancelled',updated_at=now()
		WHERE kind=$2 AND status IN ('queued','running')
		  AND payload->>'notificationId' IN (SELECT id::text FROM notifications WHERE user_id=$1)`, userID, notifications.JobKind); err != nil {
		return fmt.Errorf("cancel notification delivery jobs: %w", err)
	}
	for _, statement := range []string{
		`DELETE FROM oauth_accounts WHERE user_id=$1`,
		`DELETE FROM post_reactions WHERE user_id=$1`,
		`DELETE FROM user_follows WHERE follower_id=$1 OR following_id=$1`,
		`DELETE FROM notification_preferences WHERE user_id=$1`,
		`DELETE FROM notifications WHERE user_id=$1`,
		`UPDATE works SET status='removed',prompt=NULL,prompt_visibility='private',summary='',updated_at=now() WHERE author_id=$1`,
		`UPDATE posts SET status='removed',body='[Deleted by account owner]',updated_at=now() WHERE author_id=$1`,
		`UPDATE comments SET status='removed',body='[Deleted by account owner]' WHERE author_id=$1`,
		`UPDATE products SET status='removed',description='[Deleted by account owner]',updated_at=now() WHERE seller_id=$1`,
		`UPDATE generations SET prompt='[Deleted by account owner]',error_message=NULL,updated_at=now() WHERE owner_id=$1`,
		`UPDATE assets SET scan_status='rejected',uploaded_filename=NULL,version_note=NULL,scan_reason='Account deletion completed.',scanned_at=now() WHERE owner_id=$1`,
	} {
		if _, err := tx.Exec(ctx, statement, userID); err != nil {
			return err
		}
	}
	if err := redactSupportData(ctx, tx, userID); err != nil {
		return err
	}
	if err := redactAssetVersionEvidence(ctx, tx, userID); err != nil {
		return err
	}
	if err := redactWebhookData(ctx, tx, userID); err != nil {
		return err
	}
	if err := redactIdentityEmailActions(ctx, tx, userID); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(s.mediaRoot, "mailbox", userID.String())); err != nil {
		return fmt.Errorf("remove identity email mailbox: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET email=$2,handle=$3,display_name='Deleted account',password_hash=NULL,role='member',status='deleted',locale='en-US',timezone='UTC',updated_at=now() WHERE id=$1`, userID, "deleted+"+deletedLabel+"@hcai.invalid", "deleted_"+deletedLabel); err != nil {
		return err
	}
	domains := []map[string]string{
		{"domain": "identity", "disposition": "anonymized"}, {"domain": "sessions", "disposition": "erased"},
		{"domain": "profile", "disposition": "anonymized"}, {"domain": "community", "disposition": "anonymized"},
		{"domain": "tasks", "disposition": "retained_minimal"}, {"domain": "media", "disposition": "erased"},
		{"domain": "creative", "disposition": "anonymized"}, {"domain": "asset_versions", "disposition": "redacted_minimal"},
		{"domain": "notifications", "disposition": "erased"},
		{"domain": "developer_webhooks", "disposition": "redacted_minimal"},
		{"domain": "developer_webhook_credentials", "disposition": "erased"},
		{"domain": "identity_email_actions", "disposition": "redacted_minimal"},
		{"domain": "identity_email_tokens", "disposition": "erased"},
		{"domain": "billing", "disposition": "retained_minimal"}, {"domain": "audit", "disposition": "retained_minimal"},
		{"domain": "safety", "disposition": "retained_minimal"}, {"domain": "support", "disposition": "redacted_minimal"},
		{"domain": "external_providers", "disposition": "not_configured_local_only"},
	}
	receiptBody, _ := json.Marshal(map[string]any{"schemaVersion": 1, "requestId": requestID, "subjectRef": subject, "domains": domains, "productionBackupExpiry": "externally_blocked"})
	sum := sha256.Sum256(receiptBody)
	completedAt := time.Now().UTC()
	if _, err := tx.Exec(ctx, `INSERT INTO data_rights_deletion_receipts(request_id,subject_ref,receipt,checksum_sha256,completed_at) VALUES($1,$2,$3,$4,$5)`, requestID, subject, receiptBody, hex.EncodeToString(sum[:]), completedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE data_rights_requests SET status='completed',completed_at=$2,version=version+1,updated_at=$2 WHERE id=$1`, requestID, completedAt); err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, requestID, nil, "deletion_completed", "processing", "completed", "Local primary deletion completed; production backup expiry remains external", map[string]any{"receiptChecksum": hex.EncodeToString(sum[:])}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata) VALUES('data_rights.deletion_completed','data_rights_request',$1,'Local primary deletion completed','data-rights-worker',jsonb_build_object('subjectRef',$2::text,'receiptChecksum',$3::text))`, requestID, subject, hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) exportSnapshot(ctx context.Context, tx pgx.Tx, requestID, userID uuid.UUID, subject string, generatedAt time.Time) ([]byte, error) {
	queries := map[string]string{
		"account":  `SELECT jsonb_build_object('id',id,'email',email,'handle',handle,'displayName',display_name,'role',role,'status',status,'locale',locale,'timezone',timezone,'createdAt',created_at,'updatedAt',updated_at) FROM users WHERE id=$1`,
		"sessions": `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id,'clientLabel',client_label,'createdAt',created_at,'lastSeenAt',last_seen_at,'expiresAt',expires_at,'revokedAt',revoked_at) ORDER BY created_at),'[]') FROM sessions WHERE user_id=$1`,
		"assets":   `SELECT COALESCE(jsonb_agg(to_jsonb(a)-'media_url' ORDER BY created_at),'[]') FROM assets a WHERE owner_id=$1`,
		"assetVersionEvents": `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',e.id,'familyId',e.family_id,'assetId',e.asset_id,'previousAssetId',e.previous_asset_id,'eventType',e.event_type,'reason',e.reason,'createdAt',e.created_at) ORDER BY e.created_at,e.id),'[]'::jsonb)
			FROM asset_version_events e WHERE e.actor_id=$1`,
		"works":             `SELECT COALESCE(jsonb_agg(to_jsonb(w) ORDER BY created_at),'[]') FROM works w WHERE author_id=$1`,
		"generations":       `SELECT COALESCE(jsonb_agg(to_jsonb(g) ORDER BY created_at),'[]') FROM generations g WHERE owner_id=$1`,
		"communityPosts":    `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY created_at),'[]') FROM posts p WHERE author_id=$1`,
		"communityComments": `SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY created_at),'[]') FROM comments c WHERE author_id=$1`,
		"savedWorks": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'postId',pr.post_id,'workId',p.work_id,'savedAt',pr.created_at
		) ORDER BY pr.created_at,pr.post_id),'[]'::jsonb)
			FROM post_reactions pr JOIN posts p ON p.id=pr.post_id
			WHERE pr.user_id=$1 AND pr.kind='bookmark'`,
		"communityFollows": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'followingId',f.following_id,'createdAt',f.created_at
		) ORDER BY f.created_at,f.following_id),'[]'::jsonb)
			FROM user_follows f WHERE f.follower_id=$1`,
		"orders":     `SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY created_at),'[]') FROM orders o WHERE buyer_id=$1`,
		"tasks":      `SELECT COALESCE(jsonb_agg(to_jsonb(d) ORDER BY created_at),'[]') FROM demands d WHERE client_id=$1 OR assignee_id=$1`,
		"proposals":  `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY created_at),'[]') FROM proposals p WHERE creator_id=$1`,
		"deliveries": `SELECT COALESCE(jsonb_agg(to_jsonb(d) ORDER BY created_at),'[]') FROM deliveries d WHERE creator_id=$1`,
		"billing":    `SELECT COALESCE(jsonb_agg(to_jsonb(l) ORDER BY created_at),'[]') FROM ledger_entries l WHERE account_id=$1`,
		"notifications": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id',n.id,'kind',n.kind,'title',n.title,'body',n.body,'targetPath',n.target_path,
			'resourceType',n.resource_type,'resourceId',n.resource_id,'readAt',n.read_at,
			'deliveryStatus',n.delivery_status,'deliveryErrorCode',n.delivery_error_code,
			'deliveredAt',n.delivered_at,'suppressedAt',n.suppressed_at,'createdAt',n.created_at
		) ORDER BY n.created_at,n.id),'[]'::jsonb) FROM notifications n WHERE n.user_id=$1`,
		"developerWebhookEndpoints": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id',e.id,'name',e.name,'url',e.url,'eventTypes',e.event_types,'status',e.status,
			'currentSecretVersion',e.current_secret_version,'version',e.version,
			'createdAt',e.created_at,'updatedAt',e.updated_at,'revokedAt',e.revoked_at
		) ORDER BY e.created_at,e.id),'[]'::jsonb) FROM developer_webhook_endpoints e WHERE e.owner_id=$1`,
		"developerWebhookEvents": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id',e.id,'eventType',e.event_type,'resourceType',e.resource_type,'resourceId',e.resource_id,
			'payload',e.payload,'createdAt',e.created_at
		) ORDER BY e.created_at,e.id),'[]'::jsonb) FROM developer_webhook_events e WHERE e.owner_id=$1`,
		"developerWebhookDeliveries": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id',d.id,'endpointId',d.endpoint_id,'eventId',d.event_id,'status',d.status,'version',d.version,
			'attemptCount',d.attempt_count,'nextAttemptAt',d.next_attempt_at,'lastStatusCode',d.last_status_code,
			'lastErrorCode',d.last_error_code,'originalDeliveryId',d.original_delivery_id,
			'createdAt',d.created_at,'updatedAt',d.updated_at,'succeededAt',d.succeeded_at,'deadLetteredAt',d.dead_lettered_at,
			'attempts',COALESCE((SELECT jsonb_agg(jsonb_build_object(
				'attemptNumber',a.attempt_number,'statusCode',a.status_code,'errorCode',a.error_code,
				'responseSha256',a.response_sha256,'durationMs',a.duration_ms,'attemptedAt',a.attempted_at
			) ORDER BY a.attempt_number) FROM developer_webhook_delivery_attempts a WHERE a.delivery_id=d.id),'[]'::jsonb)
		) ORDER BY d.created_at,d.id),'[]'::jsonb)
		FROM developer_webhook_deliveries d
		JOIN developer_webhook_endpoints e ON e.id=d.endpoint_id WHERE e.owner_id=$1`,
		"identityEmailActions": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id',a.id,'kind',a.kind,'status',a.status,'locale',a.locale,'version',a.version,
			'attemptCount',a.attempt_count,'originalActionId',a.original_action_id,'expiresAt',a.expires_at,
			'createdAt',a.created_at,'updatedAt',a.updated_at,'deliveredAt',a.delivered_at,
			'consumedAt',a.consumed_at,'cancelledAt',a.cancelled_at,'deadLetteredAt',a.dead_lettered_at,
			'attempts',COALESCE((SELECT jsonb_agg(jsonb_build_object(
				'attemptNumber',d.attempt_number,'adapter',d.adapter,'status',d.status,'errorCode',d.error_code,
				'receiptSha256',d.receipt_sha256,'attemptedAt',d.attempted_at
			) ORDER BY d.attempt_number) FROM identity_email_delivery_attempts d WHERE d.action_id=a.id),'[]'::jsonb)
		) ORDER BY a.created_at,a.id),'[]'::jsonb) FROM identity_email_actions a WHERE a.user_id=$1`,
		"supportCases": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id',c.id,'category',c.category,'subject',c.subject,'details',c.details,
			'relatedResourceType',c.related_resource_type,'relatedResourceId',c.related_resource_id,
			'locale',c.locale,'claimantRelationship',c.claimant_relationship,'rightsStatement',c.rights_statement,
			'status',c.status,'version',c.version,'resolutionCode',c.resolution_code,'resolutionReason',c.resolution_reason,
			'createdAt',c.created_at,'updatedAt',c.updated_at,'resolvedAt',c.resolved_at,
			'messages',COALESCE((SELECT jsonb_agg(jsonb_build_object(
				'id',m.id,'authorRole',m.author_role,'body',m.body,'createdAt',m.created_at
			) ORDER BY m.created_at,m.id) FROM support_messages m WHERE m.case_id=c.id),'[]'::jsonb),
			'events',COALESCE((SELECT jsonb_agg(jsonb_build_object(
				'id',e.id,'kind',e.kind,'fromStatus',e.from_status,'toStatus',e.to_status,
				'reason',e.reason,'metadata',e.metadata,'createdAt',e.created_at
			) ORDER BY e.created_at,e.id) FROM support_events e WHERE e.case_id=c.id),'[]'::jsonb)
		) ORDER BY c.created_at,c.id),'[]'::jsonb) FROM support_cases c WHERE c.requester_id=$1`,
		"riskSignals": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id',s.id,'resourceType',s.resource_type,'resourceId',s.resource_id,'signalType',s.signal_type,
			'severity',s.severity,'score',s.score,'status',s.status,'summary',s.summary,'evidence',s.evidence,
			'detectedAt',s.detected_at,'updatedAt',s.updated_at,'resolvedAt',s.resolved_at
		) ORDER BY s.detected_at,s.id),'[]'::jsonb) FROM risk_signals s WHERE s.subject_user_id=$1`,
		"audit": `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id,'action',action,'resourceType',resource_type,'resourceId',resource_id,'reason',reason,'requestId',request_id,'createdAt',created_at) ORDER BY created_at),'[]') FROM audit_events WHERE actor_id=$1`,
	}
	data := make(map[string]json.RawMessage, len(queries))
	for key, query := range queries {
		var value []byte
		if err := tx.QueryRow(ctx, query, userID).Scan(&value); err != nil {
			return nil, fmt.Errorf("export %s: %w", key, err)
		}
		data[key] = value
	}
	return json.Marshal(map[string]any{"schemaVersion": 1, "requestId": requestID, "subjectRef": subject, "generatedAt": generatedAt, "data": data})
}

func redactSupportData(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.support_data_rights_maintenance','on',true)`); err != nil {
		return fmt.Errorf("enable support data-rights maintenance: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE support_messages SET body='[Redacted following account deletion]'
		WHERE case_id IN (SELECT id FROM support_cases WHERE requester_id=$1)`, userID); err != nil {
		return fmt.Errorf("redact support messages: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE support_events SET reason='Redacted following account deletion.',metadata='{}'::jsonb
		WHERE case_id IN (SELECT id FROM support_cases WHERE requester_id=$1)`, userID); err != nil {
		return fmt.Errorf("redact support events: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE support_cases SET subject='Account support record',details='[Redacted following account deletion]',
		 rights_statement=CASE WHEN rights_statement IS NULL THEN NULL ELSE '[Redacted following account deletion]' END,
		 resolution_reason=CASE WHEN resolution_reason IS NULL THEN NULL ELSE 'Redacted following account deletion.' END,
		 personal_data_redacted_at=now(),updated_at=now(),version=version+1
		WHERE requester_id=$1 AND personal_data_redacted_at IS NULL`, userID); err != nil {
		return fmt.Errorf("redact support cases: %w", err)
	}
	return nil
}

func redactAssetVersionEvidence(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.asset_data_rights_maintenance','on',true)`); err != nil {
		return fmt.Errorf("enable Asset data-rights maintenance: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE asset_version_events SET reason='Redacted following account deletion.' WHERE actor_id=$1`, userID); err != nil {
		return fmt.Errorf("redact Asset version evidence: %w", err)
	}
	return nil
}

func redactWebhookData(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.webhook_data_rights_maintenance','on',true)`); err != nil {
		return fmt.Errorf("enable Webhook data-rights maintenance: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE jobs SET status='cancelled',updated_at=now()
		WHERE kind='webhook.deliver' AND status IN ('queued','running')
		  AND payload->>'deliveryId' IN (
			SELECT d.id::text FROM developer_webhook_deliveries d
			JOIN developer_webhook_endpoints e ON e.id=d.endpoint_id WHERE e.owner_id=$1
		  )`, userID); err != nil {
		return fmt.Errorf("cancel owned Webhook jobs: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE developer_webhook_deliveries
		SET status='cancelled',version=version+1,updated_at=now(),next_attempt_at=NULL
		WHERE endpoint_id IN (SELECT id FROM developer_webhook_endpoints WHERE owner_id=$1)
		  AND status IN ('queued','delivering','retry_scheduled')`, userID); err != nil {
		return fmt.Errorf("cancel owned Webhook deliveries: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE developer_webhook_endpoints
		SET name='Deleted endpoint ' || substr(id::text,1,8),url='https://deleted.invalid/webhook',
		    status='revoked',revoked_at=COALESCE(revoked_at,now()),version=version+1,
		    updated_at=now(),personal_data_redacted_at=now()
		WHERE owner_id=$1 AND personal_data_redacted_at IS NULL`, userID); err != nil {
		return fmt.Errorf("redact owned Webhook endpoints: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE developer_webhook_events
		SET resource_type='redacted',resource_id=NULL,source_key='deleted:' || id::text,
		    payload=jsonb_build_object('schemaVersion',1,'eventId',id,'type',event_type,'createdAt',created_at)
		WHERE owner_id=$1`, userID); err != nil {
		return fmt.Errorf("minimize owned Webhook events: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM developer_webhook_secret_revisions
		WHERE endpoint_id IN (SELECT id FROM developer_webhook_endpoints WHERE owner_id=$1)`, userID); err != nil {
		return fmt.Errorf("erase owned Webhook signing secrets: %w", err)
	}
	return nil
}

func redactIdentityEmailActions(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE jobs SET status='cancelled',updated_at=now()
		WHERE kind IN ('identity.email_action.deliver','identity.email_action.expire')
		  AND status IN ('queued','running')
		  AND payload->>'actionId' IN (SELECT id::text FROM identity_email_actions WHERE user_id=$1)`, userID); err != nil {
		return fmt.Errorf("cancel identity email jobs: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE identity_email_actions
		SET status=CASE WHEN status IN ('queued','delivered','dead_letter') THEN 'cancelled' ELSE status END,
		    email_snapshot='deleted+'||substr(id::text,1,8)||'@invalid.local',
		    token_hash=NULL,token_nonce=NULL,token_ciphertext=NULL,
		    cancelled_at=CASE WHEN status IN ('queued','delivered','dead_letter') THEN now() ELSE cancelled_at END,
		    dead_lettered_at=NULL,updated_at=now(),version=version+1
		WHERE user_id=$1`, userID); err != nil {
		return fmt.Errorf("redact identity email actions: %w", err)
	}
	return nil
}

func (s *Service) removeOwnedMedia(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT storage_backend,storage_key FROM assets WHERE owner_id=$1 AND source_type IN ('generation','upload')`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var backend, key string
		if err := rows.Scan(&backend, &key); err != nil {
			return err
		}
		store, err := s.stores.Get(backend)
		if err != nil {
			return fmt.Errorf("resolve owned media storage: %w", err)
		}
		if err := store.Delete(ctx, key); err != nil {
			return fmt.Errorf("remove owned media: %w", err)
		}
	}
	return rows.Err()
}

const requestSelect = `
	SELECT r.id,r.user_id,r.request_type,r.status,r.subject_ref,r.execute_after,r.cancel_until,r.completed_at,r.failure_code,r.version,r.created_at,r.updated_at,
	       u.handle,u.email,a.checksum_sha256,a.size_bytes,a.expires_at,a.purged_at,
	       dr.id,dr.checksum_sha256,dr.completed_at,dr.receipt->'domains'
	FROM data_rights_requests r JOIN users u ON u.id=r.user_id
	LEFT JOIN data_rights_export_artifacts a ON a.request_id=r.id
	LEFT JOIN data_rights_deletion_receipts dr ON dr.request_id=r.id`

type rowScanner interface{ Scan(...any) error }

func scanRequest(row rowScanner) (Request, error) {
	var item Request
	var exportChecksum *string
	var exportSize *int64
	var exportExpiry, exportPurged *time.Time
	var receiptID *uuid.UUID
	var receiptChecksum *string
	var receiptCompleted *time.Time
	var receiptDomains []byte
	err := row.Scan(&item.ID, &item.OwnerID, &item.RequestType, &item.Status, &item.SubjectRef, &item.ExecuteAfter, &item.CancelUntil, &item.CompletedAt, &item.FailureCode, &item.Version, &item.CreatedAt, &item.UpdatedAt,
		&item.OwnerHandle, &item.OwnerEmail, &exportChecksum, &exportSize, &exportExpiry, &exportPurged, &receiptID, &receiptChecksum, &receiptCompleted, &receiptDomains)
	if err != nil {
		return Request{}, err
	}
	if exportChecksum != nil {
		item.Export = &ExportEvidence{ChecksumSHA256: *exportChecksum, SizeBytes: *exportSize, ExpiresAt: *exportExpiry, PurgedAt: exportPurged}
	}
	if receiptID != nil {
		item.Receipt = &Receipt{ID: *receiptID, ChecksumSHA256: *receiptChecksum, CompletedAt: *receiptCompleted, Domains: receiptDomains}
	}
	return item, nil
}

func appendEvent(ctx context.Context, tx pgx.Tx, requestID uuid.UUID, actorID *uuid.UUID, eventType, from, to, reason string, evidence any) error {
	if evidence == nil {
		evidence = map[string]any{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO data_rights_events(request_id,actor_id,event_type,from_status,to_status,reason,evidence) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7)`, requestID, actorID, eventType, from, to, reason, evidence)
	return err
}

func payloadRequestID(payload json.RawMessage) (uuid.UUID, error) {
	var input struct {
		RequestID uuid.UUID `json:"requestId"`
	}
	if err := json.Unmarshal(payload, &input); err != nil || input.RequestID == uuid.Nil {
		return uuid.Nil, ErrInvalid
	}
	return input.RequestID, nil
}

func subjectRef(userID uuid.UUID) string {
	sum := sha256.Sum256([]byte("hcai-data-rights:" + userID.String()))
	return "subject_" + hex.EncodeToString(sum[:])[:24]
}

func safeRequestID(value string) string {
	if strings.TrimSpace(value) == "" {
		return "data-rights"
	}
	return value
}

func isDemoUser(id uuid.UUID) bool {
	return id.String() == identity.DemoUserID || id.String() == identity.DemoPublisherID || id.String() == identity.DemoAdminID
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key")
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
