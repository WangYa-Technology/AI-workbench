package main

import (
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

func TestExactCallCeilingRequiresOneExplicitCall(t *testing.T) {
	for _, value := range []string{"", "0", "2", "one", " 1"} {
		t.Setenv("VIDEO_ACCEPTANCE_MAX_CALLS", value)
		if exactCallCeiling("VIDEO_ACCEPTANCE_MAX_CALLS", 1) {
			t.Fatalf("accepted unsafe Video call ceiling %q", value)
		}
	}
	t.Setenv("VIDEO_ACCEPTANCE_MAX_CALLS", "1")
	if !exactCallCeiling("VIDEO_ACCEPTANCE_MAX_CALLS", 1) {
		t.Fatal("rejected exact Video call ceiling")
	}
}

func TestCreativeAcceptanceConfigFailsClosed(t *testing.T) {
	base := config.Config{
		Environment: "staging", LocalProviderEnabled: false,
		VideoEnabled: true, VideoPaidCallsApproved: true, VideoAPIKey: "video", VideoModel: "video", VideoBaseURL: "https://ark.ap-southeast.bytepluses.com/api/v3",
		MusicEnabled: true, MusicPaidCallsApproved: true, MusicAPIKey: "music", MusicModel: "music", MusicBaseURL: "https://api.minimaxi.com/v1",
	}
	if err := validateCreativeAcceptanceConfig(base); err != nil {
		t.Fatalf("accepted valid staging gate was rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"wrong environment", func(cfg *config.Config) { cfg.Environment = "development" }, "APP_ENV=staging"},
		{"local provider", func(cfg *config.Config) { cfg.LocalProviderEnabled = true }, "LOCAL_PROVIDER_ENABLED=false"},
		{"video endpoint", func(cfg *config.Config) { cfg.VideoBaseURL = "http://127.0.0.1:9000" }, "official Video"},
		{"music approval", func(cfg *config.Config) { cfg.MusicPaidCallsApproved = false }, "official Music"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := base
			testCase.mutate(&candidate)
			err := validateCreativeAcceptanceConfig(candidate)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("gate did not fail closed: err=%v", err)
			}
		})
	}
}
