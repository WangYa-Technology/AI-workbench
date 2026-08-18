package providers

import (
	"time"

	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/reconciliation"
)

func NewCatalog(cfg config.Config) *creation.RuntimeCatalog {
	runtimes := make([]creation.ProviderRuntime, 0, 4)
	if cfg.LocalProviderEnabled {
		runtimes = append(runtimes, creation.NewLocalRuntime(cfg.LocalProviderSource))
	}
	if cfg.OpenAIEnabled {
		runtimes = append(runtimes, creation.NewOpenAIRuntime(creation.OpenAIRuntimeConfig{
			APIKey: cfg.OpenAIAPIKey, BaseURL: cfg.OpenAIBaseURL,
			ChatModel: cfg.OpenAIChatModel, ImageModel: cfg.OpenAIImageModel,
			ChatMaxOutputTokens: cfg.OpenAIChatMaxOutputTokens,
			ImageSize:           cfg.OpenAIImageSize, ImageQuality: cfg.OpenAIImageQuality,
			Organization: cfg.OpenAIOrganization, Project: cfg.OpenAIProject,
		}))
	}
	if cfg.VideoEnabled && cfg.VideoPaidCallsApproved {
		runtimes = append(runtimes, creation.NewVideoRuntime(creation.VideoRuntimeConfig{
			APIKey: cfg.VideoAPIKey, BaseURL: cfg.VideoBaseURL, Model: cfg.VideoModel,
			PollInterval: time.Duration(cfg.VideoPollIntervalSeconds) * time.Second,
			Timeout:      time.Duration(cfg.VideoTimeoutSeconds) * time.Second,
		}))
	}
	if cfg.MusicEnabled && cfg.MusicPaidCallsApproved {
		runtimes = append(runtimes, creation.NewMusicRuntime(creation.MusicRuntimeConfig{
			APIKey: cfg.MusicAPIKey, BaseURL: cfg.MusicBaseURL, Model: cfg.MusicModel,
		}))
	}
	return creation.NewRuntimeCatalog(runtimes...)
}

// NewOpenAICostsRuntime returns a separately gated aggregate cost reader. It
// is intentionally not part of the generation runtime catalog.
func NewOpenAICostsRuntime(cfg config.Config) (*reconciliation.OpenAICostsRuntime, error) {
	if !cfg.OpenAIReconciliationEnabled {
		return nil, nil
	}
	return reconciliation.NewOpenAICostsRuntime(reconciliation.OpenAICostsConfig{
		AdminAPIKey: cfg.OpenAIAdminAPIKey,
		BaseURL:     cfg.OpenAIBaseURL,
		ProjectID:   cfg.OpenAIProject,
	})
}
