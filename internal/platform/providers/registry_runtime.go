package providers

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"strings"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegistryRuntime resolves the active Provider/model pair from the admin
// registry immediately before a generation. Credentials remain in this
// process and are never copied into generation records or API responses.
type RegistryRuntime struct {
	pool *pgxpool.Pool
	key  []byte
}

func NewRegistryRuntime(pool *pgxpool.Pool, key []byte) *RegistryRuntime {
	if pool == nil || len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil
	}
	return &RegistryRuntime{pool: pool, key: append([]byte(nil), key...)}
}

func (r *RegistryRuntime) Provider() string { return "openai" }

// Supports is deliberately permissive because the registry is mutable after
// the HTTP server starts. Generate performs the authoritative DB lookup.
func (r *RegistryRuntime) Supports(mode, modelName string) bool {
	return r != nil && (mode == "chat" || mode == "image") && strings.TrimSpace(modelName) != ""
}

func (r *RegistryRuntime) Generate(ctx context.Context, request creation.ProviderRequest) (creation.ProviderOutput, error) {
	var endpoint, protocol string
	var nonce, ciphertext []byte
	if err := r.pool.QueryRow(ctx, `
		SELECT c.endpoint,c.protocol,c.credential_nonce,c.credential_ciphertext
		FROM provider_configs c
		JOIN provider_config_models m ON m.provider_id=c.id
		WHERE c.runtime_provider='openai' AND c.archived_at IS NULL AND c.admin_enabled=true
		  AND m.mode=$1 AND m.model_name=$2 AND m.archived_at IS NULL AND m.admin_enabled=true
		ORDER BY c.updated_at DESC, c.id
		LIMIT 1`, request.Mode, request.ModelName).Scan(&endpoint, &protocol, &nonce, &ciphertext); errors.Is(err, pgx.ErrNoRows) {
		return creation.ProviderOutput{}, creation.NewProviderFailure("provider_unavailable", 0)
	} else if err != nil {
		return creation.ProviderOutput{}, creation.NewProviderFailure("provider_request_failed", 0)
	}
	secret, err := decryptProviderSecret(r.key, nonce, ciphertext)
	if err != nil || secret == "" {
		return creation.ProviderOutput{}, creation.NewProviderFailure("provider_authentication", 0)
	}
	imageAsync := protocol == "hctopup_async_image"
	chatAPI := "responses"
	if protocol == "openai_chat_completions" {
		chatAPI = "chat_completions"
	}
	runtime := creation.NewOpenAIRuntime(creation.OpenAIRuntimeConfig{
		APIKey: secret, ChatAPIKey: secret, ImageAPIKey: secret, BaseURL: endpoint,
		ChatAPI: chatAPI, ChatModel: request.ModelName, ImageModel: request.ModelName,
		ImageAsync: imageAsync, ImagePollInterval: 10 * time.Second, ImageTimeout: 5 * time.Minute,
		ChatMaxOutputTokens: 2048, ImageSize: "1024x1024", ImageQuality: "auto",
	})
	return runtime.Generate(ctx, request)
}

func decryptProviderSecret(key, nonce, ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return "", errors.New("invalid provider credential")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(plain)), nil
}
