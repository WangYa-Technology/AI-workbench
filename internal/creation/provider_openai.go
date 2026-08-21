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
	"net/url"
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
	APIKey              string // Backwards-compatible fallback for both capabilities.
	ChatAPIKey          string
	ImageAPIKey         string
	BaseURL             string
	ChatAPI             string
	ChatModel           string
	ImageModel          string
	ImageAsync          bool
	ImagePollInterval   time.Duration
	ImageTimeout        time.Duration
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
	if config.ChatAPIKey == "" {
		config.ChatAPIKey = config.APIKey
	}
	if config.ImageAPIKey == "" {
		config.ImageAPIKey = config.APIKey
	}
	if config.ChatAPI == "" {
		config.ChatAPI = "responses"
	}
	if config.ImagePollInterval <= 0 {
		config.ImagePollInterval = 10 * time.Second
	}
	if config.ImageTimeout <= 0 {
		config.ImageTimeout = 5 * time.Minute
	}
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
	if r.config.ChatAPI == "chat_completions" {
		return r.generateChatCompletions(ctx, request)
	}
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
	if err := r.postJSONWithKey(ctx, "/responses", payload, &response, maxOpenAITextBodyBytes, r.config.ChatAPIKey); err != nil {
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

func (r *OpenAIRuntime) generateChatCompletions(ctx context.Context, request ProviderRequest) (ProviderOutput, error) {
	maxOutputTokens := r.config.ChatMaxOutputTokens
	if request.Parameters.ResponseLength == "short" && maxOutputTokens > 512 {
		maxOutputTokens = 512
	}
	messages := request.Messages
	if len(messages) == 0 {
		messages = []ProviderMessage{{Role: "user", Content: request.Prompt}}
	}
	payload := struct {
		Model     string            `json:"model"`
		Messages  []ProviderMessage `json:"messages"`
		MaxTokens int               `json:"max_tokens"`
		Stream    bool              `json:"stream"`
	}{request.ModelName, messages, maxOutputTokens, false}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := r.postJSONWithKey(ctx, "/chat/completions", payload, &response, maxOpenAITextBodyBytes, r.config.ChatAPIKey); err != nil {
		return ProviderOutput{}, err
	}
	if len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	result := response.Choices[0].Message.Content
	var usage *ProviderUsage
	if response.Usage != nil {
		candidate := &ProviderUsage{InputTokens: response.Usage.PromptTokens, OutputTokens: response.Usage.CompletionTokens, TotalTokens: response.Usage.TotalTokens}
		if candidate.InputTokens < 0 || candidate.OutputTokens < 0 || candidate.TotalTokens < candidate.InputTokens+candidate.OutputTokens {
			return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
		}
		usage = candidate
	}
	return ProviderOutput{
		Kind: "document", MIMEType: "text/plain; charset=utf-8", Extension: ".txt",
		Text: &result, Content: []byte(result), Usage: usage,
	}, nil
}

func (r *OpenAIRuntime) generateImage(ctx context.Context, request ProviderRequest) (ProviderOutput, error) {
	if r.config.ImageAsync {
		return r.generateAsyncImage(ctx, request)
	}
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
	if err := r.postJSONWithKey(ctx, "/images/generations", payload, &response, maxOpenAIImageBodyBytes, r.config.ImageAPIKey); err != nil {
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

type asyncImageTaskEnvelope struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	ImageURL string `json:"image_url"`
	Result   struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	} `json:"result"`
	Error struct {
		Code string `json:"code"`
		Type string `json:"type"`
	} `json:"error"`
}

func (r *OpenAIRuntime) generateAsyncImage(ctx context.Context, request ProviderRequest) (ProviderOutput, error) {
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
	payload := struct {
		Model        string `json:"model"`
		Prompt       string `json:"prompt"`
		Size         string `json:"size"`
		OutputFormat string `json:"output_format"`
	}{request.ModelName, request.Prompt, size, "png"}
	var submitted asyncImageTaskEnvelope
	if err := r.postJSONWithKey(ctx, "/images/generations/async", payload, &submitted, maxOpenAITextBodyBytes, r.config.ImageAPIKey); err != nil {
		return ProviderOutput{}, err
	}
	taskID := submitted.TaskID
	if taskID == "" {
		taskID = submitted.ID
	}
	if taskID == "" {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	deadline := time.Now().Add(r.config.ImageTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return ProviderOutput{}, NewProviderFailure("provider_timeout", 0)
		}
		if time.Now().After(deadline) {
			return ProviderOutput{}, NewProviderFailure("provider_timeout", 0)
		}
		var status asyncImageTaskEnvelope
		if err := r.getJSONWithKey(ctx, "/images/tasks/"+url.PathEscape(taskID), &status, maxOpenAITextBodyBytes, r.config.ImageAPIKey); err != nil {
			return ProviderOutput{}, err
		}
		switch strings.ToLower(strings.TrimSpace(status.Status)) {
		case "completed", "succeeded", "success", "done":
			if status.ImageURL == "" && len(status.Result.Data) > 0 {
				status.ImageURL = status.Result.Data[0].URL
				if status.ImageURL == "" && status.Result.Data[0].B64JSON != "" {
					content, err := base64.StdEncoding.DecodeString(status.Result.Data[0].B64JSON)
					if err != nil {
						return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
					}
					return imageProviderOutput(content)
				}
			}
			if status.ImageURL == "" {
				return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
			}
			content, err := r.downloadImage(ctx, status.ImageURL)
			if err != nil {
				return ProviderOutput{}, err
			}
			return imageProviderOutput(content)
		case "failed", "failure", "error", "cancelled", "canceled":
			return ProviderOutput{}, NewProviderFailure("provider_request_failed", 0)
		}
		select {
		case <-ctx.Done():
			return ProviderOutput{}, NewProviderFailure("provider_timeout", 0)
		case <-time.After(r.config.ImagePollInterval):
		}
	}
}

func imageProviderOutput(content []byte) (ProviderOutput, error) {
	if len(content) == 0 || len(content) > maxProviderOutputBytes {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	imageConfig, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil || (format != "png" && format != "jpeg") || imageConfig.Width < 1 || imageConfig.Height < 1 {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	mimeType, extension := "image/png", ".png"
	if format == "jpeg" {
		mimeType, extension = "image/jpeg", ".jpg"
	}
	return ProviderOutput{Kind: "image", MIMEType: mimeType, Extension: extension, Width: intPtr(imageConfig.Width), Height: intPtr(imageConfig.Height), Content: content}, nil
}

func (r *OpenAIRuntime) downloadImage(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return nil, NewProviderFailure("provider_response_invalid", 0)
	}
	base, _ := url.Parse(r.config.BaseURL)
	loopbackFixture := base != nil && (base.Hostname() == "127.0.0.1" || base.Hostname() == "localhost") && u.Scheme == "http" && u.Host == base.Host
	allowedHost := base != nil && (u.Hostname() == base.Hostname() || (base.Hostname() == "api.hctopup.com" && strings.HasSuffix(u.Hostname(), ".hctopup.com")))
	if !loopbackFixture && (u.Scheme != "https" || !allowedHost) {
		return nil, NewProviderFailure("provider_response_invalid", 0)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, NewProviderFailure("provider_response_invalid", 0)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return nil, NewProviderFailure("provider_timeout", 0)
		}
		return nil, NewProviderFailure("provider_request_failed", 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyOpenAIStatus(resp)
	}
	content, tooLarge, err := readBounded(resp.Body, maxOpenAIImageBodyBytes)
	if err != nil || tooLarge {
		return nil, NewProviderFailure("provider_response_invalid", 0)
	}
	return content, nil
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
	return r.postJSONWithKey(ctx, path, payload, destination, maxResponseBytes, r.config.APIKey)
}

func (r *OpenAIRuntime) postJSONWithKey(ctx context.Context, path string, payload, destination any, maxResponseBytes int64, apiKey string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return NewProviderFailure("provider_invalid_request", 0)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.config.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return NewProviderFailure("provider_invalid_request", 0)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
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

func (r *OpenAIRuntime) getJSONWithKey(ctx context.Context, path string, destination any, maxResponseBytes int64, apiKey string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.config.BaseURL+path, nil)
	if err != nil {
		return NewProviderFailure("provider_invalid_request", 0)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
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
