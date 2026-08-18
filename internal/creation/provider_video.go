package creation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxVideoProviderBytes = 100 * 1024 * 1024

type VideoRuntimeConfig struct {
	APIKey       string
	BaseURL      string
	Model        string
	PollInterval time.Duration
	Timeout      time.Duration
	HTTPClient   *http.Client
}

type VideoRuntime struct {
	config VideoRuntimeConfig
	client *http.Client
}

func NewVideoRuntime(config VideoRuntimeConfig) *VideoRuntime {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	if config.PollInterval <= 0 {
		config.PollInterval = 10 * time.Second
	}
	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Minute
	}
	return &VideoRuntime{config: config, client: client}
}

func (r *VideoRuntime) Provider() string { return "byteplus_video" }

func (r *VideoRuntime) Supports(mode, modelName string) bool {
	return mode == "video" && modelName != "" && modelName == r.config.Model
}

type videoTaskEnvelope struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	VideoURL string `json:"video_url"`
	Content  struct {
		VideoURL string `json:"video_url"`
		URL      string `json:"url"`
	} `json:"content"`
	Data *struct {
		ID       string `json:"id"`
		TaskID   string `json:"task_id"`
		Status   string `json:"status"`
		VideoURL string `json:"video_url"`
		Content  struct {
			VideoURL string `json:"video_url"`
			URL      string `json:"url"`
		} `json:"content"`
	} `json:"data"`
	Error any `json:"error"`
}

func (r *VideoRuntime) Generate(ctx context.Context, request ProviderRequest) (ProviderOutput, error) {
	if len(request.ReferenceAssets) != len(request.ReferenceAssetIDs) {
		return ProviderOutput{}, NewProviderFailure("provider_invalid_request", 0)
	}
	duration := request.Parameters.DurationSeconds
	if duration == 0 {
		duration = 10
	}
	if duration != 5 && duration != 10 {
		return ProviderOutput{}, NewProviderFailure("provider_invalid_request", 0)
	}
	ratio := request.Parameters.AspectRatio
	if ratio == "" || ratio == "auto" {
		ratio = "16:9"
	}
	if ratio == "4:5" {
		ratio = "3:4"
	}
	content := []map[string]any{{"type": "text", "text": request.Prompt}}
	for _, asset := range request.ReferenceAssets {
		if asset.MIMEType != "image/jpeg" && asset.MIMEType != "image/png" {
			return ProviderOutput{}, NewProviderFailure("provider_invalid_request", 0)
		}
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": "data:" + asset.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(asset.Content)},
			"role":      "reference_image",
		})
	}
	payload := map[string]any{
		"model":          request.ModelName,
		"content":        content,
		"duration":       duration,
		"ratio":          ratio,
		"generate_audio": true,
		"watermark":      false,
	}
	deadline := time.Now().Add(r.config.Timeout)
	created, err := r.doJSON(ctx, http.MethodPost, "/contents/generations/tasks", payload)
	if err != nil {
		return ProviderOutput{}, err
	}
	taskID := videoTaskID(created)
	if taskID == "" {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	for {
		if err := ctx.Err(); err != nil {
			return ProviderOutput{}, NewProviderFailure("provider_timeout", 0)
		}
		if time.Now().After(deadline) {
			return ProviderOutput{}, NewProviderFailure("provider_timeout", 0)
		}
		statusPayload, err := r.doJSON(ctx, http.MethodGet, "/contents/generations/tasks/"+url.PathEscape(taskID), nil)
		if err != nil {
			return ProviderOutput{}, err
		}
		status := strings.ToLower(videoStatus(statusPayload))
		switch status {
		case "succeeded", "success", "completed", "done":
			outputURL := videoOutputURL(statusPayload)
			if outputURL == "" {
				return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
			}
			content, err := r.download(ctx, outputURL)
			if err != nil {
				return ProviderOutput{}, err
			}
			if !looksLikeMP4(content) {
				return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
			}
			return ProviderOutput{Kind: "video", MIMEType: "video/mp4", Extension: ".mp4", Width: videoWidth(ratio), Height: videoHeight(ratio), Content: content}, nil
		case "failed", "failure", "error", "cancelled", "canceled":
			return ProviderOutput{}, NewProviderFailure("provider_content_rejected", 0)
		}
		select {
		case <-ctx.Done():
			return ProviderOutput{}, NewProviderFailure("provider_timeout", 0)
		case <-time.After(r.config.PollInterval):
		}
	}
}

func (r *VideoRuntime) doJSON(ctx context.Context, method, path string, payload any) (videoTaskEnvelope, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return videoTaskEnvelope{}, NewProviderFailure("provider_invalid_request", 0)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.config.BaseURL+path, body)
	if err != nil {
		return videoTaskEnvelope{}, NewProviderFailure("provider_invalid_request", 0)
	}
	req.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return videoTaskEnvelope{}, NewProviderFailure("provider_timeout", 0)
		}
		return videoTaskEnvelope{}, NewProviderFailure("provider_request_failed", 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return videoTaskEnvelope{}, classifyHTTPStatus(resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	data, tooLarge, err := readProviderBody(resp.Body, 2*1024*1024)
	if err != nil || tooLarge {
		return videoTaskEnvelope{}, NewProviderFailure("provider_response_invalid", 0)
	}
	var envelope videoTaskEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return videoTaskEnvelope{}, NewProviderFailure("provider_response_invalid", 0)
	}
	return envelope, nil
}

func (r *VideoRuntime) download(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return nil, NewProviderFailure("provider_response_invalid", 0)
	}
	base, _ := url.Parse(r.config.BaseURL)
	loopbackFixture := base != nil && (base.Hostname() == "127.0.0.1" || base.Hostname() == "localhost") && u.Scheme == "http" && u.Host == base.Host
	if u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !loopbackFixture) {
		return nil, NewProviderFailure("provider_response_invalid", 0)
	}
	if !loopbackFixture && !strings.HasSuffix(strings.ToLower(u.Hostname()), ".volces.com") {
		return nil, NewProviderFailure("provider_response_invalid", 0)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, NewProviderFailure("provider_response_invalid", 0)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, NewProviderFailure("provider_request_failed", 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, classifyHTTPStatus(resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	content, tooLarge, err := readProviderBody(resp.Body, maxVideoProviderBytes)
	if err != nil || tooLarge || len(content) == 0 {
		return nil, NewProviderFailure("provider_response_invalid", 0)
	}
	return content, nil
}

func videoTaskID(value videoTaskEnvelope) string {
	if value.TaskID != "" {
		return value.TaskID
	}
	if value.ID != "" {
		return value.ID
	}
	if value.Data != nil {
		if value.Data.TaskID != "" {
			return value.Data.TaskID
		}
		return value.Data.ID
	}
	return ""
}

func videoStatus(value videoTaskEnvelope) string {
	if value.Status != "" {
		return value.Status
	}
	if value.Data != nil {
		return value.Data.Status
	}
	return ""
}

func videoOutputURL(value videoTaskEnvelope) string {
	for _, candidate := range []string{value.VideoURL, value.Content.VideoURL, value.Content.URL} {
		if candidate != "" {
			return candidate
		}
	}
	if value.Data != nil {
		for _, candidate := range []string{value.Data.VideoURL, value.Data.Content.VideoURL, value.Data.Content.URL} {
			if candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func looksLikeMP4(content []byte) bool { return len(content) >= 12 && string(content[4:8]) == "ftyp" }

func videoWidth(ratio string) *int {
	value := map[string]int{"16:9": 1280, "9:16": 720, "1:1": 720, "3:4": 720}[ratio]
	if value == 0 {
		value = 1280
	}
	return &value
}
func videoHeight(ratio string) *int {
	value := map[string]int{"16:9": 720, "9:16": 1280, "1:1": 720, "3:4": 960}[ratio]
	if value == 0 {
		value = 720
	}
	return &value
}

func readProviderBody(reader io.Reader, limit int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	return body, int64(len(body)) > limit, nil
}

func classifyHTTPStatus(status int, retryAfter string) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return NewProviderFailure("provider_authentication", 0)
	}
	if status == http.StatusTooManyRequests {
		return NewProviderFailure("provider_rate_limited", parseRetryAfterHeader(retryAfter))
	}
	if status >= 500 {
		return NewProviderFailure("provider_unavailable", parseRetryAfterHeader(retryAfter))
	}
	return NewProviderFailure("provider_invalid_request", 0)
}

func parseRetryAfterHeader(value string) time.Duration {
	var seconds int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &seconds); err != nil || seconds < 0 {
		return 0
	}
	if seconds > 900 {
		seconds = 900
	}
	return time.Duration(seconds) * time.Second
}
