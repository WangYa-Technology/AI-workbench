package payments

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func saveSubscriptionContractTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	// The caller holds the plan's share lock through the intent transaction.
	result, err := tx.Exec(ctx, `INSERT INTO subscription_checkout_contracts
		(payment_id,buyer_id,plan_id,plan_name,tier_code,description,price_cents,currency,included_points,billing_period_days,model_ids)
		SELECT i.id,i.payer_id,p.id,p.name,p.tier_code,p.description,p.price_cents,p.currency,p.included_points,p.billing_period_days,
		ARRAY(SELECT m.provider_model_id FROM subscription_plan_models m WHERE m.plan_id=p.id ORDER BY m.provider_model_id)
		FROM payment_intents i JOIN subscription_plans p ON p.id=i.resource_id
		WHERE i.id=$1 AND i.purpose='subscription' AND p.active AND p.price_cents=i.amount_cents AND p.currency=i.currency`, paymentID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrCheckoutReconciliation
	}
	return nil
}

type subscriptionContract struct {
	Name, Tier string
	Points     int64
	Days       int
}

func loadSubscriptionContractTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) (subscriptionContract, error) {
	var c subscriptionContract
	err := tx.QueryRow(ctx, `SELECT c.plan_name,c.tier_code,c.included_points,c.billing_period_days
		FROM subscription_checkout_contracts c JOIN payment_intents i ON i.id=c.payment_id
		WHERE i.id=$1 AND i.purpose='subscription' AND c.buyer_id=i.payer_id AND c.plan_id=i.resource_id
		AND c.price_cents=i.amount_cents AND c.currency=i.currency`, paymentID).Scan(&c.Name, &c.Tier, &c.Points, &c.Days)
	if errors.Is(err, pgx.ErrNoRows) {
		return subscriptionContract{}, ErrCheckoutReconciliation
	}
	return c, err
}
