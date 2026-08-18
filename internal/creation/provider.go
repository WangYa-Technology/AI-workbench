package creation

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var ErrRuntimeUnavailable = providerFailure{code: "provider_unavailable", retryable: true}

type providerFailure struct {
	code       string
	retryAfter time.Duration
	retryable  bool
}

func (e providerFailure) Error() string     { return e.code }
func (e providerFailure) ErrorCode() string { return e.code }
func (e providerFailure) RetryDelay() time.Duration {
	return e.retryAfter
}
func (e providerFailure) Retryable() bool { return e.retryable }

// NewProviderFailure lets adapters expose a stable failure class without
// leaking an upstream response, request identifier, or credential material.
func NewProviderFailure(code string, retryAfter time.Duration) error {
	retryableCodes := map[string]bool{
		"provider_authentication":   false,
		"provider_content_rejected": false,
		"provider_invalid_request":  false,
		"provider_rate_limited":     true,
		"provider_request_failed":   true,
		"provider_response_invalid": false,
		"provider_timeout":          true,
		"provider_unavailable":      true,
	}
	retryable, allowed := retryableCodes[code]
	if !allowed {
		code = "provider_request_failed"
		retryable = true
	}
	if retryAfter < 0 || retryAfter > 24*time.Hour {
		retryAfter = 0
	}
	return providerFailure{code: code, retryAfter: retryAfter, retryable: retryable}
}

// ProviderRequest is the stable boundary between durable generation jobs and
// an external or local model runtime. Provider credentials stay inside the
// runtime implementation and are never persisted with the request.
type ProviderRequest struct {
	GenerationID      uuid.UUID
	Mode              string
	Provider          string
	ModelName         string
	Prompt            string
	Parameters        GenerationParameters
	Messages          []ProviderMessage
	ReferenceAssetIDs []uuid.UUID
	ReferenceAssets   []ProviderAsset
	MaskAssetID       *uuid.UUID
}

type ProviderAsset struct {
	ID       uuid.UUID
	MIMEType string
	Content  []byte
}

type ProviderMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ProviderOutput struct {
	Kind      string
	MIMEType  string
	Extension string
	Width     *int
	Height    *int
	Text      *string
	Content   []byte
	Usage     *ProviderUsage
}

// ProviderUsage is a minimized metering fact returned with a successful
// Provider response. It intentionally excludes upstream request IDs, bodies,
// credentials, organization identifiers, and prompt copies.
type ProviderUsage struct {
	InputTokens       int
	CachedInputTokens int
	OutputTokens      int
	ReasoningTokens   int
	TotalTokens       int
}

type ProviderRuntime interface {
	Provider() string
	Supports(mode, modelName string) bool
	Generate(context.Context, ProviderRequest) (ProviderOutput, error)
}

// RuntimeAvailability is intentionally small so the API and Admin surfaces can
// validate routes without receiving Provider credentials or calling a model.
type RuntimeAvailability interface {
	Available(provider, mode, modelName string) bool
}

type RuntimeCatalog struct {
	runtimes map[string]ProviderRuntime
}

func NewRuntimeCatalog(runtimes ...ProviderRuntime) *RuntimeCatalog {
	catalog := &RuntimeCatalog{runtimes: make(map[string]ProviderRuntime, len(runtimes))}
	for _, runtime := range runtimes {
		if runtime == nil {
			continue
		}
		provider := strings.TrimSpace(strings.ToLower(runtime.Provider()))
		if provider != "" {
			catalog.runtimes[provider] = runtime
		}
	}
	return catalog
}

func (c *RuntimeCatalog) Available(provider, mode, modelName string) bool {
	if c == nil {
		return false
	}
	runtime, ok := c.runtimes[strings.TrimSpace(strings.ToLower(provider))]
	return ok && runtime.Supports(strings.TrimSpace(strings.ToLower(mode)), strings.TrimSpace(modelName))
}

func (c *RuntimeCatalog) Generate(ctx context.Context, request ProviderRequest) (ProviderOutput, error) {
	if !c.Available(request.Provider, request.Mode, request.ModelName) {
		return ProviderOutput{}, ErrRuntimeUnavailable
	}
	runtime := c.runtimes[strings.TrimSpace(strings.ToLower(request.Provider))]
	output, err := runtime.Generate(ctx, request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ProviderOutput{}, NewProviderFailure("provider_timeout", 0)
		}
		var classified interface{ ErrorCode() string }
		if errors.As(err, &classified) {
			var delayed interface{ RetryDelay() time.Duration }
			if errors.As(err, &delayed) {
				return ProviderOutput{}, NewProviderFailure(classified.ErrorCode(), delayed.RetryDelay())
			}
			return ProviderOutput{}, NewProviderFailure(classified.ErrorCode(), 0)
		}
		return ProviderOutput{}, NewProviderFailure("provider_request_failed", 0)
	}
	if err := validateProviderOutput(request.Mode, output); err != nil {
		return ProviderOutput{}, NewProviderFailure("provider_response_invalid", 0)
	}
	return output, nil
}

func validateProviderOutput(mode string, output ProviderOutput) error {
	if len(output.Content) == 0 || len(output.Content) > 100*1024*1024 {
		return errors.New("output content size is outside the supported range")
	}
	type mediaContract struct {
		kind      string
		mimeType  string
		extension string
	}
	allowed := map[string][]mediaContract{
		"chat":  {{"document", "text/plain; charset=utf-8", ".txt"}},
		"image": {{"image", "image/jpeg", ".jpg"}, {"image", "image/png", ".png"}},
		"video": {{"video", "video/mp4", ".mp4"}},
		"music": {{"audio", "audio/wav", ".wav"}, {"audio", "audio/mpeg", ".mp3"}},
	}
	validContract := false
	for _, expected := range allowed[mode] {
		if output.Kind == expected.kind && output.MIMEType == expected.mimeType && output.Extension == expected.extension {
			validContract = true
			break
		}
	}
	if !validContract {
		return errors.New("output media contract does not match the generation mode")
	}
	if mode == "chat" && (output.Text == nil || strings.TrimSpace(*output.Text) == "") {
		return errors.New("chat output text is required")
	}
	if mode == "chat" && (!utf8.Valid(output.Content) || string(output.Content) != *output.Text) {
		return errors.New("chat output text and content must match")
	}
	if mode != "chat" && output.Text != nil {
		return errors.New("non-chat output cannot contain text")
	}
	if (mode == "image" || mode == "video") && (output.Width == nil || output.Height == nil || *output.Width < 1 || *output.Height < 1) {
		return errors.New("visual output dimensions are required")
	}
	if output.Usage != nil {
		usage := output.Usage
		if usage.InputTokens < 0 || usage.InputTokens > 2000000 || usage.CachedInputTokens < 0 || usage.CachedInputTokens > usage.InputTokens ||
			usage.OutputTokens < 0 || usage.OutputTokens > 2000000 || usage.ReasoningTokens < 0 || usage.ReasoningTokens > usage.OutputTokens ||
			usage.TotalTokens < usage.InputTokens+usage.OutputTokens || usage.TotalTokens > 4000000 {
			return errors.New("provider usage evidence is outside the supported range")
		}
	}
	return nil
}
