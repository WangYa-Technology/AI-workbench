package admin_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
)

type configuredRuntimes map[string]bool

func (r configuredRuntimes) Available(provider, mode, modelName string) bool {
	return r[provider+"/"+mode+"/"+modelName]
}

func TestConfiguredExternalRuntimeCanBeEnabledAndRouted(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	actorID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Provider Admin','admin','active')`, actorID, actorID.String()+"@test.local", "provider_admin_"+actorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	runtimes := configuredRuntimes{"openai/image/gpt-image-2": true}
	service := admin.NewServiceWithRuntimes(pool, runtimes)

	provider, err := service.UpdateProvider(ctx, actorID, "openai-image", admin.ProviderUpdate{
		Enabled: true}, "provider-enable")
	if err != nil {
		t.Fatal(err)
	}
	if !provider.AdminEnabled || !provider.RuntimeAvailable || !provider.EffectiveEnabled || provider.LocalTest {
		t.Fatalf("external Provider readiness projection mismatch: %#v", provider)
	}

	policy, err := service.GetModelRoutePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateModelRoute(ctx, actorID, "image", admin.ModelRouteUpdate{
		ProviderProfileID: "openai-image",
		Name:              "Verified staging image route",
		TimeoutSeconds:    90,
		MaxAttempts:       2,
		ExpectedVersion:   policy.Routes["image"].Version,
	}, "provider-route")
	if err != nil {
		t.Fatal(err)
	}
	route := updated.Routes["image"]
	if route.ProviderProfileID != "openai-image" || !route.ProviderRuntimeReady || route.LocalTest {
		t.Fatalf("external Provider route projection mismatch: %#v", route)
	}
}
