package creation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

type LocalRuntime struct {
	imageSource string
}

func NewLocalRuntime(imageSource string) *LocalRuntime {
	return &LocalRuntime{imageSource: imageSource}
}

func (r *LocalRuntime) Provider() string { return "local_test" }

func (r *LocalRuntime) Supports(mode, modelName string) bool {
	models := map[string]string{
		"chat":  "hcai-local-chat-v1",
		"image": "hcai-local-image-v1",
		"video": "hcai-local-video-v1",
		"music": "hcai-local-music-v1",
	}
	return models[mode] == modelName
}

func (r *LocalRuntime) Generate(ctx context.Context, request ProviderRequest) (ProviderOutput, error) {
	if err := ctx.Err(); err != nil {
		return ProviderOutput{}, err
	}
	switch request.Mode {
	case "image":
		content, err := os.ReadFile(filepath.Clean(r.imageSource))
		if err != nil {
			return ProviderOutput{}, fmt.Errorf("read deterministic image fixture: %w", err)
		}
		return ProviderOutput{Kind: "image", MIMEType: "image/jpeg", Extension: ".jpg", Width: intPtr(2000), Height: intPtr(2500), Content: content}, nil
	case "video":
		content, err := os.ReadFile(filepath.Join(filepath.Dir(r.imageSource), "local-video-test.mp4"))
		if err != nil {
			return ProviderOutput{}, fmt.Errorf("read deterministic video fixture: %w", err)
		}
		return ProviderOutput{Kind: "video", MIMEType: "video/mp4", Extension: ".mp4", Width: intPtr(960), Height: intPtr(540), Content: content}, nil
	case "music":
		durationSeconds := request.Parameters.DurationSeconds
		if durationSeconds == 0 {
			durationSeconds = 2
		}
		if durationSeconds < 1 || durationSeconds > 60 {
			return ProviderOutput{}, NewProviderFailure("provider_invalid_request", 0)
		}
		return ProviderOutput{Kind: "audio", MIMEType: "audio/wav", Extension: ".wav", Content: deterministicWAV(request.Prompt, durationSeconds)}, nil
	case "chat":
		priorTurns := 0
		for index := 0; index+1 < len(request.Messages); index++ {
			if request.Messages[index].Role == "user" && request.Messages[index+1].Role == "assistant" {
				priorTurns++
			}
		}
		response := fmt.Sprintf("Deterministic Local Test response\n\nYour request was recorded as:\n%s\n\nConversation context: %d prior turn(s).\n\nThis local response verifies durable chat submission, billing, result storage, and provenance. It is not a production model response.", request.Prompt, priorTurns)
		return ProviderOutput{Kind: "document", MIMEType: "text/plain; charset=utf-8", Extension: ".txt", Text: &response, Content: []byte(response)}, nil
	default:
		return ProviderOutput{}, ErrInvalid
	}
}

func NewLocalRuntimeCatalog(imageSource string, enabled bool) *RuntimeCatalog {
	if !enabled {
		return NewRuntimeCatalog()
	}
	return NewRuntimeCatalog(NewLocalRuntime(imageSource))
}

func deterministicWAV(prompt string, durationSeconds int) []byte {
	const sampleRate = 16000
	const bitsPerSample = 16
	sampleCount := sampleRate * durationSeconds
	dataSize := sampleCount * bitsPerSample / 8
	frequency := 220.0 + float64(sha256.Sum256([]byte(prompt))[0])
	buffer := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+dataSize))
	buffer.WriteString("WAVEfmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate*bitsPerSample/8))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(bitsPerSample/8))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(bitsPerSample))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(dataSize))
	for index := range sampleCount {
		envelope := math.Min(1, float64(index)/800) * math.Min(1, float64(sampleCount-index)/1200)
		sample := int16(math.Sin(2*math.Pi*frequency*float64(index)/sampleRate) * 0.18 * envelope * math.MaxInt16)
		_ = binary.Write(buffer, binary.LittleEndian, sample)
	}
	return buffer.Bytes()
}
