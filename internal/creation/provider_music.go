package creation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const maxMusicProviderBytes = 32 * 1024 * 1024

type MusicRuntimeConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

type MusicRuntime struct {
	config MusicRuntimeConfig
	client *http.Client
}

func NewMusicRuntime(config MusicRuntimeConfig) *MusicRuntime {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	return &MusicRuntime{config: config, client: client}
}

func (r *MusicRuntime) Provider() string { return "minimax_music" }
func (r *MusicRuntime) Supports(mode, modelName string) bool {
	return mode == "music" && modelName != "" && modelName == r.config.Model
}

func (r *MusicRuntime) Generate(ctx context.Context, request ProviderRequest) (ProviderOutput, error) {
	if len(request.ReferenceAssetIDs) > 0 || len(request.ReferenceAssets) > 0 {
		return ProviderOutput{}, NewProviderFailure("provider_invalid_request", 0)
	}
	duration := request.Parameters.DurationSeconds
	if duration == 0 {
		duration = 30
	}
	if duration != 5 && duration != 10 && duration != 30 && duration != 60 {
		return ProviderOutput{}, NewProviderFailure("provider_invalid_request", 0)
	}
	payload := map[string]any{
		"model":            request.ModelName,
		"prompt":           request.Prompt,
		"lyrics_optimizer": true,
		"is_instrumental":  false,
		"audio_setting":    map[string]any{"sample_rate": 44100, "bitrate": 256000, "format": "mp3"},
		"output_format":    "url",
	}
	response, err := r.post(ctx, "/music_generation", payload)
	if err != nil {
		return ProviderOutput{}, err
	}
	if response.BaseResp.StatusCode != 0 || response.Data.Audio == "" {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	audioURL, err := url.Parse(response.Data.Audio)
	if err != nil || audioURL == nil {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	base, _ := url.Parse(r.config.BaseURL)
	loopbackFixture := base != nil && (base.Hostname() == "127.0.0.1" || base.Hostname() == "localhost") && audioURL.Scheme == "http" && audioURL.Host == base.Host
	if audioURL.User != nil || audioURL.Fragment != "" || (audioURL.Scheme != "https" && !loopbackFixture) {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	if !loopbackFixture && audioURL.Host != base.Host && audioURL.Host != "filecdn.minimax.chat" {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	content, err := r.download(ctx, audioURL.String())
	if err != nil {
		return ProviderOutput{}, err
	}
	if !looksLikeMP3(content) {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	return ProviderOutput{Kind: "audio", MIMEType: "audio/mpeg", Extension: ".mp3", Content: content}, nil
}

type musicResponse struct {
	TraceID  string `json:"trace_id"`
	BaseResp struct {
		StatusCode int `json:"status_code"`
	} `json:"base_resp"`
	Data struct {
		Audio string `json:"audio"`
	} `json:"data"`
}

func (r *MusicRuntime) post(ctx context.Context, path string, payload any) (musicResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return musicResponse{}, NewProviderFailure("provider_invalid_request", 0)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.config.BaseURL+path, strings.NewReader(string(body)))
	if err != nil {
		return musicResponse{}, NewProviderFailure("provider_invalid_request", 0)
	}
	req.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return musicResponse{}, NewProviderFailure("provider_timeout", 0)
		}
		return musicResponse{}, NewProviderFailure("provider_request_failed", 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return musicResponse{}, classifyHTTPStatus(resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	data, tooLarge, err := readProviderBody(resp.Body, 2*1024*1024)
	if err != nil || tooLarge {
		return musicResponse{}, NewProviderFailure("provider_response_invalid", 0)
	}
	var value musicResponse
	if json.Unmarshal(data, &value) != nil {
		return musicResponse{}, NewProviderFailure("provider_response_invalid", 0)
	}
	return value, nil
}

func (r *MusicRuntime) download(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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
	content, tooLarge, err := readProviderBody(resp.Body, maxMusicProviderBytes)
	if err != nil || tooLarge || len(content) == 0 {
		return nil, NewProviderFailure("provider_response_invalid", 0)
	}
	return content, nil
}

func looksLikeMP3(content []byte) bool {
	if len(content) >= 3 && string(content[:3]) == "ID3" {
		return true
	}
	if len(content) < 2 {
		return false
	}
	return content[0] == 0xff && (content[1]&0xe0) == 0xe0
}
