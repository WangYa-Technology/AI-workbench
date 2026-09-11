package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
)

func TestPaymentProviderDeploymentStatusIsAppliedWithoutPersistingSecrets(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Payments Admin','admin','active')`, adminID, adminID.String()+"@test.local", "payments_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_configs(provider,enabled,environment,merchant_id,store_id,product_id_onetime) VALUES('waffo_pancake',false,'test','MER_test','STO_test','PROD_test')`); err != nil {
		t.Fatal(err)
	}
	deploymentStatus := admin.PaymentProviderDeploymentStatus{SecretConfigured: true, ConnectorConfigured: true, Environment: "test"}
	deployment := map[string]admin.PaymentProviderDeploymentStatus{"waffo_pancake": deploymentStatus}
	items, err := admin.NewService(pool, true).ListPaymentProviderConfigsWithDeployment(ctx, deployment)
	if err != nil {
		t.Fatal(err)
	}
	var found admin.PaymentProviderConfig
	for _, item := range items {
		if item.Provider == "waffo_pancake" {
			found = item
		}
	}
	if !found.SecretConfigured || !found.ConnectorConfigured {
		t.Fatalf("deployment readiness was not reflected in the Admin projection: %#v", found)
	}
	if _, err := admin.NewService(pool, true).UpdatePaymentProviderConfigWithDeployment(ctx, adminID, "waffo_pancake", admin.PaymentProviderConfigUpdate{Enabled: boolPtr(true)}, "provider-config-test", &deploymentStatus); err != nil {
		t.Fatalf("test-environment Provider activation failed: %v", err)
	}
	var persistedSecret, persistedConnector bool
	if err := pool.QueryRow(ctx, `SELECT secret_configured,connector_configured FROM payment_provider_configs WHERE provider='waffo_pancake'`).Scan(&persistedSecret, &persistedConnector); err != nil {
		t.Fatal(err)
	}
	if !persistedSecret || !persistedConnector {
		t.Fatalf("deployment status was not persisted as non-secret readiness metadata: secret=%t connector=%t", persistedSecret, persistedConnector)
	}
}

func TestPaymentProviderProductionActivationRequiresMatchingDeployment(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Payments Admin','admin','active')`, adminID, adminID.String()+"@test.local", "payments_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	deployment := admin.PaymentProviderDeploymentStatus{SecretConfigured: true, ConnectorConfigured: true, Environment: "test"}
	_, err := service.UpdatePaymentProviderConfigWithDeployment(ctx, adminID, "waffo_pancake", admin.PaymentProviderConfigUpdate{Enabled: boolPtr(true), Environment: stringPtr("prod"), MerchantID: stringPtr("MER_test"), StoreID: stringPtr("STO_test"), ProductIDOnetime: stringPtr("PROD_test")}, "provider-config-prod-test", &deployment)
	if !errors.Is(err, admin.ErrProviderConfig) {
		t.Fatalf("expected production/environment mismatch to fail closed, got %v", err)
	}
}

func boolPtr(value bool) *bool       { return &value }
func stringPtr(value string) *string { return &value }
