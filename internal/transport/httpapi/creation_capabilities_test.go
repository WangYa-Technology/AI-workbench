package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestCreationCapabilitiesArePublicAndReflectLocalRoutes(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	var capabilities creation.CreationCapabilities
	response := requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/creation/capabilities", nil, &capabilities)
	if response.StatusCode != http.StatusOK || len(capabilities.Items) != 4 {
		t.Fatalf("unexpected creation capability response: status=%d body=%#v", response.StatusCode, capabilities)
	}
	byMode := make(map[string]creation.CreationCapability, len(capabilities.Items))
	for _, item := range capabilities.Items {
		byMode[item.Mode] = item
	}
	if !byMode["video"].Available || len(byMode["video"].Durations) != 3 || byMode["video"].ReferenceKinds[0] != "image" {
		t.Fatalf("Local Test Video capability projection mismatch: %#v", byMode["video"])
	}
	if !byMode["image"].Available || !byMode["image"].SupportsMask {
		t.Fatalf("Local Test Image capability projection mismatch: %#v", byMode["image"])
	}
	if byMode["music"].ReferenceKinds == nil || byMode["chat"].AspectRatios == nil || byMode["chat"].Qualities == nil {
		t.Fatalf("empty capability collections must be encoded as arrays: %#v", byMode)
	}
	if len(byMode["music"].ResultFormats) != 1 || byMode["music"].ResultFormats[0] != "wav" {
		t.Fatalf("Local Test Music result format mismatch: %#v", byMode["music"])
	}
}

func TestCreationCapabilitiesReflectConfiguredMiniMaxRouteWithoutCallingIt(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO provider_profiles(id,mode,provider,model_name,display_name,description,estimated_cost_cents,currency,local_test,admin_enabled)
		VALUES('capability-minimax','music','minimax_music','music-capability','Capability Music','HTTP capability projection fixture.',8,'USD',false,true)`); err != nil {
		t.Fatal(err)
	}
	var activeID uuid.UUID
	var version int
	if err := pool.QueryRow(ctx, `SELECT active_revision_id,version FROM model_route_state WHERE mode='music'`).Scan(&activeID, &version); err != nil {
		t.Fatal(err)
	}
	revisionID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO model_route_revisions(id,mode,version,parent_revision_id,provider_profile_id,name,timeout_seconds,max_attempts,reason)
		VALUES($1,'music',$2,$3,'capability-minimax','Capability Music route',60,2,'Verify non-secret active capability projection.')`, revisionID, version+1, activeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE model_route_state SET active_revision_id=$1,version=$2 WHERE mode='music'`, revisionID, version+1); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
		MusicEnabled: true, MusicPaidCallsApproved: true, MusicAPIKey: "fixture-only", MusicBaseURL: "http://127.0.0.1:9000/v1", MusicModel: "music-capability",
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	var capabilities creation.CreationCapabilities
	response := requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/creation/capabilities", nil, &capabilities)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("capability request failed: %d", response.StatusCode)
	}
	var music creation.CreationCapability
	for _, item := range capabilities.Items {
		if item.Mode == "music" {
			music = item
		}
	}
	if !music.Available || music.Provider != "minimax_music" || music.ModelName != "music-capability" || len(music.ResultFormats) != 1 || music.ResultFormats[0] != "mp3" || len(music.ReferenceKinds) != 0 {
		t.Fatalf("MiniMax capability projection mismatch: %#v", music)
	}
}
