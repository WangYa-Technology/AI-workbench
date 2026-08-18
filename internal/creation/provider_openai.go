package creation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	maxProviderOutputBytes  = 100 * 1024 * 1024
	maxOpenAITextBodyBytes  = 4 * 1024 * 1024
	maxOpenAIImageBodyBytes = 136 * 1024 * 1024
	maxOpenAIErrorBodyBytes = 64 * 1024
	maxOpenAIRetryAfter     = 15 * time.Minute
)

type OpenAIRuntimeConfig struct {
	APIKey              string
	BaseURL             string
	ChatModel           string
	ImageModel          string
	ChatMaxOutputTokens int
	ImageSize           string
	ImageQuality        string
	Organization        string
	Project             string
	HTTPClient          *http.Client
}

type OpenAIRuntime struct {
	config OpenAIRuntimeConfig
	client *http.Client
}

type openAIUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	InputDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func NewOpenAIRuntime(config OpenAIRuntimeConfig) *OpenAIRuntime {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	return &OpenAIRuntime{config: config, client: client}
}

func (r *OpenAIRuntime) Provider() string { return "openai" }

func (r *OpenAIRuntime) Supports(mode, modelName string) bool {
	switch mode {
	case "chat":
		return modelName != "" && modelName == r.config.ChatModel
	case "image":
		return modelName != "" && modelName == r.config.ImageModel
	default:
		return false
	}
}

func (r *OpenAIRuntime) Generate(ctx context.Context, request ProviderRequest) (ProviderOutput, error) {
	switch request.Mode {
	case "chat":
		return r.generateChat(ctx, request)
	case "image":
		return r.generateImage(ctx, request)
	default:
		return ProviderOutput{}, NewProviderFailure("provider_invalid_request", 0)
	}
}

func (r *OpenAIRuntime) generateChat(ctx context.Context, request ProviderRequest) (ProviderOutput, error) {
	maxOutputTokens := r.config.ChatMaxOutputTokens
	if request.Parameters.ResponseLength == "short" && maxOutputTokens > 512 {
		maxOutputTokens = 512
	}
	input := any(request.Prompt)
	if len(request.Messages) > 0 {
		input = request.Messages
	}
	payload := struct {
		Model           string `json:"model"`
		Input           any    `json:"input"`
		Store           bool   `json:"store"`
		MaxOutputTokens int    `json:"max_output_tokens"`
	}{request.ModelName, input, false, maxOutputTokens}
	var response struct {
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage *openAIUsage `json:"usage"`
	}
	if err := r.postJSON(ctx, "/responses", payload, &response, maxOpenAITextBodyBytes); err != nil {
		return ProviderOutput{}, err
	}
	var text strings.Builder
	for _, item := range response.Output {
		for _, content := range item.Content {
			if content.Type == "output_text" {
				text.WriteString(content.Text)
			}
		}
	}
	result := text.String()
	if strings.TrimSpace(result) == "" {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	usage, err := providerUsageFromOpenAI(response.Usage)
	if err != nil {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	return ProviderOutput{
		Kind: "document", MIMEType: "text/plain; charset=utf-8", Extension: ".txt",
		Text: &result, Content: []byte(result), Usage: usage,
	}, nil
}

func (r *OpenAIRuntime) generateImage(ctx context.Context, request ProviderRequest) (ProviderOutput, error) {
	if request.MaskAssetID != nil {
		return ProviderOutput{}, NewProviderFailure("provider_invalid_request", 0)
	}
	size := r.config.ImageSize
	switch request.Parameters.AspectRatio {
	case "1:1":
		size = "1024x1024"
	case "4:5":
		size = "1024x1536"
	case "16:9":
		size = "1536x1024"
	}
	quality := r.config.ImageQuality
	switch request.Parameters.Quality {
	case "standard":
		quality = "medium"
	case "high":
		quality = "high"
	}
	outputFormat := request.Parameters.OutputFormat
	if outputFormat != "jpeg" && outputFormat != "png" {
		outputFormat = "png"
	}
	payload := struct {
		Model        string `json:"model"`
		Prompt       string `json:"prompt"`
		N            int    `json:"n"`
		Size         string `json:"size"`
		Quality      string `json:"quality"`
		OutputFormat string `json:"output_format"`
		Moderation   string `json:"moderation"`
	}{request.ModelName, request.Prompt, 1, size, quality, outputFormat, "auto"}
	var response struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
		Usage *openAIUsage `json:"usage"`
	}
	if err := r.postJSON(ctx, "/images/generations", payload, &response, maxOpenAIImageBodyBytes); err != nil {
		return ProviderOutput{}, err
	}
	if len(response.Data) == 0 || response.Data[0].B64JSON == "" || base64.StdEncoding.DecodedLen(len(response.Data[0].B64JSON)) > maxProviderOutputBytes {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	content, err := base64.StdEncoding.DecodeString(response.Data[0].B64JSON)
	if err != nil || len(content) == 0 || len(content) > maxProviderOutputBytes {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	imageConfig, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil || format != outputFormat || imageConfig.Width < 1 || imageConfig.Height < 1 {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	usage, err := providerUsageFromOpenAI(response.Usage)
	if err != nil {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	mimeType, extension := "image/png", ".png"
	if outputFormat == "jpeg" {
		mimeType, extension = "image/jpeg", ".jpg"
	}
	return ProviderOutput{
		Kind: "image", MIMEType: mimeType, Extension: extension,
		Width: intPtr(imageConfig.Width), Height: intPtr(imageConfig.Height), Content: content, Usage: usage,
	}, nil
}

func providerUsageFromOpenAI(value *openAIUsage) (*ProviderUsage, error) {
	if value == nil {
		return nil, nil
	}
	usage := &ProviderUsage{
		InputTokens: value.InputTokens, CachedInputTokens: value.InputDetails.CachedTokens,
		OutputTokens: value.OutputTokens, ReasoningTokens: value.OutputDetails.ReasoningTokens, TotalTokens: value.TotalTokens,
	}
	if usage.TotalTokens < usage.InputTokens+usage.OutputTokens || usage.CachedInputTokens > usage.InputTokens || usage.ReasoningTokens > usage.OutputTokens ||
		usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 || usage.CachedInputTokens < 0 || usage.ReasoningTokens < 0 {
		return nil, errors.New("OpenAI usage response is invalid")
	}
	return usage, nil
}

func (r *OpenAIRuntime) postJSON(ctx context.Context, path string, payload, destination any, maxResponseBytes int64) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return NewProviderFailure("provider_invalid_request", 0)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.config.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return NewProviderFailure("provider_invalid_request", 0)
	}
	request.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	if r.config.Organization != "" {
		request.Header.Set("OpenAI-Organization", r.config.Organization)
	}
	if r.config.Project != "" {
		request.Header.Set("OpenAI-Project", r.config.Project)
	}
	response, err := r.client.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return NewProviderFailure("provider_timeout", 0)
		}
		return NewProviderFailure("provider_request_failed", 0)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return classifyOpenAIStatus(response)
	}
	responseBody, tooLarge, err := readBounded(response.Body, maxResponseBytes)
	if err != nil {
		return NewProviderFailure("provider_request_failed", 0)
	}
	if tooLarge || json.Unmarshal(responseBody, destination) != nil {
		return NewProviderFailure("provider_response_invalid", 0)
	}
	return nil
}

func classifyOpenAIStatus(response *http.Response) error {
	retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), time.Now())
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return NewProviderFailure("provider_authentication", 0)
	case http.StatusRequestTimeout:
		return NewProviderFailure("provider_timeout", retryAfter)
	case http.StatusTooManyRequests:
		return NewProviderFailure("provider_rate_limited", retryAfter)
	}
	if response.StatusCode >= http.StatusInternalServerError {
		return NewProviderFailure("provider_unavailable", retryAfter)
	}
	body, _, _ := readBounded(response.Body, maxOpenAIErrorBodyBytes)
	var envelope struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	classification := strings.ToLower(envelope.Error.Code + " " + envelope.Error.Type)
	if strings.Contains(classification, "content_policy") || strings.Contains(classification, "image_generation_user_error") || strings.Contains(classification, "safety") {
		return NewProviderFailure("provider_content_rejected", 0)
	}
	return NewProviderFailure("provider_invalid_request", 0)
}

func readBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limit {
		return nil, true, nil
	}
	return body, false, nil
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var delay time.Duration
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		}
	} else if retryAt, err := http.ParseTime(value); err == nil {
		delay = retryAt.Sub(now)
	}
	if delay < 0 {
		return 0
	}
	if delay > maxOpenAIRetryAfter {
		return maxOpenAIRetryAfter
	}
	return delay
}
