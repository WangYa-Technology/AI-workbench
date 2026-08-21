package creation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
)

// ModelCapabilities is the executable contract for a configured model. It is
// provider-agnostic so the same projection can drive admin, public controls,
// and request validation.
type ModelCapabilities struct {
	AspectRatios    []string `json:"aspectRatios"`
	Qualities       []string `json:"qualities"`
	DurationSeconds []int    `json:"durationSeconds"`
	OutputFormats   []string `json:"outputFormats"`
	ResultFormats   []string `json:"resultFormats"`
	ReferenceKinds  []string `json:"referenceKinds"`
	SupportsMask    bool     `json:"supportsMask"`
}

// DefaultModelCapabilities provides a safe baseline for old rows and models
// whose upstream metadata does not describe capabilities.
func DefaultModelCapabilities(mode string) ModelCapabilities {
	capability := ModelCapabilities{
		AspectRatios: []string{}, Qualities: []string{"auto", "standard", "high"},
		DurationSeconds: []int{}, OutputFormats: []string{"txt"}, ResultFormats: []string{"txt"},
		ReferenceKinds: []string{}, SupportsMask: false,
	}
	switch mode {
	case "image":
		capability.AspectRatios = []string{"auto", "1:1", "4:5", "16:9"}
		capability.OutputFormats = []string{"jpeg", "png"}
		capability.ResultFormats = []string{"jpeg", "png"}
		capability.ReferenceKinds = []string{"image"}
		capability.SupportsMask = true
	case "video":
		capability.AspectRatios = []string{"auto", "1:1", "4:5", "16:9"}
		capability.DurationSeconds = []int{5, 10, 30}
		capability.OutputFormats = []string{"mp4"}
		capability.ResultFormats = []string{"mp4"}
		capability.ReferenceKinds = []string{"image"}
	case "music":
		capability.DurationSeconds = []int{5, 10, 30, 60}
		capability.OutputFormats = []string{"wav"}
		capability.ResultFormats = []string{"wav"}
		capability.ReferenceKinds = []string{"audio"}
	case "chat":
		capability.Qualities = []string{}
		capability.ReferenceKinds = []string{"document"}
	}
	return capability
}

// defaultCreationCapability keeps the legacy test and route projection helper
// available while model-level capabilities become the canonical source.
func defaultCreationCapability(mode string) CreationCapability {
	defaults := DefaultModelCapabilities(mode)
	return CreationCapability{Mode: mode, Available: false, AspectRatios: defaults.AspectRatios, Qualities: defaults.Qualities, Durations: defaults.DurationSeconds, OutputFormats: defaults.OutputFormats, ResultFormats: defaults.ResultFormats, ReferenceKinds: defaults.ReferenceKinds, SupportsMask: defaults.SupportsMask, Models: []CreationModel{}}
}

// ParseModelCapabilities accepts partial JSON and fills missing fields from
// the mode baseline. An invalid payload fails closed to the baseline.
func ParseModelCapabilities(mode string, raw []byte) ModelCapabilities {
	defaults := DefaultModelCapabilities(mode)
	if len(raw) == 0 || string(raw) == "{}" {
		return defaults
	}
	var value ModelCapabilities
	if err := json.Unmarshal(raw, &value); err != nil {
		return defaults
	}
	if value.AspectRatios == nil {
		value.AspectRatios = defaults.AspectRatios
	}
	if value.Qualities == nil {
		value.Qualities = defaults.Qualities
	}
	if value.DurationSeconds == nil {
		value.DurationSeconds = defaults.DurationSeconds
	}
	if value.OutputFormats == nil {
		value.OutputFormats = defaults.OutputFormats
	}
	if value.ResultFormats == nil {
		value.ResultFormats = defaults.ResultFormats
	}
	if value.ReferenceKinds == nil {
		value.ReferenceKinds = defaults.ReferenceKinds
	}
	return value
}

// CreationModel is a public, non-secret model directory entry.
type CreationModel struct {
	ID           string                    `json:"id"`
	ProviderID   string                    `json:"providerId"`
	ProviderName string                    `json:"providerName"`
	Provider     string                    `json:"provider"`
	ModelName    string                    `json:"modelName"`
	DisplayName  string                    `json:"displayName"`
	Description  string                    `json:"description"`
	Available    bool                      `json:"available"`
	Capabilities ModelCapabilities         `json:"capabilities"`
	PointPricing billing.ModelPointPricing `json:"pointPricing"`
}

// CreationCapability preserves mode-level fields for compatibility. Models
// is now the source of truth for the model selector.
type CreationCapability struct {
	Mode           string          `json:"mode"`
	Available      bool            `json:"available"`
	Provider       string          `json:"provider,omitempty"`
	ModelName      string          `json:"modelName,omitempty"`
	AspectRatios   []string        `json:"aspectRatios"`
	Qualities      []string        `json:"qualities"`
	Durations      []int           `json:"durationSeconds"`
	OutputFormats  []string        `json:"outputFormats"`
	ResultFormats  []string        `json:"resultFormats"`
	ReferenceKinds []string        `json:"referenceKinds"`
	SupportsMask   bool            `json:"supportsMask"`
	Models         []CreationModel `json:"models"`
}

type CreationCapabilities struct {
	Items []CreationCapability `json:"items"`
}

var creationModes = []string{"chat", "image", "video", "music"}

func (s *Service) Capabilities(ctx context.Context) (CreationCapabilities, error) {
	items := make(map[string]CreationCapability, len(creationModes))
	for _, mode := range creationModes {
		items[mode] = defaultCreationCapability(mode)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT m.id,m.provider_id,m.mode,c.name,c.runtime_provider,m.model_name,m.display_name,m.description,m.capabilities
		FROM provider_config_models m
		JOIN provider_configs c ON c.id=m.provider_id
		WHERE m.archived_at IS NULL AND c.archived_at IS NULL AND m.admin_enabled=true AND c.admin_enabled=true
		ORDER BY m.mode,lower(c.name),lower(m.display_name),m.id`)
	if err != nil {
		return CreationCapabilities{}, fmt.Errorf("load configured model capabilities: %w", err)
	}
	for rows.Next() {
		var id, providerID uuid.UUID
		var mode, providerName, provider, modelName, displayName, description string
		var raw []byte
		if err := rows.Scan(&id, &providerID, &mode, &providerName, &provider, &modelName, &displayName, &description, &raw); err != nil {
			rows.Close()
			return CreationCapabilities{}, fmt.Errorf("scan configured model capabilities: %w", err)
		}
		modelCapabilities := ParseModelCapabilities(mode, raw)
		pointPricing, pricingErr := billing.LoadModelPointPricing(ctx, s.pool, &id, "", mode)
		if pricingErr != nil {
			return CreationCapabilities{}, fmt.Errorf("load configured model point pricing: %w", pricingErr)
		}
		model := CreationModel{ID: id.String(), ProviderID: providerID.String(), ProviderName: providerName, Provider: provider, ModelName: modelName, DisplayName: displayName, Description: description, Available: s.runtimes.Available(provider, mode, modelName), Capabilities: modelCapabilities, PointPricing: pointPricing}
		capability := items[mode]
		capability.Models = append(capability.Models, model)
		if model.Available || (!capability.Available && capability.ModelName == "") {
			capability.Available = model.Available
			capability.Provider = provider
			capability.ModelName = modelName
			capability.AspectRatios = modelCapabilities.AspectRatios
			capability.Qualities = modelCapabilities.Qualities
			capability.Durations = modelCapabilities.DurationSeconds
			capability.OutputFormats = modelCapabilities.OutputFormats
			capability.ResultFormats = modelCapabilities.ResultFormats
			capability.ReferenceKinds = modelCapabilities.ReferenceKinds
			capability.SupportsMask = modelCapabilities.SupportsMask
		}
		items[mode] = capability
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CreationCapabilities{}, fmt.Errorf("iterate configured model capabilities: %w", err)
	}
	rows.Close()

	// Keep provider_profiles routes visible while installations migrate their
	// models into provider_configs. This is a fallback, never the selector data.
	rows, err = s.pool.Query(ctx, `
		SELECT s.mode,p.provider,p.model_name,p.admin_enabled
		FROM model_route_state s
		JOIN model_route_revisions r ON r.id=s.active_revision_id
		JOIN provider_profiles p ON p.id=r.provider_profile_id
		WHERE s.mode=ANY($1)`, creationModes)
	if err != nil {
		return CreationCapabilities{}, fmt.Errorf("load legacy creation capabilities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mode, provider, model string
		var enabled bool
		if err := rows.Scan(&mode, &provider, &model, &enabled); err != nil {
			return CreationCapabilities{}, fmt.Errorf("scan legacy creation capability: %w", err)
		}
		capability := items[mode]
		if len(capability.Models) == 0 {
			capability.Provider = provider
			capability.ModelName = model
			capability.Available = enabled && s.runtimes.Available(provider, mode, model)
			capability = adjustCreationCapability(capability)
		}
		items[mode] = capability
	}
	if err := rows.Err(); err != nil {
		return CreationCapabilities{}, fmt.Errorf("iterate legacy creation capabilities: %w", err)
	}
	result := CreationCapabilities{Items: make([]CreationCapability, 0, len(creationModes))}
	for _, mode := range creationModes {
		result.Items = append(result.Items, items[mode])
	}
	return result, nil
}

func adjustCreationCapability(capability CreationCapability) CreationCapability {
	switch capability.Provider {
	case "byteplus_video":
		capability.Durations = []int{5, 10}
	case "openai":
		if capability.Mode == "image" {
			capability.ReferenceKinds = []string{}
			capability.SupportsMask = false
		}
	case "minimax_music":
		capability.ReferenceKinds = []string{}
		capability.ResultFormats = []string{"mp3"}
	}
	return capability
}
