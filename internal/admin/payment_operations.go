package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/jackc/pgx/v5"
)

var stripeAccountPattern = regexp.MustCompile(`^acct_[A-Za-z0-9_]{1,250}$`)

type PaymentWorkerJob struct {
	ID            uuid.UUID `json:"id"`
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	Attempts      int       `json:"attempts"`
	MaxAttempts   int       `json:"maxAttempts"`
	LastErrorCode *string   `json:"lastErrorCode,omitempty"`
	AvailableAt   time.Time `json:"availableAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type PaymentProviderEvent struct {
	ID              uuid.UUID         `json:"id"`
	ProviderEventID string            `json:"providerEventId"`
	EventType       string            `json:"eventType"`
	ProcessingState string            `json:"processingState"`
	AttemptCount    int               `json:"attemptCount"`
	ReplayCount     int               `json:"replayCount"`
	Version         int               `json:"version"`
	ErrorCode       *string           `json:"errorCode,omitempty"`
	LastErrorCode   *string           `json:"lastErrorCode,omitempty"`
	OccurredAt      time.Time         `json:"occurredAt"`
	ReceivedAt      time.Time         `json:"receivedAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	Job             *PaymentWorkerJob `json:"job,omitempty"`
}

type PaymentDestinationSummary struct {
	ID             uuid.UUID  `json:"id"`
	DestinationID  string     `json:"destinationId"`
	Status         string     `json:"status"`
	ChargesEnabled bool       `json:"chargesEnabled"`
	PayoutsEnabled bool       `json:"payoutsEnabled"`
	Version        int        `json:"version"`
	VerifiedAt     *time.Time `json:"verifiedAt,omitempty"`
}

type PaymentDestination struct {
	PaymentDestinationSummary
	UserID      uuid.UUID `json:"userId"`
	Email       string    `json:"email"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"displayName"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type PaymentDestinationListInput struct {
	Query  string
	Status string
	Cursor string
	Limit  int
}

type PaymentDestinationPage struct {
	Items      []PaymentDestination `json:"items"`
	NextCursor *string              `json:"nextCursor,omitempty"`
}

type paymentDestinationCursor struct {
	UpdatedAt time.Time `json:"updatedAt"`
	ID        uuid.UUID `json:"id"`
}

type PaymentDestinationUpdate struct {
	DestinationID   string `json:"destinationId"`
	Enabled         bool   `json:"enabled"`
	ExpectedVersion int    `json:"expectedVersion"`
}

type PaymentOperation struct {
	ID                 uuid.UUID                  `json:"id"`
	Purpose            string                     `json:"purpose"`
	Status             string                     `json:"status"`
	AmountCents        int                        `json:"amountCents"`
	Currency           string                     `json:"currency"`
	LiveMode           bool                       `json:"liveMode"`
	PayerID            uuid.UUID                  `json:"payerId"`
	PayerEmail         string                     `json:"payerEmail"`
	PayerHandle        string                     `json:"payerHandle"`
	PayerDisplayName   string                     `json:"payerDisplayName"`
	PayeeID            *uuid.UUID                 `json:"payeeId,omitempty"`
	PayeeHandle        *string                    `json:"payeeHandle,omitempty"`
	PayeeDisplayName   *string                    `json:"payeeDisplayName,omitempty"`
	ResourceID         uuid.UUID                  `json:"resourceId"`
	ResourceTitle      string                     `json:"resourceTitle"`
	TargetPath         string                     `json:"targetPath"`
	OrderID            *uuid.UUID                 `json:"orderId,omitempty"`
	ProposalID         *uuid.UUID                 `json:"proposalId,omitempty"`
	ProviderCheckoutID *string                    `json:"providerCheckoutId,omitempty"`
	ProviderPaymentID  *string                    `json:"providerPaymentId,omitempty"`
	ProviderChargeID   *string                    `json:"providerChargeId,omitempty"`
	ProviderRefundID   *string                    `json:"providerRefundId,omitempty"`
	ProviderTransferID *string                    `json:"providerTransferId,omitempty"`
	AttentionCode      string                     `json:"attentionCode"`
	Version            int                        `json:"version"`
	PaidAt             *time.Time                 `json:"paidAt,omitempty"`
	TransferredAt      *time.Time                 `json:"transferredAt,omitempty"`
	RefundedAt         *time.Time                 `json:"refundedAt,omitempty"`
	CheckoutExpiresAt  *time.Time                 `json:"checkoutExpiresAt,omitempty"`
	CreatedAt          time.Time                  `json:"createdAt"`
	UpdatedAt          time.Time                  `json:"updatedAt"`
	Destination        *PaymentDestinationSummary `json:"destination,omitempty"`
	Job                *PaymentWorkerJob          `json:"job,omitempty"`
	ProviderEvent      *PaymentProviderEvent      `json:"providerEvent,omitempty"`
}

type PaymentOperationListInput struct {
	Query     string
	Purpose   string
	Status    string
	Mode      string
	Attention string
	Cursor    string
	Limit     int
}

type PaymentOperationPage struct {
	Items      []PaymentOperation `json:"items"`
	NextCursor *string            `json:"nextCursor,omitempty"`
}

type paymentOperationCursor struct {
	Priority  int       `json:"priority"`
	UpdatedAt time.Time `json:"updatedAt"`
	ID        uuid.UUID `json:"id"`
}

type PaymentRecovery struct {
	Action          string `json:"action"`
	ExpectedVersion int    `json:"expectedVersion"`
}

type PaymentEventReplay struct {
	ExpectedVersion int `json:"expectedVersion"`
}

const paymentOperationSelect = `
	WITH operations AS (
		SELECT pi.id AS payment_id,pi.purpose,pi.status AS payment_status,pi.amount_cents,pi.currency,pi.live_mode,
		       pi.payer_id,payer.email AS payer_email,payer.handle AS payer_handle,payer.display_name AS payer_display_name,
		       pi.payee_id,payee.handle AS payee_handle,payee.display_name AS payee_display_name,pi.resource_id,
		       COALESCE(d.title,o.product_title_snapshot,sp.name,CASE WHEN pi.purpose='wallet_topup' THEN 'Wallet top-up' END,'Unavailable resource') AS resource_title,
		       CASE WHEN pi.purpose='task' THEN '/market/demands?task='||pi.resource_id::text
		            WHEN pi.purpose IN ('wallet_topup','subscription') THEN '/workspace/billing'
		            ELSE '/workspace/orders' END AS target_path,
		       pi.order_id,pi.proposal_id,pi.provider_checkout_id,pi.provider_payment_id,pi.provider_charge_id,
		       pi.provider_refund_id,pi.provider_transfer_id,pi.version,pi.paid_at,pi.transferred_at,pi.refunded_at,
		       pi.checkout_expires_at,pi.created_at AS payment_created_at,pi.updated_at AS payment_updated_at,
		       destination.id AS destination_record_id,destination.destination_id,destination.status AS destination_status,destination.charges_enabled,destination.payouts_enabled,
		       destination.version AS destination_version,destination.verified_at AS destination_verified_at,
		       operation_job.id AS operation_job_id,operation_job.kind AS operation_job_kind,operation_job.status AS operation_job_status,operation_job.attempts AS operation_job_attempts,operation_job.max_attempts AS operation_job_max_attempts,
		       operation_job.last_error_code AS operation_job_error_code,operation_job.available_at AS operation_job_available_at,operation_job.updated_at AS operation_job_updated_at,
		       provider_event.id AS provider_event_record_id,provider_event.provider_event_id,provider_event.event_type,provider_event.processing_status,
		       provider_event.attempt_count,provider_event.replay_count,provider_event.processing_version,
		       provider_event.error_code,provider_event.last_error_code,provider_event.occurred_at,provider_event.received_at,
		       provider_event.processing_updated_at,
		       event_job.id AS event_job_id,event_job.kind AS event_job_kind,event_job.status AS event_job_status,event_job.attempts AS event_job_attempts,event_job.max_attempts AS event_job_max_attempts,event_job.last_error_code AS event_job_error_code,
		       event_job.available_at AS event_job_available_at,event_job.updated_at AS event_job_updated_at,
		       CASE
		         WHEN pi.status='refund_failed' THEN 'refund_failed'
		         WHEN provider_event.processing_status='failed' OR event_job.status='failed' THEN 'event_processing_failed'
		         WHEN pi.status='transfer_pending' AND (destination.id IS NULL OR destination.status<>'verified' OR NOT destination.charges_enabled OR NOT destination.payouts_enabled) THEN 'destination_missing'
		         WHEN pi.status='transfer_pending' AND operation_job.status='failed' THEN 'transfer_job_failed'
		         WHEN pi.status='transfer_pending' AND operation_job.id IS NULL THEN 'transfer_job_missing'
		         WHEN pi.status='refund_pending' AND pi.provider_refund_id IS NULL AND operation_job.status='failed' THEN 'refund_job_failed'
		         WHEN pi.status='refund_pending' AND pi.provider_refund_id IS NULL AND operation_job.id IS NULL THEN 'refund_job_missing'
		         WHEN pi.status='checkout_open' AND pi.checkout_expires_at<=now() THEN 'checkout_expired'
		         ELSE 'none'
		       END AS attention_code
		FROM payment_intents pi
		JOIN users payer ON payer.id=pi.payer_id
		LEFT JOIN users payee ON payee.id=pi.payee_id
		LEFT JOIN demands d ON pi.purpose='task' AND d.id=pi.resource_id
		LEFT JOIN orders o ON pi.purpose='product' AND o.id=pi.order_id
		LEFT JOIN subscription_plans sp ON pi.purpose='subscription' AND sp.id=pi.resource_id
		LEFT JOIN payment_destinations destination ON destination.provider='stripe' AND destination.user_id=pi.payee_id
		LEFT JOIN LATERAL (
			SELECT j.id,j.kind,j.status,j.attempts,j.max_attempts,j.last_error_code,j.available_at,j.updated_at
			FROM jobs j WHERE j.kind IN ('payment.transfer_task','payment.refund_task','payment.refund_product')
			  AND j.payload->>'paymentId'=pi.id::text ORDER BY j.updated_at DESC,j.id DESC LIMIT 1
		) operation_job ON true
		LEFT JOIN LATERAL (
			SELECT e.id,e.provider_event_id,e.event_type,p.status AS processing_status,p.attempt_count,p.replay_count,
			       p.version AS processing_version,p.error_code,p.last_error_code,e.occurred_at,e.received_at,p.updated_at AS processing_updated_at
			FROM payment_provider_events e JOIN payment_provider_event_processing p ON p.event_id=e.id
			WHERE e.payment_id=pi.id ORDER BY e.received_at DESC,e.id DESC LIMIT 1
		) provider_event ON true
		LEFT JOIN LATERAL (
			SELECT j.id,j.kind,j.status,j.attempts,j.max_attempts,j.last_error_code,j.available_at,j.updated_at
			FROM jobs j WHERE j.kind='payment.process_event' AND provider_event.id IS NOT NULL
			  AND j.payload->>'eventId'=provider_event.id::text ORDER BY j.updated_at DESC,j.id DESC LIMIT 1
		) event_job ON true
	)
	SELECT * FROM operations`

func (s *Service) ListPaymentOperations(ctx context.Context, input PaymentOperationListInput) (PaymentOperationPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.Purpose = strings.ToLower(strings.TrimSpace(input.Purpose))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	input.Attention = strings.ToLower(strings.TrimSpace(input.Attention))
	if len(input.Query) > 120 || (input.Purpose != "" && !oneOf(input.Purpose, "product", "task", "wallet_topup", "subscription")) ||
		(input.Status != "" && !oneOf(input.Status, "checkout_pending", "checkout_open", "paid", "payment_failed", "transfer_pending", "transferred", "refund_pending", "refund_failed", "refunded", "cancelled")) ||
		(input.Mode != "" && !oneOf(input.Mode, "test", "live")) ||
		(input.Attention != "" && !oneOf(input.Attention, "needs_attention", "healthy")) {
		return PaymentOperationPage{}, ErrInvalidPaymentFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return PaymentOperationPage{}, ErrInvalidPaymentFilter
	}
	var cursorPriority *int
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodePaymentOperationCursor(input.Cursor)
		if err != nil {
			return PaymentOperationPage{}, err
		}
		cursorPriority, cursorTime, cursorID = &cursor.Priority, &cursor.UpdatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, paymentOperationSelect+`
		WHERE ($1='' OR payment_id::text=$1 OR strpos(lower(resource_title),$1)>0 OR strpos(lower(payer_handle),$1)>0 OR strpos(lower(COALESCE(payee_handle,'')),$1)>0)
		  AND ($2='' OR purpose=$2) AND ($3='' OR payment_status=$3)
		  AND ($4='' OR ($4='live' AND live_mode) OR ($4='test' AND NOT live_mode))
		  AND ($5='' OR ($5='needs_attention' AND attention_code<>'none') OR ($5='healthy' AND attention_code='none'))
		  AND ($6::int IS NULL OR CASE WHEN attention_code<>'none' THEN 0 ELSE 1 END > $6 OR
		      (CASE WHEN attention_code<>'none' THEN 0 ELSE 1 END=$6 AND (payment_updated_at,payment_id)<($7,$8::uuid)))
		ORDER BY CASE WHEN attention_code<>'none' THEN 0 ELSE 1 END,payment_updated_at DESC,payment_id DESC LIMIT $9`,
		input.Query, input.Purpose, input.Status, input.Mode, input.Attention, cursorPriority, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return PaymentOperationPage{}, fmt.Errorf("list admin payment operations: %w", err)
	}
	defer rows.Close()
	items := make([]PaymentOperation, 0)
	for rows.Next() {
		item, err := scanPaymentOperation(rows)
		if err != nil {
			return PaymentOperationPage{}, fmt.Errorf("scan admin payment operation: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return PaymentOperationPage{}, err
	}
	page := PaymentOperationPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodePaymentOperationCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) ListPaymentDestinations(ctx context.Context, input PaymentDestinationListInput) (PaymentDestinationPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if len(input.Query) > 120 || (input.Status != "" && !oneOf(input.Status, "pending_onboarding", "pending_verification", "verified", "restricted", "disabled")) {
		return PaymentDestinationPage{}, ErrInvalidDestinationFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return PaymentDestinationPage{}, ErrInvalidDestinationFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		var cursor paymentDestinationCursor
		body, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.ID == uuid.Nil || cursor.UpdatedAt.IsZero() {
			return PaymentDestinationPage{}, ErrInvalidDestinationFilter
		}
		cursorTime, cursorID = &cursor.UpdatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT d.id,d.destination_id,d.status,d.charges_enabled,d.payouts_enabled,d.version,d.verified_at,
		       d.user_id,u.email,u.handle,u.display_name,d.created_at,d.updated_at
		FROM payment_destinations d JOIN users u ON u.id=d.user_id
		WHERE ($1='' OR strpos(lower(u.email),$1)>0 OR strpos(lower(u.handle),$1)>0 OR strpos(lower(u.display_name),$1)>0 OR lower(d.destination_id)=lower($1))
		  AND ($2='' OR d.status=$2) AND ($3::timestamptz IS NULL OR (d.updated_at,d.id)<($3,$4::uuid))
		ORDER BY d.updated_at DESC,d.id DESC LIMIT $5`, input.Query, input.Status, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return PaymentDestinationPage{}, fmt.Errorf("list payment destinations: %w", err)
	}
	defer rows.Close()
	items := make([]PaymentDestination, 0)
	for rows.Next() {
		var item PaymentDestination
		if err := rows.Scan(&item.ID, &item.DestinationID, &item.Status, &item.ChargesEnabled, &item.PayoutsEnabled, &item.Version, &item.VerifiedAt,
			&item.UserID, &item.Email, &item.Handle, &item.DisplayName, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return PaymentDestinationPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return PaymentDestinationPage{}, err
	}
	page := PaymentDestinationPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		body, _ := json.Marshal(paymentDestinationCursor{UpdatedAt: last.UpdatedAt, ID: last.ID})
		cursor := base64.RawURLEncoding.EncodeToString(body)
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) UpdatePaymentDestination(ctx context.Context, _ uuid.UUID, userID uuid.UUID, input PaymentDestinationUpdate, _ string) (PaymentDestination, error) {
	input.DestinationID = strings.TrimSpace(input.DestinationID)
	if input.ExpectedVersion < 0 || !stripeAccountPattern.MatchString(input.DestinationID) {
		return PaymentDestination{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PaymentDestination{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active')`, userID).Scan(&userExists); err != nil {
		return PaymentDestination{}, err
	}
	if !userExists {
		return PaymentDestination{}, ErrNotFound
	}
	var destinationID uuid.UUID
	var version int
	err = tx.QueryRow(ctx, `SELECT id,version FROM payment_destinations WHERE provider='stripe' AND user_id=$1 FOR UPDATE`, userID).Scan(&destinationID, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		if input.ExpectedVersion != 0 {
			return PaymentDestination{}, ErrConflict
		}
		destinationID = uuid.New()
		status := "disabled"
		var verifiedAt *time.Time
		if input.Enabled {
			status = "verified"
			now := time.Now().UTC()
			verifiedAt = &now
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO payment_destinations(id,provider,user_id,destination_id,account_type,status,charges_enabled,payouts_enabled,details_submitted,requirements_due,admin_disabled,verified_at)
			VALUES($1,'stripe',$2,$3,'manual',$4,$5,$5,$5,false,NOT $5,$6)`, destinationID, userID, input.DestinationID, status, input.Enabled, verifiedAt); err != nil {
			return PaymentDestination{}, ErrConflict
		}
	} else if err != nil {
		return PaymentDestination{}, err
	} else {
		if input.ExpectedVersion != version {
			return PaymentDestination{}, ErrConflict
		}
		status := "disabled"
		var verifiedAt *time.Time
		if input.Enabled {
			status = "verified"
			now := time.Now().UTC()
			verifiedAt = &now
		}
		if _, err := tx.Exec(ctx, `
			UPDATE payment_destinations SET destination_id=$2,account_type='manual',status=$3,charges_enabled=$4,payouts_enabled=$4,
			  details_submitted=$4,requirements_due=false,admin_disabled=NOT $4,verified_at=$5,version=version+1,updated_at=now() WHERE id=$1`, destinationID, input.DestinationID, status, input.Enabled, verifiedAt); err != nil {
			return PaymentDestination{}, ErrConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PaymentDestination{}, err
	}
	return s.paymentDestination(ctx, userID)
}

func (s *Service) paymentDestination(ctx context.Context, userID uuid.UUID) (PaymentDestination, error) {
	var item PaymentDestination
	err := s.pool.QueryRow(ctx, `
		SELECT d.id,d.destination_id,d.status,d.charges_enabled,d.payouts_enabled,d.version,d.verified_at,
		       d.user_id,u.email,u.handle,u.display_name,d.created_at,d.updated_at
		FROM payment_destinations d JOIN users u ON u.id=d.user_id WHERE d.provider='stripe' AND d.user_id=$1`, userID).Scan(
		&item.ID, &item.DestinationID, &item.Status, &item.ChargesEnabled, &item.PayoutsEnabled, &item.Version, &item.VerifiedAt,
		&item.UserID, &item.Email, &item.Handle, &item.DisplayName, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PaymentDestination{}, ErrNotFound
	}
	return item, err
}

func encodePaymentOperationCursor(item PaymentOperation) string {
	priority := 1
	if item.AttentionCode != "none" {
		priority = 0
	}
	body, _ := json.Marshal(paymentOperationCursor{Priority: priority, UpdatedAt: item.UpdatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodePaymentOperationCursor(value string) (paymentOperationCursor, error) {
	var cursor paymentOperationCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.ID == uuid.Nil || cursor.UpdatedAt.IsZero() || cursor.Priority < 0 || cursor.Priority > 1 {
		return paymentOperationCursor{}, ErrInvalidPaymentFilter
	}
	return cursor, nil
}

func (s *Service) RecoverPayment(ctx context.Context, actorID, paymentID uuid.UUID, input PaymentRecovery, _ string) (PaymentOperation, error) {
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	if input.ExpectedVersion < 1 || !oneOf(input.Action, "retry_transfer", "retry_refund") {
		return PaymentOperation{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PaymentOperation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var purpose, status string
	var version int
	var orderID *uuid.UUID
	var providerRefundText *string
	var refundOperationID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT purpose,status,version,order_id,provider_refund_id,refund_operation_id FROM payment_intents WHERE id=$1 FOR UPDATE`, paymentID).Scan(
		&purpose, &status, &version, &orderID, &providerRefundText, &refundOperationID); errors.Is(err, pgx.ErrNoRows) {
		return PaymentOperation{}, ErrNotFound
	} else if err != nil {
		return PaymentOperation{}, err
	}
	if version != input.ExpectedVersion {
		return PaymentOperation{}, ErrConflict
	}
	jobKind := ""
	fromStatus := status
	switch input.Action {
	case "retry_transfer":
		if purpose != "task" || status != "transfer_pending" {
			return PaymentOperation{}, ErrConflict
		}
		jobKind = payments.TaskTransferJobKind
	case "retry_refund":
		if purpose == "task" {
			if status == "refund_failed" {
				refundOperationID = uuidPointer(uuid.New())
				if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='refund_pending',provider_refund_id=NULL,refund_operation_id=$2,updated_at=now(),version=version+1 WHERE id=$1`, paymentID, refundOperationID); err != nil {
					return PaymentOperation{}, err
				}
			} else if status == "refund_pending" && providerRefundText == nil && refundOperationID != nil {
				if _, err := tx.Exec(ctx, `UPDATE payment_intents SET updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
					return PaymentOperation{}, err
				}
			} else {
				return PaymentOperation{}, ErrConflict
			}
			jobKind = payments.TaskRefundJobKind
		} else if purpose == "product" {
			if orderID == nil {
				return PaymentOperation{}, ErrConflict
			}
			var orderStatus string
			var orderOperationID *uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT status,refund_operation_id FROM orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&orderStatus, &orderOperationID); err != nil {
				return PaymentOperation{}, err
			}
			if status == "paid" && orderStatus == "fulfilled" {
				var latestEvent string
				if err := tx.QueryRow(ctx, `SELECT event_type FROM payment_intent_events WHERE payment_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, paymentID).Scan(&latestEvent); err != nil || latestEvent != "refund.failed" {
					return PaymentOperation{}, ErrConflict
				}
				operationID := uuid.New()
				if _, err := tx.Exec(ctx, `UPDATE orders SET status='refund_requested',refund_reason=$2,refund_idempotency_key=$3,refund_operation_id=$4,refund_requested_at=now(),updated_at=now() WHERE id=$1`,
					orderID, "Administrative payment recovery", "admin-recovery:"+operationID.String(), operationID); err != nil {
					return PaymentOperation{}, err
				}
				if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='refund_pending',provider_refund_id=NULL,updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
					return PaymentOperation{}, err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO order_events(order_id,actor_id,from_status,to_status,reason,sequence) SELECT $1,$2,'fulfilled','refund_requested',$3,COALESCE(max(sequence),0)+1 FROM order_events WHERE order_id=$1`, orderID, actorID, "Administrative payment recovery"); err != nil {
					return PaymentOperation{}, err
				}
			} else if status == "refund_pending" && orderStatus == "refund_requested" && providerRefundText == nil && orderOperationID != nil {
				if _, err := tx.Exec(ctx, `UPDATE payment_intents SET updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
					return PaymentOperation{}, err
				}
			} else {
				return PaymentOperation{}, ErrConflict
			}
			jobKind = payments.ProductRefundJobKind
		} else {
			return PaymentOperation{}, ErrConflict
		}
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2 AND status IN ('queued','running'))`, jobKind, paymentID.String()).Scan(&active); err != nil {
		return PaymentOperation{}, err
	}
	if active {
		return PaymentOperation{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('paymentId',$2::text),20)`, jobKind, paymentID); err != nil {
		return PaymentOperation{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence) VALUES($1,$2,$3,$4,jsonb_build_object('actorId',$5::text))`,
		paymentID, "admin."+input.Action, fromStatus, mapRecoveryStatus(input.Action), actorID); err != nil {
		return PaymentOperation{}, err
	}
	if input.Action == "retry_transfer" {
		if _, err := tx.Exec(ctx, `UPDATE payment_intents SET updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
			return PaymentOperation{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PaymentOperation{}, err
	}
	return s.paymentOperation(ctx, paymentID)
}

func (s *Service) ReplayPaymentEvent(ctx context.Context, _ uuid.UUID, eventID uuid.UUID, input PaymentEventReplay, _ string) (PaymentOperation, error) {
	if input.ExpectedVersion < 1 {
		return PaymentOperation{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PaymentOperation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var paymentID *uuid.UUID
	var status string
	var version int
	var errorCode *string
	if err := tx.QueryRow(ctx, `
		SELECT e.payment_id,p.status,p.version,p.error_code FROM payment_provider_events e
		JOIN payment_provider_event_processing p ON p.event_id=e.id WHERE e.id=$1 FOR UPDATE OF p`, eventID).Scan(
		&paymentID, &status, &version, &errorCode); errors.Is(err, pgx.ErrNoRows) {
		return PaymentOperation{}, ErrNotFound
	} else if err != nil {
		return PaymentOperation{}, err
	}
	if paymentID == nil || version != input.ExpectedVersion || oneOf(status, "processed", "ignored") {
		return PaymentOperation{}, ErrConflict
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE kind=$1 AND payload->>'eventId'=$2 AND status IN ('queued','running'))`, payments.PaymentEventJobKind, eventID.String()).Scan(&active); err != nil {
		return PaymentOperation{}, err
	}
	if active {
		return PaymentOperation{}, ErrConflict
	}
	if status == "failed" {
		if errorCode == nil {
			return PaymentOperation{}, ErrConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_provider_event_processing SET status='received',error_code=NULL,last_error_code=$2,next_attempt_at=NULL,processed_at=NULL,replay_count=replay_count+1,version=version+1,updated_at=now() WHERE event_id=$1`, eventID, errorCode); err != nil {
			return PaymentOperation{}, err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE payment_provider_event_processing SET replay_count=replay_count+1,version=version+1,updated_at=now() WHERE event_id=$1`, eventID); err != nil {
			return PaymentOperation{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('eventId',$2::text),8)`, payments.PaymentEventJobKind, eventID); err != nil {
		return PaymentOperation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PaymentOperation{}, err
	}
	return s.paymentOperation(ctx, *paymentID)
}

func (s *Service) paymentOperation(ctx context.Context, paymentID uuid.UUID) (PaymentOperation, error) {
	page, err := s.ListPaymentOperations(ctx, PaymentOperationListInput{Query: paymentID.String(), Limit: 1})
	if err != nil {
		return PaymentOperation{}, err
	}
	if len(page.Items) != 1 || page.Items[0].ID != paymentID {
		return PaymentOperation{}, ErrNotFound
	}
	return page.Items[0], nil
}

func mapRecoveryStatus(action string) string {
	if action == "retry_refund" {
		return "refund_pending"
	}
	return "transfer_pending"
}

func uuidPointer(value uuid.UUID) *uuid.UUID { return &value }

func scanPaymentOperation(row scanner) (PaymentOperation, error) {
	var item PaymentOperation
	var destinationID *uuid.UUID
	var destinationProviderID, destinationStatus *string
	var destinationCharges, destinationPayouts *bool
	var destinationVersion *int
	var destinationVerifiedAt *time.Time
	var jobID, eventID, eventJobID *uuid.UUID
	var jobKind, jobStatus, jobError, eventProviderID, eventType, eventStatus, eventError, eventLastError, eventJobKind, eventJobStatus, eventJobError *string
	var jobAttempts, jobMax, eventAttempts, eventReplays, eventVersion, eventJobAttempts, eventJobMax *int
	var jobAvailable, jobUpdated, eventOccurred, eventReceived, eventUpdated, eventJobAvailable, eventJobUpdated *time.Time
	err := row.Scan(
		&item.ID, &item.Purpose, &item.Status, &item.AmountCents, &item.Currency, &item.LiveMode,
		&item.PayerID, &item.PayerEmail, &item.PayerHandle, &item.PayerDisplayName,
		&item.PayeeID, &item.PayeeHandle, &item.PayeeDisplayName, &item.ResourceID, &item.ResourceTitle, &item.TargetPath,
		&item.OrderID, &item.ProposalID, &item.ProviderCheckoutID, &item.ProviderPaymentID, &item.ProviderChargeID,
		&item.ProviderRefundID, &item.ProviderTransferID, &item.Version, &item.PaidAt, &item.TransferredAt, &item.RefundedAt,
		&item.CheckoutExpiresAt, &item.CreatedAt, &item.UpdatedAt,
		&destinationID, &destinationProviderID, &destinationStatus, &destinationCharges, &destinationPayouts, &destinationVersion, &destinationVerifiedAt,
		&jobID, &jobKind, &jobStatus, &jobAttempts, &jobMax, &jobError, &jobAvailable, &jobUpdated,
		&eventID, &eventProviderID, &eventType, &eventStatus, &eventAttempts, &eventReplays, &eventVersion, &eventError, &eventLastError,
		&eventOccurred, &eventReceived, &eventUpdated,
		&eventJobID, &eventJobKind, &eventJobStatus, &eventJobAttempts, &eventJobMax, &eventJobError, &eventJobAvailable, &eventJobUpdated,
		&item.AttentionCode,
	)
	if err != nil {
		return PaymentOperation{}, err
	}
	if destinationID != nil {
		item.Destination = &PaymentDestinationSummary{ID: *destinationID, DestinationID: *destinationProviderID, Status: *destinationStatus, ChargesEnabled: *destinationCharges, PayoutsEnabled: *destinationPayouts, Version: *destinationVersion, VerifiedAt: destinationVerifiedAt}
	}
	if jobID != nil {
		item.Job = &PaymentWorkerJob{ID: *jobID, Kind: *jobKind, Status: *jobStatus, Attempts: *jobAttempts, MaxAttempts: *jobMax, LastErrorCode: jobError, AvailableAt: *jobAvailable, UpdatedAt: *jobUpdated}
	}
	if eventID != nil {
		item.ProviderEvent = &PaymentProviderEvent{ID: *eventID, ProviderEventID: *eventProviderID, EventType: *eventType, ProcessingState: *eventStatus, AttemptCount: *eventAttempts, ReplayCount: *eventReplays, Version: *eventVersion, ErrorCode: eventError, LastErrorCode: eventLastError, OccurredAt: *eventOccurred, ReceivedAt: *eventReceived, UpdatedAt: *eventUpdated}
		if eventJobID != nil {
			item.ProviderEvent.Job = &PaymentWorkerJob{ID: *eventJobID, Kind: *eventJobKind, Status: *eventJobStatus, Attempts: *eventJobAttempts, MaxAttempts: *eventJobMax, LastErrorCode: eventJobError, AvailableAt: *eventJobAvailable, UpdatedAt: *eventJobUpdated}
		}
	}
	return item, nil
}
