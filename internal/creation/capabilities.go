package creation

import (
	"context"
	"fmt"
)

// CreationCapability is a public, non-secret projection of the active route.
// It intentionally contains no credentials, costs, upstream URLs, or admin
// state; the UI uses it only to avoid presenting unsupported controls.
type CreationCapability struct {
	Mode           string   `json:"mode"`
	Available      bool     `json:"available"`
	Provider       string   `json:"provider,omitempty"`
	ModelName      string   `json:"modelName,omitempty"`
	AspectRatios   []string `json:"aspectRatios"`
	Qualities      []string `json:"qualities"`
	Durations      []int    `json:"durationSeconds"`
	OutputFormats  []string `json:"outputFormats"`
	ResultFormats  []string `json:"resultFormats"`
	ReferenceKinds []string `json:"referenceKinds"`
	SupportsMask   bool     `json:"supportsMask"`
}

type CreationCapabilities struct {
	Items []CreationCapability `json:"items"`
}

var creationModes = []string{"chat", "image", "video", "music"}

// Capabilities returns the current immutable route projection. A missing,
// disabled, or unavailable route remains visible as unavailable so the mode
// rail stays stable while the submit action remains server-authorized.
func (s *Service) Capabilities(ctx context.Context) (CreationCapabilities, error) {
	items := make(map[string]CreationCapability, len(creationModes))
	for _, mode := range creationModes {
		items[mode] = defaultCreationCapability(mode)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.mode,p.provider,p.model_name,p.admin_enabled
		FROM model_route_state s
		JOIN model_route_revisions r ON r.id=s.active_revision_id
		JOIN provider_profiles p ON p.id=r.provider_profile_id
		WHERE s.mode=ANY($1)`, creationModes)
	if err != nil {
		return CreationCapabilities{}, fmt.Errorf("load creation capabilities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mode, provider, model string
		var enabled bool
		if err := rows.Scan(&mode, &provider, &model, &enabled); err != nil {
			return CreationCapabilities{}, fmt.Errorf("scan creation capability: %w", err)
		}
		capability := defaultCreationCapability(mode)
		capability.Provider = provider
		capability.ModelName = model
		capability.Available = enabled && s.runtimes.Available(provider, mode, model)
		capability = adjustCreationCapability(capability)
		items[mode] = capability
	}
	if err := rows.Err(); err != nil {
		return CreationCapabilities{}, fmt.Errorf("iterate creation capabilities: %w", err)
	}
	result := CreationCapabilities{Items: make([]CreationCapability, 0, len(creationModes))}
	for _, mode := range creationModes {
		result.Items = append(result.Items, items[mode])
	}
	return result, nil
}

func defaultCreationCapability(mode string) CreationCapability {
	capability := CreationCapability{
		Mode: mode, Available: false,
		AspectRatios: []string{}, Qualities: []string{"auto", "standard", "high"},
		Durations: []int{}, OutputFormats: []string{"txt"}, ResultFormats: []string{"txt"}, ReferenceKinds: []string{},
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
		capability.Durations = []int{5, 10, 30}
		capability.OutputFormats = []string{"mp4"}
		capability.ResultFormats = []string{"mp4"}
		capability.ReferenceKinds = []string{"image"}
	case "music":
		capability.Durations = []int{5, 10, 30, 60}
		capability.OutputFormats = []string{"wav"}
		capability.ResultFormats = []string{"wav"}
		capability.Qualities = []string{"auto", "standard", "high"}
		capability.ReferenceKinds = []string{"audio"}
	case "chat":
		capability.Qualities = []string{}
		capability.OutputFormats = []string{"txt"}
		capability.ResultFormats = []string{"txt"}
		capability.ReferenceKinds = []string{"document"}
	}
	return capability
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
