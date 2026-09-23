package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PaymentProviderConfig struct {
	ID                    uuid.UUID `json:"id"`
	Provider              string    `json:"provider"`
	Enabled               bool      `json:"enabled"`
	Environment           string    `json:"environment"`
	MerchantID            string    `json:"merchantId"`
	StoreID               string    `json:"storeId"`
	ProductIDOnetime      string    `json:"productIdOnetime"`
	ProductIDSubscription string    `json:"productIdSubscription"`
	SecretConfigured      bool      `json:"secretConfigured"`
	ConnectorConfigured   bool      `json:"connectorConfigured"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

type PaymentProviderConfigUpdate struct {
	Enabled               *bool   `json:"enabled,omitempty"`
	Environment           *string `json:"environment,omitempty"`
	MerchantID            *string `json:"merchantId,omitempty"`
	StoreID               *string `json:"storeId,omitempty"`
	ProductIDOnetime      *string `json:"productIdOnetime,omitempty"`
	ProductIDSubscription *string `json:"productIdSubscription,omitempty"`
}

// PaymentProviderDeploymentStatus is supplied by the API process from its
// deployment configuration. Secret material is never included; these flags
// only describe whether the corresponding runtime boundary is provisioned.
type PaymentProviderDeploymentStatus struct {
	SecretConfigured    bool
	ConnectorConfigured bool
	Environment         string
}

func (s *Service) ListPaymentProviderConfigs(ctx context.Context) ([]PaymentProviderConfig, error) {
	return s.ListPaymentProviderConfigsWithDeployment(ctx, nil)
}

func (s *Service) ListPaymentProviderConfigsWithDeployment(ctx context.Context, deployment map[string]PaymentProviderDeploymentStatus) ([]PaymentProviderConfig, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,provider,enabled,environment,merchant_id,store_id,product_id_onetime,product_id_subscription,secret_configured,connector_configured,created_at,updated_at FROM payment_provider_configs ORDER BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]PaymentProviderConfig, 0, 3)
	for rows.Next() {
		var item PaymentProviderConfig
		if err := rows.Scan(&item.ID, &item.Provider, &item.Enabled, &item.Environment, &item.MerchantID, &item.StoreID, &item.ProductIDOnetime, &item.ProductIDSubscription, &item.SecretConfigured, &item.ConnectorConfigured, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if status, ok := deployment[item.Provider]; ok {
			item.SecretConfigured = status.SecretConfigured
			item.ConnectorConfigured = status.ConnectorConfigured
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(items))
	for _, item := range items {
		known[item.Provider] = true
	}
	for _, provider := range []string{"stripe", "waffo_pancake", "epay"} {
		if known[provider] {
			continue
		}
		item := PaymentProviderConfig{ID: uuid.Nil, Provider: provider, Environment: "test"}
		if status, ok := deployment[provider]; ok {
			item.SecretConfigured = status.SecretConfigured
			item.ConnectorConfigured = status.ConnectorConfigured
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) UpdatePaymentProviderConfig(ctx context.Context, actorID uuid.UUID, provider string, input PaymentProviderConfigUpdate, requestID string) (PaymentProviderConfig, error) {
	return s.UpdatePaymentProviderConfigWithDeployment(ctx, actorID, provider, input, requestID, nil)
}

func (s *Service) UpdatePaymentProviderConfigWithDeployment(ctx context.Context, actorID uuid.UUID, provider string, input PaymentProviderConfigUpdate, requestID string, deployment *PaymentProviderDeploymentStatus) (PaymentProviderConfig, error) {
	item, err := s.updatePaymentProviderConfigWithDeployment(ctx, actorID, provider, input, requestID, deployment)
	return item, financeCommandError(err)
}

func (s *Service) updatePaymentProviderConfigWithDeployment(ctx context.Context, actorID uuid.UUID, provider string, input PaymentProviderConfigUpdate, requestID string, deployment *PaymentProviderDeploymentStatus) (PaymentProviderConfig, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if actorID == uuid.Nil || !oneOfAdmin(provider, "stripe", "waffo_pancake", "epay") {
		return PaymentProviderConfig{}, ErrInvalid
	}
	// Serialize all provider switches, including first-time rows. Read the
	// current configuration only after acquiring the lock so partial updates
	// cannot restore stale fields or enable two providers concurrently.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return PaymentProviderConfig{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := financeAuthorityTx(ctx, tx, actorID, false); err != nil {
		return PaymentProviderConfig{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin:payment-provider-config',0))`); err != nil {
		return PaymentProviderConfig{}, err
	}
	current := PaymentProviderConfig{Provider: provider, Environment: "test"}
	var existingID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id,enabled,environment,merchant_id,store_id,product_id_onetime,product_id_subscription,secret_configured,connector_configured FROM payment_provider_configs WHERE provider=$1 FOR UPDATE`, provider).Scan(
		&existingID, &current.Enabled, &current.Environment, &current.MerchantID, &current.StoreID, &current.ProductIDOnetime, &current.ProductIDSubscription, &current.SecretConfigured, &current.ConnectorConfigured)
	if errors.Is(err, pgx.ErrNoRows) {
		current.ID = uuid.New()
	} else if err != nil {
		return PaymentProviderConfig{}, err
	} else {
		current.ID = existingID
	}
	if input.Enabled != nil {
		current.Enabled = *input.Enabled
	}
	if input.Environment != nil {
		current.Environment = strings.ToLower(strings.TrimSpace(*input.Environment))
	}
	if input.MerchantID != nil {
		current.MerchantID = strings.TrimSpace(*input.MerchantID)
	}
	if input.StoreID != nil {
		current.StoreID = strings.TrimSpace(*input.StoreID)
	}
	if input.ProductIDOnetime != nil {
		current.ProductIDOnetime = strings.TrimSpace(*input.ProductIDOnetime)
	}
	if input.ProductIDSubscription != nil {
		current.ProductIDSubscription = strings.TrimSpace(*input.ProductIDSubscription)
	}
	if deployment != nil {
		current.SecretConfigured = deployment.SecretConfigured
		current.ConnectorConfigured = deployment.ConnectorConfigured
		if (provider == "waffo_pancake" || provider == "stripe") && current.Enabled && deployment.Environment != "" && current.Environment != deployment.Environment {
			return PaymentProviderConfig{}, ErrProviderConfig
		}
	}
	if !oneOfAdmin(current.Environment, "test", "prod") || len(current.MerchantID) > 255 || len(current.StoreID) > 255 || len(current.ProductIDOnetime) > 255 || len(current.ProductIDSubscription) > 255 || strings.ContainsAny(current.MerchantID+current.StoreID+current.ProductIDOnetime+current.ProductIDSubscription, "\r\n") {
		return PaymentProviderConfig{}, ErrInvalid
	}
	// Production activation is an explicit, auditable action and still needs
	// deployment secrets to be present before the runtime can process payments.
	if current.Enabled && current.Environment == "prod" && (!current.SecretConfigured || (provider == "waffo_pancake" && !current.ConnectorConfigured)) {
		return PaymentProviderConfig{}, ErrProviderConfig
	}
	configurationJSON, err := json.Marshal(map[string]any{"provider": provider, "enabled": current.Enabled, "environment": current.Environment, "merchantId": current.MerchantID, "storeId": current.StoreID, "productIdOnetime": current.ProductIDOnetime, "productIdSubscription": current.ProductIDSubscription})
	if err != nil {
		return PaymentProviderConfig{}, err
	}
	if err := financeAuthorityTx(ctx, tx, actorID, true); err != nil {
		return PaymentProviderConfig{}, err
	}
	if current.Enabled {
		if _, err := tx.Exec(ctx, `UPDATE payment_provider_configs SET enabled=false,updated_at=now(),updated_by=$1 WHERE provider<>$2 AND enabled=true`, actorID, provider); err != nil {
			return PaymentProviderConfig{}, err
		}
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO payment_provider_configs(id,provider,enabled,environment,merchant_id,store_id,product_id_onetime,product_id_subscription,secret_configured,connector_configured,created_by,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)
		ON CONFLICT(provider) DO UPDATE SET enabled=EXCLUDED.enabled,environment=EXCLUDED.environment,merchant_id=EXCLUDED.merchant_id,store_id=EXCLUDED.store_id,product_id_onetime=EXCLUDED.product_id_onetime,product_id_subscription=EXCLUDED.product_id_subscription,secret_configured=EXCLUDED.secret_configured,connector_configured=EXCLUDED.connector_configured,updated_by=EXCLUDED.updated_by,updated_at=now() RETURNING id`,
		current.ID, provider, current.Enabled, current.Environment, current.MerchantID, current.StoreID, current.ProductIDOnetime, current.ProductIDSubscription, current.SecretConfigured, current.ConnectorConfigured, actorID).Scan(&current.ID); err != nil {
		return PaymentProviderConfig{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,'admin.payment_provider_config_updated','payment_provider_config',$2,'Administrator updated payment Provider configuration',$3,$4)`, actorID, current.ID, requestID, configurationJSON); err != nil {
		return PaymentProviderConfig{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PaymentProviderConfig{}, err
	}
	return s.getPaymentProviderConfig(ctx, provider)
}

func (s *Service) getPaymentProviderConfig(ctx context.Context, provider string) (PaymentProviderConfig, error) {
	var item PaymentProviderConfig
	err := s.pool.QueryRow(ctx, `SELECT id,provider,enabled,environment,merchant_id,store_id,product_id_onetime,product_id_subscription,secret_configured,connector_configured,created_at,updated_at FROM payment_provider_configs WHERE provider=$1`, provider).Scan(&item.ID, &item.Provider, &item.Enabled, &item.Environment, &item.MerchantID, &item.StoreID, &item.ProductIDOnetime, &item.ProductIDSubscription, &item.SecretConfigured, &item.ConnectorConfigured, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}
