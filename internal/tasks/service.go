package tasks

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
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/risk"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("task not found")
	ErrInvalid   = errors.New("invalid task command")
	ErrForbidden = errors.New("task command forbidden")
	ErrConflict  = errors.New("task state conflict")
)

type Person struct {
	ID          uuid.UUID `json:"id"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"displayName"`
}

type Summary struct {
	ID                uuid.UUID `json:"id"`
	Title             string    `json:"title"`
	Summary           string    `json:"summary"`
	DeliverableType   string    `json:"deliverableType"`
	BudgetCents       int       `json:"budgetCents"`
	Currency          string    `json:"currency"`
	Deadline          time.Time `json:"deadline"`
	Status            string    `json:"status"`
	ClientTimezone    string    `json:"clientTimezone"`
	AllowDirectAccept bool      `json:"allowDirectAccept"`
	ProposalCount     int       `json:"proposalCount"`
	Client            Person    `json:"client"`
	Assignee          *Person   `json:"assignee,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type Proposal struct {
	ID           uuid.UUID `json:"id"`
	Creator      Person    `json:"creator"`
	Approach     string    `json:"approach"`
	Deliverables string    `json:"deliverables"`
	AmountCents  int       `json:"amountCents"`
	TimelineDays *int      `json:"timelineDays,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Delivery struct {
	ID         uuid.UUID  `json:"id"`
	AssetID    uuid.UUID  `json:"assetId"`
	AssetTitle string     `json:"assetTitle"`
	MediaURL   string     `json:"mediaUrl"`
	Note       string     `json:"note"`
	Status     string     `json:"status"`
	Version    int        `json:"version"`
	ReviewNote string     `json:"reviewNote"`
	CreatedAt  time.Time  `json:"createdAt"`
	ReviewedAt *time.Time `json:"reviewedAt,omitempty"`
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
}

type Event struct {
	ID         uuid.UUID       `json:"id"`
	Actor      *Person         `json:"actor,omitempty"`
	Kind       string          `json:"kind"`
	FromStatus *string         `json:"fromStatus,omitempty"`
	ToStatus   string          `json:"toStatus"`
	Note       string          `json:"note"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"createdAt"`
}

type Settlement struct {
	ID          uuid.UUID `json:"id"`
	AmountCents int       `json:"amountCents"`
	Currency    string    `json:"currency"`
	Mode        string    `json:"mode"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Detail struct {
	Summary
	Brief                   string      `json:"brief"`
	Deliverables            []string    `json:"deliverables"`
	AcceptanceRules         []string    `json:"acceptanceRules"`
	RightsTerms             string      `json:"rightsTerms"`
	AIDisclosureRequirement string      `json:"aiDisclosureRequirement"`
	ViewerRole              string      `json:"viewerRole"`
	Proposals               []Proposal  `json:"proposals"`
	Deliveries              []Delivery  `json:"deliveries"`
	Events                  []Event     `json:"events"`
	Settlement              *Settlement `json:"settlement,omitempty"`
}

type ListFilter struct {
	Query           string
	DeliverableType string
	Status          string
	Sort            string
	Mine            bool
	Limit           int
}

type CreateInput struct {
	Title                   string    `json:"title"`
	Summary                 string    `json:"summary"`
	Brief                   string    `json:"brief"`
	DeliverableType         string    `json:"deliverableType"`
	Deliverables            []string  `json:"deliverables"`
	AcceptanceRules         []string  `json:"acceptanceRules"`
	RightsTerms             string    `json:"rightsTerms"`
	AIDisclosureRequirement string    `json:"aiDisclosureRequirement"`
	BudgetCents             int       `json:"budgetCents"`
	Currency                string    `json:"currency"`
	Deadline                time.Time `json:"deadline"`
	ClientTimezone          string    `json:"clientTimezone"`
	AllowDirectAccept       bool      `json:"allowDirectAccept"`
}

type ProposeInput struct {
	Approach     string `json:"approach"`
	Deliverables string `json:"deliverables"`
	AmountCents  int    `json:"amountCents"`
	TimelineDays int    `json:"timelineDays"`
}

type DeliverInput struct {
	AssetID uuid.UUID `json:"assetId"`
	Note    string    `json:"note"`
}

type ReviewInput struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, actorID uuid.UUID, filter ListFilter) ([]Summary, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	filter.DeliverableType = strings.TrimSpace(strings.ToLower(filter.DeliverableType))
	filter.Status = strings.TrimSpace(strings.ToLower(filter.Status))
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 40
	}
	order := "d.created_at DESC"
	switch filter.Sort {
	case "deadline":
		order = "d.deadline ASC"
	case "budget_desc":
		order = "d.budget_cents DESC, d.created_at DESC"
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT d.id,d.title,d.summary,d.deliverable_type,d.budget_cents,d.currency,d.deadline,d.status,
		       d.client_timezone,d.allow_direct_accept,COUNT(p.id),c.id,c.handle,c.display_name,
		       a.id,a.handle,a.display_name,d.created_at,d.updated_at
		FROM demands d
		JOIN users c ON c.id=d.client_id
		LEFT JOIN users a ON a.id=d.assignee_id
		LEFT JOIN proposals p ON p.demand_id=d.id
		WHERE ($1='' OR d.title ILIKE '%%'||$1||'%%' OR d.summary ILIKE '%%'||$1||'%%' OR d.brief ILIKE '%%'||$1||'%%')
		  AND ($2='' OR d.deliverable_type=$2)
		  AND ($3='' OR d.status=$3)
		  AND (NOT $4 OR d.client_id=$5 OR d.assignee_id=$5 OR EXISTS(SELECT 1 FROM proposals mine WHERE mine.demand_id=d.id AND mine.creator_id=$5))
		GROUP BY d.id,c.id,a.id
		ORDER BY %s LIMIT $6`, order), filter.Query, filter.DeliverableType, filter.Status, filter.Mine, actorID, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	items := make([]Summary, 0)
	for rows.Next() {
		item, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) Get(ctx context.Context, actorID, demandID uuid.UUID) (Detail, error) {
	var item Detail
	var deliverablesJSON, rulesJSON []byte
	row := s.pool.QueryRow(ctx, `
		SELECT d.id,d.title,d.summary,d.deliverable_type,d.budget_cents,d.currency,d.deadline,d.status,
		       d.client_timezone,d.allow_direct_accept,(SELECT COUNT(*) FROM proposals p WHERE p.demand_id=d.id),
		       c.id,c.handle,c.display_name,a.id,a.handle,a.display_name,d.created_at,d.updated_at,
		       d.brief,d.deliverables,d.acceptance_rules,d.rights_terms,d.ai_disclosure_requirement,d.client_id,d.assignee_id
		FROM demands d JOIN users c ON c.id=d.client_id LEFT JOIN users a ON a.id=d.assignee_id WHERE d.id=$1`, demandID)
	var assigneeID *uuid.UUID
	var assigneeHandle, assigneeName *string
	var clientID uuid.UUID
	var assignedID *uuid.UUID
	err := row.Scan(&item.ID, &item.Title, &item.Summary.Summary, &item.DeliverableType, &item.BudgetCents, &item.Currency,
		&item.Deadline, &item.Status, &item.ClientTimezone, &item.AllowDirectAccept, &item.ProposalCount,
		&item.Client.ID, &item.Client.Handle, &item.Client.DisplayName, &assigneeID, &assigneeHandle, &assigneeName,
		&item.CreatedAt, &item.UpdatedAt, &item.Brief, &deliverablesJSON, &rulesJSON, &item.RightsTerms,
		&item.AIDisclosureRequirement, &clientID, &assignedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, fmt.Errorf("get task: %w", err)
	}
	if assigneeID != nil {
		item.Assignee = &Person{ID: *assigneeID, Handle: value(assigneeHandle), DisplayName: value(assigneeName)}
	}
	_ = json.Unmarshal(deliverablesJSON, &item.Deliverables)
	_ = json.Unmarshal(rulesJSON, &item.AcceptanceRules)
	item.ViewerRole = "viewer"
	if actorID == clientID {
		item.ViewerRole = "client"
	} else if assignedID != nil && actorID == *assignedID {
		item.ViewerRole = "assignee"
	}
	proposals, err := s.listProposals(ctx, demandID, actorID, clientID)
	if err != nil {
		return Detail{}, err
	}
	item.Proposals = proposals
	deliveries, err := s.listDeliveries(ctx, demandID)
	if err != nil {
		return Detail{}, err
	}
	item.Deliveries = deliveries
	events, err := s.listEvents(ctx, demandID)
	if err != nil {
		return Detail{}, err
	}
	item.Events = events
	var settlement Settlement
	err = s.pool.QueryRow(ctx, `SELECT id,amount_cents,currency,mode,created_at FROM task_settlements WHERE demand_id=$1`, demandID).Scan(
		&settlement.ID, &settlement.AmountCents, &settlement.Currency, &settlement.Mode, &settlement.CreatedAt)
	if err == nil {
		item.Settlement = &settlement
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, fmt.Errorf("get task settlement: %w", err)
	}
	return item, nil
}

func (s *Service) Create(ctx context.Context, actorID uuid.UUID, input CreateInput, key string) (Detail, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Brief = strings.TrimSpace(input.Brief)
	input.DeliverableType = strings.ToLower(strings.TrimSpace(input.DeliverableType))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.ClientTimezone = strings.TrimSpace(input.ClientTimezone)
	input.RightsTerms = strings.TrimSpace(input.RightsTerms)
	input.AIDisclosureRequirement = strings.TrimSpace(input.AIDisclosureRequirement)
	if len(input.Title) < 5 || len(input.Summary) < 10 || len(input.Brief) < 30 || input.BudgetCents <= 0 || input.Currency != "USD" || !input.Deadline.After(time.Now()) || len(input.Deliverables) == 0 || len(input.AcceptanceRules) == 0 || input.RightsTerms == "" || input.AIDisclosureRequirement == "" || !validDeliverable(input.DeliverableType) {
		return Detail{}, ErrInvalid
	}
	if input.ClientTimezone == "" {
		input.ClientTimezone = "UTC"
	}
	if strings.TrimSpace(key) == "" || len(strings.TrimSpace(key)) < 8 {
		return Detail{}, ErrInvalid
	}
	if replayID, replay, err := findCommand(ctx, s.pool, actorID, "create", key, input); err != nil {
		return Detail{}, err
	} else if replay {
		return s.Get(ctx, actorID, replayID)
	}
	demandID := uuid.New()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.TaskCreation); err != nil {
		return Detail{}, err
	}
	deliverables, _ := json.Marshal(cleanStrings(input.Deliverables))
	rules, _ := json.Marshal(cleanStrings(input.AcceptanceRules))
	_, err = tx.Exec(ctx, `
		INSERT INTO demands(id,client_id,title,summary,brief,deliverable_type,deliverables,acceptance_rules,rights_terms,
		 ai_disclosure_requirement,budget_cents,currency,deadline,status,client_timezone,allow_direct_accept,idempotency_key)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'open',$14,$15,$16)`, demandID, actorID, input.Title,
		input.Summary, input.Brief, input.DeliverableType, deliverables, rules, input.RightsTerms, input.AIDisclosureRequirement,
		input.BudgetCents, input.Currency, input.Deadline, input.ClientTimezone, input.AllowDirectAccept, key)
	if err != nil {
		return Detail{}, fmt.Errorf("create task: %w", err)
	}
	if _, _, err := registerCommand(ctx, tx, actorID, demandID, "create", key, input); err != nil {
		return Detail{}, err
	}
	if err := insertEvent(ctx, tx, demandID, actorID, "created", nil, "open", "", nil); err != nil {
		return Detail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, actorID, demandID)
}

func (s *Service) Propose(ctx context.Context, actorID, demandID uuid.UUID, input ProposeInput, key string) (Detail, error) {
	input.Approach = strings.TrimSpace(input.Approach)
	input.Deliverables = strings.TrimSpace(input.Deliverables)
	if len(input.Approach) < 20 || len(input.Deliverables) < 10 || input.AmountCents <= 0 || input.TimelineDays <= 0 {
		return Detail{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	replayID, replay, err := registerCommand(ctx, tx, actorID, demandID, "propose", key, input)
	if err != nil {
		return Detail{}, err
	}
	if replay {
		_ = tx.Rollback(ctx)
		return s.Get(ctx, actorID, replayID)
	}
	var clientID uuid.UUID
	var status, taskTitle string
	if err := tx.QueryRow(ctx, `SELECT client_id,status,title FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&clientID, &status, &taskTitle); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if clientID == actorID {
		return Detail{}, ErrForbidden
	}
	if status != "open" {
		return Detail{}, ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO proposals(demand_id,creator_id,approach,deliverables,amount_cents,timeline_days,status,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,'submitted',$7)`, demandID, actorID, input.Approach, input.Deliverables, input.AmountCents, input.TimelineDays, key)
	if err != nil {
		if strings.Contains(err.Error(), "proposals_demand_id_creator_id_key") {
			return Detail{}, ErrConflict
		}
		return Detail{}, fmt.Errorf("create proposal: %w", err)
	}
	if err = insertEvent(ctx, tx, demandID, actorID, "proposal_submitted", stringPtr("open"), "open", "", nil); err != nil {
		return Detail{}, err
	}
	if err = notifyTask(ctx, tx, clientID, "task.proposal_submitted", "New proposal received", "A creator submitted a proposal for “"+taskTitle+"”.", demandID, "proposal:"+actorID.String()); err != nil {
		return Detail{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, actorID, demandID)
}

func (s *Service) Claim(ctx context.Context, actorID, demandID uuid.UUID, key string) (Detail, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	replayID, replay, err := registerCommand(ctx, tx, actorID, demandID, "claim", key, map[string]string{"action": "claim"})
	if err != nil {
		return Detail{}, err
	}
	if replay {
		_ = tx.Rollback(ctx)
		return s.Get(ctx, actorID, replayID)
	}
	var clientID uuid.UUID
	var status, taskTitle string
	var allow bool
	var budget int
	if err = tx.QueryRow(ctx, `SELECT client_id,status,allow_direct_accept,budget_cents,title FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&clientID, &status, &allow, &budget, &taskTitle); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if clientID == actorID {
		return Detail{}, ErrForbidden
	}
	if status != "open" || !allow {
		return Detail{}, ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO proposals(demand_id,creator_id,approach,deliverables,amount_cents,timeline_days,status,idempotency_key) VALUES($1,$2,'Direct acceptance','Deliver according to the published brief',$3,1,'accepted',$4)`, demandID, actorID, budget, key)
	if err != nil {
		return Detail{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE demands SET status='assigned',assignee_id=$2,updated_at=now() WHERE id=$1`, demandID, actorID)
	if err != nil {
		return Detail{}, err
	}
	if err = insertEvent(ctx, tx, demandID, actorID, "directly_accepted", stringPtr("open"), "assigned", "", nil); err != nil {
		return Detail{}, err
	}
	if err = notifyTask(ctx, tx, clientID, "task.directly_accepted", "Brief accepted", "A creator directly accepted “"+taskTitle+"”.", demandID, "direct-accept:"+actorID.String()); err != nil {
		return Detail{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, actorID, demandID)
}

func (s *Service) AcceptProposal(ctx context.Context, actorID, demandID, proposalID uuid.UUID, key string) (Detail, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	replayID, replay, err := registerCommand(ctx, tx, actorID, demandID, "accept_proposal", key, map[string]string{"proposalId": proposalID.String()})
	if err != nil {
		return Detail{}, err
	}
	if replay {
		_ = tx.Rollback(ctx)
		return s.Get(ctx, actorID, replayID)
	}
	var clientID uuid.UUID
	var status, taskTitle string
	if err = tx.QueryRow(ctx, `SELECT client_id,status,title FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&clientID, &status, &taskTitle); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if clientID != actorID {
		return Detail{}, ErrForbidden
	}
	if status != "open" {
		return Detail{}, ErrConflict
	}
	var creatorID uuid.UUID
	var proposalStatus string
	if err = tx.QueryRow(ctx, `SELECT creator_id,status FROM proposals WHERE id=$1 AND demand_id=$2 FOR UPDATE`, proposalID, demandID).Scan(&creatorID, &proposalStatus); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if proposalStatus != "submitted" {
		return Detail{}, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE proposals SET status=CASE WHEN id=$2 THEN 'accepted' ELSE 'rejected' END,updated_at=now() WHERE demand_id=$1 AND status='submitted'`, demandID, proposalID); err != nil {
		return Detail{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE demands SET status='assigned',assignee_id=$2,updated_at=now() WHERE id=$1`, demandID, creatorID); err != nil {
		return Detail{}, err
	}
	if err = insertEvent(ctx, tx, demandID, actorID, "proposal_accepted", stringPtr("open"), "assigned", "", map[string]string{"proposalId": proposalID.String()}); err != nil {
		return Detail{}, err
	}
	if err = notifyTask(ctx, tx, creatorID, "task.proposal_accepted", "Proposal accepted", "Your proposal for “"+taskTitle+"” was accepted. The brief is ready in your task workspace.", demandID, "proposal-accepted:"+proposalID.String()); err != nil {
		return Detail{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, actorID, demandID)
}

func (s *Service) Deliver(ctx context.Context, actorID, demandID uuid.UUID, input DeliverInput, key string) (Detail, error) {
	input.Note = strings.TrimSpace(input.Note)
	if input.AssetID == uuid.Nil || len(input.Note) < 5 {
		return Detail{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	replayID, replay, err := registerCommand(ctx, tx, actorID, demandID, "deliver", key, input)
	if err != nil {
		return Detail{}, err
	}
	if replay {
		_ = tx.Rollback(ctx)
		return s.Get(ctx, actorID, replayID)
	}
	var assigneeID *uuid.UUID
	var clientID uuid.UUID
	var status, taskTitle string
	if err = tx.QueryRow(ctx, `SELECT assignee_id,client_id,status,title FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&assigneeID, &clientID, &status, &taskTitle); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if assigneeID == nil || *assigneeID != actorID {
		return Detail{}, ErrForbidden
	}
	if status != "assigned" && status != "revision" {
		return Detail{}, ErrConflict
	}
	var assetOK bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE id=$1 AND owner_id=$2 AND scan_status='clean')`, input.AssetID, actorID).Scan(&assetOK); err != nil {
		return Detail{}, err
	}
	if !assetOK {
		return Detail{}, ErrForbidden
	}
	var version int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM deliveries WHERE demand_id=$1`, demandID).Scan(&version); err != nil {
		return Detail{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO deliveries(demand_id,creator_id,asset_id,note,status,version,idempotency_key) VALUES($1,$2,$3,$4,'submitted',$5,$6)`, demandID, actorID, input.AssetID, input.Note, version, key); err != nil {
		return Detail{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE demands SET status='submitted',updated_at=now() WHERE id=$1`, demandID); err != nil {
		return Detail{}, err
	}
	if err = insertEvent(ctx, tx, demandID, actorID, "delivery_submitted", stringPtr(status), "submitted", input.Note, map[string]any{"version": version, "assetId": input.AssetID}); err != nil {
		return Detail{}, err
	}
	if err = notifyTask(ctx, tx, clientID, "task.delivery_submitted", "Delivery ready for review", "Version "+fmt.Sprint(version)+" of “"+taskTitle+"” is ready for review.", demandID, fmt.Sprintf("delivery:%d", version)); err != nil {
		return Detail{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, actorID, demandID)
}

func (s *Service) Review(ctx context.Context, actorID, demandID uuid.UUID, input ReviewInput, key string) (Detail, error) {
	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.Note = strings.TrimSpace(input.Note)
	if input.Decision != "accept" && input.Decision != "request_revision" {
		return Detail{}, ErrInvalid
	}
	if input.Decision == "request_revision" && len(input.Note) < 10 {
		return Detail{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	replayID, replay, err := registerCommand(ctx, tx, actorID, demandID, "review", key, input)
	if err != nil {
		return Detail{}, err
	}
	if replay {
		_ = tx.Rollback(ctx)
		return s.Get(ctx, actorID, replayID)
	}
	var clientID uuid.UUID
	var assigneeID *uuid.UUID
	var status, currency, taskTitle string
	var budget int
	if err = tx.QueryRow(ctx, `SELECT d.client_id,d.assignee_id,d.status,COALESCE((SELECT p.amount_cents FROM proposals p WHERE p.demand_id=d.id AND p.status='accepted' LIMIT 1),d.budget_cents),d.currency,d.title FROM demands d WHERE d.id=$1 FOR UPDATE`, demandID).Scan(&clientID, &assigneeID, &status, &budget, &currency, &taskTitle); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if clientID != actorID {
		return Detail{}, ErrForbidden
	}
	if status != "submitted" || assigneeID == nil {
		return Detail{}, ErrConflict
	}
	var deliveryID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM deliveries WHERE demand_id=$1 AND status='submitted' ORDER BY version DESC LIMIT 1 FOR UPDATE`, demandID).Scan(&deliveryID); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrConflict
	} else if err != nil {
		return Detail{}, err
	}
	if input.Decision == "request_revision" {
		if _, err = tx.Exec(ctx, `UPDATE deliveries SET status='revision',review_note=$2,reviewed_at=now(),updated_at=now() WHERE id=$1`, deliveryID, input.Note); err != nil {
			return Detail{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE demands SET status='revision',updated_at=now() WHERE id=$1`, demandID); err != nil {
			return Detail{}, err
		}
		if err = insertEvent(ctx, tx, demandID, actorID, "revision_requested", stringPtr("submitted"), "revision", input.Note, nil); err != nil {
			return Detail{}, err
		}
		if err = notifyTask(ctx, tx, *assigneeID, "task.revision_requested", "Revision requested", "The commissioner requested a revision for “"+taskTitle+"”.", demandID, "revision:"+deliveryID.String()); err != nil {
			return Detail{}, err
		}
	} else {
		settlementID := uuid.New()
		if _, err = tx.Exec(ctx, `UPDATE deliveries SET status='accepted',review_note=$2,reviewed_at=now(),accepted_at=now(),updated_at=now() WHERE id=$1`, deliveryID, input.Note); err != nil {
			return Detail{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE demands SET status='accepted',accepted_at=now(),updated_at=now() WHERE id=$1`, demandID); err != nil {
			return Detail{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO task_settlements(id,demand_id,client_id,creator_id,amount_cents,currency,mode) VALUES($1,$2,$3,$4,$5,$6,'local_test')`, settlementID, demandID, clientID, *assigneeID, budget, currency); err != nil {
			return Detail{}, err
		}
		if err = billing.TransferTx(ctx, tx, clientID, *assigneeID, settlementID, budget, currency,
			"task_payment", "task_earning", "Local Test task settlement"); err != nil {
			return Detail{}, fmt.Errorf("apply task billing transfer: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO ledger_entries(account_id,operation_id,direction,amount_cents,currency,reason) VALUES($1,$3,'debit',$4,$5,'task_local_test_settlement'),($2,$3,'credit',$4,$5,'task_local_test_settlement')`, clientID, *assigneeID, settlementID, budget, currency); err != nil {
			return Detail{}, err
		}
		if err = insertEvent(ctx, tx, demandID, actorID, "delivery_accepted", stringPtr("submitted"), "accepted", input.Note, map[string]string{"settlementMode": "local_test"}); err != nil {
			return Detail{}, err
		}
		if err = notifyTask(ctx, tx, *assigneeID, "task.delivery_accepted", "Delivery accepted", "Your delivery for “"+taskTitle+"” was accepted and the Local Test USD settlement was recorded.", demandID, "accepted:"+deliveryID.String()); err != nil {
			return Detail{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, actorID, demandID)
}

func (s *Service) OpenDispute(ctx context.Context, actorID, demandID uuid.UUID, reason, key string) (Detail, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 20 {
		return Detail{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	replayID, replay, err := registerCommand(ctx, tx, actorID, demandID, "dispute", key, map[string]string{"reason": reason})
	if err != nil {
		return Detail{}, err
	}
	if replay {
		_ = tx.Rollback(ctx)
		return s.Get(ctx, actorID, replayID)
	}
	var clientID uuid.UUID
	var assigneeID *uuid.UUID
	var status, taskTitle string
	if err = tx.QueryRow(ctx, `SELECT client_id,assignee_id,status,title FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&clientID, &assigneeID, &status, &taskTitle); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if actorID != clientID && (assigneeID == nil || actorID != *assigneeID) {
		return Detail{}, ErrForbidden
	}
	if status != "submitted" && status != "revision" {
		return Detail{}, ErrConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO task_disputes(demand_id,opened_by,reason,idempotency_key) VALUES($1,$2,$3,$4)`, demandID, actorID, reason, key); err != nil {
		return Detail{}, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE deliveries SET status='disputed',updated_at=now() WHERE demand_id=$1 AND status IN ('submitted','revision')`, demandID); err != nil {
		return Detail{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE demands SET status='disputed',updated_at=now() WHERE id=$1`, demandID); err != nil {
		return Detail{}, err
	}
	if err = insertEvent(ctx, tx, demandID, actorID, "dispute_opened", stringPtr(status), "disputed", reason, nil); err != nil {
		return Detail{}, err
	}
	if _, err = risk.RecordTx(ctx, tx, risk.SignalInput{
		SourceKey: "task_dispute:" + demandID.String(), ResourceType: "task", ResourceID: demandID,
		SubjectUserID: actorID, ActorUserID: &actorID, SignalType: "task_dispute", Severity: "high", Score: 85,
		Summary:  "Task dispute requires operational review.",
		Evidence: map[string]any{"taskStatus": "disputed", "previousStatus": status},
	}); err != nil {
		return Detail{}, err
	}
	recipientID := clientID
	if actorID == clientID && assigneeID != nil {
		recipientID = *assigneeID
	}
	if err = notifyTask(ctx, tx, recipientID, "task.dispute_opened", "Task dispute opened", "A dispute was opened for “"+taskTitle+"”. Settlement remains paused.", demandID, "dispute"); err != nil {
		return Detail{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, actorID, demandID)
}

func (s *Service) Cancel(ctx context.Context, actorID, demandID uuid.UUID, reason, key string) (Detail, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 10 {
		return Detail{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	replayID, replay, err := registerCommand(ctx, tx, actorID, demandID, "cancel", key, map[string]string{"reason": reason})
	if err != nil {
		return Detail{}, err
	}
	if replay {
		_ = tx.Rollback(ctx)
		return s.Get(ctx, actorID, replayID)
	}
	var clientID uuid.UUID
	var status, taskTitle string
	if err = tx.QueryRow(ctx, `SELECT client_id,status,title FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&clientID, &status, &taskTitle); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if actorID != clientID {
		return Detail{}, ErrForbidden
	}
	if status != "open" {
		return Detail{}, ErrConflict
	}
	rows, err := tx.Query(ctx, `SELECT creator_id FROM proposals WHERE demand_id=$1 AND status='submitted'`, demandID)
	if err != nil {
		return Detail{}, err
	}
	proposerIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var proposerID uuid.UUID
		if err = rows.Scan(&proposerID); err != nil {
			rows.Close()
			return Detail{}, err
		}
		proposerIDs = append(proposerIDs, proposerID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Detail{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE demands SET status='cancelled',cancelled_at=now(),updated_at=now() WHERE id=$1`, demandID); err != nil {
		return Detail{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE proposals SET status='rejected',updated_at=now() WHERE demand_id=$1 AND status='submitted'`, demandID); err != nil {
		return Detail{}, err
	}
	if err = insertEvent(ctx, tx, demandID, actorID, "task_cancelled", stringPtr("open"), "cancelled", reason, nil); err != nil {
		return Detail{}, err
	}
	for _, proposerID := range proposerIDs {
		if err = notifyTask(ctx, tx, proposerID, "task.cancelled", "Brief cancelled", "The commissioner cancelled “"+taskTitle+"”. Your proposal is now closed.", demandID, "cancelled:"+proposerID.String()); err != nil {
			return Detail{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, actorID, demandID)
}

func (s *Service) listProposals(ctx context.Context, demandID, actorID, clientID uuid.UUID) ([]Proposal, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id,u.id,u.handle,u.display_name,p.approach,p.deliverables,p.amount_cents,p.timeline_days,p.status,p.created_at,p.updated_at FROM proposals p JOIN users u ON u.id=p.creator_id WHERE p.demand_id=$1 AND ($2::uuid=$3::uuid OR p.creator_id=$2::uuid) ORDER BY p.created_at`, demandID, actorID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Proposal, 0)
	for rows.Next() {
		var p Proposal
		if err = rows.Scan(&p.ID, &p.Creator.ID, &p.Creator.Handle, &p.Creator.DisplayName, &p.Approach, &p.Deliverables, &p.AmountCents, &p.TimelineDays, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (s *Service) listDeliveries(ctx context.Context, demandID uuid.UUID) ([]Delivery, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.id,d.asset_id,a.title,a.media_url,d.note,d.status,d.version,d.review_note,d.created_at,d.reviewed_at,d.accepted_at FROM deliveries d JOIN assets a ON a.id=d.asset_id WHERE d.demand_id=$1 ORDER BY d.version DESC`, demandID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Delivery, 0)
	for rows.Next() {
		var d Delivery
		if err = rows.Scan(&d.ID, &d.AssetID, &d.AssetTitle, &d.MediaURL, &d.Note, &d.Status, &d.Version, &d.ReviewNote, &d.CreatedAt, &d.ReviewedAt, &d.AcceptedAt); err != nil {
			return nil, err
		}
		items = append(items, d)
	}
	return items, rows.Err()
}

func (s *Service) listEvents(ctx context.Context, demandID uuid.UUID) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id,u.id,u.handle,u.display_name,e.kind,e.from_status,e.to_status,e.note,e.metadata,e.created_at FROM task_events e LEFT JOIN users u ON u.id=e.actor_id WHERE e.demand_id=$1 ORDER BY e.created_at,e.id`, demandID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Event, 0)
	for rows.Next() {
		var e Event
		var actorID *uuid.UUID
		var handle, name *string
		if err = rows.Scan(&e.ID, &actorID, &handle, &name, &e.Kind, &e.FromStatus, &e.ToStatus, &e.Note, &e.Metadata, &e.CreatedAt); err != nil {
			return nil, err
		}
		if actorID != nil {
			e.Actor = &Person{ID: *actorID, Handle: value(handle), DisplayName: value(name)}
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

type summaryScanner interface{ Scan(...any) error }

func scanSummary(row summaryScanner) (Summary, error) {
	var item Summary
	var aid *uuid.UUID
	var ah, an *string
	err := row.Scan(&item.ID, &item.Title, &item.Summary, &item.DeliverableType, &item.BudgetCents, &item.Currency, &item.Deadline, &item.Status, &item.ClientTimezone, &item.AllowDirectAccept, &item.ProposalCount, &item.Client.ID, &item.Client.Handle, &item.Client.DisplayName, &aid, &ah, &an, &item.CreatedAt, &item.UpdatedAt)
	if aid != nil {
		item.Assignee = &Person{ID: *aid, Handle: value(ah), DisplayName: value(an)}
	}
	return item, err
}

func registerCommand(ctx context.Context, tx pgx.Tx, actorID, demandID uuid.UUID, operation, key string, payload any) (uuid.UUID, bool, error) {
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 200 {
		return uuid.Nil, false, ErrInvalid
	}
	hash := commandHash(payload)
	tag, err := tx.Exec(ctx, `INSERT INTO task_commands(actor_id,demand_id,operation,idempotency_key,request_hash) VALUES($1,$2,$3,$4,$5) ON CONFLICT(actor_id,operation,idempotency_key) DO NOTHING`, actorID, demandID, operation, key, hash)
	if err != nil {
		return uuid.Nil, false, err
	}
	if tag.RowsAffected() == 1 {
		return demandID, false, nil
	}
	var existingID uuid.UUID
	var existingHash string
	if err = tx.QueryRow(ctx, `SELECT demand_id,request_hash FROM task_commands WHERE actor_id=$1 AND operation=$2 AND idempotency_key=$3`, actorID, operation, key).Scan(&existingID, &existingHash); err != nil {
		return uuid.Nil, false, err
	}
	if existingHash != hash {
		return uuid.Nil, false, ErrConflict
	}
	return existingID, true, nil
}

type commandQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func findCommand(ctx context.Context, query commandQuerier, actorID uuid.UUID, operation, key string, payload any) (uuid.UUID, bool, error) {
	var demandID uuid.UUID
	var existingHash string
	err := query.QueryRow(ctx, `SELECT demand_id,request_hash FROM task_commands WHERE actor_id=$1 AND operation=$2 AND idempotency_key=$3`, actorID, operation, strings.TrimSpace(key)).Scan(&demandID, &existingHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if existingHash != commandHash(payload) {
		return uuid.Nil, false, ErrConflict
	}
	return demandID, true, nil
}

func commandHash(payload any) string {
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func insertEvent(ctx context.Context, tx pgx.Tx, demandID, actorID uuid.UUID, kind string, from *string, to, note string, metadata any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	raw, _ := json.Marshal(metadata)
	_, err := tx.Exec(ctx, `INSERT INTO task_events(demand_id,actor_id,kind,from_status,to_status,note,metadata) VALUES($1,$2,$3,$4,$5,$6,$7)`, demandID, actorID, kind, from, to, note, raw)
	return err
}

func notifyTask(ctx context.Context, tx pgx.Tx, recipientID uuid.UUID, kind, title, body string, demandID uuid.UUID, sourceSuffix string) error {
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID:       recipientID,
		Kind:         kind,
		Title:        title,
		Body:         body,
		TargetPath:   "/market/demands/" + demandID.String(),
		ResourceType: "task",
		ResourceID:   &demandID,
		SourceKey:    "task:" + demandID.String() + ":" + sourceSuffix,
	})
}
func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
func validDeliverable(v string) bool {
	switch v {
	case "image", "video", "audio", "prompt", "workflow", "mixed":
		return true
	}
	return false
}
func stringPtr(v string) *string { return &v }
func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
