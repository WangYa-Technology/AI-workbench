package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	productDisputeKeyPattern      = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)
	productDisputeEvidencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/#-]{2,499}$`)
)

type ProductPaymentDispute struct {
	ID                uuid.UUID  `json:"id"`
	Provider          string     `json:"provider"`
	LiveMode          bool       `json:"liveMode"`
	ProviderDisputeID string     `json:"providerDisputeId"`
	PaymentID         *uuid.UUID `json:"paymentId,omitempty"`
	OrderID           *uuid.UUID `json:"orderId,omitempty"`
	SellerID          *uuid.UUID `json:"sellerId,omitempty"`
	SettlementID      *uuid.UUID `json:"settlementId,omitempty"`
	ProviderPaymentID string     `json:"providerPaymentId"`
	ProviderChargeID  string     `json:"providerChargeId"`
	AmountCents       int64      `json:"amountCents"`
	Currency          string     `json:"currency"`
	ProviderStatus    string     `json:"providerStatus"`
	ActionStatus      string     `json:"actionStatus"`
	ReviewStatus      string     `json:"reviewStatus"`
	ReviewRoute       string     `json:"reviewRoute"`
	EvidenceStatus    string     `json:"evidenceStatus"`
	Bound             bool       `json:"bound"`
	ProductID         *uuid.UUID `json:"productId,omitempty"`
	ProductTitle      *string    `json:"productTitle,omitempty"`
	BuyerID           *uuid.UUID `json:"buyerId,omitempty"`
	BuyerHandle       *string    `json:"buyerHandle,omitempty"`
	BuyerDisplayName  *string    `json:"buyerDisplayName,omitempty"`
	SellerHandle      *string    `json:"sellerHandle,omitempty"`
	SellerDisplayName *string    `json:"sellerDisplayName,omitempty"`
	OrderStatus       *string    `json:"orderStatus,omitempty"`
	PaymentStatus     *string    `json:"paymentStatus,omitempty"`
	SettlementStatus  *string    `json:"settlementStatus,omitempty"`
	DueBy             time.Time  `json:"dueBy"`
	LatestEventAt     time.Time  `json:"latestEventAt"`
	Version           int64      `json:"version"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type ProductPaymentDisputeEvent struct {
	ID                uuid.UUID `json:"id"`
	ProviderEventID   uuid.UUID `json:"providerEventId"`
	EventType         string    `json:"eventType"`
	ProviderStatus    string    `json:"providerStatus"`
	Reason            string    `json:"reason"`
	NetworkReasonCode string    `json:"networkReasonCode"`
	DueBy             time.Time `json:"dueBy"`
	OccurredAt        time.Time `json:"occurredAt"`
	Applied           bool      `json:"applied"`
	CreatedAt         time.Time `json:"createdAt"`
}

type ProductPaymentDisputeOperation struct {
	ID                 uuid.UUID `json:"id"`
	ActorID            uuid.UUID `json:"actorId"`
	ActorHandle        string    `json:"actorHandle"`
	ActorDisplayName   string    `json:"actorDisplayName"`
	Action             string    `json:"action"`
	Route              string    `json:"route"`
	Reason             string    `json:"reason"`
	EvidenceReference  *string   `json:"evidenceReference,omitempty"`
	ExpectedVersion    int64     `json:"expectedVersion"`
	ResultingVersion   int64     `json:"resultingVersion"`
	FromReviewStatus   string    `json:"fromReviewStatus"`
	ToReviewStatus     string    `json:"toReviewStatus"`
	FromEvidenceStatus string    `json:"fromEvidenceStatus"`
	ToEvidenceStatus   string    `json:"toEvidenceStatus"`
	RequestID          string    `json:"requestId"`
	CreatedAt          time.Time `json:"createdAt"`
}

type ProductPaymentDisputeEvidence struct {
	ID                uuid.UUID `json:"id"`
	OperationID       uuid.UUID `json:"operationId"`
	SubmittedBy       uuid.UUID `json:"submittedBy"`
	SubmitterHandle   string    `json:"submitterHandle"`
	ProviderReference string    `json:"providerReference"`
	SubmittedAt       time.Time `json:"submittedAt"`
}

type ProductPaymentDisputeDetail struct {
	ProductPaymentDispute
	Events              []ProductPaymentDisputeEvent     `json:"events"`
	Operations          []ProductPaymentDisputeOperation `json:"operations"`
	EvidenceSubmissions []ProductPaymentDisputeEvidence  `json:"evidenceSubmissions"`
}

type ProductPaymentDisputeListInput struct {
	Query        string
	ActionStatus string
	ReviewStatus string
	Binding      string
	Mode         string
	Cursor       string
	Limit        int
}

type ProductPaymentDisputePage struct {
	Items      []ProductPaymentDispute `json:"items"`
	NextCursor *string                 `json:"nextCursor,omitempty"`
}

type ProductPaymentDisputeCommand struct {
	Action            string `json:"action"`
	Route             string `json:"route"`
	EvidenceReference string `json:"evidenceReference,omitempty"`
	ExpectedVersion   int64  `json:"expectedVersion"`
	Reason            string `json:"reason"`
	Confirmed         bool   `json:"confirmed"`
}

type ProductPaymentDisputeCommandResult struct {
	ProductPaymentDisputeDetail
	OperationID uuid.UUID `json:"operationId"`
	Replayed    bool      `json:"replayed"`
}

type productPaymentDisputeCursor struct {
	Version     int       `json:"v"`
	Priority    int       `json:"p"`
	DueBy       time.Time `json:"due"`
	ID          uuid.UUID `json:"id"`
	FilterScope string    `json:"scope"`
}

const productPaymentDisputeSelect = `
	SELECT d.id,d.provider,d.live_mode,d.provider_dispute_id,d.payment_id,d.order_id,d.seller_id,d.settlement_id,
	       d.provider_payment_id,d.provider_charge_id,d.amount_cents,d.currency,d.provider_status,d.action_status,
	       d.review_status,d.review_route,d.evidence_status,
	       (d.payment_id IS NOT NULL AND d.order_id IS NOT NULL) AS bound,
	       o.product_id,COALESCE(o.product_title_snapshot,p.title),o.buyer_id,buyer.handle,buyer.display_name,
	       seller.handle,seller.display_name,o.status,pi.status,ps.status,d.due_by,d.latest_event_at,d.version,d.created_at,d.updated_at
	FROM product_payment_disputes d
	LEFT JOIN payment_intents pi ON pi.id=d.payment_id
	LEFT JOIN orders o ON o.id=d.order_id
	LEFT JOIN products p ON p.id=o.product_id
	LEFT JOIN users buyer ON buyer.id=o.buyer_id
	LEFT JOIN users seller ON seller.id=d.seller_id
	LEFT JOIN product_settlements ps ON ps.id=d.settlement_id`

func (s *Service) ListProductPaymentDisputes(ctx context.Context, actorID uuid.UUID, input ProductPaymentDisputeListInput) (ProductPaymentDisputePage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.ActionStatus = strings.ToLower(strings.TrimSpace(input.ActionStatus))
	input.ReviewStatus = strings.ToLower(strings.TrimSpace(input.ReviewStatus))
	input.Binding = strings.ToLower(strings.TrimSpace(input.Binding))
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	if actorID == uuid.Nil || utf8.RuneCountInString(input.Query) > 120 ||
		(input.ActionStatus != "" && !oneOf(input.ActionStatus, "needs_response", "warning_needs_response", "under_review", "warning_under_review", "won", "lost", "charge_refunded", "prevented", "requires_review")) ||
		(input.ReviewStatus != "" && !oneOf(input.ReviewStatus, "new", "acknowledged", "evidence_requested", "evidence_submitted", "escalated")) ||
		(input.Binding != "" && !oneOf(input.Binding, "bound", "unbound")) ||
		(input.Mode != "" && !oneOf(input.Mode, "live", "test")) {
		return ProductPaymentDisputePage{}, ErrInvalidProductDisputeFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return ProductPaymentDisputePage{}, ErrInvalidProductDisputeFilter
	}
	scope := productPaymentDisputeFilterScope(input)
	var cursorPriority *int
	var cursorDueBy *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeProductPaymentDisputeCursor(input.Cursor, scope)
		if err != nil {
			return ProductPaymentDisputePage{}, err
		}
		cursorPriority, cursorDueBy, cursorID = &cursor.Priority, &cursor.DueBy, &cursor.ID
	}
	// The authority check takes a FOR SHARE lock so a role or permission
	// revocation cannot race a private read. PostgreSQL does not permit row
	// locks in a read-only transaction, so keep this transaction read-only by
	// behavior rather than setting the read-only transaction flag.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProductPaymentDisputePage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := financeAuthorityTx(ctx, tx, actorID, true); err != nil {
		return ProductPaymentDisputePage{}, err
	}
	rows, err := tx.Query(ctx, productPaymentDisputeSelect+`
		WHERE ($1='' OR d.id::text=$1 OR lower(d.provider_dispute_id)=$1 OR lower(d.provider_payment_id)=$1 OR lower(d.provider_charge_id)=$1
		  OR strpos(lower(COALESCE(o.product_title_snapshot,p.title,'')),$1)>0 OR strpos(lower(COALESCE(buyer.handle,'')),$1)>0 OR strpos(lower(COALESCE(seller.handle,'')),$1)>0)
		  AND ($2='' OR d.action_status=$2) AND ($3='' OR d.review_status=$3)
		  AND ($4='' OR ($4='bound' AND d.payment_id IS NOT NULL AND d.order_id IS NOT NULL) OR ($4='unbound' AND (d.payment_id IS NULL OR d.order_id IS NULL)))
		  AND ($5='' OR ($5='live' AND d.live_mode) OR ($5='test' AND NOT d.live_mode))
		  AND ($6::int IS NULL OR CASE WHEN d.action_status='won' THEN 1 ELSE 0 END>$6
		    OR (CASE WHEN d.action_status='won' THEN 1 ELSE 0 END=$6 AND (d.due_by,d.id)>($7,$8::uuid)))
		ORDER BY CASE WHEN d.action_status='won' THEN 1 ELSE 0 END,d.due_by,d.id LIMIT $9`,
		input.Query, input.ActionStatus, input.ReviewStatus, input.Binding, input.Mode,
		cursorPriority, cursorDueBy, cursorID, input.Limit+1)
	if err != nil {
		return ProductPaymentDisputePage{}, fmt.Errorf("list product payment disputes: %w", err)
	}
	defer rows.Close()
	items := make([]ProductPaymentDispute, 0)
	for rows.Next() {
		item, err := scanProductPaymentDispute(rows)
		if err != nil {
			return ProductPaymentDisputePage{}, fmt.Errorf("scan product payment dispute: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ProductPaymentDisputePage{}, err
	}
	page := ProductPaymentDisputePage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeProductPaymentDisputeCursor(page.Items[len(page.Items)-1], scope)
		page.NextCursor = &cursor
	}
	if err := tx.Commit(ctx); err != nil {
		return ProductPaymentDisputePage{}, err
	}
	return page, nil
}

func (s *Service) GetProductPaymentDispute(ctx context.Context, actorID, disputeID uuid.UUID) (ProductPaymentDisputeDetail, error) {
	if actorID == uuid.Nil || disputeID == uuid.Nil {
		return ProductPaymentDisputeDetail{}, ErrInvalid
	}
	// See ListProductPaymentDisputes: the locked authority check is required
	// for private reads and is incompatible with a PostgreSQL read-only tx.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProductPaymentDisputeDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := financeAuthorityTx(ctx, tx, actorID, true); err != nil {
		return ProductPaymentDisputeDetail{}, err
	}
	detail, err := productPaymentDisputeDetailTx(ctx, tx, disputeID, false)
	if err != nil {
		return ProductPaymentDisputeDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProductPaymentDisputeDetail{}, err
	}
	return detail, nil
}

func (s *Service) OperateProductPaymentDispute(ctx context.Context, actorID, disputeID uuid.UUID, input ProductPaymentDisputeCommand, key, requestID string) (ProductPaymentDisputeCommandResult, error) {
	result, err := s.operateProductPaymentDispute(ctx, actorID, disputeID, input, key, requestID)
	return result, financeCommandError(err)
}

func (s *Service) operateProductPaymentDispute(ctx context.Context, actorID, disputeID uuid.UUID, input ProductPaymentDisputeCommand, key, requestID string) (ProductPaymentDisputeCommandResult, error) {
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	input.Route = strings.ToLower(strings.TrimSpace(input.Route))
	input.Reason = strings.TrimSpace(input.Reason)
	input.EvidenceReference = strings.TrimSpace(input.EvidenceReference)
	reasonLength := utf8.RuneCountInString(input.Reason)
	validEvidence := input.Action == "record_evidence_submission" && productDisputeEvidencePattern.MatchString(input.EvidenceReference)
	if actorID == uuid.Nil || disputeID == uuid.Nil || !productDisputeKeyPattern.MatchString(key) || !input.Confirmed || input.ExpectedVersion < 1 ||
		!oneOf(input.Action, "route", "request_evidence", "record_evidence_submission", "escalate_recovery") ||
		!oneOf(input.Route, "finance", "seller_support", "provider_review", "collections") || reasonLength < 10 || reasonLength > 2000 || strings.ContainsRune(input.Reason, '\x00') ||
		(input.Action == "record_evidence_submission" && !validEvidence) || (input.Action != "record_evidence_submission" && input.EvidenceReference != "") ||
		(input.Action == "escalate_recovery" && input.Route != "collections") {
		return ProductPaymentDisputeCommandResult{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ProductPaymentDisputeCommandResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := financeAuthorityTx(ctx, tx, actorID, false); err != nil {
		return ProductPaymentDisputeCommandResult{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "admin:product-dispute:"+actorID.String()+":"+key); err != nil {
		return ProductPaymentDisputeCommandResult{}, err
	}
	var operationID, previousDisputeID uuid.UUID
	var previousAction, previousRoute, previousReason string
	var previousEvidence *string
	var previousExpected, previousResulting int64
	err = tx.QueryRow(ctx, `SELECT id,dispute_id,action,route,reason,evidence_reference,expected_version,resulting_version
		FROM product_payment_dispute_operations WHERE actor_id=$1 AND idempotency_key=$2`, actorID, key).Scan(
		&operationID, &previousDisputeID, &previousAction, &previousRoute, &previousReason, &previousEvidence, &previousExpected, &previousResulting)
	replayed := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ProductPaymentDisputeCommandResult{}, err
	}
	if replayed && (previousDisputeID != disputeID || previousAction != input.Action || previousRoute != input.Route || previousReason != input.Reason ||
		previousExpected != input.ExpectedVersion || stringPointerValue(previousEvidence) != input.EvidenceReference) {
		return ProductPaymentDisputeCommandResult{}, ErrConflict
	}
	detail, err := productPaymentDisputeDetailTx(ctx, tx, disputeID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductPaymentDisputeCommandResult{}, ErrNotFound
	}
	if err != nil {
		return ProductPaymentDisputeCommandResult{}, err
	}
	if err := financeAuthorityTx(ctx, tx, actorID, true); err != nil {
		return ProductPaymentDisputeCommandResult{}, err
	}
	if replayed {
		if detail.Version < previousResulting {
			return ProductPaymentDisputeCommandResult{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return ProductPaymentDisputeCommandResult{}, err
		}
		return ProductPaymentDisputeCommandResult{ProductPaymentDisputeDetail: detail, OperationID: operationID, Replayed: true}, nil
	}
	if detail.Version != input.ExpectedVersion {
		return ProductPaymentDisputeCommandResult{}, ErrConflict
	}
	fromReview, fromEvidence := detail.ReviewStatus, detail.EvidenceStatus
	toReview, toEvidence := fromReview, fromEvidence
	switch input.Action {
	case "route":
		if toReview == "new" {
			toReview = "acknowledged"
		}
	case "request_evidence":
		if fromEvidence == "submitted" {
			return ProductPaymentDisputeCommandResult{}, ErrConflict
		}
		toReview, toEvidence = "evidence_requested", "requested"
	case "record_evidence_submission":
		if fromEvidence == "not_requested" {
			return ProductPaymentDisputeCommandResult{}, ErrConflict
		}
		toReview, toEvidence = "evidence_submitted", "submitted"
	case "escalate_recovery":
		toReview = "escalated"
	}
	operationID = uuid.New()
	var evidence any
	if input.EvidenceReference != "" {
		evidence = input.EvidenceReference
	}
	if _, err := tx.Exec(ctx, `INSERT INTO product_payment_dispute_operations(
		id,dispute_id,actor_id,idempotency_key,action,route,reason,evidence_reference,expected_version,resulting_version,
		from_review_status,to_review_status,from_evidence_status,to_evidence_status,request_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9::bigint+1,$10,$11,$12,$13,$14)`, operationID, disputeID, actorID, key,
		input.Action, input.Route, input.Reason, evidence, input.ExpectedVersion, fromReview, toReview, fromEvidence, toEvidence, requestID); err != nil {
		return ProductPaymentDisputeCommandResult{}, err
	}
	if input.Action == "record_evidence_submission" {
		if _, err := tx.Exec(ctx, `INSERT INTO product_payment_dispute_evidence_submissions(dispute_id,operation_id,submitted_by,provider_reference)
			VALUES($1,$2,$3,$4)`, disputeID, operationID, actorID, input.EvidenceReference); err != nil {
			return ProductPaymentDisputeCommandResult{}, err
		}
	}
	command, err := tx.Exec(ctx, `UPDATE product_payment_disputes SET review_status=$2,review_route=$3,evidence_status=$4,version=version+1,updated_at=clock_timestamp()
		WHERE id=$1 AND version=$5`, disputeID, toReview, input.Route, toEvidence, input.ExpectedVersion)
	if err != nil {
		return ProductPaymentDisputeCommandResult{}, err
	}
	if command.RowsAffected() != 1 {
		return ProductPaymentDisputeCommandResult{}, ErrConflict
	}
	metadata, _ := json.Marshal(map[string]any{
		"operationId": operationID, "action": input.Action, "route": input.Route,
		"expectedVersion": input.ExpectedVersion, "resultingVersion": input.ExpectedVersion + 1,
		"fromReviewStatus": fromReview, "toReviewStatus": toReview,
		"fromEvidenceStatus": fromEvidence, "toEvidenceStatus": toEvidence,
	})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
		VALUES($1,'admin.product_payment_dispute_operated','product_payment_dispute',$2,$3,$4,$5::jsonb)`, actorID, disputeID, input.Reason, requestID, metadata); err != nil {
		return ProductPaymentDisputeCommandResult{}, err
	}
	detail, err = productPaymentDisputeDetailTx(ctx, tx, disputeID, false)
	if err != nil {
		return ProductPaymentDisputeCommandResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProductPaymentDisputeCommandResult{}, err
	}
	return ProductPaymentDisputeCommandResult{ProductPaymentDisputeDetail: detail, OperationID: operationID}, nil
}

func productPaymentDisputeDetailTx(ctx context.Context, tx pgx.Tx, disputeID uuid.UUID, lock bool) (ProductPaymentDisputeDetail, error) {
	query := productPaymentDisputeSelect + ` WHERE d.id=$1`
	if lock {
		query += ` FOR UPDATE OF d`
	}
	item, err := scanProductPaymentDispute(tx.QueryRow(ctx, query, disputeID))
	if err != nil {
		return ProductPaymentDisputeDetail{}, err
	}
	detail := ProductPaymentDisputeDetail{ProductPaymentDispute: item, Events: []ProductPaymentDisputeEvent{}, Operations: []ProductPaymentDisputeOperation{}, EvidenceSubmissions: []ProductPaymentDisputeEvidence{}}
	eventRows, err := tx.Query(ctx, `SELECT id,provider_event_id,event_type,provider_status,reason,network_reason_code,due_by,occurred_at,applied,created_at
		FROM product_payment_dispute_events WHERE dispute_id=$1 ORDER BY occurred_at DESC,provider_event_id DESC`, disputeID)
	if err != nil {
		return ProductPaymentDisputeDetail{}, err
	}
	for eventRows.Next() {
		var event ProductPaymentDisputeEvent
		if err := eventRows.Scan(&event.ID, &event.ProviderEventID, &event.EventType, &event.ProviderStatus, &event.Reason, &event.NetworkReasonCode, &event.DueBy, &event.OccurredAt, &event.Applied, &event.CreatedAt); err != nil {
			eventRows.Close()
			return ProductPaymentDisputeDetail{}, err
		}
		detail.Events = append(detail.Events, event)
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return ProductPaymentDisputeDetail{}, err
	}
	eventRows.Close()
	operationRows, err := tx.Query(ctx, `SELECT op.id,op.actor_id,u.handle,u.display_name,op.action,op.route,op.reason,op.evidence_reference,
		op.expected_version,op.resulting_version,op.from_review_status,op.to_review_status,op.from_evidence_status,op.to_evidence_status,op.request_id,op.created_at
		FROM product_payment_dispute_operations op JOIN users u ON u.id=op.actor_id WHERE op.dispute_id=$1 ORDER BY op.created_at DESC,op.id DESC`, disputeID)
	if err != nil {
		return ProductPaymentDisputeDetail{}, err
	}
	for operationRows.Next() {
		var operation ProductPaymentDisputeOperation
		if err := operationRows.Scan(&operation.ID, &operation.ActorID, &operation.ActorHandle, &operation.ActorDisplayName, &operation.Action, &operation.Route,
			&operation.Reason, &operation.EvidenceReference, &operation.ExpectedVersion, &operation.ResultingVersion, &operation.FromReviewStatus, &operation.ToReviewStatus,
			&operation.FromEvidenceStatus, &operation.ToEvidenceStatus, &operation.RequestID, &operation.CreatedAt); err != nil {
			operationRows.Close()
			return ProductPaymentDisputeDetail{}, err
		}
		detail.Operations = append(detail.Operations, operation)
	}
	if err := operationRows.Err(); err != nil {
		operationRows.Close()
		return ProductPaymentDisputeDetail{}, err
	}
	operationRows.Close()
	evidenceRows, err := tx.Query(ctx, `SELECT e.id,e.operation_id,e.submitted_by,u.handle,e.provider_reference,e.submitted_at
		FROM product_payment_dispute_evidence_submissions e JOIN users u ON u.id=e.submitted_by WHERE e.dispute_id=$1 ORDER BY e.submitted_at DESC,e.id DESC`, disputeID)
	if err != nil {
		return ProductPaymentDisputeDetail{}, err
	}
	defer evidenceRows.Close()
	for evidenceRows.Next() {
		var evidence ProductPaymentDisputeEvidence
		if err := evidenceRows.Scan(&evidence.ID, &evidence.OperationID, &evidence.SubmittedBy, &evidence.SubmitterHandle, &evidence.ProviderReference, &evidence.SubmittedAt); err != nil {
			return ProductPaymentDisputeDetail{}, err
		}
		detail.EvidenceSubmissions = append(detail.EvidenceSubmissions, evidence)
	}
	if err := evidenceRows.Err(); err != nil {
		return ProductPaymentDisputeDetail{}, err
	}
	return detail, nil
}

type productDisputeScanner interface {
	Scan(dest ...any) error
}

func scanProductPaymentDispute(row productDisputeScanner) (ProductPaymentDispute, error) {
	var item ProductPaymentDispute
	err := row.Scan(&item.ID, &item.Provider, &item.LiveMode, &item.ProviderDisputeID, &item.PaymentID, &item.OrderID, &item.SellerID, &item.SettlementID,
		&item.ProviderPaymentID, &item.ProviderChargeID, &item.AmountCents, &item.Currency, &item.ProviderStatus, &item.ActionStatus,
		&item.ReviewStatus, &item.ReviewRoute, &item.EvidenceStatus, &item.Bound, &item.ProductID, &item.ProductTitle, &item.BuyerID,
		&item.BuyerHandle, &item.BuyerDisplayName, &item.SellerHandle, &item.SellerDisplayName, &item.OrderStatus, &item.PaymentStatus,
		&item.SettlementStatus, &item.DueBy, &item.LatestEventAt, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func productPaymentDisputeFilterScope(input ProductPaymentDisputeListInput) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{input.Query, input.ActionStatus, input.ReviewStatus, input.Binding, input.Mode}, "\x00")))
	return fmt.Sprintf("%x", sum[:16])
}

func encodeProductPaymentDisputeCursor(item ProductPaymentDispute, scope string) string {
	priority := 0
	if item.ActionStatus == "won" {
		priority = 1
	}
	body, _ := json.Marshal(productPaymentDisputeCursor{Version: 1, Priority: priority, DueBy: item.DueBy, ID: item.ID, FilterScope: scope})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeProductPaymentDisputeCursor(value, scope string) (productPaymentDisputeCursor, error) {
	var cursor productPaymentDisputeCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, ErrInvalidProductDisputeFilter
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return cursor, ErrInvalidProductDisputeFilter
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return cursor, ErrInvalidProductDisputeFilter
	}
	if cursor.Version != 1 || cursor.Priority < 0 || cursor.Priority > 1 || cursor.DueBy.IsZero() || cursor.ID == uuid.Nil || cursor.FilterScope != scope {
		return cursor, ErrInvalidProductDisputeFilter
	}
	return cursor, nil
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
