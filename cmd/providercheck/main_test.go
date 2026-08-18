package main

import (
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

func TestFixedImageSizeAcceptsOnlyPositiveWidthAndHeight(t *testing.T) {
	valid := []string{"1024x1024", "1536x1024"}
	for _, value := range valid {
		if width, height, ok := fixedImageSize(value); !ok || width < 1 || height < 1 {
			t.Fatalf("rejected valid image size %q: %d %d %v", value, width, height, ok)
		}
	}
	for _, value := range []string{"auto", "1024", "0x1024", "1024x0", "1024X1024", "1024x1024x1"} {
		if _, _, ok := fixedImageSize(value); ok {
			t.Fatalf("accepted unsafe image size %q", value)
		}
	}
}

func TestOpenAIAcceptanceConfigFailsClosed(t *testing.T) {
	base := config.Config{
		Environment: "staging", LocalProviderEnabled: false,
		OpenAIEnabled: true, OpenAIPaidCallsApproved: true, OpenAIAPIKey: "fixture-only",
		OpenAIBaseURL: "https://api.openai.com/v1", OpenAIChatModel: "chat", OpenAIImageModel: "image", OpenAIImageSize: "1024x1024",
	}
	if err := validateOpenAIAcceptanceConfig(base); err != nil {
		t.Fatalf("accepted valid OpenAI staging gate was rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"wrong environment", func(cfg *config.Config) { cfg.Environment = "development" }, "APP_ENV=staging"},
		{"local provider", func(cfg *config.Config) { cfg.LocalProviderEnabled = true }, "LOCAL_PROVIDER_ENABLED=false"},
		{"unapproved calls", func(cfg *config.Config) { cfg.OpenAIPaidCallsApproved = false }, "enabled official runtime"},
		{"non-official endpoint", func(cfg *config.Config) { cfg.OpenAIBaseURL = "http://127.0.0.1:9000/v1" }, "enabled official runtime"},
		{"missing credential", func(cfg *config.Config) { cfg.OpenAIAPIKey = "" }, "credentials"},
		{"auto image size", func(cfg *config.Config) { cfg.OpenAIImageSize = "auto" }, "fixed OPENAI_IMAGE_SIZE"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := base
			testCase.mutate(&candidate)
			err := validateOpenAIAcceptanceConfig(candidate)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("gate did not fail closed: err=%v", err)
			}
		})
	}
}
