package providers_test

import (
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/providers"
)

func TestCatalogRegistersOnlyExplicitlyEnabledRuntimes(t *testing.T) {
	disabled := providers.NewCatalog(config.Config{
		LocalProviderSource: "fixture.jpg",
		OpenAIBaseURL:       "https://api.openai.com/v1",
		OpenAIChatModel:     "gpt-5.6-terra",
		OpenAIImageModel:    "gpt-image-2",
	})
	if disabled.Available("local_test", "chat", "hcai-local-chat-v1") || disabled.Available("openai", "chat", "gpt-5.6-terra") {
		t.Fatal("disabled Provider runtime was registered")
	}

	enabled := providers.NewCatalog(config.Config{
		LocalProviderEnabled:      true,
		LocalProviderSource:       "fixture.jpg",
		OpenAIEnabled:             true,
		OpenAIPaidCallsApproved:   true,
		OpenAIAPIKey:              "test-secret-never-sent",
		OpenAIBaseURL:             "https://api.openai.com/v1",
		OpenAIChatModel:           "gpt-5.6-terra",
		OpenAIImageModel:          "gpt-image-2",
		OpenAIChatMaxOutputTokens: 2048,
		OpenAIImageSize:           "1024x1024",
		OpenAIImageQuality:        "medium",
		VideoEnabled:              true,
		VideoPaidCallsApproved:    true,
		VideoAPIKey:               "video-secret-never-sent",
		VideoBaseURL:              "https://ark.ap-southeast.bytepluses.com/api/v3",
		VideoModel:                "dreamina-seedance-2-0-fast-260128",
		VideoPollIntervalSeconds:  10,
		VideoTimeoutSeconds:       300,
		MusicEnabled:              true,
		MusicPaidCallsApproved:    true,
		MusicAPIKey:               "music-secret-never-sent",
		MusicBaseURL:              "https://api.minimaxi.com/v1",
		MusicModel:                "music-3.0",
	})
	for _, capability := range []struct{ provider, mode, model string }{
		{"local_test", "chat", "hcai-local-chat-v1"},
		{"local_test", "image", "hcai-local-image-v1"},
		{"openai", "chat", "gpt-5.6-terra"},
		{"openai", "image", "gpt-image-2"},
		{"byteplus_video", "video", "dreamina-seedance-2-0-fast-260128"},
		{"minimax_music", "music", "music-3.0"},
	} {
		if !enabled.Available(capability.provider, capability.mode, capability.model) {
			t.Fatalf("expected runtime capability to be registered: %#v", capability)
		}
	}
	if enabled.Available("openai", "video", "gpt-image-2") || enabled.Available("openai", "chat", "gpt-5.6") {
		t.Fatal("catalog accepted an unconfigured OpenAI capability")
	}
}

func TestCostsRuntimeUsesSeparateApprovalBoundary(t *testing.T) {
	disabled := config.Config{OpenAIBaseURL: "https://api.openai.com/v1", OpenAIProject: "proj_test"}
	if runtime, err := providers.NewOpenAICostsRuntime(disabled); err != nil || runtime != nil {
		t.Fatalf("disabled reconciliation runtime was registered: runtime=%v err=%v", runtime, err)
	}
	enabled := disabled
	enabled.OpenAIReconciliationEnabled = true
	enabled.OpenAIAdminAPIKey = "admin-test-never-sent"
	runtime, err := providers.NewOpenAICostsRuntime(enabled)
	if err != nil || runtime == nil {
		t.Fatalf("approved reconciliation runtime was not constructed: runtime=%v err=%v", runtime, err)
	}
}
