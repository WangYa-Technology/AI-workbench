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
	"github.com/jackc/pgx/v5"
)

type RequestJob struct {
	ID                uuid.UUID  `json:"id"`
	RequestID         *uuid.UUID `json:"requestId,omitempty"`
	UserID            *uuid.UUID `json:"userId,omitempty"`
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
type RequestJobPage struct {
	Items      []RequestJob `json:"items"`
	NextCursor *string      `json:"nextCursor,omitempty"`
}

// Keep the existing export service contract while sharing recovery mechanics.
type ExportJob = RequestJob
type ExportJobPage = RequestJobPage
type DeletionJob = RequestJob
type DeletionJobPage = RequestJobPage

type requestRecoveryPolicy struct {
	requestType, scope, table, view, action string
	kinds                                   []string
	jobKinds                                []string
}

var exportRecoveryPolicy = requestRecoveryPolicy{
	requestType: "data_export", scope: "data_export_jobs_v1", table: "data_export_recoveries", view: "data_export_job_policy", action: "export_job_retried",
	kinds: []string{"all", "export", "expiry"}, jobKinds: []string{ExportJobKind, ExportExpiryJobKind},
}
var deletionRecoveryPolicy = requestRecoveryPolicy{
	requestType: "account_deletion", scope: "account_deletion_jobs_v1", table: "account_deletion_recoveries", view: "account_deletion_job_policy", action: "deletion_job_retried",
	kinds: []string{"all", "prepare", "cleanup"}, jobKinds: []string{DeletionJobKind},
}

// Identifiers come only from the private policies above, never user input.
func (p requestRecoveryPolicy) selectJobs() string {
	reason := "unavailable_reason"
	if p.requestType == "account_deletion" {
		reason = `CASE WHEN unavailable_reason='' AND EXISTS(SELECT 1 FROM account_deletion_reconciliations evidence WHERE evidence.request_id=account_deletion_job_policy.request_id)
 THEN COALESCE((SELECT policy.unavailable_reason FROM account_deletion_reconciliation_policy policy WHERE policy.request_id=account_deletion_job_policy.request_id),'inconsistent_stage') ELSE unavailable_reason END`
	}
	return `SELECT id,request_id,user_id,kind,status,attempts,max_attempts,last_error_code,
 retry_job_id,retry_of,` + reason + `,created_at,updated_at FROM ` + p.view
}

func scanRequestJob(row rowScanner) (RequestJob, error) {
	var item RequestJob
	err := row.Scan(&item.ID, &item.RequestID, &item.UserID, &item.Kind, &item.Status, &item.Attempts, &item.MaxAttempts, &item.ErrorCode, &item.RetryJobID, &item.RetryOf, &item.UnavailableReason, &item.CreatedAt, &item.UpdatedAt)
	item.CanRetry = item.UnavailableReason == ""
	return item, err
}

func (s *Service) ListExportJobs(ctx context.Context, input MediaCleanupListInput) (ExportJobPage, error) {
	return s.listRequestJobs(ctx, input, exportRecoveryPolicy)
}
func (s *Service) ListDeletionJobs(ctx context.Context, input MediaCleanupListInput) (DeletionJobPage, error) {
	return s.listRequestJobs(ctx, input, deletionRecoveryPolicy)
}
func (s *Service) listRequestJobs(ctx context.Context, input MediaCleanupListInput, policy requestRecoveryPolicy) (RequestJobPage, error) {
	page := RequestJobPage{Items: []RequestJob{}}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Kind == "" {
		input.Kind = "all"
	}
	if input.Status == "" {
		input.Status = "failed"
	}
	if input.Limit < 1 || input.Limit > 50 || len(input.Cursor) > 1024 || !oneOf(input.Kind, policy.kinds...) || !oneOf(input.Status, "all", "failed", "queued", "running", "succeeded", "cancelled") {
		return page, ErrInvalidList
	}
	var at *time.Time
	var id *uuid.UUID
	if input.Cursor != "" {
		body, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		var cursor cleanupCursor
		if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.Scope != policy.scope || cursor.Kind != input.Kind || cursor.Status != input.Status || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
			return page, ErrInvalidList
		}
		at, id = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, policy.selectJobs()+` WHERE ($1='all' OR kind=$1) AND ($2='all' OR status=$2)
 AND ($3::timestamptz IS NULL OR (created_at,id)<($3,$4)) ORDER BY created_at DESC,id DESC LIMIT $5`, input.Kind, input.Status, at, id, input.Limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanRequestJob(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		body, _ := json.Marshal(cleanupCursor{Scope: policy.scope, Kind: input.Kind, Status: input.Status, CreatedAt: last.CreatedAt, ID: last.ID})
		cursor := base64.RawURLEncoding.EncodeToString(body)
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) RetryExportJob(ctx context.Context, actorID, jobID uuid.UUID, input MediaCleanupRetryInput, requestHeader string) (ExportJob, error) {
	return s.retryRequestJob(ctx, actorID, jobID, input, requestHeader, exportRecoveryPolicy)
}
func (s *Service) RetryDeletionJob(ctx context.Context, actorID, jobID uuid.UUID, input MediaCleanupRetryInput, requestHeader string) (DeletionJob, error) {
	return s.retryRequestJob(ctx, actorID, jobID, input, requestHeader, deletionRecoveryPolicy)
}
func (s *Service) retryRequestJob(ctx context.Context, actorID, jobID uuid.UUID, input MediaCleanupRetryInput, requestHeader string, policy requestRecoveryPolicy) (RequestJob, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if actorID == uuid.Nil || jobID == uuid.Nil || !input.Confirmed || input.ExpectedAttempts == nil || *input.ExpectedAttempts < 0 || !utf8.ValidString(input.Reason) || strings.ContainsRune(input.Reason, 0) || utf8.RuneCountInString(input.Reason) < 10 || utf8.RuneCountInString(input.Reason) > 2000 {
		return RequestJob{}, ErrInvalid
	}
	// Post-lock and enqueue authorization must see committed revocations,
	// including on installations with a stronger default transaction isolation.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RequestJob{}, err
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
	if err = authorized(); err != nil {
		return RequestJob{}, err
	}
	// Read a candidate binding, then lock the request before the job. The worker
	// holds the request during generation; a late completion must win over retry.
	var requestID, userID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT r.id,r.user_id FROM jobs j JOIN data_rights_requests r ON r.id::text=j.payload->>'requestId'
 WHERE j.id=$1 AND j.kind=ANY($2::text[]) AND r.request_type=$3`, jobID, policy.jobKinds, policy.requestType).Scan(&requestID, &userID); errors.Is(err, pgx.ErrNoRows) {
		return RequestJob{}, ErrNotFound
	} else if err != nil {
		return RequestJob{}, err
	}
	if policy.requestType == "account_deletion" {
		if err = lockDeletionSubject(ctx, tx, userID); err != nil {
			return RequestJob{}, err
		}
	}
	var requestState string
	if err = tx.QueryRow(ctx, `SELECT status FROM data_rights_requests WHERE id=$1 FOR UPDATE`, requestID).Scan(&requestState); err != nil {
		return RequestJob{}, err
	}
	var kind string
	if err = tx.QueryRow(ctx, `SELECT kind FROM jobs WHERE id=$1 AND payload->>'requestId'=$2 AND kind=ANY($3::text[]) FOR UPDATE`, jobID, requestID.String(), policy.jobKinds).Scan(&kind); errors.Is(err, pgx.ErrNoRows) {
		return RequestJob{}, ErrConflict
	} else if err != nil {
		return RequestJob{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, userID); err != nil {
		return RequestJob{}, err
	}
	if err = authorized(); err != nil {
		return RequestJob{}, err
	}
	var priorID, priorActor uuid.UUID
	var priorReason string
	var priorAttempts int
	err = tx.QueryRow(ctx, `SELECT retry_job_id,requested_by,reason,expected_attempts FROM `+policy.table+` WHERE original_job_id=$1`, jobID).Scan(&priorID, &priorActor, &priorReason, &priorAttempts)
	if err == nil {
		if priorActor != actorID || priorReason != input.Reason || priorAttempts != *input.ExpectedAttempts {
			return RequestJob{}, ErrConflict
		}
		item, err := scanRequestJob(tx.QueryRow(ctx, policy.selectJobs()+` WHERE id=$1`, priorID))
		if err != nil {
			return RequestJob{}, err
		}
		return item, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RequestJob{}, err
	}
	current, err := scanRequestJob(tx.QueryRow(ctx, policy.selectJobs()+` WHERE id=$1`, jobID))
	if err != nil {
		return RequestJob{}, err
	}
	if !current.CanRetry || current.Attempts != *input.ExpectedAttempts {
		return RequestJob{}, ErrConflict
	}
	var retryID uuid.UUID
	// Bind the mutation to current authority in the same statement, not only
	// the earlier permission check before policy evaluation or database waits.
	if err = tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
 SELECT $1,jsonb_build_object('requestId',$2::text),5
 WHERE EXISTS(SELECT 1 FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=$3 AND u.status='active' AND rp.permission_id='admin:data-rights')
 RETURNING id`, kind, requestID, actorID).Scan(&retryID); errors.Is(err, pgx.ErrNoRows) {
		return RequestJob{}, ErrCleanupForbidden
	} else if err != nil {
		return RequestJob{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO `+policy.table+`(original_job_id,retry_job_id,request_id,requested_by,expected_attempts,reason) VALUES($1,$2,$3,$4,$5,$6)`, jobID, retryID, requestID, actorID, *input.ExpectedAttempts, input.Reason); err != nil {
		return RequestJob{}, err
	}
	if err = appendEvent(ctx, tx, requestID, &actorID, policy.action, requestState, requestState, input.Reason, map[string]any{"originalJobId": jobID, "retryJobId": retryID, "kind": kind}); err != nil {
		return RequestJob{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
 VALUES($1,$9,'job',$2,$3,$4,jsonb_build_object('retryJobId',$5::text,'requestId',$6::text,'kind',$7::text,'expectedAttempts',$8::integer))`, actorID, jobID, input.Reason, safeRequestID(requestHeader), retryID, requestID, kind, *input.ExpectedAttempts, "data_rights."+policy.action); err != nil {
		return RequestJob{}, err
	}
	item, err := scanRequestJob(tx.QueryRow(ctx, policy.selectJobs()+` WHERE id=$1`, retryID))
	if err != nil {
		return RequestJob{}, err
	}
	return item, tx.Commit(ctx)
}
