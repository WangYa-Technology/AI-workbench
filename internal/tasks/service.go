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
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/risk"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/hcai-chat/hcai-chat/internal/taskdelivery"
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

type DeliveryAsset struct {
	ID       uuid.UUID `json:"id"`
	Title    string    `json:"title"`
	MediaURL string    `json:"mediaUrl"`
	Kind     string    `json:"kind"`
}

type Delivery struct {
	Assets         []DeliveryAsset `json:"assets"`
	RightsEvidence string          `json:"rightsEvidence"`
	AIDisclosure   string          `json:"aiDisclosure"`

	Creator    Person     `json:"creator"`
	ID         uuid.UUID  `json:"id"`
	AssetID    uuid.UUID  `json:"assetId"`
	AssetTitle string     `json:"assetTitle"`
	MediaURL   string     `json:"mediaUrl"`
	MediaKind  string     `json:"mediaKind"`
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

type Funding struct {
	Status            string     `json:"status"`
	AmountCents       int        `json:"amountCents"`
	Currency          string     `json:"currency"`
	PaymentMode       string     `json:"paymentMode"`
	LiveMode          bool       `json:"liveMode"`
	ProposalID        *uuid.UUID `json:"proposalId,omitempty"`
	CheckoutURL       *string    `json:"checkoutUrl,omitempty"`
	CheckoutExpiresAt *time.Time `json:"checkoutExpiresAt,omitempty"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type Detail struct {
	DeadlineChange       *DeadlineChange `json:"deadlineChange,omitempty"`
	AllowDerivativeReuse bool            `json:"allowDerivativeReuse"`
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
	Funding                 *Funding    `json:"funding,omitempty"`
	Settlement              *Settlement `json:"settlement,omitempty"`
}

type ListFilter struct {
	Cursor          string
	Query           string
	DeliverableType string
	Status          string
	Sort            string
	Mine            bool
	Limit           int
}

type CreateInput struct {
	AllowDerivativeReuse    bool      `json:"allowDerivativeReuse"`
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
	AssetIDs        []uuid.UUID `json:"assetIds"`
	RightsEvidence  string      `json:"rightsEvidence"`
	AIDisclosure    string      `json:"aiDisclosure"`
	RightsConfirmed bool        `json:"rightsConfirmed"`

	AssetID uuid.UUID `json:"assetId"`
	Note    string    `json:"note"`
}

type ReviewInput struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

type Service struct {
	pool             *pgxpool.Pool
	providerPayments bool
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func NewServiceWithPayments(pool *pgxpool.Pool, providerPayments bool) *Service {
	return &Service{pool: pool, providerPayments: providerPayments}
}

func (s *Service) List(ctx context.Context, actorID uuid.UUID, filter ListFilter) ([]Summary, error) {
	page, err := s.ListPage(ctx, actorID, filter)
	return page.Items, err
}

func (s *Service) Get(ctx context.Context, actorID, demandID uuid.UUID) (Detail, error) {
	var operator bool
	if actorID != uuid.Nil {
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN role_permissions rp ON rp.role=u.role WHERE u.id=$1 AND u.status='active' AND rp.permission_id='admin:tasks')`, actorID).Scan(&operator); err != nil {
			return Detail{}, err
		}
	}
	var item Detail
	var deliverablesJSON, rulesJSON []byte
	row := s.pool.QueryRow(ctx, `
		SELECT d.id,d.title,d.summary,d.deliverable_type,d.budget_cents,d.currency,d.deadline,d.status,
		       d.client_timezone,d.allow_direct_accept,(SELECT COUNT(*) FROM proposals p WHERE p.demand_id=d.id),
		       c.id,c.handle,c.display_name,a.id,a.handle,a.display_name,d.created_at,d.updated_at,
		       d.brief,d.deliverables,d.acceptance_rules,d.rights_terms,d.ai_disclosure_requirement,d.client_id,d.assignee_id,d.allow_derivative_reuse
		FROM demands d JOIN users c ON c.id=d.client_id LEFT JOIN users a ON a.id=d.assignee_id WHERE d.id=$1
		AND ($3 OR c.status='active' OR d.client_id=$2 OR d.assignee_id=$2 OR EXISTS(SELECT 1 FROM proposals mine WHERE mine.demand_id=d.id AND mine.creator_id=$2))`, demandID, actorID, operator)
	var assigneeID *uuid.UUID
	var assigneeHandle, assigneeName *string
	var clientID uuid.UUID
	var assignedID *uuid.UUID
	err := row.Scan(&item.ID, &item.Title, &item.Summary.Summary, &item.DeliverableType, &item.BudgetCents, &item.Currency,
		&item.Deadline, &item.Status, &item.ClientTimezone, &item.AllowDirectAccept, &item.ProposalCount,
		&item.Client.ID, &item.Client.Handle, &item.Client.DisplayName, &assigneeID, &assigneeHandle, &assigneeName,
		&item.CreatedAt, &item.UpdatedAt, &item.Brief, &deliverablesJSON, &rulesJSON, &item.RightsTerms,
		&item.AIDisclosureRequirement, &clientID, &assignedID, &item.AllowDerivativeReuse)
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
	proposalViewer := actorID
	if operator && item.ViewerRole == "viewer" {
		item.ViewerRole = "operator"
		proposalViewer = clientID
	}
	proposals, err := s.listProposals(ctx, demandID, proposalViewer, clientID)
	if err != nil {
		return Detail{}, err
	}
	item.Proposals = proposals
	item.Deliveries = []Delivery{}
	item.Events = []Event{}
	if item.ViewerRole != "viewer" {
		item.Deliveries, err = s.listDeliveries(ctx, demandID)
		if err != nil {
			return Detail{}, err
		}
		item.Events, err = s.listEvents(ctx, demandID)
		if err != nil {
			return Detail{}, err
		}
	}
	var funding Funding
	err = s.pool.QueryRow(ctx, `
		SELECT provider,status,amount_cents,currency,live_mode,proposal_id,
		       CASE WHEN payer_id=$2 AND status='checkout_open' THEN checkout_url END,
		       CASE WHEN payer_id=$2 AND status='checkout_open' THEN checkout_expires_at END,
		       updated_at
		FROM payment_intents
		WHERE purpose='task' AND resource_id=$1
		ORDER BY created_at DESC,id DESC LIMIT 1`, demandID, actorID).Scan(
		&funding.PaymentMode, &funding.Status, &funding.AmountCents, &funding.Currency, &funding.LiveMode, &funding.ProposalID,
		&funding.CheckoutURL, &funding.CheckoutExpiresAt, &funding.UpdatedAt)
	if err == nil {
		visible := item.ViewerRole != "viewer" || funding.ProposalID == nil
		for _, proposal := range item.Proposals {
			if funding.ProposalID != nil && proposal.ID == *funding.ProposalID {
				visible = true
			}
		}
		if visible {
			item.Funding = &funding
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, fmt.Errorf("get task funding: %w", err)
	}
	if item.ViewerRole == "viewer" {
		return item, nil
	}
	var pending DeadlineChange
	err = s.pool.QueryRow(ctx, `SELECT id,proposed_by,deadline,reason FROM task_deadline_changes WHERE demand_id=$1 AND status='pending'`, demandID).Scan(&pending.ID, &pending.ProposedBy, &pending.Deadline, &pending.Reason)
	if err == nil {
		item.DeadlineChange = &pending
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, err
	}
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
	input.Deliverables = cleanStrings(input.Deliverables)
	input.AcceptanceRules = cleanStrings(input.AcceptanceRules)
	var validType int
	if err := s.pool.QueryRow(ctx, `SELECT 1 FROM task_types WHERE code=$1`, input.DeliverableType).Scan(&validType); err != nil {
		return Detail{}, ErrInvalid
	}
	if utf8.RuneCountInString(input.Title) < 5 || utf8.RuneCountInString(input.Summary) < 10 || utf8.RuneCountInString(input.Brief) < 30 || input.BudgetCents < 50 || input.BudgetCents > 99999999 || input.Currency != "USD" || len(input.Deliverables) == 0 || len(input.AcceptanceRules) == 0 || input.RightsTerms == "" || input.AIDisclosureRequirement == "" {
		return Detail{}, ErrInvalid
	}
	if input.ClientTimezone == "" {
		input.ClientTimezone = "UTC"
	}
	if _, err := time.LoadLocation(input.ClientTimezone); err != nil {
		return Detail{}, ErrInvalid
	}
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 200 {
		return Detail{}, ErrInvalid
	}
	demandID := uuid.New()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize identical creation requests before inserting a randomly identified task.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, actorID.String()+":task:create:"+strings.TrimSpace(key)); err != nil {
		return Detail{}, err
	}
	if replayID, replay, err := findCommand(ctx, tx, actorID, "create", key, input); err != nil {
		return Detail{}, err
	} else if replay {
		_ = tx.Rollback(ctx)
		return s.Get(ctx, actorID, replayID)
	}
	if !input.Deadline.After(time.Now()) {
		return Detail{}, ErrInvalid
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.TaskCreation); err != nil {
		return Detail{}, err
	}
	deliverables, _ := json.Marshal(cleanStrings(input.Deliverables))
	rules, _ := json.Marshal(cleanStrings(input.AcceptanceRules))
	_, err = tx.Exec(ctx, `
		INSERT INTO demands(id,client_id,title,summary,brief,deliverable_type,deliverables,acceptance_rules,rights_terms,
		 ai_disclosure_requirement,budget_cents,currency,deadline,status,client_timezone,allow_direct_accept,idempotency_key,allow_derivative_reuse)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'open',$14,$15,$16,$17)`, demandID, actorID, input.Title,
		input.Summary, input.Brief, input.DeliverableType, deliverables, rules, input.RightsTerms, input.AIDisclosureRequirement,
		input.BudgetCents, input.Currency, input.Deadline, input.ClientTimezone, input.AllowDirectAccept, key, input.AllowDerivativeReuse)
	if err != nil {
		return Detail{}, fmt.Errorf("create task: %w", err)
	}
	if _, _, err := registerCommand(ctx, tx, actorID, demandID, "create", key, input); err != nil {
		return Detail{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO jobs(kind,payload,available_at,max_attempts) VALUES($1,jsonb_build_object('taskId',$2::text),$3,20)`, ExpiryJobKind, demandID, input.Deadline); err != nil {
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
	if utf8.RuneCountInString(input.Approach) < 20 || utf8.RuneCountInString(input.Deliverables) < 10 || input.AmountCents < 50 || input.AmountCents > 99999999 || input.TimelineDays <= 0 {
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
	var deadline time.Time
	var clientID uuid.UUID
	var status, taskTitle string
	if err := tx.QueryRow(ctx, `SELECT client_id,status,title,deadline FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&clientID, &status, &taskTitle, &deadline); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if clientID == actorID {
		return Detail{}, ErrForbidden
	}
	if !deadline.After(time.Now()) || status != "open" {
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
	var deadline time.Time
	var clientID uuid.UUID
	var status, taskTitle string
	var allow bool
	var budget int
	if err = tx.QueryRow(ctx, `SELECT client_id,status,allow_direct_accept,budget_cents,title,deadline FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&clientID, &status, &allow, &budget, &taskTitle, &deadline); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if clientID == actorID {
		return Detail{}, ErrForbidden
	}
	if !deadline.After(time.Now()) || status != "open" || !allow {
		return Detail{}, ErrConflict
	}
	if err := s.requireTaskFundingTx(ctx, tx, demandID, nil, actorID, budget, "USD"); err != nil {
		return Detail{}, err
	}
	if err = closeOtherProposals(ctx, tx, demandID, actorID, taskTitle); err != nil {
		return Detail{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO proposals(demand_id,creator_id,approach,deliverables,amount_cents,timeline_days,status,idempotency_key) VALUES($1,$2,'Direct acceptance','Deliver according to the published brief',$3,1,'accepted',$4) ON CONFLICT(demand_id,creator_id) DO UPDATE SET status='accepted',amount_cents=EXCLUDED.amount_cents,updated_at=now() WHERE proposals.status='submitted'`, demandID, actorID, budget, key)
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
	var deadline time.Time
	var clientID uuid.UUID
	var status, taskTitle string
	if err = tx.QueryRow(ctx, `SELECT client_id,status,title,deadline FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&clientID, &status, &taskTitle, &deadline); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	} else if err != nil {
		return Detail{}, err
	}
	if clientID != actorID {
		return Detail{}, ErrForbidden
	}
	if !deadline.After(time.Now()) || status != "open" {
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
	var proposalAmount int
	if err = tx.QueryRow(ctx, `SELECT amount_cents FROM proposals WHERE id=$1`, proposalID).Scan(&proposalAmount); err != nil {
		return Detail{}, err
	}
	if err := s.requireTaskFundingTx(ctx, tx, demandID, &proposalID, creatorID, proposalAmount, "USD"); err != nil {
		return Detail{}, err
	}
	if err = closeOtherProposals(ctx, tx, demandID, creatorID, taskTitle); err != nil {
		return Detail{}, err
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
	input.RightsEvidence = strings.TrimSpace(input.RightsEvidence)
	input.AIDisclosure = strings.TrimSpace(input.AIDisclosure)
	if len(input.AssetIDs) == 0 && input.AssetID != uuid.Nil {
		input.AssetIDs = []uuid.UUID{input.AssetID}
	}
	if len(input.AssetIDs) < 1 || len(input.AssetIDs) > 20 || !input.RightsConfirmed || utf8.RuneCountInString(input.RightsEvidence) < 10 || utf8.RuneCountInString(input.AIDisclosure) < 5 {
		return Detail{}, ErrInvalid
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range input.AssetIDs {
		if id == uuid.Nil || seen[id] {
			return Detail{}, ErrInvalid
		}
		seen[id] = true
	}
	input.AssetID = input.AssetIDs[0]
	if input.AssetID == uuid.Nil || utf8.RuneCountInString(input.Note) < 5 {
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
	var status, taskTitle, deliverableType string
	if err = tx.QueryRow(ctx, `SELECT assignee_id,client_id,status,title,deliverable_type FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&assigneeID, &clientID, &status, &taskTitle, &deliverableType); errors.Is(err, pgx.ErrNoRows) {
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
	// Share the original-file lock with product publication. A submitted task
	// delivery must not simultaneously become a privately sold product source.
	lockedAssets, lockErr := tx.Query(ctx, `SELECT id FROM assets WHERE id=ANY($1) ORDER BY id FOR UPDATE`, input.AssetIDs)
	if lockErr != nil {
		return Detail{}, lockErr
	}
	for lockedAssets.Next() {
		var id uuid.UUID
		if err = lockedAssets.Scan(&id); err != nil {
			lockedAssets.Close()
			return Detail{}, err
		}
	}
	err = lockedAssets.Err()
	lockedAssets.Close()
	if err != nil {
		return Detail{}, err
	}
	// Require source ownership, a clean scan, no resale of purchased/granted assets,
	// and at least one primary asset matching the brief. Supporting documents are allowed.
	var assetCount, matching int
	if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE kind=$3 OR ($3='text' AND kind='document') OR $3='mixed')
 FROM assets a WHERE a.id=ANY($1) AND a.owner_id=$2 AND a.scan_status='clean'
 AND a.source_type<>'purchase' AND a.license_code<>'task-contract'
 AND NOT EXISTS(SELECT 1 FROM product_delivery_roots root WHERE root.asset_id=COALESCE(a.origin_asset_id,a.id))`, input.AssetIDs, actorID, deliverableType).Scan(&assetCount, &matching); err != nil {
		return Detail{}, err
	}
	if assetCount != len(input.AssetIDs) {
		return Detail{}, ErrForbidden
	}
	if matching == 0 {
		return Detail{}, ErrInvalid
	}
	var version int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM deliveries WHERE demand_id=$1`, demandID).Scan(&version); err != nil {
		return Detail{}, err
	}
	var deliveryID uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO deliveries(demand_id,creator_id,asset_id,note,status,version,idempotency_key,rights_evidence,ai_disclosure) VALUES($1,$2,$3,$4,'submitted',$5,$6,$7,$8) RETURNING id`, demandID, actorID, input.AssetID, input.Note, version, key, input.RightsEvidence, input.AIDisclosure).Scan(&deliveryID); err != nil {
		return Detail{}, err
	}
	for position, id := range input.AssetIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO delivery_assets(delivery_id,asset_id,position) VALUES($1,$2,$3)`, deliveryID, id, position); err != nil {
			return Detail{}, err
		}
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
	if input.Decision == "request_revision" && utf8.RuneCountInString(input.Note) < 10 {
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
		settlementMode := "local_test"
		settlementMessage := "Your delivery for “" + taskTitle + "” was accepted and the Local Test USD settlement was recorded."
		if s.providerPayments {
			var paymentID uuid.UUID
			var paymentAmount int
			var paymentCurrency string
			err = tx.QueryRow(ctx, `
				SELECT id,amount_cents,currency FROM payment_intents
				WHERE purpose='task' AND resource_id=$1 AND payer_id=$2 AND payee_id=$3 AND status='paid' FOR UPDATE`,
				demandID, clientID, *assigneeID).Scan(&paymentID, &paymentAmount, &paymentCurrency)
			if errors.Is(err, pgx.ErrNoRows) || paymentAmount != budget || paymentCurrency != currency {
				return Detail{}, ErrConflict
			}
			if err != nil {
				return Detail{}, err
			}
			settlementMode = "provider_pending"
			settlementMessage = "Your delivery for “" + taskTitle + "” was accepted. The verified Provider payout is pending."
			if _, err = tx.Exec(ctx, `INSERT INTO task_settlements(id,demand_id,client_id,creator_id,amount_cents,currency,mode) VALUES($1,$2,$3,$4,$5,$6,$7)`, settlementID, demandID, clientID, *assigneeID, budget, currency, settlementMode); err != nil {
				return Detail{}, err
			}
			if _, err = tx.Exec(ctx, `UPDATE payment_intents SET status='transfer_pending',updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
				return Detail{}, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence) VALUES($1,'transfer.requested','paid','transfer_pending',jsonb_build_object('taskId',$2::text,'settlementId',$3::text))`, paymentID, demandID, settlementID); err != nil {
				return Detail{}, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('paymentId',$2::text),20)`, payments.TaskTransferJobKind, paymentID); err != nil {
				return Detail{}, err
			}
		} else {
			if _, err = tx.Exec(ctx, `INSERT INTO task_settlements(id,demand_id,client_id,creator_id,amount_cents,currency,mode) VALUES($1,$2,$3,$4,$5,$6,$7)`, settlementID, demandID, clientID, *assigneeID, budget, currency, settlementMode); err != nil {
				return Detail{}, err
			}
			if err = billing.TransferTx(ctx, tx, clientID, *assigneeID, settlementID, budget, currency,
				"task_payment", "task_earning", "Local Test task settlement"); err != nil {
				return Detail{}, fmt.Errorf("apply task billing transfer: %w", err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO ledger_entries(account_id,operation_id,direction,amount_cents,currency,reason) VALUES($1,$3,'debit',$4,$5,'task_local_test_settlement'),($2,$3,'credit',$4,$5,'task_local_test_settlement')`, clientID, *assigneeID, settlementID, budget, currency); err != nil {
				return Detail{}, err
			}
		}
		if err = taskdelivery.GrantTx(ctx, tx, deliveryID); errors.Is(err, taskdelivery.ErrUnavailable) {
			return Detail{}, ErrConflict
		} else if err != nil {
			return Detail{}, err
		}
		if err = insertEvent(ctx, tx, demandID, actorID, "delivery_accepted", stringPtr("submitted"), "accepted", input.Note, map[string]string{"settlementMode": settlementMode}); err != nil {
			return Detail{}, err
		}
		if err = notifyTask(ctx, tx, *assigneeID, "task.delivery_accepted", "Delivery accepted", settlementMessage, demandID, "accepted:"+deliveryID.String()); err != nil {
			return Detail{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, actorID, demandID)
}

func (s *Service) requireTaskFundingTx(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, proposalID *uuid.UUID, assigneeID uuid.UUID, amount int, currency string) error {
	if !s.providerPayments {
		return nil
	}
	var paymentID uuid.UUID
	var fundedProposalID, payeeID *uuid.UUID
	var fundedAmount int
	var fundedCurrency, status string
	err := tx.QueryRow(ctx, `
		SELECT id,proposal_id,payee_id,amount_cents,currency,status FROM payment_intents
		WHERE purpose='task' AND resource_id=$1 AND status='paid' FOR UPDATE`, taskID).Scan(
		&paymentID, &fundedProposalID, &payeeID, &fundedAmount, &fundedCurrency, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if status != "paid" || !sameUUID(fundedProposalID, proposalID) || fundedAmount != amount || fundedCurrency != currency || (payeeID != nil && *payeeID != assigneeID) {
		return ErrConflict
	}
	result, err := tx.Exec(ctx, `UPDATE payment_intents SET payee_id=$2,updated_at=now(),version=version+1 WHERE id=$1 AND (payee_id IS NULL OR payee_id=$2)`, paymentID, assigneeID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func sameUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (s *Service) OpenDispute(ctx context.Context, actorID, demandID uuid.UUID, reason, key string) (Detail, error) {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) < 20 {
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
	if status != "assigned" && status != "submitted" && status != "revision" {
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
	if utf8.RuneCountInString(reason) < 10 {
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
	cancellationEvidence := map[string]any{"paymentMode": "local_test"}
	if s.providerPayments {
		var paymentID uuid.UUID
		var paymentProvider, paymentStatus string
		err = tx.QueryRow(ctx, `
			SELECT id,provider,status FROM payment_intents
			WHERE purpose='task' AND resource_id=$1
			ORDER BY created_at DESC,id DESC LIMIT 1 FOR UPDATE`, demandID).Scan(&paymentID, &paymentProvider, &paymentStatus)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, err
		}
		if err == nil {
			cancellationEvidence = map[string]any{"paymentMode": paymentProvider, "paymentId": paymentID, "fundingStatus": paymentStatus}
			switch paymentStatus {
			case "checkout_pending", "checkout_open", "payment_failed":
				if _, err = tx.Exec(ctx, `UPDATE payment_intents SET status='cancelled',updated_at=now(),version=version+1 WHERE id=$1`, paymentID); err != nil {
					return Detail{}, err
				}
				if _, err = tx.Exec(ctx, `
					INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
					VALUES($1,'task.cancelled',$2,'cancelled',jsonb_build_object('taskId',$3::text))`, paymentID, paymentStatus, demandID); err != nil {
					return Detail{}, err
				}
				cancellationEvidence["fundingStatus"] = "cancelled"
			case "paid", "refund_failed":
				operationID := uuid.New()
				if _, err = tx.Exec(ctx, `
					UPDATE payment_intents SET status='refund_pending',refund_operation_id=$2,provider_refund_id=NULL,updated_at=now(),version=version+1
					WHERE id=$1`, paymentID, operationID); err != nil {
					return Detail{}, err
				}
				if _, err = tx.Exec(ctx, `
					INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
					VALUES($1,'refund.requested',$2,'refund_pending',jsonb_build_object('taskId',$3::text,'reason','task_cancelled'))`, paymentID, paymentStatus, demandID); err != nil {
					return Detail{}, err
				}
				if _, err = tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('paymentId',$2::text),20)`, payments.TaskRefundJobKind, paymentID); err != nil {
					return Detail{}, err
				}
				cancellationEvidence["fundingStatus"] = "refund_pending"
			case "refund_pending", "refunded":
				cancellationEvidence["fundingStatus"] = paymentStatus
			default:
				return Detail{}, ErrConflict
			}
		}
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
	if err = insertEvent(ctx, tx, demandID, actorID, "task_cancelled", stringPtr("open"), "cancelled", reason, cancellationEvidence); err != nil {
		return Detail{}, err
	}
	if cancellationEvidence["fundingStatus"] == "refund_pending" {
		if err = notifyTask(ctx, tx, clientID, "task.refund_requested", "Task refund requested", "The funded task \u201c"+taskTitle+"\u201d was cancelled. The Provider refund is pending signed confirmation.", demandID, "refund-requested"); err != nil {
			return Detail{}, err
		}
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
	rows, err := s.pool.Query(ctx, `SELECT u.id,u.handle,u.display_name,d.id,d.asset_id,a.title,a.media_url,a.kind,d.note,d.status,d.version,d.review_note,d.created_at,d.reviewed_at,d.accepted_at,d.rights_evidence,d.ai_disclosure,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a2.id,'title',a2.title,'mediaUrl',a2.media_url,'kind',a2.kind) ORDER BY da.position) FROM delivery_assets da JOIN assets a2 ON a2.id=da.asset_id WHERE da.delivery_id=d.id),'[]'::jsonb) FROM deliveries d JOIN assets a ON a.id=d.asset_id JOIN users u ON u.id=d.creator_id WHERE d.demand_id=$1 ORDER BY d.version DESC`, demandID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Delivery, 0)
	for rows.Next() {
		var d Delivery
		var bundle []byte
		if err = rows.Scan(&d.Creator.ID, &d.Creator.Handle, &d.Creator.DisplayName, &d.ID, &d.AssetID, &d.AssetTitle, &d.MediaURL, &d.MediaKind, &d.Note, &d.Status, &d.Version, &d.ReviewNote, &d.CreatedAt, &d.ReviewedAt, &d.AcceptedAt, &d.RightsEvidence, &d.AIDisclosure, &bundle); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(bundle, &d.Assets); err != nil {
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
	if existingHash != hash || (operation != "create" && existingID != demandID) {
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

// Close losing proposals in the same locked-task transaction as assignment.
func closeOtherProposals(ctx context.Context, tx pgx.Tx, demandID, winnerID uuid.UUID, title string) error {
	rows, err := tx.Query(ctx, `UPDATE proposals SET status='rejected',updated_at=now() WHERE demand_id=$1 AND creator_id<>$2 AND status='submitted' RETURNING creator_id`, demandID, winnerID)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err = notifyTask(ctx, tx, id, "task.proposal_rejected", "Proposal closed", "Another creator was selected for “"+title+"”.", demandID, "proposal-rejected:"+id.String()); err != nil {
			return err
		}
	}
	return nil
}
