package creation

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeGenerationParametersByMode(t *testing.T) {
	tests := []struct {
		mode string
		want GenerationParameters
	}{
		{"chat", GenerationParameters{OutputFormat: "txt", ResponseLength: "balanced"}},
		{"image", GenerationParameters{AspectRatio: "auto", Quality: "auto", OutputFormat: "jpeg"}},
		{"video", GenerationParameters{AspectRatio: "auto", Quality: "auto", OutputFormat: "mp4", DurationSeconds: 10}},
		{"music", GenerationParameters{Quality: "auto", OutputFormat: "wav", DurationSeconds: 30}},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			got, err := normalizeGenerationParameters(test.mode, GenerationParameters{})
			if err != nil || got != test.want {
				t.Fatalf("normalize %s parameters: got=%#v want=%#v err=%v", test.mode, got, test.want, err)
			}
		})
	}
}

func TestNormalizeSourceAssetIDsPreservesOrderAndDeduplicates(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	got, err := normalizeSourceAssetIDs(&first, []uuid.UUID{first, second, first})
	if err != nil || len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("unexpected normalized references: %#v err=%v", got, err)
	}
	tooMany := make([]uuid.UUID, 9)
	for index := range tooMany {
		tooMany[index] = uuid.New()
	}
	if _, err := normalizeSourceAssetIDs(nil, tooMany); err != ErrInvalid {
		t.Fatalf("expected reference limit to fail, got %v", err)
	}
}

func TestNormalizeGenerationParametersRejectsCrossModeSettings(t *testing.T) {
	tests := []struct {
		mode  string
		input GenerationParameters
	}{
		{"chat", GenerationParameters{DurationSeconds: 10}},
		{"image", GenerationParameters{ResponseLength: "short"}},
		{"video", GenerationParameters{OutputFormat: "wav"}},
		{"music", GenerationParameters{AspectRatio: "16:9"}},
		{"video", GenerationParameters{DurationSeconds: 60}},
	}
	for _, test := range tests {
		if _, err := normalizeGenerationParameters(test.mode, test.input); err != ErrInvalid {
			t.Errorf("expected %s parameters %#v to fail, got %v", test.mode, test.input, err)
		}
	}
}

func TestLocalMusicDurationControlsWaveLength(t *testing.T) {
	runtime := NewLocalRuntime("unused")
	short, err := runtime.Generate(context.Background(), ProviderRequest{
		Mode: "music", Prompt: "short track", Parameters: GenerationParameters{DurationSeconds: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	long, err := runtime.Generate(context.Background(), ProviderRequest{
		Mode: "music", Prompt: "long track", Parameters: GenerationParameters{DurationSeconds: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(short.Content) != 44+16000*5*2 || len(long.Content) != 44+16000*10*2 || len(long.Content) <= len(short.Content) {
		t.Fatalf("duration was not reflected in WAV length: short=%d long=%d", len(short.Content), len(long.Content))
	}
}
