package creation_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/creation"
)

func TestOpenAIStagingAcceptanceReturnsMinimizedTwoCallEvidence(t *testing.T) {
	runtime := &acceptanceRuntime{}
	result, err := creation.RunOpenAIStagingAcceptance(context.Background(), creation.NewRuntimeCatalog(runtime), "chat-staging", "image-staging", 1024, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "passed" || result.Provider != "openai" || result.PaidCallCount != 2 || runtime.calls != 2 || len(result.Checks) != 2 {
		t.Fatalf("unexpected staging acceptance result: result=%+v calls=%d", result, runtime.calls)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{"staging acceptance response body", "minimal image bytes", "Do not include personal data", "plain white background"} {
		if strings.Contains(string(encoded), prohibited) {
			t.Fatalf("acceptance evidence exposed output or prompt content: %s", encoded)
		}
	}
}

func TestOpenAIStagingAcceptanceStopsAfterSafeProviderFailure(t *testing.T) {
	runtime := &acceptanceRuntime{failureMode: "chat"}
	result, err := creation.RunOpenAIStagingAcceptance(context.Background(), creation.NewRuntimeCatalog(runtime), "chat-staging", "image-staging", 1024, 1024)
	if err == nil || !strings.Contains(err.Error(), "provider_rate_limited") || result.PaidCallCount != 1 || runtime.calls != 1 {
		t.Fatalf("provider failure boundary mismatch: result=%+v calls=%d err=%v", result, runtime.calls, err)
	}
}

func TestOpenAIStagingAcceptanceRequiresUsageAndExactImageSize(t *testing.T) {
	missingUsage := &acceptanceRuntime{omitUsageMode: "chat"}
	if result, err := creation.RunOpenAIStagingAcceptance(context.Background(), creation.NewRuntimeCatalog(missingUsage), "chat-staging", "image-staging", 1024, 1024); err == nil || result.PaidCallCount != 1 {
		t.Fatalf("missing usage was accepted: result=%+v err=%v", result, err)
	}
	wrongSize := &acceptanceRuntime{imageWidth: 1536}
	if result, err := creation.RunOpenAIStagingAcceptance(context.Background(), creation.NewRuntimeCatalog(wrongSize), "chat-staging", "image-staging", 1024, 1024); err == nil || result.PaidCallCount != 2 {
		t.Fatalf("wrong image dimensions were accepted: result=%+v err=%v", result, err)
	}
}

func TestVideoAndMusicStagingAcceptanceAreSingleCallAndTyped(t *testing.T) {
	videoRuntime := &mediaAcceptanceRuntime{provider: "byteplus_video"}
	musicRuntime := &mediaAcceptanceRuntime{provider: "minimax_music"}
	catalog := creation.NewRuntimeCatalog(videoRuntime, musicRuntime)
	video, err := creation.RunVideoStagingAcceptance(context.Background(), catalog, "video-staging")
	if err != nil || video.Status != "passed" || video.PaidCallCount != 1 || len(video.Checks) != 1 || videoRuntime.calls != 1 {
		t.Fatalf("unexpected video acceptance result: result=%+v calls=%d err=%v", video, videoRuntime.calls, err)
	}
	music, err := creation.RunMusicStagingAcceptance(context.Background(), catalog, "music-staging")
	if err != nil || music.Status != "passed" || music.PaidCallCount != 1 || len(music.Checks) != 1 || musicRuntime.calls != 1 {
		t.Fatalf("unexpected music acceptance result: result=%+v calls=%d err=%v", music, musicRuntime.calls, err)
	}
	if video.Checks[0].MIMEType != "video/mp4" || video.Checks[0].Width == nil || video.Checks[0].Height == nil || music.Checks[0].MIMEType != "audio/mpeg" {
		t.Fatalf("typed media evidence missing: video=%+v music=%+v", video, music)
	}
	encoded, _ := json.Marshal(struct {
		Video creation.MediaProviderAcceptanceResult `json:"video"`
		Music creation.MediaProviderAcceptanceResult `json:"music"`
	}{video, music})
	for _, prohibited := range []string{"abstract product reveal", "cinematic finish", "https://", "request"} {
		if strings.Contains(string(encoded), prohibited) {
			t.Fatalf("media acceptance evidence exposed prohibited data: %s", encoded)
		}
	}
}

func TestMediaStagingAcceptanceStopsAfterProviderFailure(t *testing.T) {
	runtime := &mediaAcceptanceRuntime{provider: "byteplus_video", failureMode: "video"}
	result, err := creation.RunVideoStagingAcceptance(context.Background(), creation.NewRuntimeCatalog(runtime), "video-staging")
	if err == nil || !strings.Contains(err.Error(), "provider_rate_limited") || result.PaidCallCount != 1 || runtime.calls != 1 {
		t.Fatalf("video failure was not bounded to one call: result=%+v calls=%d err=%v", result, runtime.calls, err)
	}
}

type acceptanceRuntime struct {
	calls         int
	failureMode   string
	omitUsageMode string
	imageWidth    int
}

type mediaAcceptanceRuntime struct {
	provider    string
	calls       int
	failureMode string
}

func (r *mediaAcceptanceRuntime) Provider() string { return r.provider }

func (r *mediaAcceptanceRuntime) Supports(mode, model string) bool {
	return (r.provider == "byteplus_video" && mode == "video" && model == "video-staging") || (r.provider == "minimax_music" && mode == "music" && model == "music-staging")
}

func (r *mediaAcceptanceRuntime) Generate(_ context.Context, request creation.ProviderRequest) (creation.ProviderOutput, error) {
	r.calls++
	if request.Mode == r.failureMode {
		return creation.ProviderOutput{}, creation.NewProviderFailure("provider_rate_limited", 0)
	}
	if request.Mode == "video" {
		width, height := 1280, 720
		return creation.ProviderOutput{Kind: "video", MIMEType: "video/mp4", Extension: ".mp4", Width: &width, Height: &height, Content: []byte("mp4")}, nil
	}
	return creation.ProviderOutput{Kind: "audio", MIMEType: "audio/mpeg", Extension: ".mp3", Content: []byte("ID3")}, nil
}

func (r *acceptanceRuntime) Provider() string { return "openai" }

func (r *acceptanceRuntime) Supports(mode, model string) bool {
	return (mode == "chat" && model == "chat-staging") || (mode == "image" && model == "image-staging")
}

func (r *acceptanceRuntime) Generate(_ context.Context, request creation.ProviderRequest) (creation.ProviderOutput, error) {
	r.calls++
	if request.Mode == r.failureMode {
		return creation.ProviderOutput{}, creation.NewProviderFailure("provider_rate_limited", 0)
	}
	usage := &creation.ProviderUsage{InputTokens: 7, OutputTokens: 5, TotalTokens: 12}
	if request.Mode == r.omitUsageMode {
		usage = nil
	}
	if request.Mode == "chat" {
		text := "staging acceptance response body"
		return creation.ProviderOutput{Kind: "document", MIMEType: "text/plain; charset=utf-8", Extension: ".txt", Text: &text, Content: []byte(text), Usage: usage}, nil
	}
	width := r.imageWidth
	if width == 0 {
		width = 1024
	}
	height := 1024
	return creation.ProviderOutput{Kind: "image", MIMEType: "image/png", Extension: ".png", Width: &width, Height: &height, Content: []byte("minimal image bytes"), Usage: usage}, nil
}

var _ creation.ProviderRuntime = (*acceptanceRuntime)(nil)
