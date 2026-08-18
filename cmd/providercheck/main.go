package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/providers"
)

const (
	providerAcceptanceConfirmation = "I_APPROVE_OPENAI_STAGING_CALLS"
	providerAcceptanceMaxCalls     = "2"
)

func main() {
	if os.Getenv("PROVIDER_ACCEPTANCE_CONFIRM") != providerAcceptanceConfirmation || os.Getenv("OPENAI_ACCEPTANCE_MAX_CALLS") != providerAcceptanceMaxCalls {
		fmt.Fprintln(os.Stderr, "OpenAI staging acceptance disabled: explicit confirmation and OPENAI_ACCEPTANCE_MAX_CALLS=2 are required")
		os.Exit(1)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "OpenAI staging acceptance configuration invalid:", err)
		os.Exit(1)
	}
	if err := validateOpenAIAcceptanceConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	width, height, _ := fixedImageSize(cfg.OpenAIImageSize)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := creation.RunOpenAIStagingAcceptance(ctx, providers.NewCatalog(cfg), cfg.OpenAIChatModel, cfg.OpenAIImageModel, width, height)
	if err != nil {
		fmt.Fprintln(os.Stderr, "OpenAI staging acceptance failed:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "encode OpenAI staging acceptance summary:", err)
		os.Exit(1)
	}
}

func validateOpenAIAcceptanceConfig(cfg config.Config) error {
	if cfg.Environment != "staging" {
		return fmt.Errorf("OpenAI staging acceptance requires APP_ENV=staging")
	}
	if cfg.OpenAIBaseURL != "https://api.openai.com/v1" || !cfg.OpenAIEnabled || !cfg.OpenAIPaidCallsApproved || cfg.LocalProviderEnabled {
		return fmt.Errorf("OpenAI staging acceptance requires the enabled official runtime, paid-call approval, and LOCAL_PROVIDER_ENABLED=false")
	}
	if cfg.OpenAIAPIKey == "" || cfg.OpenAIChatModel == "" || cfg.OpenAIImageModel == "" {
		return fmt.Errorf("OpenAI staging acceptance requires credentials and registered Chat/Image models")
	}
	if _, _, ok := fixedImageSize(cfg.OpenAIImageSize); !ok {
		return fmt.Errorf("OpenAI staging acceptance requires a fixed OPENAI_IMAGE_SIZE")
	}
	return nil
}

func fixedImageSize(value string) (int, int, bool) {
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, widthErr := strconv.Atoi(parts[0])
	height, heightErr := strconv.Atoi(parts[1])
	return width, height, widthErr == nil && heightErr == nil && width > 0 && height > 0
}
