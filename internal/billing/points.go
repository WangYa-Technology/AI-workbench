package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrPointAccountNotFound = errors.New("point account not found")
	ErrSubscriptionRequired = errors.New("active subscription required")
	ErrModelNotIncluded     = errors.New("model is not included in subscription")
	ErrPricingNotConfigured = errors.New("model point pricing is not configured")
	ErrInvalidPlan          = errors.New("invalid subscription plan")
	ErrPlanConflict         = errors.New("subscription plan changed")
	ErrPlanForbidden        = errors.New("subscription plan finance authority required")
	ErrInvalidPricing       = errors.New("invalid model point pricing")
	ErrSubscriptionReplay   = errors.New("subscription idempotency conflict")
	ErrSubscriptionActive   = errors.New("subscription plan is already active")
	ErrInvalidPointEntries  = errors.New("invalid point entry pagination")
)

type PointAccount struct {
	UserID               uuid.UUID `json:"userId"`
	BalancePoints        int64     `json:"balancePoints"`
	ReservedPoints       int64     `json:"reservedPoints"`
	AvailablePoints      int64     `json:"availablePoints"`
	LifetimeEarnedPoints int64     `json:"lifetimeEarnedPoints"`
	LifetimeSpentPoints  int64     `json:"lifetimeSpentPoints"`
	Version              int64     `json:"version"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type PointEntry struct {
	ID                 uuid.UUID      `json:"id"`
	OperationID        uuid.UUID      `json:"operationId"`
	EntryType          string         `json:"entryType"`
	Direction          string         `json:"direction"`
	AmountPoints       int64          `json:"amountPoints"`
	BalanceAfterPoints int64          `json:"balanceAfterPoints"`
	Description        string         `json:"description"`
	Metadata           map[string]any `json:"metadata"`
	CreatedAt          time.Time      `json:"createdAt"`
}

type SubscriptionPlan struct {
	ID                uuid.UUID   `json:"id"`
	TierCode          string      `json:"tierCode"`
	Name              string      `json:"name"`
	Description       string      `json:"description"`
	PriceCents        int         `json:"priceCents"`
	Currency          string      `json:"currency"`
	IncludedPoints    int64       `json:"includedPoints"`
	BillingPeriodDays int         `json:"billingPeriodDays"`
	SortOrder         int         `json:"sortOrder"`
	Active            bool        `json:"active"`
	ModelIDs          []uuid.UUID `json:"modelIds"`
	Version           int64       `json:"version"`
	CreatedAt         time.Time   `json:"createdAt"`
	UpdatedAt         time.Time   `json:"updatedAt"`
}

type UserSubscription struct {
	ID               uuid.UUID `json:"id"`
	UserID           uuid.UUID `json:"userId"`
	PlanID           uuid.UUID `json:"planId"`
	PlanName         string    `json:"planName"`
	TierCode         string    `json:"tierCode"`
	Status           string    `json:"status"`
	PriceCents       int       `json:"priceCents"`
	Currency         string    `json:"currency"`
	GrantedPoints    int64     `json:"grantedPoints"`
	StartedAt        time.Time `json:"startedAt"`
	CurrentPeriodEnd time.Time `json:"currentPeriodEnd"`
}

type PointOverview struct {
	Account             PointAccount       `json:"account"`
	CurrentSubscription *UserSubscription  `json:"currentSubscription,omitempty"`
	Plans               []SubscriptionPlan `json:"plans"`
	Entries             []PointEntry       `json:"entries"`
	NextEntryCursor     *string            `json:"nextEntryCursor,omitempty"`
}

type pointEntryCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type SubscriptionPlanInput struct {
	TierCode          string      `json:"tierCode"`
	Name              string      `json:"name"`
	Description       string      `json:"description"`
	PriceCents        int         `json:"priceCents"`
	Currency          string      `json:"currency"`
	IncludedPoints    int64       `json:"includedPoints"`
	BillingPeriodDays int         `json:"billingPeriodDays"`
	SortOrder         int         `json:"sortOrder"`
	Active            bool        `json:"active"`
	ModelIDs          []uuid.UUID `json:"modelIds"`
}

type SubscriptionPlanUpdate struct {
	ExpectedVersion   int64        `json:"expectedVersion"`
	TierCode          *string      `json:"tierCode,omitempty"`
	Name              *string      `json:"name,omitempty"`
	Description       *string      `json:"description,omitempty"`
	PriceCents        *int         `json:"priceCents,omitempty"`
	Currency          *string      `json:"currency,omitempty"`
	IncludedPoints    *int64       `json:"includedPoints,omitempty"`
	BillingPeriodDays *int         `json:"billingPeriodDays,omitempty"`
	SortOrder         *int         `json:"sortOrder,omitempty"`
	Active            *bool        `json:"active,omitempty"`
	ModelIDs          *[]uuid.UUID `json:"modelIds,omitempty"`
}

type ImageResolutionPrice struct {
	Resolution string `json:"resolution"`
	Points     int    `json:"points"`
}

type ModelPointPricing struct {
	ID                      uuid.UUID              `json:"id"`
	Mode                    string                 `json:"mode"`
	InputPointsPer1KTokens  int                    `json:"inputPointsPer1KTokens"`
	OutputPointsPer1KTokens int                    `json:"outputPointsPer1KTokens"`
	PointsPerSecond         int                    `json:"pointsPerSecond"`
	MinimumPoints           int                    `json:"minimumPoints"`
	ImageResolutionPrices   []ImageResolutionPrice `json:"imageResolutionPrices"`
	Version                 int                    `json:"version"`
}

type MeterInput struct {
	PromptCharacters int
	ResponseLength   string
	AspectRatio      string
	DurationSeconds  int
	ImageCount       int
}

type UsageMetrics struct {
	InputTokens      int
	OutputTokens     int
	DurationSeconds  int
	ImageCount       int
	Width            int
	Height           int
	ProviderReported bool
}

type pricingSnapshot struct {
	Rule                ModelPointPricing `json:"rule"`
	RequestedResolution string            `json:"requestedResolution,omitempty"`
	EstimateInput       MeterInput        `json:"estimateInput"`
}

func (s *Service) PointOverview(ctx context.Context, userID uuid.UUID) (PointOverview, error) {
	return s.PointOverviewPage(ctx, userID, "", 30)
}

func (s *Service) PointOverviewPage(ctx context.Context, userID uuid.UUID, cursor string, limit int) (PointOverview, error) {
	account, err := getPointAccount(ctx, s.pool, userID)
	if err != nil {
		return PointOverview{}, err
	}
	plans, err := s.ListSubscriptionPlans(ctx, false)
	if err != nil {
		return PointOverview{}, err
	}
	current, err := currentSubscription(ctx, s.pool, userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PointOverview{}, err
	}
	if limit == 0 {
		limit = 30
	}
	if limit < 1 || limit > 50 {
		return PointOverview{}, ErrInvalidPointEntries
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if cursor != "" {
		decoded, err := decodePointEntryCursor(cursor)
		if err != nil {
			return PointOverview{}, ErrInvalidPointEntries
		}
		cursorTime, cursorID = &decoded.CreatedAt, &decoded.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,operation_id,entry_type,direction,amount_points,balance_after_points,description,metadata,created_at
		FROM point_entries WHERE user_id=$1
		  AND ($2::timestamptz IS NULL OR (created_at,id) < ($2,$3::uuid))
		ORDER BY created_at DESC,id DESC LIMIT $4`, userID, cursorTime, cursorID, limit+1)
	if err != nil {
		return PointOverview{}, fmt.Errorf("list point entries: %w", err)
	}
	defer rows.Close()
	entries := make([]PointEntry, 0)
	for rows.Next() {
		var item PointEntry
		if err := rows.Scan(&item.ID, &item.OperationID, &item.EntryType, &item.Direction, &item.AmountPoints, &item.BalanceAfterPoints, &item.Description, &item.Metadata, &item.CreatedAt); err != nil {
			return PointOverview{}, fmt.Errorf("scan point entry: %w", err)
		}
		entries = append(entries, item)
	}
	result := PointOverview{Account: account, Plans: plans, Entries: entries}
	if len(result.Entries) > limit {
		result.Entries = result.Entries[:limit]
		next := encodePointEntryCursor(result.Entries[len(result.Entries)-1])
		result.NextEntryCursor = &next
	}
	if err == nil && current.ID != uuid.Nil {
		result.CurrentSubscription = &current
	}
	return result, rows.Err()
}

func encodePointEntryCursor(item PointEntry) string {
	body, _ := json.Marshal(pointEntryCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodePointEntryCursor(value string) (pointEntryCursor, error) {
	var cursor pointEntryCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
		return pointEntryCursor{}, ErrInvalidPointEntries
	}
	return cursor, nil
}

// ExpireSubscriptions marks active subscriptions whose paid period has ended.
// The predicate makes repeated runs idempotent and avoids touching newer plans.
func (s *Service) ExpireSubscriptions(ctx context.Context, limit int) (int64, error) {
	if limit == 0 {
		limit = 1000
	}
	if limit < 1 || limit > 10000 {
		return 0, fmt.Errorf("invalid subscription expiry limit")
	}
	result, err := s.pool.Exec(ctx, `
		WITH expired AS (
			SELECT id FROM user_subscriptions
			WHERE status='active' AND current_period_end <= now()
			ORDER BY current_period_end,id LIMIT $1 FOR UPDATE SKIP LOCKED
		)
		UPDATE user_subscriptions s SET status='expired',updated_at=now()
		FROM expired e WHERE s.id=e.id AND s.status='active' AND s.current_period_end <= now()`, limit)
	if err != nil {
		return 0, fmt.Errorf("expire subscriptions: %w", err)
	}
	return result.RowsAffected(), nil
}

func getPointAccount(ctx context.Context, q rowQuerier, userID uuid.UUID) (PointAccount, error) {
	var item PointAccount
	err := q.QueryRow(ctx, `
		SELECT user_id,balance_points,reserved_points,balance_points-reserved_points,lifetime_earned_points,lifetime_spent_points,version,updated_at
		FROM point_accounts WHERE user_id=$1`, userID).Scan(&item.UserID, &item.BalancePoints, &item.ReservedPoints, &item.AvailablePoints, &item.LifetimeEarnedPoints, &item.LifetimeSpentPoints, &item.Version, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PointAccount{}, ErrPointAccountNotFound
	}
	if err != nil {
		return PointAccount{}, fmt.Errorf("get point account: %w", err)
	}
	return item, nil
}

func currentSubscription(ctx context.Context, q rowQuerier, userID uuid.UUID) (UserSubscription, error) {
	var item UserSubscription
	err := q.QueryRow(ctx, `
		SELECT s.id,s.user_id,s.plan_id,COALESCE(c.plan_name,p.name),COALESCE(c.tier_code,p.tier_code),s.status,s.price_cents,s.currency,s.granted_points,s.started_at,s.current_period_end
		FROM user_subscriptions s JOIN subscription_plans p ON p.id=s.plan_id
		LEFT JOIN subscription_checkout_contracts c ON c.payment_id=s.purchase_operation_id AND c.buyer_id=s.user_id AND c.plan_id=s.plan_id
		AND c.price_cents=s.price_cents AND c.currency=s.currency AND c.included_points=s.granted_points
		WHERE s.user_id=$1 AND s.status='active' AND s.current_period_end>now()
		AND (c.payment_id IS NOT NULL OR NOT EXISTS(SELECT 1 FROM payment_intents i WHERE i.id=s.purchase_operation_id AND i.purpose='subscription'))`, userID).Scan(
		&item.ID, &item.UserID, &item.PlanID, &item.PlanName, &item.TierCode, &item.Status, &item.PriceCents, &item.Currency, &item.GrantedPoints, &item.StartedAt, &item.CurrentPeriodEnd)
	return item, err
}

func (s *Service) ListSubscriptionPlans(ctx context.Context, includeInactive bool) ([]SubscriptionPlan, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id,p.tier_code,p.name,p.description,p.price_cents,p.currency,p.included_points,p.billing_period_days,p.sort_order,p.active,p.created_at,p.updated_at,p.version,
		       COALESCE(array_agg(pm.provider_model_id ORDER BY pm.provider_model_id) FILTER (WHERE pm.provider_model_id IS NOT NULL),'{}'::uuid[])
		FROM subscription_plans p LEFT JOIN subscription_plan_models pm ON pm.plan_id=p.id
		WHERE ($1 OR p.active=true)
		GROUP BY p.id ORDER BY p.sort_order,lower(p.name),p.id`, includeInactive)
	if err != nil {
		return nil, fmt.Errorf("list subscription plans: %w", err)
	}
	defer rows.Close()
	items := make([]SubscriptionPlan, 0)
	for rows.Next() {
		var item SubscriptionPlan
		if err := rows.Scan(&item.ID, &item.TierCode, &item.Name, &item.Description, &item.PriceCents, &item.Currency, &item.IncludedPoints, &item.BillingPeriodDays, &item.SortOrder, &item.Active, &item.CreatedAt, &item.UpdatedAt, &item.Version, &item.ModelIDs); err != nil {
			return nil, fmt.Errorf("scan subscription plan: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func validatePlanInput(input SubscriptionPlanInput) error {
	input.TierCode = strings.TrimSpace(strings.ToLower(input.TierCode))
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if len(input.TierCode) < 2 || len(input.TierCode) > 32 || len(input.Name) < 2 || len(input.Name) > 80 || len(input.Description) < 1 || len(input.Description) > 500 || input.PriceCents < 0 || input.PriceCents > 100000000 || input.Currency != "USD" || input.IncludedPoints < 1 || input.IncludedPoints > 1000000000 || input.BillingPeriodDays < 1 || input.BillingPeriodDays > 366 || len(input.ModelIDs) > 500 {
		return ErrInvalidPlan
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range input.ModelIDs {
		if id == uuid.Nil || seen[id] {
			return ErrInvalidPlan
		}
		seen[id] = true
	}
	return nil
}

func (s *Service) CreateSubscriptionPlan(ctx context.Context, actorID uuid.UUID, input SubscriptionPlanInput, requestID string) (_ SubscriptionPlan, resultErr error) {
	defer func() { resultErr = planCommandError(resultErr) }()
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return SubscriptionPlan{}, ErrInvalidPlan
	}
	input.TierCode = strings.TrimSpace(strings.ToLower(input.TierCode))
	input.Name, input.Description, input.Currency = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), strings.ToUpper(strings.TrimSpace(input.Currency))
	if err := validatePlanInput(input); err != nil {
		return SubscriptionPlan{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return SubscriptionPlan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := planFinanceAuthorityTx(ctx, tx, actorID, true); err != nil {
		return SubscriptionPlan{}, err
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO subscription_plans(tier_code,name,description,price_cents,currency,included_points,billing_period_days,sort_order,active,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10) RETURNING id`, input.TierCode, input.Name, input.Description, input.PriceCents, input.Currency, input.IncludedPoints, input.BillingPeriodDays, input.SortOrder, input.Active, actorID).Scan(&id); err != nil {
		return SubscriptionPlan{}, fmt.Errorf("create subscription plan: %w", err)
	}
	if err := replacePlanModels(ctx, tx, id, input.ModelIDs); err != nil {
		return SubscriptionPlan{}, err
	}
	item, err := subscriptionPlanTx(ctx, tx, id)
	if err != nil {
		return SubscriptionPlan{}, err
	}
	if err := recordPlanCommandTx(ctx, tx, actorID, nil, item, requestID); err != nil {
		return SubscriptionPlan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubscriptionPlan{}, err
	}
	return item, nil
}

func (s *Service) UpdateSubscriptionPlan(ctx context.Context, actorID, planID uuid.UUID, input SubscriptionPlanUpdate, requestID string) (_ SubscriptionPlan, resultErr error) {
	defer func() { resultErr = planCommandError(resultErr) }()
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || input.ExpectedVersion < 1 {
		return SubscriptionPlan{}, ErrInvalidPlan
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return SubscriptionPlan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := planFinanceAuthorityTx(ctx, tx, actorID, false); err != nil {
		return SubscriptionPlan{}, err
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT version FROM subscription_plans WHERE id=$1 FOR UPDATE`, planID).Scan(&revision); errors.Is(err, pgx.ErrNoRows) {
		return SubscriptionPlan{}, ErrInvalidPlan
	} else if err != nil {
		return SubscriptionPlan{}, err
	}
	if revision != input.ExpectedVersion {
		return SubscriptionPlan{}, ErrPlanConflict
	}
	current, err := subscriptionPlanTx(ctx, tx, planID)
	if err != nil {
		return SubscriptionPlan{}, err
	}
	value := SubscriptionPlanInput{TierCode: current.TierCode, Name: current.Name, Description: current.Description, PriceCents: current.PriceCents, Currency: current.Currency, IncludedPoints: current.IncludedPoints, BillingPeriodDays: current.BillingPeriodDays, SortOrder: current.SortOrder, Active: current.Active, ModelIDs: current.ModelIDs}
	if input.TierCode != nil {
		value.TierCode = *input.TierCode
	}
	if input.Name != nil {
		value.Name = *input.Name
	}
	if input.Description != nil {
		value.Description = *input.Description
	}
	if input.PriceCents != nil {
		value.PriceCents = *input.PriceCents
	}
	if input.Currency != nil {
		value.Currency = *input.Currency
	}
	if input.IncludedPoints != nil {
		value.IncludedPoints = *input.IncludedPoints
	}
	if input.BillingPeriodDays != nil {
		value.BillingPeriodDays = *input.BillingPeriodDays
	}
	if input.SortOrder != nil {
		value.SortOrder = *input.SortOrder
	}
	if input.Active != nil {
		value.Active = *input.Active
	}
	if input.ModelIDs != nil {
		value.ModelIDs = *input.ModelIDs
	}
	value.TierCode = strings.TrimSpace(strings.ToLower(value.TierCode))
	value.Name = strings.TrimSpace(value.Name)
	value.Description = strings.TrimSpace(value.Description)
	value.Currency = strings.ToUpper(strings.TrimSpace(value.Currency))
	if err := validatePlanInput(value); err != nil {
		return SubscriptionPlan{}, err
	}
	if err := planFinanceAuthorityTx(ctx, tx, actorID, true); err != nil {
		return SubscriptionPlan{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE subscription_plans SET tier_code=$2,name=$3,description=$4,price_cents=$5,currency=$6,included_points=$7,billing_period_days=$8,sort_order=$9,active=$10,updated_by=$11,updated_at=now() WHERE id=$1`, planID, value.TierCode, value.Name, value.Description, value.PriceCents, value.Currency, value.IncludedPoints, value.BillingPeriodDays, value.SortOrder, value.Active, actorID)
	if err != nil {
		return SubscriptionPlan{}, fmt.Errorf("update subscription plan: %w", err)
	}
	if result.RowsAffected() == 0 {
		return SubscriptionPlan{}, ErrInvalidPlan
	}
	if input.ModelIDs != nil {
		if err := replacePlanModels(ctx, tx, planID, value.ModelIDs); err != nil {
			return SubscriptionPlan{}, err
		}
	}
	item, err := subscriptionPlanTx(ctx, tx, planID)
	if err != nil {
		return SubscriptionPlan{}, err
	}
	if err := recordPlanCommandTx(ctx, tx, actorID, &current, item, requestID); err != nil {
		return SubscriptionPlan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubscriptionPlan{}, err
	}
	return item, nil
}

func replacePlanModels(ctx context.Context, tx pgx.Tx, planID uuid.UUID, modelIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM subscription_plan_models WHERE plan_id=$1`, planID); err != nil {
		return err
	}
	for _, modelID := range modelIDs {
		result, err := tx.Exec(ctx, `INSERT INTO subscription_plan_models(plan_id,provider_model_id)
		 SELECT $1,m.id FROM provider_config_models m JOIN provider_configs p ON p.id=m.provider_id
		 WHERE m.id=$2 AND m.archived_at IS NULL AND p.archived_at IS NULL FOR SHARE OF m,p`, planID, modelID)
		if err != nil {
			return fmt.Errorf("assign plan model: %w", err)
		}
		if result.RowsAffected() == 0 {
			return ErrInvalidPlan
		}
	}
	return nil
}

func (s *Service) getSubscriptionPlan(ctx context.Context, id uuid.UUID) (SubscriptionPlan, error) {
	items, err := s.ListSubscriptionPlans(ctx, true)
	if err != nil {
		return SubscriptionPlan{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return SubscriptionPlan{}, ErrInvalidPlan
}

func (s *Service) PurchaseSubscription(ctx context.Context, userID, planID uuid.UUID, idempotencyKey string) (PointOverview, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 8 || len(idempotencyKey) > 120 {
		return PointOverview{}, ErrInvalidPlan
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PointOverview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var existingPlanID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT plan_id FROM user_subscriptions WHERE user_id=$1 AND idempotency_key=$2`, userID, idempotencyKey).Scan(&existingPlanID); err == nil {
		if existingPlanID != planID {
			return PointOverview{}, ErrSubscriptionReplay
		}
		_ = tx.Rollback(ctx)
		return s.PointOverview(ctx, userID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return PointOverview{}, err
	}
	var plan SubscriptionPlan
	if err := tx.QueryRow(ctx, `SELECT id,tier_code,name,description,price_cents,currency,included_points,billing_period_days,sort_order,active,created_at,updated_at FROM subscription_plans WHERE id=$1 AND active=true FOR SHARE`, planID).Scan(&plan.ID, &plan.TierCode, &plan.Name, &plan.Description, &plan.PriceCents, &plan.Currency, &plan.IncludedPoints, &plan.BillingPeriodDays, &plan.SortOrder, &plan.Active, &plan.CreatedAt, &plan.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return PointOverview{}, ErrInvalidPlan
	} else if err != nil {
		return PointOverview{}, err
	}
	var currentPlanID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT plan_id FROM user_subscriptions WHERE user_id=$1 AND status='active' AND current_period_end>now() FOR UPDATE`, userID).Scan(&currentPlanID); err == nil && currentPlanID == plan.ID {
		return PointOverview{}, ErrSubscriptionActive
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PointOverview{}, err
	}
	operationID, subscriptionID := uuid.New(), uuid.New()
	if plan.PriceCents > 0 {
		var balance, reserved int64
		if err := tx.QueryRow(ctx, `SELECT balance_cents,reserved_cents FROM billing_accounts WHERE user_id=$1 AND currency=$2 FOR UPDATE`, userID, plan.Currency).Scan(&balance, &reserved); errors.Is(err, pgx.ErrNoRows) {
			return PointOverview{}, ErrAccountNotFound
		} else if err != nil {
			return PointOverview{}, err
		}
		if balance-reserved < int64(plan.PriceCents) {
			return PointOverview{}, ErrInsufficientFunds
		}
		balance -= int64(plan.PriceCents)
		if _, err := tx.Exec(ctx, `UPDATE billing_accounts SET balance_cents=$3,version=version+1,updated_at=now() WHERE user_id=$1 AND currency=$2`, userID, plan.Currency, balance); err != nil {
			return PointOverview{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO billing_entries(user_id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description,metadata) VALUES($1,$2,'subscription_purchase','debit',$3,$4,$5,$6,$7)`, userID, operationID, plan.PriceCents, plan.Currency, balance, "Subscription: "+plan.Name, map[string]any{"planId": plan.ID, "subscriptionId": subscriptionID}); err != nil {
			return PointOverview{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE user_subscriptions SET status=CASE WHEN current_period_end<=now() THEN 'expired' ELSE 'cancelled' END,cancelled_at=CASE WHEN current_period_end>now() THEN now() ELSE cancelled_at END,updated_at=now() WHERE user_id=$1 AND status='active'`, userID); err != nil {
		return PointOverview{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_subscriptions(id,user_id,plan_id,price_cents,currency,granted_points,current_period_end,purchase_operation_id,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,now()+make_interval(days => $7),$8,$9)`, subscriptionID, userID, plan.ID, plan.PriceCents, plan.Currency, plan.IncludedPoints, plan.BillingPeriodDays, operationID, idempotencyKey); err != nil {
		return PointOverview{}, fmt.Errorf("purchase subscription: %w", err)
	}
	var balanceAfter int64
	if err := tx.QueryRow(ctx, `UPDATE point_accounts SET balance_points=balance_points+$2,lifetime_earned_points=lifetime_earned_points+$2,version=version+1,updated_at=now() WHERE user_id=$1 RETURNING balance_points`, userID, plan.IncludedPoints).Scan(&balanceAfter); errors.Is(err, pgx.ErrNoRows) {
		return PointOverview{}, ErrPointAccountNotFound
	} else if err != nil {
		return PointOverview{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO point_entries(user_id,operation_id,entry_type,direction,amount_points,balance_after_points,description,metadata) VALUES($1,$2,'subscription_credit','credit',$3,$4,$5,$6)`, userID, operationID, plan.IncludedPoints, balanceAfter, "Subscription points: "+plan.Name, map[string]any{"planId": plan.ID, "subscriptionId": subscriptionID}); err != nil {
		return PointOverview{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PointOverview{}, err
	}
	return s.PointOverview(ctx, userID)
}

func ValidateModelPointPricing(mode string, rule ModelPointPricing) error {
	if rule.MinimumPoints < 1 || rule.MinimumPoints > 100000000 || rule.InputPointsPer1KTokens < 0 || rule.InputPointsPer1KTokens > 100000000 || rule.OutputPointsPer1KTokens < 0 || rule.OutputPointsPer1KTokens > 100000000 || rule.PointsPerSecond < 0 || rule.PointsPerSecond > 100000000 {
		return ErrInvalidPricing
	}
	switch mode {
	case "chat":
		if rule.InputPointsPer1KTokens < 1 || rule.OutputPointsPer1KTokens < 1 || rule.PointsPerSecond != 0 || len(rule.ImageResolutionPrices) != 0 {
			return ErrInvalidPricing
		}
	case "image":
		if rule.InputPointsPer1KTokens != 0 || rule.OutputPointsPer1KTokens != 0 || rule.PointsPerSecond != 0 || len(rule.ImageResolutionPrices) < 1 || len(rule.ImageResolutionPrices) > 20 {
			return ErrInvalidPricing
		}
		seen := map[string]bool{}
		for _, price := range rule.ImageResolutionPrices {
			if !validResolution(price.Resolution) || price.Points < 1 || price.Points > 100000000 || seen[price.Resolution] {
				return ErrInvalidPricing
			}
			seen[price.Resolution] = true
		}
	case "video", "music":
		if rule.InputPointsPer1KTokens != 0 || rule.OutputPointsPer1KTokens != 0 || rule.PointsPerSecond < 1 || len(rule.ImageResolutionPrices) != 0 {
			return ErrInvalidPricing
		}
	default:
		return ErrInvalidPricing
	}
	return nil
}

func validResolution(value string) bool {
	var width, height int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%dx%d", &width, &height); err != nil {
		return false
	}
	return width >= 64 && width <= 16384 && height >= 64 && height <= 16384
}

func LoadModelPointPricing(ctx context.Context, q rowQuerier, providerModelID *uuid.UUID, providerProfileID, mode string) (ModelPointPricing, error) {
	var item ModelPointPricing
	var raw []byte
	err := q.QueryRow(ctx, `SELECT id,mode,input_points_per_1k_tokens,output_points_per_1k_tokens,points_per_second,minimum_points,image_resolution_prices,version FROM model_point_pricing_rules WHERE (($1::uuid IS NOT NULL AND provider_model_id=$1) OR ($1::uuid IS NULL AND provider_profile_id=$2)) AND mode=$3`, providerModelID, providerProfileID, mode).Scan(&item.ID, &item.Mode, &item.InputPointsPer1KTokens, &item.OutputPointsPer1KTokens, &item.PointsPerSecond, &item.MinimumPoints, &raw, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		var legacyCost int
		if providerModelID != nil {
			err = q.QueryRow(ctx, `SELECT estimated_cost_cents FROM provider_config_models WHERE id=$1 AND mode=$2 AND archived_at IS NULL`, *providerModelID, mode).Scan(&legacyCost)
		} else if providerProfileID != "" {
			err = q.QueryRow(ctx, `SELECT estimated_cost_cents FROM provider_profiles WHERE id=$1 AND mode=$2`, providerProfileID, mode).Scan(&legacyCost)
		}
		if err == nil {
			return fallbackModelPointPricing(mode, legacyCost), nil
		}
		return ModelPointPricing{}, ErrPricingNotConfigured
	}
	if err != nil {
		return ModelPointPricing{}, fmt.Errorf("load model point pricing: %w", err)
	}
	if err := json.Unmarshal(raw, &item.ImageResolutionPrices); err != nil {
		return ModelPointPricing{}, fmt.Errorf("decode model point pricing: %w", err)
	}
	if err := ValidateModelPointPricing(mode, item); err != nil {
		return ModelPointPricing{}, err
	}
	return item, nil
}

func fallbackModelPointPricing(mode string, legacyCost int) ModelPointPricing {
	minimum := maxInt(1, legacyCost*10)
	rule := ModelPointPricing{Mode: mode, MinimumPoints: minimum, Version: 1}
	switch mode {
	case "chat":
		rule.InputPointsPer1KTokens = maxInt(1, legacyCost*2)
		rule.OutputPointsPer1KTokens = maxInt(2, legacyCost*8)
	case "image":
		rule.ImageResolutionPrices = []ImageResolutionPrice{
			{Resolution: "1024x1024", Points: maxInt(10, legacyCost*10)},
			{Resolution: "1024x1536", Points: maxInt(15, legacyCost*15)},
			{Resolution: "1536x1024", Points: maxInt(15, legacyCost*15)},
		}
	case "video", "music":
		rule.PointsPerSecond = maxInt(1, legacyCost)
	}
	return rule
}

func UpsertModelPointPricing(ctx context.Context, tx pgx.Tx, actorID, providerModelID uuid.UUID, mode string, rule ModelPointPricing) (ModelPointPricing, error) {
	rule.Mode = mode
	if rule.ImageResolutionPrices == nil {
		rule.ImageResolutionPrices = make([]ImageResolutionPrice, 0)
	}
	if err := ValidateModelPointPricing(mode, rule); err != nil {
		return ModelPointPricing{}, err
	}
	raw, _ := json.Marshal(rule.ImageResolutionPrices)
	err := tx.QueryRow(ctx, `INSERT INTO model_point_pricing_rules(provider_model_id,mode,input_points_per_1k_tokens,output_points_per_1k_tokens,points_per_second,minimum_points,image_resolution_prices,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8) ON CONFLICT(provider_model_id) DO UPDATE SET mode=excluded.mode,input_points_per_1k_tokens=excluded.input_points_per_1k_tokens,output_points_per_1k_tokens=excluded.output_points_per_1k_tokens,points_per_second=excluded.points_per_second,minimum_points=excluded.minimum_points,image_resolution_prices=excluded.image_resolution_prices,version=model_point_pricing_rules.version+1,updated_by=excluded.updated_by,updated_at=now() RETURNING id,version`, providerModelID, mode, rule.InputPointsPer1KTokens, rule.OutputPointsPer1KTokens, rule.PointsPerSecond, rule.MinimumPoints, raw, actorID).Scan(&rule.ID, &rule.Version)
	if err != nil {
		return ModelPointPricing{}, fmt.Errorf("save model point pricing: %w", err)
	}
	return rule, nil
}

func EstimatePoints(rule ModelPointPricing, input MeterInput) (int64, string) {
	var points int64
	resolution := requestedImageResolution(input.AspectRatio)
	switch rule.Mode {
	case "chat":
		inputTokens := maxInt(1, (input.PromptCharacters+3)/4)
		outputTokens := 2048
		if input.ResponseLength == "short" {
			outputTokens = 512
		} else if input.ResponseLength == "detailed" {
			outputTokens = 4096
		}
		points = ceilPerThousand(inputTokens, int64(rule.InputPointsPer1KTokens)) + ceilPerThousand(outputTokens, int64(rule.OutputPointsPer1KTokens))
	case "image":
		points = int64(imagePoints(rule, resolution)) * int64(maxInt(1, input.ImageCount))
	case "video", "music":
		points = int64(maxInt(1, input.DurationSeconds)) * int64(rule.PointsPerSecond)
	}
	if points < int64(rule.MinimumPoints) {
		points = int64(rule.MinimumPoints)
	}
	return points, resolution
}

func ActualPoints(rule ModelPointPricing, estimate MeterInput, usage UsageMetrics) int64 {
	var points int64
	switch rule.Mode {
	case "chat":
		if !usage.ProviderReported || usage.InputTokens+usage.OutputTokens <= 0 {
			points, _ = EstimatePoints(rule, estimate)
		} else {
			points = ceilPerThousand(usage.InputTokens, int64(rule.InputPointsPer1KTokens)) + ceilPerThousand(usage.OutputTokens, int64(rule.OutputPointsPer1KTokens))
		}
	case "image":
		resolution := requestedImageResolution(estimate.AspectRatio)
		if usage.Width > 0 && usage.Height > 0 {
			resolution = fmt.Sprintf("%dx%d", usage.Width, usage.Height)
		}
		points = int64(imagePoints(rule, resolution)) * int64(maxInt(1, usage.ImageCount))
	case "video", "music":
		duration := usage.DurationSeconds
		if duration < 1 {
			duration = estimate.DurationSeconds
		}
		points = int64(maxInt(1, duration)) * int64(rule.PointsPerSecond)
	}
	if points < int64(rule.MinimumPoints) {
		points = int64(rule.MinimumPoints)
	}
	return points
}

func ReserveGenerationPointsTx(ctx context.Context, tx pgx.Tx, userID, generationID uuid.UUID, providerModelID *uuid.UUID, providerProfileID, mode string, input MeterInput) (int64, []byte, error) {
	rule, err := LoadModelPointPricing(ctx, tx, providerModelID, providerProfileID, mode)
	if err != nil {
		return 0, nil, err
	}
	var subscriptionID uuid.UUID
	var modelIncluded bool
	// External subscriptions use only accepted model IDs. A profile without a
	// concrete model ID cannot establish membership in that contract. Starter
	// and local wallet subscriptions retain their existing membership rules.
	err = tx.QueryRow(ctx, `SELECT s.id,
		CASE WHEN c.payment_id IS NOT NULL THEN $2::uuid IS NOT NULL AND $2=ANY(c.model_ids)
		ELSE NOT EXISTS(SELECT 1 FROM payment_intents i WHERE i.id=s.purchase_operation_id AND i.purpose='subscription')
		AND ($2::uuid IS NULL OR EXISTS(SELECT 1 FROM subscription_plan_models pm WHERE pm.plan_id=s.plan_id AND pm.provider_model_id=$2)) END
		FROM user_subscriptions s
		LEFT JOIN subscription_checkout_contracts c ON c.payment_id=s.purchase_operation_id AND c.buyer_id=s.user_id AND c.plan_id=s.plan_id
		AND c.price_cents=s.price_cents AND c.currency=s.currency AND c.included_points=s.granted_points
		WHERE s.user_id=$1 AND s.status='active' AND s.current_period_end>now()`, userID, providerModelID).Scan(&subscriptionID, &modelIncluded)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, ErrSubscriptionRequired
	}
	if err != nil {
		return 0, nil, err
	}
	if !modelIncluded {
		return 0, nil, ErrModelNotIncluded
	}
	estimate, resolution := EstimatePoints(rule, input)
	snapshot, _ := json.Marshal(pricingSnapshot{Rule: rule, RequestedResolution: resolution, EstimateInput: input})
	var balance, reserved int64
	if err := tx.QueryRow(ctx, `SELECT balance_points,reserved_points FROM point_accounts WHERE user_id=$1 FOR UPDATE`, userID).Scan(&balance, &reserved); errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, ErrPointAccountNotFound
	} else if err != nil {
		return 0, nil, err
	}
	if balance-reserved < estimate {
		return 0, nil, ErrInsufficientFunds
	}
	if _, err := tx.Exec(ctx, `UPDATE point_accounts SET reserved_points=reserved_points+$2,version=version+1,updated_at=now() WHERE user_id=$1`, userID, estimate); err != nil {
		return 0, nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO point_reservations(user_id,generation_id,subscription_id,held_points,pricing_snapshot) VALUES($1,$2,$3,$4,$5)`, userID, generationID, subscriptionID, estimate, snapshot); err != nil {
		return 0, nil, err
	}
	return estimate, snapshot, nil
}

func CaptureGenerationPointsTx(ctx context.Context, tx pgx.Tx, generationID uuid.UUID, usage UsageMetrics) (int64, []byte, error) {
	var userID uuid.UUID
	var held int64
	var status string
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT user_id,held_points,status,pricing_snapshot FROM point_reservations WHERE generation_id=$1 FOR UPDATE`, generationID).Scan(&userID, &held, &status, &raw); errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, ErrPointAccountNotFound
	} else if err != nil {
		return 0, nil, err
	}
	if status == "captured" {
		var charged int64
		var usageRaw []byte
		err := tx.QueryRow(ctx, `SELECT charged_points,usage_snapshot FROM point_reservations WHERE generation_id=$1`, generationID).Scan(&charged, &usageRaw)
		return charged, usageRaw, err
	}
	if status != "held" {
		return 0, nil, ErrReservationState
	}
	var snapshot pricingSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return 0, nil, err
	}
	charge := ActualPoints(snapshot.Rule, snapshot.EstimateInput, usage)
	usageRaw, _ := json.Marshal(usage)
	var balance, reserved int64
	if err := tx.QueryRow(ctx, `SELECT balance_points,reserved_points FROM point_accounts WHERE user_id=$1 FOR UPDATE`, userID).Scan(&balance, &reserved); err != nil {
		return 0, nil, err
	}
	// The balance includes every currently held reservation. Once this
	// reservation is captured, the other reservations must still remain
	// covered. Checking only balance >= charge could spend points held for a
	// different generation when actual usage exceeds this reservation's
	// estimate.
	availableAfterRelease := balance - reserved + held
	if reserved < held || availableAfterRelease < charge {
		return 0, nil, ErrInsufficientFunds
	}
	balance -= charge
	if _, err := tx.Exec(ctx, `UPDATE point_accounts SET balance_points=$2,reserved_points=reserved_points-$3,lifetime_spent_points=lifetime_spent_points+$4,version=version+1,updated_at=now() WHERE user_id=$1`, userID, balance, held, charge); err != nil {
		return 0, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE point_reservations SET status='captured',charged_points=$2,usage_snapshot=$3,updated_at=now() WHERE generation_id=$1`, generationID, charge, usageRaw); err != nil {
		return 0, nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO point_entries(user_id,operation_id,entry_type,direction,amount_points,balance_after_points,description,metadata) VALUES($1,$2,'generation_charge','debit',$3,$4,'AI generation',$5) ON CONFLICT(user_id,operation_id,entry_type,direction) DO NOTHING`, userID, generationID, charge, balance, map[string]any{"pricing": snapshot, "usage": usage}); err != nil {
		return 0, nil, err
	}
	return charge, usageRaw, nil
}

func ReleaseGenerationPointsTx(ctx context.Context, tx pgx.Tx, generationID uuid.UUID, reason string) error {
	var userID uuid.UUID
	var held int64
	var status string
	if err := tx.QueryRow(ctx, `SELECT user_id,held_points,status FROM point_reservations WHERE generation_id=$1 FOR UPDATE`, generationID).Scan(&userID, &held, &status); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if status == "released" {
		return nil
	}
	if status != "held" {
		return ErrReservationState
	}
	if result, err := tx.Exec(ctx, `UPDATE point_accounts SET reserved_points=reserved_points-$2,version=version+1,updated_at=now() WHERE user_id=$1 AND reserved_points >= $2`, userID, held); err != nil {
		return err
	} else if result.RowsAffected() != 1 {
		return ErrReservationState
	}
	_, err := tx.Exec(ctx, `UPDATE point_reservations SET status='released',release_reason=$2,updated_at=now() WHERE generation_id=$1`, generationID, strings.TrimSpace(reason))
	return err
}

func requestedImageResolution(aspect string) string {
	switch aspect {
	case "4:5":
		return "1024x1536"
	case "16:9":
		return "1536x1024"
	default:
		return "1024x1024"
	}
}
func imagePoints(rule ModelPointPricing, resolution string) int {
	nearestPoints, nearestDistance := rule.MinimumPoints, int64(1<<62)
	var targetWidth, targetHeight int
	_, _ = fmt.Sscanf(resolution, "%dx%d", &targetWidth, &targetHeight)
	targetPixels := int64(targetWidth) * int64(targetHeight)
	for _, item := range rule.ImageResolutionPrices {
		if item.Resolution == resolution {
			return item.Points
		}
		var width, height int
		_, _ = fmt.Sscanf(item.Resolution, "%dx%d", &width, &height)
		distance := int64(width)*int64(height) - targetPixels
		if distance < 0 {
			distance = -distance
		}
		if distance < nearestDistance {
			nearestDistance, nearestPoints = distance, item.Points
		}
	}
	return nearestPoints
}
func ceilPerThousand(value int, rate int64) int64 {
	if value <= 0 {
		return 0
	}
	return (int64(value)*rate + 999) / 1000
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func SortResolutionPrices(items []ImageResolutionPrice) {
	sort.Slice(items, func(i, j int) bool { return items[i].Resolution < items[j].Resolution })
}
