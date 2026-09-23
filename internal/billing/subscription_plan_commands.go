package billing

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func planFinanceAuthorityTx(ctx context.Context, tx pgx.Tx, actor uuid.UUID, lock bool) error {
	query := `SELECT u.id FROM users u JOIN role_permissions rp ON rp.role=u.role
	 WHERE u.id=$1 AND u.status='active' AND rp.permission_id='admin:finance'`
	if lock {
		query += " FOR SHARE OF u,rp"
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, query, actor).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPlanForbidden
	}
	return err
}

func planCommandError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", "40P01":
			return ErrPlanConflict
		case "23505":
			return ErrInvalidPlan
		}
	}
	return err
}

// The caller holds the parent row lock while loading both terms and members.
func subscriptionPlanTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (SubscriptionPlan, error) {
	var p SubscriptionPlan
	err := tx.QueryRow(ctx, `SELECT p.id,p.tier_code,p.name,p.description,p.price_cents,p.currency,p.included_points,
	 p.billing_period_days,p.sort_order,p.active,p.version,p.created_at,p.updated_at,
	 ARRAY(SELECT m.provider_model_id FROM subscription_plan_models m WHERE m.plan_id=p.id ORDER BY m.provider_model_id)
	 FROM subscription_plans p WHERE p.id=$1`, id).Scan(&p.ID, &p.TierCode, &p.Name, &p.Description, &p.PriceCents, &p.Currency,
		&p.IncludedPoints, &p.BillingPeriodDays, &p.SortOrder, &p.Active, &p.Version, &p.CreatedAt, &p.UpdatedAt, &p.ModelIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubscriptionPlan{}, ErrInvalidPlan
	}
	return p, err
}

func recordPlanCommandTx(ctx context.Context, tx pgx.Tx, actor uuid.UUID, before *SubscriptionPlan, after SubscriptionPlan, requestID string) error {
	action := "billing.plan_created"
	if before != nil {
		action = "billing.plan_updated"
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
	 VALUES($1,$2,'subscription_plan',$3,$4,$5)`, actor, action, after.ID, requestID, map[string]any{"before": before, "after": after})
	return err
}
