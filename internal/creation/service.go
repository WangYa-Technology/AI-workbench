package creation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const JobKind = "generation.local"

var (
	ErrNotFound            = errors.New("generation not found")
	ErrInvalid             = errors.New("invalid generation request")
	ErrForbidden           = errors.New("generation access forbidden")
	ErrProviderOff         = errors.New("local provider is disabled")
	ErrConflict            = errors.New("generation state conflict")
	ErrIdempotency         = errors.New("invalid idempotency key")
	ErrIdempotencyConflict = errors.New("idempotency key reused for a different generation command")
	ErrNoCredits           = billing.ErrInsufficientFunds
)

type Generation struct {
	ID                  uuid.UUID         `json:"id"`
	Mode                string            `json:"mode"`
	Provider            string            `json:"provider"`
	ModelName           string            `json:"modelName"`
	Prompt              string            `json:"prompt"`
	Status              string            `json:"status"`
	Progress            int               `json:"progress"`
	EstimatedCostCents  int               `json:"estimatedCostCents"`
	ChargedCostCents    int               `json:"chargedCostCents"`
	OutputAssetID       *uuid.UUID        `json:"outputAssetId,omitempty"`
	OutputMediaURL      *string           `json:"outputMediaUrl,omitempty"`
	OutputScanStatus    *string           `json:"outputScanStatus,omitempty"`
	OutputText          *string           `json:"outputText,omitempty"`
	SourceWorkID        *uuid.UUID        `json:"sourceWorkId,omitempty"`
	SourceAssetID       *uuid.UUID        `json:"sourceAssetId,omitempty"`
	SourceTaskID        *uuid.UUID        `json:"sourceTaskId,omitempty"`
	RetryOfGenerationID *uuid.UUID        `json:"retryOfGenerationId,omitempty"`
	ErrorCode           *string           `json:"errorCode,omitempty"`
	ErrorMessage        *string           `json:"errorMessage,omitempty"`
	CancelledAt         *time.Time        `json:"cancelledAt,omitempty"`
	CancelReason        *string           `json:"cancelReason,omitempty"`
	CreatedAt           time.Time         `json:"createdAt"`
	UpdatedAt           time.Time         `json:"updatedAt"`
	Actions             GenerationActions `json:"actions"`
}

type GenerationActions struct {
	CanView       bool    `json:"canView"`
	CanCancel     bool    `json:"canCancel"`
	CanRetry      bool    `json:"canRetry"`
	CanDownload   bool    `json:"canDownload"`
	CanReuse      bool    `json:"canReuse"`
	ViewPath      string  `json:"viewPath"`
	WorkspacePath string  `json:"workspacePath"`
	DownloadPath  *string `json:"downloadPath,omitempty"`
	ReusePath     *string `json:"reusePath,omitempty"`
}

type GenerationListInput struct {
	Mode     string
	Status   string
	DateFrom *time.Time
	DateTo   *time.Time
	Cursor   string
	Limit    int
}

type GenerationPage struct {
	Items      []Generation `json:"items"`
	NextCursor *string      `json:"nextCursor,omitempty"`
}

type generationCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type SubmitInput struct {
	Mode          string     `json:"mode"`
	Prompt        string     `json:"prompt"`
	SourceWorkID  *uuid.UUID `json:"sourceWorkId"`
	SourceAssetID *uuid.UUID `json:"sourceAssetId"`
	SourceTaskID  *uuid.UUID `json:"sourceTaskId"`
}

type jobPayload struct {
	GenerationID uuid.UUID `json:"generationId"`
}

type Service struct {
	pool           *pgxpool.Pool
	mediaRoot      string
	providerSource string
	providerOn     bool
}

func NewService(pool *pgxpool.Pool, mediaRoot, providerSource string, providerOn bool) *Service {
	return &Service{pool: pool, mediaRoot: mediaRoot, providerSource: providerSource, providerOn: providerOn}
}

func (s *Service) Submit(ctx context.Context, ownerID uuid.UUID, input SubmitInput) (Generation, error) {
	return s.SubmitCommand(ctx, ownerID, input, uuid.NewString(), "generation-submit")
}

func (s *Service) SubmitCommand(ctx context.Context, ownerID uuid.UUID, input SubmitInput, idempotencyKey, requestID string) (Generation, error) {
	input.Mode = strings.TrimSpace(strings.ToLower(input.Mode))
	input.Prompt = strings.TrimSpace(input.Prompt)
	if !oneOf(input.Mode, "chat", "image", "video", "music") || len(input.Prompt) < 3 || len(input.Prompt) > 2000 {
		return Generation{}, ErrInvalid
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 8 || len(idempotencyKey) > 128 {
		return Generation{}, ErrIdempotency
	}
	if !s.providerOn {
		return Generation{}, ErrProviderOff
	}
	requestHash := hashCommand(struct {
		Mode          string     `json:"mode"`
		Prompt        string     `json:"prompt"`
		SourceWorkID  *uuid.UUID `json:"sourceWorkId"`
		SourceAssetID *uuid.UUID `json:"sourceAssetId"`
		SourceTaskID  *uuid.UUID `json:"sourceTaskId"`
	}{input.Mode, input.Prompt, input.SourceWorkID, input.SourceAssetID, input.SourceTaskID})

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Generation{}, fmt.Errorf("begin generation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if replay, found, err := commandReplay(ctx, tx, ownerID, "submit", idempotencyKey, requestHash); err != nil {
		return Generation{}, err
	} else if found {
		return replay, nil
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Generations); err != nil {
		return Generation{}, err
	}

	var provider, modelName string
	var estimatedCost, routeVersion int
	var routeRevisionID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT p.provider,p.model_name,p.estimated_cost_cents,r.id,r.version
		FROM model_route_state s JOIN model_route_revisions r ON r.id=s.active_revision_id
		JOIN provider_profiles p ON p.id=r.provider_profile_id
		WHERE s.mode=$1 AND p.local_test=true AND p.admin_enabled=true`, input.Mode).Scan(&provider, &modelName, &estimatedCost, &routeRevisionID, &routeVersion); errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, ErrProviderOff
	} else if err != nil {
		return Generation{}, fmt.Errorf("load provider capability: %w", err)
	}

	if input.SourceWorkID != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM works WHERE id=$1 AND status='published')`, input.SourceWorkID).Scan(&exists); err != nil {
			return Generation{}, fmt.Errorf("check source work: %w", err)
		}
		if !exists {
			return Generation{}, ErrInvalid
		}
	}
	if input.SourceAssetID != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
			  SELECT 1 FROM assets a
			  WHERE a.id=$1 AND a.owner_id=$2 AND a.scan_status='clean'
			    AND (a.source_type<>'purchase' OR EXISTS(
			      SELECT 1 FROM entitlements e JOIN licenses l ON l.code=e.license_code
			      WHERE e.asset_id=a.id AND e.user_id=$2 AND e.status='active' AND l.allows_derivatives
			    ))
			)`,
			input.SourceAssetID, ownerID).Scan(&exists); err != nil {
			return Generation{}, fmt.Errorf("check source asset: %w", err)
		}
		if !exists {
			return Generation{}, ErrInvalid
		}
	}
	if input.SourceTaskID != nil {
		var allowed bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM demands WHERE id=$1 AND assignee_id=$2 AND status IN ('assigned','revision'))`,
			input.SourceTaskID, ownerID).Scan(&allowed); err != nil {
			return Generation{}, fmt.Errorf("check source task: %w", err)
		}
		if !allowed {
			return Generation{}, ErrInvalid
		}
	}

	id := uuid.New()
	if err := billing.ReserveTx(ctx, tx, ownerID, id, estimatedCost, "USD"); err != nil {
		return Generation{}, err
	}
	var generation Generation
	err = tx.QueryRow(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,source_work_id,source_asset_id,source_task_id,model_route_revision_id,model_route_version)
		VALUES($1,$2,$3,$8,$9,$4,'queued',0,$10,$5,$6,$7,$11,$12)
		RETURNING id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,charged_cost_cents,
		          output_asset_id,output_text,source_work_id,source_asset_id,source_task_id,retry_of_generation_id,error_code,error_message,cancelled_at,cancel_reason,created_at,updated_at`,
		id, ownerID, input.Mode, input.Prompt, input.SourceWorkID, input.SourceAssetID, input.SourceTaskID, provider, modelName, estimatedCost, routeRevisionID, routeVersion).Scan(
		&generation.ID, &generation.Mode, &generation.Provider, &generation.ModelName, &generation.Prompt,
		&generation.Status, &generation.Progress, &generation.EstimatedCostCents, &generation.ChargedCostCents,
		&generation.OutputAssetID, &generation.OutputText, &generation.SourceWorkID, &generation.SourceAssetID, &generation.SourceTaskID, &generation.RetryOfGenerationID,
		&generation.ErrorCode, &generation.ErrorMessage, &generation.CancelledAt, &generation.CancelReason,
		&generation.CreatedAt, &generation.UpdatedAt,
	)
	if err != nil {
		return Generation{}, fmt.Errorf("insert generation: %w", err)
	}
	payload, err := json.Marshal(jobPayload{GenerationID: generation.ID})
	if err != nil {
		return Generation{}, fmt.Errorf("marshal generation job: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,$2)`, JobKind, payload); err != nil {
		return Generation{}, fmt.Errorf("enqueue generation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO generation_commands(actor_id,operation,idempotency_key,generation_id,result_generation_id,request_hash)
		VALUES($1,'submit',$2,$3,$3,$4)`, ownerID, idempotencyKey, generation.ID, requestHash); err != nil {
		return Generation{}, fmt.Errorf("record generation submit command: %w", err)
	}
	if err := writeAudit(ctx, tx, ownerID, "generation.submitted", generation.ID, requestID, map[string]any{
		"mode": generation.Mode, "provider": generation.Provider, "estimatedCostCents": generation.EstimatedCostCents, "modelRouteRevisionId": routeRevisionID, "modelRouteVersion": routeVersion,
		"paymentMode": "local_test",
	}); err != nil {
		return Generation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Generation{}, fmt.Errorf("commit generation: %w", err)
	}
	return withGenerationActions(generation), nil
}

func (s *Service) Cancel(ctx context.Context, ownerID, generationID uuid.UUID, idempotencyKey, requestID, reason string) (Generation, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	reason = strings.TrimSpace(reason)
	if len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || len(reason) < 3 || len(reason) > 300 {
		return Generation{}, ErrInvalid
	}
	requestHash := hashCommand(map[string]any{"generationId": generationID, "reason": reason})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Generation{}, fmt.Errorf("begin generation cancellation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, found, err := commandReplay(ctx, tx, ownerID, "cancel", idempotencyKey, requestHash); err != nil {
		return Generation{}, err
	} else if found {
		return replay, nil
	}
	var actualOwner uuid.UUID
	var status string
	if err := tx.QueryRow(ctx, `SELECT owner_id,status FROM generations WHERE id=$1 FOR UPDATE`, generationID).Scan(&actualOwner, &status); errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, ErrNotFound
	} else if err != nil {
		return Generation{}, fmt.Errorf("lock generation for cancellation: %w", err)
	}
	if actualOwner != ownerID {
		return Generation{}, ErrForbidden
	}
	if status != "queued" && status != "running" && status != "cancelled" {
		return Generation{}, ErrConflict
	}
	if status != "cancelled" {
		if _, err := tx.Exec(ctx, `
			UPDATE generations SET status='cancelled',progress=0,cancelled_at=now(),cancel_reason=$2,error_code=NULL,error_message=NULL,updated_at=now()
			WHERE id=$1`, generationID, reason); err != nil {
			return Generation{}, fmt.Errorf("cancel generation: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE jobs SET status='cancelled',lease_owner=NULL,lease_expires_at=NULL,updated_at=now()
			WHERE kind=$2 AND payload->>'generationId'=$1 AND status IN ('queued','running')`, generationID.String(), JobKind); err != nil {
			return Generation{}, fmt.Errorf("cancel generation job: %w", err)
		}
		if err := billing.ReleaseGenerationTx(ctx, tx, generationID, "cancelled: "+reason); err != nil {
			return Generation{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO generation_commands(actor_id,operation,idempotency_key,generation_id,result_generation_id,request_hash) VALUES($1,'cancel',$2,$3,$3,$4)`, ownerID, idempotencyKey, generationID, requestHash); err != nil {
		return Generation{}, fmt.Errorf("record generation cancellation: %w", err)
	}
	if err := writeAudit(ctx, tx, ownerID, "generation.cancelled", generationID, requestID, map[string]any{"reason": reason, "charged": false}); err != nil {
		return Generation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Generation{}, fmt.Errorf("commit generation cancellation: %w", err)
	}
	return s.Get(ctx, ownerID, generationID)
}

func (s *Service) Retry(ctx context.Context, ownerID, generationID uuid.UUID, idempotencyKey, requestID string) (Generation, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 8 || len(idempotencyKey) > 128 {
		return Generation{}, ErrIdempotency
	}
	requestHash := hashCommand(map[string]any{"generationId": generationID})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Generation{}, fmt.Errorf("begin generation retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, found, err := commandReplay(ctx, tx, ownerID, "retry", idempotencyKey, requestHash); err != nil {
		return Generation{}, err
	} else if found {
		return replay, nil
	}
	var input SubmitInput
	var actualOwner uuid.UUID
	var status string
	if err := tx.QueryRow(ctx, `
		SELECT owner_id,mode,prompt,source_work_id,source_asset_id,source_task_id,status
		FROM generations WHERE id=$1 FOR UPDATE`, generationID).Scan(
		&actualOwner, &input.Mode, &input.Prompt, &input.SourceWorkID, &input.SourceAssetID, &input.SourceTaskID, &status); errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, ErrNotFound
	} else if err != nil {
		return Generation{}, fmt.Errorf("load generation retry source: %w", err)
	}
	if actualOwner != ownerID {
		return Generation{}, ErrForbidden
	}
	if status != "failed" && status != "cancelled" {
		return Generation{}, ErrConflict
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Generations); err != nil {
		return Generation{}, err
	}
	var provider, modelName string
	var estimatedCost, routeVersion int
	var routeRevisionID uuid.UUID
	if !s.providerOn {
		return Generation{}, ErrProviderOff
	}
	if err := tx.QueryRow(ctx, `
		SELECT p.provider,p.model_name,p.estimated_cost_cents,r.id,r.version
		FROM model_route_state s JOIN model_route_revisions r ON r.id=s.active_revision_id
		JOIN provider_profiles p ON p.id=r.provider_profile_id
		WHERE s.mode=$1 AND p.local_test=true AND p.admin_enabled=true`, input.Mode).Scan(&provider, &modelName, &estimatedCost, &routeRevisionID, &routeVersion); errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, ErrProviderOff
	} else if err != nil {
		return Generation{}, fmt.Errorf("load retry provider: %w", err)
	}
	newID := uuid.New()
	if err := billing.ReserveTx(ctx, tx, ownerID, newID, estimatedCost, "USD"); err != nil {
		return Generation{}, err
	}
	var generation Generation
	if err := tx.QueryRow(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,source_work_id,source_asset_id,source_task_id,retry_of_generation_id,model_route_revision_id,model_route_version)
		VALUES($1,$2,$3,$10,$11,$4,'queued',0,$5,$6,$7,$8,$9,$12,$13)
		RETURNING id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,charged_cost_cents,
		output_asset_id,output_text,source_work_id,source_asset_id,source_task_id,retry_of_generation_id,error_code,error_message,cancelled_at,cancel_reason,created_at,updated_at`,
		newID, ownerID, input.Mode, input.Prompt, estimatedCost, input.SourceWorkID, input.SourceAssetID, input.SourceTaskID, generationID, provider, modelName, routeRevisionID, routeVersion).Scan(
		&generation.ID, &generation.Mode, &generation.Provider, &generation.ModelName, &generation.Prompt, &generation.Status,
		&generation.Progress, &generation.EstimatedCostCents, &generation.ChargedCostCents, &generation.OutputAssetID, &generation.OutputText,
		&generation.SourceWorkID, &generation.SourceAssetID, &generation.SourceTaskID, &generation.RetryOfGenerationID,
		&generation.ErrorCode, &generation.ErrorMessage, &generation.CancelledAt, &generation.CancelReason, &generation.CreatedAt, &generation.UpdatedAt); err != nil {
		return Generation{}, fmt.Errorf("insert generation retry: %w", err)
	}
	payload, _ := json.Marshal(jobPayload{GenerationID: newID})
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,$2)`, JobKind, payload); err != nil {
		return Generation{}, fmt.Errorf("enqueue generation retry: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO generation_commands(actor_id,operation,idempotency_key,generation_id,result_generation_id,request_hash) VALUES($1,'retry',$2,$3,$4,$5)`, ownerID, idempotencyKey, generationID, newID, requestHash); err != nil {
		return Generation{}, fmt.Errorf("record generation retry: %w", err)
	}
	if err := writeAudit(ctx, tx, ownerID, "generation.retried", generationID, requestID, map[string]any{"resultGenerationId": newID, "estimatedCostCents": estimatedCost, "modelRouteRevisionId": routeRevisionID, "modelRouteVersion": routeVersion}); err != nil {
		return Generation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Generation{}, fmt.Errorf("commit generation retry: %w", err)
	}
	return withGenerationActions(generation), nil
}

func (s *Service) Get(ctx context.Context, ownerID, id uuid.UUID) (Generation, error) {
	generation, err := scanGeneration(s.pool.QueryRow(ctx, generationSelect+` WHERE g.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, ErrNotFound
	}
	if err != nil {
		return Generation{}, fmt.Errorf("get generation: %w", err)
	}
	var actualOwner uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT owner_id FROM generations WHERE id=$1`, id).Scan(&actualOwner); err != nil {
		return Generation{}, fmt.Errorf("get generation owner: %w", err)
	}
	if actualOwner != ownerID {
		return Generation{}, ErrForbidden
	}
	return withGenerationActions(generation), nil
}

func (s *Service) List(ctx context.Context, ownerID uuid.UUID, input GenerationListInput) (GenerationPage, error) {
	input.Mode = strings.TrimSpace(strings.ToLower(input.Mode))
	input.Status = strings.TrimSpace(strings.ToLower(input.Status))
	if (input.Mode != "" && !oneOf(input.Mode, "chat", "image", "video", "music")) ||
		(input.Status != "" && !oneOf(input.Status, "queued", "running", "succeeded", "failed", "cancelled")) ||
		(input.DateFrom != nil && input.DateTo != nil && input.DateFrom.After(*input.DateTo)) {
		return GenerationPage{}, ErrInvalid
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return GenerationPage{}, ErrInvalid
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeGenerationCursor(input.Cursor)
		if err != nil {
			return GenerationPage{}, ErrInvalid
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, generationSelect+`
		WHERE g.owner_id=$1
		  AND ($2='' OR g.mode=$2)
		  AND ($3='' OR g.status=$3)
		  AND ($4::timestamptz IS NULL OR g.created_at >= $4)
		  AND ($5::timestamptz IS NULL OR g.created_at <= $5)
		  AND ($6::timestamptz IS NULL OR (g.created_at,g.id) < ($6,$7::uuid))
		ORDER BY g.created_at DESC,g.id DESC LIMIT $8`, ownerID, input.Mode, input.Status, input.DateFrom, input.DateTo, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return GenerationPage{}, fmt.Errorf("list generations: %w", err)
	}
	defer rows.Close()
	items := make([]Generation, 0)
	for rows.Next() {
		item, err := scanGeneration(rows)
		if err != nil {
			return GenerationPage{}, fmt.Errorf("scan generation: %w", err)
		}
		items = append(items, withGenerationActions(item))
	}
	if err := rows.Err(); err != nil {
		return GenerationPage{}, err
	}
	page := GenerationPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeGenerationCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) HandleJob(ctx context.Context, job jobs.Job) error {
	var payload jobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.GenerationID == uuid.Nil {
		return fmt.Errorf("invalid generation payload")
	}
	err := s.process(ctx, payload.GenerationID)
	if err == nil {
		return nil
	}
	if job.Attempts < job.MaxAttempts {
		_, updateErr := s.pool.Exec(ctx, `
			UPDATE generations SET status='queued',progress=0,error_code='local_provider_retrying',error_message=$2,updated_at=now()
			WHERE id=$1 AND status <> 'succeeded' AND status <> 'cancelled'`, payload.GenerationID, err.Error())
		if updateErr != nil {
			return fmt.Errorf("%w; record generation retry: %v", err, updateErr)
		}
		return err
	}
	tx, beginErr := s.pool.Begin(ctx)
	if beginErr != nil {
		return fmt.Errorf("%w; begin final generation failure: %v", err, beginErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var ownerID uuid.UUID
	var status string
	if lockErr := tx.QueryRow(ctx, `SELECT owner_id,status FROM generations WHERE id=$1 FOR UPDATE`, payload.GenerationID).Scan(&ownerID, &status); lockErr != nil {
		return fmt.Errorf("%w; lock failed generation: %v", err, lockErr)
	}
	if status != "succeeded" && status != "cancelled" {
		if _, updateErr := tx.Exec(ctx, `
			UPDATE generations SET status='failed',progress=0,error_code='local_provider_failed',error_message=$2,updated_at=now()
			WHERE id=$1`, payload.GenerationID, err.Error()); updateErr != nil {
			return fmt.Errorf("%w; record generation failure: %v", err, updateErr)
		}
		if releaseErr := billing.ReleaseGenerationTx(ctx, tx, payload.GenerationID, "provider failed after all attempts"); releaseErr != nil {
			return fmt.Errorf("%w; release failed generation credits: %v", err, releaseErr)
		}
		if auditErr := writeAudit(ctx, tx, ownerID, "generation.failed", payload.GenerationID, "worker", map[string]any{"errorCode": "local_provider_failed", "charged": false}); auditErr != nil {
			return fmt.Errorf("%w; audit failed generation: %v", err, auditErr)
		}
		if notifyErr := notifications.CreateTx(ctx, tx, notifications.CreateInput{
			UserID: ownerID, Kind: "generation.failed", Title: "Generation failed",
			Body:       "The generation could not be completed. Reserved Local Test credits were released.",
			TargetPath: "/workspace/generations", ResourceType: "generation", ResourceID: &payload.GenerationID,
			SourceKey: "generation:" + payload.GenerationID.String() + ":failed",
		}); notifyErr != nil {
			return fmt.Errorf("%w; notify failed generation: %v", err, notifyErr)
		}
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return fmt.Errorf("%w; commit failed generation: %v", err, commitErr)
	}
	return err
}

func (s *Service) process(ctx context.Context, generationID uuid.UUID) error {
	if !s.providerOn {
		return ErrProviderOff
	}
	var ownerID uuid.UUID
	var prompt, status, mode string
	err := s.pool.QueryRow(ctx, `SELECT owner_id,prompt,status,mode FROM generations WHERE id=$1`, generationID).Scan(&ownerID, &prompt, &status, &mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load generation: %w", err)
	}
	if status == "succeeded" || status == "cancelled" || status == "failed" {
		return nil
	}
	if _, err := s.pool.Exec(ctx, `UPDATE generations SET status='running',progress=35,error_code=NULL,error_message=NULL,updated_at=now() WHERE id=$1 AND status='queued'`, generationID); err != nil {
		return fmt.Errorf("start generation: %w", err)
	}

	assetID := uuid.NewSHA1(generationID, []byte("hcai-local-output"))
	output, err := s.produceLocalOutput(mode, prompt, assetID)
	if err != nil {
		return err
	}
	outputPath := filepath.Join(s.mediaRoot, assetID.String()+output.Extension)
	keepOutput := false
	defer func() {
		if !keepOutput {
			_ = os.Remove(outputPath)
		}
	}()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin generation result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `SELECT status FROM generations WHERE id=$1 FOR UPDATE`, generationID).Scan(&status); err != nil {
		return fmt.Errorf("lock generation: %w", err)
	}
	if status == "cancelled" {
		return tx.Commit(ctx)
	}
	if err := billing.CaptureGenerationTx(ctx, tx, generationID, "Local Test "+mode+" generation"); err != nil {
		return fmt.Errorf("capture generation charge: %w", err)
	}
	title := strings.TrimSpace(strings.SplitN(prompt, "\n\n", 2)[0])
	titleRunes := []rune(title)
	if len(titleRunes) > 72 {
		title = string(titleRunes[:72])
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,source_id,license_code)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'clean','generation',$9,'creator-owned')
		ON CONFLICT (id) DO NOTHING`, assetID, ownerID, output.Kind, title, "/api/v1/assets/"+assetID.String()+"/content", output.MIMEType, output.Width, output.Height, generationID)
	if err != nil {
		return fmt.Errorf("insert generated asset: %w", err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE generations SET status='succeeded',progress=100,output_asset_id=$2,output_text=$3,charged_cost_cents=estimated_cost_cents,error_code=NULL,error_message=NULL,updated_at=now()
		WHERE id=$1 AND status <> 'cancelled'`, generationID, assetID, output.Text)
	if err != nil {
		return fmt.Errorf("complete generation: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("generation was cancelled")
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: ownerID, Kind: "generation.completed", Title: "Generation ready",
		Body:       "Your local " + mode + " generation finished and was saved to Assets.",
		TargetPath: "/workspace/assets/" + assetID.String(), ResourceType: "generation", ResourceID: &generationID,
		SourceKey: "generation:" + generationID.String() + ":completed",
	}); err != nil {
		return fmt.Errorf("notify generation completion: %w", err)
	}
	if err := writeAudit(ctx, tx, ownerID, "generation.succeeded", generationID, "worker", map[string]any{
		"assetId": assetID, "charged": true, "paymentMode": "local_test",
	}); err != nil {
		return err
	}
	if err := webhooks.EnqueueTx(ctx, tx, webhooks.EventInput{OwnerID: ownerID, EventType: "generation.completed", ResourceType: "generation", ResourceID: &generationID, SourceKey: "generation:" + generationID.String() + ":completed"}); err != nil {
		return fmt.Errorf("enqueue generation webhook: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit generation result: %w", err)
	}
	keepOutput = true
	return nil
}

const generationSelect = `
	SELECT g.id,g.mode,g.provider,g.model_name,g.prompt,g.status,g.progress,g.estimated_cost_cents,g.charged_cost_cents,
	       g.output_asset_id,a.media_url,a.scan_status,g.output_text,g.source_work_id,g.source_asset_id,g.source_task_id,g.retry_of_generation_id,
	       g.error_code,g.error_message,g.cancelled_at,g.cancel_reason,g.created_at,g.updated_at
	FROM generations g LEFT JOIN assets a ON a.id=g.output_asset_id`

type scanner interface {
	Scan(...any) error
}

func scanGeneration(row scanner) (Generation, error) {
	var item Generation
	err := row.Scan(&item.ID, &item.Mode, &item.Provider, &item.ModelName, &item.Prompt, &item.Status, &item.Progress,
		&item.EstimatedCostCents, &item.ChargedCostCents, &item.OutputAssetID, &item.OutputMediaURL, &item.OutputScanStatus, &item.OutputText,
		&item.SourceWorkID, &item.SourceAssetID, &item.SourceTaskID, &item.RetryOfGenerationID, &item.ErrorCode, &item.ErrorMessage,
		&item.CancelledAt, &item.CancelReason, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func withGenerationActions(item Generation) Generation {
	item.Actions = GenerationActions{
		CanView:       true,
		CanCancel:     item.Status == "queued" || item.Status == "running",
		CanRetry:      item.Status == "failed" || item.Status == "cancelled",
		ViewPath:      "/workspace/generations?generationId=" + item.ID.String(),
		WorkspacePath: "/create/" + item.Mode,
	}
	if item.Status == "succeeded" && item.OutputAssetID != nil && item.OutputScanStatus != nil && *item.OutputScanStatus == "clean" {
		item.Actions.ViewPath = "/workspace/assets/" + item.OutputAssetID.String()
		item.Actions.CanDownload = item.OutputMediaURL != nil
		item.Actions.CanReuse = true
		item.Actions.DownloadPath = item.OutputMediaURL
		reusePath := "/create/" + item.Mode + "?sourceAssetId=" + item.OutputAssetID.String()
		item.Actions.ReusePath = &reusePath
	}
	return item
}

func encodeGenerationCursor(item Generation) string {
	body, _ := json.Marshal(generationCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeGenerationCursor(value string) (generationCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return generationCursor{}, err
	}
	var cursor generationCursor
	if err := json.Unmarshal(body, &cursor); err != nil || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
		return generationCursor{}, ErrInvalid
	}
	return cursor, nil
}

func commandReplay(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, operation, idempotencyKey, expectedHash string) (Generation, bool, error) {
	var resultID uuid.UUID
	var storedHash string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(result_generation_id,generation_id),request_hash FROM generation_commands
		WHERE actor_id=$1 AND operation=$2 AND idempotency_key=$3`, actorID, operation, idempotencyKey).Scan(&resultID, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, false, nil
	}
	if err != nil {
		return Generation{}, false, fmt.Errorf("load generation command replay: %w", err)
	}
	if storedHash != expectedHash {
		return Generation{}, false, ErrIdempotencyConflict
	}
	item, err := scanGeneration(tx.QueryRow(ctx, generationSelect+` WHERE g.id=$1 AND g.owner_id=$2`, resultID, actorID))
	if err != nil {
		return Generation{}, false, fmt.Errorf("load generation command result: %w", err)
	}
	return withGenerationActions(item), true, nil
}

func hashCommand(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum)
}

func writeAudit(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, action string, generationID uuid.UUID, requestID string, metadata map[string]any) error {
	if strings.TrimSpace(requestID) == "" {
		requestID = "generation"
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode generation audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,$2,'generation',$3,$4,$5)`, actorID, action, generationID, requestID, body); err != nil {
		return fmt.Errorf("write generation audit: %w", err)
	}
	return nil
}

type localOutput struct {
	Kind      string
	MIMEType  string
	Extension string
	Width     *int
	Height    *int
	Text      *string
}

func (s *Service) produceLocalOutput(mode, prompt string, assetID uuid.UUID) (localOutput, error) {
	var output localOutput
	switch mode {
	case "image":
		output = localOutput{Kind: "image", MIMEType: "image/jpeg", Extension: ".jpg", Width: intPtr(2000), Height: intPtr(2500)}
		if err := copyAtomic(s.providerSource, filepath.Join(s.mediaRoot, assetID.String()+output.Extension)); err != nil {
			return localOutput{}, fmt.Errorf("create deterministic image: %w", err)
		}
	case "video":
		output = localOutput{Kind: "video", MIMEType: "video/mp4", Extension: ".mp4", Width: intPtr(960), Height: intPtr(540)}
		source := filepath.Join(filepath.Dir(s.providerSource), "local-video-test.mp4")
		if err := copyAtomic(source, filepath.Join(s.mediaRoot, assetID.String()+output.Extension)); err != nil {
			return localOutput{}, fmt.Errorf("create deterministic video: %w", err)
		}
	case "music":
		output = localOutput{Kind: "audio", MIMEType: "audio/wav", Extension: ".wav"}
		if err := writeAtomic(filepath.Join(s.mediaRoot, assetID.String()+output.Extension), deterministicWAV(prompt)); err != nil {
			return localOutput{}, fmt.Errorf("create deterministic audio: %w", err)
		}
	case "chat":
		response := "Deterministic Local Test response\n\nYour request was recorded as:\n" + prompt + "\n\nThis local response verifies durable chat submission, billing, result storage, and provenance. It is not a production model response."
		output = localOutput{Kind: "document", MIMEType: "text/plain; charset=utf-8", Extension: ".txt", Text: &response}
		if err := writeAtomic(filepath.Join(s.mediaRoot, assetID.String()+output.Extension), []byte(response)); err != nil {
			return localOutput{}, fmt.Errorf("create deterministic chat result: %w", err)
		}
	default:
		return localOutput{}, ErrInvalid
	}
	return output, nil
}

func deterministicWAV(prompt string) []byte {
	const sampleRate = 16000
	const durationSeconds = 2
	const bitsPerSample = 16
	sampleCount := sampleRate * durationSeconds
	dataSize := sampleCount * bitsPerSample / 8
	frequency := 220.0 + float64(sha256.Sum256([]byte(prompt))[0])
	buffer := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+dataSize))
	buffer.WriteString("WAVEfmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate*bitsPerSample/8))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(bitsPerSample/8))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(bitsPerSample))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(dataSize))
	for index := range sampleCount {
		envelope := math.Min(1, float64(index)/800) * math.Min(1, float64(sampleCount-index)/1200)
		sample := int16(math.Sin(2*math.Pi*frequency*float64(index)/sampleRate) * 0.18 * envelope * math.MaxInt16)
		_ = binary.Write(buffer, binary.LittleEndian, sample)
	}
	return buffer.Bytes()
}

func writeAtomic(targetPath string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(targetPath), ".generation-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, targetPath)
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func intPtr(value int) *int { return &value }

func copyAtomic(sourcePath, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	source, err := os.Open(filepath.Clean(sourcePath))
	if err != nil {
		return err
	}
	defer source.Close()
	temporary, err := os.CreateTemp(filepath.Dir(targetPath), ".generation-*.jpg")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := io.Copy(temporary, source); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, targetPath)
}
