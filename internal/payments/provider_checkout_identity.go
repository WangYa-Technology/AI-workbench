package payments

import "context"

// Bump these when changing checkout serialization, including connector defaults.
const stripeProductCheckoutVersion = "stripe-product-checkout-v1"
const waffoProductCheckoutVersion = "waffo-product-checkout-v1"
const waffoProductCheckoutAPI = "pancake-ts-0.19.1"

func (r *StripeRuntime) ProductCheckoutIdentity(ctx context.Context) (ProductCheckoutIdentity, error) {
	if r == nil || r.config.SecretKey == "" || r.config.BaseURL == "" || r.config.APIVersion == "" {
		return ProductCheckoutIdentity{}, newProviderFailure("payment_invalid_request", 0)
	}
	var account struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	if err := r.getJSON(ctx, "/account", &account); err != nil {
		return ProductCheckoutIdentity{}, err
	}
	if account.Object != "account" || !validStripeID(account.ID, "acct_") {
		return ProductCheckoutIdentity{}, newProviderFailure("payment_response_invalid", 0)
	}
	if err := r.verifyCredentialMode(ctx); err != nil {
		return ProductCheckoutIdentity{}, err
	}
	return ProductCheckoutIdentity{Provider: r.Provider(), MerchantID: account.ID, LiveMode: r.config.LiveMode,
		Endpoint: r.config.BaseURL, APIVersion: r.config.APIVersion, RequestVersion: stripeProductCheckoutVersion}, nil
}

// Account objects have no livemode property. Authenticate the credential mode
// through Balance before creating accounts or financial objects.
func (r *StripeRuntime) verifyCredentialMode(ctx context.Context) error {
	var balance struct {
		Object   string `json:"object"`
		LiveMode *bool  `json:"livemode"`
	}
	if err := r.getJSON(ctx, "/balance", &balance); err != nil {
		return err
	}
	if balance.Object != "balance" || balance.LiveMode == nil || *balance.LiveMode != r.config.LiveMode {
		return newProviderFailure("payment_response_invalid", 0)
	}
	return nil
}

func (r *WaffoRuntime) ProductCheckoutIdentity(ctx context.Context) (ProductCheckoutIdentity, error) {
	if r == nil || !r.validConnector() || !oneOf(r.config.Environment, "test", "prod") {
		return ProductCheckoutIdentity{}, newProviderFailure("payment_invalid_request", 0)
	}
	var response struct {
		ProductCheckoutIdentity
		LiveMode *bool `json:"liveMode"`
	}
	if err := r.postJSON(ctx, "/checkout/identity", struct{}{}, &response); err != nil {
		return ProductCheckoutIdentity{}, err
	}
	if response.LiveMode == nil {
		return ProductCheckoutIdentity{}, newProviderFailure("payment_response_invalid", 0)
	}
	identity := response.ProductCheckoutIdentity
	identity.LiveMode = *response.LiveMode
	if identity.Provider != r.Provider() || identity.MerchantID == "" || identity.StoreID == "" ||
		identity.LiveMode != (r.config.Environment == "prod") || identity.APIVersion != waffoProductCheckoutAPI ||
		identity.RequestVersion != waffoProductCheckoutVersion || (r.config.StoreID != "" && identity.StoreID != r.config.StoreID) {
		return ProductCheckoutIdentity{}, newProviderFailure("payment_response_invalid", 0)
	}
	// The authenticated connector asserts the merchant used by its SDK. Its
	// address is separately bound by the API and not accepted from the response.
	identity.Endpoint = r.config.ConnectorURL
	return identity, nil
}
