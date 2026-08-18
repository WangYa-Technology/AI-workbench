package creation

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type ProviderAcceptanceUsage struct {
	InputTokens       int `json:"inputTokens"`
	CachedInputTokens int `json:"cachedInputTokens"`
	OutputTokens      int `json:"outputTokens"`
	ReasoningTokens   int `json:"reasoningTokens"`
	TotalTokens       int `json:"totalTokens"`
}

type ProviderAcceptanceCheck struct {
	Mode        string                  `json:"mode"`
	Model       string                  `json:"model"`
	MIMEType    string                  `json:"mimeType"`
	OutputBytes int                     `json:"outputBytes"`
	Width       *int                    `json:"width,omitempty"`
	Height      *int                    `json:"height,omitempty"`
	Usage       ProviderAcceptanceUsage `json:"usage"`
}

type ProviderAcceptanceResult struct {
	Status        string                    `json:"status"`
	Provider      string                    `json:"provider"`
	PaidCallCount int                       `json:"paidCallCount"`
	Checks        []ProviderAcceptanceCheck `json:"checks"`
}

// MediaProviderAcceptanceResult is the deliberately smaller evidence surface
// used by the paid Video/Music staging checks. Provider-specific URLs,
// prompts, response bodies, and request identifiers never leave the runtime.
type MediaProviderAcceptanceResult struct {
	Status        string                         `json:"status"`
	Provider      string                         `json:"provider"`
	PaidCallCount int                            `json:"paidCallCount"`
	Checks        []MediaProviderAcceptanceCheck `json:"checks"`
}

type MediaProviderAcceptanceCheck struct {
	Mode        string `json:"mode"`
	Model       string `json:"model"`
	MIMEType    string `json:"mimeType"`
	OutputBytes int    `json:"outputBytes"`
	Width       *int   `json:"width,omitempty"`
	Height      *int   `json:"height,omitempty"`
}

// RunOpenAIStagingAcceptance performs exactly one Chat and one Image call.
// It returns only bounded metadata and never exposes prompts or output bodies.
func RunOpenAIStagingAcceptance(ctx context.Context, catalog *RuntimeCatalog, chatModel, imageModel string, expectedWidth, expectedHeight int) (ProviderAcceptanceResult, error) {
	if catalog == nil || chatModel == "" || imageModel == "" || expectedWidth < 1 || expectedHeight < 1 ||
		!catalog.Available("openai", "chat", chatModel) || !catalog.Available("openai", "image", imageModel) {
		return ProviderAcceptanceResult{}, fmt.Errorf("OpenAI staging acceptance capability is unavailable")
	}
	result := ProviderAcceptanceResult{Provider: "openai", Checks: make([]ProviderAcceptanceCheck, 0, 2)}
	requests := []ProviderRequest{
		{GenerationID: uuid.New(), Mode: "chat", Provider: "openai", ModelName: chatModel, Prompt: "Reply with one short sentence confirming a staged AI creation system check. Do not include personal data."},
		{GenerationID: uuid.New(), Mode: "image", Provider: "openai", ModelName: imageModel, Prompt: "A minimal black circle centered on a plain white background, no text, no people, no logos."},
	}
	for _, request := range requests {
		output, err := catalog.Generate(ctx, request)
		result.PaidCallCount++
		if err != nil {
			return result, fmt.Errorf("OpenAI staging %s check failed: %w", request.Mode, err)
		}
		if output.Usage == nil {
			return result, fmt.Errorf("OpenAI staging %s check did not report usage", request.Mode)
		}
		if request.Mode == "image" && (output.Width == nil || output.Height == nil || *output.Width != expectedWidth || *output.Height != expectedHeight) {
			return result, fmt.Errorf("OpenAI staging image dimensions did not match the configured size")
		}
		result.Checks = append(result.Checks, ProviderAcceptanceCheck{
			Mode: request.Mode, Model: request.ModelName, MIMEType: output.MIMEType, OutputBytes: len(output.Content), Width: output.Width, Height: output.Height,
			Usage: ProviderAcceptanceUsage{
				InputTokens: output.Usage.InputTokens, CachedInputTokens: output.Usage.CachedInputTokens,
				OutputTokens: output.Usage.OutputTokens, ReasoningTokens: output.Usage.ReasoningTokens, TotalTokens: output.Usage.TotalTokens,
			},
		})
	}
	result.Status = "passed"
	return result, nil
}

// RunVideoStagingAcceptance performs one bounded Video call. Seedance is an
// asynchronous API, but the Provider runtime owns polling and returns only a
// typed MP4 result to this acceptance boundary.
func RunVideoStagingAcceptance(ctx context.Context, catalog *RuntimeCatalog, model string) (MediaProviderAcceptanceResult, error) {
	if catalog == nil || model == "" || !catalog.Available("byteplus_video", "video", model) {
		return MediaProviderAcceptanceResult{}, fmt.Errorf("Video staging acceptance capability is unavailable")
	}
	result := MediaProviderAcceptanceResult{Provider: "byteplus_video", Checks: make([]MediaProviderAcceptanceCheck, 0, 1)}
	output, err := catalog.Generate(ctx, ProviderRequest{
		GenerationID: uuid.New(), Mode: "video", Provider: "byteplus_video", ModelName: model,
		Prompt:     "A short abstract product reveal on a clean studio background, no people, no text, no logos.",
		Parameters: GenerationParameters{AspectRatio: "16:9", DurationSeconds: 5},
	})
	result.PaidCallCount++
	if err != nil {
		return result, fmt.Errorf("Video staging check failed: %w", err)
	}
	if output.MIMEType != "video/mp4" || output.Extension != ".mp4" || output.Width == nil || output.Height == nil || len(output.Content) == 0 {
		return result, fmt.Errorf("Video staging check returned an invalid MP4 contract")
	}
	result.Checks = append(result.Checks, MediaProviderAcceptanceCheck{
		Mode: "video", Model: model, MIMEType: output.MIMEType, OutputBytes: len(output.Content), Width: output.Width, Height: output.Height,
	})
	result.Status = "passed"
	return result, nil
}

// RunMusicStagingAcceptance performs one bounded Music call and verifies that
// the adapter returned an MP3 asset rather than an untyped URL or body.
func RunMusicStagingAcceptance(ctx context.Context, catalog *RuntimeCatalog, model string) (MediaProviderAcceptanceResult, error) {
	if catalog == nil || model == "" || !catalog.Available("minimax_music", "music", model) {
		return MediaProviderAcceptanceResult{}, fmt.Errorf("Music staging acceptance capability is unavailable")
	}
	result := MediaProviderAcceptanceResult{Provider: "minimax_music", Checks: make([]MediaProviderAcceptanceCheck, 0, 1)}
	output, err := catalog.Generate(ctx, ProviderRequest{
		GenerationID: uuid.New(), Mode: "music", Provider: "minimax_music", ModelName: model,
		Prompt:     "A short instrumental electronic bed with a restrained, cinematic finish; no vocals or copyrighted melodies.",
		Parameters: GenerationParameters{DurationSeconds: 30},
	})
	result.PaidCallCount++
	if err != nil {
		return result, fmt.Errorf("Music staging check failed: %w", err)
	}
	if output.MIMEType != "audio/mpeg" || output.Extension != ".mp3" || len(output.Content) == 0 {
		return result, fmt.Errorf("Music staging check returned an invalid MP3 contract")
	}
	result.Checks = append(result.Checks, MediaProviderAcceptanceCheck{
		Mode: "music", Model: model, MIMEType: output.MIMEType, OutputBytes: len(output.Content),
	})
	result.Status = "passed"
	return result, nil
}
