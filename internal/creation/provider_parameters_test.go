package creation

import (
	"strings"
	"testing"
)

func TestValidateProviderRequestKeepsExternalCapabilitiesExact(t *testing.T) {
	baseVideo := GenerationParameters{AspectRatio: "16:9", Quality: "auto", OutputFormat: "mp4", DurationSeconds: 10}
	if err := validateProviderRequest("byteplus_video", "video", baseVideo, 0, false); err != nil {
		t.Fatalf("accepted Seedance duration was rejected: %v", err)
	}
	baseVideo.DurationSeconds = 30
	if err := validateProviderRequest("byteplus_video", "video", baseVideo, 0, false); err != ErrInvalid {
		t.Fatalf("unsupported Seedance duration was accepted: %v", err)
	}
	baseVideo.DurationSeconds = 30
	if err := validateProviderRequest("local_test", "video", baseVideo, 0, false); err != nil {
		t.Fatalf("Local Test duration should remain broad: %v", err)
	}
	if err := validateProviderRequest("minimax_music", "music", GenerationParameters{OutputFormat: "wav", DurationSeconds: 30}, 0, false); err != nil {
		t.Fatalf("accepted MiniMax output was rejected: %v", err)
	}
	if err := validateProviderRequest("minimax_music", "music", GenerationParameters{OutputFormat: "wav", DurationSeconds: 30}, 1, false); err != ErrInvalid {
		t.Fatalf("unsupported MiniMax reference was accepted: %v", err)
	}
	if err := validateProviderRequest("openai", "image", GenerationParameters{OutputFormat: "png"}, 0, true); err != ErrInvalid {
		t.Fatalf("unsupported OpenAI image mask was accepted: %v", err)
	}
}

func TestChatReferencesBecomeBoundedDataContextBeforeLatestPrompt(t *testing.T) {
	messages, err := chatMessagesWithReferences([]ProviderMessage{
		{Role: "user", Content: "Earlier request"},
		{Role: "assistant", Content: "Earlier answer"},
		{Role: "user", Content: "Use this brief"},
	}, []ProviderAsset{{MIMEType: "text/plain", Content: []byte("Launch in September")}})
	if err != nil || len(messages) != 4 {
		t.Fatalf("chat reference context was not inserted: messages=%+v err=%v", messages, err)
	}
	if messages[2].Role != "system" || !strings.Contains(messages[2].Content, "Launch in September") || messages[3].Content != "Use this brief" {
		t.Fatalf("reference context was not placed before the latest prompt: %+v", messages)
	}
	if _, err := chatMessagesWithReferences([]ProviderMessage{{Role: "user", Content: "Prompt"}}, []ProviderAsset{{MIMEType: "image/png", Content: []byte("binary")}}); err != ErrInvalid {
		t.Fatalf("non-text chat reference was accepted: %v", err)
	}
}

func TestVideoReferencesStayWithinReviewedImageContract(t *testing.T) {
	kinds := referenceKindsForMode("video")
	if len(kinds) != 1 || kinds[0] != "image" {
		t.Fatalf("video reference kinds widened beyond the reviewed adapter contract: %#v", kinds)
	}
}

func TestMusicCapabilitySeparatesRequestedAndProviderResultFormats(t *testing.T) {
	capability := adjustCreationCapability(CreationCapability{
		Mode: "music", Provider: "minimax_music", OutputFormats: []string{"wav"}, ResultFormats: []string{"wav"}, ReferenceKinds: []string{"audio"},
	})
	if len(capability.OutputFormats) != 1 || capability.OutputFormats[0] != "wav" || len(capability.ResultFormats) != 1 || capability.ResultFormats[0] != "mp3" || len(capability.ReferenceKinds) != 0 {
		t.Fatalf("MiniMax capability projection did not separate request/result formats: %#v", capability)
	}
}
