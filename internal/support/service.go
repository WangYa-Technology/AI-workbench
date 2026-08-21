package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalid            = errors.New("invalid support case input")
	ErrInvalidAdminFilter = errors.New("invalid admin support filter")
	ErrInvalidOwnerFilter = errors.New("invalid owner support filter")
	ErrNotFound           = errors.New("support case not found")
	ErrConflict           = errors.New("support case version or state conflict")
	ErrSensitiveData      = errors.New("sensitive data is not accepted")
	ErrRelatedNotFound    = errors.New("related resource not found")
	longNumberPattern     = regexp.MustCompile(`(?:\d[ -]?){13,19}`)
	governmentPattern     = regexp.MustCompile(`(?i)\b(?:ssn|social security|passport|national id)\b`)
)

type Case struct {
	ID                     uuid.UUID  `json:"id"`
	RequesterID            uuid.UUID  `json:"requesterId"`
	RequesterHandle        string     `json:"requesterHandle"`
	Category               string     `json:"category"`
	Subject                string     `json:"subject"`
	Details                string     `json:"details"`
	RelatedResourceType    *string    `json:"relatedResourceType,omitempty"`
	RelatedResourceID      *uuid.UUID `json:"relatedResourceId,omitempty"`
	Locale                 string     `json:"locale"`
	ClaimantRelationship   *string    `json:"claimantRelationship,omitempty"`
	RightsStatement        *string    `json:"rightsStatement,omitempty"`
	Status                 string     `json:"status"`
	Version                int        `json:"version"`
	AssignedOperatorID     *uuid.UUID `json:"assignedOperatorId,omitempty"`
	AssignedOperatorHandle *string    `json:"assignedOperatorHandle,omitempty"`
	ResolutionCode         *string    `json:"resolutionCode,omitempty"`
	ResolutionReason       *string    `json:"resolutionReason,omitempty"`
	CreatedAt              time.Time  `json:"createdAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
	ResolvedAt             *time.Time `json:"resolvedAt,omitempty"`
	Messages               []Message  `json:"messages"`
	Events                 []Event    `json:"events"`
}

type Message struct {
	ID           uuid.UUID  `json:"id"`
	AuthorID     *uuid.UUID `json:"authorId,omitempty"`
	AuthorHandle *string    `json:"authorHandle,omitempty"`
	AuthorRole   string     `json:"authorRole"`
	Body         string     `json:"body"`
	CreatedAt    time.Time  `json:"createdAt"`
}

type Event struct {
	ID         uuid.UUID      `json:"id"`
	ActorID    *uuid.UUID     `json:"actorId,omitempty"`
	Kind       string         `json:"kind"`
	FromStatus *string        `json:"fromStatus,omitempty"`
	ToStatus   string         `json:"toStatus"`
	Reason     string         `json:"reason"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"createdAt"`
}

type CreateInput struct {
	Category             string     `json:"category"`
	Subject              string     `json:"subject"`
	Details              string     `json:"details"`
	RelatedResourceType  string     `json:"relatedResourceType"`
	RelatedResourceID    *uuid.UUID `json:"relatedResourceId"`
	Locale               string     `json:"locale"`
	ClaimantRelationship string     `json:"claimantRelationship"`
	RightsStatement      string     `json:"rightsStatement"`
}

type ReplyInput struct {
	Body            string `json:"body"`
	ExpectedVersion int    `json:"expectedVersion"`
}

type AdminReplyInput struct {
	Body            string `json:"body"`
	ExpectedVersion int    `json:"expectedVersion"`
}

type AdminUpdateInput struct {
	Status          string `json:"status"`
	ResolutionCode  string `json:"resolutionCode"`
	ExpectedVersion int    `json:"expectedVersion"`
}

type ListFilter struct {
	Query    string
	Status   string
	Category string
	Cursor   string
	Limit    int
}

type Page struct {
	Items      []Case  `json:"items"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Create(ctx context.Context, requesterID uuid.UUID, input CreateInput, requestID string) (Case, error) {
	normalizeCreate(&input)
	if err := validateCreate(input); err != nil {
		return Case{}, err
	}
	if input.RelatedResourceID != nil {
		visible, err := s.resourceVisible(ctx, requesterID, input.RelatedResourceType, *input.RelatedResourceID)
		if err != nil {
			return Case{}, err
		}
		if !visible {
			return Case{}, ErrRelatedNotFound
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Case{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO support_cases(requester_id,category,subject,details,related_resource_type,related_resource_id,locale,claimant_relationship,rights_statement)
		VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,NULLIF($8,''),NULLIF($9,'')) RETURNING id`,
		requesterID, input.Category, input.Subject, input.Details, input.RelatedResourceType, input.RelatedResourceID, input.Locale, input.ClaimantRelationship, input.RightsStatement).Scan(&id)
	if err != nil {
		return Case{}, fmt.Errorf("create support case: %w", err)
	}
	var messageID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO support_messages(case_id,author_id,author_role,body) VALUES($1,$2,'requester',$3) RETURNING id`, id, requesterID, input.Details).Scan(&messageID); err != nil {
		return Case{}, fmt.Errorf("create support opening message: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO support_events(case_id,actor_id,kind,to_status,message_id,reason,metadata) VALUES($1,$2,'created','open',$3,'Requester opened the case',jsonb_build_object('category',$4::text))`, id, requesterID, messageID, input.Category); err != nil {
		return Case{}, fmt.Errorf("record support case creation: %w", err)
	}
	if err := insertAudit(ctx, tx, requesterID, "support.case_created", id, "Requester opened a support case", requestID, map[string]any{"category": input.Category, "relatedResourceType": input.RelatedResourceType}); err != nil {
		return Case{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Case{}, err
	}
	return s.GetOwned(ctx, requesterID, id)
}

func (s *Service) GetOwned(ctx context.Context, requesterID, caseID uuid.UUID) (Case, error) {
	return s.get(ctx, caseID, &requesterID)
}

func (s *Service) ReplyOwned(ctx context.Context, requesterID, caseID uuid.UUID, input ReplyInput, requestID string) (Case, error) {
	input.Body = clean(input.Body)
	if !validText(input.Body, 2, 4000) || input.ExpectedVersion < 1 {
		return Case{}, ErrInvalid
	}
	if hasSensitiveData(input.Body) {
		return Case{}, ErrSensitiveData
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Case{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var version int
	err = tx.QueryRow(ctx, `SELECT status,version FROM support_cases WHERE id=$1 AND requester_id=$2 FOR UPDATE`, caseID, requesterID).Scan(&status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, ErrNotFound
	}
	if err != nil {
		return Case{}, err
	}
	if version != input.ExpectedVersion || !oneOf(status, "open", "in_review", "waiting_for_requester") {
		return Case{}, ErrConflict
	}
	next := status
	if status == "waiting_for_requester" {
		next = "in_review"
	}
	var messageID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO support_messages(case_id,author_id,author_role,body) VALUES($1,$2,'requester',$3) RETURNING id`, caseID, requesterID, input.Body).Scan(&messageID); err != nil {
		return Case{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE support_cases SET status=$2,version=version+1,updated_at=now() WHERE id=$1`, caseID, next); err != nil {
		return Case{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO support_events(case_id,actor_id,kind,from_status,to_status,message_id,reason) VALUES($1,$2,'requester_replied',$3,$4,$5,'Requester added information')`, caseID, requesterID, status, next, messageID); err != nil {
		return Case{}, err
	}
	if err := insertAudit(ctx, tx, requesterID, "support.requester_replied", caseID, "Requester added information", requestID, nil); err != nil {
		return Case{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Case{}, err
	}
	return s.GetOwned(ctx, requesterID, caseID)
}

func (s *Service) GetAdmin(ctx context.Context, caseID uuid.UUID) (Case, error) {
	return s.get(ctx, caseID, nil)
}

func (s *Service) AdminReply(ctx context.Context, actorID, caseID uuid.UUID, input AdminReplyInput, _ string) (Case, error) {
	input.Body = clean(input.Body)
	if !validText(input.Body, 2, 4000) || input.ExpectedVersion < 1 {
		return Case{}, ErrInvalid
	}
	if hasSensitiveData(input.Body) {
		return Case{}, ErrSensitiveData
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Case{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var requesterID uuid.UUID
	var status string
	var version int
	err = tx.QueryRow(ctx, `SELECT requester_id,status,version FROM support_cases WHERE id=$1 FOR UPDATE`, caseID).Scan(&requesterID, &status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, ErrNotFound
	}
	if err != nil {
		return Case{}, err
	}
	if version != input.ExpectedVersion || !oneOf(status, "open", "in_review", "waiting_for_requester") {
		return Case{}, ErrConflict
	}
	next := status
	if status == "open" {
		next = "in_review"
	}
	var messageID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO support_messages(case_id,author_id,author_role,body) VALUES($1,$2,'operator',$3) RETURNING id`, caseID, actorID, input.Body).Scan(&messageID); err != nil {
		return Case{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE support_cases SET status=$2,assigned_operator_id=COALESCE(assigned_operator_id,$3),version=version+1,updated_at=now() WHERE id=$1`, caseID, next, actorID); err != nil {
		return Case{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO support_events(case_id,actor_id,kind,from_status,to_status,message_id,reason) VALUES($1,$2,'operator_replied',$3,$4,$5,'Operator reply added')`, caseID, actorID, status, next, messageID); err != nil {
		return Case{}, err
	}
	if err := notifyRequester(ctx, tx, requesterID, caseID, "Support replied", "A support operator added a reply to your case.", "reply:"+messageID.String()); err != nil {
		return Case{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Case{}, err
	}
	return s.GetAdmin(ctx, caseID)
}

func (s *Service) AdminUpdate(ctx context.Context, actorID, caseID uuid.UUID, input AdminUpdateInput, _ string) (Case, error) {
	input.Status = strings.TrimSpace(strings.ToLower(input.Status))
	input.ResolutionCode = strings.TrimSpace(strings.ToLower(input.ResolutionCode))
	if !validStatus(input.Status) || input.ExpectedVersion < 1 {
		return Case{}, ErrInvalid
	}
	if oneOf(input.Status, "resolved", "closed") != validResolution(input.ResolutionCode) {
		return Case{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Case{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var requesterID uuid.UUID
	var status string
	var version int
	err = tx.QueryRow(ctx, `SELECT requester_id,status,version FROM support_cases WHERE id=$1 FOR UPDATE`, caseID).Scan(&requesterID, &status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, ErrNotFound
	}
	if err != nil {
		return Case{}, err
	}
	if version != input.ExpectedVersion || !transitionAllowed(status, input.Status) {
		return Case{}, ErrConflict
	}
	resolved := oneOf(input.Status, "resolved", "closed")
	statusReason := "Administrative status change: " + input.Status
	if _, err := tx.Exec(ctx, `
		UPDATE support_cases SET status=$2,assigned_operator_id=COALESCE(assigned_operator_id,$3),version=version+1,
		 resolution_code=CASE WHEN $4 THEN $5 ELSE NULL END,resolution_reason=CASE WHEN $4 THEN $6 ELSE NULL END,
		 resolved_at=CASE WHEN $4 THEN now() ELSE NULL END,updated_at=now() WHERE id=$1`, caseID, input.Status, actorID, resolved, nullable(input.ResolutionCode), statusReason); err != nil {
		return Case{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO support_events(case_id,actor_id,kind,from_status,to_status,reason,metadata) VALUES($1,$2,'status_changed',$3,$4,$5,jsonb_build_object('resolutionCode',NULLIF($6::text,'')))`, caseID, actorID, status, input.Status, statusReason, input.ResolutionCode); err != nil {
		return Case{}, err
	}
	if err := notifyRequester(ctx, tx, requesterID, caseID, "Support case updated", supportStatusBody(input.Status), "status:"+fmt.Sprint(version+1)); err != nil {
		return Case{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Case{}, err
	}
	return s.GetAdmin(ctx, caseID)
}

const caseSelect = `
	SELECT c.id,c.requester_id,u.handle,c.category,c.subject,c.details,c.related_resource_type,c.related_resource_id,c.locale,
	 c.claimant_relationship,c.rights_statement,c.status,c.version,c.assigned_operator_id,operator.handle,c.resolution_code,c.resolution_reason,
	 c.created_at,c.updated_at,c.resolved_at
	FROM support_cases c JOIN users u ON u.id=c.requester_id LEFT JOIN users operator ON operator.id=c.assigned_operator_id `

func (s *Service) list(ctx context.Context, suffix string, args ...any) ([]Case, error) {
	rows, err := s.pool.Query(ctx, caseSelect+suffix, args...)
	if err != nil {
		return nil, fmt.Errorf("list support cases: %w", err)
	}
	defer rows.Close()
	items := make([]Case, 0)
	for rows.Next() {
		item, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) get(ctx context.Context, caseID uuid.UUID, requesterID *uuid.UUID) (Case, error) {
	query := caseSelect + `WHERE c.id=$1`
	args := []any{caseID}
	if requesterID != nil {
		query += ` AND c.requester_id=$2`
		args = append(args, *requesterID)
	}
	item, err := scanCase(s.pool.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, ErrNotFound
	}
	if err != nil {
		return Case{}, fmt.Errorf("get support case: %w", err)
	}
	messages, err := s.messages(ctx, caseID)
	if err != nil {
		return Case{}, err
	}
	events, err := s.events(ctx, caseID)
	if err != nil {
		return Case{}, err
	}
	item.Messages, item.Events = messages, events
	return item, nil
}

type scanner interface{ Scan(...any) error }

func scanCase(row scanner) (Case, error) {
	var item Case
	item.Messages, item.Events = []Message{}, []Event{}
	err := row.Scan(&item.ID, &item.RequesterID, &item.RequesterHandle, &item.Category, &item.Subject, &item.Details, &item.RelatedResourceType, &item.RelatedResourceID, &item.Locale, &item.ClaimantRelationship, &item.RightsStatement, &item.Status, &item.Version, &item.AssignedOperatorID, &item.AssignedOperatorHandle, &item.ResolutionCode, &item.ResolutionReason, &item.CreatedAt, &item.UpdatedAt, &item.ResolvedAt)
	return item, err
}

func (s *Service) messages(ctx context.Context, caseID uuid.UUID) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.id,m.author_id,u.handle,m.author_role,m.body,m.created_at FROM support_messages m LEFT JOIN users u ON u.id=m.author_id WHERE m.case_id=$1 ORDER BY m.created_at,m.id`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Message, 0)
	for rows.Next() {
		var item Message
		if err := rows.Scan(&item.ID, &item.AuthorID, &item.AuthorHandle, &item.AuthorRole, &item.Body, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) events(ctx context.Context, caseID uuid.UUID) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,actor_id,kind,from_status,to_status,reason,metadata,created_at FROM support_events WHERE case_id=$1 ORDER BY created_at,id`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Event, 0)
	for rows.Next() {
		var item Event
		var metadata []byte
		if err := rows.Scan(&item.ID, &item.ActorID, &item.Kind, &item.FromStatus, &item.ToStatus, &item.Reason, &metadata, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metadata, &item.Metadata); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) resourceVisible(ctx context.Context, userID uuid.UUID, kind string, id uuid.UUID) (bool, error) {
	queries := map[string]string{
		"work":       `SELECT EXISTS(SELECT 1 FROM works w JOIN assets a ON a.id=w.asset_id WHERE w.id=$2 AND (w.author_id=$1 OR (w.status='published' AND a.scan_status='clean')))`,
		"product":    `SELECT EXISTS(SELECT 1 FROM products WHERE id=$2 AND (seller_id=$1 OR status='active'))`,
		"post":       `SELECT EXISTS(SELECT 1 FROM posts WHERE id=$2 AND (author_id=$1 OR status='published'))`,
		"asset":      `SELECT EXISTS(SELECT 1 FROM assets WHERE id=$2 AND owner_id=$1)`,
		"generation": `SELECT EXISTS(SELECT 1 FROM generations WHERE id=$2 AND owner_id=$1)`,
		"order":      `SELECT EXISTS(SELECT 1 FROM orders WHERE id=$2 AND buyer_id=$1)`,
		"task":       `SELECT EXISTS(SELECT 1 FROM demands d WHERE d.id=$2 AND (d.client_id=$1 OR d.assignee_id=$1 OR d.status='open' OR EXISTS(SELECT 1 FROM proposals p WHERE p.demand_id=d.id AND p.creator_id=$1)))`,
	}
	query, ok := queries[kind]
	if !ok {
		return false, ErrInvalid
	}
	var visible bool
	if err := s.pool.QueryRow(ctx, query, userID, id).Scan(&visible); err != nil {
		return false, err
	}
	return visible, nil
}

func normalizeCreate(input *CreateInput) {
	input.Category = strings.TrimSpace(strings.ToLower(input.Category))
	input.Subject, input.Details = clean(input.Subject), clean(input.Details)
	input.RelatedResourceType = strings.TrimSpace(strings.ToLower(input.RelatedResourceType))
	input.Locale = strings.TrimSpace(input.Locale)
	input.ClaimantRelationship = strings.TrimSpace(strings.ToLower(input.ClaimantRelationship))
	input.RightsStatement = clean(input.RightsStatement)
}

func validateCreate(input CreateInput) error {
	if !validCategory(input.Category) || !validText(input.Subject, 4, 160) || !validText(input.Details, 20, 4000) || !oneOf(input.Locale, "en-US", "zh-CN") {
		return ErrInvalid
	}
	paired := input.RelatedResourceType != "" && input.RelatedResourceID != nil
	if paired != (input.RelatedResourceType != "" || input.RelatedResourceID != nil) {
		return ErrInvalid
	}
	if input.RelatedResourceType != "" && !oneOf(input.RelatedResourceType, "work", "product", "post", "asset", "generation", "order", "task") {
		return ErrInvalid
	}
	if input.Category == "copyright" && (!paired || !oneOf(input.RelatedResourceType, "work", "product", "post") || !oneOf(input.ClaimantRelationship, "rights_holder", "authorized_agent") || !validText(input.RightsStatement, 20, 1500)) {
		return ErrInvalid
	}
	if input.Category != "copyright" && (input.ClaimantRelationship != "" || input.RightsStatement != "") {
		return ErrInvalid
	}
	if hasSensitiveData(input.Subject) || hasSensitiveData(input.Details) || hasSensitiveData(input.RightsStatement) {
		return ErrSensitiveData
	}
	return nil
}

func clean(value string) string { return strings.TrimSpace(strings.ReplaceAll(value, "\x00", "")) }
func validText(value string, min, max int) bool {
	return len([]rune(value)) >= min && len([]rune(value)) <= max
}
func validCategory(value string) bool {
	return oneOf(value, "general_support", "billing", "account", "task_or_order", "copyright")
}
func validStatus(value string) bool {
	return oneOf(value, "open", "in_review", "waiting_for_requester", "resolved", "closed")
}
func validResolution(value string) bool {
	return oneOf(value, "answered", "fixed", "refund_guidance", "content_restricted", "no_action", "duplicate", "withdrawn")
}
func oneOf(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}
func hasSensitiveData(value string) bool {
	return longNumberPattern.MatchString(value) || governmentPattern.MatchString(value)
}
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func transitionAllowed(from, to string) bool {
	if from == to || from == "closed" {
		return false
	}
	allowed := map[string][]string{
		"open":                  {"in_review", "waiting_for_requester", "resolved", "closed"},
		"in_review":             {"waiting_for_requester", "resolved", "closed"},
		"waiting_for_requester": {"in_review", "resolved", "closed"},
		"resolved":              {"in_review", "closed"},
	}
	return oneOf(to, allowed[from]...)
}

func notifyRequester(ctx context.Context, tx pgx.Tx, requesterID, caseID uuid.UUID, title, body, suffix string) error {
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{UserID: requesterID, Kind: "support.case_updated", Title: title, Body: body, TargetPath: "/support/" + caseID.String(), ResourceType: "support_case", ResourceID: &caseID, SourceKey: "support:" + caseID.String() + ":" + suffix})
}

func supportStatusBody(status string) string {
	switch status {
	case "waiting_for_requester":
		return "Support needs more information from you."
	case "resolved":
		return "Your support case was resolved with recorded decision evidence."
	case "closed":
		return "Your support case was closed with recorded decision evidence."
	default:
		return "Your support case moved to " + strings.ReplaceAll(status, "_", " ") + "."
	}
}

func insertAudit(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, action string, caseID uuid.UUID, reason, requestID string, metadata map[string]any) error {
	if strings.TrimSpace(requestID) == "" {
		requestID = "support-service"
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,$2,'support_case',$3,$4,$5,$6)`, actorID, action, caseID, reason, requestID, body)
	return err
}
