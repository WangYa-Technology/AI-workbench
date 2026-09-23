package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const ProductRefundCheckJobKind = "payment.check_product_refunds"

var ErrRefundHistoryNotFound = errors.New("product refund history not found")

type RefundAttempt struct {
	OperationID            uuid.UUID `json:"operationId"`
	Provider               string    `json:"provider"`
	ProviderRefundID       *string   `json:"providerRefundId,omitempty"`
	AmountCents            int       `json:"amountCents"`
	Currency               string    `json:"currency"`
	Status                 string    `json:"status"`
	ReconciliationRequired bool      `json:"reconciliationRequired"`
	RequestedAt            time.Time `json:"requestedAt"`
}
type RefundCheck struct {
	Origin          string              `json:"origin"`
	ID              uuid.UUID           `json:"id"`
	Status          string              `json:"status"`
	UnresolvedCount int                 `json:"unresolvedCount"`
	ErrorCode       *string             `json:"errorCode,omitempty"`
	CreatedAt       time.Time           `json:"createdAt"`
	ObservedAt      *time.Time          `json:"observedAt,omitempty"`
	CompletedAt     *time.Time          `json:"completedAt,omitempty"`
	Observations    []RefundObservation `json:"observations"`
}
type RefundHistory struct {
	Items          []RefundAttempt `json:"items"`
	NextCursor     *string         `json:"nextCursor,omitempty"`
	LatestCheck    *RefundCheck    `json:"latestCheck,omitempty"`
	CanCheck       bool            `json:"canCheck"`
	PaymentVersion int             `json:"paymentVersion"`
}

func (s *Service) refundReader(provider string) (RefundReader, bool) {
	if s == nil || !s.config.Enabled || provider != "stripe" {
		return nil, false
	}
	runtime, err := s.runtimes.Runtime(provider)
	if err != nil {
		return nil, false
	}
	reader, ok := runtime.(RefundReader)
	return reader, ok
}
func (s *Service) RefundHistory(ctx context.Context, paymentID uuid.UUID, cursor string, limit int) (RefundHistory, error) {
	result := RefundHistory{Items: []RefundAttempt{}}
	if s == nil || s.pool == nil || paymentID == uuid.Nil {
		return result, ErrRefundHistoryNotFound
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 {
		return result, ErrInvalidRefund
	}
	var provider, status, providerPaymentID string
	var closedRecovery bool
	if err := s.pool.QueryRow(ctx, `SELECT provider,status,version,COALESCE(provider_payment_id,''),EXISTS(SELECT 1 FROM product_closed_checkout_recoveries WHERE payment_id=$1) FROM payment_intents WHERE id=$1 AND purpose='product'`, paymentID).Scan(&provider, &status, &result.PaymentVersion, &providerPaymentID, &closedRecovery); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrRefundHistoryNotFound
		}
		return result, err
	}
	_, result.CanCheck = s.refundReader(provider)
	result.CanCheck = result.CanCheck && providerPaymentID != "" && (oneOf(status, "paid", "refund_pending", "refund_failed", "refunded") || (closedRecovery && oneOf(status, "cancelled", "payment_failed")))
	var missingIdentity bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_payment_identity_gaps WHERE payment_id=$1)`, paymentID).Scan(&missingIdentity); err != nil {
		return result, err
	}
	result.CanCheck = result.CanCheck && !missingIdentity
	var cursorID *uuid.UUID
	var cursorTime *time.Time
	if cursor != "" {
		id, err := uuid.Parse(cursor)
		if err != nil || id == uuid.Nil {
			return result, ErrInvalidRefund
		}
		var at time.Time
		if err := s.pool.QueryRow(ctx, `SELECT requested_at FROM product_refund_attempts WHERE operation_id=$1 AND payment_id=$2`, id, paymentID).Scan(&at); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return result, ErrInvalidRefund
			}
			return result, err
		}
		cursorID = &id
		cursorTime = &at
	}
	rows, err := s.pool.Query(ctx, `SELECT operation_id,provider,provider_refund_id,amount_cents,currency,status,
   reconciliation_required OR EXISTS(SELECT 1 FROM product_refund_dispatch_review d WHERE d.operation_id=a.operation_id),requested_at
   FROM product_refund_attempts a WHERE payment_id=$1 AND ($2::timestamptz IS NULL OR (requested_at,operation_id)<($2,$3))
   ORDER BY requested_at DESC,operation_id DESC LIMIT $4`, paymentID, cursorTime, cursorID, limit+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item RefundAttempt
		if err := rows.Scan(&item.OperationID, &item.Provider, &item.ProviderRefundID, &item.AmountCents, &item.Currency, &item.Status, &item.ReconciliationRequired, &item.RequestedAt); err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	rows.Close()
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		value := result.Items[limit-1].OperationID.String()
		result.NextCursor = &value
	}
	var check RefundCheck
	var observations []byte
	err = s.pool.QueryRow(ctx, `SELECT c.origin,c.id,CASE WHEN j.status IN ('failed','cancelled') AND c.status IN ('requested','observed') THEN 'failed' ELSE c.status END,
   c.unresolved_count,COALESCE(c.error_code,j.last_error_code),c.created_at,c.observed_at,c.completed_at,COALESCE(c.observations,'[]'::jsonb)
   FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.payment_id=$1 ORDER BY c.created_at DESC,c.id DESC LIMIT 1`, paymentID).Scan(
		&check.Origin, &check.ID, &check.Status, &check.UnresolvedCount, &check.ErrorCode, &check.CreatedAt, &check.ObservedAt, &check.CompletedAt, &observations)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if err == nil {
		if err := json.Unmarshal(observations, &check.Observations); err != nil {
			return result, err
		}
		result.LatestCheck = &check
		if oneOf(check.Status, "requested", "observed") {
			result.CanCheck = false
		}
	}
	return result, nil
}

func (s *Service) RequestRefundCheck(ctx context.Context, actorID, paymentID uuid.UUID, expectedVersion int) (history RefundHistory, resultErr error) {
	defer func() {
		var conflict *pgconn.PgError
		if errors.As(resultErr, &conflict) && (conflict.Code == "40001" || conflict.Code == "40P01") {
			history, resultErr = RefundHistory{}, ErrRefundConflict
		}
	}()
	if actorID == uuid.Nil || paymentID == uuid.Nil || expectedVersion < 1 {
		return RefundHistory{}, ErrInvalidRefund
	}
	if s == nil || s.pool == nil || !s.config.Enabled {
		return RefundHistory{}, ErrDisabled
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return RefundHistory{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := paymentFinanceAuthority(ctx, tx, actorID, false); err != nil {
		return RefundHistory{}, err
	}
	var provider, status, providerPaymentID string
	var closedRecovery bool
	var version int
	if err := tx.QueryRow(ctx, `SELECT provider,status,version,COALESCE(provider_payment_id,''),EXISTS(SELECT 1 FROM product_closed_checkout_recoveries WHERE payment_id=$1) FROM payment_intents WHERE id=$1 AND purpose='product' FOR UPDATE`, paymentID).Scan(&provider, &status, &version, &providerPaymentID, &closedRecovery); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RefundHistory{}, ErrRefundHistoryNotFound
		}
		return RefundHistory{}, err
	}
	if providerPaymentID == "" || expectedVersion != version || !(oneOf(status, "paid", "refund_pending", "refund_failed", "refunded") || (closedRecovery && oneOf(status, "cancelled", "payment_failed"))) {
		return RefundHistory{}, ErrRefundConflict
	}
	var missingIdentity bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_payment_identity_gaps WHERE payment_id=$1)`, paymentID).Scan(&missingIdentity); err != nil {
		return RefundHistory{}, err
	}
	if missingIdentity {
		return RefundHistory{}, ErrRefundConflict
	}
	if _, ok := s.refundReader(provider); !ok {
		return RefundHistory{}, ErrProviderUnavailable
	}
	if err := paymentFinanceAuthority(ctx, tx, actorID, true); err != nil {
		return RefundHistory{}, err
	}
	// Failed/exhausted jobs retain their observations but cannot hold the active
	// request slot forever. A fresh check is a new audited read, not a refund.
	if _, err := tx.Exec(ctx, `UPDATE product_refund_checks c SET status='failed',error_code=COALESCE(j.last_error_code,'payment_request_failed'),completed_at=now()
   FROM jobs j WHERE c.payment_id=$1 AND c.job_id=j.id AND j.status IN ('failed','cancelled') AND c.status IN ('requested','observed')`, paymentID); err != nil {
		return RefundHistory{}, err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_checks WHERE payment_id=$1 AND status IN ('requested','observed'))`, paymentID).Scan(&active); err != nil {
		return RefundHistory{}, err
	}
	if active {
		return RefundHistory{}, ErrRefundConflict
	}
	id, err := insertProductRefundCheckTx(ctx, tx, actorID, paymentID)
	if err != nil {
		return RefundHistory{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET version=version+1,updated_at=now() WHERE id=$1`, paymentID); err != nil {
		return RefundHistory{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
   VALUES($1,'payment.refund_check_requested','payment',$2,$3,jsonb_build_object('checkId',$4::text))`, actorID, paymentID, "refund-check:"+id.String(), id); err != nil {
		return RefundHistory{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RefundHistory{}, err
	}
	return s.RefundHistory(ctx, paymentID, "", 20)
}

func insertProductRefundCheckTx(ctx context.Context, tx pgx.Tx, actorID, paymentID uuid.UUID) (uuid.UUID, error) {
	return insertProductRefundCheckWithOriginTx(ctx, tx, &actorID, paymentID, "operator")
}

func insertProductRefundCheckWithOriginTx(ctx context.Context, tx pgx.Tx, actorID *uuid.UUID, paymentID uuid.UUID, origin string) (uuid.UUID, error) {
	id := uuid.New()
	var jobID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('checkId',$2::text),5) RETURNING id`, ProductRefundCheckJobKind, id).Scan(&jobID); err != nil {
		return uuid.Nil, err
	}
	_, err := tx.Exec(ctx, `INSERT INTO product_refund_checks(id,payment_id,requested_by,job_id,status,origin,created_at) VALUES($1,$2,$3,$4,'requested',$5,clock_timestamp())`, id, paymentID, actorID, jobID, origin)
	return id, err
}

func (s *Service) HandleProductRefundCheckJob(ctx context.Context, job jobs.Job) (resultErr error) {
	var payload struct {
		CheckID uuid.UUID `json:"checkId"`
	}
	if json.Unmarshal(job.Payload, &payload) != nil || payload.CheckID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	var request RefundReadRequest
	var status, provider string
	execution := refundCheckExecution{jobID: job.ID}
	if err := s.pool.QueryRow(ctx, `SELECT c.status,pi.id,pi.resource_id,pi.provider_payment_id,pi.amount_cents,pi.currency,pi.live_mode,pi.provider,
 j.status,j.attempts,j.lease_token
   FROM product_refund_checks c JOIN payment_intents pi ON pi.id=c.payment_id JOIN jobs j ON j.id=c.job_id
 WHERE c.id=$1 AND c.job_id=$2 AND j.kind=$3`, payload.CheckID, job.ID, ProductRefundCheckJobKind).Scan(
		&status, &request.PaymentID, &request.ResourceID, &request.ProviderPaymentID, &request.AmountCents, &request.Currency, &request.LiveMode, &provider,
		&execution.status, &execution.attempts, &execution.leaseToken); err != nil {
		return err
	}
	// Terminal checks are immutable: an already completed delivery of this
	// job is a no-op, even though its lease has naturally been released.
	if oneOf(status, "completed", "failed") {
		return nil
	}
	if job.LeaseToken != uuid.Nil && (execution.status != "running" || execution.leaseToken == nil || *execution.leaseToken != job.LeaseToken || execution.attempts != job.Attempts) {
		return jobs.ErrLeaseLost
	}
	failureState := status
	defer func() {
		if resultErr != nil && failureState != "" && !jobs.ShouldRetry(resultErr) {
			code := SanitizeProviderError(resultErr).Error()
			failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			changed, err := s.failRefundCheckExecution(failureCtx, execution, payload.CheckID, failureState, code)
			if err != nil {
				// A failed evidence write is not a completed terminal transition.
				resultErr = err
			} else if !changed {
				resultErr = jobs.ErrLeaseLost
			}
		}
	}()
	if status == "requested" {
		reader, ok := s.refundReader(provider)
		if !ok {
			return ErrProviderUnavailable
		}
		runtime, err := s.runtimes.Runtime(provider)
		if err != nil {
			return err
		}
		if _, _, err := verifyProductPaymentIdentity(ctx, s.pool, runtime, request.PaymentID); err != nil {
			return err
		}
		deadline, err := s.beginRefundRead(ctx, payload.CheckID, request.PaymentID, &execution)
		if err != nil {
			return err
		}
		readCtx, cancel := context.WithDeadline(ctx, deadline)
		var observations []RefundObservation
		if err = readCtx.Err(); err == nil {
			observations, err = reader.ReadProductRefunds(readCtx, request)
		}
		cancel()
		if err != nil {
			if len(observations) > 0 {
				// A cancelled read may still have verified earlier pages. Give the
				// evidence commit its own bounded lifetime, without another remote call.
				persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				stored, saveErr := s.saveRefundReadResult(persistCtx, payload.CheckID, request, observations, err, &execution)
				persistCancel()
				if saveErr != nil {
					return saveErr
				}
				if !stored {
					// Another execution already saved a result. Re-read that state on
					// retry instead of failing or applying a different observation set.
					return newProviderFailure("payment_request_failed", 0)
				}
				// This attempt is immutable and incomplete. A fresh audited query
				// can reconcile it; retrying this check must not overwrite evidence.
				failureState = "" // The failed observation was already committed atomically.
				return providerFailure{code: SanitizeProviderError(err).Error()}
			}
			persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			saveErr := s.recordFailedRefundRead(persistCtx, payload.CheckID, request.PaymentID, &execution, err)
			persistCancel()
			if saveErr != nil {
				return saveErr
			}
			return SanitizeProviderError(err)
		}
		persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, saveErr := s.saveRefundReadResult(persistCtx, payload.CheckID, request, observations, nil, &execution)
		persistCancel()
		if saveErr != nil {
			return saveErr
		}
	}
	failureState = "observed"
	rows, err := s.pool.Query(ctx, `SELECT id FROM payment_provider_events WHERE refund_check_id=$1 ORDER BY id`, payload.CheckID)
	if err != nil {
		return err
	}
	var events []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		events = append(events, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range events {
		if err := s.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, id.String()))}); err != nil {
			return err
		}
	}
	return s.finishRefundCheck(ctx, payload.CheckID, request.PaymentID)
}

func (s *Service) saveRefundObservations(ctx context.Context, checkID uuid.UUID, request RefundReadRequest, observations []RefundObservation) error {
	_, err := s.saveRefundReadResult(ctx, checkID, request, observations, nil, nil)
	return err
}

func (s *Service) saveRefundReadResult(ctx context.Context, checkID uuid.UUID, request RefundReadRequest, observations []RefundObservation, readErr error, execution *refundCheckExecution) (bool, error) {
	if observations == nil {
		observations = []RefundObservation{}
	}
	if len(observations) > 1000 {
		return false, newProviderFailure("payment_response_invalid", 0)
	}
	seen := map[string]bool{}
	for _, observation := range observations {
		if !validStripeID(observation.ProviderID, "re_") || seen[observation.ProviderID] || observation.ProviderPaymentID != request.ProviderPaymentID || observation.Currency != request.Currency || observation.AmountCents < 1 || observation.AmountCents > request.AmountCents || observation.OperationID != nil && *observation.OperationID == uuid.Nil || !oneOf(observation.Status, "pending", "requires_action", "succeeded", "failed", "canceled") {
			return false, newProviderFailure("payment_response_invalid", 0)
		}
		seen[observation.ProviderID] = true
	}
	return retryPaymentEvidenceWrite(ctx, func(writeCtx context.Context) (bool, error) {
		return s.saveRefundReadResultOnce(writeCtx, checkID, request, observations, readErr, execution)
	})
}

func (s *Service) saveRefundReadResultOnce(ctx context.Context, checkID uuid.UUID, request RefundReadRequest, observations []RefundObservation, readErr error, execution *refundCheckExecution) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var paymentID uuid.UUID
	// Match callback lock ordering: payment/order before operation evidence.
	if err := tx.QueryRow(ctx, `SELECT pi.id FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1 FOR UPDATE OF pi,o`, request.PaymentID).Scan(&paymentID); err != nil {
		return false, err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM product_refund_checks WHERE id=$1 AND payment_id=$2 FOR UPDATE`, checkID, paymentID).Scan(&status); err != nil {
		return false, err
	}
	if status != "requested" {
		if len(observations) > 0 {
			if err := saveLateRefundReceiptTx(ctx, tx, checkID, paymentID, observations, readErr, execution); err != nil {
				return false, err
			}
		}
		if err := recordRefundReadTx(ctx, tx, checkID, paymentID, execution, readErr); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	encoded, err := json.Marshal(observations)
	if err != nil {
		return false, err
	}
	if readErr != nil {
		code := SanitizeProviderError(readErr).Error()
		if _, err := tx.Exec(ctx, `UPDATE product_refund_checks SET observations=$2,observed_at=now(),status='failed',
 unresolved_count=$3,error_code=$4,completed_at=now() WHERE id=$1`, checkID, encoded, len(observations), code); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_intents SET version=version+1,updated_at=now() WHERE id=$1`, paymentID); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 VALUES('payment.refund_check_incomplete','payment',$1,$2,jsonb_build_object('checkId',$3::uuid,'observationCount',$4::integer,'errorCode',$5::text))`, paymentID, "refund-check:"+checkID.String(), checkID, len(observations), code); err != nil {
			return false, err
		}
		// No financial event is synthesized from an incomplete read. Shared
		// observation review and retention consult this immutable failed check.
		if err := recordRefundReadTx(ctx, tx, checkID, paymentID, execution, readErr); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if err := RecordProductRefundAttemptTx(ctx, tx, paymentID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE product_refund_checks SET observations=$2,observed_at=now(),status='observed' WHERE id=$1`, checkID, encoded); err != nil {
		return false, err
	}
	for _, observation := range observations {
		var operation uuid.UUID
		// Exact bound IDs identify legacy refunds; missing IDs require metadata and
		// explicit correlation support. Unknown/manual/partial refunds remain review evidence.
		err := tx.QueryRow(ctx, `SELECT operation_id FROM product_refund_attempts WHERE payment_id=$1 AND provider='stripe'
    AND provider_payment_id=$2 AND amount_cents=$3 AND currency=$4
    AND ($5::uuid IS NULL OR operation_id=$5)
    AND (provider_refund_id=$6 OR (provider_refund_id IS NULL AND operation_id=$5 AND correlation_enabled))`, paymentID, observation.ProviderPaymentID, observation.AmountCents, observation.Currency, observation.OperationID, observation.ProviderID).Scan(&operation)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		body, _ := json.Marshal(observation)
		digest := sha256.Sum256(body)
		var eventID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO payment_provider_events(provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,
    object_id,object_type,payment_id,resource_id,purpose,amount_cents,currency,payment_status,provider_payment_id,refund_operation_id,evidence_source,refund_check_id)
    VALUES('stripe',$1,'refund.observed',$2,$3,now(),$4,$5,'refund',$6,$7,'product',$8,$9,$10,$11,$12,'provider_query',$13) RETURNING id`,
			"query_"+strings.ReplaceAll(checkID.String(), "-", "")+"_"+observation.ProviderID, s.config.APIVersion, request.LiveMode, hex.EncodeToString(digest[:]), observation.ProviderID, paymentID, request.ResourceID, observation.AmountCents, observation.Currency, observation.Status, observation.ProviderPaymentID, operation, checkID).Scan(&eventID); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payment_provider_event_processing(event_id,status) VALUES($1,'received')`, eventID); err != nil {
			return false, err
		}
	}
	if err := recordRefundReadTx(ctx, tx, checkID, paymentID, execution, readErr); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Service) finishRefundCheck(ctx context.Context, checkID, paymentID uuid.UUID) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, paymentID).Scan(&id); err != nil {
		return err
	}
	var state string
	var encoded []byte
	if err := tx.QueryRow(ctx, `SELECT status,observations FROM product_refund_checks WHERE id=$1 FOR UPDATE`, checkID).Scan(&state, &encoded); err != nil {
		return err
	}
	if state != "observed" {
		return tx.Commit(ctx)
	}
	var observations []RefundObservation
	if err := json.Unmarshal(encoded, &observations); err != nil {
		return err
	}
	unresolved := 0
	matched := map[uuid.UUID]bool{}
	unresolvedRemote := map[string]bool{}
	for _, observation := range observations {
		var operation uuid.UUID
		var localStatus string
		err := tx.QueryRow(ctx, `SELECT operation_id,status FROM product_refund_attempts WHERE payment_id=$1 AND provider_refund_id=$2 AND amount_cents=$3
    AND ($4::uuid IS NULL OR operation_id=$4)`, paymentID, observation.ProviderID, observation.AmountCents, observation.OperationID).Scan(&operation, &localStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			unresolved++
			unresolvedRemote[observation.ProviderID] = true
			continue
		}
		if err != nil {
			return err
		}
		matched[operation] = true
		expected := "failed"
		if observation.Status == "succeeded" {
			expected = "succeeded"
		}
		pending := oneOf(observation.Status, "pending", "requires_action")
		needsReview := pending || localStatus != expected
		if needsReview {
			unresolved++
			unresolvedRemote[observation.ProviderID] = true
		}
		if _, err := tx.Exec(ctx, `UPDATE product_refund_attempts SET reconciliation_required=$2,updated_at=now() WHERE operation_id=$1`, operation, needsReview); err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, `SELECT operation_id FROM product_refund_attempts WHERE payment_id=$1`, paymentID)
	if err != nil {
		return err
	}
	var missing []uuid.UUID
	for rows.Next() {
		var operation uuid.UUID
		if err := rows.Scan(&operation); err != nil {
			rows.Close()
			return err
		}
		if !matched[operation] {
			missing = append(missing, operation)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	unresolved += len(missing)
	if len(missing) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE product_refund_attempts SET reconciliation_required=true WHERE operation_id=ANY($1::uuid[])`, missing); err != nil {
			return err
		}
	}
	var successes int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM product_refund_attempts WHERE payment_id=$1 AND status='succeeded'`, paymentID).Scan(&successes); err != nil {
		return err
	}
	if successes > 1 {
		unresolved += successes
		if _, err := tx.Exec(ctx, `UPDATE product_refund_attempts SET reconciliation_required=true WHERE payment_id=$1 AND status='succeeded'`, paymentID); err != nil {
			return err
		}
	}
	// Preserve unmatched authenticated refunds from older reads even when a
	// newer list omits them. Failed/empty reads are not reversal evidence.
	gaps, err := tx.Query(ctx, `SELECT DISTINCT provider_refund_id FROM product_refund_observation_gaps WHERE payment_id=$1`, paymentID)
	if err != nil {
		return err
	}
	for gaps.Next() {
		var remote *string
		if err := gaps.Scan(&remote); err != nil {
			gaps.Close()
			return err
		}
		if remote == nil || !unresolvedRemote[*remote] {
			unresolved++
		}
	}
	err = gaps.Err()
	gaps.Close()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE product_refund_checks SET status='completed',unresolved_count=$2,completed_at=now() WHERE id=$1`, checkID, unresolved); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET version=version+1,updated_at=now() WHERE id=$1`, paymentID); err != nil {
		return err
	}
	if err := confirmClosedCheckoutRefundTx(ctx, tx, paymentID, checkID); err != nil {
		return err
	}
	if err := enqueueCheckedClosedCheckoutTx(ctx, tx, paymentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
