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
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/jackc/pgx/v5"
)

type TaskOperation struct {
	ID                    uuid.UUID  `json:"id"`
	Title                 string     `json:"title"`
	Status                string     `json:"status"`
	DeliverableType       string     `json:"deliverableType"`
	AmountCents           int        `json:"amountCents"`
	Currency              string     `json:"currency"`
	Deadline              time.Time  `json:"deadline"`
	ClientID              uuid.UUID  `json:"clientId"`
	ClientHandle          string     `json:"clientHandle"`
	ClientDisplayName     string     `json:"clientDisplayName"`
	AssigneeID            *uuid.UUID `json:"assigneeId,omitempty"`
	AssigneeHandle        *string    `json:"assigneeHandle,omitempty"`
	AssigneeDisplayName   *string    `json:"assigneeDisplayName,omitempty"`
	ProposalCount         int        `json:"proposalCount"`
	LatestDeliveryVersion *int       `json:"latestDeliveryVersion,omitempty"`
	DisputeID             *uuid.UUID `json:"disputeId,omitempty"`
	DisputeOpenedByID     *uuid.UUID `json:"disputeOpenedById,omitempty"`
	DisputeOpenedByHandle *string    `json:"disputeOpenedByHandle,omitempty"`
	DisputeReason         *string    `json:"disputeReason,omitempty"`
	DisputeStatus         *string    `json:"disputeStatus,omitempty"`
	DisputeVersion        *int       `json:"disputeVersion,omitempty"`
	DisputeCreatedAt      *time.Time `json:"disputeCreatedAt,omitempty"`
	DisputeResolvedAt     *time.Time `json:"disputeResolvedAt,omitempty"`
	RiskStatus            *string    `json:"riskStatus,omitempty"`
	SettlementID          *uuid.UUID `json:"settlementId,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

type TaskDisputeResolution struct {
	Decision        string `json:"decision"`
	ExpectedVersion int    `json:"expectedVersion"`
}

type TaskOperationListInput struct {
	Query         string
	Status        string
	DisputeStatus string
	Cursor        string
	Limit         int
}

type TaskOperationPage struct {
	Items      []TaskOperation `json:"items"`
	NextCursor *string         `json:"nextCursor,omitempty"`
}

type taskOperationCursor struct {
	Priority  int       `json:"priority"`
	UpdatedAt time.Time `json:"updatedAt"`
	ID        uuid.UUID `json:"id"`
}

func (s *Service) ListTaskOperations(ctx context.Context, input TaskOperationListInput) (TaskOperationPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	input.DisputeStatus = strings.ToLower(strings.TrimSpace(input.DisputeStatus))
	if len(input.Query) > 120 || (input.Status != "" && !oneOf(input.Status, "draft", "open", "assigned", "submitted", "revision", "accepted", "disputed", "cancelled")) ||
		(input.DisputeStatus != "" && !oneOf(input.DisputeStatus, "none", "open", "resolved_creator", "resolved_client")) {
		return TaskOperationPage{}, ErrInvalidTaskFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return TaskOperationPage{}, ErrInvalidTaskFilter
	}
	var cursorPriority *int
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeTaskOperationCursor(input.Cursor)
		if err != nil {
			return TaskOperationPage{}, err
		}
		cursorPriority, cursorTime, cursorID = &cursor.Priority, &cursor.UpdatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, adminTaskSelect+`
		WHERE ($1='' OR strpos(lower(d.title),$1)>0 OR strpos(lower(c.handle),$1)>0 OR
			strpos(lower(COALESCE(a.handle,'')),$1)>0 OR strpos(lower(COALESCE(td.reason,'')),$1)>0)
		  AND ($2='' OR d.status=$2)
		  AND ($3='' OR ($3='none' AND td.status IS NULL) OR td.status=$3)
		  AND ($4::int IS NULL OR CASE WHEN td.status='open' THEN 0 ELSE 1 END > $4 OR
			(CASE WHEN td.status='open' THEN 0 ELSE 1 END = $4 AND (d.updated_at,d.id) < ($5,$6::uuid)))
		ORDER BY CASE WHEN td.status='open' THEN 0 ELSE 1 END,d.updated_at DESC,d.id DESC LIMIT $7`,
		input.Query, input.Status, input.DisputeStatus, cursorPriority, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return TaskOperationPage{}, fmt.Errorf("list admin task operations: %w", err)
	}
	defer rows.Close()
	items := make([]TaskOperation, 0)
	for rows.Next() {
		item, err := scanTaskOperation(rows)
		if err != nil {
			return TaskOperationPage{}, fmt.Errorf("scan admin task operation: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return TaskOperationPage{}, err
	}
	page := TaskOperationPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeTaskOperationCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func encodeTaskOperationCursor(item TaskOperation) string {
	priority := 1
	if item.DisputeStatus != nil && *item.DisputeStatus == "open" {
		priority = 0
	}
	body, _ := json.Marshal(taskOperationCursor{Priority: priority, UpdatedAt: item.UpdatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeTaskOperationCursor(value string) (taskOperationCursor, error) {
	var cursor taskOperationCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.ID == uuid.Nil || cursor.UpdatedAt.IsZero() || cursor.Priority < 0 || cursor.Priority > 1 {
		return taskOperationCursor{}, ErrInvalidTaskFilter
	}
	return cursor, nil
}

func (s *Service) ResolveTaskDispute(ctx context.Context, actorID, taskID uuid.UUID, input TaskDisputeResolution, _ string) (TaskOperation, error) {
	input.Decision = strings.TrimSpace(strings.ToLower(input.Decision))
	if input.ExpectedVersion < 1 || !oneOf(input.Decision, "release_creator", "cancel_without_settlement") {
		return TaskOperation{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TaskOperation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status, currency, title, disputeStatus string
	var clientID, disputeID uuid.UUID
	var assigneeID *uuid.UUID
	var amount, disputeVersion int
	err = tx.QueryRow(ctx, `
		SELECT d.status,d.client_id,d.assignee_id,d.currency,d.title,
		       COALESCE((SELECT p.amount_cents FROM proposals p WHERE p.demand_id=d.id AND p.status='accepted' LIMIT 1),d.budget_cents),
		       td.id,td.status,td.version
		FROM demands d JOIN task_disputes td ON td.demand_id=d.id
		WHERE d.id=$1 ORDER BY td.created_at DESC,td.id DESC LIMIT 1 FOR UPDATE OF d,td`, taskID).Scan(&status, &clientID, &assigneeID, &currency, &title, &amount, &disputeID, &disputeStatus, &disputeVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskOperation{}, ErrNotFound
	}
	if err != nil {
		return TaskOperation{}, err
	}
	if status != "disputed" || disputeStatus != "open" || disputeVersion != input.ExpectedVersion || assigneeID == nil {
		return TaskOperation{}, ErrConflict
	}

	providerPayment := false
	var paymentID uuid.UUID
	var paymentStatus, paymentCurrency string
	var paymentPayeeID *uuid.UUID
	var paymentAmount int
	err = tx.QueryRow(ctx, `
		SELECT id,status,payee_id,amount_cents,currency FROM payment_intents
		WHERE purpose='task' AND resource_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1 FOR UPDATE`, taskID).Scan(
		&paymentID, &paymentStatus, &paymentPayeeID, &paymentAmount, &paymentCurrency)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return TaskOperation{}, err
	}
	if err == nil {
		providerPayment = true
		if paymentPayeeID == nil || *paymentPayeeID != *assigneeID || paymentAmount != amount || paymentCurrency != currency || !oneOf(paymentStatus, "paid", "refund_failed") {
			return TaskOperation{}, ErrConflict
		}
	}

	note := "Administrative decision: " + input.Decision
	metadata := map[string]any{"decision": input.Decision, "previousStatus": status, "disputeId": disputeID, "amountCents": 0, "currency": currency, "paymentMode": "local_test"}
	if providerPayment {
		metadata["paymentMode"] = "stripe"
		metadata["paymentId"] = paymentID
	}
	toStatus := "cancelled"
	resolutionStatus := "resolved_client"
	eventKind := "admin_dispute_cancelled"
	var settlementID uuid.UUID
	if input.Decision == "release_creator" {
		var deliveryID uuid.UUID
		err = tx.QueryRow(ctx, `
			UPDATE deliveries SET status='accepted',review_note=$2,reviewed_at=now(),accepted_at=now(),updated_at=now()
			WHERE id=(SELECT id FROM deliveries WHERE demand_id=$1 AND status='disputed' ORDER BY version DESC,id DESC LIMIT 1)
			RETURNING id`, taskID, note).Scan(&deliveryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return TaskOperation{}, ErrConflict
		}
		if err != nil {
			return TaskOperation{}, err
		}
		settlementID = uuid.New()
		settlementMode := "local_test"
		if providerPayment {
			settlementMode = "stripe_pending"
			if _, err = tx.Exec(ctx, `INSERT INTO task_settlements(id,demand_id,client_id,creator_id,amount_cents,currency,mode) VALUES($1,$2,$3,$4,$5,$6,$7)`, settlementID, taskID, clientID, *assigneeID, amount, currency, settlementMode); err != nil {
				return TaskOperation{}, err
			}
			if _, err = tx.Exec(ctx, `UPDATE payment_intents SET status='transfer_pending',updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
				return TaskOperation{}, err
			}
			if _, err = tx.Exec(ctx, `
				INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
				VALUES($1,'transfer.requested',$2,'transfer_pending',jsonb_build_object('taskId',$3::text,'settlementId',$4::text,'adminDisputeId',$5::text))`, paymentID, paymentStatus, taskID, settlementID, disputeID); err != nil {
				return TaskOperation{}, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('paymentId',$2::text),20)`, payments.TaskTransferJobKind, paymentID); err != nil {
				return TaskOperation{}, err
			}
		} else {
			if _, err = tx.Exec(ctx, `INSERT INTO task_settlements(id,demand_id,client_id,creator_id,amount_cents,currency,mode) VALUES($1,$2,$3,$4,$5,$6,$7)`, settlementID, taskID, clientID, *assigneeID, amount, currency, settlementMode); err != nil {
				return TaskOperation{}, err
			}
			if err = billing.TransferTx(ctx, tx, clientID, *assigneeID, settlementID, amount, currency, "task_payment", "task_earning", "Local Test administrator dispute settlement"); err != nil {
				return TaskOperation{}, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO ledger_entries(account_id,operation_id,direction,amount_cents,currency,reason) VALUES($1,$3,'debit',$4,$5,'task_local_test_admin_settlement'),($2,$3,'credit',$4,$5,'task_local_test_admin_settlement')`, clientID, *assigneeID, settlementID, amount, currency); err != nil {
				return TaskOperation{}, err
			}
		}
		toStatus = "accepted"
		resolutionStatus = "resolved_creator"
		eventKind = "admin_dispute_settled"
		metadata["amountCents"] = amount
		metadata["settlementId"] = settlementID
		metadata["deliveryId"] = deliveryID
		if _, err = tx.Exec(ctx, `UPDATE demands SET status='accepted',accepted_at=now(),updated_at=now() WHERE id=$1`, taskID); err != nil {
			return TaskOperation{}, err
		}
	} else {
		if providerPayment {
			operationID := uuid.New()
			if _, err = tx.Exec(ctx, `
				UPDATE payment_intents SET status='refund_pending',refund_operation_id=$2,provider_refund_id=NULL,updated_at=now(),version=version+1 WHERE id=$1`, paymentID, operationID); err != nil {
				return TaskOperation{}, err
			}
			if _, err = tx.Exec(ctx, `
				INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
				VALUES($1,'refund.requested',$2,'refund_pending',jsonb_build_object('taskId',$3::text,'adminDisputeId',$4::text))`, paymentID, paymentStatus, taskID, disputeID); err != nil {
				return TaskOperation{}, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('paymentId',$2::text),20)`, payments.TaskRefundJobKind, paymentID); err != nil {
				return TaskOperation{}, err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE demands SET status='cancelled',cancelled_at=now(),updated_at=now() WHERE id=$1`, taskID); err != nil {
			return TaskOperation{}, err
		}
	}

	if _, err = tx.Exec(ctx, `UPDATE task_disputes SET status=$2,resolution_note=$3,resolved_by=$4,resolved_at=now(),version=version+1 WHERE id=$1`, disputeID, resolutionStatus, note, actorID); err != nil {
		return TaskOperation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO task_events(demand_id,actor_id,kind,from_status,to_status,note,metadata) VALUES($1,$2,$3,'disputed',$4,$5,$6)`, taskID, actorID, eventKind, toStatus, note, metadata); err != nil {
		return TaskOperation{}, err
	}
	paymentMode := metadata["paymentMode"].(string)
	if err = notifyTaskResolution(ctx, tx, clientID, taskID, disputeID, title, input.Decision, paymentMode, true); err != nil {
		return TaskOperation{}, err
	}
	if err = notifyTaskResolution(ctx, tx, *assigneeID, taskID, disputeID, title, input.Decision, paymentMode, false); err != nil {
		return TaskOperation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return TaskOperation{}, err
	}
	return s.taskOperation(ctx, taskID)
}

func notifyTaskResolution(ctx context.Context, tx pgx.Tx, userID, taskID, disputeID uuid.UUID, taskTitle, decision, paymentMode string, client bool) error {
	title := "Task dispute resolved"
	body := "Operations cancelled “" + taskTitle + "” without a Local Test settlement."
	if decision == "release_creator" {
		body = "Operations accepted the delivery for “" + taskTitle + "” and recorded the Local Test USD settlement."
	}
	if paymentMode == "stripe" && decision == "release_creator" {
		body = "Operations accepted the delivery for “" + taskTitle + "”. The verified Provider payout is pending."
	} else if paymentMode == "stripe" {
		body = "Operations cancelled “" + taskTitle + "”. The Provider refund is pending signed confirmation."
	}
	audience := "creator"
	if client {
		audience = "client"
	}
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: userID, Kind: "task.dispute_resolved", Title: title, Body: body,
		TargetPath: "/market/demands/" + taskID.String(), ResourceType: "task", ResourceID: &taskID,
		SourceKey: "admin-task-resolution:" + disputeID.String() + ":" + audience,
	})
}

func (s *Service) taskOperation(ctx context.Context, id uuid.UUID) (TaskOperation, error) {
	item, err := scanTaskOperation(s.pool.QueryRow(ctx, adminTaskSelect+` WHERE d.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskOperation{}, ErrNotFound
	}
	return item, err
}

const adminTaskSelect = `
	SELECT d.id,d.title,d.status,d.deliverable_type,
	       COALESCE((SELECT p.amount_cents FROM proposals p WHERE p.demand_id=d.id AND p.status='accepted' LIMIT 1),d.budget_cents),
	       d.currency,d.deadline,c.id,c.handle,c.display_name,a.id,a.handle,a.display_name,
	       (SELECT count(*) FROM proposals p WHERE p.demand_id=d.id),ld.version,
	       td.id,opener.id,opener.handle,td.reason,td.status,td.version,td.created_at,td.resolved_at,
	       rs.status,ts.id,d.created_at,d.updated_at
	FROM demands d
	JOIN users c ON c.id=d.client_id
	LEFT JOIN users a ON a.id=d.assignee_id
	LEFT JOIN LATERAL (SELECT version FROM deliveries WHERE demand_id=d.id ORDER BY version DESC,id DESC LIMIT 1) ld ON true
	LEFT JOIN LATERAL (SELECT * FROM task_disputes WHERE demand_id=d.id ORDER BY created_at DESC,id DESC LIMIT 1) td ON true
	LEFT JOIN users opener ON opener.id=td.opened_by
	LEFT JOIN risk_signals rs ON rs.source_key='task_dispute:'||d.id::text
	LEFT JOIN task_settlements ts ON ts.demand_id=d.id`

func scanTaskOperation(row scanner) (TaskOperation, error) {
	var item TaskOperation
	err := row.Scan(&item.ID, &item.Title, &item.Status, &item.DeliverableType, &item.AmountCents, &item.Currency,
		&item.Deadline, &item.ClientID, &item.ClientHandle, &item.ClientDisplayName, &item.AssigneeID, &item.AssigneeHandle,
		&item.AssigneeDisplayName, &item.ProposalCount, &item.LatestDeliveryVersion, &item.DisputeID, &item.DisputeOpenedByID,
		&item.DisputeOpenedByHandle, &item.DisputeReason, &item.DisputeStatus, &item.DisputeVersion, &item.DisputeCreatedAt,
		&item.DisputeResolvedAt, &item.RiskStatus, &item.SettlementID, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}
