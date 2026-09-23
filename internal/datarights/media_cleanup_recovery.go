package datarights

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5"
)

var ErrCleanupForbidden = errors.New("media cleanup operation is not authorized")

type MediaCleanup struct {
	ID                uuid.UUID  `json:"id"`
	UserID            *uuid.UUID `json:"userId,omitempty"`
	OrderID           *uuid.UUID `json:"orderId,omitempty"`
	Kind              string     `json:"kind"`
	Status            string     `json:"status"`
	Attempts          int        `json:"attempts"`
	MaxAttempts       int        `json:"maxAttempts"`
	ErrorCode         *string    `json:"errorCode,omitempty"`
	RetryJobID        *uuid.UUID `json:"retryJobId,omitempty"`
	RetryOf           *uuid.UUID `json:"retryOf,omitempty"`
	CanRetry          bool       `json:"canRetry"`
	UnavailableReason string     `json:"unavailableReason"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}
type MediaCleanupPage struct {
	Items      []MediaCleanup `json:"items"`
	NextCursor *string        `json:"nextCursor,omitempty"`
}
type MediaCleanupListInput struct {
	ListInput
	Status string
	Kind   string
}
type MediaCleanupRetryInput struct {
	ExpectedAttempts *int   `json:"expectedAttempts"`
	Reason           string `json:"reason"`
	Confirmed        bool   `json:"confirmed"`
}
type cleanupCursor struct {
	Scope     string    `json:"scope"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"at"`
	ID        uuid.UUID `json:"id"`
}

const cleanupSelect = `SELECT j.id,COALESCE(u.id,o.buyer_id),o.id,
 CASE WHEN j.kind='data_rights.media_cleanup' THEN 'account' ELSE 'product' END,
 j.status,j.attempts,j.max_attempts,j.last_error_code,r.retry_job_id,parent.original_job_id,
 CASE WHEN j.status<>'failed' THEN 'not_failed'
 WHEN r.retry_job_id IS NOT NULL THEN 'already_retried'
 WHEN (j.kind='data_rights.media_cleanup' AND u.id IS NULL) OR
      (j.kind='product.delivery_cleanup' AND (o.id IS NULL OR d.order_id IS NULL)) THEN 'missing_subject'
 WHEN j.kind='data_rights.media_cleanup' AND u.status<>'deleted' THEN 'account_active'
 WHEN j.kind='data_rights.media_cleanup' AND (` + originalMediaReconciliationReason + `)<>'' THEN (` + originalMediaReconciliationReason + `)
 WHEN j.kind='product.delivery_cleanup' AND d.state='removed' THEN 'already_removed'
 WHEN j.kind='product.delivery_cleanup' AND EXISTS(SELECT 1 FROM product_order_funds_retention f WHERE f.order_id=o.id) THEN 'order_unresolved'
 WHEN j.kind='product.delivery_cleanup' AND d.needed THEN 'delivery_required'
 WHEN j.kind='product.delivery_cleanup' AND j.payload->>'retentionCheck'='resolved_order' AND NOT EXISTS(SELECT 1 FROM product_cleanup_resolved_orders WHERE order_id=o.id) THEN 'order_unresolved'
 WHEN COALESCE(d.held,false) OR EXISTS(SELECT 1 FROM data_rights_legal_holds h WHERE h.user_id=u.id AND h.status='active' AND h.expires_at>now()) THEN 'legal_hold'
 WHEN EXISTS(SELECT 1 FROM jobs active WHERE active.kind=j.kind AND active.status IN ('queued','running')
  AND ((j.kind='data_rights.media_cleanup' AND active.payload->>'userId'=u.id::text)
    OR (j.kind='product.delivery_cleanup' AND active.payload->>'orderId'=o.id::text))) THEN 'active_job'
 ELSE '' END,j.created_at,j.updated_at
 FROM jobs j
 LEFT JOIN users u ON j.kind='data_rights.media_cleanup' AND u.id::text=j.payload->>'userId'
 LEFT JOIN orders o ON j.kind='product.delivery_cleanup' AND o.id::text=j.payload->>'orderId'
 LEFT JOIN product_delivery_cleanup_policy d ON d.order_id=o.id
 LEFT JOIN media_cleanup_recoveries r ON r.original_job_id=j.id
 LEFT JOIN media_cleanup_recoveries parent ON parent.retry_job_id=j.id`

func scanMediaCleanup(row interface{ Scan(...any) error }) (MediaCleanup, error) {
	var item MediaCleanup
	err := row.Scan(&item.ID, &item.UserID, &item.OrderID, &item.Kind, &item.Status, &item.Attempts, &item.MaxAttempts, &item.ErrorCode, &item.RetryJobID, &item.RetryOf, &item.UnavailableReason, &item.CreatedAt, &item.UpdatedAt)
	item.CanRetry = item.UnavailableReason == ""
	return item, err
}

// These bounded operational reads expand policy views with many rarely taken
// branches. JIT compilation dominates their execution even for a single job.
// Keep the exception transaction-local so pooled connections and unrelated
// analytical queries retain their configured JIT setting.
func disableCleanupPolicyJIT(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SET LOCAL jit = off`)
	return err
}

func (s *Service) ListMediaCleanups(ctx context.Context, input MediaCleanupListInput) (MediaCleanupPage, error) {
	page := MediaCleanupPage{Items: []MediaCleanup{}}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Kind == "" {
		input.Kind = "all"
	}
	if input.Kind != "all" && input.Kind != "account" && input.Kind != "product" {
		return page, ErrInvalidList
	}
	if input.Status == "" {
		input.Status = "failed"
	}
	if input.Limit < 1 || input.Limit > 50 || len(input.Cursor) > 1024 {
		return page, ErrInvalidList
	}
	switch input.Status {
	case "all", "failed", "queued", "running", "succeeded", "cancelled":
	default:
		return page, ErrInvalidList
	}
	var at *time.Time
	var id *uuid.UUID
	if input.Cursor != "" {
		body, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		var cursor cleanupCursor
		if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.Scope != "media_cleanup_v2" || cursor.Kind != input.Kind || cursor.Status != input.Status || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
			return page, ErrInvalidList
		}
		at, id = &cursor.CreatedAt, &cursor.ID
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer tx.Rollback(ctx)
	if err := disableCleanupPolicyJIT(ctx, tx); err != nil {
		return page, err
	}
	rows, err := tx.Query(ctx, cleanupSelect+` WHERE j.kind IN ('data_rights.media_cleanup','product.delivery_cleanup')
 AND ($1='all' OR ($1='account' AND j.kind='data_rights.media_cleanup') OR ($1='product' AND j.kind='product.delivery_cleanup'))
 AND ($2='all' OR j.status=$2)
 AND ($3::timestamptz IS NULL OR (j.created_at,j.id)<($3,$4)) ORDER BY j.created_at DESC,j.id DESC LIMIT $5`, input.Kind, input.Status, at, id, input.Limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanMediaCleanup(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		body, _ := json.Marshal(cleanupCursor{Scope: "media_cleanup_v2", Kind: input.Kind, Status: input.Status, CreatedAt: last.CreatedAt, ID: last.ID})
		cursor := base64.RawURLEncoding.EncodeToString(body)
		page.NextCursor = &cursor
	}
	return page, tx.Commit(ctx)
}

func (s *Service) RetryMediaCleanup(ctx context.Context, actorID, jobID uuid.UUID, input MediaCleanupRetryInput, requestID string) (MediaCleanup, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if actorID == uuid.Nil || jobID == uuid.Nil || !input.Confirmed || input.ExpectedAttempts == nil || *input.ExpectedAttempts < 0 || !utf8.ValidString(input.Reason) || strings.ContainsRune(input.Reason, 0) || utf8.RuneCountInString(input.Reason) < 10 || utf8.RuneCountInString(input.Reason) > 2000 {
		return MediaCleanup{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return MediaCleanup{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorized := func() error {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN role_permissions rp ON rp.role=u.role WHERE u.id=$1 AND u.status='active' AND rp.permission_id='admin:data-rights')`, actorID).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return ErrCleanupForbidden
		}
		return nil
	}
	if err := authorized(); err != nil {
		return MediaCleanup{}, err
	}
	var kind, status string
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT kind,payload,status FROM jobs WHERE id=$1 AND kind IN ($2,$3) FOR UPDATE`, jobID, MediaCleanupJobKind, productdelivery.CleanupJobKind).Scan(&kind, &raw, &status); errors.Is(err, pgx.ErrNoRows) {
		return MediaCleanup{}, ErrNotFound
	} else if err != nil {
		return MediaCleanup{}, err
	}
	// Successful jobs can be reconciliation predecessors. Reject them before
	// taking subjects, allowing the dispatcher to record its predecessor FK.
	if status != "failed" {
		return MediaCleanup{}, ErrConflict
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(raw, &payload) != nil {
		return MediaCleanup{}, ErrConflict
	}
	field := "userId"
	if kind == productdelivery.CleanupJobKind {
		field = "orderId"
	}
	var subject uuid.UUID
	if json.Unmarshal(payload[field], &subject) != nil || subject == uuid.Nil {
		return MediaCleanup{}, ErrConflict
	}
	// Serialize different failed jobs for the same subject and kind. Original
	// attempts/evidence stay intact, including when a replacement fails later.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "media-cleanup-recovery:"+kind+":"+subject.String()); err != nil {
		return MediaCleanup{}, err
	}
	if kind == productdelivery.CleanupJobKind {
		if err := productdelivery.LockCleanupTx(ctx, tx, subject); err != nil {
			return MediaCleanup{}, err
		}
	} else {
		// Share CreateHold's lock before projecting eligibility. The worker
		// repeats this check when the replacement job eventually runs.
		if _, err := tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, subject); err != nil {
			return MediaCleanup{}, err
		}
	}
	// The initial authorization can predate a long wait on any of these locks.
	if err := authorized(); err != nil {
		return MediaCleanup{}, err
	}
	// These conditions already make recovery impossible, independent of the
	// financial/retention policy. Check them while holding the same subject
	// locks as automatic scheduling, rather than expanding that policy once
	// for every concurrent request waiting behind a successful recovery.
	var alreadyScheduled bool
	if err := tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM media_cleanup_recoveries WHERE original_job_id=$1)
 OR EXISTS(SELECT 1 FROM jobs WHERE kind=$2 AND status IN ('queued','running')
 AND payload->>$3=$4)`, jobID, kind, field, subject.String()).Scan(&alreadyScheduled); err != nil {
		return MediaCleanup{}, err
	}
	if alreadyScheduled {
		return MediaCleanup{}, ErrConflict
	}
	if err := disableCleanupPolicyJIT(ctx, tx); err != nil {
		return MediaCleanup{}, err
	}
	current, err := scanMediaCleanup(tx.QueryRow(ctx, cleanupSelect+` WHERE j.id=$1`, jobID))
	if err != nil {
		return MediaCleanup{}, err
	}
	if !current.CanRetry || current.Attempts != *input.ExpectedAttempts {
		return MediaCleanup{}, ErrConflict
	}
	var retryID uuid.UUID
	// Check authority in the enqueue statement as well: a role/status or
	// permission revocation may commit after the post-lock check but before
	// this command is executed. The locked original job cannot disappear;
	// no inserted row here therefore means the operator is no longer allowed.
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
 SELECT $1,jsonb_build_object($2::text,$3::text) || CASE WHEN kind='product.delivery_cleanup' AND payload->>'retentionCheck'='resolved_order'
 THEN jsonb_build_object('retentionCheck','resolved_order') ELSE '{}'::jsonb END,20 FROM jobs WHERE id=$4
 AND EXISTS(SELECT 1 FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=$5 AND u.status='active' AND rp.permission_id='admin:data-rights')
 RETURNING id`, kind, field, subject, jobID, actorID).Scan(&retryID); errors.Is(err, pgx.ErrNoRows) {
		return MediaCleanup{}, ErrCleanupForbidden
	} else if err != nil {
		return MediaCleanup{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO media_cleanup_recoveries(original_job_id,retry_job_id,requested_by,expected_attempts,reason) VALUES($1,$2,$3,$4,$5)`, jobID, retryID, actorID, input.ExpectedAttempts, input.Reason); err != nil {
		return MediaCleanup{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
 VALUES($1,'data_rights.media_cleanup_retried','job',$2,$3,$4,jsonb_build_object('retryJobId',$5::text,'expectedAttempts',$6::integer,'kind',$7::text,'subjectId',$8::text))`, actorID, jobID, input.Reason, requestID, retryID, input.ExpectedAttempts, kind, subject); err != nil {
		return MediaCleanup{}, err
	}
	// This newly inserted job is still invisible outside this transaction and
	// cannot have run or failed. Return its actual stored metadata without
	// evaluating the complete retry policy again for a known queued job.
	item := MediaCleanup{UserID: current.UserID, OrderID: current.OrderID, Kind: current.Kind,
		RetryOf: &jobID, UnavailableReason: "not_failed"}
	if err := tx.QueryRow(ctx, `SELECT id,status,attempts,max_attempts,last_error_code,created_at,updated_at
 FROM jobs WHERE id=$1`, retryID).Scan(&item.ID, &item.Status, &item.Attempts, &item.MaxAttempts,
		&item.ErrorCode, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return MediaCleanup{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MediaCleanup{}, err
	}
	return item, nil
}
