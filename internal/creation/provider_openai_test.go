package creation_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/creation"
)

func TestOpenAIRuntimeChatContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" {
			t.Errorf("unexpected request target: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-secret" || request.Header.Get("OpenAI-Organization") != "org-test" || request.Header.Get("OpenAI-Project") != "proj-test" {
			t.Errorf("required OpenAI headers were not sent")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["model"] != "gpt-test-chat" || body["input"] != "Build a launch brief" || body["store"] != false || body["max_output_tokens"] != float64(512) {
			t.Errorf("unexpected Responses request: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"content":[{"type":"output_text","text":"First"},{"type":"refusal","text":"ignored"}]},{"content":[{"type":"output_text","text":" response"}]}],"usage":{"input_tokens":19,"input_tokens_details":{"cached_tokens":4},"output_tokens":11,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":30}}`))
	}))
	defer server.Close()

	runtime := testOpenAIRuntime(server.URL + "/v1")
	output, err := runtime.Generate(context.Background(), creation.ProviderRequest{
		Provider: "openai", Mode: "chat", ModelName: "gpt-test-chat", Prompt: "Build a launch brief",
		Parameters: creation.GenerationParameters{ResponseLength: "short", OutputFormat: "txt"},
	})
	if err != nil || output.Text == nil || *output.Text != "First response" || string(output.Content) != "First response" {
		t.Fatalf("unexpected Chat output: output=%#v err=%v", output, err)
	}
	if output.Usage == nil || output.Usage.InputTokens != 19 || output.Usage.CachedInputTokens != 4 || output.Usage.OutputTokens != 11 || output.Usage.ReasoningTokens != 3 || output.Usage.TotalTokens != 30 {
		t.Fatalf("unexpected Chat usage evidence: %#v", output.Usage)
	}
}

func TestOpenAIRuntimeChatSendsOrderedConversationMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Input []creation.ProviderMessage `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := []creation.ProviderMessage{
			{Role: "user", Content: "Draft a launch position"},
			{Role: "assistant", Content: "Initial position"},
			{Role: "user", Content: "Make it useful for product teams"},
		}
		if len(body.Input) != len(want) {
			t.Fatalf("unexpected conversation length: %#v", body.Input)
		}
		for index := range want {
			if body.Input[index] != want[index] {
				t.Fatalf("conversation order changed: got=%#v want=%#v", body.Input, want)
			}
		}
		_, _ = w.Write([]byte(`{"output":[{"content":[{"type":"output_text","text":"Product-ready position"}]}]}`))
	}))
	defer server.Close()

	runtime := testOpenAIRuntime(server.URL)
	output, err := runtime.Generate(context.Background(), creation.ProviderRequest{
		Provider: "openai", Mode: "chat", ModelName: "gpt-test-chat", Prompt: "Make it useful for product teams",
		Messages: []creation.ProviderMessage{
			{Role: "user", Content: "Draft a launch position"},
			{Role: "assistant", Content: "Initial position"},
			{Role: "user", Content: "Make it useful for product teams"},
		},
	})
	if err != nil || output.Text == nil || *output.Text != "Product-ready position" {
		t.Fatalf("unexpected multi-turn response: output=%#v err=%v", output, err)
	}
}

func TestOpenAIRuntimeImageContract(t *testing.T) {
	var imageBytes bytes.Buffer
	source := image.NewRGBA(image.Rect(0, 0, 3, 2))
	source.Set(1, 1, color.RGBA{R: 30, G: 160, B: 220, A: 255})
	if err := png.Encode(&imageBytes, source); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/images/generations" {
			t.Errorf("unexpected image request path: %s", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["model"] != "gpt-test-image" || body["prompt"] != "A precise cyan interface" || body["n"] != float64(1) ||
			body["size"] != "1536x1024" || body["quality"] != "high" || body["output_format"] != "png" || body["moderation"] != "auto" {
			t.Errorf("unexpected Image request: %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageBytes.Bytes())}},
			"usage": map[string]any{
				"input_tokens": 37, "input_tokens_details": map[string]int{"cached_tokens": 5},
				"output_tokens": 2048, "output_tokens_details": map[string]int{"reasoning_tokens": 0}, "total_tokens": 2085,
			},
		})
	}))
	defer server.Close()

	runtime := testOpenAIRuntime(server.URL + "/v1")
	output, err := runtime.Generate(context.Background(), creation.ProviderRequest{
		Provider: "openai", Mode: "image", ModelName: "gpt-test-image", Prompt: "A precise cyan interface",
		Parameters: creation.GenerationParameters{AspectRatio: "16:9", Quality: "high", OutputFormat: "png"},
	})
	if err != nil || output.MIMEType != "image/png" || output.Width == nil || *output.Width != 3 || output.Height == nil || *output.Height != 2 || !bytes.Equal(output.Content, imageBytes.Bytes()) {
		t.Fatalf("unexpected Image output: output=%#v err=%v", output, err)
	}
	if output.Usage == nil || output.Usage.InputTokens != 37 || output.Usage.CachedInputTokens != 5 || output.Usage.OutputTokens != 2048 || output.Usage.ReasoningTokens != 0 || output.Usage.TotalTokens != 2085 {
		t.Fatalf("unexpected Image usage evidence: %#v", output.Usage)
	}
}

func TestOpenAIRuntimeRejectsMaskWhenEditTransportIsUnavailable(t *testing.T) {
	runtime := testOpenAIRuntime("http://127.0.0.1:1")
	maskID := uuid.New()
	_, err := runtime.Generate(context.Background(), creation.ProviderRequest{
		Provider: "openai", Mode: "image", ModelName: "gpt-test-image", Prompt: "Edit the marked region", MaskAssetID: &maskID,
	})
	assertProviderFailure(t, err, "provider_invalid_request", false, 0)
}

func TestOpenAIRuntimeFailureClassificationAndSanitization(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		code       string
		retryable  bool
		delay      time.Duration
	}{
		{"authentication", http.StatusUnauthorized, `{"error":{"message":"secret upstream detail"}}`, "", "provider_authentication", false, 0},
		{"rate limit", http.StatusTooManyRequests, `{"error":{"message":"request req_private"}}`, "9999", "provider_rate_limited", true, 15 * time.Minute},
		{"content rejection", http.StatusBadRequest, `{"error":{"type":"image_generation_user_error","message":"private prompt"}}`, "", "provider_content_rejected", false, 0},
		{"invalid request", http.StatusUnprocessableEntity, `{"error":{"code":"invalid_value","message":"private value"}}`, "", "provider_invalid_request", false, 0},
		{"unavailable", http.StatusServiceUnavailable, `upstream-private-body`, "12", "provider_unavailable", true, 12 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", test.retryAfter)
				w.Header().Set("x-request-id", "req_private")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			_, err := testOpenAIRuntime(server.URL).Generate(context.Background(), creation.ProviderRequest{Mode: "chat", ModelName: "gpt-test-chat", Prompt: "test prompt"})
			assertProviderFailure(t, err, test.code, test.retryable, test.delay)
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "test-secret") {
				t.Fatalf("Provider failure leaked upstream or credential data: %v", err)
			}
		})
	}
}

func TestOpenAIRuntimeRejectsTimeoutMalformedAndOversizedResponses(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			select {
			case <-request.Context().Done():
			case <-time.After(100 * time.Millisecond):
			}
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err := testOpenAIRuntime(server.URL).Generate(ctx, creation.ProviderRequest{Mode: "chat", ModelName: "gpt-test-chat", Prompt: "test prompt"})
		assertProviderFailure(t, err, "provider_timeout", true, 0)
	})

	for _, test := range []struct {
		name string
		body string
	}{
		{"malformed", `{"output":`},
		{"missing output", `{"output":[{"content":[{"type":"refusal","text":"no output"}]}]}`},
		{"invalid usage", `{"output":[{"content":[{"type":"output_text","text":"valid output"}]}],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":11},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":13}}`},
		{"oversized", strings.Repeat("x", 4*1024*1024+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			_, err := testOpenAIRuntime(server.URL).Generate(context.Background(), creation.ProviderRequest{Mode: "chat", ModelName: "gpt-test-chat", Prompt: "test prompt"})
			assertProviderFailure(t, err, "provider_response_invalid", false, 0)
		})
	}
}

func testOpenAIRuntime(baseURL string) *creation.OpenAIRuntime {
	return creation.NewOpenAIRuntime(creation.OpenAIRuntimeConfig{
		APIKey: "test-secret", BaseURL: baseURL,
		ChatModel: "gpt-test-chat", ImageModel: "gpt-test-image", ChatMaxOutputTokens: 777,
		ImageSize: "1024x1024", ImageQuality: "medium", Organization: "org-test", Project: "proj-test",
	})
}

func assertProviderFailure(t *testing.T, err error, expectedCode string, expectedRetryable bool, expectedDelay time.Duration) {
	t.Helper()
	if err == nil || err.Error() != expectedCode {
		t.Fatalf("expected %q, got %v", expectedCode, err)
	}
	var coded interface{ ErrorCode() string }
	var retryable interface{ Retryable() bool }
	var delayed interface{ RetryDelay() time.Duration }
	if !errors.As(err, &coded) || coded.ErrorCode() != expectedCode || !errors.As(err, &retryable) || retryable.Retryable() != expectedRetryable || !errors.As(err, &delayed) || delayed.RetryDelay() != expectedDelay {
		t.Fatalf("unexpected Provider classification: code=%v retryable=%v delay=%v", coded, retryable, delayed)
	}
}
