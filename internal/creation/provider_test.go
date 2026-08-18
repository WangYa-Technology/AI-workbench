package creation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/creation"
)

type contractRuntime struct {
	provider string
	mode     string
	model    string
	output   creation.ProviderOutput
	err      error
	requests []creation.ProviderRequest
}

func (r *contractRuntime) Provider() string { return r.provider }

func (r *contractRuntime) Supports(mode, modelName string) bool {
	return mode == r.mode && modelName == r.model
}

func (r *contractRuntime) Generate(_ context.Context, request creation.ProviderRequest) (creation.ProviderOutput, error) {
	r.requests = append(r.requests, request)
	return r.output, r.err
}

func TestRuntimeCatalogRoutesExactCapability(t *testing.T) {
	text := "Provider contract response"
	runtime := &contractRuntime{
		provider: "contract", mode: "chat", model: "contract-chat-v1",
		output: creation.ProviderOutput{
			Kind: "document", MIMEType: "text/plain; charset=utf-8", Extension: ".txt",
			Text: &text, Content: []byte(text),
		},
	}
	catalog := creation.NewRuntimeCatalog(runtime)
	if !catalog.Available("contract", "chat", "contract-chat-v1") {
		t.Fatal("expected exact Provider capability to be available")
	}
	if catalog.Available("contract", "image", "contract-chat-v1") || catalog.Available("contract", "chat", "other-model") {
		t.Fatal("runtime catalog accepted an unsupported mode or model")
	}
	generationID := uuid.New()
	output, err := catalog.Generate(context.Background(), creation.ProviderRequest{
		GenerationID: generationID, Provider: "contract", Mode: "chat", ModelName: "contract-chat-v1", Prompt: "Build a launch brief",
	})
	if err != nil || output.Text == nil || *output.Text != text {
		t.Fatalf("route Provider request: output=%#v err=%v", output, err)
	}
	if len(runtime.requests) != 1 || runtime.requests[0].GenerationID != generationID || runtime.requests[0].Prompt != "Build a launch brief" {
		t.Fatalf("runtime did not receive exact generation evidence: %#v", runtime.requests)
	}
}

func TestRuntimeCatalogRejectsUnavailableAndInvalidOutput(t *testing.T) {
	catalog := creation.NewRuntimeCatalog()
	_, err := catalog.Generate(context.Background(), creation.ProviderRequest{Provider: "missing", Mode: "chat", ModelName: "missing-v1"})
	if !errors.Is(err, creation.ErrRuntimeUnavailable) {
		t.Fatalf("expected unavailable runtime error, got %v", err)
	}
	var retryable interface{ Retryable() bool }
	if !errors.As(err, &retryable) || !retryable.Retryable() {
		t.Fatalf("unavailable runtime must remain retryable: %v", err)
	}

	runtime := &contractRuntime{
		provider: "contract", mode: "image", model: "contract-image-v1",
		output: creation.ProviderOutput{Kind: "image", MIMEType: "image/jpeg", Extension: ".png", Width: intPointer(32), Height: intPointer(32), Content: []byte("mismatched-contract")},
	}
	catalog = creation.NewRuntimeCatalog(runtime)
	if _, err := catalog.Generate(context.Background(), creation.ProviderRequest{Provider: "contract", Mode: "image", ModelName: "contract-image-v1"}); err == nil {
		t.Fatal("expected mismatched Provider output contract to fail closed")
	}
}

func TestRuntimeCatalogSanitizesProviderFailures(t *testing.T) {
	runtime := &contractRuntime{
		provider: "contract", mode: "chat", model: "contract-chat-v1",
		err: errors.New("upstream response containing private request details"),
	}
	catalog := creation.NewRuntimeCatalog(runtime)
	_, err := catalog.Generate(context.Background(), creation.ProviderRequest{Provider: "contract", Mode: "chat", ModelName: "contract-chat-v1"})
	if err == nil || err.Error() != "provider_request_failed" {
		t.Fatalf("unclassified Provider failure was not sanitized: %v", err)
	}

	runtime.err = creation.NewProviderFailure("provider_rate_limited", 15*time.Second)
	_, err = catalog.Generate(context.Background(), creation.ProviderRequest{Provider: "contract", Mode: "chat", ModelName: "contract-chat-v1"})
	var coded interface{ ErrorCode() string }
	var delayed interface{ RetryDelay() time.Duration }
	if !errors.As(err, &coded) || coded.ErrorCode() != "provider_rate_limited" || !errors.As(err, &delayed) || delayed.RetryDelay() != 15*time.Second {
		t.Fatalf("classified Provider failure lost its safe retry evidence: %v", err)
	}
	var retryable interface{ Retryable() bool }
	if !errors.As(err, &retryable) || !retryable.Retryable() {
		t.Fatalf("rate limit failure lost retry classification: %v", err)
	}

	runtime.err = creation.NewProviderFailure("provider_authentication", time.Minute)
	_, err = catalog.Generate(context.Background(), creation.ProviderRequest{Provider: "contract", Mode: "chat", ModelName: "contract-chat-v1"})
	if !errors.As(err, &retryable) || retryable.Retryable() {
		t.Fatalf("authentication failure must be terminal: %v", err)
	}
	if !errors.As(err, &delayed) || delayed.RetryDelay() != time.Minute {
		t.Fatalf("classified terminal failure lost bounded delay evidence: %v", err)
	}
}

func intPointer(value int) *int { return &value }
