package payments

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func isStripeDisputeEvent(eventType string) bool {
	return oneOf(eventType, "charge.dispute.created", "charge.dispute.updated", "charge.dispute.closed")
}

// bindStripeDisputePaymentTx deliberately requires the complete immutable
// provider tuple. It returns no binding for zero or multiple matches so the
// evidence can be reviewed without freezing an unrelated seller's funds.
func bindStripeDisputePaymentTx(ctx context.Context, tx pgx.Tx, event minimizedProviderEvent) (*uuid.UUID, *uuid.UUID, *string, error) {
	if event.ProviderPaymentID == nil || event.ProviderChargeID == nil || event.AmountCents == nil || event.Currency == nil {
		return nil, nil, nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id,resource_id
		FROM payment_intents
		WHERE provider='stripe' AND purpose='product' AND live_mode=$1
		  AND provider_payment_id=$2 AND provider_charge_id=$3
		  AND amount_cents=$4 AND currency=$5
		ORDER BY id
		LIMIT 2`, event.LiveMode, *event.ProviderPaymentID, *event.ProviderChargeID, *event.AmountCents, *event.Currency)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	var matches []struct{ paymentID, resourceID uuid.UUID }
	for rows.Next() {
		var item struct{ paymentID, resourceID uuid.UUID }
		if err := rows.Scan(&item.paymentID, &item.resourceID); err != nil {
			return nil, nil, nil, err
		}
		matches = append(matches, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	if len(matches) != 1 {
		return nil, nil, nil, nil
	}
	paymentID, resourceID, purpose := matches[0].paymentID, matches[0].resourceID, "product"
	return &paymentID, &resourceID, &purpose, nil
}
