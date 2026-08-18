package reconciliation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const JobKind = "provider.cost.reconcile"

var (
	ErrUnavailable = errors.New("provider cost reconciliation is unavailable")
	ErrInvalid     = errors.New("invalid provider cost reconciliation request")
	ErrConflict    = errors.New("provider cost reconciliation is already in progress")
	ErrNotFound    = errors.New("provider cost reconciliation not found")
)

// CostReader is the deliberately narrow Provider boundary used by the durable
// reconciliation job. It returns organization-period facts, never a per-run
// allocation.
type CostReader interface {
	Fetch(context.Context, time.Time, time.Time) (CostSummary, error)
}

type Service struct {
	pool             *pgxpool.Pool
	runtime          CostReader
	overageThreshold int64
}

type RequestInput struct {
	Provider    string    `json:"provider"`
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
	Reason      string    `json:"reason"`
	Confirmed   bool      `json:"confirmed"`
}

type ListInput struct {
	Status string
	Cursor string
	Limit  int
}

type Reconciliation struct {
	ID                       uuid.UUID  `json:"id"`
	Provider                 string     `json:"provider"`
	PeriodStart              time.Time  `json:"periodStart"`
	PeriodEnd                time.Time  `json:"periodEnd"`
	Currency                 *string    `json:"currency,omitempty"`
	ProviderCostMicros       *int64     `json:"providerCostMicros,omitempty"`
	LocalEstimatedCostMicros *int64     `json:"localEstimatedCostMicros,omitempty"`
	ReportedInputTokens      *int64     `json:"reportedInputTokens,omitempty"`
	ReportedTotalTokens      *int64     `json:"reportedTotalTokens,omitempty"`
	VarianceMicros           *int64     `json:"varianceMicros,omitempty"`
	OverageThresholdMicros   int64      `json:"overageThresholdMicros"`
	Status                   string     `json:"status"`
	RequestReason            string     `json:"requestReason"`
	RequestedBy              uuid.UUID  `json:"requestedBy"`
	JobID                    *uuid.UUID `json:"jobId,omitempty"`
	ErrorCode                *string    `json:"errorCode,omitempty"`
	StartedAt                *time.Time `json:"startedAt,omitempty"`
	CompletedAt              *time.Time `json:"completedAt,omitempty"`
	Version                  int        `json:"version"`
	CreatedAt                time.Time  `json:"createdAt"`
	UpdatedAt                time.Time  `json:"updatedAt"`
}

type Page struct {
	Items      []Reconciliation `json:"items"`
	NextCursor *string          `json:"nextCursor,omitempty"`
}

type reconciliationJobPayload struct {
	ReconciliationID uuid.UUID `json:"reconciliationId"`
}

type reconciliationCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

func NewService(pool *pgxpool.Pool, runtime CostReader, overageThresholdMicros int64) *Service {
	if runtime != nil {
		value := reflect.ValueOf(runtime)
		if value.Kind() == reflect.Ptr && value.IsNil() {
			runtime = nil
		}
	}
	return &Service{pool: pool, runtime: runtime, overageThreshold: overageThresholdMicros}
}

func (s *Service) Available() bool {
	return s != nil && s.pool != nil && s.runtime != nil
}

func (s *Service) Request(ctx context.Context, actorID uuid.UUID, input RequestInput, requestID string) (Reconciliation, error) {
	if !s.Available() {
		return Reconciliation{}, ErrUnavailable
	}
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	input.Reason = strings.TrimSpace(input.Reason)
	input.PeriodStart = input.PeriodStart.UTC()
	input.PeriodEnd = input.PeriodEnd.UTC()
	if actorID == uuid.Nil || s.overageThreshold < 0 || s.overageThreshold > 1_000_000_000_000 || input.Provider != "openai" || !input.Confirmed || len(input.Reason) < 12 || len(input.Reason) > 1000 ||
		!isUTCDay(input.PeriodStart) || !isUTCDay(input.PeriodEnd) || !input.PeriodEnd.After(input.PeriodStart) || input.PeriodEnd.Sub(input.PeriodStart) > maxCostDays*24*time.Hour {
		return Reconciliation{}, ErrInvalid
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Reconciliation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	id := uuid.New()
	var jobID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('reconciliationId',$2::text),5) RETURNING id`, JobKind, id).Scan(&jobID); err != nil {
		return Reconciliation{}, fmt.Errorf("enqueue provider cost reconciliation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO provider_cost_reconciliations(id,provider,period_start,period_end,overage_threshold_micros,status,request_reason,requested_by,job_id)
		VALUES($1,$2,$3,$4,$5,'queued',$6,$7,$8)`, id, input.Provider, input.PeriodStart, input.PeriodEnd, s.overageThreshold, input.Reason, actorID, jobID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Reconciliation{}, ErrConflict
		}
		return Reconciliation{}, fmt.Errorf("create provider cost reconciliation: %w", err)
	}
	if err := writeAudit(ctx, tx, actorID, "admin.provider_cost_reconciliation_requested", id, input.Reason, requestID, map[string]any{
		"provider": input.Provider, "periodStart": input.PeriodStart.Format(time.RFC3339), "periodEnd": input.PeriodEnd.Format(time.RFC3339),
		"thresholdMicros": s.overageThreshold, "jobId": jobID.String(),
	}); err != nil {
		return Reconciliation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Reconciliation{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Reconciliation, error) {
	if s == nil || s.pool == nil || id == uuid.Nil {
		return Reconciliation{}, ErrNotFound
	}
	item, err := scanReconciliation(s.pool.QueryRow(ctx, reconciliationSelect+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Reconciliation{}, ErrNotFound
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("get provider cost reconciliation: %w", err)
	}
	return item, nil
}

func (s *Service) List(ctx context.Context, input ListInput) (Page, error) {
	if !s.Available() {
		return Page{}, ErrUnavailable
	}
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if input.Status != "" && !oneOf(input.Status, "queued", "running", "matched", "overage", "failed") {
		return Page{}, ErrInvalid
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return Page{}, ErrInvalid
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeCursor(input.Cursor)
		if err != nil {
			return Page{}, ErrInvalid
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, reconciliationSelect+`
		WHERE ($1='' OR status=$1)
		  AND ($2::timestamptz IS NULL OR (created_at,id)<($2,$3::uuid))
		ORDER BY created_at DESC,id DESC LIMIT $4`, input.Status, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("list provider cost reconciliations: %w", err)
	}
	defer rows.Close()
	items := make([]Reconciliation, 0, input.Limit)
	for rows.Next() {
		item, err := scanReconciliation(rows)
		if err != nil {
			return Page{}, fmt.Errorf("scan provider cost reconciliation: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	page := Page{Items: items}
	if len(page.Items) > input.Limit {
		last := page.Items[input.Limit-1]
		page.Items = page.Items[:input.Limit]
		encoded := encodeCursor(reconciliationCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = &encoded
	}
	return page, nil
}

func (s *Service) HandleJob(ctx context.Context, job jobs.Job) error {
	var payload reconciliationJobPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.ReconciliationID == uuid.Nil {
		return permanentError("provider_response_invalid")
	}
	item, err := s.begin(ctx, payload.ReconciliationID)
	if err != nil {
		return err
	}
	if item.Status == "matched" || item.Status == "overage" || item.Status == "failed" {
		return nil
	}
	if s.runtime == nil {
		_ = s.fail(ctx, item.ID, "provider_unavailable")
		return permanentError("provider_unavailable")
	}
	summary, err := s.runtime.Fetch(ctx, item.PeriodStart, item.PeriodEnd)
	if err != nil {
		if jobs.ShouldRetry(err) && job.Attempts < job.MaxAttempts {
			return err
		}
		code := errorCode(err)
		_ = s.fail(ctx, item.ID, code)
		return permanentError(code)
	}
	if summary.Provider != item.Provider || summary.Currency != "USD" || !summary.PeriodStart.Equal(item.PeriodStart) || !summary.PeriodEnd.Equal(item.PeriodEnd) {
		_ = s.fail(ctx, item.ID, "provider_response_invalid")
		return permanentError("provider_response_invalid")
	}
	localCost, inputTokens, totalTokens, err := s.localEvidence(ctx, item)
	if err != nil {
		return err
	}
	if err := s.finish(ctx, item, summary, localCost, inputTokens, totalTokens); err != nil {
		return err
	}
	return nil
}

func (s *Service) begin(ctx context.Context, id uuid.UUID) (Reconciliation, error) {
	if s == nil || s.pool == nil {
		return Reconciliation{}, ErrUnavailable
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE provider_cost_reconciliations SET status='running',started_at=COALESCE(started_at,now()),updated_at=now(),version=version+1
		WHERE id=$1 AND status='queued'`, id); err != nil {
		return Reconciliation{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) localEvidence(ctx context.Context, item Reconciliation) (int64, int64, int64, error) {
	var estimatedCents, inputTokens, totalTokens int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(g.estimated_cost_cents),0),COALESCE(SUM(u.input_tokens),0),COALESCE(SUM(u.total_tokens),0)
		FROM generations g
		LEFT JOIN generation_provider_usage u ON u.generation_id=g.id
		WHERE g.provider=$1 AND g.status='succeeded' AND g.updated_at >= $2 AND g.updated_at < $3`,
		item.Provider, item.PeriodStart, item.PeriodEnd).Scan(&estimatedCents, &inputTokens, &totalTokens)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("aggregate local provider evidence: %w", err)
	}
	if estimatedCents > 922337203685477 || inputTokens < 0 || totalTokens < 0 {
		return 0, 0, 0, permanentError("provider_response_invalid")
	}
	return estimatedCents * 10_000, inputTokens, totalTokens, nil
}

func (s *Service) finish(ctx context.Context, item Reconciliation, summary CostSummary, localCost, inputTokens, totalTokens int64) error {
	status := "matched"
	if summary.CostMicros > localCost+item.OverageThresholdMicros {
		status = "overage"
	}
	result, err := s.pool.Exec(ctx, `
		UPDATE provider_cost_reconciliations
		SET currency=$2,provider_cost_micros=$3,local_estimated_cost_micros=$4,reported_input_tokens=$5,reported_total_tokens=$6,
		    variance_micros=$3::bigint-$4::bigint,status=$7,completed_at=now(),updated_at=now(),version=version+1
		WHERE id=$1 AND status='running'`, item.ID, summary.Currency, summary.CostMicros, localCost, inputTokens, totalTokens, status)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Service) fail(ctx context.Context, id uuid.UUID, code string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE provider_cost_reconciliations SET status='failed',error_code=$2,completed_at=now(),updated_at=now(),version=version+1
		WHERE id=$1 AND status IN ('queued','running')`, id, code)
	return err
}

const reconciliationSelect = `
	SELECT id,provider,period_start,period_end,currency,provider_cost_micros,local_estimated_cost_micros,reported_input_tokens,reported_total_tokens,
	       variance_micros,overage_threshold_micros,status,request_reason,requested_by,job_id,error_code,started_at,completed_at,version,created_at,updated_at
	FROM provider_cost_reconciliations`

type rowScanner interface{ Scan(...any) error }

func scanReconciliation(row rowScanner) (Reconciliation, error) {
	var item Reconciliation
	err := row.Scan(&item.ID, &item.Provider, &item.PeriodStart, &item.PeriodEnd, &item.Currency, &item.ProviderCostMicros,
		&item.LocalEstimatedCostMicros, &item.ReportedInputTokens, &item.ReportedTotalTokens, &item.VarianceMicros,
		&item.OverageThresholdMicros, &item.Status, &item.RequestReason, &item.RequestedBy, &item.JobID, &item.ErrorCode,
		&item.StartedAt, &item.CompletedAt, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func writeAudit(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, action string, reconciliationID uuid.UUID, reason, requestID string, metadata map[string]any) error {
	if strings.TrimSpace(requestID) == "" {
		requestID = "admin"
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,$2,'provider_cost_reconciliation',$3,$4,$5,$6)`, actorID, action, reconciliationID, reason, requestID, body)
	return err
}

func encodeCursor(cursor reconciliationCursor) string {
	body, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeCursor(value string) (reconciliationCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(body) == 0 || len(body) > 512 {
		return reconciliationCursor{}, ErrInvalid
	}
	var cursor reconciliationCursor
	if json.Unmarshal(body, &cursor) != nil || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
		return reconciliationCursor{}, ErrInvalid
	}
	return cursor, nil
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

type permanentFailure string

func (e permanentFailure) Error() string     { return string(e) }
func (e permanentFailure) ErrorCode() string { return string(e) }
func (e permanentFailure) Retryable() bool   { return false }

func permanentError(code string) error { return permanentFailure(code) }

func errorCode(err error) string {
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		code := strings.TrimSpace(strings.ToLower(coded.ErrorCode()))
		if len(code) >= 3 && len(code) <= 80 {
			return code
		}
	}
	return "provider_request_failed"
}
