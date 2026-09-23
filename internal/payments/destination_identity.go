package payments

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// destinationIdentityMatchesTx only accepts a destination that carries the
// same authenticated merchant and environment as the original payment or
// onboarding command. An unbound historical/manual destination is deliberately
// not treated as proof of ownership.
func destinationIdentityMatchesTx(ctx context.Context, tx pgx.Tx, provider string, userID uuid.UUID, destinationID string, identity ProductCheckoutIdentity) (bool, error) {
	var merchant, store, endpoint, apiVersion, requestVersion *string
	var liveMode *bool
	err := tx.QueryRow(ctx, `
		SELECT original_merchant_id,original_store_id,original_live_mode,original_endpoint,
		       original_api_version,original_request_version
		FROM payment_destinations
		WHERE provider=$1 AND user_id=$2 AND destination_id=$3
		FOR SHARE`, provider, userID, destinationID).Scan(&merchant, &store, &liveMode, &endpoint, &apiVersion, &requestVersion)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if merchant == nil || liveMode == nil || endpoint == nil || apiVersion == nil || requestVersion == nil {
		return false, nil
	}
	storeValue := ""
	if store != nil {
		storeValue = *store
	}
	return *merchant == identity.MerchantID && storeValue == identity.StoreID &&
		*liveMode == identity.LiveMode && *endpoint == identity.Endpoint &&
		*apiVersion == identity.APIVersion && *requestVersion == identity.RequestVersion, nil
}
