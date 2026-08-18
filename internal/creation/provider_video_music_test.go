package creation_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/creation"
)

func TestVideoRuntimeCreatesPollsAndPersistsTypedOutput(t *testing.T) {
	var statusCalls atomic.Int32
	var fixture *httptest.Server
	fixture = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer video-secret" && request.URL.Path != "/output.mp4" {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v3/contents/generations/tasks":
			var body map[string]any
			if json.NewDecoder(request.Body).Decode(&body) != nil || body["model"] != "video-model" || body["duration"] != float64(5) {
				http.Error(response, "invalid body", http.StatusBadRequest)
				return
			}
			content, ok := body["content"].([]any)
			if !ok || len(content) != 2 || !strings.Contains(content[1].(map[string]any)["image_url"].(map[string]any)["url"].(string), "data:image/png;base64,") {
				http.Error(response, "missing reference", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"id": "task-video-1", "status": "queued"})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v3/contents/generations/tasks/task-video-1":
			if statusCalls.Add(1) == 1 {
				_ = json.NewEncoder(response).Encode(map[string]any{"id": "task-video-1", "status": "running"})
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"id": "task-video-1", "status": "succeeded",
				"content": map[string]any{"video_url": fixture.URL + "/output.mp4?signature=bounded"},
			})
		case request.Method == http.MethodGet && request.URL.Path == "/output.mp4":
			_, _ = response.Write([]byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0})
		default:
			http.NotFound(response, request)
		}
	}))
	defer fixture.Close()

	runtime := creation.NewVideoRuntime(creation.VideoRuntimeConfig{
		APIKey: "video-secret", BaseURL: fixture.URL + "/api/v3", Model: "video-model",
		PollInterval: time.Millisecond, Timeout: time.Second, HTTPClient: fixture.Client(),
	})
	output, err := runtime.Generate(context.Background(), creation.ProviderRequest{
		GenerationID: uuid.New(), Mode: "video", Provider: "byteplus_video", ModelName: "video-model",
		Prompt: "A cinematic product reveal", Parameters: creation.GenerationParameters{AspectRatio: "16:9", DurationSeconds: 5},
		ReferenceAssetIDs: []uuid.UUID{uuid.New()},
		ReferenceAssets:   []creation.ProviderAsset{{ID: uuid.New(), MIMEType: "image/png", Content: []byte("png-reference")}},
	})
	if err != nil || output.MIMEType != "video/mp4" || output.Extension != ".mp4" || output.Width == nil || *output.Width != 1280 || statusCalls.Load() != 2 {
		t.Fatalf("unexpected video result: output=%+v calls=%d err=%v", output, statusCalls.Load(), err)
	}
}

func TestMusicRuntimeGeneratesAndDownloadsMP3(t *testing.T) {
	var fixture *httptest.Server
	fixture = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/music_generation":
			if request.Header.Get("Authorization") != "Bearer music-secret" {
				http.Error(response, "unauthorized", http.StatusUnauthorized)
				return
			}
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"model":"music-3.0"`) || !strings.Contains(string(body), `"output_format":"url"`) {
				http.Error(response, "invalid request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"trace_id": "music-1", "base_resp": map[string]any{"status_code": 0},
				"data": map[string]any{"audio": fixture.URL + "/track.mp3?signature=bounded"},
			})
		case "/track.mp3":
			_, _ = response.Write([]byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 0})
		default:
			http.NotFound(response, request)
		}
	}))
	defer fixture.Close()

	runtime := creation.NewMusicRuntime(creation.MusicRuntimeConfig{
		APIKey: "music-secret", BaseURL: fixture.URL + "/v1", Model: "music-3.0", HTTPClient: fixture.Client(),
	})
	output, err := runtime.Generate(context.Background(), creation.ProviderRequest{
		GenerationID: uuid.New(), Mode: "music", Provider: "minimax_music", ModelName: "music-3.0",
		Prompt: "A restrained electronic score", Parameters: creation.GenerationParameters{DurationSeconds: 30},
	})
	if err != nil || output.Kind != "audio" || output.MIMEType != "audio/mpeg" || output.Extension != ".mp3" || !strings.HasPrefix(string(output.Content), "ID3") {
		t.Fatalf("unexpected music result: output=%+v err=%v", output, err)
	}
}

func TestVideoAndMusicRuntimesFailClosed(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Error(response, "private upstream body must not escape", http.StatusUnauthorized)
	}))
	defer fixture.Close()

	video := creation.NewVideoRuntime(creation.VideoRuntimeConfig{APIKey: "bad", BaseURL: fixture.URL, Model: "video", PollInterval: time.Millisecond, Timeout: time.Second})
	_, videoErr := video.Generate(context.Background(), creation.ProviderRequest{Mode: "video", ModelName: "video", Prompt: "test", Parameters: creation.GenerationParameters{DurationSeconds: 5}})
	music := creation.NewMusicRuntime(creation.MusicRuntimeConfig{APIKey: "bad", BaseURL: fixture.URL, Model: "music"})
	_, musicErr := music.Generate(context.Background(), creation.ProviderRequest{Mode: "music", ModelName: "music", Prompt: "test", Parameters: creation.GenerationParameters{DurationSeconds: 30}})
	for name, err := range map[string]error{"video": videoErr, "music": musicErr} {
		if err == nil || err.Error() != "provider_authentication" || strings.Contains(err.Error(), "private") {
			t.Fatalf("%s failure was not safely classified: %v", name, err)
		}
	}
}
