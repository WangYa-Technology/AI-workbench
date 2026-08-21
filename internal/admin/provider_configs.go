package admin

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/jackc/pgx/v5"
)

var ErrProviderSecret = errors.New("provider credentials are not configured for secure storage")
var ErrProviderSync = errors.New("provider model sync is unavailable")

type ProviderModel struct {
	ID                 uuid.UUID                  `json:"id"`
	ProviderID         uuid.UUID                  `json:"providerId"`
	Mode               string                     `json:"mode"`
	ModelName          string                     `json:"modelName"`
	DisplayName        string                     `json:"displayName"`
	Description        string                     `json:"description"`
	EstimatedCostCents int                        `json:"estimatedCostCents"`
	PointPricing       billing.ModelPointPricing  `json:"pointPricing"`
	Capabilities       creation.ModelCapabilities `json:"capabilities"`
	AdminEnabled       bool                       `json:"adminEnabled"`
	Archived           bool                       `json:"archived"`
	CreatedAt          time.Time                  `json:"createdAt"`
	UpdatedAt          time.Time                  `json:"updatedAt"`
}

type ProviderConfig struct {
	ID                   uuid.UUID       `json:"id"`
	Name                 string          `json:"name"`
	Protocol             string          `json:"protocol"`
	Endpoint             string          `json:"endpoint"`
	RuntimeProvider      string          `json:"runtimeProvider"`
	CredentialConfigured bool            `json:"credentialConfigured"`
	CredentialHint       string          `json:"credentialHint,omitempty"`
	AdminEnabled         bool            `json:"adminEnabled"`
	Archived             bool            `json:"archived"`
	Models               []ProviderModel `json:"models"`
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`
}

type ProviderConfigCreate struct {
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	Endpoint     string `json:"endpoint"`
	APIKey       string `json:"apiKey"`
	AdminEnabled bool   `json:"adminEnabled"`
}

type ProviderConfigUpdate struct {
	Name         *string `json:"name,omitempty"`
	Protocol     *string `json:"protocol,omitempty"`
	Endpoint     *string `json:"endpoint,omitempty"`
	APIKey       *string `json:"apiKey,omitempty"`
	AdminEnabled *bool   `json:"adminEnabled,omitempty"`
}

type ProviderModelSync struct {
	Mode      string `json:"mode"`
	Imported  int    `json:"imported"`
	Available int    `json:"available"`
}

type ProviderModelCreate struct {
	Mode               string                     `json:"mode"`
	ModelName          string                     `json:"modelName"`
	DisplayName        string                     `json:"displayName"`
	Description        string                     `json:"description"`
	EstimatedCostCents int                        `json:"estimatedCostCents"`
	PointPricing       billing.ModelPointPricing  `json:"pointPricing"`
	Capabilities       creation.ModelCapabilities `json:"capabilities"`
	AdminEnabled       bool                       `json:"adminEnabled"`
}

type ProviderModelUpdate struct {
	Mode               *string                     `json:"mode,omitempty"`
	ModelName          *string                     `json:"modelName,omitempty"`
	DisplayName        *string                     `json:"displayName,omitempty"`
	Description        *string                     `json:"description,omitempty"`
	EstimatedCostCents *int                        `json:"estimatedCostCents,omitempty"`
	PointPricing       *billing.ModelPointPricing  `json:"pointPricing,omitempty"`
	Capabilities       *creation.ModelCapabilities `json:"capabilities,omitempty"`
	AdminEnabled       *bool                       `json:"adminEnabled,omitempty"`
}

var providerProtocols = map[string]bool{
	"openai_responses": true, "openai_chat_completions": true, "openai_images": true,
	"hctopup_async_image": true, "custom": true,
}

func validateProviderConfigInput(name, protocol, endpoint, apiKey string) error {
	name, protocol, endpoint, apiKey = strings.TrimSpace(name), strings.TrimSpace(protocol), strings.TrimSpace(endpoint), strings.TrimSpace(apiKey)
	if len(name) < 2 || len(name) > 120 || !providerProtocols[protocol] || len(apiKey) < 8 || strings.ContainsAny(apiKey, "\r\n") {
		return ErrInvalid
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ErrInvalid
	}
	return nil
}

func validateProviderModelInput(mode, modelName, displayName, description string, cost int) error {
	if !oneOfAdmin(mode, "chat", "image", "video", "music") || len(strings.TrimSpace(modelName)) < 1 || len(strings.TrimSpace(modelName)) > 160 || len(strings.TrimSpace(displayName)) < 2 || len(strings.TrimSpace(displayName)) > 120 || len(strings.TrimSpace(description)) > 1000 || cost < 0 || cost > 1000000 {
		return ErrInvalid
	}
	return nil
}

func normalizeProviderModelCapabilities(mode string, value creation.ModelCapabilities) creation.ModelCapabilities {
	if value.AspectRatios == nil && value.Qualities == nil && value.DurationSeconds == nil && value.OutputFormats == nil && value.ResultFormats == nil && value.ReferenceKinds == nil && !value.SupportsMask {
		return creation.DefaultModelCapabilities(mode)
	}
	return value
}

func validateProviderModelCapabilities(mode string, value creation.ModelCapabilities) error {
	value = normalizeProviderModelCapabilities(mode, value)
	if len(value.OutputFormats) == 0 || len(value.ResultFormats) == 0 {
		return ErrInvalid
	}
	all := append([]string{}, value.AspectRatios...)
	all = append(all, value.Qualities...)
	all = append(all, value.OutputFormats...)
	all = append(all, value.ResultFormats...)
	all = append(all, value.ReferenceKinds...)
	for _, item := range all {
		if len(strings.TrimSpace(item)) == 0 || len(item) > 40 {
			return ErrInvalid
		}
	}
	for _, item := range value.DurationSeconds {
		if item < 1 || item > 3600 {
			return ErrInvalid
		}
	}
	return nil
}

func defaultModelPointPricing(mode string, legacyCost int) billing.ModelPointPricing {
	minimum := legacyCost * 10
	if minimum < 1 {
		minimum = 1
	}
	rule := billing.ModelPointPricing{Mode: mode, MinimumPoints: minimum}
	switch mode {
	case "chat":
		rule.InputPointsPer1KTokens = maxAdmin(1, legacyCost*2)
		rule.OutputPointsPer1KTokens = maxAdmin(2, legacyCost*8)
	case "image":
		rule.ImageResolutionPrices = []billing.ImageResolutionPrice{
			{Resolution: "1024x1024", Points: maxAdmin(10, legacyCost*10)},
			{Resolution: "1024x1536", Points: maxAdmin(15, legacyCost*15)},
			{Resolution: "1536x1024", Points: maxAdmin(15, legacyCost*15)},
		}
	case "video", "music":
		rule.PointsPerSecond = maxAdmin(1, legacyCost)
	}
	return rule
}

func maxAdmin(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func runtimeProviderForProtocol(protocol string) string {
	switch protocol {
	case "openai_responses", "openai_chat_completions", "openai_images", "hctopup_async_image":
		return "openai"
	default:
		return "custom"
	}
}

func (s *Service) ListProviderConfigs(ctx context.Context) ([]ProviderConfig, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,protocol,endpoint,runtime_provider,credential_ciphertext IS NOT NULL,COALESCE(credential_hint,''),admin_enabled,created_at,updated_at FROM provider_configs WHERE archived_at IS NULL ORDER BY lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ProviderConfig, 0)
	for rows.Next() {
		var item ProviderConfig
		if err := rows.Scan(&item.ID, &item.Name, &item.Protocol, &item.Endpoint, &item.RuntimeProvider, &item.CredentialConfigured, &item.CredentialHint, &item.AdminEnabled, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Models, err = s.listProviderModels(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// SyncProviderModels fetches an OpenAI-compatible /models catalog and imports
// new entries as disabled models. Capabilities are intentionally left at the
// mode baseline until an administrator reviews them.
func (s *Service) SyncProviderModels(ctx context.Context, actorID, providerID uuid.UUID, mode, _ string) (ProviderConfig, error) {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if !oneOfAdmin(mode, "chat", "image", "video", "music") || len(s.providerSecretKey) == 0 {
		return ProviderConfig{}, ErrInvalid
	}
	var endpoint, protocol string
	var nonce, ciphertext []byte
	if err := s.pool.QueryRow(ctx, `SELECT endpoint,protocol,credential_nonce,credential_ciphertext FROM provider_configs WHERE id=$1 AND archived_at IS NULL AND admin_enabled=true`, providerID).Scan(&endpoint, &protocol, &nonce, &ciphertext); errors.Is(err, pgx.ErrNoRows) {
		return ProviderConfig{}, ErrNotFound
	} else if err != nil {
		return ProviderConfig{}, err
	}
	if protocol == "custom" || protocol == "hctopup_async_image" {
		return ProviderConfig{}, ErrProviderSync
	}
	secret, err := decryptProviderSecret(s.providerSecretKey, nonce, ciphertext)
	if err != nil || secret == "" {
		return ProviderConfig{}, ErrProviderSecret
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/models", nil)
	if err != nil {
		return ProviderConfig{}, ErrProviderSync
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return ProviderConfig{}, ErrProviderSync
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return ProviderConfig{}, ErrProviderSync
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ProviderConfig{}, ErrProviderSync
	}
	modelIDs := make([]string, 0, len(payload.Data)+len(payload.Models))
	seen := make(map[string]struct{})
	for _, item := range append(payload.Data, payload.Models...) {
		id := strings.TrimSpace(item.ID)
		if id == "" || len(id) > 160 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		modelIDs = append(modelIDs, id)
	}
	if len(modelIDs) == 0 {
		return ProviderConfig{}, ErrProviderSync
	}
	capabilitiesJSON, _ := json.Marshal(creation.DefaultModelCapabilities(mode))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProviderConfig{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	imported := 0
	for _, modelID := range modelIDs {
		var importedModelID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO provider_config_models(provider_id,mode,model_name,display_name,description,estimated_cost_cents,capabilities,admin_enabled,created_by,updated_by)
			VALUES($1,$2,$3,$3,'Imported from upstream model catalog.',0,$4,false,$5,$5)
			ON CONFLICT(provider_id,mode,model_name) DO UPDATE SET archived_at=NULL,updated_at=now(),updated_by=EXCLUDED.updated_by
			RETURNING id`, providerID, mode, modelID, capabilitiesJSON, actorID).Scan(&importedModelID)
		if err != nil {
			return ProviderConfig{}, err
		}
		if _, err := billing.UpsertModelPointPricing(ctx, tx, actorID, importedModelID, mode, defaultModelPointPricing(mode, 0)); err != nil {
			return ProviderConfig{}, err
		}
		imported++
	}
	if err := tx.Commit(ctx); err != nil {
		return ProviderConfig{}, err
	}
	return s.providerConfig(ctx, providerID)
}

func (s *Service) listProviderModels(ctx context.Context, providerID uuid.UUID) ([]ProviderModel, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,provider_id,mode,model_name,display_name,description,estimated_cost_cents,capabilities,admin_enabled,archived_at IS NOT NULL,created_at,updated_at FROM provider_config_models WHERE provider_id=$1 AND archived_at IS NULL ORDER BY mode,lower(display_name),id`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ProviderModel, 0)
	for rows.Next() {
		var item ProviderModel
		var raw []byte
		if err := rows.Scan(&item.ID, &item.ProviderID, &item.Mode, &item.ModelName, &item.DisplayName, &item.Description, &item.EstimatedCostCents, &raw, &item.AdminEnabled, &item.Archived, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Capabilities = creation.ParseModelCapabilities(item.Mode, raw)
		item.PointPricing, err = billing.LoadModelPointPricing(ctx, s.pool, &item.ID, "", item.Mode)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateProviderConfig(ctx context.Context, actorID uuid.UUID, input ProviderConfigCreate, _ string) (ProviderConfig, error) {
	if validateProviderConfigInput(input.Name, input.Protocol, input.Endpoint, input.APIKey) != nil || len(s.providerSecretKey) == 0 {
		return ProviderConfig{}, ErrInvalid
	}
	nonce, ciphertext, err := encryptProviderSecret(s.providerSecretKey, input.APIKey)
	if err != nil {
		return ProviderConfig{}, ErrProviderSecret
	}
	name, protocol, endpoint := strings.TrimSpace(input.Name), strings.TrimSpace(input.Protocol), strings.TrimRight(strings.TrimSpace(input.Endpoint), "/")
	item := ProviderConfig{ID: uuid.New(), Name: name, Protocol: protocol, Endpoint: endpoint, RuntimeProvider: runtimeProviderForProtocol(protocol), CredentialConfigured: true, CredentialHint: secretHint(input.APIKey), AdminEnabled: input.AdminEnabled, Models: []ProviderModel{}}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProviderConfig{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `INSERT INTO provider_configs(id,name,protocol,endpoint,runtime_provider,credential_nonce,credential_ciphertext,credential_hint,admin_enabled,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, item.ID, item.Name, item.Protocol, item.Endpoint, item.RuntimeProvider, nonce, ciphertext, item.CredentialHint, item.AdminEnabled, actorID); err != nil {
		return ProviderConfig{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProviderConfig{}, err
	}
	return s.providerConfig(ctx, item.ID)
}

func (s *Service) UpdateProviderConfig(ctx context.Context, actorID uuid.UUID, id uuid.UUID, input ProviderConfigUpdate, _ string) (ProviderConfig, error) {
	var name, protocol, endpoint string
	var enabled bool
	if err := s.pool.QueryRow(ctx, `SELECT name,protocol,endpoint,admin_enabled FROM provider_configs WHERE id=$1 AND archived_at IS NULL`, id).Scan(&name, &protocol, &endpoint, &enabled); errors.Is(err, pgx.ErrNoRows) {
		return ProviderConfig{}, ErrNotFound
	} else if err != nil {
		return ProviderConfig{}, err
	}
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	if input.Protocol != nil {
		protocol = strings.TrimSpace(*input.Protocol)
	}
	if input.Endpoint != nil {
		endpoint = strings.TrimRight(strings.TrimSpace(*input.Endpoint), "/")
	}
	if len(name) < 2 || len(name) > 120 || !providerProtocols[protocol] {
		return ProviderConfig{}, ErrInvalid
	}
	if parsed, err := url.Parse(endpoint); err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ProviderConfig{}, ErrInvalid
	}
	var nonce, ciphertext []byte
	var hint string
	var encryptErr error
	if input.APIKey != nil && strings.TrimSpace(*input.APIKey) != "" {
		if len(s.providerSecretKey) == 0 {
			return ProviderConfig{}, ErrProviderSecret
		}
		nonce, ciphertext, encryptErr = encryptProviderSecret(s.providerSecretKey, strings.TrimSpace(*input.APIKey))
		if encryptErr != nil {
			return ProviderConfig{}, ErrProviderSecret
		}
		hint = secretHint(strings.TrimSpace(*input.APIKey))
	}
	if input.AdminEnabled != nil {
		enabled = *input.AdminEnabled
	}
	_, err := s.pool.Exec(ctx, `UPDATE provider_configs SET name=$2,protocol=$3,endpoint=$4,runtime_provider=$5,admin_enabled=$6,credential_nonce=CASE WHEN $7::bytea IS NULL THEN credential_nonce ELSE $7 END,credential_ciphertext=CASE WHEN $8::bytea IS NULL THEN credential_ciphertext ELSE $8 END,credential_hint=CASE WHEN $9='' THEN credential_hint ELSE $9 END,updated_by=$10,updated_at=now() WHERE id=$1`, id, name, protocol, endpoint, runtimeProviderForProtocol(protocol), enabled, nonce, ciphertext, hint, actorID)
	if err != nil {
		return ProviderConfig{}, err
	}
	return s.providerConfig(ctx, id)
}

func (s *Service) ArchiveProviderConfig(ctx context.Context, actorID uuid.UUID, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `UPDATE provider_configs SET archived_at=now(),admin_enabled=false,updated_by=$2,updated_at=now() WHERE id=$1 AND archived_at IS NULL`, id, actorID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_config_models SET archived_at=now(),admin_enabled=false,updated_by=$2,updated_at=now() WHERE provider_id=$1 AND archived_at IS NULL`, id, actorID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_profiles SET admin_enabled=false WHERE id IN (SELECT id::text FROM provider_config_models WHERE provider_id=$1)`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) CreateProviderModel(ctx context.Context, actorID uuid.UUID, providerID uuid.UUID, input ProviderModelCreate, _ string) (ProviderModel, error) {
	if err := validateProviderModelInput(input.Mode, input.ModelName, input.DisplayName, input.Description, input.EstimatedCostCents); err != nil {
		return ProviderModel{}, err
	}
	input.Capabilities = normalizeProviderModelCapabilities(input.Mode, input.Capabilities)
	if err := validateProviderModelCapabilities(input.Mode, input.Capabilities); err != nil {
		return ProviderModel{}, err
	}
	if input.PointPricing.MinimumPoints == 0 {
		input.PointPricing = defaultModelPointPricing(input.Mode, input.EstimatedCostCents)
	}
	if err := billing.ValidateModelPointPricing(input.Mode, input.PointPricing); err != nil {
		return ProviderModel{}, ErrInvalid
	}
	var runtimeProvider string
	if err := s.pool.QueryRow(ctx, `SELECT runtime_provider FROM provider_configs WHERE id=$1 AND archived_at IS NULL`, providerID).Scan(&runtimeProvider); errors.Is(err, pgx.ErrNoRows) {
		return ProviderModel{}, ErrNotFound
	} else if err != nil {
		return ProviderModel{}, err
	}
	modelID := uuid.New()
	modelName, displayName, description := strings.TrimSpace(input.ModelName), strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.Description)
	capabilitiesJSON, err := json.Marshal(input.Capabilities)
	if err != nil {
		return ProviderModel{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProviderModel{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `INSERT INTO provider_config_models(id,provider_id,mode,model_name,display_name,description,estimated_cost_cents,capabilities,admin_enabled,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, modelID, providerID, input.Mode, modelName, displayName, description, input.EstimatedCostCents, capabilitiesJSON, input.AdminEnabled, actorID); err != nil {
		return ProviderModel{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO provider_profiles(id,mode,provider,model_name,display_name,description,estimated_cost_cents,capabilities,local_test,admin_enabled,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,false,$9,$10) ON CONFLICT (id) DO UPDATE SET mode=EXCLUDED.mode,provider=EXCLUDED.provider,model_name=EXCLUDED.model_name,display_name=EXCLUDED.display_name,description=EXCLUDED.description,estimated_cost_cents=EXCLUDED.estimated_cost_cents,capabilities=EXCLUDED.capabilities,admin_enabled=EXCLUDED.admin_enabled,updated_by=EXCLUDED.updated_by,updated_at=now()`, modelID.String(), input.Mode, runtimeProvider, modelName, displayName, description, input.EstimatedCostCents, capabilitiesJSON, input.AdminEnabled, actorID); err != nil {
		return ProviderModel{}, err
	}
	if _, err = billing.UpsertModelPointPricing(ctx, tx, actorID, modelID, input.Mode, input.PointPricing); err != nil {
		return ProviderModel{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ProviderModel{}, err
	}
	return s.providerModel(ctx, modelID)
}

func (s *Service) UpdateProviderModel(ctx context.Context, actorID uuid.UUID, modelID uuid.UUID, input ProviderModelUpdate, _ string) (ProviderModel, error) {
	var mode, modelName, displayName, description string
	var estimatedCost int
	var enabled bool
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT mode,model_name,display_name,description,estimated_cost_cents,capabilities,admin_enabled FROM provider_config_models WHERE id=$1 AND archived_at IS NULL`, modelID).Scan(&mode, &modelName, &displayName, &description, &estimatedCost, &raw, &enabled); errors.Is(err, pgx.ErrNoRows) {
		return ProviderModel{}, ErrNotFound
	} else if err != nil {
		return ProviderModel{}, err
	}
	capabilities := creation.ParseModelCapabilities(mode, raw)
	pointPricing, pricingErr := billing.LoadModelPointPricing(ctx, s.pool, &modelID, "", mode)
	if pricingErr != nil && !errors.Is(pricingErr, billing.ErrPricingNotConfigured) {
		return ProviderModel{}, pricingErr
	}
	previousMode := mode
	if input.Mode != nil {
		mode = strings.TrimSpace(*input.Mode)
	}
	if input.ModelName != nil {
		modelName = strings.TrimSpace(*input.ModelName)
	}
	if input.DisplayName != nil {
		displayName = strings.TrimSpace(*input.DisplayName)
	}
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	if input.EstimatedCostCents != nil {
		estimatedCost = *input.EstimatedCostCents
	}
	if input.Capabilities != nil {
		capabilities = normalizeProviderModelCapabilities(mode, *input.Capabilities)
	}
	if input.AdminEnabled != nil {
		enabled = *input.AdminEnabled
	}
	if input.PointPricing != nil {
		pointPricing = *input.PointPricing
	} else if mode != previousMode || errors.Is(pricingErr, billing.ErrPricingNotConfigured) {
		pointPricing = defaultModelPointPricing(mode, estimatedCost)
	}
	if validateProviderModelInput(mode, modelName, displayName, description, estimatedCost) != nil {
		return ProviderModel{}, ErrInvalid
	}
	if err := validateProviderModelCapabilities(mode, capabilities); err != nil {
		return ProviderModel{}, err
	}
	if err := billing.ValidateModelPointPricing(mode, pointPricing); err != nil {
		return ProviderModel{}, ErrInvalid
	}
	capabilitiesJSON, err := json.Marshal(capabilities)
	if err != nil {
		return ProviderModel{}, ErrInvalid
	}
	var provider string
	if err := s.pool.QueryRow(ctx, `SELECT runtime_provider FROM provider_configs c JOIN provider_config_models m ON m.provider_id=c.id WHERE m.id=$1 AND c.archived_at IS NULL`, modelID).Scan(&provider); errors.Is(err, pgx.ErrNoRows) {
		return ProviderModel{}, ErrNotFound
	} else if err != nil {
		return ProviderModel{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProviderModel{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `UPDATE provider_config_models SET mode=$2,model_name=$3,display_name=$4,description=$5,estimated_cost_cents=$6,capabilities=$7,admin_enabled=$8,updated_by=$9,updated_at=now() WHERE id=$1 AND archived_at IS NULL`, modelID, mode, modelName, displayName, description, estimatedCost, capabilitiesJSON, enabled, actorID); err != nil {
		return ProviderModel{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_profiles SET mode=$2,provider=$3,model_name=$4,display_name=$5,description=$6,estimated_cost_cents=$7,capabilities=$8,admin_enabled=$9,updated_by=$10,updated_at=now() WHERE id=$1`, modelID.String(), mode, provider, modelName, displayName, description, estimatedCost, capabilitiesJSON, enabled, actorID); err != nil {
		return ProviderModel{}, err
	}
	if _, err = billing.UpsertModelPointPricing(ctx, tx, actorID, modelID, mode, pointPricing); err != nil {
		return ProviderModel{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ProviderModel{}, err
	}
	return s.providerModel(ctx, modelID)
}

func (s *Service) ArchiveProviderModel(ctx context.Context, actorID uuid.UUID, modelID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `UPDATE provider_config_models SET archived_at=now(),admin_enabled=false,updated_by=$2,updated_at=now() WHERE id=$1 AND archived_at IS NULL`, modelID, actorID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_profiles SET admin_enabled=false WHERE id=$1`, modelID.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) providerConfig(ctx context.Context, id uuid.UUID) (ProviderConfig, error) {
	var item ProviderConfig
	if err := s.pool.QueryRow(ctx, `SELECT id,name,protocol,endpoint,runtime_provider,credential_ciphertext IS NOT NULL,COALESCE(credential_hint,''),admin_enabled,created_at,updated_at FROM provider_configs WHERE id=$1 AND archived_at IS NULL`, id).Scan(&item.ID, &item.Name, &item.Protocol, &item.Endpoint, &item.RuntimeProvider, &item.CredentialConfigured, &item.CredentialHint, &item.AdminEnabled, &item.CreatedAt, &item.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return ProviderConfig{}, ErrNotFound
	} else if err != nil {
		return ProviderConfig{}, err
	}
	var err error
	item.Models, err = s.listProviderModels(ctx, id)
	return item, err
}

func (s *Service) providerModel(ctx context.Context, id uuid.UUID) (ProviderModel, error) {
	var item ProviderModel
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id,provider_id,mode,model_name,display_name,description,estimated_cost_cents,capabilities,admin_enabled,archived_at IS NOT NULL,created_at,updated_at FROM provider_config_models WHERE id=$1 AND archived_at IS NULL`, id).Scan(&item.ID, &item.ProviderID, &item.Mode, &item.ModelName, &item.DisplayName, &item.Description, &item.EstimatedCostCents, &raw, &item.AdminEnabled, &item.Archived, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderModel{}, ErrNotFound
	}
	item.Capabilities = creation.ParseModelCapabilities(item.Mode, raw)
	if err == nil {
		item.PointPricing, err = billing.LoadModelPointPricing(ctx, s.pool, &item.ID, "", item.Mode)
	}
	return item, err
}

func encryptProviderSecret(key []byte, secret string) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, []byte(secret), nil), nil
}

func decryptProviderSecret(key, nonce, ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return "", ErrProviderSecret
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(plain)), nil
}

func secretHint(secret string) string {
	if len(secret) <= 6 {
		return "••••••"
	}
	return "••••" + secret[len(secret)-6:]
}
func parsedHost(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
