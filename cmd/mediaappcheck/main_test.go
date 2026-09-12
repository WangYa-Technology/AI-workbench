package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
)

func TestIsolatedSchema(t *testing.T) {
	valid := "hcai_media_acceptance_20260818_ab12"
	if !isolatedSchema("postgres://localhost/hcai?sslmode=require&search_path="+valid, valid) {
		t.Fatal("expected isolated media acceptance schema to pass")
	}
	for _, test := range []struct {
		url    string
		schema string
	}{
		{"postgres://localhost/hcai?search_path=public", "public"},
		{"postgres://localhost/hcai?search_path=" + valid, "different"},
		{"postgres://localhost/hcai", valid},
		{"not a database URL", valid},
	} {
		if isolatedSchema(test.url, test.schema) {
			t.Fatalf("unsafe schema accepted: url=%q schema=%q", test.url, test.schema)
		}
	}
}

func TestRunAcceptanceApplicationContract(t *testing.T) {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	rootPool, err := database.Open(ctx, baseURL)
	if err != nil {
		testutil.ExternalDatabaseUnavailable(t, err)
	}
	defer rootPool.Close()
	schema := "media_app_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = rootPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") }()
	separator := "?"
	if strings.Contains(baseURL, "?") {
		separator = "&"
	}
	pool, err := database.Open(ctx, baseURL+separator+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Environment: "test", DatabaseURL: baseURL, WebOrigin: "https://acceptance.example.test",
		MediaRoot: t.TempDir(), MediaStorageAdapter: "local_file", MediaScannerAdapter: "local_deterministic",
		LocalProviderEnabled: true, EmailDeliveryMode: "disabled",
	}
	result, err := runAcceptance(ctx, cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "passed" || result.StorageAdapter != "local_file" || result.ScannerAdapter != "local_deterministic" ||
		!result.PendingPrivate || !result.CrossAccountPrivate || !result.FullReadVerified || !result.RangeReadVerified ||
		!result.DurableJobVerified || !result.AuditVerified || !result.NotificationVerified || !result.Deleted {
		t.Fatalf("unexpected application acceptance result: %#v", result)
	}
}
