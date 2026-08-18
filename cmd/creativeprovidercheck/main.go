package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/providers"
)

const (
	creativeAcceptanceConfirmation = "I_APPROVE_CREATIVE_STAGING_CALLS"
	videoAcceptanceMaxCalls        = 1
	musicAcceptanceMaxCalls        = 1
)

type acceptanceSummary struct {
	Video creation.MediaProviderAcceptanceResult `json:"video"`
	Music creation.MediaProviderAcceptanceResult `json:"music"`
}

func main() {
	if os.Getenv("CREATIVE_PROVIDER_ACCEPTANCE_CONFIRM") != creativeAcceptanceConfirmation ||
		!exactCallCeiling("VIDEO_ACCEPTANCE_MAX_CALLS", videoAcceptanceMaxCalls) ||
		!exactCallCeiling("MUSIC_ACCEPTANCE_MAX_CALLS", musicAcceptanceMaxCalls) {
		fail("creative staging acceptance disabled: exact confirmation and one-call ceilings are required")
	}
	cfg, err := config.Load()
	if err != nil {
		fail("creative staging acceptance configuration invalid: " + err.Error())
	}
	if err := validateCreativeAcceptanceConfig(cfg); err != nil {
		fail(err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	catalog := providers.NewCatalog(cfg)
	video, err := creation.RunVideoStagingAcceptance(ctx, catalog, cfg.VideoModel)
	if err != nil {
		fail("Video staging acceptance failed: " + err.Error())
	}
	music, err := creation.RunMusicStagingAcceptance(ctx, catalog, cfg.MusicModel)
	if err != nil {
		fail("Music staging acceptance failed: " + err.Error())
	}
	if err := json.NewEncoder(os.Stdout).Encode(acceptanceSummary{Video: video, Music: music}); err != nil {
		fail("encode creative staging acceptance summary: " + err.Error())
	}
}

func exactCallCeiling(name string, expected int) bool {
	value, err := strconv.Atoi(os.Getenv(name))
	return err == nil && value == expected
}

func validateCreativeAcceptanceConfig(cfg config.Config) error {
	if cfg.Environment != "staging" {
		return fmt.Errorf("creative staging acceptance requires APP_ENV=staging")
	}
	if cfg.LocalProviderEnabled {
		return fmt.Errorf("creative staging acceptance requires LOCAL_PROVIDER_ENABLED=false")
	}
	if !cfg.VideoEnabled || !cfg.VideoPaidCallsApproved || cfg.VideoAPIKey == "" || cfg.VideoModel == "" || cfg.VideoBaseURL != "https://ark.ap-southeast.bytepluses.com/api/v3" {
		return fmt.Errorf("creative staging acceptance requires the approved official Video runtime")
	}
	if !cfg.MusicEnabled || !cfg.MusicPaidCallsApproved || cfg.MusicAPIKey == "" || cfg.MusicModel == "" || cfg.MusicBaseURL != "https://api.minimaxi.com/v1" {
		return fmt.Errorf("creative staging acceptance requires the approved official Music runtime")
	}
	return nil
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
