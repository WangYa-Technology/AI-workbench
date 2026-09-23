package payments

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// readTaskPaymentIdentityTx returns the immutable identity captured when a
// task checkout was created. A missing snapshot is legacy evidence and must
// never authorize an automatic transfer.
func readTaskPaymentIdentityTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) (ProductCheckoutIdentity, bool, error) {
	var merchant, store, endpoint, apiVersion, requestVersion *string
	var liveMode *bool
	err := tx.QueryRow(ctx, `
		SELECT task_original_merchant_id,task_original_store_id,task_original_live_mode,
		       task_original_endpoint,task_original_api_version,task_original_request_version
		FROM payment_intents WHERE id=$1 FOR SHARE`, paymentID).Scan(
		&merchant, &store, &liveMode, &endpoint, &apiVersion, &requestVersion)
	if err != nil {
		return ProductCheckoutIdentity{}, false, err
	}
	values := []*string{merchant, store, endpoint, apiVersion, requestVersion}
	complete := merchant != nil && liveMode != nil && endpoint != nil && apiVersion != nil && requestVersion != nil
	if !complete {
		for _, value := range values {
			if value != nil {
				return ProductCheckoutIdentity{}, false, errors.New("task payment identity snapshot is incomplete")
			}
		}
		if liveMode != nil {
			return ProductCheckoutIdentity{}, false, errors.New("task payment identity snapshot is incomplete")
		}
		return ProductCheckoutIdentity{}, false, nil
	}
	return ProductCheckoutIdentity{Provider: "", MerchantID: *merchant, StoreID: derefString(store), LiveMode: *liveMode,
		Endpoint: *endpoint, APIVersion: *apiVersion, RequestVersion: *requestVersion}, true, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func taskPaymentIdentityMatches(provider string, stored, current ProductCheckoutIdentity) bool {
	return stored.Provider == "" && provider == current.Provider && stored.MerchantID == current.MerchantID &&
		stored.StoreID == current.StoreID && stored.LiveMode == current.LiveMode &&
		stored.Endpoint == current.Endpoint && stored.APIVersion == current.APIVersion &&
		stored.RequestVersion == current.RequestVersion
}

func taskProviderIdentityMatchesConfig(provider persistedProviderConfig, identity ProductCheckoutIdentity, liveMode bool) bool {
	if identity.Provider != provider.Provider || identity.LiveMode != liveMode {
		return false
	}
	if provider.Provider == "waffo_pancake" {
		return provider.MerchantID == "" || provider.MerchantID == identity.MerchantID
	}
	return true
}
